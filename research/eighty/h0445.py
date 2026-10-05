# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 444
# cycle_index: 35
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict

def day_to_ts(day_str):
    """Convert YYYY-MM-DD to unix timestamp at midnight UTC."""
    parts = day_str.split('-')
    if len(parts) != 3:
        return None
    y, m, d = map(int, parts)
    # Approximate: days since epoch * 86400
    # Using a simple conversion
    import datetime
    return int(datetime.datetime(y, m, d, tzinfo=datetime.timezone.utc).timestamp())

def ts_to_day(ts):
    """Convert unix timestamp to YYYY-MM-DD."""
    import datetime
    return datetime.datetime.fromtimestamp(ts, tz=datetime.timezone.utc).strftime('%Y-%m-%d')

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    # Get all symbols with daily bars from 2018-07-26 onward
    cur.execute("""
        SELECT DISTINCT symbol_id FROM bars 
        WHERE tf = '1d' AND ts >= strftime('%s', '2018-07-26')
    """)
    symbols = [row[0] for row in cur.fetchall()]
    
    if not symbols:
        print("INSUFFICIENT=1")
        return
    
    # Get sentiment data for these symbols
    placeholders = ','.join('?' * len(symbols))
    cur.execute(f"""
        SELECT symbol_id, day, mean_score 
        FROM sentiment_features 
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbols)
    sentiment_rows = cur.fetchall()
    
    sentiment_dict = defaultdict(list)
    for sid, day, score in sentiment_rows:
        ts = day_to_ts(day)
        if ts is not None:
            sentiment_dict[sid].append((ts, score))
    
    # Get institutional holdings aggregated by symbol and period
    cur.execute(f"""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id IN ({placeholders})
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """, symbols)
    inst_rows = cur.fetchall()
    
    inst_dict = defaultdict(list)
    for sid, period, shares in inst_rows:
        # period format: 'YYYY-QN' e.g., '2023-Q1'
        # Convert quarter end to timestamp (approximate)
        # Q1 ends Mar 31, Q2 Jun 30, Q3 Sep 30, Q4 Dec 31
        try:
            year_str, q_str = period.split('-Q')
            year = int(year_str)
            quarter = int(q_str)
            if quarter == 1:
                month, day = 3, 31
            elif quarter == 2:
                month, day = 6, 30
            elif quarter == 3:
                month, day = 9, 30
            else:
                month, day = 12, 31
            import datetime
            period_ts = int(datetime.datetime(year, month, day, tzinfo=datetime.timezone.utc).timestamp())
            inst_dict[sid].append((period_ts, shares, period))
        except:
            continue
    
    # Get shares outstanding (fundamentals)
    cur.execute(f"""
        SELECT symbol_id, value, fetched_at 
        FROM fundamentals 
        WHERE metric = 'SharesOutstanding' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, fetched_at
    """, symbols)
    shares_rows = cur.fetchall()
    
    shares_dict = defaultdict(list)
    for sid, value, fetched_at in shares_rows:
        try:
            shares_dict[sid].append((fetched_at, float(value)))
        except:
            continue
    
    # Get VIX data
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'VIXCLS' ORDER BY ts")
    vix_rows = cur.fetchall()
    vix_dict = {}
    for ts, val in vix_rows:
        day_ts = (ts // 86400) * 86400
        vix_dict[day_ts] = val
    
    # Get prediction outcomes for 21-day horizon (labels)
    cur.execute(f"""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21 AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbols)
    label_rows = cur.fetchall()
    
    label_dict = defaultdict(dict)
    for sid, ts, up, fwd_ret in label_rows:
        label_dict[sid][ts] = (up, fwd_ret)
    
    # Get all daily bars for symbols
    # We'll fetch per symbol to avoid memory issues
    all_opportunities = []
    all_issued = []
    
    for sid in symbols:
        if sid not in sentiment_dict or not sentiment_dict[sid]:
            continue
        if sid not in inst_dict or not inst_dict[sid]:
            continue
        if sid not in shares_dict or not shares_dict[sid]:
            continue
            
        cur.execute("""
            SELECT ts, close, volume FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sid,))
        bars = cur.fetchall()
        
        if len(bars) < 252:
            continue
        
        dates = [row[0] for row in bars]
        closes = [row[1] for row in bars]
        volumes = [row[2] for row in bars]
        
        # Precompute 200-day MA
        ma200 = {}
        for i in range(199, len(bars)):
            ma200[dates[i]] = sum(closes[i-199:i+1]) / 200
        
        # Precompute 20-day average dollar volume
        avg_dollar_vol = {}
        for i in range(19, len(bars)):
            total = 0.0
            for j in range(i-19, i+1):
                total += closes[j] * volumes[j]
            avg_dollar_vol[dates[i]] = total / 20
        
        # Sort sentiment data by timestamp
        sent_data = sorted(sentiment_dict[sid])
        sent_ts = [d for d, _ in sent_data]
        sent_scores = [s for _, s in sent_data]
        
        # Sort inst holdings by period_ts
        inst_data = sorted(inst_dict[sid], key=lambda x: x[0])
        
        # Sort shares outstanding by fetched_at
        shares_data = sorted(shares_dict[sid], key=lambda x: x[0])
        
        # For each bar date, evaluate entry conditions
        for i, ts in enumerate(dates):
            if ts < dates[199]:  # Need 200 days for MA
                continue
            if ts not in ma200 or ts not in avg_dollar_vol:
                continue
            
            # Check volume requirement
            if avg_dollar_vol[ts] < 5_000_000:
                continue
            
            # Check VIX requirement (use day-aligned timestamp)
            day_ts = (ts // 86400) * 86400
            vix_val = vix_dict.get(day_ts)
            if vix_val is not None and vix_val > 35:
                continue
            
            # Check price above 200-day MA
            if closes[i] <= ma200[ts]:
                continue
            
            # Check sentiment data availability for prior 20 days
            day_idx = ts // 86400
            prior_sent = [s for d, s in sent_data if day_idx - 20 <= (d // 86400) <= day_idx]
            if len(prior_sent) < 20:
                continue
            
            # Calculate 252-day rolling sentiment z-score
            window_sent = [s for d, s in sent_data if day_idx - 252 <= (d // 86400) <= day_idx]
            if len(window_sent) < 100:
                continue
            mean_s = sum(window_sent) / len(window_sent)
            std_s = (sum((x - mean_s) ** 2 for x in window_sent) / len(window_sent)) ** 0.5
            if std_s == 0:
                continue
            
            # Current sentiment (most recent within 5 days)
            recent_sent = [s for d, s in sent_data if day_idx - 5 <= (d // 86400) <= day_idx]
            if not recent_sent:
                continue
            current_sent = recent_sent[-1]
            sentiment_z = (current_sent - mean_s) / std_s
            
            if sentiment_z >= -2.33:
                continue
            
            # Check institutional ownership
            # Find most recent 13F filing where period_end + 45 days <= ts and ts - period_end <= 135 days
            valid_inst_shares = None
            valid_period_ts = None
            for period_ts, inst_shares, period_str in reversed(inst_data):
                # Filing becomes public ~45 days after period end
                filing_ts = period_ts + 45 * 86400
                if filing_ts <= ts and (ts - period_ts) <= 135 * 86400:
                    valid_inst_shares = inst_shares
                    valid_period_ts = period_ts
                    break
            
            if valid_inst_shares is None:
                continue
            
            # Get shares outstanding as of filing date (knowable at filing_ts)
            # Use most recent fetched_at <= filing_ts
            shares_outstanding = None
            for fetched_at, value in reversed(shares_data):
                if fetched_at <= filing_ts:
                    shares_outstanding = value
                    break
            
            if shares_outstanding is None or shares_outstanding == 0:
                continue
            
            inst_ownership_pct = valid_inst_shares / shares_outstanding
            if inst_ownership_pct <= 0.30:
                continue
            
            # All entry conditions met - this is an opportunity
            all_opportunities.append((sid, ts))
            
            # Get label for 21-day horizon
            # prediction_outcomes ts is the prediction timestamp
            # We need the outcome for a prediction made at ts
            label_key = ts
            if label_key in label_dict[sid]:
                up, fwd_ret = label_dict[sid][label_key]
                hit = 1 if up == 1 else 0
                all_issued.append((sid, ts, hit))
    
    if not all_opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Sort by timestamp
    all_opportunities.sort(key=lambda x: x[1])
    all_issued.sort(key=lambda x: x[1])
    
    # Split: hold out most recent 20% as sealed era
    n_total = len(all_opportunities)
    split_idx = int(n_total * 0.8)
    
    train_opps = all_opportunities[:split_idx]
    sealed_opps = all_opportunities[split_idx:]
    
    train_issued = [x for x in all_issued if x[1] <= train_opps[-1][1]] if train_opps else []
    sealed_issued = [x for x in all_issued if x[1] > train_opps[-1][1]] if train_opps else all_issued
    
    # Compute metrics on full set
    issued_count = len(all_issued)
    opportunities_count = len(all_opportunities)
    
    if issued_count == 0:
        print("INSUFFICIENT=1")
        return
    
    hits = sum(x[2] for x in all_issued)
    precision = hits / issued_count
    
    # Base rate within issued subset
    base_rate = hits / issued_count  # Same as precision for binary up/down
    
    # Distinct days among issued calls
    issued_days = set()
    for _, ts, _ in all_issued:
        issued_days.add(ts // 86400)
    distinct_days = len(issued_days)
    
    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Estimate ICC from data: correlation of outcomes within same day
    # Group issued by day
    day_outcomes = defaultdict(list)
    for _, ts, hit in all_issued:
        day_outcomes[ts // 86400].append(hit)
    
    # Calculate ICC (intraclass correlation)
    # Using ANOVA estimator
    all_hits = [h for _, _, h in all_issued]
    grand_mean = sum(all_hits) / len(all_hits)
    
    # Between-group variance
    n_groups = len(day_outcomes)
    if n_groups > 1:
        group_means = [sum(v)/len(v) for v in day_outcomes.values()]
        group_sizes = [len(v) for v in day_outcomes.values()]
        
        ssb = sum(sz * (gm - grand_mean)**2 for sz, gm in zip(group_sizes, group_means))
        msb = ssb / (n_groups - 1) if n_groups > 1 else 0
        
        # Within-group variance
        ssw = sum(sum((h - gm)**2 for h in day_outcomes[d]) for d, gm in zip(day_outcomes.keys(), group_means))
        dfw = sum(group_sizes) - n_groups
        msw = ssw / dfw if dfw > 0 else 0
        
        if msb > 0 and msw > 0:
            icc = (msb - msw) / (msb + (sum(group_sizes)/n_groups - 1) * msw)
            icc = max(0, min(1, icc))
        else:
            icc = 0
        
        avg_cluster_size = sum(group_sizes) / n_groups
        design_effect = 1 + (avg_cluster_size - 1) * icc
        design_effect = max(1.0, design_effect)
    else:
        design_effect = 1.0
    
    effective_n = issued_count / design_effect
    
    # Sealed era metrics
    sealed_hits = sum(x[2] for x in sealed_issued)
    sealed_issued_count = len(sealed_issued)
    sealed_precision = sealed_hits / sealed_issued_count if sealed_issued_count > 0 else 0.0
    
    # Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()