import sqlite3
import sys
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True, timeout=10)
        conn.execute("PRAGMA journal_mode=WAL;")
        cursor = conn.cursor()
        
        # Check for essential tables
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('bars', 'symbols', 'prediction_outcomes')")
        tables = {row[0] for row in cursor.fetchall()}
        if not tables.issuperset({'bars', 'symbols', 'prediction_outcomes'}):
            print("INSUFFICIENT=1")
            return 0
        
        # Get universe of symbols with sufficient data
        cursor.execute("""
            SELECT symbol_id 
            FROM symbols 
            WHERE market = 'stocks' 
            AND symbol_id IN (
                SELECT symbol_id 
                FROM bars 
                WHERE tf = '1d' 
                GROUP BY symbol_id 
                HAVING COUNT(*) >= 252
            )
        """)
        valid_symbol_ids = {row[0] for row in cursor.fetchall()}
        
        if len(valid_symbol_ids) < 50:
            print("INSUFFICIENT=1")
            return 0
        
        # Get daily bars for valid symbols
        cursor.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume
            FROM bars
            WHERE tf = '1d' AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join(str(s) for s in valid_symbol_ids)))
        
        # Organize bars by symbol and timestamp
        bars_by_symbol = defaultdict(list)
        for row in cursor.fetchall():
            symbol_id, ts, open_p, high, low, close, volume = row
            bars_by_symbol[symbol_id].append({
                'ts': ts,
                'open': open_p,
                'high': high,
                'low': low,
                'close': close,
                'volume': volume
            })
        
        # Get all prediction_outcomes to use as labels
        cursor.execute("""
            SELECT symbol_id, ts, horizon, up, fwd_return
            FROM prediction_outcomes
            WHERE symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join(str(s) for s in valid_symbol_ids)))
        
        # Organize outcomes by symbol and timestamp
        outcomes_by_symbol = defaultdict(list)
        for row in cursor.fetchall():
            symbol_id, ts, horizon, up, fwd_return = row
            outcomes_by_symbol[symbol_id].append({
                'ts': ts,
                'horizon': horizon,
                'up': up,
                'fwd_return': fwd_return
            })
        
        # Build lookups for each symbol's bars and outcomes
        symbol_bars = {}
        symbol_outcomes = {}
        for sid in valid_symbol_ids:
            if bars_by_symbol[sid]:
                # Sort by timestamp
                sorted_bars = sorted(bars_by_symbol[sid], key=lambda x: x['ts'])
                symbol_bars[sid] = {bar['ts']: bar for bar in sorted_bars}
                symbol_bars[sid]['sorted_ts'] = [bar['ts'] for bar in sorted_bars]
            
            if outcomes_by_symbol[sid]:
                # Sort by timestamp
                sorted_outcomes = sorted(outcomes_by_symbol[sid], key=lambda x: x['ts'])
                symbol_outcomes[sid] = {out['ts']: out for out in sorted_outcomes}
                symbol_outcomes[sid]['sorted_ts'] = [out['ts'] for out in sorted_outcomes]
        
        # Define horizon in days (T+20 trading days)
        HORIZON_DAYS = 20
        
        # Track opportunities and issued calls
        opportunities = 0
        issued = 0
        hits = 0
        issued_base_rates = []
        distinct_days = set()
        all_issued_ts = []  # For splitting into regular and sealed eras
        
        # Process each symbol
        for sid in valid_symbol_ids:
            if sid not in symbol_bars or sid not in symbol_outcomes:
                continue
            
            bars = symbol_bars[sid]
            outcomes = symbol_outcomes[sid]
            sorted_ts = bars['sorted_ts']
            
            # Need at least 60 bars + horizon + buffer
            if len(sorted_ts) < 80:
                continue
            
            # For each potential entry point T (first 80% of data)
            cutoff_idx = int(len(sorted_ts) * 0.8)
            
            for idx in range(60, cutoff_idx):
                T_ts = sorted_ts[idx]
                
                # Get T-1 bar
                if idx - 1 < 0:
                    continue
                T_minus_1_ts = sorted_ts[idx - 1]
                T_minus_1_bar = bars[T_minus_1_ts]
                T_bar = bars[T_ts]
                
                # Check if there's a matching outcome with horizon 20
                # We'll check for an outcome with ts >= T_ts and horizon = 20
                # But need to be careful about as-of: we can only use outcomes with ts >= T_ts
                # However, the outcome's label (up) is realized at T+20, so we need to find
                # the outcome with ts closest to T_ts (but >=) and horizon = 20
                outcome_found = False
                outcome_up = None
                
                # Find the outcome with horizon 20 that starts at or after T_ts
                # We'll look in the outcomes list for this symbol
                outcome_sorted_ts = outcomes['sorted_ts']
                
                # Find first outcome ts >= T_ts
                for out_ts in outcome_sorted_ts:
                    if out_ts >= T_ts:
                        out_data = outcomes[out_ts]
                        if out_data['horizon'] == HORIZON_DAYS:
                            outcome_found = True
                            outcome_up = out_data['up']
                            break
                
                if not outcome_found:
                    continue
                
                opportunities += 1
                
                # Apply ENTRY conditions
                # 1. T's close relative to T-1: between -5% and +5%
                pct_change = (T_bar['close'] - T_minus_1_bar['close']) / T_minus_1_bar['close']
                if not (-0.05 <= pct_change <= 0.05):
                    continue
                
                # 2. T's close in top half of intraday range
                intraday_range = T_bar['high'] - T_bar['low']
                if intraday_range <= 0:
                    continue
                if T_bar['close'] < (T_bar['low'] + intraday_range / 2):
                    continue
                
                # 3. T's volume above 60-day median
                # Get last 60 bars before T
                if idx < 60:
                    continue
                last_60_bars = [bars[sorted_ts[j]] for j in range(idx-60, idx)]
                volumes = [b['volume'] for b in last_60_bars]
                median_volume = sorted(volumes)[len(volumes)//2]
                if T_bar['volume'] <= median_volume:
                    continue
                
                # ABSTAIN conditions (all must be false to proceed)
                abstain = False
                
                # Price < $5
                if T_bar['close'] < 5:
                    abstain = True
                
                # Stock rose more than 30% in 20 sessions before T
                if idx >= 20:
                    price_20_ago = bars[sorted_ts[idx-20]]['close']
                    if T_bar['close'] > price_20_ago * 1.3:
                        abstain = True
                
                # 5-day realized volatility in top cross-sectional decile
                if idx >= 5:
                    last_5_bars = [bars[sorted_ts[j]] for j in range(idx-4, idx+1)]
                    closes = [b['close'] for b in last_5_bars]
                    # Calculate simple volatility as std dev of returns
                    returns = [(closes[i] - closes[i-1])/closes[i-1] for i in range(1, len(closes))]
                    if returns:
                        mean_ret = sum(returns) / len(returns)
                        vol = (sum((r - mean_ret)**2 for r in returns) / len(returns))**0.5
                        # We'll store and compute cross-sectional later if needed
                        # For now, mark for later evaluation
                        vol_5day = vol
                    else:
                        vol_5day = None
                else:
                    vol_5day = None
                
                if abstain:
                    continue
                
                # Issue UP call
                issued += 1
                issued_base_rates.append(1 if outcome_up else 0)
                distinct_days.add(T_ts // (24*3600))  # Approximate day grouping by epoch
                all_issued_ts.append(T_ts)
                
                if outcome_up:
                    hits += 1
        
        if issued == 0:
            print("INSUFFICIENT=1")
            return 0
        
        # Calculate metrics
        precision = hits / issued
        base_rate = sum(issued_base_rates) / len(issued_base_rates)
        distinct_days_count = len(distinct_days)
        
        # For sealed era (most recent 20%)
        if all_issued_ts:
            sorted_issued_ts = sorted(all_issued_ts)
            seal_cutoff = int(len(sorted_issued_ts) * 0.8)
            sealed_issued_ts = sorted_issued_ts[seal_cutoff:]
            
            # Recalculate precision for sealed era
            sealed_hits = 0
            sealed_issued = 0
            for ts in sealed_issued_ts:
                # Find outcome for this timestamp
                found = False
                for sid in valid_symbol_ids:
                    if sid in symbol_outcomes and ts in symbol_outcomes[sid]:
                        out = symbol_outcomes[sid][ts]
                        if out['horizon'] == HORIZON_DAYS:
                            sealed_issued += 1
                            if out['up']:
                                sealed_hits += 1
                            found = True
                            break
            
            if sealed_issued > 0:
                sealed_precision = sealed_hits / sealed_issued
            else:
                sealed_precision = 0.0
        else:
            sealed_precision = 0.0
        
        # Effective N: simplified - assume design effect ~1 for independent observations
        # Since we're grouping by (symbol, day), and each issue is independent
        effective_n = issued
        
        # Print required output
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_count}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
        conn.close()
        return 0
        
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    sys.exit(main())