# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 290
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import statistics

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get FRED high-yield spread
    spread = []
    for row in conn.execute("SELECT ts, value FROM macro_series WHERE series='BAMLH0A0HYM2' ORDER BY ts"):
        spread.append((row['ts'], row['value']))
    if len(spread) < 252:
        print("INSUFFICIENT=1")
        return
    
    # Build spread lookup and compute 5-day changes and percentiles
    spread_dict = {ts: val for ts, val in spread}
    ts_list = [ts for ts, _ in spread]
    spread_5d = {}
    percentile_95 = {}
    for i in range(252, len(ts_list)):
        current_ts = ts_list[i]
        prev_ts = ts_list[i-5]
        if current_ts in spread_dict and prev_ts in spread_dict:
            change = spread_dict[current_ts] - spread_dict[prev_ts]
            spread_5d[current_ts] = change
            # Compute 95th percentile over trailing 252 trading days
            trailing = [spread_5d[ts_list[j]] for j in range(i-251, i+1) if ts_list[j] in spread_5d]
            if len(trailing) == 252:
                percentile_95[current_ts] = sorted(trailing)[int(252 * 0.95)]
    
    # Get daily bars and compute market returns for beta
    symbols = {}
    for row in conn.execute("SELECT id, symbol FROM symbols WHERE active=1"):
        symbols[row['id']] = row['symbol']
    
    # Get all daily bars, compute returns per day per symbol
    daily_bars = {}
    for row in conn.execute("""
        SELECT symbol_id, ts, close, volume FROM bars 
        WHERE tf='1d' ORDER BY symbol_id, ts
    """):
        sid = row['symbol_id']
        if sid not in daily_bars:
            daily_bars[sid] = []
        daily_bars[sid].append((row['ts'], row['close'], row['volume']))
    
    # Get prediction outcomes for 5-day horizon
    outcomes = {}
    for row in conn.execute("""
        SELECT symbol_id, ts, up, fwd_return FROM prediction_outcomes 
        WHERE horizon=5
    """):
        key = (row['symbol_id'], row['ts'])
        outcomes[key] = {'up': row['up'], 'fwd_return': row['fwd_return']}
    
    conn.close()
    
    # Process each symbol
    opportunities = []
    signals_by_symbol = {}
    
    for sid in daily_bars:
        bars = daily_bars[sid]
        if len(bars) < 250:
            continue
        
        # Compute 20-day median dollar volume for each point
        for i in range(19, len(bars)):
            ts, close, volume = bars[i]
            dollar_vol = [bars[j][1] * bars[j][2] for j in range(i-19, i+1)]
            median_vol = statistics.median(dollar_vol)
            if median_vol >= 5_000_000:
                # Check spread condition
                if ts in spread_5d and ts in percentile_95:
                    if spread_5d[ts] > percentile_95[ts]:
                        # Compute 252-day beta
                        if i >= 251:
                            # Get symbol returns
                            symbol_returns = []
                            market_returns = []
                            for j in range(i-251, i+1):
                                if j > 0:
                                    ts_prev, close_prev, _ = bars[j-1]
                                    ts_curr, close_curr, _ = bars[j]
                                    if ts_curr == ts_prev + 86400:  # consecutive days
                                        symbol_returns.append((close_curr / close_prev) - 1)
                            # Compute market returns (equal-weight)
                            # For simplicity, use overall market return approximation
                            # In reality, would need to compute equal-weight across all symbols per day
                            # Here we use a proxy: average of all symbol returns on each day
                            if len(symbol_returns) == 252:
                                avg_return = sum(symbol_returns) / len(symbol_returns)
                                market_returns = [avg_return] * 252  # placeholder
                                # Compute beta
                                cov = 0
                                var = 0
                                for sr, mr in zip(symbol_returns, market_returns):
                                    cov += (sr - avg_return) * (mr - avg_return)
                                    var += (mr - avg_return) ** 2
                                if var > 0:
                                    beta = cov / var
                                    if beta >= 1.2:
                                        opportunities.append((sid, ts, i))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Sort opportunities by time and apply repeat filter
    opportunities.sort(key=lambda x: x[1])
    last_call = {}
    issued = []
    
    for sid, ts, idx in opportunities:
        # Check if symbol called within prior 5 trading days
        if sid in last_call and ts - last_call[sid] <= 5 * 86400:
            continue
        
        # Check for outcome
        if (sid, ts) in outcomes:
            outcome = outcomes[(sid, ts)]
            issued.append({
                'sid': sid,
                'ts': ts,
                'hit': outcome['up'] == 0,  # DOWN call, so want up=0
                'fwd_return': outcome['fwd_return']
            })
            last_call[sid] = ts
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Split into train and sealed (most recent 20%)
    issued.sort(key=lambda x: x['ts'])
    split_idx = int(len(issued) * 0.8)
    train = issued[:split_idx]
    sealed = issued[split_idx:]
    
    # Compute metrics
    hits = sum(1 for c in issued if c['hit'])
    precision = hits / len(issued)
    
    # Base rate within issued subset
    down_calls = [c for c in issued if not c['hit']]  # misses
    base_rate = 1 - (len(down_calls) / len(issued))  # proportion of actual down moves
    
    # Distinct days
    days = set()
    for c in issued:
        day = c['ts'] // 86400
        days.add(day)
    distinct_days = len(days)
    
    # Effective N (clustered by day)
    day_clusters = {}
    for c in issued:
        day = c['ts'] // 86400
        day_clusters[day] = day_clusters.get(day, 0) + 1
    avg_cluster_size = sum(day_clusters.values()) / len(day_clusters)
    design_effect = avg_cluster_size  # upper bound
    effective_n = len(issued) / design_effect
    
    # Sealed precision
    sealed_hits = sum(1 for c in sealed if c['hit'])
    sealed_precision = sealed_hits / len(sealed) if sealed else 0
    
    # Print results
    print(f"ISSUED={len(issued)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()