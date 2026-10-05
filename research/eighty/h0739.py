# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 738
# cycle_index: 8
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def next_trading_day(conn, symbol_id, ts):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>? ORDER BY ts LIMIT 1",
        (symbol_id, ts)
    )
    row = cur.fetchone()
    return row[0] if row else None

def nth_trading_day_after(conn, symbol_id, start_ts, n):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT ? OFFSET ?",
        (symbol_id, start_ts, n + 1, n)
    )
    row = cur.fetchone()
    return row[0] if row else None

def get_open(conn, symbol_id, ts):
    cur = conn.execute(
        "SELECT open FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, ts)
    )
    row = cur.fetchone()
    return row[0] if row else None

def get_close(conn, symbol_id, ts):
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, ts)
    )
    row = cur.fetchone()
    return row[0] if row else None

def avg_dollar_volume(conn, symbol_id, ts, lookback_sessions=20):
    cur = conn.execute(
        "SELECT close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT ?",
        (symbol_id, ts, lookback_sessions)
    )
    rows = cur.fetchall()
    if len(rows) < lookback_sessions // 2:
        return 0
    total = sum(c * v for c, v in rows if c and v)
    return total / len(rows)

def has_prior_424b2(conn, symbol_id, filed_ts, window_days=730):
    window_sec = window_days * 86400
    cur = conn.execute(
        "SELECT 1 FROM filings WHERE symbol_id=? AND form='424B2' AND filed_ts>? AND filed_ts<? LIMIT 1",
        (symbol_id, filed_ts - window_sec, filed_ts)
    )
    return cur.fetchone() is not None

def has_officer_sale(conn, symbol_id, filed_ts, window_days=63):
    window_sec = window_days * 86400
    cur = conn.execute(
        """SELECT 1 FROM insider_trades 
           WHERE symbol_id=? AND code='S' AND filed_ts>? AND filed_ts<=?
           AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%PRESIDENT%' OR title LIKE '%CHIEF%')
           LIMIT 1""",
        (symbol_id, filed_ts - window_sec, filed_ts)
    )
    return cur.fetchone() is not None

def find_officer_purchase_after(conn, symbol_id, after_ts, window_days=14):
    window_sec = window_days * 86400
    cur = conn.execute(
        """SELECT filed_ts FROM insider_trades 
           WHERE symbol_id=? AND code='P' AND filed_ts>? AND filed_ts<=?
           AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%PRESIDENT%' OR title LIKE '%CHIEF%')
           ORDER BY filed_ts LIMIT 1""",
        (symbol_id, after_ts, after_ts + window_sec)
    )
    row = cur.fetchone()
    return row[0] if row else None

def is_active_stock(conn, symbol_id, decision_ts):
    cur = conn.execute(
        "SELECT market, active, delisted_at FROM symbols WHERE id=?",
        (symbol_id,)
    )
    row = cur.fetchone()
    if not row:
        return False
    market, active, delisted_at = row
    if market != 'stocks' or active != 1:
        return False
    if delisted_at and delisted_at <= decision_ts:
        return False
    return True

def has_sufficient_bars(conn, symbol_id, decision_ts, min_sessions=252):
    cur = conn.execute(
        "SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND ts<?",
        (symbol_id, decision_ts)
    )
    count = cur.fetchone()[0]
    return count >= min_sessions

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row

    # Find all 424B2 filings
    cur = conn.execute(
        "SELECT symbol_id, filed_ts FROM filings WHERE form='424B2' ORDER BY symbol_id, filed_ts"
    )
    filings_424b2 = cur.fetchall()

    entries = []
    for filing in filings_424b2:
        symbol_id = filing['symbol_id']
        filing_ts = filing['filed_ts']

        # Check if first 424B2 in 730 days
        if has_prior_424b2(conn, symbol_id, filing_ts, 730):
            continue

        # Check universe constraints at filing time (approx)
        if not is_active_stock(conn, symbol_id, filing_ts):
            continue
        if not has_sufficient_bars(conn, symbol_id, filing_ts, 252):
            continue

        # Find officer purchase within ~10 trading days (14 calendar days)
        purchase_ts = find_officer_purchase_after(conn, symbol_id, filing_ts, 14)
        if not purchase_ts:
            continue

        # Check abstain conditions at purchase time
        if has_officer_sale(conn, symbol_id, purchase_ts, 63):
            continue

        # Entry at next trading day open after purchase filing
        entry_ts = next_trading_day(conn, symbol_id, purchase_ts)
        if not entry_ts:
            continue

        # Check price and volume at entry
        entry_open = get_open(conn, symbol_id, entry_ts)
        if not entry_open or entry_open < 5:
            continue

        if avg_dollar_volume(conn, symbol_id, entry_ts, 20) < 1_000_000:
            continue

        # Exit at 21 trading sessions later close
        exit_ts = nth_trading_day_after(conn, symbol_id, entry_ts, 21)
        if not exit_ts:
            continue

        exit_close = get_close(conn, symbol_id, exit_ts)
        if not exit_close:
            continue

        ret = (exit_close - entry_open) / entry_open
        hit = 1 if ret > 0 else 0

        entries.append({
            'symbol_id': symbol_id,
            'entry_ts': entry_ts,
            'purchase_ts': purchase_ts,
            'filing_ts': filing_ts,
            'hit': hit,
            'return': ret
        })

    if not entries:
        print("INSUFFICIENT=1")
        return 0

    # Sort by entry_ts
    entries.sort(key=lambda x: x['entry_ts'])

    # Hold out most recent 20% as sealed era
    n_total = len(entries)
    n_sealed = max(1, n_total // 5)
    in_sample = entries[:-n_sealed]
    sealed = entries[-n_sealed:]

    def compute_metrics(entries_list):
        if not entries_list:
            return 0, 0, 0, 0, 0
        issued = len(entries_list)
        hits = sum(e['hit'] for e in entries_list)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate of positive class in issued
        distinct_days = len(set(datetime.utcfromtimestamp(e['entry_ts']).date() for e in entries_list))
        # Design effect approximation
        avg_cluster = issued / distinct_days if distinct_days else 1
        icc = 0.2
        deff = 1 + (avg_cluster - 1) * icc
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(in_sample)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed)

    # Opportunities: count of qualifying 424B2 filings that passed universe checks
    # (simplified: count of 424B2 that were first in 730d and passed universe)
    opportunities = 0
    for filing in filings_424b2:
        symbol_id = filing['symbol_id']
        filing_ts = filing['filed_ts']
        if has_prior_424b2(conn, symbol_id, filing_ts, 730):
            continue
        if not is_active_stock(conn, symbol_id, filing_ts):
            continue
        if not has_sufficient_bars(conn, symbol_id, filing_ts, 252):
            continue
        opportunities += 1

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())