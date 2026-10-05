# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 694
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

MECHANISM = "Insiders possess private information about fundamental improvement that first appears in news sentiment momentum before price; when 20-day news sentiment slope turns positive while 20-day price slope remains negative, insider open-market purchases confirm the leading indicator."
HORIZON = "20d"
UNIVERSE = "Symbols with >=252 daily bars, >=20 days of sentiment_features history, and >=1 insider open-market purchase (code='P') in the test window."
ENTRY = "On disclosure date (filed_ts) of an open-market insider purchase (code='P'), when 20-day slope of daily mean_score (sentiment_features) > 0 and 20-day slope of daily close (bars, tf='1d') < 0, both computed using data strictly prior to disclosure date."
ABSTAIN = "If fewer than 20 valid sentiment days or 20 valid price days in the lookback window, or if the insider trade is not an open-market purchase (code != 'P'), or if forward 20-day return cannot be computed from bars."
CLAIM = "Precision exceeds base rate by at least 0.10 at 20-day horizon, with effective sample size > 30."

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all insider open-market purchases with filed_ts (disclosure date)
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
        ORDER BY it.filed_ts
    """)
    insider_buys = cur.fetchall()
    if not insider_buys:
        print("INSUFFICIENT=1")
        return 0

    # Build daily close prices from bars (tf='1d') for all symbols
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_rows = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_symbol = {}
    for row in bars_rows:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append((row['ts'], row['close']))

    # Build daily sentiment mean_score from sentiment_features
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        ORDER BY symbol_id, day
    """)
    sent_rows = cur.fetchall()

    sent_by_symbol = {}
    for row in sent_rows:
        sid = row['symbol_id']
        if sid not in sent_by_symbol:
            sent_by_symbol[sid] = []
        # day is 'YYYY-MM-DD' string, convert to epoch at midnight UTC
        dt = datetime.strptime(row['day'], '%Y-%m-%d')
        epoch = int(dt.timestamp())
        sent_by_symbol[sid].append((epoch, row['mean_score']))

    # For each insider buy, compute slopes and forward return
    opportunities = []
    issued = []

    for buy in insider_buys:
        sid = buy['symbol_id']
        filed_ts = buy['filed_ts']
        filed_date = datetime.utcfromtimestamp(filed_ts).date()

        # Need bars and sentiment for this symbol
        if sid not in bars_by_symbol or sid not in sent_by_symbol:
            continue

        bars = bars_by_symbol[sid]
        sents = sent_by_symbol[sid]

        # Filter bars and sentiment to strictly before filed_ts
        prior_bars = [(ts, close) for ts, close in bars if ts < filed_ts]
        prior_sents = [(ts, score) for ts, score in sents if ts < filed_ts]

        if len(prior_bars) < 20 or len(prior_sents) < 20:
            continue

        # Take last 20 days for slope calculation
        recent_bars = prior_bars[-20:]
        recent_sents = prior_sents[-20:]

        # Compute 20-day slope (simple linear regression slope)
        def slope(points):
            n = len(points)
            if n < 2:
                return 0
            x = list(range(n))
            y = [p[1] for p in points]
            x_mean = sum(x) / n
            y_mean = sum(y) / n
            num = sum((x[i] - x_mean) * (y[i] - y_mean) for i in range(n))
            den = sum((x[i] - x_mean) ** 2 for i in range(n))
            return num / den if den != 0 else 0

        price_slope = slope(recent_bars)
        sent_slope = slope(recent_sents)

        # Entry condition
        if sent_slope > 0 and price_slope < 0:
            opportunities.append((filed_ts, filed_date, sid, buy['symbol']))

            # Compute forward 20-day return from bars
            # Find bars at or after filed_ts
            future_bars = [(ts, close) for ts, close in bars if ts >= filed_ts]
            if len(future_bars) >= 20:
                entry_close = future_bars[0][1]
                exit_close = future_bars[19][1]
                fwd_return = (exit_close - entry_close) / entry_close
                up = 1 if fwd_return > 0 else 0
                issued.append((filed_ts, filed_date, sid, up, fwd_return))

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # Hold out most recent 20% as sealed era
    issued.sort(key=lambda x: x[0])
    n_total = len(issued)
    n_sealed = max(1, int(n_total * 0.2))
    main_issued = issued[:-n_sealed]
    sealed_issued = issued[-n_sealed:]

    def compute_metrics(data):
        if not data:
            return 0, 0, 0, 0
        n = len(data)
        hits = sum(1 for d in data if d[3] == 1)
        precision = hits / n
        base_rate = hits / n  # base rate within issued subset
        distinct_days = len(set(d[1] for d in data))
        # Design effect approximation: 1 + (avg_per_day - 1) * 0.05
        avg_per_day = n / distinct_days if distinct_days > 0 else 1
        design_effect = 1 + (avg_per_day - 1) * 0.05
        effective_n = n / design_effect
        return precision, base_rate, distinct_days, effective_n

    main_prec, main_br, main_dd, main_en = compute_metrics(main_issued)
    sealed_prec, _, _, _ = compute_metrics(sealed_issued)

    print(f"ISSUED={len(main_issued)}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={main_prec:.6f}")
    print(f"BASE_RATE={main_br:.6f}")
    print(f"DISTINCT_DAYS={main_dd}")
    print(f"EFFECTIVE_N={main_en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())