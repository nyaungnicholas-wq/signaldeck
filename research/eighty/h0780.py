# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 779
# cycle_index: 49
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from bisect import bisect_left, bisect_right

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn):
    """Return dict symbol_id -> sorted list of (ts, close, volume) for tf='1d'"""
    cur = conn.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    days = defaultdict(list)
    for symbol_id, ts, close, volume in cur:
        days[symbol_id].append((ts, close, volume))
    return days

def get_symbols(conn):
    """Return dict symbol_id -> (symbol, market, delisted_at)"""
    cur = conn.execute("SELECT id, symbol, market, delisted_at FROM symbols")
    return {row[0]: (row[1], row[2], row[3]) for row in cur}

def get_insider_purchases(conn):
    """Return list of (symbol_id, filed_ts, accession) for Form 4 open-market purchases (code='P')"""
    cur = conn.execute("""
        SELECT it.symbol_id, it.filed_ts, it.accession
        FROM insider_trades it
        JOIN filings f ON f.symbol_id = it.symbol_id AND f.form = '4'
        WHERE it.code = 'P'
        ORDER BY it.symbol_id, it.filed_ts
    """)
    return [(row[0], row[1], row[2]) for row in cur]

def get_shares_outstanding(conn):
    """Return dict symbol_id -> list of (as_of, value, fetched_at) for SharesOutstanding"""
    cur = conn.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding' AND as_of > 0
        ORDER BY symbol_id, as_of
    """)
    so = defaultdict(list)
    for symbol_id, as_of, value, fetched_at in cur:
        so[symbol_id].append((as_of, value, fetched_at))
    return so

def get_news_dates(conn):
    """Return dict symbol_id -> set of date strings (YYYY-MM-DD) with news"""
    cur = conn.execute("SELECT symbol_id, ts FROM news")
    news_dates = defaultdict(set)
    for symbol_id, ts in cur:
        import datetime
        dt = datetime.datetime.utcfromtimestamp(ts).date()
        news_dates[symbol_id].add(dt.isoformat())
    return news_dates

def find_trading_day_index(trading_days, target_ts):
    """Return index of trading day with ts <= target_ts, or -1 if none"""
    ts_list = [d[0] for d in trading_days]
    idx = bisect_right(ts_list, target_ts) - 1
    return idx

def get_close_at(trading_days, idx):
    return trading_days[idx][1] if 0 <= idx < len(trading_days) else None

def get_volume_at(trading_days, idx):
    return trading_days[idx][2] if 0 <= idx < len(trading_days) else None

def compute_returns(trading_days, lookback):
    """Compute lookback-session returns for each day: (close[idx] / close[idx-lookback] - 1)"""
    n = len(trading_days)
    returns = [None] * n
    for i in range(lookback, n):
        c_now = trading_days[i][1]
        c_then = trading_days[i - lookback][1]
        if c_then and c_then > 0:
            returns[i] = (c_now / c_then) - 1.0
    return returns

def compute_trailing_quintile(returns, idx, window, quintile):
    """Check if returns[idx] is in bottom quintile of trailing window"""
    if idx < window:
        return False
    trailing = [r for r in returns[idx - window:idx] if r is not None]
    if len(trailing) < window * 0.8:
        return False
    trailing.sort()
    threshold = trailing[int(len(trailing) * quintile)]
    return returns[idx] is not None and returns[idx] <= threshold

def get_latest_so_before(so_history, filed_ts):
    """Get latest SharesOutstanding with fetched_at <= filed_ts"""
    best = None
    for as_of, value, fetched_at in so_history:
        if fetched_at <= filed_ts:
            best = (as_of, value, fetched_at)
        else:
            break
    return best

def get_quarter_ends_before(so_history, filed_ts):
    """Get list of (as_of, value) for quarters ending before filed_ts, sorted by as_of"""
    quarters = []
    for as_of, value, fetched_at in so_history:
        if as_of < filed_ts:
            quarters.append((as_of, value))
    return quarters

def shares_declined_2q(quarters):
    """Check if SharesOutstanding declined for 2+ consecutive quarters"""
    if len(quarters) < 3:
        return False
    for i in range(len(quarters) - 2):
        if quarters[i+1][1] < quarters[i][1] and quarters[i+2][1] < quarters[i+1][1]:
            return True
    return False

def sessions_since_quarter_end(trading_days, quarter_end_ts, trade_idx):
    """Count trading sessions between quarter_end_ts and trade_idx (exclusive of quarter end)"""
    q_idx = find_trading_day_index(trading_days, quarter_end_ts)
    if q_idx < 0 or q_idx >= trade_idx:
        return 0
    return trade_idx - q_idx

def avg_dollar_volume(trading_days, trade_idx, window=252):
    """Average daily dollar volume over trailing window sessions"""
    if trade_idx < window:
        return 0.0
    total = 0.0
    count = 0
    for i in range(trade_idx - window, trade_idx):
        close = trading_days[i][1]
        vol = trading_days[i][2]
        if close and vol:
            total += close * vol
            count += 1
    return total / count if count else 0.0

def forward_return_21d(trading_days, trade_idx):
    """21-session forward return from trade_idx"""
    if trade_idx + 21 >= len(trading_days):
        return None
    c_now = trading_days[trade_idx][1]
    c_fwd = trading_days[trade_idx + 21][1]
    if c_now and c_fwd and c_now > 0:
        return (c_fwd / c_now) - 1.0
    return None

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row

    print("Loading data...", file=sys.stderr)
    trading_days = get_trading_days(conn)
    symbols = get_symbols(conn)
    insider_purchases = get_insider_purchases(conn)
    so_history = get_shares_outstanding(conn)
    news_dates = get_news_dates(conn)

    print(f"Symbols with bars: {len(trading_days)}", file=sys.stderr)
    print(f"Insider purchases (Form 4, code=P): {len(insider_purchases)}", file=sys.stderr)
    print(f"Symbols with SharesOutstanding: {len(so_history)}", file=sys.stderr)
    print(f"Symbols with news: {len(news_dates)}", file=sys.stderr)

    # Filter symbols with minimum 252 sessions
    eligible_symbols = {sid for sid, days in trading_days.items() if len(days) >= 252}
    print(f"Symbols with 252+ sessions: {len(eligible_symbols)}", file=sys.stderr)

    # Precompute returns for each symbol
    returns_63 = {}
    for sid in eligible_symbols:
        if sid in trading_days:
            returns_63[sid] = compute_returns(trading_days[sid], 63)

    # Process each insider purchase
    opportunities = []
    issued_calls = []

    for symbol_id, filed_ts, accession in insider_purchases:
        if symbol_id not in eligible_symbols:
            continue
        if symbol_id not in so_history or symbol_id not in news_dates:
            continue

        td = trading_days[symbol_id]
        trade_idx = find_trading_day_index(td, filed_ts)
        if trade_idx < 252:  # Need 252 trailing sessions for distribution
            continue

        # Check zero news on trade date
        import datetime
        trade_date = datetime.datetime.utcfromtimestamp(filed_ts).date().isoformat()
        if trade_date in news_dates[symbol_id]:
            continue

        # Check SharesOutstanding declined 2+ consecutive quarters ending before trade date
        quarters = get_quarter_ends_before(so_history[symbol_id], filed_ts)
        if not shares_declined_2q(quarters):
            continue

        # Check 63-session return in bottom quintile of trailing 252
        if not compute_trailing_quintile(returns_63[symbol_id], trade_idx, 252, 0.2):
            continue

        # Check trade date >= 5 sessions after quarter-end
        latest_quarter = quarters[-1][0] if quarters else 0
        if sessions_since_quarter_end(td, latest_quarter, trade_idx) < 5:
            continue

        # Market cap > $500M at trade date
        latest_so = get_latest_so_before(so_history[symbol_id], filed_ts)
        if not latest_so:
            continue
        so_value = latest_so[1]
        close = get_close_at(td, trade_idx)
        if not close or close * so_value <= 500_000_000:
            continue

        # Average daily dollar volume > $10M
        if avg_dollar_volume(td, trade_idx, 252) <= 10_000_000:
            continue

        # This is a valid opportunity
        opportunities.append((symbol_id, filed_ts, trade_idx, accession))

    print(f"Opportunities passing entry conditions: {len(opportunities)}", file=sys.stderr)

    # Check abstention: fewer than 3 qualifying setups in prior 252 sessions for the symbol
    # Group opportunities by symbol
    by_symbol = defaultdict(list)
    for opp in opportunities:
        by_symbol[opp[0]].append(opp)

    for symbol_id, opps in by_symbol.items():
        opps.sort(key=lambda x: x[1])  # sort by filed_ts
        for i, (sid, filed_ts, trade_idx, accession) in enumerate(opps):
            # Count prior qualifying setups in last 252 sessions
            prior_count = 0
            for j in range(i):
                _, prior_ts, _, _ = opps[j]
                if filed_ts - prior_ts <= 252 * 86400 * 1.5:  # approx 252 trading days in seconds
                    prior_count += 1
            if prior_count < 3:
                # Abstain
                continue
            issued_calls.append((sid, filed_ts, trade_idx, accession))

    print(f"Issued calls before sealed era: {len(issued_calls)}", file=sys.stderr)

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Sort issued calls by trade date
    issued_calls.sort(key=lambda x: x[1])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(issued_calls) * 0.8)
    train_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]

    # Compute labels and metrics
    def evaluate_calls(calls):
        hits = 0
        total = 0
        for symbol_id, filed_ts, trade_idx, accession in calls:
            fwd_ret = forward_return_21d(trading_days[symbol_id], trade_idx)
            if fwd_ret is not None:
                total += 1
                if fwd_ret > 0:
                    hits += 1
        return hits, total

    train_hits, train_total = evaluate_calls(train_calls)
    sealed_hits, sealed_total = evaluate_calls(sealed_calls)

    issued = train_total + sealed_total
    if issued == 0:
        print("INSUFFICIENT=1")
        return

    precision = (train_hits + sealed_hits) / issued
    base_rate = (train_hits + sealed_hits) / issued  # base rate of positive class in issued subset
    distinct_days = len(set(datetime.datetime.utcfromtimestamp(ts).date() for _, ts, _, _ in issued_calls[:issued]))

    # Design effect: cluster by symbol and time
    # Simple approximation: group calls within 5 days of same symbol
    clusters = []
    for sid, filed_ts, _, _ in issued_calls[:issued]:
        placed = False
        for cluster in clusters:
            if cluster[0] == sid and abs(filed_ts - cluster[1]) < 5 * 86400:
                cluster.append(filed_ts)
                placed = True
                break
        if not placed:
            clusters.append([sid, filed_ts])
    design_effect = len(issued_calls[:issued]) / len(clusters) if clusters else 1.0
    effective_n = issued / design_effect if design_effect > 1 else issued - 1

    opportunities_count = len(opportunities)
    abstention_rate = 1 - (issued / opportunities_count) if opportunities_count else 0

    sealed_precision = sealed_hits / sealed_total if sealed_total else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()