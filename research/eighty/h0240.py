import sqlite3
import math
from collections import defaultdict
import datetime

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Check horizon=20 exists in prediction_outcomes
    cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon=20")
    if cur.fetchone()[0] == 0:
        print("INSUFFICIENT=1")
        return
    
    # Load symbols
    cur.execute("SELECT id, delisted_at FROM symbols")
    symbols = {row[0]: row[1] for row in cur.fetchall()}
    
    # Load daily bars (tf='1d')
    cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d'")
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row[0]].append((row[1], row[2], row[3]))
    
    # Sort bars by timestamp for each symbol
    for sid in bars_by_symbol:
        bars_by_symbol[sid].sort(key=lambda x: x[0])
    
    # Load insider trades (code='P' for purchase)
    cur.execute("SELECT symbol_id, tx_ts, filed_ts FROM insider_trades WHERE code='P'")
    trades_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        trades_by_symbol[row[0]].append((row[1], row[2]))
    
    # Load prediction outcomes (horizon=20)
    cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=20")
    outcomes = {}
    for row in cur.fetchall():
        outcomes[(row[0], row[1])] = row[2]
    
    # Build mapping from timestamp to trading day index for each symbol
    day_index = {}
    for sid, bars in bars_by_symbol.items():
        for idx, (ts, _, _) in enumerate(bars):
            day_index[(sid, ts)] = idx
    
    # Precompute returns and volatility for each symbol
    returns = {}
    volatilities = {}
    dollar_volume_avg = {}
    
    for sid, bars in bars_by_symbol.items():
        n = len(bars)
        if n < 262:  # Need at least 252 + 10 buffer
            continue
        
        # Precompute 20-session returns (through each day)
        for i in range(20, n):
            ts = bars[i][0]
            close_prev = bars[i-1][1]
            close_20 = bars[i-20][1]
            if close_20 > 0:
                returns[(sid, ts)] = close_prev / close_20 - 1
            else:
                returns[(sid, ts)] = 0
        
        # Precompute 20-session volatility (rolling std of log returns)
        for i in range(20, n):
            log_returns = []
            for j in range(i-19, i+1):
                close_prev = bars[j-1][1]
                close_curr = bars[j][1]
                if close_prev > 0:
                    log_returns.append(math.log(close_curr/close_prev))
            if len(log_returns) >= 2:
                mean = sum(log_returns)/len(log_returns)
                var = sum((x-mean)**2 for x in log_returns)/(len(log_returns)-1)
                volatilities[(sid, bars[i][0])] = math.sqrt(var)
            else:
                volatilities[(sid, bars[i][0])] = 0
        
        # Precompute 60-day average daily dollar volume
        for i in range(60, n):
            ts = bars[i][0]
            total_vol = 0
            for j in range(i-59, i+1):
                close, volume = bars[j][1], bars[j][2]
                total_vol += close * volume
            dollar_volume_avg[(sid, ts)] = total_vol / 60
    
    # Collect candidate (symbol, T) pairs
    candidates = []
    
    for sid, trades in trades_by_symbol.items():
        bars = bars_by_symbol.get(sid, [])
        if not bars:
            continue
        
        # Check delisted_at: if delisted, we can only use data up to delisted_at
        delisted_at = symbols.get(sid)
        if delisted_at and delisted_at != 'None' and delisted_at != '':
            try:
                delisted_ts = int(delisted_at)
            except:
                delisted_ts = float('inf')
        else:
            delisted_ts = float('inf')
        
        # For each insider trade
        for tx_ts, filed_ts in trades:
            # Map filed_ts to a trading day T
            if (sid, filed_ts) not in day_index:
                # Find next trading day >= filed_ts
                found = False
                for ts, _, _ in bars:
                    if ts >= filed_ts:
                        T_ts = ts
                        found = True
                        break
                if not found:
                    continue
            else:
                T_ts = filed_ts
            
            # Check if T_ts is before delisted_at
            if T_ts >= delisted_ts:
                continue
            
            T_idx = day_index.get((sid, T_ts))
            if T_idx is None:
                continue
            
            # Universe conditions
            if T_idx < 252:
                continue
            
            # Close at T-1 >= $5
            if bars[T_idx-1][1] < 5:
                continue
            
            # Average daily dollar volume >= $5M over T-60..T-1
            vol_key = (sid, T_ts)
            if vol_key in dollar_volume_avg:
                if dollar_volume_avg[vol_key] < 5_000_000:
                    continue
            else:
                continue
            
            # 20-session return through T-1 <= -10%
            ret_key = (sid, T_ts)
            if ret_key in returns:
                if returns[ret_key] > -0.10:
                    continue
            else:
                continue
            
            # Check trade date (tx_ts) is within T-10..T-1
            trade_day = None
            if (sid, tx_ts) in day_index:
                trade_day = day_index[(sid, tx_ts)]
            else:
                # Find next trading day >= tx_ts
                for ts, _, _ in bars:
                    if ts >= tx_ts:
                        trade_day = day_index.get((sid, ts))
                        break
            
            if trade_day is None or trade_day < T_idx - 10 or trade_day > T_idx - 1:
                continue
            
            # Check volatility not in top cross-sectional decile at T
            vol_at_T = volatilities.get((sid, T_ts))
            if vol_at_T is None:
                continue
            
            # We'll check cross-sectional decile later when we have all candidates at each T
            # For now, add as candidate
            candidates.append((sid, T_ts, T_idx))
    
    # Group candidates by decision date T_ts
    candidates_by_T = defaultdict(list)
    for sid, T_ts, T_idx in candidates:
        candidates_by_T[T_ts].append((sid, T_idx))
    
    # Compute volatility deciles for each T
    all_vol_at_T = defaultdict(list)
    for (sid, T_ts), vol in volatilities.items():
        if any(T_ts == c[1] for c in candidates):
            all_vol_at_T[T_ts].append(vol)
    
    deciles = {}
    for T_ts, vol_list in all_vol_at_T.items():
        if vol_list:
            vol_list.sort()
            idx_90 = int(len(vol_list) * 0.9)
            if idx_90 >= len(vol_list):
                idx_90 = len(vol_list) - 1
            deciles[T_ts] = vol_list[idx_90]
    
    # Apply abstain conditions and collect issued calls
    issued = []
    opportunities = 0
    call_history = defaultdict(list)  # symbol -> list of call T_idx
    
    for T_ts in sorted(candidates_by_T.keys()):
        # Compute cross-sectional volatility decile for this T
        vol_threshold = deciles.get(T_ts, float('inf'))
        
        for sid, T_idx in candidates_by_T[T_ts]:
            opportunities += 1
            
            # Check volatility not in top cross-sectional decile
            vol_key = (sid, T_ts)
            if vol_key in volatilities:
                if volatilities[vol_key] >= vol_threshold:
                    continue
            
            # Check no call issued for same symbol in prior 20 trading days
            recent_calls = [c for c in call_history[sid] if T_idx - c <= 20]
            if recent_calls:
                continue
            
            # Check label exists
            label = outcomes.get((sid, T_ts))
            if label is None:
                continue
            
            # Issue call
            issued.append((sid, T_ts, T_idx, label))
            call_history[sid].append(T_idx)
    
    # Check if insufficient observations
    if len(issued) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Compute metrics
    hits = sum(1 for _, _, _, up in issued if up == 1)
    precision = hits / len(issued)
    
    # Base rate of predicted class (UP) within issued subset
    base_rate = hits / len(issued)
    
    # Distinct days among issued calls
    distinct_days = len(set(T_ts for _, T_ts, _, _ in issued))
    
    # Design effect: cluster by decision date
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for _, T_ts, _, up in issued:
        day_counts[T_ts] += 1
        if up == 1:
            day_hits[T_ts] += 1
    
    n_clusters = len(day_counts)
    if n_clusters > 1 and len(issued) > 1:
        # Compute ICC for binary outcomes
        overall_p = precision
        between_var = 0
        within_var = 0
        for T_ts, count in day_counts.items():
            p_d = day_hits[T_ts] / count
            between_var += count * (p_d - overall_p) ** 2
        between_var /= (len(issued) - 1)
        within_var = overall_p * (1 - overall_p) - between_var
        if within_var < 0:
            within_var = 0
        if between_var + within_var > 0:
            icc = between_var / (between_var + within_var)
        else:
            icc = 0
        
        avg_cluster_size = len(issued) / n_clusters
        design_effect = 1 + (avg_cluster_size - 1) * icc
        if design_effect <= 1:
            design_effect = 1.01
    else:
        design_effect = 1.01
    
    effective_n = len(issued) / design_effect
    
    # Sealed era: most recent 20% of calls by T_ts
    issued_sorted = sorted(issued, key=lambda x: x[1])
    sealed_idx = int(len(issued_sorted) * 0.8)
    sealed = issued_sorted[sealed_idx:]
    
    if sealed:
        sealed_hits = sum(1 for _, _, _, up in sealed if up == 1)
        sealed_precision = sealed_hits / len(sealed)
    else:
        sealed_precision = 0.0
    
    # Output
    print(f"ISSUED={len(issued)}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()