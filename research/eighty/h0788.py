# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 787
# cycle_index: 57
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def quarter_end_to_dt(qend_str):
    return datetime.strptime(qend_str, '%Y-%m-%d')

def dt_to_unix(dt):
    return int(dt.timestamp())

def unix_to_dt(ts):
    return datetime.utcfromtimestamp(ts)

def add_days(dt, days):
    return dt + timedelta(days=days)

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all 13F periods and aggregate institutional shares per symbol per quarter
    cur.execute("""
        SELECT symbol_id, period, SUM(shares) as total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    rows = cur.fetchall()

    # Organize by symbol
    from collections import defaultdict
    symbol_quarters = defaultdict(list)
    for r in rows:
        symbol_quarters[r['symbol_id']].append({
            'period': r['period'],
            'period_dt': quarter_end_to_dt(r['period']),
            'shares': r['total_shares']
        })

    # 2. Get news article counts per symbol per quarter
    cur.execute("""
        SELECT symbol_id, ts
        FROM news
    """)
    news_rows = cur.fetchall()

    symbol_news_quarters = defaultdict(lambda: defaultdict(int))
    for r in news_rows:
        dt = unix_to_dt(r['ts'])
        # Determine quarter end for this news date
        month = dt.month
        if month <= 3:
            qend = datetime(dt.year, 3, 31)
        elif month <= 6:
            qend = datetime(dt.year, 6, 30)
        elif month <= 9:
            qend = datetime(dt.year, 9, 30)
        else:
            qend = datetime(dt.year, 12, 31)
        qkey = qend.strftime('%Y-%m-%d')
        symbol_news_quarters[r['symbol_id']][qkey] += 1

    # 3. Get Form 144 filings per symbol per quarter
    cur.execute("""
        SELECT symbol_id, filed_ts
        FROM filings
        WHERE form = '144'
    """)
    filing_rows = cur.fetchall()

    symbol_144_quarters = defaultdict(lambda: defaultdict(int))
    for r in filing_rows:
        dt = unix_to_dt(r['filed_ts'])
        month = dt.month
        if month <= 3:
            qend = datetime(dt.year, 3, 31)
        elif month <= 6:
            qend = datetime(dt.year, 6, 30)
        elif month <= 9:
            qend = datetime(dt.year, 9, 30)
        else:
            qend = datetime(dt.year, 12, 31)
        qkey = qend.strftime('%Y-%m-%d')
        symbol_144_quarters[r['symbol_id']][qkey] += 1

    # 4. Build decision points
    decisions = []  # (symbol_id, decision_dt, label_dt, forward_return)
    
    for sym_id, quarters in symbol_quarters.items():
        if len(quarters) < 4:
            continue
        quarters.sort(key=lambda x: x['period_dt'])
        
        # Compute quarter-over-quarter share changes
        for i in range(2, len(quarters)):
            q0 = quarters[i-2]
            q1 = quarters[i-1]
            q2 = quarters[i]
            
            # Check 2+ consecutive quarters of institutional accumulation
            if q1['shares'] > q0['shares'] and q2['shares'] > q1['shares']:
                # Decision date = q2 period end + 45 days (13F filing lag)
                decision_dt = add_days(q2['period_dt'], 45)
                
                # Check news coverage in prior 4 quarters (q2, q1, q0, q-1)
                news_ok = True
                for q in [q2, q1, q0]:
                    qkey = q['period_dt'].strftime('%Y-%m-%d')
                    if symbol_news_quarters[sym_id].get(qkey, 0) > 0:
                        news_ok = False
                        break
                # Also check quarter before q0
                if i-3 >= 0:
                    qm1 = quarters[i-3]
                    qkey = qm1['period_dt'].strftime('%Y-%m-%d')
                    if symbol_news_quarters[sym_id].get(qkey, 0) > 0:
                        news_ok = False
                
                # Check no Form 144 in prior quarter (q2)
                q2_key = q2['period_dt'].strftime('%Y-%m-%d')
                if symbol_144_quarters[sym_id].get(q2_key, 0) > 0:
                    news_ok = False
                
                if not news_ok:
                    continue
                
                # Get 63-day forward return from bars (tf='1d')
                decision_ts = dt_to_unix(decision_dt)
                label_dt = add_days(decision_dt, 63)
                label_ts = dt_to_unix(label_dt)
                
                cur.execute("""
                    SELECT close FROM bars
                    WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
                    ORDER BY ts
                    LIMIT 2
                """, (sym_id, decision_ts, label_ts))
                bar_rows = cur.fetchall()
                
                if len(bar_rows) < 2:
                    continue
                
                entry_px = bar_rows[0]['close']
                exit_px = bar_rows[-1]['close']
                fwd_return = (exit_px - entry_px) / entry_px
                label = 1 if fwd_return > 0 else 0
                
                decisions.append({
                    'symbol_id': sym_id,
                    'decision_dt': decision_dt,
                    'decision_ts': decision_ts,
                    'label': label,
                    'fwd_return': fwd_return
                })

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    decisions.sort(key=lambda x: x['decision_ts'])
    
    # Hold out most recent 20% as sealed era
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_decisions = decisions[:n_train]
    sealed_decisions = decisions[n_train:]

    # Compute metrics on training set (for threshold/claim validation)
    # But we need to report on FULL set and SEALED separately per spec
    # The spec says: "Hold out the most recent 20% as a sealed era and report it separately"
    # And print SEALED_PRECISION
    
    # For the hypothesis test, we issue calls on ALL decision points that meet criteria
    # Then compute metrics on full and sealed
    
    all_issued = [d for d in decisions if d['label'] == 1]  # We "call" positive when label=1? No.
    # Wait - the hypothesis is a PREDICTION. We need to define when we ISSUE a call.
    # The ENTRY condition defines when we issue a call. The label is the outcome.
    # So every decision point above IS an issued call (we entered the trade).
    # The label tells us if it was correct.
    
    issued = decisions  # All decision points meeting entry criteria are issued calls
    n_issued = len(issued)
    n_hits = sum(1 for d in issued if d['label'] == 1)
    precision = n_hits / n_issued if n_issued > 0 else 0
    
    # Base rate within issued subset
    base_rate = n_hits / n_issued if n_issued > 0 else 0
    
    # Distinct days among issued calls
    distinct_days = len(set(d['decision_dt'].date() for d in issued))
    
    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: cluster by week, compute design effect
    from collections import Counter
    week_counts = Counter()
    for d in issued:
        week_key = d['decision_dt'].strftime('%Y-W%U')
        week_counts[week_key] += 1
    if week_counts:
        avg_cluster = sum(week_counts.values()) / len(week_counts)
        # Conservative ICC estimate for financial returns ~0.1
        icc = 0.1
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = n_issued / design_effect
    else:
        effective_n = n_issued
    
    # Sealed era precision
    sealed_issued = [d for d in sealed_decisions]
    n_sealed_issued = len(sealed_issued)
    n_sealed_hits = sum(1 for d in sealed_issued if d['label'] == 1)
    sealed_precision = n_sealed_hits / n_sealed_issued if n_sealed_issued > 0 else 0
    
    # Opportunities = total decision points considered (before entry filter)
    # We need to count all (symbol, quarter) where we could have decided
    opportunities = 0
    for sym_id, quarters in symbol_quarters.items():
        if len(quarters) < 4:
            continue
        opportunities += max(0, len(quarters) - 3)  # Each quarter from index 2 onward is a decision point
    
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()