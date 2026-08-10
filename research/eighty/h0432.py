# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 431
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys

DB_PATH = 'data/signaldeck.db'
HORIZON = 21
YTD_THRESHOLD = -0.30
LIQUIDITY_THRESHOLD = 5_000_000
MIN_HISTORY = 250

def main():
    try:
        conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return

    cur = conn.cursor()

    # Get all daily bar timestamps and identify December trading days
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_trading_days = [row[0] for row in cur.fetchall()]
    if not all_trading_days:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Identify December trading days and group by year
    december_days = {}
    for ts in all_trading_days:
        # Convert unix epoch to calendar year
        import datetime
        dt = datetime.datetime.utcfromtimestamp(ts)
        if dt.month == 12:
            year = dt.year
            if year not in december_days:
                december_days[year] = []
            december_days[year].append(ts)

    # For each year, get last 10 trading days of December
    target_days = []
    for year, days in sorted(december_days.items()):
        if len(days) >= 10:
            target_days.extend(days[-10:])

    if not target_days:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get symbols with sufficient history
    cur.execute("""
        SELECT symbol_id 
        FROM bars 
        WHERE tf='1d' 
        GROUP BY symbol_id 
        HAVING COUNT(*) >= ?
    """, (MIN_HISTORY,))
    eligible_symbols = {row[0] for row in cur.fetchall()}

    if not eligible_symbols:
        print("INSUFFICIENT=1")
        conn.close()
        return

    opportunities = 0
    calls = []
    issued_per_symbol_season = set()

    for symbol_id in eligible_symbols:
        # Get all daily bars for this symbol
        cur.execute("""
            SELECT ts, close, volume 
            FROM bars 
            WHERE symbol_id=? AND tf='1d' 
            ORDER BY ts
        """, (symbol_id,))
        bars = cur.fetchall()
        if len(bars) < MIN_HISTORY + 22:  # Need extra for horizon
            continue

        # Create lookup dict
        bar_dict = {}
        for ts, close, volume in bars:
            bar_dict[ts] = (close, volume)

        # Get all trading days for this symbol
        symbol_days = [ts for ts, _, _ in bars]

        # Check each target day
        for entry_ts in target_days:
            if entry_ts not in bar_dict:
                continue

            # Find index of entry day in symbol's trading days
            try:
                idx = symbol_days.index(entry_ts)
            except ValueError:
                continue

            # Need at least MIN_HISTORY days before entry
            if idx < MIN_HISTORY - 1:
                continue

            # Check liquidity: trailing 20-day average dollar volume
            if idx < 19:
                continue

            avg_vol = 0
            for i in range(idx - 19, idx + 1):
                close, volume = bar_dict[symbol_days[i]]
                avg_vol += close * volume
            avg_vol /= 20.0

            if avg_vol < LIQUIDITY_THRESHOLD:
                continue

            # Calculate YTD return
            # Find first trading day of current year
            import datetime
            entry_date = datetime.datetime.utcfromtimestamp(entry_ts)
            year_start = datetime.datetime(entry_date.year, 1, 1)
            year_start_ts = int(year_start.timestamp())

            # Find first trading day of year for this symbol
            first_year_day = None
            for ts in symbol_days:
                if ts >= year_start_ts:
                    first_year_day = ts
                    break

            if first_year_day is None or first_year_day >= entry_ts:
                continue

            # Get close on first year day and prior day close
            first_close, _ = bar_dict[first_year_day]
            if idx == 0:
                continue
            prior_close, _ = bar_dict[symbol_days[idx - 1]]

            ytd_return = (prior_close - first_close) / first_close

            if ytd_return > YTD_THRESHOLD:
                continue

            # Check if we already issued a call this season
            season_key = (symbol_id, entry_date.year)
            if season_key in issued_per_symbol_season:
                continue

            # Need 21 trading days ahead for label
            if idx + 21 >= len(symbol_days):
                continue

            exit_ts = symbol_days[idx + 21]
            if exit_ts not in bar_dict:
                continue

            exit_close, _ = bar_dict[exit_ts]
            if exit_close > prior_close:
                hit = 1
            else:
                hit = 0

            opportunities += 1
            issued_per_symbol_season.add(season_key)
            calls.append((entry_ts, hit))

    conn.close()

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort calls by time
    calls.sort(key=lambda x: x[0])

    # Split into main and sealed (last 20%)
    n = len(calls)
    split_idx = int(n * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    # Calculate metrics
    issued = len(calls)
    if issued == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(hit for _, hit in calls)
    precision = hits / issued if issued > 0 else 0.0

    # Base rate of UP within issued calls
    base_rate = precision  # Since UP is the only class in issued calls

    # Distinct days
    distinct_days = len(set(entry_ts for entry_ts, _ in calls))

    # Design effect (clustering in time)
    # Simple approach: count unique months as proxy for independence
    import datetime
    months = set()
    for ts, _ in calls:
        dt = datetime.datetime.utcfromtimestamp(ts)
        months.add((dt.year, dt.month))
    
    design_effect = max(1.0, issued / len(months)) if months else 1.0
    effective_n = issued / design_effect

    # Sealed precision
    sealed_hits = sum(hit for _, hit in sealed_calls)
    sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()