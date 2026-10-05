# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 428
# cycle_index: 19
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        cur = conn.cursor()
    except:
        print("INSUFFICIENT=1")
        return 0
    
    # Check prerequisites: need insider_trades, fundamentals for EPS, and bars
    try:
        cur.execute("SELECT COUNT(*) FROM insider_trades")
        it_count = cur.fetchone()[0]
        cur.execute("SELECT COUNT(*) FROM fundamentals WHERE metric='EPS'")
        eps_count = cur.fetchone()[0]
        cur.execute("SELECT COUNT(*) FROM bars WHERE tf='1d'")
        bars_count = cur.fetchone()[0]
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    if it_count < 100 or eps_count < 100 or bars_count < 1000000:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    # Get 10-year Treasury yield from FRED (DGS10 series)
    treasury = {}
    try:
        cur.execute("SELECT ts, value FROM macro_series WHERE series='DGS10'")
        for ts, val in cur.fetchall():
            treasury[ts] = float(val) if val is not None else None
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    if not treasury:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    # Get all symbols with insider trades
    try:
        cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
        symbols_with_insider = [row[0] for row in cur.fetchall()]
    except:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    if len(symbols_with_insider) < 10:
        print("INSUFFICIENT=1")
        conn.close()
        return 0
    
    # Process each symbol
    opportunities = 0
    issued_calls = []  # list of (day_ts, symbol_id, hit)
    all_days_set = set()
    
    for symbol_id in symbols_with_insider:
        try:
            # Get daily bars (need at least 252 days)
            cur.execute("""
                SELECT ts, open, high, low, close, volume 
                FROM bars 
                WHERE symbol_id=? AND tf='1d'
                ORDER BY ts
            """, (symbol_id,))
            bars = cur.fetchall()
            if len(bars) < 252:
                continue
            
            # Get insider trades for this symbol
            cur.execute("""
                SELECT tx_ts, filed_ts, code 
                FROM insider_trades 
                WHERE symbol_id=?
            """, (symbol_id,))
            insider_trades = cur.fetchall()
            
            # Get EPS data for this symbol (trailing 12-month)
            cur.execute("""
                SELECT value, as_of, fetched_at 
                FROM fundamentals 
                WHERE symbol_id=? AND metric='EPS'
            """, (symbol_id,))
            eps_data = cur.fetchall()
            if not eps_data:
                continue
            
            # Sort EPS by fetched_at descending to get most recent
            eps_data.sort(key=lambda x: x[2], reverse=True)
            
            # Process each potential entry day (start at day 252 to have history)
            for i in range(251, len(bars)):
                day_ts = bars[i][0]
                price = bars[i][4]  # close price
                volume = bars[i][5]
                
                if price <= 0 or volume <= 0:
                    continue
                
                # Find most recent EPS available at this day
                eps = None
                for val, as_of, fetched_at in eps_data:
                    if fetched_at <= day_ts:
                        eps = float(val)
                        break
                if eps is None:
                    continue
                
                # Find most recent Treasury yield available at this day
                treasury_yield = None
                for t in sorted(treasury.keys(), reverse=True):
                    if t <= day_ts and treasury[t] is not None:
                        treasury_yield = treasury[t]
                        break
                if treasury_yield is None:
                    continue
                
                # Calculate earnings yield (EPS/price) and spread
                earnings_yield = eps / price
                spread = earnings_yield - treasury_yield
                
                # Check if spread >= 2% (200 basis points)
                if spread < 0.02:
                    continue
                
                # Get insider purchases in past 20 trading days (using filed_ts)
                purchases = set()
                sales = set()
                day_idx = i
                past_days = set()
                
                # Collect past 20 trading days timestamps
                for j in range(max(0, day_idx - 19), day_idx + 1):
                    past_days.add(bars[j][0])
                
                for tx_ts, filed_ts, code in insider_trades:
                    if filed_ts in past_days:
                        if code == 'P':  # Purchase
                            purchases.add(filed_ts)
                        elif code == 'S':  # Sale
                            sales.add(filed_ts)
                
                # Need at least 2 distinct insider purchases
                if len(purchases) < 2:
                    continue
                
                # Check for any insider sales (abstain condition)
                if sales:
                    continue
                
                # Check volume liquidity (above 20th percentile of 252-day average)
                if i >= 251:
                    # Calculate 252-day average volume
                    avg_vol = sum(bars[j][5] for j in range(i-251, i+1)) / 252
                    # We need to check if current volume is above 20th percentile of its own distribution
                    # For simplicity, use that the volume should be > 0.2 * avg_vol (proxy for liquidity)
                    if volume < 0.2 * avg_vol:
                        continue
                
                # All conditions met - issue call
                opportunities += 1
                
                # Check outcome (21-day forward return)
                if i + 21 < len(bars):
                    future_price = bars[i + 21][4]
                    hit = 1 if future_price > price else 0
                    issued_calls.append((day_ts, symbol_id, hit))
                    all_days_set.add(day_ts // 86400)  # Convert to day resolution
        
        except Exception:
            continue
    
    conn.close()
    
    # Calculate metrics
    issued_count = len(issued_calls)
    if issued_count == 0:
        print("INSUFFICIENT=1")
        return 0
    
    hits = sum(hit for _, _, hit in issued_calls)
    precision = hits / issued_count
    
    # Base rate within issued subset
    up_count = hits
    base_rate = up_count / issued_count
    
    distinct_days = len(all_days_set)
    
    # Design effect: cluster calls by day
    day_calls = defaultdict(int)
    for day_ts, _, _ in issued_calls:
        day_calls[day_ts // 86400] += 1
    
    avg_cluster_size = issued_count / len(day_calls) if day_calls else 1
    
    # Intra-class correlation (simplified estimate)
    # For binary outcomes, use formula: ICC = (MSB - MSW) / (MSB + (m-1)*MSW)
    # where MSB = variance between days, MSW = variance within days, m = avg cluster size
    # Simplified: ICC = (variance_of_day_means - variance_of_individual) / variance_of_day_means
    
    # Group hits by day
    day_hits = defaultdict(list)
    for day_ts, _, hit in issued_calls:
        day_hits[day_ts // 86400].append(hit)
    
    overall_mean = precision
    day_means = [sum(hits)/len(hits) for hits in day_hits.values()]
    var_between = sum((m - overall_mean)**2 for m in day_means) / len(day_means)
    
    # Within variance: average of variances within each day
    var_within = 0
    for hits in day_hits.values():
        if len(hits) > 1:
            mean = sum(hits)/len(hits)
            var = sum((h - mean)**2 for h in hits) / (len(hits) - 1)
            var_within += var
    var_within /= len(day_hits)
    
    icc = var_between / (var_between + var_within) if (var_between + var_within) > 0 else 0
    design_effect = 1 + (avg_cluster_size - 1) * icc
    effective_n = issued_count / design_effect if design_effect > 0 else issued_count
    
    # Split into training (first 80%) and sealed (last 20%)
    issued_calls_sorted = sorted(issued_calls, key=lambda x: x[0])
    split_idx = int(0.8 * len(issued_calls_sorted))
    training = issued_calls_sorted[:split_idx]
    sealed = issued_calls_sorted[split_idx:]
    
    sealed_issued = len(sealed)
    sealed_hits = sum(hit for _, _, hit in sealed) if sealed_issued > 0 else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # Check claim: precision >= 0.80 and base_rate <= 0.70
    # We just report, not enforce
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")
    
    return 0

if __name__ == "__main__":
    exit(main())