# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 725
# cycle_index: 52
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get active stock symbols
    cur.execute("""
        SELECT id, symbol FROM symbols
        WHERE market = 'stocks' AND active = 1
    """)
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return

    sym_ids = list(symbols.keys())
    placeholders = ','.join('?' * len(sym_ids))

    # 2. Load quarterly fundamentals: EPS, Revenues, SharesOutstanding
    # Only keep rows with as_of > 0 (real periods)
    cur.execute(f"""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE symbol_id IN ({placeholders})
          AND metric IN ('EPS','Revenues','SharesOutstanding')
          AND as_of > 0
        ORDER BY symbol_id, metric, as_of
    """, sym_ids)
    fund_rows = cur.fetchall()

    # Organize by symbol_id -> metric -> list of (as_of, value, fetched_at)
    fund = defaultdict(lambda: defaultdict(list))
    for r in fund_rows:
        fund[r['symbol_id']][r['metric']].append((r['as_of'], r['value'], r['fetched_at']))

    # 3. Load insider open-market purchases (code='P')
    cur.execute(f"""
        SELECT symbol_id, filed_ts, shares, price, value
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
          AND code = 'P'
        ORDER BY symbol_id, filed_ts
    """, sym_ids)
    insider_rows = cur.fetchall()

    # 4. Load daily bars (tf='1d') for price data
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE symbol_id IN ({placeholders})
          AND tf = '1d'
        ORDER BY symbol_id, ts
    """, sym_ids)
    bar_rows = cur.fetchall()

    bars = defaultdict(list)
    for r in bar_rows:
        bars[r['symbol_id']].append((r['ts'], r['close']))

    # Helper: get latest fundamental value as of a given timestamp (fetched_at <= ts)
    def get_latest_fund(sym_id, metric, as_of_ts):
        series = fund[sym_id].get(metric, [])
        # binary search for latest fetched_at <= as_of_ts
        lo, hi = 0, len(series)
        while lo < hi:
            mid = (lo + hi) // 2
            if series[mid][2] <= as_of_ts:
                lo = mid + 1
            else:
                hi = mid
        if lo == 0:
            return None
        return series[lo - 1]  # (as_of, value, fetched_at)

    # Helper: get price at or before timestamp
    def get_price_at(sym_id, ts):
        series = bars.get(sym_id, [])
        lo, hi = 0, len(series)
        while lo < hi:
            mid = (lo + hi) // 2
            if series[mid][0] <= ts:
                lo = mid + 1
            else:
                hi = mid
        if lo == 0:
            return None
        return series[lo - 1][1]

    # Helper: get forward 63-day return from bars
    def get_forward_return(sym_id, start_ts, horizon_days=63):
        series = bars.get(sym_id, [])
        # find index of start_ts (exact or next)
        lo, hi = 0, len(series)
        while lo < hi:
            mid = (lo + hi) // 2
            if series[mid][0] < start_ts:
                lo = mid + 1
            else:
                hi = mid
        start_idx = lo
        end_idx = start_idx + horizon_days
        if end_idx >= len(series):
            return None
        start_px = series[start_idx][1]
        end_px = series[end_idx][1]
        if start_px <= 0:
            return None
        return (end_px - start_px) / start_px

    # 5. For each symbol, build quarterly acceleration history
    # We need to align EPS, Revenue, SharesOutstanding by as_of (quarter end)
    # For each quarter where we have all three, compute growth and acceleration
    events = []  # (filed_ts, sym_id, fwd_return)

    for sym_id in sym_ids:
        eps_series = fund[sym_id].get('EPS', [])
        rev_series = fund[sym_id].get('Revenues', [])
        so_series = fund[sym_id].get('SharesOutstanding', [])

        if len(eps_series) < 4 or len(rev_series) < 4 or len(so_series) < 4:
            continue

        # Build quarterly records keyed by as_of
        # Each metric may have different fetched_at for same as_of; take earliest fetched_at per as_of per metric
        def dedup_by_asof(series):
            out = {}
            for as_of, val, fetched in series:
                if as_of not in out or fetched < out[as_of][1]:
                    out[as_of] = (val, fetched)
            return sorted(out.items())  # list of (as_of, (val, fetched))

        eps_q = dedup_by_asof(eps_series)
        rev_q = dedup_by_asof(rev_series)
        so_q = dedup_by_asof(so_series)

        # Align by as_of (quarter end)
        eps_dict = {as_of: (val, fetched) for as_of, (val, fetched) in eps_q}
        rev_dict = {as_of: (val, fetched) for as_of, (val, fetched) in rev_q}
        so_dict = {as_of: (val, fetched) for as_of, (val, fetched) in so_q}

        common_asofs = sorted(set(eps_dict.keys()) & set(rev_dict.keys()) & set(so_dict.keys()))
        if len(common_asofs) < 4:
            continue

        # Compute quarterly growth rates and acceleration
        # growth_q = (val_q - val_q-1) / val_q-1
        # accel_q = growth_q - growth_q-1
        eps_growth = {}
        rev_growth = {}
        for i in range(1, len(common_asofs)):
            a_prev = common_asofs[i-1]
            a_curr = common_asofs[i]
            eps_prev = eps_dict[a_prev][0]
            eps_curr = eps_dict[a_curr][0]
            rev_prev = rev_dict[a_prev][0]
            rev_curr = rev_dict[a_curr][0]
            if eps_prev != 0:
                eps_growth[a_curr] = (eps_curr - eps_prev) / eps_prev
            if rev_prev != 0:
                rev_growth[a_curr] = (rev_curr - rev_prev) / rev_prev

        eps_accel = {}
        rev_accel = {}
        asofs_with_growth = sorted(set(eps_growth.keys()) & set(rev_growth.keys()))
        for i in range(1, len(asofs_with_growth)):
            a_prev = asofs_with_growth[i-1]
            a_curr = asofs_with_growth[i]
            eps_accel[a_curr] = eps_growth[a_curr] - eps_growth[a_prev]
            rev_accel[a_curr] = rev_growth[a_curr] - rev_growth[a_prev]

        # Flag quarters with 3+ consecutive positive acceleration for both
        # We need to know, at any point in time, whether the latest 3 quarters (as of that time) show positive accel
        accel_quarters = sorted(set(eps_accel.keys()) & set(rev_accel.keys()))
        # For each quarter, check if it and previous 2 have positive accel
        three_accel = set()
        for i in range(2, len(accel_quarters)):
            a0 = accel_quarters[i-2]
            a1 = accel_quarters[i-1]
            a2 = accel_quarters[i]
            if (eps_accel.get(a0, -1) > 0 and eps_accel.get(a1, -1) > 0 and eps_accel.get(a2, -1) > 0 and
                rev_accel.get(a0, -1) > 0 and rev_accel.get(a1, -1) > 0 and rev_accel.get(a2, -1) > 0):
                three_accel.add(a2)  # the third quarter in the streak

        # 6. Build daily P/S ratio history for multi-year low detection
        # P/S = (close * SharesOutstanding) / (Revenue * 4)  [annualized quarterly revenue]
        # We'll compute at each bar timestamp using latest available fundamentals (fetched_at <= bar_ts)
        # But for efficiency, compute at quarter ends and interpolate? Simpler: compute at each insider trade time.
        # For multi-year low, we need historical P/S values. Let's compute P/S at each quarter end using that quarter's data.
        ps_history = []  # list of (as_of, ps_ratio)
        for a in common_asofs:
            eps_val, eps_fetched = eps_dict.get(a, (None, None))
            rev_val, rev_fetched = rev_dict.get(a, (None, None))
            so_val, so_fetched = so_dict.get(a, (None, None))
            if rev_val and rev_val > 0 and so_val and so_val > 0:
                # Get price at quarter end (as_of)
                px = get_price_at(sym_id, a)
                if px and px > 0:
                    ps = (px * so_val) / (rev_val * 4)  # annualized
                    if ps > 0:
                        ps_history.append((a, ps))

        if len(ps_history) < 12:  # need at least 3 years (12 quarters) for multi-year low
            continue

        # 7. Process insider trades for this symbol
        sym_trades = [t for t in insider_rows if t['symbol_id'] == sym_id]
        for t in sym_trades:
            filed_ts = t['filed_ts']
            # Check condition 1: at filed_ts, latest available fundamentals show 3+ quarter accel streak
            # Find latest quarter end <= filed_ts where we have acceleration data
            latest_accel_q = None
            for a in reversed(accel_quarters):
                if a <= filed_ts:
                    # Check if this quarter is in three_accel (meaning streak ending at this quarter)
                    if a in three_accel:
                        # But we need to know if this streak was KNOWABLE at filed_ts
                        # The quarter a's data must have been fetched by filed_ts
                        eps_fetched = eps_dict[a][1]
                        rev_fetched = rev_dict[a][1]
                        so_fetched = so_dict[a][1]
                        # Also need the two prior quarters' data fetched
                        # Find indices
                        try:
                            idx = accel_quarters.index(a)
                            if idx >= 2:
                                a0 = accel_quarters[idx-2]
                                a1 = accel_quarters[idx-1]
                                eps_f0 = eps_dict[a0][1]
                                eps_f1 = eps_dict[a1][1]
                                rev_f0 = rev_dict[a0][1]
                                rev_f1 = rev_dict[a1][1]
                                so_f0 = so_dict[a0][1]
                                so_f1 = so_dict[a1][1]
                                if (eps_fetched <= filed_ts and rev_fetched <= filed_ts and so_fetched <= filed_ts and
                                    eps_f0 <= filed_ts and eps_f1 <= filed_ts and
                                    rev_f0 <= filed_ts and rev_f1 <= filed_ts and
                                    so_f0 <= filed_ts and so_f1 <= filed_ts):
                                    latest_accel_q = a
                                    break
                        except ValueError:
                            pass
            if not latest_accel_q:
                continue

            # Condition 2: P/S at multi-year low (lowest in past 12 quarters / 3 years)
            # Get current P/S using latest price and latest available revenue/shares
            # Use latest fundamentals fetched by filed_ts
            latest_rev = get_latest_fund(sym_id, 'Revenues', filed_ts)
            latest_so = get_latest_fund(sym_id, 'SharesOutstanding', filed_ts)
            if not latest_rev or not latest_so:
                continue
            rev_val = latest_rev[1]
            so_val = latest_so[1]
            if rev_val <= 0 or so_val <= 0:
                continue
            px = get_price_at(sym_id, filed_ts)
            if not px or px <= 0:
                continue
            current_ps = (px * so_val) / (rev_val * 4)
            if current_ps <= 0:
                continue

            # Check if current_ps is at 12-quarter low
            # Use ps_history quarters with as_of <= filed_ts
            historical_ps = [ps for a, ps in ps_history if a <= filed_ts]
            if len(historical_ps) < 12:
                continue
            # Look at last 12 quarters (including current)
            recent_ps = historical_ps[-12:]
            if current_ps > min(recent_ps):
                continue  # not at multi-year low

            # All conditions met - compute forward return
            fwd_ret = get_forward_return(sym_id, filed_ts, 63)
            if fwd_ret is not None:
                events.append((filed_ts, sym_id, fwd_ret))

    if not events:
        print("INSUFFICIENT=1")
        return

    # 8. Split by time: hold out most recent 20% as sealed era
    events.sort(key=lambda x: x[0])
    n = len(events)
    split_idx = int(n * 0.8)
    train_events = events[:split_idx]
    sealed_events = events[split_idx:]

    def compute_metrics(evts):
        if not evts:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(evts)
        # Base rate: proportion of positive forward returns in issued set
        hits = sum(1 for _, _, ret in evts if ret > 0)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate of predicted class (positive return) within issued subset
        # Distinct UTC days among issued calls
        days = set()
        for ts, _, _ in evts:
            # Convert unix ts to UTC date string
            import datetime
            dt = datetime.datetime.utcfromtimestamp(ts)
            days.add(dt.strftime('%Y-%m-%d'))
        distinct_days = len(days)
        # Design effect: estimate from autocorrelation of returns? Simplified: use 1 + 2*sum(rho_k)
        # For simplicity, assume design effect >= 1.5 (conservative)
        # But we need to measure it. Compute variance inflation from clustering.
        # Group by day, compute day-level mean returns, then design effect = (var_individual / var_day_mean) * (n_days / n)
        # Actually standard formula: deff = 1 + (m-1)*rho where m=avg cluster size, rho=ICC
        # Compute ICC (intraclass correlation) of returns within days
        day_rets = defaultdict(list)
        for ts, _, ret in evts:
            dt = datetime.datetime.utcfromtimestamp(ts)
            day = dt.strftime('%Y-%m-%d')
            day_rets[day].append(ret)
        if len(day_rets) > 1:
            # Overall mean
            all_rets = [ret for _, _, ret in evts]
            overall_mean = sum(all_rets) / len(all_rets)
            # Between-day variance
            day_means = [sum(v)/len(v) for v in day_rets.values()]
            n_days = len(day_means)
            if n_days > 1:
                between_var = sum((m - overall_mean)**2 for m in day_means) / (n_days - 1)
                # Within-day variance
                within_var = sum(sum((x - m)**2 for x in v) for v, m in zip(day_rets.values(), day_means))
                total_within_n = sum(len(v) - 1 for v in day_rets.values())
                if total_within_n > 0:
                    within_var = within_var / total_within_n
                else:
                    within_var = 0
                if within_var > 0 and between_var > 0:
                    icc = between_var / (between_var + within_var)
                else:
                    icc = 0
                avg_cluster = issued / n_days
                deff = 1 + (avg_cluster - 1) * icc
                if deff < 1:
                    deff = 1
            else:
                deff = 1
        else:
            deff = 1
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_days, train_eff_n = compute_metrics(train_events)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff_n = compute_metrics(sealed_events)

    # Print required lines
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={train_issued}")  # opportunities = decision points considered that met entry criteria
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_br:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()