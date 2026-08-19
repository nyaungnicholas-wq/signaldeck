# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 643
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all symbols with insider trades
    cur.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades
        WHERE code = 'S'
    """)
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]
    
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return 0

    # Get all open-market sales (code='S') with their filing delays
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT 
            it.symbol_id,
            it.tx_ts,
            it.filed_ts,
            (it.filed_ts - it.tx_ts) / 86400.0 as delay_days
        FROM insider_trades it
        WHERE it.code = 'S'
        AND it.symbol_id IN ({placeholders})
        ORDER BY it.symbol_id, it.filed_ts
    """, symbol_ids)
    
    sales = cur.fetchall()
    if not sales:
        print("INSUFFICIENT=1")
        return 0

    # Group by symbol
    from collections import defaultdict
    sales_by_symbol = defaultdict(list)
    for s in sales:
        sales_by_symbol[s['symbol_id']].append(s)

    # Get prediction outcomes for labels (horizon=21 trading days ~ 30 calendar days)
    # We need fwd_return for 21-day horizon
    cur.execute("""
        SELECT symbol_id, ts, fwd_return, up
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    outcomes = cur.fetchall()
    outcomes_by_symbol = defaultdict(list)
    for o in outcomes:
        outcomes_by_symbol[o['symbol_id']].append(o)

    if not outcomes:
        print("INSUFFICIENT=1")
        return 0

    # For each symbol, compute rolling median delay over prior 252 trading days
    # and check entry conditions
    calls = []  # (decision_ts, symbol_id, label_ts, label_up, label_fwd)
    
    for symbol_id, sym_sales in sales_by_symbol.items():
        sym_outcomes = outcomes_by_symbol.get(symbol_id, [])
        if not sym_outcomes:
            continue
            
        # Sort outcomes by ts
        sym_outcomes.sort(key=lambda x: x['ts'])
        
        # For each sale, check if it qualifies
        for i, sale in enumerate(sym_sales):
            filed_ts = sale['filed_ts']
            tx_ts = sale['tx_ts']
            delay = sale['delay_days']
            
            # Check trade date within prior 10 sessions (10 trading days ~ 14 calendar days)
            if filed_ts - tx_ts > 14 * 86400:
                continue
            
            # Get prior sales for median delay baseline (252 trading days ~ 365 calendar days)
            baseline_start = filed_ts - 365 * 86400
            prior_delays = [
                s['delay_days'] for s in sym_sales[:i]
                if s['filed_ts'] >= baseline_start
            ]
            
            if len(prior_delays) < 20:
                continue
            
            prior_delays.sort()
            median_delay = prior_delays[len(prior_delays) // 2]
            
            # Check if delay exceeds median by 3+ days
            if delay < median_delay + 3:
                continue
            
            # Find the outcome for this decision point
            # Decision is at filed_ts, label is 21 trading days forward
            # Find outcome with ts closest to filed_ts (decision time)
            best_outcome = None
            best_diff = float('inf')
            for o in sym_outcomes:
                diff = abs(o['ts'] - filed_ts)
                if diff < best_diff:
                    best_diff = diff
                    best_outcome = o
            
            if best_outcome and best_diff < 86400:  # within 1 day
                calls.append({
                    'decision_ts': filed_ts,
                    'symbol_id': symbol_id,
                    'label_up': best_outcome['up'],
                    'label_fwd': best_outcome['fwd_return']
                })

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # Sort calls by decision_ts
    calls.sort(key=lambda x: x['decision_ts'])
    
    # Hold out most recent 20% as sealed era
    n_calls = len(calls)
    split_idx = int(n_calls * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]
    
    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        # Down call: predict down (up=0)
        hits = sum(1 for c in call_list if c['label_up'] == 0)
        precision = hits / issued if issued > 0 else 0.0
        # Base rate of down class within issued subset
        base_rate = sum(1 for c in call_list if c['label_up'] == 0) / issued if issued > 0 else 0.0
        # Distinct UTC days
        distinct_days = len(set(
            datetime.utcfromtimestamp(c['decision_ts']).date() 
            for c in call_list
        ))
        # Design effect: cluster by day, compute effective N
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use intraclass correlation approximation
        day_counts = defaultdict(int)
        for c in call_list:
            day = datetime.utcfromtimestamp(c['decision_ts']).date()
            day_counts[day] += 1
        if len(day_counts) > 1:
            avg_cluster = issued / len(day_counts)
            # Conservative rho estimate for financial returns
            rho = 0.1
            design_effect = 1 + (avg_cluster - 1) * rho
            effective_n = issued / design_effect
        else:
            effective_n = 1.0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Opportunities: each (symbol, day) where we could have issued a call
    # Count decision points considered (each sale filing that met basic criteria)
    opportunities = 0
    for symbol_id, sym_sales in sales_by_symbol.items():
        for i, sale in enumerate(sym_sales):
            filed_ts = sale['filed_ts']
            tx_ts = sale['tx_ts']
            if filed_ts - tx_ts > 14 * 86400:
                continue
            baseline_start = filed_ts - 365 * 86400
            prior_delays = [
                s['delay_days'] for s in sym_sales[:i]
                if s['filed_ts'] >= baseline_start
            ]
            if len(prior_delays) >= 20:
                opportunities += 1

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == "__main__":
    sys.exit(main())