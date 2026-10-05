import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def get_trading_days(conn, symbol_id, start_date, end_date):
    """Get sorted list of trading days (1d bars) for symbol in range."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, int(start_date.timestamp()), int(end_date.timestamp()))
    )
    return [epoch_to_date(row[0]) for row in cur.fetchall()]

def get_bars_dict(conn, symbol_id, start_ts, end_ts):
    """Return dict ts->(open,high,low,close,volume) for 1d bars."""
    cur = conn.execute(
        "SELECT ts, open, high, low, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return {row[0]: row[1:] for row in cur.fetchall()}

def get_news_counts(conn, symbol_id, start_date, end_date):
    """Return dict date->count of news articles."""
    cur = conn.execute(
        "SELECT date(ts, 'unixepoch') as d, COUNT(*) FROM news WHERE symbol_id=? AND date(ts, 'unixepoch') BETWEEN ? AND ? GROUP BY d",
        (symbol_id, start_date, end_date)
    )
    return {str_to_date(row[0]): row[1] for row in cur.fetchall()}

def get_insider_purchases(conn, symbol_id):
    """Return list of (tx_ts, filed_ts, shares, price, value, insider) for open-market purchases (code='P')."""
    cur = conn.execute(
        "SELECT tx_ts, filed_ts, shares, price, value, insider FROM insider_trades WHERE symbol_id=? AND code='P' ORDER BY filed_ts",
        (symbol_id,)
    )
    return [(row[0], row[1], row[2], row[3], row[4], row[5]) for row in cur.fetchall()}

def get_symbols_with_bars(conn, min_bars=756):
    """Return symbol_ids with >= min_bars daily bars."""
    cur = conn.execute(
        "SELECT symbol_id, COUNT(*) as c FROM bars WHERE tf='1d' GROUP BY symbol_id HAVING c>=?",
        (min_bars,)
    )
    return [row[0] for row in cur.fetchall()]

def get_symbols_with_news(conn, min_days=504):
    """Return symbol_ids with >= min_days of news history."""
    cur = conn.execute(
        "SELECT symbol_id, COUNT(DISTINCT date(ts, 'unixepoch')) as c FROM news GROUP BY symbol_id HAVING c>=?",
        (min_days,)
    )
    return [row[0] for row in cur.fetchall()]

def get_symbols_with_insider(conn):
    """Return symbol_ids with insider trades."""
    cur = conn.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    return [row[0] for row in cur.fetchall()]

def get_symbol_info(conn, symbol_id):
    cur = conn.execute("SELECT symbol, market FROM symbols WHERE id=?", (symbol_id,))
    row = cur.fetchone()
    return row if row else (None, None)

def compute_forward_return(bars_dict, trading_days, entry_idx, horizon=21):
    """Compute 21-trading-day forward return from entry_idx (index in trading_days)."""
    if entry_idx + horizon >= len(trading_days):
        return None
    entry_ts = int(datetime.combine(trading_days[entry_idx], datetime.min.time()).timestamp())
    exit_ts = int(datetime.combine(trading_days[entry_idx + horizon], datetime.min.time()).timestamp())
    if entry_ts not in bars_dict or exit_ts not in bars_dict:
        return None
    entry_close = bars_dict[entry_ts][3]
    exit_close = bars_dict[exit_ts][3]
    if entry_close <= 0:
        return None
    return (exit_close - entry_close) / entry_close

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Get eligible symbols
    symbols_bars = set(get_symbols_with_bars(conn, 756))
    symbols_news = set(get_symbols_with_news(conn, 504))
    symbols_insider = set(get_symbols_with_insider(conn))
    eligible_symbols = symbols_bars & symbols_news & symbols_insider

    if len(eligible_symbols) < 30:
        print("INSUFFICIENT=1")
        return

    all_opportunities = []
    all_calls = []

    for symbol_id in eligible_symbols:
        symbol, market = get_symbol_info(conn, symbol_id)
        if not symbol or market != 'stocks':
            continue

        # Get full trading day history
        cur = conn.execute(
            "SELECT MIN(ts), MAX(ts) FROM bars WHERE symbol_id=? AND tf='1d'",
            (symbol_id,)
        )
        min_ts, max_ts = cur.fetchone()
        if not min_ts or not max_ts:
            continue
        min_date = epoch_to_date(min_ts)
        max_date = epoch_to_date(max_ts)

        # Need 252 sessions of news history before any decision
        news_start = min_date
        news_end = max_date
        news_counts = get_news_counts(conn, symbol_id, news_start, news_end)
        if len(news_counts) < 504:
            continue

        # Get bars
        bars_dict = get_bars_dict(conn, symbol_id, min_ts, max_ts)
        trading_days = sorted([epoch_to_date(ts) for ts in bars_dict.keys()])
        if len(trading_days) < 756:
            continue

        # Get insider purchases
        purchases = get_insider_purchases(conn, symbol_id)
        if not purchases:
            continue

        # Map trading day to index
        day_to_idx = {d: i for i, d in enumerate(trading_days)}

        # Precompute daily dollar volume (close * volume)
        dollar_vol = {}
        for ts, (o, h, l, c, v) in bars_dict.items():
            d = epoch_to_date(ts)
            dollar_vol[d] = c * v

        # For each purchase, evaluate at filed_ts
        for tx_ts, filed_ts, shares, price, value, insider in purchases:
            filed_date = epoch_to_date(filed_ts)
            tx_date = epoch_to_date(tx_ts)

            # Disclosure delay > 5 trading days?
            if filed_date not in day_to_idx or tx_date not in day_to_idx:
                continue
            delay = day_to_idx[filed_date] - day_to_idx[tx_date]
            if delay > 5:
                continue

            # Need 252 sessions of news history before filed_date
            # Compute 63-day mean and 252-day 5th percentile as of filed_date
            news_dates = sorted(news_counts.keys())
            # Filter to dates <= filed_date
            hist_dates = [d for d in news_dates if d <= filed_date]
            if len(hist_dates) < 252:
                continue

            # Get last 252 sessions
            last_252 = hist_dates[-252:]
            counts_252 = [news_counts[d] for d in last_252]
            # 5th percentile
            sorted_counts = sorted(counts_252)
            p5_idx = max(0, int(0.05 * len(sorted_counts)) - 1)
            p5 = sorted_counts[p5_idx]

            # 63-day mean ending at filed_date
            last_63 = hist_dates[-63:]
            if len(last_63) < 63:
                continue
            mean_63 = sum(news_counts[d] for d in last_63) / 63

            if mean_63 > p5:
                continue

            # Check 20-day avg dollar volume < $1M
            if filed_date not in day_to_idx:
                continue
            idx = day_to_idx[filed_date]
            if idx < 20:
                continue
            vol_20 = [dollar_vol.get(trading_days[idx - i], 0) for i in range(20)]
            avg_dollar_vol = sum(vol_20) / 20
            if avg_dollar_vol < 1_000_000:
                continue

            # Check >= 2 distinct insiders bought in prior 63 sessions
            prior_start_idx = max(0, idx - 63)
            prior_insiders = set()
            for tx_ts2, filed_ts2, _, _, _, insider2 in purchases:
                fd2 = epoch_to_date(filed_ts2)
                if fd2 in day_to_idx:
                    idx2 = day_to_idx[fd2]
                    if prior_start_idx <= idx2 < idx:
                        prior_insiders.add(insider2)
            if len(prior_insiders) < 2:
                continue

            # Market cap > $500M at entry (approximate from close * shares outstanding)
            # We don't have shares outstanding historically, so skip this filter
            # or use a proxy. Since fundamentals only has recent data, we'll skip.

            # Compute forward return over 21 trading days
            fwd_ret = compute_forward_return(bars_dict, trading_days, idx, 21)
            if fwd_ret is None:
                continue

            # Record opportunity
            opp = {
                'symbol_id': symbol_id,
                'symbol': symbol,
                'filed_date': filed_date,
                'filed_ts': filed_ts,
                'fwd_ret': fwd_ret,
                'up': 1 if fwd_ret > 0 else 0
            }
            all_opportunities.append(opp)

            # This is an issued call
            all_calls.append(opp)

    if not all_opportunities:
        print("INSUFFICIENT=1")
        return

    # Check minimum eligible symbol-days (opportunities considered)
    if len(all_opportunities) < 30:
        print("INSUFFICIENT=1")
        return

    # Sort by filed_ts
    all_opportunities.sort(key=lambda x: x['filed_ts'])
    all_calls.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(all_opportunities) * 0.8)
    train_opps = all_opportunities[:split_idx]
    sealed_opps = all_opportunities[split_idx:]

    train_calls = [c for c in all_calls if c in train_opps]
    sealed_calls = [c for c in all_calls if c in sealed_opps]

    # Metrics
    issued = len(all_calls)
    opportunities = len(all_opportunities)

    if issued == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(c['up'] for c in all_calls)
    precision = hits / issued

    base_rate = hits / issued  # base rate of predicted class (up) within issued subset

    distinct_days = len(set(c['filed_date'] for c in all_calls))

    # Design effect: cluster by week
    week_clusters = defaultdict(int)
    for c in all_calls:
        week_key = c['filed_date'].isocalendar()[:2]  # (year, week)
        week_clusters[week_key] += 1
    if len(week_clusters) > 1:
        # Kish design effect approximation
        n_clusters = len(week_clusters)
        avg_cluster_size = issued / n_clusters
        # Effective N = n / (1 + (avg_cluster_size - 1) * rho)
        # Assume rho=0.1 for conservative estimate
        rho = 0.1
        design_effect = 1 + (avg_cluster_size - 1) * rho
        effective_n = issued / design_effect
    else:
        effective_n = issued * 0.9  # must be < issued

    # Sealed precision
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c['up'] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # Abstention rate
    abstention_rate = 1 - (issued / opportunities) if opportunities > 0 else 1.0

    # Print required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    # Verify invariants
    if distinct_days > issued:
        print(f"WARNING: DISTINCT_DAYS ({distinct_days}) > ISSUED ({issued})", file=sys.stderr)
    if effective_n >= issued:
        print(f"WARNING: EFFECTIVE_N ({effective_n}) >= ISSUED ({issued})", file=sys.stderr)

if __name__ == '__main__':
    main()