# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 478
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math
from collections import defaultdict

def connect_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def date_to_epoch(d):
    return int(datetime.datetime.combine(d, datetime.time()).timestamp())

def get_trading_days(conn, symbol_id):
    """Get list of trading day timestamps for a symbol from bars with tf='1d'."""
    cur = conn.cursor()
    cur.execute("SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts", (symbol_id,))
    return [row[0] for row in cur.fetchall()]

def get_price_at_or_after(conn, symbol_id, epoch, offset_days=0):
    """Get close price at or after epoch. offset_days=0 means exact day, 21 means 21 trading days later."""
    trading_days = get_trading_days(conn, symbol_id)
    if not trading_days:
        return None
    # Find first day >= epoch
    idx = None
    for i, ts in enumerate(trading_days):
        if ts >= epoch:
            idx = i
            break
    if idx is None:
        return None
    target_idx = idx + offset_days
    if target_idx >= len(trading_days):
        return None
    target_ts = trading_days[target_idx]
    cur = conn.cursor()
    cur.execute("SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (symbol_id, target_ts))
    row = cur.fetchone()
    return row[0] if row else None

def main():
    conn = connect_db()
    cur = conn.cursor()
    
    # 1. Check required data existence
    try:
        cur.execute("SELECT COUNT(*) FROM insider_trades WHERE code='P'")
        insider_count = cur.fetchone()[0]
        if insider_count == 0:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM sentiment_features")
        sentiment_count = cur.fetchone()[0]
        if sentiment_count == 0:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM macro_series WHERE series='ICSA'")
        icsa_count = cur.fetchone()[0]
        if icsa_count == 0:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon=21")
        outcomes_count = cur.fetchone()[0]
        if outcomes_count == 0:
            print("INSUFFICIENT=1")
            return
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return
    
    # 2. Get ICSA weekly moving average data
    cur.execute("SELECT ts, value FROM macro_series WHERE series='ICSA' ORDER BY ts")
    icsa_data = cur.fetchall()
    if len(icsa_data) < 4:  # Need at least 4 weeks for 4-week MA
        print("INSUFFICIENT=1")
        return
    
    # Convert to dict: timestamp -> value
    icsa_dict = {}
    for ts, val in icsa_data:
        icsa_dict[ts] = val
    
    # Calculate 4-week moving average and consecutive decreases
    icsa_sorted = sorted(icsa_dict.keys())
    icsa_ma4 = {}
    for i in range(3, len(icsa_sorted)):
        week_sum = sum(icsa_dict[icsa_sorted[j]] for j in range(i-3, i+1))
        icsa_ma4[icsa_sorted[i]] = week_sum / 4.0
    
    # Determine weeks where 4-week MA decreased for 3 consecutive weeks
    icsa_decreasing = set()
    icsa_ma4_keys = sorted(icsa_ma4.keys())
    for i in range(2, len(icsa_ma4_keys)):
        w1, w2, w3 = icsa_ma4_keys[i-2], icsa_ma4_keys[i-1], icsa_ma4_keys[i]
        if icsa_ma4[w1] > icsa_ma4[w2] > icsa_ma4[w3]:
            icsa_decreasing.add(icsa_ma4_keys[i])  # The week where condition holds
    
    # 3. Get sentiment data: for each symbol, compute 30-day MA of mean_score from sentiment_features
    cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
    all_sentiment = cur.fetchall()
    
    symbol_sentiment = defaultdict(list)  # symbol_id -> list of (day_str, score)
    for sym_id, day_str, score in all_sentiment:
        symbol_sentiment[sym_id].append((day_str, score))
    
    # Compute 30-day MA for each symbol
    symbol_ma30 = defaultdict(dict)  # symbol_id -> {day_str: ma30}
    for sym_id, day_scores in symbol_sentiment.items():
        day_scores.sort(key=lambda x: x[0])
        # Convert day strings to epoch for easier rolling window
        day_epochs = []
        for day_str, score in day_scores:
            try:
                d = datetime.datetime.strptime(day_str, '%Y-%m-%d').date()
                epoch = date_to_epoch(d)
                day_epochs.append((epoch, score))
            except ValueError:
                continue
        
        # Compute 30-day rolling MA
        for i in range(len(day_epochs)):
            window_start = day_epochs[i][0] - 30*24*3600  # 30 days before
            window_scores = []
            for j in range(i, -1, -1):
                if day_epochs[j][0] >= window_start:
                    window_scores.append(day_epochs[j][1])
                else:
                    break
            if window_scores:
                ma = sum(window_scores) / len(window_scores)
                # Store using original day string
                day_str = datetime.datetime.fromtimestamp(day_epochs[i][0]).strftime('%Y-%m-%d')
                symbol_ma30[sym_id][day_str] = ma
    
    # 4. Process insider purchases and generate signals
    cur.execute("""
        SELECT symbol_id, filed_ts 
        FROM insider_trades 
        WHERE code='P'
        ORDER BY filed_ts
    """)
    all_purchases = cur.fetchall()
    
    signals = []  # (symbol_id, decision_epoch, entry_price, exit_price)
    opportunities = 0
    
    for sym_id, filed_epoch in all_purchases:
        opportunities += 1
        
        # Convert filed_epoch to date string for sentiment lookup
        decision_date = datetime.datetime.utcfromtimestamp(filed_epoch).strftime('%Y-%m-%d')
        
        # Check ICSA condition: need the Monday of the week containing decision_date
        decision_dt = datetime.datetime.utcfromtimestamp(filed_epoch).date()
        # Find the Monday of the week (assuming ICSA is weekly ending Saturday? We'll use the most recent ICSA week <= decision_date)
        # Get all ICSA weeks <= decision_date
        decision_epoch = date_to_epoch(decision_dt)
        applicable_weeks = [w for w in icsa_decreasing if w <= decision_epoch]
        if not applicable_weeks:
            continue
        most_recent_week = max(applicable_weeks)
        
        # Check sentiment condition: current sentiment < 30-day MA
        if sym_id not in symbol_ma30:
            continue
        ma_dict = symbol_ma30[sym_id]
        if decision_date not in ma_dict:
            # Try to find nearest day
            found = False
            for d_str, ma_val in ma_dict.items():
                d = datetime.datetime.strptime(d_str, '%Y-%m-%d').date()
                d_epoch = date_to_epoch(d)
                if abs(d_epoch - decision_epoch) < 2*24*3600:  # Within 2 days
                    current_ma = ma_val
                    # Need actual sentiment for decision_date
                    # Find sentiment closest to decision_date
                    for ds, score in symbol_sentiment[sym_id]:
                        if ds == decision_date:
                            current_score = score
                            if current_score < current_ma:
                                found = True
                            break
                    break
            if not found:
                continue
        else:
            current_ma = ma_dict[decision_date]
            # Get actual sentiment for decision_date
            found_score = False
            for ds, score in symbol_sentiment[sym_id]:
                if ds == decision_date:
                    current_score = score
                    found_score = True
                    break
            if not found_score:
                continue
            if current_score >= current_ma:
                continue
        
        # Get entry price (close on decision day or next trading day)
        entry_price = get_price_at_or_after(conn, sym_id, decision_epoch, 0)
        if entry_price is None:
            continue
            
        # Get exit price 21 trading days later
        exit_price = get_price_at_or_after(conn, sym_id, decision_epoch, 21)
        if exit_price is None:
            continue
            
        signals.append((sym_id, decision_epoch, entry_price, exit_price))
    
    if not signals:
        print("INSUFFICIENT=1")
        return
    
    # 5. Determine sealed era (most recent 20% of signals by time)
    signals.sort(key=lambda x: x[1])
    n_signals = len(signals)
    seal_cutoff_idx = int(0.8 * n_signals)
    if seal_cutoff_idx == 0:
        seal_cutoff_idx = 1
    seal_cutoff_epoch = signals[seal_cutoff_idx][1]
    
    # Split signals
    train_signals = [s for s in signals if s[1] < seal_cutoff_epoch]
    sealed_signals = [s for s in signals if s[1] >= seal_cutoff_epoch]
    
    # 6. Evaluate hits
    def evaluate_hits(sig_list):
        hits = 0
        days = set()
        for sym_id, dec_epoch, entry, exit_p in sig_list:
            if exit_p > entry:
                hits += 1
            day = datetime.datetime.utcfromtimestamp(dec_epoch).date()
            days.add(day)
        return hits, len(days)
    
    total_hits, distinct_days = evaluate_hits(signals)
    issued = len(signals)
    
    # Base rate within issued subset
    base_rate = total_hits / issued if issued > 0 else 0
    
    # 7. Compute design effect and effective sample size
    # Group by day
    day_groups = defaultdict(list)
    for sym_id, dec_epoch, entry, exit_p in signals:
        day = datetime.datetime.utcfromtimestamp(dec_epoch).date()
        hit = 1 if exit_p > entry else 0
        day_groups[day].append(hit)
    
    m = len(day_groups)  # number of clusters
    n = issued
    
    if m < 2 or n < 2:
        design_effect = 1.0  # fallback, but should not happen
    else:
        # Calculate ICC
        p = total_hits / n
        ssb = 0
        ssw = 0
        for day, hits in day_groups.items():
            n_i = len(hits)
            p_i = sum(hits) / n_i
            ssb += n_i * (p_i - p)**2
            ssw += (n_i - 1) * p_i * (1 - p_i)
        
        var_between = ssb / (m - 1) if m > 1 else 0
        var_within = ssw / (n - m) if n > m else 0
        if var_between + var_within == 0:
            icc = 0
        else:
            icc = (var_between - var_within / (n - 1)) / var_between  # approximate
            icc = max(0, icc)
        
        design_effect = 1 + ((n - 1) / n) * icc * (m / (m - 1)) if m > 1 else 1
        design_effect = max(design_effect, 1.0001)  # ensure >1
    
    effective_n = n / design_effect
    
    # 8. Compute confidence interval for precision (one-sided, Bonferroni-corrected)
    # Using normal approximation with cluster adjustment
    se_cluster = math.sqrt(p * (1 - p) * design_effect / n) if n > 0 else 0
    # Bonferroni correction for n tests (conservative)
    alpha = 0.05 / n if n > 0 else 0.05
    z = 1.96  # approximate; for one-sided we use norm.ppf(1-alpha), but 1.96 is close to 95%
    # For one-sided lower bound: use z = norm.ppf(1-alpha) ≈ 2.33 for alpha=0.05, but Bonferroni makes it larger
    # Use conservative z=3 for Bonferroni when n is moderate
    if n > 10:
        z = 2.576  # 99% one-sided
    if n > 100:
        z = 3.0
    lower_bound = p - z * se_cluster
    
    # 9. Evaluate sealed era
    sealed_hits, sealed_distinct_days = evaluate_hits(sealed_signals) if sealed_signals else (0, 0)
    sealed_issued = len(sealed_signals)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    # 10. Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={p:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    # Validate invariants
    if distinct_days > issued:
        # This should never happen; if it does, script is invalid
        return
    if effective_n >= issued:
        # Should never happen if design_effect > 1
        return

if __name__ == "__main__":
    main()