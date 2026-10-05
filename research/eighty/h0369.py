# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 368
# cycle_index: 36
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON_DAYS = 21
OWNERSHIP_INCREASE_PCT = 5.0

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()
    
    # Check for sufficient data: need inst_holdings and insider_trades
    try:
        cursor.execute("SELECT COUNT(*) FROM inst_holdings")
        ih_count = cursor.fetchone()[0]
        cursor.execute("SELECT COUNT(*) FROM insider_trades")
        it_count = cursor.fetchone()[0]
        if ih_count == 0 or it_count == 0:
            print("INSUFFICIENT=1")
            return
    except sqlite3.OperationalError:
        print("INSUFFICIENT=1")
        return

    # Get all symbols with both data types
    symbols_with_13f = set()
    cursor.execute("SELECT DISTINCT symbol_id FROM inst_holdings")
    for row in cursor.fetchall():
        symbols_with_13f.add(row[0])
    
    symbols_with_insider = set()
    cursor.execute("SELECT DISTINCT symbol_id FROM insider_trades WHERE code = 'P'")
    for row in cursor.fetchall():
        symbols_with_insider.add(row[0])
    
    common_symbols = symbols_with_13f.intersection(symbols_with_insider)
    if not common_symbols:
        print("INSUFFICIENT=1")
        return

    # Process each symbol to find qualifying quarters
    opportunities = []  # list of (symbol_id, decision_ts, entry_date)
    
    for symbol_id in common_symbols:
        # Get 13F data: group by period (quarter end)
        cursor.execute("""
            SELECT period, SUM(value) as total_value
            FROM inst_holdings
            WHERE symbol_id = ?
            GROUP BY period
            ORDER BY period
        """, (symbol_id,))
        periods = cursor.fetchall()
        
        if len(periods) < 2:
            continue
        
        # Calculate quarterly changes in ownership value (proxy for shares)
        # We need shares outstanding to compute percentage, but fundamentals might not be available
        # Use value as proxy: large increase from small base
        quarterly_changes = []
        for i in range(1, len(periods)):
            prev_period = periods[i-1][0]
            curr_period = periods[i][0]
            prev_value = periods[i-1][1]
            curr_value = periods[i][1]
            
            if prev_value is None or curr_value is None:
                continue
            
            # Calculate percentage increase
            if prev_value == 0:
                pct_change = 100.0 if curr_value > 0 else 0.0
            else:
                pct_change = ((curr_value - prev_value) / prev_value) * 100
            
            quarterly_changes.append((prev_period, curr_period, pct_change))
        
        if not quarterly_changes:
            continue
        
        # Find top decile threshold
        changes = [abs(c[2]) for c in quarterly_changes]
        changes.sort()
        top_decile_idx = max(0, int(len(changes) * 0.9))
        threshold = changes[top_decile_idx] if changes else 0
        
        # Find insider purchases in same calendar quarter
        cursor.execute("""
            SELECT filed_ts, tx_ts
            FROM insider_trades
            WHERE symbol_id = ? AND code = 'P'
            ORDER BY filed_ts
        """, (symbol_id,))
        insider_trades = cursor.fetchall()
        
        # Group insider trades by calendar quarter (based on filed_ts)
        insider_quarters = defaultdict(list)
        for trade in insider_trades:
            if trade[0] is None:
                continue
            # filed_ts is knowable, use it for as-of discipline
            trade_date = trade[0]
            # Extract year and quarter from timestamp
            # Assuming timestamp is Unix epoch
            from datetime import datetime
            try:
                trade_dt = datetime.utcfromtimestamp(trade_date)
                quarter_key = (trade_dt.year, (trade_dt.month - 1) // 3 + 1)
                insider_quarters[quarter_key].append(trade_date)
            except (ValueError, OSError):
                continue
        
        # Match 13F quarters with insider purchase quarters
        for prev_period, curr_period, pct_change in quarterly_changes:
            # Check if increase meets threshold
            if pct_change < OWNERSHIP_INCREASE_PCT:
                continue
            
            # Check if in top decile
            if abs(pct_change) < threshold:
                continue
            
            # Convert 13F period to datetime
            try:
                # Assuming period is timestamp like 1719792000 (2024-07-01)
                period_dt = datetime.utcfromtimestamp(curr_period)
                quarter_key = (period_dt.year, (period_dt.month - 1) // 3 + 1)
            except (ValueError, OSError):
                continue
            
            # Check if there were insider purchases in same calendar quarter
            if quarter_key in insider_quarters and insider_quarters[quarter_key]:
                # Decision time: latest of 13F filing (period + 45 days) and insider filing
                # We don't have exact 13F filing date, use period + 45 days as proxy
                from datetime import timedelta
                min_decision_ts = curr_period + (45 * 24 * 3600)  # 45 days in seconds
                
                # Find latest insider filing in same quarter
                latest_insider = max(insider_quarters[quarter_key])
                decision_ts = max(min_decision_ts, latest_insider)
                
                opportunities.append((symbol_id, decision_ts, curr_period))

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Sort opportunities by decision time
    opportunities.sort(key=lambda x: x[1])
    
    # Determine sealed era cutoff (most recent 20%)
    n_total = len(opportunities)
    sealed_cutoff_idx = int(n_total * 0.8)
    sealed_era_start = opportunities[sealed_cutoff_idx][1] if sealed_cutoff_idx < n_total else float('inf')
    
    # Get labels from prediction_outcomes
    issued = []
    base_rate_counts = []
    
    for symbol_id, decision_ts, entry_ts in opportunities:
        # Find prediction outcomes for this symbol with horizon 21 days
        # Decision time must be before label window
        cursor.execute("""
            SELECT up, ts
            FROM prediction_outcomes
            WHERE symbol_id = ? AND horizon = ?
            AND ts >= ?
            ORDER BY ts
            LIMIT 1
        """, (symbol_id, HORIZON_DAYS, decision_ts))
        
        row = cursor.fetchone()
        if row is None:
            continue
        
        label_up = row[0]
        label_ts = row[1]
        
        # Ensure we're not using lookahead: decision must be before label
        if decision_ts >= label_ts:
            continue
        
        issued.append({
            'symbol_id': symbol_id,
            'decision_ts': decision_ts,
            'label_up': label_up,
            'in_sealed': decision_ts >= sealed_era_start
        })
        
        if label_up is not None:
            base_rate_counts.append(label_up)
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # Calculate metrics
    total_issued = len(issued)
    total_opportunities = len(opportunities)
    hits = sum(1 for item in issued if item['label_up'] == 1)
    precision = hits / total_issued if total_issued > 0 else 0.0
    
    # Base rate within issued subset
    base_rate = sum(base_rate_counts) / len(base_rate_counts) if base_rate_counts else 0.0
    
    # Distinct days among issued calls
    distinct_days = set()
    for item in issued:
        # Convert timestamp to day (UTC)
        from datetime import datetime
        dt = datetime.utcfromtimestamp(item['decision_ts'])
        distinct_days.add(dt.date())
    
    # Calculate design effect (assume clustering by day)
    day_counts = defaultdict(int)
    for item in issued:
        from datetime import datetime
        dt = datetime.utcfromtimestamp(item['decision_ts'])
        day_counts[dt.date()] += 1
    
    # Design effect = 1 + (variance of day counts / mean of day counts) * (n-1)/n
    if day_counts:
        counts = list(day_counts.values())
        mean_count = sum(counts) / len(counts)
        if mean_count > 0:
            variance = sum((x - mean_count) ** 2 for x in counts) / len(counts)
            design_effect = 1 + (variance / mean_count) * ((total_issued - 1) / total_issued) if total_issued > 1 else 1.0
        else:
            design_effect = 1.0
    else:
        design_effect = 1.0
    
    effective_n = total_issued / design_effect if design_effect > 0 else total_issued
    
    # Sealed era metrics
    sealed_issued = [item for item in issued if item['in_sealed']]
    sealed_hits = sum(1 for item in sealed_issued if item['label_up'] == 1)
    sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0.0
    
    # Verify invariants
    if len(distinct_days) > total_issued:
        print("INVARIANT_VIOLATION")
        return
    if effective_n >= total_issued:
        print("INVARIANT_VIOLATION")
        return
    
    # Output required lines
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={len(distinct_days)}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()