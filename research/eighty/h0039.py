import sqlite3
import datetime
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    # Check for required tables
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    required = {'bars', 'symbols'}
    if not required.issubset(tables):
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get all symbols (U.S.-listed common stocks)
    cur.execute("SELECT id, symbol FROM symbols WHERE market = 'stocks'")
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Preload all bars into memory for speed (we need historical prices)
    cur.execute("SELECT symbol_id, tf, ts, close FROM bars WHERE tf = '1d'")
    bars = {}
    for sid, tf, ts, close in cur.fetchall():
        bars.setdefault(sid, []).append((ts, close))
    conn.close()

    # For each symbol, sort bars by timestamp
    for sid in bars:
        bars[sid].sort()

    # Helper to find bar index at timestamp
    def find_bar_idx(sid, target_ts):
        bar_list = bars.get(sid, [])
        # Binary search for timestamp
        lo, hi = 0, len(bar_list) - 1
        while lo <= hi:
            mid = (lo + hi) // 2
            if bar_list[mid][0] < target_ts:
                lo = mid + 1
            elif bar_list[mid][0] > target_ts:
                hi = mid - 1
            else:
                return mid
        # If not found, return -1
        return -1

    # Get all unique year-end trading days
    # We need last trading day of each calendar year from bars
    all_ts = []
    for sid in bars:
        for ts, close in bars[sid]:
            all_ts.append(ts)
    if not all_ts:
        print("INSUFFICIENT=1")
        return
    all_ts = sorted(set(all_ts))
    # Convert to datetime (UTC)
    dt_ts = [datetime.datetime.utcfromtimestamp(ts) for ts in all_ts]

    # Find year-end trading days: last trading day before Jan 1 of each year
    year_ends = {}  # year -> (year_end_ts, next_year_start_ts)
    years = sorted(set(dt.year for dt in dt_ts))
    for i, year in enumerate(years):
        # Find last trading day of year
        last_ts = None
        first_next = None
        for j, dt in enumerate(dt_ts):
            if dt.year == year:
                last_ts = all_ts[j]
            if year+1 in years and dt.year == year+1:
                if first_next is None:
                    first_next = all_ts[j]
                break
        if last_ts is not None and (year+1) in years and first_next is not None:
            year_ends[year] = (last_ts, first_next)

    if len(year_ends) < 2:
        print("INSUFFICIENT=1")
        return

    # Collect decision points: each (symbol, year) where we have data at year-end and next T
    opportunities = []  # (symbol_id, year, T_ts, year_end_ts, close_at_T, low_20d_at_T, ytd_return, ...)
    for year, (ye_ts, T_ts) in year_ends.items():
        for sid in symbols:
            s_id = sid[0]
            bar_list = bars.get(s_id, [])
            if not bar_list:
                continue
            # Find year-end bar index
            idx_ye = find_bar_idx(s_id, ye_ts)
            if idx_ye == -1:
                continue
            # Find T bar index
            idx_T = find_bar_idx(s_id, T_ts)
            if idx_T == -1:
                continue
            # Ensure enough history: at least 60 days before T for avg volume, 20 days for low, 10 days before T for catalyst check, etc.
            if idx_T < 60:
                continue
            # Price at T must be >= $5
            close_T = bar_list[idx_T][1]
            if close_T < 5.0:
                continue
            # Compute average daily dollar volume over prior 60 sessions at T
            # dollar volume = close * volume; but volume not in bars? We only have close. We cannot compute dollar volume without volume.
            # Wait: bars schema does not include volume? Actually it does: bars(symbol_id, tf, ts, open, high, low, close, volume)
            # We didn't select volume earlier. Let's adjust: we need to reload bars with volume.
            # We must restart with volume.
            # Since we already closed conn, we cannot. We must reopen.
            # But we are already in the main function. We'll reopen.
            # Actually, we can't because we closed the connection. Let's restructure: we'll do all in one connection.
            # We'll break and redo.
            print("INSUFFICIENT=1")
            return
    # We need to redo with volume. Let's start over in a cleaner way.

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    # Check tables
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    if not {'bars', 'symbols'}.issubset(tables):
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get all symbols (U.S.-listed common stocks)
    cur.execute("SELECT id, symbol FROM symbols WHERE market = 'stocks'")
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Load all daily bars with volume
    cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf = '1d'")
    raw = cur.fetchall()
    conn.close()

    # Organize bars per symbol
    bars = {}
    for sid, ts, close, vol in raw:
        bars.setdefault(sid, []).append((ts, close, vol))

    # Sort each symbol's bars by timestamp
    for sid in bars:
        bars[sid].sort(key=lambda x: x[0])

    # Helper to find bar index at timestamp (binary search)
    def find_bar_idx(sid, target_ts):
        bar_list = bars.get(sid, [])
        lo, hi = 0, len(bar_list) - 1
        while lo <= hi:
            mid = (lo + hi) // 2
            if bar_list[mid][0] < target_ts:
                lo = mid + 1
            elif bar_list[mid][0] > target_ts:
                hi = mid - 1
            else:
                return mid
        return -1

    # Get all unique timestamps to find year-end trading days
    all_ts_set = set()
    for sid in bars:
        for ts, _, _ in bars[sid]:
            all_ts_set.add(ts)
    if not all_ts_set:
        print("INSUFFICIENT=1")
        return
    all_ts = sorted(all_ts_set)

    # Group by year to find last trading day of each year and first trading day of next year
    ts_by_year = {}
    for ts in all_ts:
        year = datetime.datetime.utcfromtimestamp(ts).year
        ts_by_year.setdefault(year, []).append(ts)
    years = sorted(ts_by_year.keys())
    if len(years) < 2:
        print("INSUFFICIENT=1")
        return

    year_ends = {}  # year -> (ye_ts, T_ts)
    for i in range(len(years)-1):
        year = years[i]
        next_year = years[i+1]
        ye_ts = max(ts_by_year[year])
        T_ts = min(ts_by_year[next_year])
        year_ends[year] = (ye_ts, T_ts)

    # Collect opportunities: decision points at each (symbol, year)
    opportunities = []  # (sid, year, T_ts, ye_ts)
    for year, (ye_ts, T_ts) in year_ends.items():
        for sid in symbols:
            s_id = sid[0]
            bar_list = bars.get(s_id, [])
            if not bar_list:
                continue
            idx_ye = find_bar_idx(s_id, ye_ts)
            idx_T = find_bar_idx(s_id, T_ts)
            if idx_ye == -1 or idx_T == -1:
                continue
            # Need at least 60 days before T for avg volume
            if idx_T < 60:
                continue
            opportunities.append((s_id, year, T_ts, ye_ts, idx_T))

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # For each opportunity, apply entry and abstain rules
    issued_calls = []  # (sid, T_ts, label) where label = 1 if correct up, else 0
    abstained = 0
    for sid, year, T_ts, ye_ts, idx_T in opportunities:
        bar_list = bars[sid]
        # Price at T must be >= $5
        close_T = bar_list[idx_T][1]
        if close_T < 5.0:
            continue

        # Average daily dollar volume >= $10M over prior 60 sessions at T
        total_dollar_vol = 0.0
        for j in range(idx_T-60, idx_T):
            total_dollar_vol += bar_list[j][1] * bar_list[j][2]  # close * volume
        avg_dollar_vol = total_dollar_vol / 60
        if avg_dollar_vol < 10_000_000:
            continue

        # 20-day close low at T
        low_20d = min(bar_list[idx_T-20+k][1] for k in range(21))  # 20 days inclusive? We need close low over prior 20 sessions including T? Usually look back 20 bars before T. Let's define: low_20d = min(close for last 20 bars up to and including T)
        # Actually, we want the 20-day close low at T, so the lowest close in the 20 days ending at T.
        # We'll take indices from idx_T-19 to idx_T (20 bars).
        low_20d = min(bar_list[idx_T-19 + k][1] for k in range(20)) if idx_T >= 19 else close_T
        # Check if close_T is within 5% of low_20d
        if close_T > low_20d * 1.05:
            continue

        # Year-to-date return: from first trading day of year to ye_ts
        first_ts_year = min(ts_by_year[year])
        idx_first = find_bar_idx(sid, first_ts_year)
        if idx_first == -1:
            continue
        close_first = bar_list[idx_first][1]
        close_ye = bar_list[idx_ye][1]
        ytd_return = (close_ye - close_first) / close_first
        if ytd_return > -0.25:  # need down at least 25%
            continue

        # No non-tax catalyst in prior 10 sessions: we don't have event data, so we skip this condition (assume none).
        # Abstain criteria:
        # 1. Earnings scheduled within 10 sessions or before horizon end: no data -> assume none.
        # 2. 5-day realized volatility in top cross-sectional decile: we cannot compute cross-sectionally without the full universe at each time. We'll skip this due to data limitation.
        # 3. Merger, offering, buyback, activist 13D, guidance change, or dividend cut in prior 20 sessions: no event data -> assume none.
        # 4. Negative book equity or going-concern: no financial data -> assume none.
        # 5. Price < $5 already checked.

        # We have insufficient data for most abstain criteria and catalyst checks.
        # According to the task, if data is insufficient, we must print INSUFFICIENT=1 and exit.
        print("INSUFFICIENT=1")
        return

    # If we reached here, we still lack data for labels (prediction_outcomes or actual forward returns)
    # We need to compute forward return over next 20 trading days from T.
    # But we don't have the actual forward return in the schema. We have prediction_outcomes which might have forward returns, but we are to derive labels from bars closes or from prediction_outcomes.up / fwd_return.
    # We could use prediction_outcomes, but we need to match symbol, time, horizon.
    # However, we are to test the hypothesis independently, so we should compute from bars: the close 20 trading days after T.
    # But we don't have that data because we only loaded up to the latest bars? Actually we loaded all bars, so we can compute forward return if we have the bar 20 days after T.
    # However, we must respect as-of discipline: we cannot use bars at or after the label window to inform the call. But for the label (forward return), we need to look at future bars.
    # That's allowed for the label, but not for the entry criteria.
    # So we can compute the forward return from bars.

    # We need to find the index of the bar 20 trading days after T.
    # We'll go through opportunities again to compute labels.

    # Reset for label computation
    issued_calls = []
    abstained = 0
    for sid, year, T_ts, ye_ts, idx_T in opportunities:
        bar_list = bars[sid]
        close_T = bar_list[idx_T][1]
        if close_T < 5.0:
            continue
        # Recompute other criteria quickly? We already filtered in the previous loop, but we didn't store results. We'll recompute everything again in a single pass.

    # We need to restructure: we'll do a single pass through opportunities and apply all filters, then for those that pass, compute forward return label.

    # But note: we already printed INSUFFICIENT=1 due to missing catalyst data. So we cannot proceed.

    # Therefore, we must conclude insufficient data.

    print("INSUFFICIENT=1")
    return

if __name__ == "__main__":
    main()