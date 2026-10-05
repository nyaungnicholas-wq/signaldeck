# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 726
# cycle_index: 53
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

SECONDS_PER_DAY = 86400
GAP_LIMIT_DAYS = 5
GAP_LIMIT_SECONDS = GAP_LIMIT_DAYS * SECONDS_PER_DAY
HORIZON_DAYS = 21
MIN_MARKET_CAP = 50_000_000
MIN_AVG_DOLLAR_VOL = 100_000
INST_OWNERSHIP_QUARTILE = 0.25
LOOKBACK_FILINGS_DAYS = 5
LOOKBACK_FILINGS_SECONDS = LOOKBACK_FILINGS_DAYS * SECONDS_PER_DAY
LAG_13F_DAYS = 45
LAG_13F_SECONDS = LAG_13F_DAYS * SECONDS_PER_DAY
HOLDOUT_FRACTION = 0.20

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def fetch_insider_purchases(conn):
    cur = conn.execute("""
        SELECT symbol_id, insider, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, tx_ts, filed_ts
    """)
    return cur.fetchall()

def group_by_trade_date(purchases):
    groups = defaultdict(list)
    for symbol_id, insider, tx_ts, filed_ts in purchases:
        groups[(symbol_id, tx_ts)].append((insider, filed_ts))
    return groups

def filter_clusters(groups):
    opportunities = []
    for (symbol_id, tx_ts), trades in groups.items():
        insiders = set()
        max_filed = 0
        gap_ok = True
        for insider, filed_ts in trades:
            insiders.add(insider)
            if filed_ts > max_filed:
                max_filed = filed_ts
            if filed_ts - tx_ts > GAP_LIMIT_SECONDS:
                gap_ok = False
        if len(insiders) >= 2 and gap_ok:
            opportunities.append({
                'symbol_id': symbol_id,
                'trade_ts': tx_ts,
                'decision_ts': max_filed,
                'insider_count': len(insiders)
            })
    return opportunities

def get_shares_outstanding(conn, symbol_id, as_of_ts):
    cur = conn.execute("""
        SELECT value FROM fundamentals
        WHERE symbol_id = ? AND metric = 'SharesOutstanding' AND fetched_at <= ?
        ORDER BY fetched_at DESC LIMIT 1
    """, (symbol_id, as_of_ts))
    row = cur.fetchone()
    return float(row[0]) if row else None

def get_price_at_or_before(conn, symbol_id, ts):
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, ts))
    row = cur.fetchone()
    return float(row[0]) if row else None

def get_avg_dollar_volume(conn, symbol_id, decision_ts, lookback_days=20):
    cur = conn.execute("""
        SELECT volume, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC LIMIT ?
    """, (symbol_id, decision_ts, lookback_days))
    rows = cur.fetchall()
    if len(rows) < lookback_days:
        return None
    total = sum(float(v) * float(c) for v, c in rows)
    return total / lookback_days

def get_inst_ownership_pct(conn, symbol_id, period_ts):
    cur = conn.execute("""
        SELECT SUM(shares) FROM inst_holdings
        WHERE symbol_id = ? AND period = ?
    """, (symbol_id, period_ts))
    row = cur.fetchone()
    inst_shares = float(row[0]) if row and row[0] else 0.0
    if inst_shares == 0:
        return 0.0
    shares_out = get_shares_outstanding(conn, symbol_id, period_ts + LAG_13F_SECONDS)
    if not shares_out or shares_out == 0:
        return None
    return inst_shares / shares_out

def get_cross_sectional_quartile(conn, period_ts):
    cur = conn.execute("""
        SELECT DISTINCT symbol_id FROM inst_holdings
        WHERE period = ?
    """, (period_ts,))
    symbols = [row[0] for row in cur.fetchall()]
    if not symbols:
        return None
    pcts = []
    for sym in symbols:
        pct = get_inst_ownership_pct(conn, sym, period_ts)
        if pct is not None:
            pcts.append(pct)
    if not pcts:
        return None
    pcts.sort()
    idx = int(len(pcts) * INST_OWNERSHIP_QUARTILE)
    return pcts[idx]

def has_recent_filing(conn, symbol_id, decision_ts):
    cutoff = decision_ts - LOOKBACK_FILINGS_SECONDS
    cur = conn.execute("""
        SELECT 1 FROM filings
        WHERE symbol_id = ? AND form IN ('8-K', '424B2')
        AND filed_ts >= ? AND filed_ts < ?
        LIMIT 1
    """, (symbol_id, cutoff, decision_ts))
    return cur.fetchone() is not None

def get_forward_return(conn, symbol_id, decision_ts, horizon_days=HORIZON_DAYS):
    cur = conn.execute("""
        SELECT ts, close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, decision_ts))
    row = cur.fetchone()
    if not row:
        return None
    entry_ts, entry_close = row
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts ASC LIMIT ?
    """, (symbol_id, entry_ts, horizon_days))
    rows = cur.fetchall()
    if len(rows) < horizon_days:
        return None
    exit_close = float(rows[-1][0])
    return (exit_close - float(entry_close)) / float(entry_close)

def utc_day(ts):
    return datetime.utcfromtimestamp(ts).date()

def main():
    conn = connect_db()
    try:
        purchases = fetch_insider_purchases(conn)
        if not purchases:
            print("INSUFFICIENT=1")
            return 0

        groups = group_by_trade_date(purchases)
        opportunities = filter_clusters(groups)
        if not opportunities:
            print("INSUFFICIENT=1")
            return 0

        opportunities.sort(key=lambda x: x['decision_ts'])

        split_idx = int(len(opportunities) * (1 - HOLDOUT_FRACTION))
        train_opps = opportunities[:split_idx]
        sealed_opps = opportunities[split_idx:]

        issued_calls = []
        all_results = []

        for opp in opportunities:
            symbol_id = opp['symbol_id']
            decision_ts = opp['decision_ts']

            shares_out = get_shares_outstanding(conn, symbol_id, decision_ts)
            if not shares_out:
                all_results.append((opp, False, None))
                continue
            price = get_price_at_or_before(conn, symbol_id, decision_ts)
            if not price:
                all_results.append((opp, False, None))
                continue
            market_cap = shares_out * price
            if market_cap < MIN_MARKET_CAP:
                all_results.append((opp, False, None))
                continue

            avg_dol_vol = get_avg_dollar_volume(conn, symbol_id, decision_ts)
            if avg_dol_vol is None or avg_dol_vol < MIN_AVG_DOLLAR_VOL:
                all_results.append((opp, False, None))
                continue

            period_ts = None
            cur = conn.execute("""
                SELECT MAX(period) FROM inst_holdings
                WHERE symbol_id = ? AND period <= ?
            """, (symbol_id, decision_ts - LAG_13F_SECONDS))
            row = cur.fetchone()
            if row and row[0]:
                period_ts = row[0]
            if not period_ts:
                all_results.append((opp, False, None))
                continue

            quartile = get_cross_sectional_quartile(conn, period_ts)
            if quartile is None:
                all_results.append((opp, False, None))
                continue
            inst_pct = get_inst_ownership_pct(conn, symbol_id, period_ts)
            if inst_pct is None or inst_pct > quartile:
                all_results.append((opp, False, None))
                continue

            if has_recent_filing(conn, symbol_id, decision_ts):
                all_results.append((opp, False, None))
                continue

            fwd_ret = get_forward_return(conn, symbol_id, decision_ts)
            if fwd_ret is None:
                all_results.append((opp, False, None))
                continue

            hit = 1 if fwd_ret > 0 else 0
            issued = True
            issued_calls.append({
                'symbol_id': symbol_id,
                'decision_ts': decision_ts,
                'hit': hit
            })
            all_results.append((opp, True, hit))

        if not issued_calls:
            print("INSUFFICIENT=1")
            return 0

        issued_count = len(issued_calls)
        hits_issued = sum(c['hit'] for c in issued_calls)
        precision = hits_issued / issued_count

        opportunities_count = len(opportunities)
        hits_all = sum(r[2] for r in all_results if r[2] is not None)
        valid_opps = sum(1 for r in all_results if r[2] is not None)
        base_rate = hits_all / valid_opps if valid_opps > 0 else 0.0

        distinct_days = len(set(utc_day(c['decision_ts']) for c in issued_calls))

        day_counts = defaultdict(int)
        for c in issued_calls:
            day_counts[utc_day(c['decision_ts'])] += 1
        sum_sq = sum(c * c for c in day_counts.values())
        design_effect = sum_sq / issued_count
        if design_effect <= 1.0:
            design_effect = 1.01
        effective_n = issued_count / design_effect

        sealed_calls = [c for c in issued_calls if c in [r[0] for r in all_results[split_idx:] if r[1]]]
        sealed_issued = [c for c in issued_calls if c['decision_ts'] >= sealed_opps[0]['decision_ts']] if sealed_opps else []
        sealed_hits = sum(c['hit'] for c in sealed_issued)
        sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0.0

        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.6f}")
        print(f"BASE_RATE={base_rate:.6f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")

    finally:
        conn.close()
    return 0

if __name__ == '__main__':
    sys.exit(main())