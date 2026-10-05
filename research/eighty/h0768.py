# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 767
# cycle_index: 37
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

def get_trading_days(conn, start_ts, end_ts):
    """Get all trading days (1d bars) in range as sorted list of timestamps (market close)."""
    cur = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE tf='1d' AND ts BETWEEN ? AND ? ORDER BY ts",
        (start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def get_prior_trading_day(trading_days, ts):
    """Get the trading day timestamp strictly before ts."""
    for td in reversed(trading_days):
        if td < ts:
            return td
    return None

def get_nth_next_trading_day(trading_days, ts, n):
    """Get the n-th trading day after ts (1-indexed)."""
    idx = -1
    for i, td in enumerate(trading_days):
        if td > ts:
            idx = i
            break
    if idx == -1 or idx + n - 1 >= len(trading_days):
        return None
    return trading_days[idx + n - 1]

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # 1. Load all trading days from bars (1d) for session counting and forward returns
    print("Loading trading days...", file=sys.stderr)
    cur = conn.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    min_ts, max_ts = cur.fetchone()
    trading_days = get_trading_days(conn, min_ts, max_ts)
    if not trading_days:
        print("INSUFFICIENT=1")
        return 0
    trading_day_set = set(trading_days)
    print(f"Trading days: {len(trading_days)} from {ts_to_date(min_ts)} to {ts_to_date(max_ts)}", file=sys.stderr)

    # 2. Load insider trades: code='P', officer titles (CEO/CFO)
    print("Loading insider trades...", file=sys.stderr)
    cur = conn.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY symbol_id, filed_ts
    """)
    insider_trades = [dict(row) for row in cur.fetchall()]
    print(f"Officer purchases: {len(insider_trades)}", file=sys.stderr)

    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0

    # 3. Build insider dormancy: for each insider, track last purchase trade date (tx_ts)
    insider_last_purchase_tx = defaultdict(lambda: 0)
    # We'll process trades in chronological order of tx_ts to build dormancy
    trades_by_tx = sorted(insider_trades, key=lambda x: x['tx_ts'])

    # 4. Load fundamentals: SharesOutstanding and Revenues
    print("Loading fundamentals...", file=sys.stderr)
    cur = conn.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('SharesOutstanding', 'Revenues')
        ORDER BY symbol_id, metric, fetched_at
    """)
    fund_rows = [dict(row) for row in cur.fetchall()]

    # Organize fundamentals by symbol, metric, as_of (quarter), keeping latest fetched_at per quarter
    fund_by_symbol = defaultdict(lambda: defaultdict(dict))  # symbol_id -> metric -> as_of -> (value, fetched_at)
    for row in fund_rows:
        sym = row['symbol_id']
        metric = row['metric']
        as_of = row['as_of']
        val = row['value']
        fetched = row['fetched_at']
        if as_of == 0:
            continue  # sentinel, not a real date
        if as_of not in fund_by_symbol[sym][metric] or fetched > fund_by_symbol[sym][metric][as_of][1]:
            fund_by_symbol[sym][metric][as_of] = (val, fetched)

    # 5. Load symbols to filter market='stocks'
    cur = conn.execute("SELECT id FROM symbols WHERE market='stocks' AND active=1")
    stock_symbol_ids = {row[0] for row in cur.fetchall()}
    print(f"Active stock symbols: {len(stock_symbol_ids)}", file=sys.stderr)

    # 6. Process each insider trade as a potential decision point
    decisions = []  # list of (symbol_id, decision_ts, filed_ts, tx_ts, insider, horizon_ts, fwd_return)

    # For dormancy check, we need to know prior purchases by same insider
    # Process trades in order of tx_ts (trade date)
    for trade in trades_by_tx:
        sym = trade['symbol_id']
        if sym not in stock_symbol_ids:
            continue
        insider = trade['insider']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']

        # Disclosure lag check: filed_ts - tx_ts <= 2 days (172800 seconds)
        if filed_ts - tx_ts > 172800:
            continue

        # Dormancy check: no prior purchase by same insider in previous 252 trading sessions
        last_tx = insider_last_purchase_tx[insider]
        if last_tx > 0:
            # Count trading days between last_tx and tx_ts
            prior_days = [td for td in trading_days if last_tx < td < tx_ts]
            if len(prior_days) < 252:
                continue

        # Update last purchase tx_ts for this insider
        insider_last_purchase_tx[insider] = tx_ts

        # Fundamentals check as of filed_ts (as-of discipline: use latest fetched_at <= filed_ts)
        # Need SharesOutstanding for past 12 quarters (3 years) and Revenues for acceleration
        # Get all quarters with data where fetched_at <= filed_ts
        so_quarters = []
        rev_quarters = []
        for as_of, (val, fetched) in fund_by_symbol[sym].get('SharesOutstanding', {}).items():
            if fetched <= filed_ts:
                so_quarters.append((as_of, val))
        for as_of, (val, fetched) in fund_by_symbol[sym].get('Revenues', {}).items():
            if fetched <= filed_ts:
                rev_quarters.append((as_of, val))

        if len(so_quarters) < 12 or len(rev_quarters) < 4:
            continue

        # Sort by as_of (quarter end)
        so_quarters.sort(key=lambda x: x[0])
        rev_quarters.sort(key=lambda x: x[0])

        # Check SharesOutstanding growth <=2% for each of past 12 quarters
        # Growth = (current - prior) / prior
        so_ok = True
        for i in range(1, min(12, len(so_quarters))):
            prior_val = so_quarters[i-1][1]
            curr_val = so_quarters[i][1]
            if prior_val > 0:
                growth = (curr_val - prior_val) / prior_val
                if growth > 0.02:
                    so_ok = False
                    break
        if not so_ok:
            continue

        # Check revenue growth acceleration for 3+ consecutive quarters
        # Need at least 4 quarters to compute 3 growth rates and 2 accelerations
        if len(rev_quarters) < 4:
            continue
        growth_rates = []
        for i in range(1, len(rev_quarters)):
            prior = rev_quarters[i-1][1]
            curr = rev_quarters[i][1]
            if prior > 0:
                growth_rates.append((curr - prior) / prior)
            else:
                growth_rates.append(None)
        # Acceleration: growth_rate[i] > growth_rate[i-1]
        accel_count = 0
        max_consec_accel = 0
        for i in range(1, len(growth_rates)):
            if growth_rates[i] is not None and growth_rates[i-1] is not None:
                if growth_rates[i] > growth_rates[i-1]:
                    accel_count += 1
                    max_consec_accel = max(max_consec_accel, accel_count)
                else:
                    accel_count = 0
        if max_consec_accel < 2:  # 3 quarters accelerating means 2 consecutive accelerations
            continue

        # All entry conditions met. Decision timestamp = filed_ts (disclosure date)
        decision_ts = filed_ts

        # Compute 5-day forward return from daily bars
        # Entry at next trading day open? Use next trading day close to 5th trading day close
        # We'll use close-to-close: buy at close of next trading day after decision, sell at close of 5th trading day after that
        entry_day = get_prior_trading_day(trading_days, decision_ts)  # This gives day <= decision_ts
        # Actually we want the NEXT trading day after decision_ts
        next_day = None
        for td in trading_days:
            if td > decision_ts:
                next_day = td
                break
        if not next_day:
            continue
        exit_day = get_nth_next_trading_day(trading_days, next_day, 5)
        if not exit_day:
            continue

        # Get close prices
        cur = conn.execute("SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sym, next_day))
        entry_row = cur.fetchone()
        cur = conn.execute("SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sym, exit_day))
        exit_row = cur.fetchone()
        if not entry_row or not exit_row:
            continue
        entry_px = entry_row[0]
        exit_px = exit_row[0]
        if entry_px <= 0:
            continue
        fwd_return = (exit_px - entry_px) / entry_px
        hit = 1 if fwd_return > 0 else 0

        decisions.append({
            'symbol_id': sym,
            'decision_ts': decision_ts,
            'decision_date': ts_to_date(decision_ts),
            'entry_day': next_day,
            'exit_day': exit_day,
            'fwd_return': fwd_return,
            'hit': hit
        })

    print(f"Qualifying decisions: {len(decisions)}", file=sys.stderr)

    if len(decisions) < 10:
        print("INSUFFICIENT=1")
        return 0

    # 7. Hold out most recent 20% by decision_date as sealed era
    decisions.sort(key=lambda x: x['decision_ts'])
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    n_main = n_total - n_sealed
    main_decisions = decisions[:n_main]
    sealed_decisions = decisions[n_main:]

    # 8. Compute metrics
    def compute_metrics(dec_list):
        if not dec_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(dec_list)
        hits = sum(d['hit'] for d in dec_list)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(d['decision_date'] for d in dec_list))
        # Design effect: cluster by day, compute variance inflation
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use Kish's effective sample size: n_eff = (sum w)^2 / sum w^2 where w=1 per obs
        # But clustered: group by day, each day has k_i observations
        # Effective N = (sum k_i)^2 / sum k_i^2 = issued^2 / sum k_i^2
        day_counts = defaultdict(int)
        for d in dec_list:
            day_counts[d['decision_date']] += 1
        sum_k_sq = sum(c*c for c in day_counts.values())
        effective_n = (issued * issued) / sum_k_sq if sum_k_sq > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_main, hits_main, prec_main, base_main, distinct_main, eff_main = compute_metrics(main_decisions)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed, eff_sealed = compute_metrics(sealed_decisions)

    # 9. Output required lines
    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={len(trades_by_tx)}")  # total officer purchases considered
    print(f"PRECISION={prec_main:.6f}")
    print(f"BASE_RATE={base_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_main}")
    print(f"EFFECTIVE_N={eff_main:.2f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())