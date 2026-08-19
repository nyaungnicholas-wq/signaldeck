# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 627
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from bisect import bisect_left, bisect_right

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Identify retail sales series
    cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%retail%' OR series LIKE '%RSAFS%' OR series LIKE '%RSXFS%' OR series LIKE '%RETAIL%'")
    retail_series = [row['series'] for row in cur.fetchall()]
    if not retail_series:
        print("INSUFFICIENT=1")
        return
    # Prefer RSAFS (Advance Retail Sales: Retail Trade)
    target_series = None
    for s in retail_series:
        if 'RSAFS' in s.upper():
            target_series = s
            break
    if not target_series:
        target_series = retail_series[0]

    # 2. Load retail sales data (ts, value)
    cur.execute("SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts", (target_series,))
    retail_rows = cur.fetchall()
    if len(retail_rows) < 61:
        print("INSUFFICIENT=1")
        return

    # Compute MoM % changes and trailing 60-month z-scores
    # retail_rows are monthly, ts = release date (assumed)
    mom_changes = []  # (ts, mom_pct)
    for i in range(1, len(retail_rows)):
        prev_val = retail_rows[i-1]['value']
        curr_val = retail_rows[i]['value']
        if prev_val != 0:
            mom = (curr_val - prev_val) / prev_val * 100.0
            mom_changes.append((retail_rows[i]['ts'], mom))

    if len(mom_changes) < 60:
        print("INSUFFICIENT=1")
        return

    # For each month, compute z-score vs trailing 60 MoM changes
    surprise_z = {}  # ts -> z-score
    for i in range(60, len(mom_changes)):
        window = [mom_changes[j][1] for j in range(i-60, i)]
        mean_w = sum(window) / len(window)
        std_w = math.sqrt(sum((x - mean_w)**2 for x in window) / len(window)) if len(window) > 1 else 0
        curr_ts, curr_mom = mom_changes[i]
        if std_w > 0:
            z = (curr_mom - mean_w) / std_w
            surprise_z[curr_ts] = z

    # 3. Get symbols with daily bars (tf='1d') and news since 2018
    # 2018-01-01 00:00:00 UTC = 1514764800
    START_EPOCH = 1514764800
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        JOIN news n ON n.symbol_id = s.id
        WHERE n.ts >= ?
    """, (START_EPOCH,))
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return
    symbol_ids = [s['id'] for s in symbols]
    sym_id_to_sym = {s['id']: s['symbol'] for s in symbols}

    # 4. For each symbol, load daily bar timestamps (trading days) and news headlines
    # We need: for each symbol, sorted list of trading days (from bars), and news counts per day
    # Since 1,777 symbols * ~1700 days = ~3M rows, do in batches or per symbol
    # Let's load all daily bar timestamps for these symbols
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bar_rows = cur.fetchall()

    # Group trading days by symbol
    sym_trading_days = defaultdict(list)
    for row in bar_rows:
        sym_trading_days[row['symbol_id']].append(row['ts'])

    # Load news: symbol_id, ts (unix epoch), count per day
    cur.execute(f"""
        SELECT symbol_id, ts FROM news
        WHERE symbol_id IN ({placeholders}) AND ts >= ?
        ORDER BY symbol_id, ts
    """, symbol_ids + [START_EPOCH])
    news_rows = cur.fetchall()

    # Group news by symbol and day (floor to UTC day)
    def day_floor(ts):
        return (ts // 86400) * 86400

    sym_news_days = defaultdict(lambda: defaultdict(int))
    for row in news_rows:
        d = day_floor(row['ts'])
        sym_news_days[row['symbol_id']][d] += 1

    # 5. For each symbol, compute 5-day headline counts and trailing 252-day terciles
    # We'll process each symbol's trading days in chronological order
    # For each trading day D (decision point), we need:
    #   - Most recent retail sales release on or before D
    #   - Symbol's 5-day headline count ending on D (or D-1 day? Use D)
    #   - Trailing 252 trading days of 5-day counts (up to D-1)
    #
    # Retail sales releases are monthly. For a given D, find max release_ts <= D.
    release_ts_sorted = sorted(surprise_z.keys())

    def get_latest_surprise(decision_ts):
        # binary search for rightmost release_ts <= decision_ts
        idx = bisect_right(release_ts_sorted, decision_ts) - 1
        if idx >= 0:
            return surprise_z[release_ts_sorted[idx]]
        return None

    # For each symbol, precompute 5-day headline counts for each trading day
    # 5-day window: [D-4 days, D] in calendar days? Hypothesis says "5-day headline count"
    # Use calendar days for news, trading days for decisions.
    # For each trading day D, sum news counts for calendar days in [day_floor(D)-4*86400, day_floor(D)]
    sym_5day_counts = defaultdict(dict)  # symbol_id -> {decision_ts: count}
    sym_5day_history = defaultdict(list)  # symbol_id -> list of (decision_ts, count) in order

    for sid in symbol_ids:
        trading_days = sym_trading_days.get(sid, [])
        if not trading_days:
            continue
        news_by_day = sym_news_days.get(sid, {})
        for td in trading_days:
            d_floor = day_floor(td)
            count = 0
            for offset in range(5):
                count += news_by_day.get(d_floor - offset * 86400, 0)
            sym_5day_counts[sid][td] = count
            sym_5day_history[sid].append((td, count))

    # 6. Generate calls
    calls = []  # (decision_ts, symbol_id, surprise_z, news_count, tercile_rank)
    for sid in symbol_ids:
        history = sym_5day_history.get(sid, [])
        if len(history) < 252:
            continue  # not enough history for tercile
        # We'll maintain a sorted list of past 5-day counts for tercile calculation
        # For each decision point i (0-indexed), trailing window is max(0, i-252) to i-1
        past_counts = []
        for i, (td, count) in enumerate(history):
            if i >= 252:
                # Remove count from i-252
                old_count = history[i-252][1]
                # Keep past_counts sorted for tercile
                pos = bisect_left(past_counts, old_count)
                if pos < len(past_counts) and past_counts[pos] == old_count:
                    past_counts.pop(pos)
            # Add previous day's count (i-1) if exists
            if i > 0:
                prev_count = history[i-1][1]
                insort_pos = bisect_right(past_counts, prev_count)
                past_counts.insert(insort_pos, prev_count)

            # Now at decision point i, we have trailing 252 days in past_counts (up to i-1)
            if len(past_counts) < 252:
                continue  # not enough trailing history yet

            # Check retail surprise
            z = get_latest_surprise(td)
            if z is None or z <= 1.0:
                continue

            # Check news tercile: bottom tercile = count <= 33rd percentile
            # Tercile boundaries: bottom 1/3, middle 1/3, top 1/3
            n = len(past_counts)
            tercile_idx = n // 3
            bottom_threshold = past_counts[tercile_idx - 1] if tercile_idx > 0 else past_counts[0]
            if count > bottom_threshold:
                continue

            # Both conditions met -> issue UP call
            calls.append((td, sid, z, count, bottom_threshold))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # 7. Get labels from prediction_outcomes for horizon=21 (trading days)
    # prediction_outcomes has horizon column. Assume horizon=21 means 21 trading days.
    call_keys = [(sid, td) for td, sid, _, _, _ in calls]
    # Build a set for fast lookup
    call_set = set(call_keys)

    # Query prediction_outcomes for these symbol/ts with horizon=21
    # But prediction_outcomes ts might not exactly match our decision_ts (could be same day different hour)
    # We'll match on symbol_id and ts (exact) and horizon=21
    # Since we have many calls, do a bulk query
    # Create a temporary table or use IN with pairs? SQLite doesn't support tuples in IN easily.
    # Instead, query all prediction_outcomes for our symbols with horizon=21 and ts in range
    min_ts = min(td for td, _ in call_keys)
    max_ts = max(td for td, _ in call_keys)
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, horizon, ts, up
        FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders}) AND horizon = 21 AND ts BETWEEN ? AND ?
    """, symbol_ids + [min_ts, max_ts])
    label_rows = cur.fetchall()

    labels = {}
    for row in label_rows:
        key = (row['symbol_id'], row['ts'])
        if key in call_set:
            labels[key] = row['up']  # 1 for up, 0 for down (assumed)

    # 8. Attach labels to calls
    labeled_calls = []
    for td, sid, z, nc, bt in calls:
        key = (sid, td)
        if key in labels:
            labeled_calls.append((td, sid, labels[key]))

    if not labeled_calls:
        print("INSUFFICIENT=1")
        return

    # 9. Split into sealed era (most recent 20% by time)
    labeled_calls.sort(key=lambda x: x[0])  # sort by decision_ts
    n_total = len(labeled_calls)
    split_idx = int(n_total * 0.8)
    train_calls = labeled_calls[:split_idx]
    sealed_calls = labeled_calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(1 for _, _, up in call_list if up == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate of predicted class (UP) within issued subset
        # Distinct UTC days among issued calls
        distinct_days = len(set(day_floor(td) for td, _, _ in call_list))
        # Design effect: approximate by 1 + (avg calls per day - 1) * intraclass_corr
        # Simple approximation: design_effect = issued / distinct_days (if clustered)
        # But must be >1. Use max(1.0, issued / distinct_days) but distinct_days <= issued
        if distinct_days > 0:
            design_effect = issued / distinct_days
        else:
            design_effect = 1.0
        if design_effect < 1.0:
            design_effect = 1.0
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_dd, train_en = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_dd, sealed_en = compute_metrics(sealed_calls)

    # Opportunities: total decision points considered (symbol-days where we evaluated conditions)
    # This is the number of (symbol, trading_day) pairs where we had enough history to check
    # We didn't track this explicitly. Let's compute: for each symbol, number of trading days with >=252 history
    opportunities = 0
    for sid in symbol_ids:
        history = sym_5day_history.get(sid, [])
        if len(history) < 252:
            continue
        # Count days where trailing 252 available (i.e., index >= 252)
        opportunities += max(0, len(history) - 252)

    # Print required lines
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_br:.6f}")
    print(f"DISTINCT_DAYS={train_dd}")
    print(f"EFFECTIVE_N={train_en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == "__main__":
    main()