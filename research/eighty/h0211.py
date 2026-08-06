import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def parse_period(period_str):
    return datetime.strptime(period_str, '%Y-%m-%d').date()

def add_days(d, days):
    return d + timedelta(days=days)

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Find symbols present in both daily bars and inst_holdings
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        JOIN inst_holdings ih ON ih.symbol_id = s.id
        WHERE s.active = 1
    """)
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = [s[0] for s in symbols]
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Load daily bars for these symbols (tf='1d'), sorted by symbol_id, ts
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'date': unix_to_date(row['ts']),
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })

    # 3. Load and aggregate 13F holdings per symbol per period
    cur.execute(f"""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id IN ({placeholders})
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """, symbol_ids)
    
    holdings_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        period_date = parse_period(row['period'])
        disclosure_date = add_days(period_date, 45)
        holdings_by_symbol[row['symbol_id']].append({
            'period': period_date,
            'disclosure_date': disclosure_date,
            'total_shares': row['total_shares']
        })

    # 4. Load prediction_outcomes for labels (fwd_return for horizon=20)
    # We need to map from decision timestamp T to outcome at T+20 trading days
    # prediction_outcomes has: symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch
    cur.execute(f"""
        SELECT symbol_id, ts, fwd_return, up
        FROM prediction_outcomes
        WHERE horizon = 20 AND symbol_id IN ({placeholders}) AND fwd_return IS NOT NULL
    """, symbol_ids)
    
    outcomes_by_symbol = defaultdict(dict)
    for row in cur.fetchall():
        outcomes_by_symbol[row['symbol_id']][row['ts']] = {
            'fwd_return': row['fwd_return'],
            'up': row['up']
        }

    # 5. Process each symbol
    all_decisions = []  # (decision_ts, symbol_id, symbol, issued, hit, abstain_reason)
    
    for symbol_id, symbol in symbols:
        bars = bars_by_symbol.get(symbol_id, [])
        holdings = holdings_by_symbol.get(symbol_id, [])
        outcomes = outcomes_by_symbol.get(symbol_id, {})
        
        if len(bars) < 252 + 20:  # Need 252 prior + horizon
            continue
        if len(holdings) < 2:
            continue
        
        # Precompute rolling stats for bars
        n = len(bars)
        closes = [b['close'] for b in bars]
        volumes = [b['volume'] for b in bars]
        highs = [b['high'] for b in bars]
        
        # 252-session high (rolling max of high over 252 sessions)
        high_252 = [0] * n
        for i in range(n):
            if i >= 251:
                high_252[i] = max(highs[i-251:i+1])
            else:
                high_252[i] = max(highs[:i+1])
        
        # 20-session median volume
        vol_median_20 = [0] * n
        for i in range(n):
            if i >= 19:
                vol_median_20[i] = sorted(volumes[i-19:i+1])[10]
            else:
                vol_median_20[i] = sorted(volumes[:i+1])[len(volumes[:i+1])//2]
        
        # 20-session gain (close[i] / close[i-20] - 1)
        gain_20 = [0] * n
        for i in range(n):
            if i >= 20:
                gain_20[i] = closes[i] / closes[i-20] - 1
            else:
                gain_20[i] = 0
        
        # 20-session realized volatility (std of daily returns)
        vol_20 = [0] * n
        for i in range(n):
            if i >= 20:
                rets = [(closes[j] / closes[j-1] - 1) for j in range(i-19, i+1)]
                mean_ret = sum(rets) / len(rets)
                var = sum((r - mean_ret)**2 for r in rets) / len(rets)
                vol_20[i] = math.sqrt(var)
            else:
                vol_20[i] = 0
        
        # 60-session average daily dollar volume
        dollar_vol_60 = [0] * n
        for i in range(n):
            if i >= 59:
                dollar_vol_60[i] = sum(closes[j] * volumes[j] for j in range(i-59, i+1)) / 60
            else:
                dollar_vol_60[i] = sum(closes[j] * volumes[j] for j in range(i+1)) / (i+1)
        
        # Track last call day for this symbol (for 20-day cooldown)
        last_call_idx = -100
        
        # For cross-sectional volatility decile, we need to compute per day across symbols
        # We'll collect volatility data first, then compute deciles
        # But we need to do this per trading day across all symbols
        # Let's collect all decision points first, then filter by volatility decile
        
        # Find valid decision points (trading days with enough history)
        for i in range(252, n - 20):  # Need 252 prior, and 20 forward for outcome
            bar = bars[i]
            ts = bar['ts']
            date = bar['date']
            close = bar['close']
            volume = bar['volume']
            
            # Price filter
            if close < 5:
                continue
            
            # 252 prior sessions check (already satisfied by loop start)
            # Average daily dollar volume >= $5M over prior 60 sessions
            if dollar_vol_60[i] < 5_000_000:
                continue
            
            # Trailing 20-session gain > 30%
            if gain_20[i] > 0.30:
                continue
            
            # Cooldown: no call in prior 20 trading days
            if i - last_call_idx < 20:
                continue
            
            # Close-to-close return between -1% and +3%
            prev_close = closes[i-1]
            c2c_return = close / prev_close - 1
            if c2c_return < -0.01 or c2c_return > 0.03:
                continue
            
            # Volume not above 1.5x 20-session median
            if volume > 1.5 * vol_median_20[i]:
                continue
            
            # Close at least 10% below 252-session high
            if close > 0.9 * high_252[i]:
                continue
            
            # 13F condition: latest holdings with disclosure_date <= date
            # Find the latest holdings record where disclosure_date <= date
            valid_holdings = [h for h in holdings if h['disclosure_date'] <= date]
            if len(valid_holdings) < 2:
                continue
            
            latest = valid_holdings[-1]
            previous = valid_holdings[-2]
            
            # Aggregate shares held at least 10% above previous
            if latest['total_shares'] < 1.10 * previous['total_shares']:
                continue
            
            # All entry conditions met - check outcome
            outcome = outcomes.get(ts)
            if not outcome:
                continue
            
            hit = 1 if outcome['up'] == 1 else 0
            
            # Store decision point with volatility for later cross-sectional filtering
            all_decisions.append({
                'ts': ts,
                'date': date,
                'symbol_id': symbol_id,
                'symbol': symbol,
                'bar_idx': i,
                'vol_20': vol_20[i],
                'hit': hit,
                'issued': 1  # tentatively issued, may be abstained due to vol decile
            })
            last_call_idx = i
    
    if not all_decisions:
        print("INSUFFICIENT=1")
        return 0
    
    # 6. Cross-sectional volatility decile filtering
    # Group by date, compute decile threshold, filter out top decile
    decisions_by_date = defaultdict(list)
    for d in all_decisions:
        decisions_by_date[d['date']].append(d)
    
    filtered_decisions = []
    for date, decs in decisions_by_date.items():
        if len(decs) < 10:  # Need enough for decile
            filtered_decisions.extend(decs)
            continue
        vols = sorted([d['vol_20'] for d in decs])
        # Top decile = 90th percentile
        threshold_idx = int(len(vols) * 0.9)
        threshold = vols[threshold_idx]
        for d in decs:
            if d['vol_20'] <= threshold:
                filtered_decisions.append(d)
    
    if len(filtered_decisions) < 30:
        print("INSUFFICIENT=1")
        return 0
    
    # 7. Split into sealed era (most recent 20%) and rest
    filtered_decisions.sort(key=lambda x: x['ts'])
    n_total = len(filtered_decisions)
    split_idx = int(n_total * 0.8)
    main_decisions = filtered_decisions[:split_idx]
    sealed_decisions = filtered_decisions[split_idx:]
    
    def compute_metrics(decisions):
        issued = [d for d in decisions if d['issued'] == 1]
        if not issued:
            return {
                'issued': 0,
                'opportunities': len(decisions),
                'precision': 0,
                'base_rate': 0,
                'distinct_days': 0,
                'effective_n': 0
            }
        
        hits = sum(d['hit'] for d in issued)
        precision = hits / len(issued)
        base_rate = hits / len(issued)  # Base rate of predicted class (UP) within issued subset
        distinct_days = len(set(d['date'] for d in issued))
        
        # Design effect: 1 + (avg_cluster_size - 1) * ICC
        # Simplified: cluster by date, assume ICC > 0
        # Effective N = issued / design_effect
        # Design effect > 1 always, so effective_n < issued
        clusters = defaultdict(int)
        for d in issued:
            clusters[d['date']] += 1
        avg_cluster = sum(clusters.values()) / len(clusters) if clusters else 1
        # Conservative ICC estimate of 0.1 for financial returns
        icc = 0.1
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = len(issued) / design_effect
        
        return {
            'issued': len(issued),
            'opportunities': len(decisions),
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }
    
    main_metrics = compute_metrics(main_decisions)
    sealed_metrics = compute_metrics(sealed_decisions)
    
    # 8. Print results
    print(f"ISSUED={main_metrics['issued']}")
    print(f"OPPORTUNITIES={main_metrics['opportunities']}")
    print(f"PRECISION={main_metrics['precision']:.6f}")
    print(f"BASE_RATE={main_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={main_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={main_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())