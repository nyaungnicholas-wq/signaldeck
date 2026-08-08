# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 350
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all trading days from daily bars
    cur.execute("""
        SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts
    """)
    trading_days = [row['ts'] for row in cur.fetchall()]
    if not trading_days:
        print("INSUFFICIENT=1")
        return

    # Convert to datetime for easier manipulation
    trading_dates = [datetime.utcfromtimestamp(ts).date() for ts in trading_days]
    ts_to_date = dict(zip(trading_days, trading_dates))
    date_to_ts = {d: ts for ts, d in zip(trading_days, trading_dates)}

    # Sealed era: most recent 20% of trading days
    split_idx = int(len(trading_days) * 0.8)
    sealed_start_ts = trading_days[split_idx] if split_idx < len(trading_days) else trading_days[-1]
    sealed_start_date = ts_to_date[sealed_start_ts]

    # Get symbols with daily bars
    cur.execute("""
        SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'
    """)
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]

    # Load daily bars: symbol_id -> {date: close}
    cur.execute("""
        SELECT symbol_id, ts, close FROM bars WHERE tf = '1d' ORDER BY symbol_id, ts
    """)
    bars_by_symbol = defaultdict(dict)
    for row in cur.fetchall():
        d = ts_to_date[row['ts']]
        bars_by_symbol[row['symbol_id']][d] = row['close']

    # Load daily news sentiment: symbol_id -> {date: avg_score}
    cur.execute("""
        SELECT symbol_id, ts, score FROM news WHERE score IS NOT NULL ORDER BY symbol_id, ts
    """)
    news_by_symbol = defaultdict(lambda: defaultdict(list))
    for row in cur.fetchall():
        d = datetime.utcfromtimestamp(row['ts']).date()
        news_by_symbol[row['symbol_id']][d].append(row['score'])

    news_daily = defaultdict(dict)
    for sym, day_scores in news_by_symbol.items():
        for d, scores in day_scores.items():
            news_daily[sym][d] = sum(scores) / len(scores)

    # Load daily StockTwits bullish-bearish: symbol_id -> {date: bullish - bearish}
    cur.execute("""
        SELECT symbol_id, ts, bullish, bearish FROM stocktwits_sentiment ORDER BY symbol_id, ts
    """)
    st_by_symbol = defaultdict(lambda: defaultdict(list))
    for row in cur.fetchall():
        d = datetime.utcfromtimestamp(row['ts']).date()
        st_by_symbol[row['symbol_id']][d].append(row['bullish'] - row['bearish'])

    st_daily = defaultdict(dict)
    for sym, day_vals in st_by_symbol.items():
        for d, vals in day_vals.items():
            st_daily[sym][d] = sum(vals) / len(vals)

    # For each symbol, align data on trading days
    issued_calls = []  # list of (symbol_id, decision_date, is_sealed, hit)
    opportunities = 0

    for sym in symbol_ids:
        bars = bars_by_symbol.get(sym, {})
        news = news_daily.get(sym, {})
        st = st_daily.get(sym, {})

        # Get trading days where this symbol has bars
        sym_trading_days = sorted([d for d in trading_dates if d in bars])
        if len(sym_trading_days) < 252 + 5:  # need 252 history + 5 forward
            continue

        # Build aligned series for this symbol
        aligned = []
        for d in sym_trading_days:
            close = bars[d]
            news_score = news.get(d)
            st_score = st.get(d)
            aligned.append((d, close, news_score, st_score))

        # Need at least 252 days of overlapping data for trailing window
        # We'll iterate through each day as decision day t
        for i in range(252, len(aligned) - 5):
            d = aligned[i][0]
            decision_ts = date_to_ts[d]

            # Trailing 252 days: indices i-252 to i-1 (inclusive)
            trailing_news = [aligned[j][2] for j in range(i-252, i) if aligned[j][2] is not None]
            trailing_st = [aligned[j][3] for j in range(i-252, i) if aligned[j][3] is not None]

            # Check data sufficiency
            if len(trailing_news) < 60 or len(trailing_st) < 60:
                continue
            if max(trailing_news) == min(trailing_news) or max(trailing_st) == min(trailing_st):
                continue

            # Current day values
            news_today = aligned[i][2]
            st_today = aligned[i][3]
            if news_today is None or st_today is None:
                continue

            # Condition (c): close-to-close return between -2% and +2%
            close_today = aligned[i][1]
            close_yesterday = aligned[i-1][1]
            ret_1d = (close_today - close_yesterday) / close_yesterday
            if ret_1d < -0.02 or ret_1d > 0.02:
                continue

            opportunities += 1

            # Percentiles
            news_p10 = sorted(trailing_news)[int(0.10 * len(trailing_news))]
            st_p90 = sorted(trailing_st)[int(0.90 * len(trailing_st))]

            # Entry conditions
            if news_today <= news_p10 and st_today >= st_p90:
                # Compute 5-day forward return
                close_t5 = aligned[i+5][1]
                fwd_ret_5d = (close_t5 - close_today) / close_today
                hit = 1 if fwd_ret_5d < 0 else 0  # DOWN call hits if return < 0

                is_sealed = d >= sealed_start_date
                issued_calls.append((sym, d, is_sealed, hit))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Compute metrics
    total_issued = len(issued_calls)
    hits = sum(c[3] for c in issued_calls)
    precision = hits / total_issued if total_issued > 0 else 0.0

    # Base rate within issued subset: proportion of DOWN (negative return) in issued calls
    base_rate = 1 - precision  # since hit=1 means DOWN (negative return), base rate of DOWN class

    # Distinct days among issued calls
    distinct_days = len(set(c[1] for c in issued_calls))

    # Design effect for day clustering
    # Group by day, count calls per day
    day_counts = defaultdict(int)
    for c in issued_calls:
        day_counts[c[1]] += 1
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: use average cluster size as design effect proxy
    # Effective N = N / design_effect
    if day_counts:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        design_effect = max(1.0, avg_cluster)  # at least 1
        effective_n = total_issued / design_effect
    else:
        effective_n = float(total_issued)

    # Sealed era metrics
    sealed_calls = [c for c in issued_calls if c[2]]
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c[3] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # Print required lines
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()