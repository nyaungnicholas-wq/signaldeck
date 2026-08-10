# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 490
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Check for gold series in macro_series
    gold_rows = cur.execute("""
        SELECT DISTINCT series FROM macro_series 
        WHERE series LIKE '%gold%' OR series LIKE '%Gold%' OR series LIKE '%GOLD%'
    """).fetchall()
    if len(gold_rows) != 1:
        print("INSUFFICIENT=1")
        return
    gold_series = gold_rows[0][0]

    # Get gold daily prices
    gold_data = cur.execute("""
        SELECT ts, value FROM macro_series 
        WHERE series = ? ORDER BY ts
    """, (gold_series,)).fetchall()

    if len(gold_data) < 252:
        print("INSUFFICIENT=1")
        return

    # Build gold price lookup and compute returns
    gold_prices = {}
    for row in gold_data:
        dt = datetime.utcfromtimestamp(row[0]).strftime('%Y-%m-%d')
        gold_prices[dt] = row[1]

    # Compute 5-day returns and rolling percentiles
    gold_returns = {}
    gold_dates = sorted(gold_prices.keys())
    for i, dt in enumerate(gold_dates):
        if i >= 5:
            prev_dt = gold_dates[i-5]
            ret = gold_prices[dt] / gold_prices[prev_dt] - 1
            gold_returns[dt] = ret

    # Compute rolling 252-day 90th percentile
    gold_percentiles = {}
    for i, dt in enumerate(gold_dates):
        window_returns = []
        for j in range(max(0, i-251), i+1):
            d = gold_dates[j]
            if d in gold_returns:
                window_returns.append(gold_returns[d])
        if len(window_returns) >= 100:
            window_returns.sort()
            idx = int(len(window_returns) * 0.9) - 1
            if idx < 0:
                idx = 0
            gold_percentiles[dt] = window_returns[idx]

    # Get all symbols with sufficient daily history
    symbols = cur.execute("""
        SELECT id FROM symbols WHERE active = 1
    """).fetchall()
    symbol_ids = [s[0] for s in symbols]

    # Get all daily bars
    all_bars = cur.execute("""
        SELECT symbol_id, ts, close FROM bars WHERE tf = '1d' ORDER BY ts
    """).fetchall()

    # Organize bars by symbol and date
    symbol_bars = defaultdict(list)
    for bar in all_bars:
        sid, ts, close = bar
        dt = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        symbol_bars[sid].append((dt, close))

    # Filter symbols with at least 120 trading days
    valid_symbols = [sid for sid, bars in symbol_bars.items() if len(bars) >= 120]

    # Compute gold beta for each symbol-day (120-day window)
    # First, align gold returns with stock dates
    gold_aligned = {}
    for dt in gold_returns:
        gold_aligned[dt] = gold_returns[dt]

    # We'll compute beta in a streaming fashion for each symbol
    betas = {}  # key: (symbol_id, dt), value: beta

    for sid in valid_symbols:
        bars = symbol_bars[sid]
        # Compute daily returns for the symbol
        stock_returns = {}
        for i in range(1, len(bars)):
            dt_curr, close_curr = bars[i]
            dt_prev, close_prev = bars[i-1]
            if close_prev != 0:
                ret = close_curr / close_prev - 1
                stock_returns[dt_curr] = (ret, dt_prev)

        # For each day where we have both stock and gold returns
        dates = [dt for dt, _ in bars]
        for i in range(120, len(dates)):
            dt = dates[i]
            if dt not in stock_returns or dt not in gold_aligned:
                continue

            # Get 120-day window of returns
            window_dates = dates[i-120:i]
            x_vals = []
            y_vals = []
            for wd in window_dates:
                if wd in stock_returns and wd in gold_aligned:
                    y_vals.append(stock_returns[wd][0])
                    x_vals.append(gold_aligned[wd])

            if len(x_vals) < 120:
                continue

            # Compute beta: covariance(x,y)/variance(x)
            mean_x = sum(x_vals) / len(x_vals)
            mean_y = sum(y_vals) / len(y_vals)
            var_x = sum((xi - mean_x)**2 for xi in x_vals) / len(x_vals)
            cov_xy = sum((xi - mean_x)*(yi - mean_y) for xi, yi in zip(x_vals, y_vals)) / len(x_vals)

            if var_x > 1e-12:
                beta = cov_xy / var_x
                betas[(sid, dt)] = beta

    # Get prediction outcomes for horizon=21
    outcomes = {}
    rows = cur.execute("""
        SELECT symbol_id, ts, up FROM prediction_outcomes 
        WHERE horizon = 21
    """).fetchall()
    for row in rows:
        sid, ts, up = row
        dt = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
        outcomes[(sid, dt)] = up

    # Issue calls based on conditions
    calls = []
    all_days = set()

    for sid in valid_symbols:
        bars = symbol_bars[sid]
        for i in range(120, len(bars)):
            dt, _ = bars[i]

            # Check gold return condition
            if dt not in gold_percentiles or dt not in gold_returns:
                continue
            gold_ret = gold_returns[dt]
            gold_pct = gold_percentiles[dt]
            if gold_ret < gold_pct:
                continue

            # Check beta condition
            if (sid, dt) not in betas:
                continue

            # Get beta percentile cross-section on this day
            day_betas = {s: b for (s, d), b in betas.items() if d == dt}
            if len(day_betas) < 10:
                continue

            beta_vals = sorted(day_betas.values())
            top_decile = beta_vals[int(len(beta_vals) * 0.9)]
            if betas[(sid, dt)] < top_decile:
                continue

            # Issue call
            calls.append((sid, dt))
            all_days.add(dt)

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Split into train and sealed (most recent 20%)
    all_call_dates = sorted(list(all_days))
    split_idx = int(len(all_call_dates) * 0.8)
    sealed_dates = set(all_call_dates[split_idx:])

    # Compute metrics for all calls
    total_issued = len(calls)
    opportunities = len(all_days)  # one observation per day considered? Actually we issue per symbol-day, but base count is symbol-days that meet conditions
    # Actually, opportunities = number of symbol-days that met entry conditions
    opportunities = total_issued  # because we only issue when conditions met

    hits = 0
    base_up = 0
    for sid, dt in calls:
        if (sid, dt) in outcomes and outcomes[(sid, dt)] == 1:
            hits += 1
            base_up += 1

    precision = hits / total_issued if total_issued > 0 else 0
    base_rate = base_up / total_issued if total_issued > 0 else 0

    distinct_days = len(all_days)

    # Compute design effect for day clustering
    # Group calls by day
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for sid, dt in calls:
        day_counts[dt] += 1
        if (sid, dt) in outcomes and outcomes[(sid, dt)] == 1:
            day_hits[dt] += 1

    k = len(day_counts)  # number of clusters
    if k <= 1:
        # Cannot compute design effect with one cluster
        design_effect = 1.0
    else:
        # Compute ICC
        total_calls = total_issued
        mean_p = hits / total_calls

        # Between-cluster variance
        ssb = 0.0
        for dt, cnt in day_counts.items():
            if cnt > 0:
                day_prop = day_hits[dt] / cnt
                ssb += cnt * (day_prop - mean_p)**2
        msb = ssb / (k - 1) if k > 1 else 0

        # Within-cluster variance
        ssw = 0.0
        for dt, cnt in day_counts.items():
            if cnt > 0:
                day_prop = day_hits[dt] / cnt
                for _ in range(cnt):
                    # Binary outcome: either hit or miss
                    # Contribution to within-cluster variance
                    # For binary, variance = p(1-p)
                    ssw += day_prop * (1 - day_prop) * cnt
        n = total_calls
        m0 = (1 / (k - 1)) * (n - sum(cnt**2 for cnt in day_counts.values()) / n) if k > 1 else 1
        if msb > 1e-12 and m0 > 1:
            icc = (msb - ssw/n) / (msb + (m0 - 1) * (ssw/n)) if (msb + (m0 - 1) * (ssw/n)) > 1e-12 else 0
            design_effect = 1 + (m0 - 1) * icc
        else:
            design_effect = 1.0

    effective_n = total_issued / design_effect

    # Sealed era metrics
    sealed_calls = [(sid, dt) for sid, dt in calls if dt in sealed_dates]
    sealed_issued = len(sealed_calls)
    sealed_hits = 0
    for sid, dt in sealed_calls:
        if (sid, dt) in outcomes and outcomes[(sid, dt)] == 1:
            sealed_hits += 1
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.4f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

    conn.close()

if __name__ == "__main__":
    main()