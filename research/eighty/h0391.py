# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 390
# cycle_index: 58
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_horizons(conn):
    cur = conn.execute("SELECT DISTINCT horizon FROM prediction_outcomes")
    return sorted([row[0] for row in cur.fetchall()])

def get_sealed_cutoff(conn):
    cur = conn.execute("SELECT MAX(ts) FROM prediction_outcomes")
    max_ts = cur.fetchone()[0]
    cur = conn.execute("SELECT MIN(ts) FROM prediction_outcomes")
    min_ts = cur.fetchone()[0]
    span = max_ts - min_ts
    cutoff = max_ts - int(span * 0.2)
    return cutoff, min_ts, max_ts

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    horizons = get_horizons(conn)
    if not horizons:
        print("INSUFFICIENT=1")
        return 0
    
    horizon = 21
    if horizon not in horizons:
        horizon = horizons[len(horizons)//2]
    
    sealed_cutoff, min_ts, max_ts = get_sealed_cutoff(conn)
    
    query = """
    SELECT 
        it.symbol_id,
        it.filed_ts as decision_ts,
        po.up as label,
        po.fwd_return
    FROM insider_trades it
    JOIN prediction_outcomes po ON po.symbol_id = it.symbol_id AND po.horizon = ? AND po.ts = it.filed_ts
    WHERE it.code = 'P'
      AND it.filed_ts IS NOT NULL
      AND po.resolved_at IS NOT NULL
    ORDER BY it.filed_ts
    """
    
    cur = conn.execute(query, (horizon,))
    rows = cur.fetchall()
    
    if len(rows) < 50:
        print("INSUFFICIENT=1")
        return 0
    
    in_sample = [r for r in rows if r['decision_ts'] < sealed_cutoff]
    sealed = [r for r in rows if r['decision_ts'] >= sealed_cutoff]
    
    def compute_metrics(data, label):
        if not data:
            return None
        issued = len(data)
        hits = sum(1 for r in data if r['label'] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0
        distinct_days = len(set(r['decision_ts'] // 86400 for r in data))
        design_effect = 1.0 + (issued / max(1, distinct_days) - 1) * 0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }
    
    in_metrics = compute_metrics(in_sample, 'in_sample')
    sealed_metrics = compute_metrics(sealed, 'sealed')
    
    if not in_metrics or in_metrics['issued'] == 0:
        print("INSUFFICIENT=1")
        return 0
    
    opportunities = in_metrics['issued']
    
    print(f"ISSUED={in_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={in_metrics['precision']:.4f}")
    print(f"BASE_RATE={in_metrics['base_rate']:.4f}")
    print(f"DISTINCT_DAYS={in_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={in_metrics['effective_n']:.2f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.4f}" if sealed_metrics else "SEALED_PRECISION=0.0000")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())