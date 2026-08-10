# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 452
# cycle_index: 43
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import datetime
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
MIN_TS = int(datetime.datetime(2018, 7, 1).timestamp())
HORIZON = 21

def get_trading_days_after(conn, symbol_id, ts_epoch, n_days):
    """Get the close price n_days after ts_epoch for symbol_id."""
    cur = conn.cursor()
    # Get rank of ts_epoch
    rank_sql = """
        SELECT rank FROM (
            SELECT ts, RANK() OVER (ORDER BY ts) as rank
            FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=?
        ) WHERE ts=?
    """
    row = cur.execute(rank_sql, (symbol_id, ts_epoch, ts_epoch)).fetchone()
    if not row:
        return None
    rank = row[0]
    target_rank = rank + n_days
    close_sql = """
        SELECT close FROM (
            SELECT ts, RANK() OVER (ORDER BY ts) as rank
            FROM bars WHERE symbol_id=? AND tf='1d'
        ) WHERE rank=?
    """
    row = cur.execute(close_sql, (symbol_id, target_rank)).fetchone()
    if not row:
        return None
    return row[0]

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    cur = conn.cursor()

    # Get all insider trades with code 'P' and valid dates
    trades_sql = """
        SELECT t.symbol_id, t.tx_ts, t.filed_ts
        FROM insider_trades t
        JOIN symbols s ON t.symbol_id = s.id
        WHERE t.code = 'P'
          AND t.tx_ts IS NOT NULL AND t.filed_ts IS NOT NULL
          AND t.filed_ts >= ?
          AND t.tx_ts < t.filed_ts
    """
    trades = cur.execute(trades_sql, (MIN_TS,)).fetchall()

    signals = []
    seen = {}  # (symbol_id, filed_ts) -> one call per symbol per disclosure day

    for symbol_id, tx_ts, filed_ts in trades:
        key = (symbol_id, filed_ts)
        if key in seen:
            continue  # already have a call for this symbol on this disclosure day

        # Get close prices on trade date and disclosure date
        close_sql = "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?"
        trade_row = cur.execute(close_sql, (symbol_id, tx_ts)).fetchone()
        disc_row = cur.execute(close_sql, (symbol_id, filed_ts)).fetchone()
        if not trade_row or not disc_row:
            continue

        close_trade = trade_row[0]
        close_disc = disc_row[0]
        if close_trade is None or close_disc is None:
            continue

        # Check condition: trade-to-disclosure return < 0
        if close_trade <= close_disc:
            continue

        # Get forward return 21 trading days after disclosure
        fwd_close = get_trading_days_after(conn, symbol_id, filed_ts, HORIZON)
        if fwd_close is None:
            continue

        fwd_return = (fwd_close / close_disc) - 1
        up = 1 if fwd_return > 0 else 0

        # Convert filed_ts to date string for grouping
        disc_date = datetime.datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
        signals.append((symbol_id, disc_date, up))
        seen[key] = True

    conn.close()

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Sort signals by date for time-based split
    signals.sort(key=lambda x: x[1])
    n = len(signals)
    split_idx = int(n * 0.8)
    train = signals[:split_idx]
    test = signals[split_idx:]

    def compute_metrics(subset):
        if not subset:
            return None, None, None, None, None
        total = len(subset)
        hits = sum(1 for _, _, up in subset if up == 1)
        precision = hits / total
        # Base rate is proportion of up in the subset
        base_rate = precision
        # Distinct days
        distinct_days = len(set(day for _, day, _ in subset))
        # Effective N using design effect (cluster by day)
        clusters = defaultdict(list)
        for _, day, up in subset:
            clusters[day].append(up)
        m = len(clusters)
        sum_nc2 = 0
        sum_nc = 0
        sum_p_c = 0.0
        for day, ups in clusters.items():
            nc = len(ups)
            pc = sum(ups) / nc
            sum_nc2 += nc * nc
            sum_nc += nc
            sum_p_c += nc * pc
        p = sum_p_c / sum_nc  # overall proportion
        # Variance under clustering
        var_cluster = 0.0
        for day, ups in clusters.items():
            nc = len(ups)
            pc = sum(ups) / nc
            var_cluster += nc * nc * (pc - p) ** 2 + nc * pc * (1 - pc)
        var_cluster /= (sum_nc * sum_nc)
        # Variance under independence
        var_indep = p * (1 - p) / sum_nc if sum_nc > 1 else 0
        # Design effect
        deff = var_cluster / var_indep if var_indep > 0 else 1.0
        effective_n = sum_nc / deff
        return precision, base_rate, distinct_days, effective_n, total

    precision, base_rate, distinct_days, effective_n, total = compute_metrics(train)
    sealed_precision, _, _, _, sealed_total = compute_metrics(test)

    if total is None:
        print("INSUFFICIENT=1")
        return

    # Print required lines
    print(f"ISSUED={total}")
    print(f"OPPORTUNITIES={n}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}" if sealed_precision is not None else "SEALED_PRECISION=NA")

if __name__ == "__main__":
    main()