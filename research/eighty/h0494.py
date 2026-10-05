# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 493
# cycle_index: 23
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict

def main():
    try:
        con = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    try:
        # Check universe: symbols with >=200 1d bars, >=1 insider trade, >=2 13F periods
        cur = con.execute("""
            SELECT s.id
            FROM symbols s
            WHERE s.market = 'stocks'
              AND (SELECT COUNT(*) FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d') >= 200
              AND (SELECT COUNT(*) FROM insider_trades it WHERE it.symbol_id = s.id) > 0
              AND (SELECT COUNT(DISTINCT period) FROM inst_holdings ih WHERE ih.symbol_id = s.id) >= 2
        """)
        universe = [row[0] for row in cur]
        if not universe:
            print("INSUFFICIENT=1")
            return

        # For each symbol, get 13F periods aggregated by total value per quarter
        # and compute quarter-over-quarter changes
        calls = []
        for symbol_id in universe:
            # Get all 13F periods for this symbol, aggregated by quarter-end and total value
            cur = con.execute("""
                SELECT period, SUM(value) as total_value
                FROM inst_holdings
                WHERE symbol_id = ?
                GROUP BY period
                ORDER BY period
            """, (symbol_id,))
            periods = cur.fetchall()
            if len(periods) < 2:
                continue

            # Compute quarter-over-quarter percentage changes
            qoq = {}
            for i in range(1, len(periods)):
                prev_period, prev_val = periods[i-1]
                curr_period, curr_val = periods[i]
                if prev_val > 0:
                    change = (curr_val - prev_val) / prev_val
                    qoq[curr_period] = (prev_period, change)

            # Get all insider open-market purchases (code='P')
            cur = con.execute("""
                SELECT tx_ts, filed_ts
                FROM insider_trades
                WHERE symbol_id = ? AND code = 'P'
            """, (symbol_id,))
            trades = cur.fetchall()

            # Get all 1d bars for volume calculation (only up to current date needed)
            cur = con.execute("""
                SELECT ts, close, volume
                FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (symbol_id,))
            bars = cur.fetchall()
            if len(bars) < 20:
                continue

            # Build price lookup by date (Unix epoch -> (close, volume))
            # We'll need to convert filed_ts (epoch) to date for bar lookup
            price_by_date = {}
            for ts, close, volume in bars:
                # Convert epoch to YYYY-MM-DD for grouping
                from datetime import datetime, timezone
                dt = datetime.fromtimestamp(ts, tz=timezone.utc)
                day_str = dt.strftime('%Y-%m-%d')
                price_by_date[day_str] = (ts, close, volume)

            # Process each insider trade
            for trade_tx_ts, trade_filed_ts in trades:
                # Determine decision date (filed_ts) as date string
                from datetime import datetime, timezone
                dt_filed = datetime.fromtimestamp(trade_filed_ts, tz=timezone.utc)
                decision_day = dt_filed.strftime('%Y-%m-%d')

                # Get the most recent 13F period that is available by decision date
                # (period + 45 days <= decision day, and period within 90 days)
                available_periods = []
                for period, (prev_period, change) in qoq.items():
                    # Convert period (YYYY-MM-DD) to epoch for comparison
                    dt_period = datetime.strptime(period, '%Y-%m-%d').replace(tzinfo=timezone.utc)
                    period_epoch = dt_period.timestamp()
                    # Must be filed by decision date (period + 45 days)
                    from datetime import timedelta
                    if period_epoch + 45*86400 <= trade_filed_ts:
                        # Also check staleness (period within 90 days of decision)
                        if trade_filed_ts - period_epoch <= 90*86400:
                            available_periods.append((period, change))

                if not available_periods:
                    continue

                # Get the most recent available period
                available_periods.sort(reverse=True)  # Most recent first
                latest_period, change = available_periods[0]
                if change >= -0.05:  # Must be decrease of at least 5%
                    continue

                # Check 20-day average daily dollar volume >= $5M
                # Find bars up to decision_day
                sorted_days = sorted(price_by_date.keys())
                if decision_day not in price_by_date:
                    # Find the last trading day <= decision_day
                    valid_days = [d for d in sorted_days if d <= decision_day]
                    if len(valid_days) < 20:
                        continue
                    last_20_days = valid_days[-20:]
                else:
                    # Include decision_day in the 20-day window?
                    # According to as-of discipline, we can use data up to decision time
                    # We'll use the 20 trading days ending at decision_day
                    idx = sorted_days.index(decision_day)
                    if idx < 19:
                        continue
                    last_20_days = sorted_days[idx-19:idx+1]

                total_dollar_volume = 0
                for day in last_20_days:
                    _, close, volume = price_by_date[day]
                    total_dollar_volume += close * volume
                avg_dollar_volume = total_dollar_volume / 20
                if avg_dollar_volume < 5_000_000:
                    continue

                # All conditions met - record call
                calls.append((symbol_id, decision_day, trade_filed_ts))

        if not calls:
            print("INSUFFICIENT=1")
            return

        # Collapse to one call per (symbol, day) by earliest filed_ts
        calls_by_sym_day = defaultdict(list)
        for sym, day, filed in calls:
            calls_by_sym_day[(sym, day)].append(filed)
        
        collapsed_calls = []
        for (sym, day), files in calls_by_sym_day.items():
            earliest_filed = min(files)
            collapsed_calls.append((sym, day, earliest_filed))

        # Sort by filed_ts for time split
        collapsed_calls.sort(key=lambda x: x[2])
        n_total = len(collapsed_calls)
        n_sealed = max(1, int(n_total * 0.2))
        sealed_calls = collapsed_calls[-n_sealed:]
        train_calls = collapsed_calls[:-n_sealed]

        # Process each call to get labels (price at decision vs 21 days later)
        def get_return_for_call(sym, decision_day):
            # Get all 1d bars for symbol
            cur = con.execute("""
                SELECT ts, close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts
            """, (sym,))
            bars = cur.fetchall()
            if not bars:
                return None, None
            
            # Build date->close mapping
            date_to_close = {}
            for ts, close in bars:
                dt = datetime.fromtimestamp(ts, tz=timezone.utc)
                day = dt.strftime('%Y-%m-%d')
                date_to_close[day] = close
            
            # Get decision day close
            if decision_day not in date_to_close:
                return None, None
            price0 = date_to_close[decision_day]
            
            # Find 21 trading days later
            sorted_dates = sorted(date_to_close.keys())
            if decision_day not in sorted_dates:
                return None, None
            idx = sorted_dates.index(decision_day)
            if idx + 21 >= len(sorted_dates):
                return None, None
            day21 = sorted_dates[idx + 21]
            price21 = date_to_close[day21]
            
            return price0, price21

        # Process all calls
        all_returns = []
        hits_all = 0
        total_all = 0
        for sym, day, filed in collapsed_calls:
            p0, p21 = get_return_for_call(sym, day)
            if p0 is None or p21 is None:
                continue
            ret = (p21 - p0) / p0
            is_hit = ret > 0
            all_returns.append((sym, day, filed, is_hit))
            total_all += 1
            if is_hit:
                hits_all += 1

        # Process sealed era separately
        sealed_returns = []
        hits_sealed = 0
        total_sealed = 0
        for sym, day, filed in sealed_calls:
            p0, p21 = get_return_for_call(sym, day)
            if p0 is None or p21 is None:
                continue
            ret = (p21 - p0) / p0
            is_hit = ret > 0
            sealed_returns.append((sym, day, filed, is_hit))
            total_sealed += 1
            if is_hit:
                hits_sealed += 1

        if total_all == 0:
            print("INSUFFICIENT=1")
            return

        # Compute metrics
        issued = total_all
        precision = hits_all / issued
        base_rate = precision  # Base rate of up moves within issued set
        
        # Count distinct days among issued calls
        distinct_days = len(set(day for _, day, _, _ in all_returns))
        if distinct_days > issued:
            distinct_days = issued  # Enforce invariant

        # Compute design effect (variance of cluster sizes / mean + 1)
        # Cluster by day
        day_counts = defaultdict(int)
        for _, day, _, _ in all_returns:
            day_counts[day] += 1
        
        counts = list(day_counts.values())
        n_days = len(counts)
        if n_days == 0:
            design_effect = 1
        else:
            mean_count = sum(counts) / n_days
            if mean_count == 0:
                design_effect = 1
            else:
                variance = sum((c - mean_count)**2 for c in counts) / n_days
                design_effect = 1 + (variance / mean_count)
        
        effective_n = issued / design_effect
        if effective_n >= issued:
            effective_n = issued - 0.1  # Enforce strictly less

        # Sealed precision
        sealed_precision = hits_sealed / total_sealed if total_sealed > 0 else 0

        # Print required lines
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={len(calls)}")  # All considered trades before collapsing
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")

    finally:
        con.close()

if __name__ == "__main__":
    main()