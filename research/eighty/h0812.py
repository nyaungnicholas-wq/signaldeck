# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 811
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def connect_db():
    return sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)

def get_symbols_with_bars(conn):
    """Get symbols with >=756 daily bars, market=stocks"""
    cur = conn.cursor()
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        JOIN (
            SELECT symbol_id, COUNT(*) as cnt
            FROM bars
            WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING cnt >= 756
        ) b ON s.id = b.symbol_id
        WHERE s.market = 'stocks'
    """)
    return cur.fetchall()

def get_ceo_cfo_purchases(conn):
    """Get CEO/CFO open-market purchases (code P) with filed_ts"""
    cur = conn.cursor()
    cur.execute("""
        SELECT symbol_id, filed_ts, accession
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
          AND filed_ts IS NOT NULL
        ORDER BY filed_ts
    """)
    return cur.fetchall()

def get_fundamentals(conn, symbol_id, as_of_ts):
    """Get quarterly fundamentals with fetched_at <= as_of_ts"""
    cur = conn.cursor()
    cur.execute("""
        SELECT metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE symbol_id = ? AND fetched_at <= ? AND as_of > 0
          AND metric IN ('Revenues', 'EPS', 'SharesOutstanding')
        ORDER BY as_of
    """, (symbol_id, as_of_ts))
    return cur.fetchall()

def get_bars_before(conn, symbol_id, ts, limit):
    """Get daily bars with ts <= given timestamp"""
    cur = conn.cursor()
    cur.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC
        LIMIT ?
    """, (symbol_id, ts, limit))
    return cur.fetchall()

def get_bars_after(conn, symbol_id, ts, limit):
    """Get daily bars with ts > given timestamp"""
    cur = conn.cursor()
    cur.execute("""
        SELECT ts, close
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts ASC
        LIMIT ?
    """, (symbol_id, ts, limit))
    return cur.fetchall()

def is_near_quarter_end(ts, bars_1d):
    """Check if timestamp is within 5 sessions of quarter end"""
    dt = datetime.utcfromtimestamp(ts)
    quarter_ends = [
        datetime(dt.year, 3, 31),
        datetime(dt.year, 6, 30),
        datetime(dt.year, 9, 30),
        datetime(dt.year, 12, 31)
    ]
    for qe in quarter_ends:
        qe_ts = int(qe.timestamp())
        sessions_near = sum(1 for b_ts, _ in bars_1d if abs(b_ts - qe_ts) < 86400 * 5)
        if sessions_near > 0:
            return True
    return False

def compute_ps_percentile(current_ps, historical_ps_list):
    """Compute percentile of current_ps in historical_ps_list"""
    if not historical_ps_list:
        return 100
    sorted_ps = sorted(historical_ps_list)
    n = len(sorted_ps)
    rank = sum(1 for v in sorted_ps if v <= current_ps)
    return (rank / n) * 100

def main():
    conn = connect_db()
    
    symbols = get_symbols_with_bars(conn)
    if not symbols:
        print("INSUFFICIENT=1")
        return 0
    
    symbol_ids = {s[0] for s in symbols}
    purchases = get_ceo_cfo_purchases(conn)
    purchases = [p for p in purchases if p[0] in symbol_ids]
    
    if not purchases:
        print("INSUFFICIENT=1")
        return 0
    
    calls = []
    opportunities = 0
    
    for symbol_id, filed_ts, accession in purchases:
        opportunities += 1
        
        bars_before = get_bars_before(conn, symbol_id, filed_ts, 756)
        if len(bars_before) < 756:
            continue
        
        if is_near_quarter_end(filed_ts, bars_before):
            continue
        
        fundamentals = get_fundamentals(conn, symbol_id, filed_ts)
        if not fundamentals:
            continue
        
        by_quarter = {}
        for metric, value, as_of, fetched_at in fundamentals:
            if as_of not in by_quarter:
                by_quarter[as_of] = {}
            by_quarter[as_of][metric] = value
        
        complete_quarters = []
        for as_of in sorted(by_quarter.keys()):
            q = by_quarter[as_of]
            if all(k in q for k in ('Revenues', 'EPS', 'SharesOutstanding')):
                complete_quarters.append((as_of, q['Revenues'], q['EPS'], q['SharesOutstanding']))
        
        if len(complete_quarters) < 3:
            continue
        
        latest_q = complete_quarters[-1]
        rev_latest, eps_latest, so_latest = latest_q[1], latest_q[2], latest_q[3]
        if rev_latest <= 0 or so_latest <= 0:
            continue
        
        close_ts, close_price = bars_before[0]
        ps_current = close_price / (rev_latest / so_latest)
        
        historical_ps = []
        for i, (b_ts, b_close) in enumerate(bars_before):
            q_idx = len(complete_quarters) - 1
            while q_idx >= 0 and complete_quarters[q_idx][0] > b_ts:
                q_idx -= 1
            if q_idx >= 0:
                r, e, s = complete_quarters[q_idx][1], complete_quarters[q_idx][2], complete_quarters[q_idx][3]
                if r > 0 and s > 0:
                    historical_ps.append(b_close / (r / s))
        
        if len(historical_ps) < 756:
            continue
        
        ps_pct = compute_ps_percentile(ps_current, historical_ps)
        if ps_pct > 5:
            continue
        
        rev_growth = []
        for i in range(1, len(complete_quarters)):
            prev_rev = complete_quarters[i-1][1]
            curr_rev = complete_quarters[i][1]
            if prev_rev > 0:
                rev_growth.append((curr_rev / prev_rev) - 1)
        
        if len(rev_growth) < 2:
            continue
        if not (rev_growth[-1] > rev_growth[-2] > 0):
            continue
        
        margins = []
        for as_of, rev, eps, so in complete_quarters:
            if rev > 0:
                margins.append((eps * so) / rev)
        
        if len(margins) < 3:
            continue
        if not (margins[-1] >= margins[-2] >= margins[-3]):
            continue
        
        bars_after = get_bars_after(conn, symbol_id, filed_ts, 63)
        if len(bars_after) < 63:
            continue
        
        entry_price = close_price
        exit_price = bars_after[-1][1]
        fwd_return = (exit_price / entry_price) - 1
        hit = 1 if fwd_return > 0 else 0
        
        calls.append({
            'symbol_id': symbol_id,
            'filed_ts': filed_ts,
            'hit': hit,
            'fwd_return': fwd_return
        })
    
    if not calls:
        print("INSUFFICIENT=1")
        return 0
    
    calls.sort(key=lambda x: x['filed_ts'])
    n_total = len(calls)
    n_sealed = max(1, int(n_total * 0.2))
    train_calls = calls[:-n_sealed]
    sealed_calls = calls[-n_sealed:]
    
    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0, 0
        issued = len(call_list)
        hits = sum(c['hit'] for c in call_list)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0
        distinct_days = len(set(datetime.utcfromtimestamp(c['filed_ts']).date() for c in call_list))
        return issued, hits, precision, base_rate, distinct_days
    
    issued_train, hits_train, prec_train, base_train, distinct_train = compute_metrics(train_calls)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed = compute_metrics(sealed_calls)
    
    issued_total = issued_train + issued_sealed
    hits_total = hits_train + hits_sealed
    precision_total = hits_total / issued_total if issued_total > 0 else 0
    base_rate_total = hits_total / issued_total if issued_total > 0 else 0
    distinct_total = len(set(datetime.utcfromtimestamp(c['filed_ts']).date() for c in calls))
    
    if distinct_total < issued_total:
        design_effect = issued_total / distinct_total
    else:
        design_effect = 1.01
    effective_n = issued_total / design_effect
    
    print(f"ISSUED={issued_total}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_total:.6f}")
    print(f"BASE_RATE={base_rate_total:.6f}")
    print(f"DISTINCT_DAYS={distinct_total}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())