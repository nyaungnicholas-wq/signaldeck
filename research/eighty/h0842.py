# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 841
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

POSITIVE_8K_KEYWORDS = [
    "results of operations",
    "financial statements",
    "entry into agreement",
    "acquisition",
    "guidance",
    "beat",
    "raise",
    "positive",
    "upward",
    "increase",
    "strong",
    "record",
    "exceed",
    "outperform",
]

CEO_CFO_TITLES = [
    "ceo",
    "cfo",
    "chief executive",
    "chief financial",
    "president",
    "chief operating",
    "coo",
]

def is_ceo_cfo(title: str) -> bool:
    if not title:
        return False
    t = title.lower()
    return any(kw in t for kw in CEO_CFO_TITLES)

def is_positive_8k(title: str) -> bool:
    if not title:
        return False
    t = title.lower()
    return any(kw in t for kw in POSITIVE_8K_KEYWORDS)

def epoch_to_date(ts: int) -> datetime:
    return datetime.utcfromtimestamp(ts)

def date_to_epoch(dt: datetime) -> int:
    return int(dt.timestamp())

def get_trading_days(conn, start_ts: int, end_ts: int) -> list:
    """Return sorted list of trading day timestamps (unix epoch at 00:00 UTC) from bars tf='1d'."""
    cur = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE tf='1d' AND ts >= ? AND ts <= ? ORDER BY ts",
        (start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_next_trading_days(conn, trading_days: list, start_ts: int, n: int) -> list:
    """Return the next n trading day timestamps after start_ts (exclusive)."""
    idx = 0
    while idx < len(trading_days) and trading_days[idx] <= start_ts:
        idx += 1
    return trading_days[idx:idx + n]

def compute_forward_return(conn, symbol_id: int, decision_ts: int, horizon_days: int) -> float:
    """Compute forward return over horizon_days trading days from decision_ts close."""
    trading_days = get_trading_days(conn, decision_ts - 86400 * 400, decision_ts + 86400 * 400)
    if not trading_days:
        return None
    # Find the decision day (trading day <= decision_ts)
    decision_day = None
    for d in trading_days:
        if d <= decision_ts:
            decision_day = d
        else:
            break
    if decision_day is None:
        return None
    # Get next horizon_days trading days
    future_days = get_next_trading_days(conn, trading_days, decision_day, horizon_days)
    if len(future_days) < horizon_days:
        return None
    # Get close on decision day and close on horizon day
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts IN (?, ?)",
        (symbol_id, decision_day, future_days[-1])
    )
    rows = cur.fetchall()
    if len(rows) != 2:
        return None
    close_start, close_end = rows[0][0], rows[1][0]
    if close_start <= 0:
        return None
    return (close_end - close_start) / close_start

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row

    # Load symbols universe
    cur = conn.execute(
        "SELECT id, symbol, delisted_at FROM symbols WHERE market='stocks' AND active=1"
    )
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return

    # Load 8-K filings with positive titles
    cur = conn.execute(
        "SELECT symbol_id, filed_ts, title FROM filings WHERE form='8-K' AND title IS NOT NULL"
    )
    filings_8k = []
    for row in cur.fetchall():
        if row['symbol_id'] in symbols and is_positive_8k(row['title']):
            filings_8k.append((row['symbol_id'], row['filed_ts'], row['title']))
    if not filings_8k:
        print("INSUFFICIENT=1")
        return

    # Load insider trades: code='P' (purchase), CEO/CFO
    cur = conn.execute(
        "SELECT symbol_id, trade_ts, filed_ts, title, code FROM insider_trades WHERE code='P' AND title IS NOT NULL"
    )
    insider_buys = []
    for row in cur.fetchall():
        if row['symbol_id'] in symbols and is_ceo_cfo(row['title']):
            insider_buys.append((row['symbol_id'], row['trade_ts'], row['filed_ts'], row['title']))
    if not insider_buys:
        print("INSUFFICIENT=1")
        return

    # Build trading day calendar from bars
    cur = conn.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    min_ts, max_ts = cur.fetchone()
    if min_ts is None or max_ts is None:
        print("INSUFFICIENT=1")
        return
    all_trading_days = get_trading_days(conn, min_ts, max_ts)
    trading_day_set = set(all_trading_days)

    # For each insider filing (decision point), check for qualifying 8-K in prior 3 trading days
    # Decision timestamp = insider filed_ts (when Form 4 becomes public)
    opportunities = 0
    issued_calls = []  # list of (symbol_id, decision_ts, decision_date, label)

    # Index 8-Ks by symbol
    filings_by_symbol = defaultdict(list)
    for sym_id, filed_ts, title in filings_8k:
        filings_by_symbol[sym_id].append((filed_ts, title))

    for sym_id, trade_ts, filed_ts, insider_title in insider_buys:
        opportunities += 1
        # ABSTAIN: insider trade_ts before any 8-K? We'll check per 8-K
        # ABSTAIN: symbol delisted before decision
        delisted_at = symbols[sym_id]['delisted_at']
        if delisted_at and delisted_at <= filed_ts:
            continue

        # Find 8-Ks in prior 3 trading days (inclusive of decision day)
        # Decision day = trading day of filed_ts
        decision_day = None
        for d in all_trading_days:
            if d <= filed_ts:
                decision_day = d
            else:
                break
        if decision_day is None:
            continue

        # Get prior 3 trading days including decision_day
        idx = all_trading_days.index(decision_day)
        prior_days = all_trading_days[max(0, idx-2):idx+1]  # up to 3 days

        qualifying_8ks = []
        for f_filed_ts, f_title in filings_by_symbol.get(sym_id, []):
            # 8-K filing day
            f_day = None
            for d in all_trading_days:
                if d <= f_filed_ts:
                    f_day = d
                else:
                    break
            if f_day in prior_days:
                # Check trade_ts >= 8-K filed_ts
                if trade_ts >= f_filed_ts:
                    qualifying_8ks.append((f_filed_ts, f_title))

        # ABSTAIN: multiple qualifying 8-Ks in prior 5 trading days (confounded)
        # Check prior 5 trading days for any 8-K (not just positive)
        prior_5_days = all_trading_days[max(0, idx-4):idx+1]
        all_8ks_in_window = 0
        for f_filed_ts, f_title in filings_by_symbol.get(sym_id, []):
            f_day = None
            for d in all_trading_days:
                if d <= f_filed_ts:
                    f_day = d
                else:
                    break
            if f_day in prior_5_days:
                all_8ks_in_window += 1
        if all_8ks_in_window > 1:
            continue

        if not qualifying_8ks:
            continue

        # Check sufficient future bars for 5-day forward return
        future_days = get_next_trading_days(conn, all_trading_days, decision_day, 5)
        if len(future_days) < 5:
            continue

        # Compute label: 5-trading-day forward return sign
        fwd_ret = compute_forward_return(conn, sym_id, decision_day, 5)
        if fwd_ret is None:
            continue
        label = 1 if fwd_ret > 0 else 0

        issued_calls.append({
            'symbol_id': sym_id,
            'decision_ts': filed_ts,
            'decision_day': decision_day,
            'label': label,
        })

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Deduplicate: one call per symbol per decision_day
    dedup = {}
    for call in issued_calls:
        key = (call['symbol_id'], call['decision_day'])
        if key not in dedup:
            dedup[key] = call
    issued_calls = list(dedup.values())

    # Sort by decision_day
    issued_calls.sort(key=lambda x: x['decision_day'])

    # Split: most recent 20% of decision_days as sealed era
    distinct_days = sorted(set(c['decision_day'] for c in issued_calls))
    n_sealed_days = max(1, int(len(distinct_days) * 0.2))
    sealed_days = set(distinct_days[-n_sealed_days:])

    train_calls = [c for c in issued_calls if c['decision_day'] not in sealed_days]
    sealed_calls = [c for c in issued_calls if c['decision_day'] in sealed_days]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c['label'] for c in calls)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate within issued subset
        distinct_d = len(set(c['decision_day'] for c in calls))
        # Effective N: Kish formula for clustered data (cluster = decision_day)
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c['decision_day']] += 1
        n_d = list(day_counts.values())
        sum_n = sum(n_d)
        sum_n2 = sum(x*x for x in n_d)
        effective_n = (sum_n * sum_n) / sum_n2 if sum_n2 > 0 else 0
        return issued, hits, precision, base_rate, distinct_d, effective_n

    train_issued, train_hits, train_precision, train_base, train_distinct, train_eff = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, sealed_base, sealed_distinct, sealed_eff = compute_metrics(sealed_calls)

    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    total_precision = total_hits / total_issued if total_issued else 0
    total_base = total_hits / total_issued if total_issued else 0
    total_distinct = len(set(c['decision_day'] for c in issued_calls))
    # Overall effective N
    day_counts = defaultdict(int)
    for c in issued_calls:
        day_counts[c['decision_day']] += 1
    n_d = list(day_counts.values())
    sum_n = sum(n_d)
    sum_n2 = sum(x*x for x in n_d)
    total_eff = (sum_n * sum_n) / sum_n2 if sum_n2 > 0 else 0

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base:.6f}")
    print(f"DISTINCT_DAYS={total_distinct}")
    print(f"EFFECTIVE_N={total_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()