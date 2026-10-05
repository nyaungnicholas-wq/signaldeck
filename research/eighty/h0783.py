# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 782
# cycle_index: 52
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day, tzinfo=timezone.utc).timestamp())

def get_trading_days(cur, start_ts, end_ts):
    cur.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as d
        FROM bars
        WHERE tf='1d' AND ts >= ? AND ts <= ?
        ORDER BY d
    """, (start_ts, end_ts))
    return [row['d'] for row in cur.fetchall()]

def forward_return_21d(cur, symbol_id, decision_ts):
    """Compute 21-trading-day forward return from decision_ts using 1d bars.
    Decision is at filed_ts (public disclosure). Use next trading day's open? 
    Standard: return from close on decision day to close 21 trading days later.
    But decision_ts may be after market close. Use next trading day's close to close+21.
    """
    cur.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id=? AND tf='1d' AND ts >= ?
        ORDER BY ts LIMIT 22
    """, (symbol_id, decision_ts))
    rows = cur.fetchall()
    if len(rows) < 22:
        return None
    entry_close = rows[0]['close']
    exit_close = rows[21]['close']
    if entry_close == 0:
        return None
    return (exit_close - entry_close) / entry_close

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return ('CEO' in t or 'CHIEF EXECUTIVE' in t or 
            'CFO' in t or 'CHIEF FINANCIAL' in t)

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all officer open-market purchases (code='P') with filed_ts
    cur.execute("""
        SELECT it.symbol_id, it.insider, it.title, it.shares, it.price, 
               it.tx_ts, it.filed_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P' AND it.filed_ts IS NOT NULL
        ORDER BY it.filed_ts
    """)
    all_trades = cur.fetchall()
    if not all_trades:
        print("INSUFFICIENT=1")
        return 0

    # Build track record for each officer: for each trade, look at prior 3 years of same officer's purchases
    # We'll process chronologically and maintain history
    officer_history = {}  # (symbol_id, insider) -> list of (filed_ts, fwd_return_21d)
    trade_records = []    # each: dict with trade info + track_record_positive_count, track_record_total

    for row in all_trades:
        key = (row['symbol_id'], row['insider'])
        filed_ts = row['filed_ts']
        
        # Compute track record from history BEFORE this trade
        prior = officer_history.get(key, [])
        # Filter to last 3 years (756 trading days ~ 3*252)
        cutoff = filed_ts - 3 * 365 * 86400
        recent_prior = [r for r in prior if r[0] >= cutoff]
        
        pos_count = sum(1 for r in recent_prior if r[1] is not None and r[1] > 0)
        total_count = sum(1 for r in recent_prior if r[1] is not None)
        
        # Compute forward return for THIS trade (for future track records)
        fwd = forward_return_21d(cur, row['symbol_id'], filed_ts)
        
        # Record this trade's track record status
        trade_records.append({
            'symbol_id': row['symbol_id'],
            'symbol': row['symbol'],
            'insider': row['insider'],
            'title': row['title'],
            'filed_ts': filed_ts,
            'fwd_return': fwd,
            'track_pos': pos_count,
            'track_total': total_count,
            'is_officer': is_officer(row['title'])
        })
        
        # Update history
        if fwd is not None:
            officer_history.setdefault(key, []).append((filed_ts, fwd))

    # Filter: officer trades with >=3 prior tracked trades and 100% positive track record
    candidates = [t for t in trade_records 
                  if t['is_officer'] and t['track_total'] >= 3 and t['track_pos'] == t['track_total'] and t['fwd_return'] is not None]
    
    if not candidates:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filed_ts
    candidates.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    n = len(candidates)
    split_idx = int(n * 0.8)
    train = candidates[:split_idx]
    sealed = candidates[split_idx:]

    def evaluate(trades):
        if not trades:
            return 0, 0, 0, 0, 0
        issued = [t for t in trades if t['fwd_return'] > 0]  # predicted up
        # Actually, the hypothesis IS the call: we issue a call for every candidate
        # The prediction is "up" (positive return). Hit if fwd_return > 0.
        issued_count = len(trades)
        hits = sum(1 for t in trades if t['fwd_return'] > 0)
        precision = hits / issued_count if issued_count else 0
        base_rate = hits / issued_count if issued_count else 0  # base rate of positive class in issued subset
        distinct_days = len(set(epoch_to_date(t['filed_ts']) for t in trades))
        return issued_count, hits, precision, base_rate, distinct_days

    # Full sample
    issued_full, hits_full, prec_full, base_full, days_full = evaluate(candidates)
    # Train era
    issued_tr, hits_tr, prec_tr, base_tr, days_tr = evaluate(train)
    # Sealed era
    issued_se, hits_se, prec_se, base_se, days_se = evaluate(sealed)

    # Design effect: cluster by (symbol, UTC day)
    # For each issued call, compute cluster size
    from collections import Counter
    cluster_counts = Counter((t['symbol_id'], epoch_to_date(t['filed_ts'])) for t in candidates)
    # Design effect = 1 + (avg cluster size - 1) * ICC
    # Simplified: effective_n = issued / (1 + (mean_cluster_size - 1))
    # But standard approach: deff = 1 + (m-1)*rho, assume rho=0.5 for conservative
    # Use Kish's effective sample size: n_eff = (sum w)^2 / sum w^2 where w=1/cluster_size
    total_weight = sum(1.0 / cluster_counts[(t['symbol_id'], epoch_to_date(t['filed_ts']))] for t in candidates)
    effective_n = total_weight  # Kish formula

    print(f"ISSUED={issued_full}")
    print(f"OPPORTUNITIES={n}")
    print(f"PRECISION={prec_full:.6f}")
    print(f"BASE_RATE={base_full:.6f}")
    print(f"DISTINCT_DAYS={days_full}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={prec_se:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())