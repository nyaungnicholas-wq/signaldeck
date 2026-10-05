# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 764
# cycle_index: 34
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    spy_row = cur.execute("SELECT id FROM symbols WHERE symbol = 'SPY'").fetchone()
    if not spy_row:
        print("INSUFFICIENT=1")
        return
    spy_id = spy_row['id']

    spy_bars = cur.execute(
        "SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts",
        (spy_id,)
    ).fetchall()
    if len(spy_bars) < 2:
        print("INSUFFICIENT=1")
        return

    spy_returns = {}
    prev_close = None
    for row in spy_bars:
        ts = row['ts']
        close = row['close']
        if prev_close is not None and prev_close != 0:
            spy_returns[ts] = (close - prev_close) / prev_close
        prev_close = close

    trades = cur.execute(
        "SELECT symbol_id, tx_ts, filed_ts, code FROM insider_trades WHERE code = 'P' ORDER BY filed_ts"
    ).fetchall()

    qualifying = []
    for t in trades:
        tx_ts = t['tx_ts']
        filed_ts = t['filed_ts']
        if tx_ts in spy_returns and spy_returns[tx_ts] <= -0.02:
            qualifying.append((t['symbol_id'], filed_ts, tx_ts, spy_returns[tx_ts]))

    if not qualifying:
        print("INSUFFICIENT=1")
        return

    all_disclosures = cur.execute(
        "SELECT DISTINCT symbol_id, date(filed_ts, 'unixepoch') as disc_date, filed_ts "
        "FROM insider_trades ORDER BY filed_ts"
    ).fetchall()

    opp_by_sym = {}
    for row in all_disclosures:
        sym = row['symbol_id']
        disc_date = row['disc_date']
        filed_ts = row['filed_ts']
        opp_by_sym.setdefault(sym, []).append((disc_date, filed_ts))

    qual_by_sym = {}
    for sym, filed_ts, tx_ts, spy_ret in qualifying:
        disc_date = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        qual_by_sym.setdefault(sym, []).append((disc_date, filed_ts, tx_ts, spy_ret))

    symbols_needed = set(qual_by_sym.keys()) | set(opp_by_sym.keys())
    bars_data = {}
    for sym in symbols_needed:
        rows = cur.execute(
            "SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts",
            (sym,)
        ).fetchall()
        if rows:
            bars_data[sym] = [(row['ts'], row['close']) for row in rows]

    def get_forward_return(sym, filed_ts, horizon_days=21):
        if sym not in bars_data:
            return None
        bars = bars_data[sym]
        disc_date = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        idx = -1
        for i, (ts, _) in enumerate(bars):
            bar_date = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
            if bar_date >= disc_date:
                idx = i
                break
        if idx == -1 or idx + horizon_days >= len(bars):
            return None
        close_0 = bars[idx][1]
        close_h = bars[idx + horizon_days][1]
        if close_0 == 0:
            return None
        return (close_h - close_0) / close_0

    opportunities = []
    for sym, disclosures in opp_by_sym.items():
        for disc_date, filed_ts in disclosures:
            fwd = get_forward_return(sym, filed_ts)
            if fwd is not None:
                opportunities.append((sym, disc_date, filed_ts, fwd))

    issued = []
    for sym, quals in qual_by_sym.items():
        for disc_date, filed_ts, tx_ts, spy_ret in quals:
            fwd = get_forward_return(sym, filed_ts)
            if fwd is not None:
                issued.append((sym, disc_date, filed_ts, fwd, spy_ret))

    if not opportunities or not issued:
        print("INSUFFICIENT=1")
        return

    opportunities.sort(key=lambda x: x[2])
    issued.sort(key=lambda x: x[2])

    n_total = len(opportunities)
    n_issued = len(issued)
    split_idx = int(n_total * 0.8)
    seal_date = opportunities[split_idx][1] if split_idx < n_total else None

    train_issued = [x for x in issued if x[1] < seal_date] if seal_date else issued
    sealed_issued = [x for x in issued if x[1] >= seal_date] if seal_date else []

    def compute_precision(lst):
        if not lst:
            return 0.0
        hits = sum(1 for x in lst if x[3] > 0)
        return hits / len(lst)

    def compute_base_rate(lst):
        if not lst:
            return 0.0
        hits = sum(1 for x in lst if x[3] > 0)
        return hits / len(lst)

    train_precision = compute_precision(train_issued)
    sealed_precision = compute_precision(sealed_issued)
    base_rate = compute_base_rate(train_issued)

    distinct_days = len(set(x[1] for x in train_issued))
    design_effect = n_issued / distinct_days if distinct_days > 0 else 1.0
    effective_n = n_issued / design_effect if design_effect > 0 else 0

    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={n_total}")
    print(f"PRECISION={train_precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()