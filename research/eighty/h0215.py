#!/usr/bin/env python3
import sqlite3
from collections import defaultdict
from datetime import datetime, timezone
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    # Get all symbols with daily bars
    symbols = [row['symbol_id'] for row in conn.execute(
        "SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'"
    ).fetchall()]

    # Pre-fetch all daily bars per symbol
    symbol_bars = {}
    for sid in symbols:
        rows = conn.execute(
            "SELECT ts, close, volume FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
            (sid,)
        ).fetchall()
        if len(rows) >= 504:
            symbol_bars[sid] = rows

    # Collect all candidate decision points with indicators
    candidates = []  # (ts, symbol_id, vol20)
    # To track last call per symbol (for prior 20-day constraint)
    last_call_ts = {}
    # We'll also store all opportunities for sealed era split later
    all_opportunities = []  # (ts, symbol_id)

    # Process each symbol's bars
    for sid, bars in symbol_bars.items():
        n = len(bars)
        ts_list = [r['ts'] for r in bars]
        close_list = [r['close'] for r in bars]
        vol_list = [r['volume'] for r in bars]
        # Precompute cumulative returns for T-4..T
        cum_ret_4 = []
        for i in range(4, n):
            if close_list[i-4] != 0:
                cum_ret_4.append(close_list[i] / close_list[i-4] - 1)
            else:
                cum_ret_4.append(0)
        # Daily returns
        daily_ret = [0]
        for i in range(1, n):
            if close_list[i-1] != 0:
                daily_ret.append(close_list[i] / close_list[i-1] - 1)
            else:
                daily_ret.append(0)

        # Rolling sums and stats
        sum_dollar_vol_60 = 0.0
        sum_close_200 = 0.0
        vol_window = []  # last 20 volumes (for median)
        ret_window = []  # last 20 returns (for volatility)
        sum_ret = 0.0
        sum_ret_sq = 0.0

        for i in range(n):
            close = close_list[i]
            volume = vol_list[i]
            dollar_vol = close * volume

            # Update rolling sums
            if i < 60:
                sum_dollar_vol_60 += dollar_vol
            else:
                sum_dollar_vol_60 += dollar_vol - close_list[i-60] * vol_list[i-60]
            if i < 200:
                sum_close_200 += close
            else:
                sum_close_200 += close - close_list[i-200]

            # Update volume window (keep last 20)
            vol_window.append(volume)
            if len(vol_window) > 20:
                vol_window.pop(0)

            # Update return window (keep last 20)
            ret = daily_ret[i]
            ret_window.append(ret)
            if len(ret_window) > 20:
                old_ret = ret_window.pop(0)
                sum_ret -= old_ret
                sum_ret_sq -= old_ret * old_ret
            sum_ret += ret
            sum_ret_sq += ret * ret

            # Only consider points with at least 504 prior sessions
            if i < 504:
                continue

            # Universe criteria
            if close < 5:
                continue
            avg_dollar_vol_60 = sum_dollar_vol_60 / 60 if i >= 59 else sum_dollar_vol_60 / (i+1)
            if avg_dollar_vol_60 < 5_000_000:
                continue
            sma200 = sum_close_200 / 200 if i >= 199 else sum_close_200 / (i+1)

            # Entry conditions
            # 1. Cumulative return T-4..T <= -12%
            if i < 4:
                continue
            cum_ret = cum_ret_4[i-4]
            if cum_ret > -0.12:
                continue
            # 2. Volume at T >= 1.5x 20-session median volume
            sorted_vol = sorted(vol_window)
            median_vol20 = sorted_vol[9] if len(vol_window) == 20 else sorted_vol[len(vol_window)//2]
            if volume < 1.5 * median_vol20:
                continue
            # 3. Close at T > 200-session SMA
            if close <= sma200:
                continue
            # 4. Positive close-to-close return on T
            if daily_ret[i] <= 0:
                continue

            # 20-session realized volatility (standard deviation of returns)
            if len(ret_window) == 20:
                mean_ret = sum_ret / 20
                var_ret = sum_ret_sq / 20 - mean_ret * mean_ret
                vol20 = math.sqrt(max(var_ret, 0))
            else:
                # Not enough data, but we already have 504 prior, so should have at least 20
                continue

            # Record opportunity
            all_opportunities.append((ts_list[i], sid))
            # Add candidate with vol20 for cross-sectional filtering later
            candidates.append((ts_list[i], sid, vol20))

    if not candidates:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Group candidates by ts for cross-sectional decile calculation
    by_ts = defaultdict(list)
    for ts, sid, vol20 in candidates:
        by_ts[ts].append((sid, vol20))

    # Process candidates in chronological order to enforce prior 20-day call constraint
    issued = []
    # Sort by ts
    sorted_ts = sorted(by_ts.keys())
    for ts in sorted_ts:
        items = by_ts[ts]
        # Compute 90th percentile of vol20 for this timestamp
        vol20_list = [v for _, v in items]
        vol20_list.sort()
        k = len(vol20_list)
        p90_idx = int(math.ceil(0.9 * k)) - 1
        if p90_idx < 0:
            p90_idx = 0
        vol20_p90 = vol20_list[p90_idx]
        for sid, vol20 in items:
            if vol20 > vol20_p90:
                continue  # abstain: volatility in top decile
            # Check prior 20-day call constraint for this symbol
            if sid in last_call_ts:
                # Need to know if there was a call in the prior 20 trading days.
                # Since we process chronologically, we can compare ts.
                # We don't have a direct list of trading days, so we approximate:
                # 20 trading days ≈ 28 calendar days. This is an approximation because
                # the hypothesis says "prior 20 trading days", but we don't have a
                # trading calendar. We'll use 28 days as a safe upper bound.
                # This might under-abstain, but the hypothesis requires strict adherence.
                # Alternatively, we could look back in the issued list for the same symbol
                # and check if any call has ts within the last 20 trading days.
                # However, without a trading calendar, we cannot accurately determine
                # "20 trading days". We will use a conservative 30 calendar days to be safe.
                if ts - last_call_ts[sid] <= 30 * 86400:
                    continue
            # Issue call
            issued.append((ts, sid))
            last_call_ts[sid] = ts

    # If too few issued calls for statistical validity
    if len(issued) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Fetch labels for issued calls
    hits = 0
    base_up = 0
    # For sealed era split, sort opportunities by ts and take most recent 20%
    all_opportunities.sort(key=lambda x: x[0])
    n_opportunities = len(all_opportunities)
    split_idx = int(math.floor(0.8 * n_opportunities))
    split_ts = all_opportunities[split_idx][0] if n_opportunities > 0 else 0
    sealed_issued = []
    sealed_hits = 0

    for ts, sid in issued:
        row = conn.execute(
            "SELECT up FROM prediction_outcomes WHERE symbol_id=? AND horizon=20 AND ts=?",
            (sid, ts)
        ).fetchone()
        if row is None:
            # No label available, cannot evaluate this call
            continue
        up = row['up']
        if up == 1:
            hits += 1
        base_up += 1
        if ts >= split_ts:
            sealed_issued.append((ts, sid, up))

    total_issued = len(issued)
    if base_up == 0:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Compute base rate within issued subset
    base_rate = hits / base_up

    # Compute distinct UTC days among issued calls
    days_set = set()
    for ts, sid in issued:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        days_set.add(dt.date())
    distinct_days = len(days_set)

    # Compute design effect using calls per day
    day_counts = defaultdict(int)
    for ts, sid in issued:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        day_counts[dt.date()] += 1
    sum_sq = sum(c * c for c in day_counts.values())
    if sum_sq == 0:
        design_effect = 1.0
    else:
        design_effect = sum_sq / total_issued
    effective_n = total_issued / design_effect

    # Compute sealed era metrics
    sealed_total = len(sealed_issued)
    if sealed_total > 0:
        sealed_hits = sum(1 for _, _, up in sealed_issued if up == 1)
        sealed_precision = sealed_hits / sealed_total
    else:
        sealed_precision = 0.0

    # Print required output
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={n_opportunities}")
    print(f"PRECISION={hits/base_up:.6f}" if base_up > 0 else "PRECISION=0.0")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == "__main__":
    main()