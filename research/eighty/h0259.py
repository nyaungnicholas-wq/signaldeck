import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import statistics

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load symbols in news
    cur.execute("SELECT DISTINCT symbol_id FROM news")
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return 0

    placeholders = ','.join('?' * len(symbol_ids))

    # Load daily sentiment aggregates from news
    cur.execute(f"""
        SELECT symbol_id, date(ts, 'unixepoch') as day, AVG(score) as avg_score
        FROM news
        WHERE symbol_id IN ({placeholders})
        GROUP BY symbol_id, day
        ORDER BY symbol_id, day
    """, symbol_ids)
    
    sentiment_by_symbol = defaultdict(dict)
    for row in cur.fetchall():
        sentiment_by_symbol[row['symbol_id']][row['day']] = row['avg_score']

    # Load daily bars for these symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        day_str = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'day': day_str,
            'close': row['close'],
            'volume': row['volume']
        })

    # Build aligned trading day series per symbol
    all_opportunities = []  # (ts, symbol_id, day_str, data_dict)
    
    for sym_id in symbol_ids:
        bars = bars_by_symbol.get(sym_id, [])
        if len(bars) < 253:  # need 252 prior + current
            continue
        
        sentiment_map = sentiment_by_symbol.get(sym_id, {})
        
        # Build series with sentiment
        series = []
        for i, bar in enumerate(bars):
            day = bar['day']
            sent = sentiment_map.get(day)
            series.append({
                'ts': bar['ts'],
                'day': day,
                'close': bar['close'],
                'volume': bar['volume'],
                'sentiment': sent,
                'index': i
            })
        
        # Compute rolling metrics
        n = len(series)
        for i in range(252, n):  # start from 253rd trading day (0-indexed 252)
            T = series[i]
            if T['sentiment'] is None:
                continue
            if T['close'] < 5:
                continue
            
            # 252 prior trading days for sentiment distribution (indices i-252 to i-1)
            prior_sentiments = [series[j]['sentiment'] for j in range(i-252, i) if series[j]['sentiment'] is not None]
            if len(prior_sentiments) < 200:  # require most days to have sentiment
                continue
            
            # Sentiment percentile
            pct = sum(1 for s in prior_sentiments if s <= T['sentiment']) / len(prior_sentiments)
            if pct > 0.20:  # not in bottom 20%
                continue
            
            # Close-to-close return at T (from T-1 to T)
            ret_T = (T['close'] / series[i-1]['close']) - 1
            if ret_T > -0.01:
                continue
            
            # 60-day average dollar volume (T-60 to T-1)
            if i < 60:
                continue
            dollar_vols = [series[j]['close'] * series[j]['volume'] for j in range(i-60, i)]
            avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
            if avg_dollar_vol < 5_000_000:
                continue
            
            # 20-session realized volatility at T (using returns T-20 to T-1)
            if i < 20:
                continue
            rets_20 = [(series[j]['close'] / series[j-1]['close']) - 1 for j in range(i-19, i+1)]
            rv_20 = statistics.stdev(rets_20) * (252 ** 0.5) if len(rets_20) > 1 else 0
            
            all_opportunities.append({
                'ts': T['ts'],
                'day': T['day'],
                'symbol_id': sym_id,
                'close': T['close'],
                'ret_T': ret_T,
                'rv_20': rv_20,
                'sentiment': T['sentiment'],
                'index': i,
                'series': series
            })

    if not all_opportunities:
        print("INSUFFICIENT=1")
        return 0

    # Cross-sectional volatility decile per day
    rv_by_day = defaultdict(list)
    for opp in all_opportunities:
        rv_by_day[opp['ts']].append(opp['rv_20'])
    
    rv_threshold_by_day = {}
    for ts, rvs in rv_by_day.items():
        if len(rvs) >= 10:
            sorted_rvs = sorted(rvs)
            idx = int(0.9 * (len(sorted_rvs) - 1))
            rv_threshold_by_day[ts] = sorted_rvs[idx]
        else:
            rv_threshold_by_day[ts] = float('inf')

    # Evaluate entry/abstain conditions
    last_call_day = {}  # symbol_id -> last call ts
    issued_calls = []
    opportunities_considered = 0
    
    for opp in all_opportunities:
        opportunities_considered += 1
        ts = opp['ts']
        sym_id = opp['symbol_id']
        day = opp['day']
        i = opp['index']
        series = opp['series']
        
        # High volatility abstain
        if opp['rv_20'] >= rv_threshold_by_day.get(ts, float('inf')):
            continue
        
        # Prior call in last 20 trading days
        last_call = last_call_day.get(sym_id)
        if last_call is not None:
            # Find index of last call in series
            last_call_idx = None
            for idx, s in enumerate(series):
                if s['ts'] == last_call:
                    last_call_idx = idx
                    break
            if last_call_idx is not None and (i - last_call_idx) <= 20:
                continue
        
        # All conditions met - issue UP call
        issued_calls.append({
            'ts': ts,
            'day': day,
            'symbol_id': sym_id,
            'close_T': opp['close'],
            'index': i,
            'series': series
        })
        last_call_day[sym_id] = ts

    if len(issued_calls) < 30:
        print("INSUFFICIENT=1")
        return 0

    # Compute outcomes (T+5 close-to-close)
    hits = 0
    for call in issued_calls:
        i = call['index']
        series = call['series']
        if i + 5 < len(series):
            close_T5 = series[i + 5]['close']
            if close_T5 > call['close_T']:
                hits += 1

    # Split into sealed era (most recent 20% of opportunities by time)
    all_opportunities.sort(key=lambda x: x['ts'])
    issued_calls.sort(key=lambda x: x['ts'])
    
    split_idx = int(len(all_opportunities) * 0.8)
    sealed_ts_cutoff = all_opportunities[split_idx]['ts'] if split_idx < len(all_opportunities) else float('inf')
    
    sealed_calls = [c for c in issued_calls if c['ts'] >= sealed_ts_cutoff]
    sealed_hits = 0
    for call in sealed_calls:
        i = call['index']
        series = call['series']
        if i + 5 < len(series):
            close_T5 = series[i + 5]['close']
            if close_T5 > call['close_T']:
                sealed_hits += 1

    # Compute metrics
    ISSUED = len(issued_calls)
    OPPORTUNITIES = opportunities_considered
    PRECISION = hits / ISSUED if ISSUED > 0 else 0
    
    # Base rate: overall UP rate among all opportunities
    total_up = 0
    for opp in all_opportunities:
        i = opp['index']
        series = opp['series']
        if i + 5 < len(series):
            if series[i + 5]['close'] > series[i]['close']:
                total_up += 1
    BASE_RATE = total_up / OPPORTUNITIES if OPPORTUNITIES > 0 else 0
    
    # Distinct UTC days among issued calls
    distinct_days = len(set(c['day'] for c in issued_calls))
    DISTINCT_DAYS = distinct_days
    
    # Effective N
    if DISTINCT_DAYS < ISSUED:
        design_effect = ISSUED / DISTINCT_DAYS
    else:
        design_effect = 1.01  # minimum design effect > 1
    EFFECTIVE_N = ISSUED / design_effect
    
    SEALED_PRECISION = sealed_hits / len(sealed_calls) if sealed_calls else 0

    print(f"ISSUED={ISSUED}")
    print(f"OPPORTUNITIES={OPPORTUNITIES}")
    print(f"PRECISION={PRECISION:.6f}")
    print(f"BASE_RATE={BASE_RATE:.6f}")
    print(f"DISTINCT_DAYS={DISTINCT_DAYS}")
    print(f"EFFECTIVE_N={EFFECTIVE_N:.6f}")
    print(f"SEALED_PRECISION={SEALED_PRECISION:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())