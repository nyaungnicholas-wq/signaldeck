import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timezone
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def get_qualifying_symbols(conn):
    q = """
    SELECT s.id, s.symbol, COUNT(b.ts) as n_bars
    FROM symbols s
    JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
    JOIN stocktwits_sentiment st ON st.symbol_id = s.id
    GROUP BY s.id, s.symbol
    HAVING n_bars >= 400
    """
    cur = conn.execute(q)
    return [(row[0], row[1], row[2]) for row in cur.fetchall()]

def load_bars(conn, symbol_id):
    q = "SELECT ts, open, high, low, close, volume FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts"
    cur = conn.execute(q, (symbol_id,))
    rows = cur.fetchall()
    return [
        {'ts': r[0], 'open': r[1], 'high': r[2], 'low': r[3], 'close': r[4], 'volume': r[5],
         'date': datetime.fromtimestamp(r[0], tz=timezone.utc).strftime('%Y-%m-%d')}
        for r in rows
    ]

def load_stocktwits_daily(conn, symbol_id):
    q = "SELECT ts, bullish, bearish, untagged, total FROM stocktwits_sentiment WHERE symbol_id = ? ORDER BY ts"
    cur = conn.execute(q, (symbol_id,))
    rows = cur.fetchall()
    daily = defaultdict(lambda: {'bullish': 0, 'bearish': 0, 'untagged': 0, 'total': 0})
    for r in rows:
        ts, bull, bear, untag, tot = r
        day = datetime.fromtimestamp(ts, tz=timezone.utc).strftime('%Y-%m-%d')
        daily[day]['bullish'] += bull
        daily[day]['bearish'] += bear
        daily[day]['untagged'] += untag
        daily[day]['total'] += tot
    out = []
    for day, vals in sorted(daily.items()):
        denom = vals['bullish'] + vals['bearish']
        if denom > 0:
            bear_ratio = vals['bearish'] / denom
            out.append((day, bear_ratio, vals['bullish'], vals['bearish'], vals['total']))
    return out

def compute_rsi(closes, period=14):
    if len(closes) < period + 1:
        return [None] * len(closes)
    gains = []
    losses = []
    for i in range(1, len(closes)):
        change = closes[i] - closes[i-1]
        gains.append(max(change, 0))
        losses.append(max(-change, 0))
    avg_gain = sum(gains[:period]) / period
    avg_loss = sum(losses[:period]) / period
    rsi = [None] * period
    if avg_loss == 0:
        rsi.append(100.0)
    else:
        rs = avg_gain / avg_loss
        rsi.append(100 - 100 / (1 + rs))
    for i in range(period, len(gains)):
        avg_gain = (avg_gain * (period - 1) + gains[i]) / period
        avg_loss = (avg_loss * (period - 1) + losses[i]) / period
        if avg_loss == 0:
            rsi.append(100.0)
        else:
            rs = avg_gain / avg_loss
            rsi.append(100 - 100 / (1 + rs))
    return rsi

def compute_returns(closes):
    rets = [None]
    for i in range(1, len(closes)):
        if closes[i-1] != 0:
            rets.append(closes[i] / closes[i-1] - 1)
        else:
            rets.append(None)
    return rets

def rolling_percentile_rank(values, window, current_idx):
    if current_idx < window:
        return None
    trailing = values[current_idx - window:current_idx]
    valid = [v for v in trailing if v is not None]
    if len(valid) < 10:
        return None
    current = values[current_idx]
    if current is None:
        return None
    rank = sum(1 for v in valid if v <= current) / len(valid)
    return rank

def rolling_std(values, window, current_idx):
    if current_idx < window:
        return None
    trailing = values[current_idx - window:current_idx]
    valid = [v for v in trailing if v is not None]
    if len(valid) < window:
        return None
    mean = sum(valid) / len(valid)
    var = sum((v - mean) ** 2 for v in valid) / len(valid)
    return math.sqrt(var)

def load_labels(conn):
    """Load prediction_outcomes as labels: (symbol_id, ts, up) for horizon=5."""
    q = "SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon = 5 AND up IS NOT NULL"
    cur = conn.execute(q)
    labels = {}
    for row in cur.fetchall():
        labels[(row[0], row[1])] = row[2]
    return labels

def process_symbol(symbol_id, symbol, bars, st_daily, labels):
    bar_by_date = {b['date']: b for b in bars}
    st_by_date = {d: (br, bull, bear, tot) for d, br, bull, bear, tot in st_daily}
    
    common_dates = sorted(set(bar_by_date.keys()) & set(st_by_date.keys()))
    if len(common_dates) < 252 + 126:
        return [], []
    
    dates = []
    closes = []
    volumes = []
    bear_ratios = []
    for d in common_dates:
        b = bar_by_date[d]
        br, bull, bear, tot = st_by_date[d]
        dates.append(d)
        closes.append(b['close'])
        volumes.append(b['volume'])
        bear_ratios.append(br)
    
    n = len(dates)
    if n < 252 + 126:
        return [], []
    
    rsi = compute_rsi(closes, 14)
    returns = compute_returns(closes)
    
    ret_20 = [None] * n
    for i in range(21, n):
        if closes[i-21] != 0:
            ret_20[i] = closes[i-1] / closes[i-21] - 1
    
    vol_20 = [None] * n
    for i in range(20, n):
        vol_20[i] = rolling_std(returns, 20, i)
    
    bear_pctl = [None] * n
    for i in range(126, n):
        bear_pctl[i] = rolling_percentile_rank(bear_ratios, 126, i)
    
    # Cross-sectional vol decile per day
    # We'll compute this after collecting all symbols' vol_20
    # For now, return raw data for later cross-sectional calc
    opportunities = []
    for i in range(max(252, 126), n):
        if closes[i] < 5:
            continue
        if bear_pctl[i] is None or rsi[i] is None or ret_20[i] is None or vol_20[i] is None:
            continue
        
        # Dollar volume check: avg daily $ volume over T-60..T-1 >= 5M
        if i < 60:
            continue
        dollar_vols = [closes[j] * volumes[j] for j in range(i-60, i) if closes[j] and volumes[j]]
        if len(dollar_vols) < 30:
            continue
        avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
        if avg_dollar_vol < 5_000_000:
            continue
        
        # Label lookup: need T+5 close-to-close return > 0
        # prediction_outcomes uses ts as unix epoch; our bars ts is also unix epoch
        bar_ts = bar_by_date[dates[i]]['ts']
        label_key = (symbol_id, bar_ts)
        up = labels.get(label_key)
        if up is None:
            continue
        
        opportunities.append({
            'symbol_id': symbol_id,
            'symbol': symbol,
            'date': dates[i],
            'ts': bar_ts,
            'close': closes[i],
            'bear_pctl': bear_pctl[i],
            'rsi': rsi[i],
            'ret_20': ret_20[i],
            'vol_20': vol_20[i],
            'up': up,
            'i': i,
            'dates': dates,
            'vol_20_series': vol_20,
        })
    return opportunities, []

def main():
    conn = connect_ro()
    labels = load_labels(conn)
    symbols = get_qualifying_symbols(conn)
    
    all_opportunities = []
    for symbol_id, symbol, n_bars in symbols:
        bars = load_bars(conn, symbol_id)
        st_daily = load_stocktwits_daily(conn, symbol_id)
        opps, _ = process_symbol(symbol_id, symbol, bars, st_daily, labels)
        all_opportunities.extend(opps)
    
    if not all_opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Cross-sectional vol decile per day
    by_date = defaultdict(list)
    for opp in all_opportunities:
        by_date[opp['date']].append(opp)
    
    for date, opps in by_date.items():
        vols = [o['vol_20'] for o in opps if o['vol_20'] is not None]
        if len(vols) >= 10:
            vols_sorted = sorted(vols)
            decile_90 = vols_sorted[int(len(vols_sorted) * 0.9)]
            for o in opps:
                o['vol_top_decile'] = o['vol_20'] is not None and o['vol_20'] >= decile_90
        else:
            for o in opps:
                o['vol_top_decile'] = False
    
    # Sort by date then symbol for deterministic processing
    all_opportunities.sort(key=lambda x: (x['date'], x['symbol']))
    
    # Apply entry conditions and abstentions
    issued = []
    last_call_date = {}  # symbol -> date index of last call
    
    for idx, opp in enumerate(all_opportunities):
        sym = opp['symbol']
        date = opp['date']
        
        # Entry conditions
        if opp['bear_pctl'] < 0.95:
            continue
        if opp['rsi'] > 30:
            continue
        if opp['ret_20'] > -0.08:
            continue
        if opp.get('vol_top_decile', False):
            continue
        
        # Prior call in last 20 trading days
        if sym in last_call_date:
            # Find index of last call date in opportunities
            # Since we process in order, we can track by index
            pass  # We'll handle this with a separate tracking
        
        # For now, track by symbol and check if any call in last 20 trading days
        # We need to know trading days, not calendar days. Use the opportunities list indices.
        # Simpler: track last call index per symbol
        pass
    
    # Re-process with proper prior call tracking
    last_call_idx = {}
    for idx, opp in enumerate(all_opportunities):
        sym = opp['symbol']
        
        if opp['bear_pctl'] < 0.95:
            continue
        if opp['rsi'] > 30:
            continue
        if opp['ret_20'] > -0.08:
            continue
        if opp.get('vol_top_decile', False):
            continue
        
        if sym in last_call_idx and idx - last_call_idx[sym] <= 20:
            continue
        
        # All conditions met - issue call
        issued.append(opp)
        last_call_idx[sym] = idx
    
    if len(issued) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Hold out most recent 20% as sealed era
    issued.sort(key=lambda x: x['date'])
    split_idx = int(len(issued) * 0.8)
    train_issued = issued[:split_idx]
    sealed_issued = issued[split_idx:]
    
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        n = len(calls)
        hits = sum(1 for c in calls if c['up'] == 1)
        precision = hits / n
        base_rate = sum(c['up'] for c in calls) / n
        distinct_days = len(set(c['date'] for c in calls))
        
        # Design effect: 1 + (avg_cluster_size - 1) * ICC
        # Estimate ICC from data: correlation of outcomes within same day
        by_day = defaultdict(list)
        for c in calls:
            by_day[c['date']].append(c['up'])
        
        if len(by_day) > 1:
            # Between-day variance vs within-day
            day_means = [sum(v)/len(v) for v in by_day.values()]
            overall_mean = sum(day_means) / len(day_means)
            between_var = sum((m - overall_mean)**2 for m in day_means) / len(day_means) if len(day_means) > 1 else 0
            within_var = sum(sum((v - m)**2 for v in vals) for m, vals in zip(day_means, by_day.values())) / sum(len(v) for v in by_day.values()) if sum(len(v) for v in by_day.values()) > 0 else 0
            total_var = between_var + within_var
            icc = between_var / total_var if total_var > 0 else 0
            avg_cluster = n / len(by_day)
            design_effect = 1 + (avg_cluster - 1) * icc
        else:
            design_effect = 1.0
        
        effective_n = n / design_effect if design_effect > 0 else n
        return n, hits, precision, base_rate, distinct_days, effective_n
    
    train_n, train_hits, train_prec, train_br, train_days, train_eff = compute_metrics(train_issued)
    sealed_n, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed_issued)
    
    # Overall metrics on all issued
    all_n, all_hits, all_prec, all_br, all_days, all_eff = compute_metrics(issued)
    all_opps = len(all_opportunities)
    
    print(f"ISSUED={all_n}")
    print(f"OPPORTUNITIES={all_opps}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_eff:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()