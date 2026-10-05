# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 832
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check 1h bar availability for VWAP computation
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1h'")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return
    h1_min_ts, h1_max_ts = row[0], row[1]
    h1_min_date = epoch_to_date(h1_min_ts)
    h1_max_date = epoch_to_date(h1_max_ts)

    # Get officer open-market purchases (code P) with title containing CEO/CFO
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.tx_ts, it.filed_ts, it.price, it.shares, it.title
        FROM insider_trades it
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Filter trades where trade date has 1h bars for VWAP
    valid_trades = []
    for t in trades:
        tx_date = epoch_to_date(t['tx_ts'])
        tx_ts_start = date_to_epoch(tx_date)
        tx_ts_end = tx_ts_start + 86400
        cur.execute("SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1h' AND ts>=? AND ts<?", (t['symbol_id'], tx_ts_start, tx_ts_end))
        if cur.fetchone()[0] > 0:
            valid_trades.append(t)

    if len(valid_trades) < 30:
        print("INSUFFICIENT=1")
        return

    # Get symbols with daily bars
    cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    symbols_with_daily = {r[0] for r in cur.fetchall()}

    # Get fundamentals SharesOutstanding history
    cur.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric='SharesOutstanding' AND as_of>0
        ORDER BY symbol_id, as_of
    """)
    so_rows = cur.fetchall()
    so_by_symbol = {}
    for r in so_rows:
        so_by_symbol.setdefault(r['symbol_id'], []).append((r['as_of'], r['value'], r['fetched_at']))

    # Get WALCL (Fed balance sheet) monthly
    cur.execute("SELECT ts, value FROM macro_series WHERE series='WALCL' ORDER BY ts")
    walcl = cur.fetchall()
    if not walcl:
        print("INSUFFICIENT=1")
        return
    walcl_dates = [epoch_to_date(r['ts']) for r in walcl]
    walcl_values = [r['value'] for r in walcl]

    def walcl_3m_change(as_of_date):
        # Find WALCL value at as_of_date and 3 months prior
        idx = None
        for i, d in enumerate(walcl_dates):
            if d <= as_of_date:
                idx = i
            else:
                break
        if idx is None or idx < 3:
            return None
        # Approximate 3 months as ~63 trading days / 3 months
        # Use monthly spacing: find entry ~3 months back
        target_date = as_of_date - timedelta(days=90)
        idx2 = None
        for i, d in enumerate(walcl_dates):
            if d <= target_date:
                idx2 = i
            else:
                break
        if idx2 is None:
            return None
        return (walcl_values[idx] - walcl_values[idx2]) / walcl_values[idx2]

    # Get daily bars for forward returns
    # We'll fetch on demand per symbol

    # Process each trade as a decision point at filed_ts
    decisions = []
    for t in valid_trades:
        filed_date = epoch_to_date(t['filed_ts'])
        tx_date = epoch_to_date(t['tx_ts'])
        symbol_id = t['symbol_id']

        if symbol_id not in symbols_with_daily:
            continue

        # Check disclosure delay <= 5 sessions (approx 7 calendar days)
        if (t['filed_ts'] - t['tx_ts']) > 7 * 86400:
            continue

        # Compute VWAP for trade date from 1h bars
        tx_ts_start = date_to_epoch(tx_date)
        tx_ts_end = tx_ts_start + 86400
        cur.execute("""
            SELECT SUM(close * volume) / SUM(volume) as vwap
            FROM bars
            WHERE symbol_id=? AND tf='1h' AND ts>=? AND ts<? AND volume>0
        """, (symbol_id, tx_ts_start, tx_ts_end))
        vwap_row = cur.fetchone()
        if not vwap_row or vwap_row['vwap'] is None:
            continue
        vwap = vwap_row['vwap']
        if t['price'] >= vwap:
            continue

        # Check non-diluting: zero quarters of >2% SharesOutstanding growth in prior 12 quarters
        so_hist = so_by_symbol.get(symbol_id, [])
        # Find latest as_of <= filed_date with fetched_at <= filed_ts
        relevant_so = [(as_of, val) for as_of, val, fetched in so_hist if fetched <= t['filed_ts'] and as_of <= date_to_epoch(filed_date)]
        if len(relevant_so) < 12:
            continue
        relevant_so.sort(key=lambda x: x[0])
        last_12 = relevant_so[-12:]
        diluted = False
        for i in range(1, len(last_12)):
            prev = last_12[i-1][1]
            curr = last_12[i][1]
            if prev > 0 and (curr - prev) / prev > 0.02:
                diluted = True
                break
        if diluted:
            continue

        # Check Fed expansion: WALCL 3M change > 0 as of filing date
        walcl_chg = walcl_3m_change(filed_date)
        if walcl_chg is None or walcl_chg <= 0:
            continue

        # Compute 21-trading-day forward return from filing date using daily bars
        filed_ts = date_to_epoch(filed_date)
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id=? AND tf='1d' AND ts>=?
            ORDER BY ts LIMIT 22
        """, (symbol_id, filed_ts))
        bars_21 = cur.fetchall()
        if len(bars_21) < 22:
            continue
        entry_px = bars_21[0]['close']
        exit_px = bars_21[21]['close']
        fwd_return = (exit_px - entry_px) / entry_px
        label = 1 if fwd_return > 0 else 0

        decisions.append({
            'symbol_id': symbol_id,
            'decision_date': filed_date,
            'label': label,
            'fwd_return': fwd_return
        })

    if len(decisions) < 30:
        print("INSUFFICIENT=1")
        return

    # Deduplicate by (symbol_id, decision_date) - one observation per symbol-day
    seen = set()
    unique_decisions = []
    for d in decisions:
        key = (d['symbol_id'], d['decision_date'])
        if key not in seen:
            seen.add(key)
            unique_decisions.append(d)

    if len(unique_decisions) < 30:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_date
    unique_decisions.sort(key=lambda x: x['decision_date'])

    # Hold out most recent 20% as sealed era
    n = len(unique_decisions)
    split_idx = int(n * 0.8)
    train = unique_decisions[:split_idx]
    sealed = unique_decisions[split_idx:]

    # In this hypothesis, we issue a call for EVERY qualifying decision (no abstention beyond filters)
    # So issued = all decisions in each era
    def compute_metrics(decs):
        if not decs:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(decs)
        hits = sum(d['label'] for d in decs)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # since we issue on all, base rate within issued = precision
        distinct_days = len(set(d['decision_date'] for d in decs))
        # Design effect: approximate by 1 + (avg cluster size - 1) * autocorr
        # Simple approximation: group by day, count per day
        from collections import Counter
        day_counts = Counter(d['decision_date'] for d in decs)
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        # Assume intra-day correlation ~0.5 for conservative design effect
        deff = 1 + (avg_cluster - 1) * 0.5
        effective_n = issued / deff if deff > 1 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_days, train_eff = compute_metrics(train)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed)

    # Overall issued = train + sealed (but we report sealed separately)
    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    overall_prec = total_hits / total_issued if total_issued else 0.0
    overall_br = overall_prec
    all_days = len(set(d['decision_date'] for d in unique_decisions))
    # Overall effective n
    from collections import Counter
    all_day_counts = Counter(d['decision_date'] for d in unique_decisions)
    avg_cl = sum(all_day_counts.values()) / len(all_day_counts) if all_day_counts else 1
    deff_all = 1 + (avg_cl - 1) * 0.5
    eff_all = total_issued / deff_all if deff_all > 1 else total_issued

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={len(valid_trades)}")
    print(f"PRECISION={overall_prec:.6f}")
    print(f"BASE_RATE={overall_br:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={eff_all:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()