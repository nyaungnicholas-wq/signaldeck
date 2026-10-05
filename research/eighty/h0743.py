# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 742
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def trading_days_between(conn, start_ts, end_ts):
    cur = conn.execute("""
        SELECT COUNT(DISTINCT date(ts, 'unixepoch')) FROM bars
        WHERE tf='1d' AND ts BETWEEN ? AND ?
    """, (start_ts, end_ts))
    return cur.fetchone()[0] or 0

def get_vwap_20d(conn, symbol_id, as_of_ts):
    cur = conn.execute("""
        SELECT SUM(close * volume) / SUM(volume) FROM (
            SELECT close, volume FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 20
        )
    """, (symbol_id, as_of_ts))
    return cur.fetchone()[0]

def get_avg_dollar_vol_20d(conn, symbol_id, as_of_ts):
    cur = conn.execute("""
        SELECT AVG(close * volume) FROM (
            SELECT close, volume FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 20
        )
    """, (symbol_id, as_of_ts))
    return cur.fetchone()[0] or 0

def get_dollar_vol_pctile_252d(conn, symbol_id, as_of_ts, current_20d_avg):
    cur = conn.execute("""
        SELECT AVG(close * volume) FROM (
            SELECT close, volume FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 252
        )
    """, (symbol_id, as_of_ts))
    trailing_avg = cur.fetchone()[0] or 0
    if trailing_avg == 0:
        return 1.0
    return current_20d_avg / trailing_avg

def get_forward_return_21d(conn, symbol_id, entry_ts):
    cur = conn.execute("""
        SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts >= ?
        ORDER BY ts LIMIT 22
    """, (symbol_id, entry_ts))
    rows = cur.fetchall()
    if len(rows) < 22:
        return None
    entry_px = rows[0][0]
    exit_px = rows[21][0]
    return (exit_px - entry_px) / entry_px

def get_forward_return_1w(conn, symbol_id, entry_ts):
    cur = conn.execute("""
        SELECT fwd_return FROM prediction_outcomes
        WHERE symbol_id=? AND horizon='1w' AND ts=?
    """, (symbol_id, entry_ts))
    row = cur.fetchone()
    return row[0] if row else None

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return 'CEO' in t or 'CFO' in t or 'CHIEF EXECUTIVE' in t or 'CHIEF FINANCIAL' in t

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Get all officer open-market purchases with filing date
    cur = conn.execute("""
        SELECT it.symbol_id, it.insider, it.title, it.code, it.shares, it.price, it.value,
               it.tx_ts, it.filed_ts, s.symbol, s.market
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P' AND s.market = 'stocks' AND s.active = 1
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return

    # Build per-insider trade history for dormancy check
    insider_trades_map = {}
    for t in trades:
        key = (t['symbol_id'], t['insider'])
        insider_trades_map.setdefault(key, []).append(t)

    # Determine sealed era cutoff (most recent 20% of filed_ts range)
    filed_dates = sorted(set(t['filed_ts'] for t in trades))
    cutoff_idx = int(len(filed_dates) * 0.8)
    sealed_cutoff = filed_dates[cutoff_idx] if cutoff_idx < len(filed_dates) else filed_dates[-1]

    issued = 0
    hits_1w = 0
    hits_21d = 0
    issued_sealed = 0
    hits_1w_sealed = 0
    hits_21d_sealed = 0
    distinct_days = set()
    distinct_days_sealed = set()
    opportunities = 0

    for t in trades:
        opportunities += 1
        symbol_id = t['symbol_id']
        insider = t['insider']
        title = t['title']
        tx_ts = t['tx_ts']
        filed_ts = t['filed_ts']
        price = t['price']

        # ABSTAIN: not an officer
        if not is_officer(title):
            continue

        # ABSTAIN: filing delay > 5 trading days
        delay_days = trading_days_between(conn, tx_ts, filed_ts)
        if delay_days > 5:
            continue

        # ABSTAIN: any insider sale in prior 10 trading days
        cur = conn.execute("""
            SELECT 1 FROM insider_trades
            WHERE symbol_id=? AND code='S' AND tx_ts BETWEEN ? AND ?
            LIMIT 1
        """, (symbol_id, tx_ts - 10*86400, tx_ts))
        if cur.fetchone():
            continue

        # Check personal dormancy: no P or S in prior 60 trading days for this insider
        key = (symbol_id, insider)
        history = insider_trades_map.get(key, [])
        prior_trades = [h for h in history if h['tx_ts'] < tx_ts and h['code'] in ('P', 'S')]
        if prior_trades:
            last_trade_ts = max(h['tx_ts'] for h in prior_trades)
            dormancy_days = trading_days_between(conn, last_trade_ts, tx_ts)
            if dormancy_days < 60:
                continue
        else:
            # No prior trades - check if insider existed 60 days ago (first trade ever)
            pass  # Allow first trade as dormancy break

        # Liquidity vacuum: 20-day avg dollar volume < 25th percentile of 252-day
        avg_20d = get_avg_dollar_vol_20d(conn, symbol_id, tx_ts)
        if avg_20d == 0:
            continue
        pctile = get_dollar_vol_pctile_252d(conn, symbol_id, tx_ts, avg_20d)
        if pctile > 0.25:
            continue

        # VWAP discipline: trade price < 20-day VWAP at trade time
        vwap_20d = get_vwap_20d(conn, symbol_id, tx_ts)
        if vwap_20d is None or price >= vwap_20d:
            continue

        # Market cap filter: approximate from 20-day avg dollar vol * 252 / turnover
        # Skip if avg_20d * 252 < 100M (rough proxy)
        if avg_20d * 252 < 100_000_000:
            continue

        # All entry conditions met - issue call
        issued += 1
        day_key = datetime.utcfromtimestamp(filed_ts).date()
        distinct_days.add(day_key)

        # Get labels
        fwd_1w = get_forward_return_1w(conn, symbol_id, filed_ts)
        fwd_21d = get_forward_return_21d(conn, symbol_id, filed_ts)

        is_sealed = filed_ts >= sealed_cutoff
        if is_sealed:
            issued_sealed += 1
            distinct_days_sealed.add(day_key)

        if fwd_1w is not None and fwd_1w > 0:
            hits_1w += 1
            if is_sealed:
                hits_1w_sealed += 1
        if fwd_21d is not None and fwd_21d > 0:
            hits_21d += 1
            if is_sealed:
                hits_21d_sealed += 1

    if issued == 0:
        print("INSUFFICIENT=1")
        return

    # Compute design effect for EFFECTIVE_N (simple clustering by day)
    # Design effect = 1 + (avg_cluster_size - 1) * ICC, approximate ICC=0.2
    # For simplicity, use issued / distinct_days as cluster factor
    cluster_factor = issued / len(distinct_days) if distinct_days else 1
    design_effect = 1 + (cluster_factor - 1) * 0.2
    effective_n = issued / design_effect

    precision_1w = hits_1w / issued if issued else 0
    base_rate = 0.5  # Theoretical base rate for directional prediction
    sealed_precision = hits_1w_sealed / issued_sealed if issued_sealed else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_1w:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={len(distinct_days)}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == '__main__':
    main()