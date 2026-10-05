# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 296
# cycle_index: 19
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import collections

def main():
    db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    db.row_factory = sqlite3.Row
    c = db.cursor()
    
    # Get all insider trades
    c.execute('''SELECT symbol_id, code, tx_ts, filed_ts 
                 FROM insider_trades ORDER BY symbol_id, filed_ts''')
    all_trades = c.fetchall()
    if not all_trades:
        print("INSUFFICIENT=1")
        return 0
    
    # Group trades by symbol
    symbols = {}
    for t in all_trades:
        sid = t[0]
        if sid not in symbols:
            symbols[sid] = []
        symbols[sid].append(t)
    
    # Get symbols with daily bars
    c.execute('''SELECT symbol_id, MIN(ts) as min_ts, MAX(ts) as max_ts 
                 FROM bars WHERE tf='1d' GROUP BY symbol_id''')
    bar_info = {r[0]: (r[1], r[2]) for r in c.fetchall()}
    
    # Process each symbol
    opportunities = []
    for sid, trades in symbols.items():
        if sid not in bar_info:
            continue
        min_ts, max_ts = bar_info[sid]
        
        # For each potential decision point (each Form 4 purchase)
        for i, trade in enumerate(trades):
            code = trade[1]
            filed_ts = trade[3]
            
            # Must be a purchase (P)
            if code != 'P':
                continue
            
            # Check if there are at least 1000 calendar days of price history as of filed_ts
            # Convert filed_ts to date
            filed_date = datetime.datetime.utcfromtimestamp(filed_ts).date()
            min_date = datetime.datetime.utcfromtimestamp(min_ts).date()
            if (filed_date - min_date).days < 1000:
                continue
            
            # Check 730-day drought: no other purchase in past 730 calendar days
            drought = True
            cutoff_ts = filed_ts - 730*24*3600
            for prev_trade in trades[:i]:
                if prev_trade[1] == 'P' and prev_trade[2] >= cutoff_ts:
                    drought = False
                    break
            if not drought:
                continue
            
            # Check no insider sale disclosed in 90 calendar days before filing
            sale_cutoff_ts = filed_ts - 90*24*3600
            has_sale = False
            for t in trades:
                if t[1] == 'S' and t[3] >= sale_cutoff_ts and t[3] < filed_ts:
                    has_sale = True
                    break
            if has_sale:
                continue
            
            # Found a qualifying call - now need future return
            opportunities.append((sid, filed_ts))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by filing date and split into train/sealed (80%/20%)
    opportunities.sort(key=lambda x: x[1])
    split_idx = int(len(opportunities) * 0.8)
    sealed = opportunities[split_idx:]
    train = opportunities[:split_idx]
    
    # Get future returns for each opportunity
    def get_return(sid, entry_ts, horizon_days=60):
        # Get entry price
        c.execute('''SELECT close FROM bars 
                     WHERE symbol_id=? AND tf='1d' AND ts<=? 
                     ORDER BY ts DESC LIMIT 1''', (sid, entry_ts))
        row = c.fetchone()
        if not row:
            return None
        entry_price = row[0]
        
        # Get exit price (horizon_days trading days later)
        c.execute('''SELECT ts, close FROM bars 
                     WHERE symbol_id=? AND tf='1d' AND ts>? 
                     ORDER BY ts ASC''', (sid, entry_ts))
        rows = c.fetchall()
        if len(rows) < horizon_days:
            return None
        exit_price = rows[horizon_days-1][1]
        
        return (exit_price - entry_price) / entry_price
    
    # Process all opportunities
    all_calls = []
    for sid, entry_ts in opportunities:
        ret = get_return(sid, entry_ts)
        if ret is not None:
            hit = 1 if ret > 0 else 0
            entry_date = datetime.datetime.utcfromtimestamp(entry_ts).date()
            all_calls.append((sid, entry_ts, entry_date, hit))
    
    if not all_calls:
        print("INSUFFICIENT=1")
        return 0
    
    # Split into train/sealed
    sealed_calls = [c for c in all_calls if c[1] >= opportunities[split_idx][1]]
    train_calls = [c for c in all_calls if c[1] < opportunities[split_idx][1]]
    
    # Calculate metrics
    def calculate_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0, 0
        
        issued = len(calls)
        hits = sum(c[3] for c in calls)
        precision = hits / issued if issued > 0 else 0
        distinct_days = len(set(c[2] for c in calls))
        
        # Base rate of positive returns in issued subset
        # (should be same as precision since we only issued positive?)
        # Actually base rate is proportion of positive returns in opportunities
        # But requirement says "within the issued subset"
        base_rate = precision
        
        # Design effect: group by day
        day_groups = collections.Counter(c[2] for c in calls)
        n_clusters = len(day_groups)
        if n_clusters == 0:
            return 0, 0, 0, 0, 0, 0
        
        # Calculate intracluster correlation (ICC)
        # Using one-way random effects ANOVA for binary outcomes
        k_bar = issued / n_clusters  # average cluster size
        
        # Calculate group means
        day_means = {}
        for date, count in day_groups.items():
            day_hits = sum(1 for c in calls if c[2] == date and c[3] == 1)
            day_means[date] = day_hits / count
        
        # Overall mean
        overall_mean = hits / issued if issued > 0 else 0
        
        # Between-cluster variance
        ss_between = 0
        for date, mean in day_means.items():
            count = day_groups[date]
            ss_between += count * (mean - overall_mean) ** 2
        ms_between = ss_between / (n_clusters - 1) if n_clusters > 1 else 0
        
        # Within-cluster variance
        ss_within = 0
        for date, count in day_groups.items():
            mean = day_means[date]
            for c in calls:
                if c[2] == date:
                    ss_within += (c[3] - mean) ** 2
        ms_within = ss_within / (issued - n_clusters) if issued > n_clusters else 0
        
        # ICC for binary data
        if ms_within > 0:
            icc = (ms_between - ms_within) / (ms_between + (k_bar - 1) * ms_within)
        else:
            icc = 0
        
        # Design effect
        deff = 1 + (k_bar - 1) * icc
        
        effective_n = issued / deff if deff > 0 else issued
        
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    all_issued, all_hits, all_prec, all_br, all_days, all_eff = calculate_metrics(all_calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = calculate_metrics(sealed_calls)
    
    # Print required metrics
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_eff:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")
    
    return 0

if __name__ == "__main__":
    main()