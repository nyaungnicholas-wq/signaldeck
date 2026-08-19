# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 638
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from datetime import datetime, UTC
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Find SPY symbol_id
    spy_row = cur.execute("SELECT id FROM symbols WHERE symbol = 'SPY' AND market = 'stocks'").fetchone()
    if not spy_row:
        print("INSUFFICIENT=1")
        return
    spy_id = spy_row['id']

    # 2. Find CPI series in macro_series
    cpi_series_rows = cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%CPI%' ORDER BY series").fetchall()
    if not cpi_series_rows:
        print("INSUFFICIENT=1")
        return
    series_names = [r['series'] for r in cpi_series_rows]
    cpi_series = 'CPIAUCSL' if 'CPIAUCSL' in series_names else series_names[0]

    # 3. Get CPI release timestamps
    cpi_rows = cur.execute("SELECT ts FROM macro_series WHERE series = ? ORDER BY ts", (cpi_series,)).fetchall()
    if not cpi_rows:
        print("INSUFFICIENT=1")
        return
    cpi_timestamps = [r['ts'] for r in cpi_rows]

    # 4. Build trading day calendar from SPY 1d bars
    spy_bars = cur.execute("SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts", (spy_id,)).fetchall()
    if not spy_bars:
        print("INSUFFICIENT=1")
        return
    trading_days = [r['ts'] for r in spy_bars]
    ts_to_idx = {ts: i for i, ts in enumerate(trading_days)}
    spy_close = {r['ts']: r['close'] for r in spy_bars}

    # 5. Map CPI timestamps to trading days (T) and decision days (T-5)
    # CPI timestamps are Unix epoch seconds. Convert to date, find first trading day >= that date.
    cpi_events = []
    for cpi_ts in cpi_timestamps:
        # Handle potential millisecond timestamps
        if cpi_ts > 1e12:
            cpi_ts = cpi_ts // 1000
        try:
            cpi_dt = datetime.fromtimestamp(cpi_ts, UTC)
        except (OSError, ValueError, OverflowError):
            continue
        cpi_date_ts = int(datetime(cpi_dt.year, cpi_dt.month, cpi_dt.day, tzinfo=UTC).timestamp())
        cpi_day_ts = None
        for td in trading_days:
            if td >= cpi_date_ts:
                cpi_day_ts = td
                break
        if cpi_day_ts is None:
            continue
        cpi_idx = ts_to_idx[cpi_day_ts]
        if cpi_idx < 5:
            continue
        decision_ts = trading_days[cpi_idx - 5]
        cpi_events.append((cpi_ts, cpi_day_ts, decision_ts))

    if not cpi_events:
        print("INSUFFICIENT=1")
        return

    # 5. Get all symbols with 1d bars (excluding SPY)
    symbol_rows = cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d' AND symbol_id != ?", (spy_id,)).fetchall()
    all_symbol_ids = [r['symbol_id'] for r in symbol_rows]
    if not all_symbol_ids:
        print("INSUFFICIENT=1")
        return

    # 6. Load all 1d close prices for needed symbols
    # We need prices for all symbols at decision_ts and cpi_day_ts for all events
    # And returns for beta calculation (252 days before each decision)
    # Collect all timestamps we need
    needed_ts = set()
    for _, cpi_day_ts, decision_ts in cpi_events:
        needed_ts.add(decision_ts)
        needed_ts.add(cpi_day_ts)
        idx = ts_to_idx[decision_ts]
        for j in range(idx - 251, idx + 1):
            needed_ts.add(trading_days[j])
        # Also need 5d and 20d windows for vol
        for j in range(idx - 19, idx + 1):
            needed_ts.add(trading_days[j])
        for j in range(idx - 4, idx + 1):
            needed_ts.add(trading_days[j])

    needed_ts_list = sorted(needed_ts)
    placeholders = ','.join('?' * len(needed_ts_list))
    query = f"SELECT symbol_id, ts, close FROM bars WHERE tf = '1d' AND symbol_id IN ({','.join('?'*len(all_symbol_ids))}) AND ts IN ({placeholders})"
    params = all_symbol_ids + needed_ts_list
    bar_rows = cur.execute(query, params).fetchall()

    # Build price and return lookups
    price_lookup = defaultdict(dict)
    for row in bar_rows:
        price_lookup[row['symbol_id']][row['ts']] = row['close']

    # Also need SPY prices for beta calculation
    spy_needed_ts = set()
    for _, cpi_day_ts, decision_ts in cpi_events:
        idx = ts_to_idx[decision_ts]
        for j in range(idx - 251, idx + 1):
            spy_needed_ts.add(trading_days[j])
    spy_needed_ts = sorted(spy_needed_ts)
    spy_placeholders = ','.join('?' * len(spy_needed_ts))
    spy_bar_rows = cur.execute(f"SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' AND ts IN ({spy_placeholders})", [spy_id] + spy_needed_ts).fetchall()
    for row in spy_bar_rows:
        price_lookup[spy_id][row['ts']] = row['close']

    # Build returns lookup
    returns_lookup = defaultdict(dict)
    for sym_id, prices in price_lookup.items():
        sorted_ts = sorted(prices.keys())
        for i in range(1, len(sorted_ts)):
            prev_ts = sorted_ts[i-1]
            curr_ts = sorted_ts[i]
            prev_close = prices[prev_ts]
            curr_close = prices[curr_ts]
            if prev_close > 0:
                returns_lookup[sym_id][curr_ts] = (curr_close / prev_close) - 1.0

    if spy_id not in returns_lookup:
        print("INSUFFICIENT=1")
        return

    spy_returns = returns_lookup[spy_id]

    # 7. Process each CPI event
    calls = []
    last_call_session = {}

    for cpi_ts, cpi_day_ts, decision_ts in cpi_events:
        idx = ts_to_idx.get(decision_ts)
        if idx is None or idx < 251:
            continue

        # SPY return window (252 days ending at decision_ts)
        spy_ret_window = []
        for j in range(idx - 251, idx + 1):
            ts = trading_days[j]
            r = spy_returns.get(ts)
            if r is not None:
                spy_ret_window.append(r)
        if len(spy_ret_window) < 100:
            continue
        spy_mean = sum(spy_ret_window) / len(spy_ret_window)
        spy_var = sum((r - spy_mean) ** 2 for r in spy_ret_window) / len(spy_ret_window)
        if spy_var == 0:
            continue

        # Compute betas for all symbols
        betas = {}
        for sym_id in all_symbol_ids:
            sym_rets = returns_lookup.get(sym_id, {})
            sym_ret_window = []
            for j in range(idx - 251, idx + 1):
                ts = trading_days[j]
                r = sym_rets.get(ts)
                if r is not None:
                    sym_ret_window.append(r)
            if len(sym_ret_window) != len(spy_ret_window) or len(sym_ret_window) < 100:
                continue
            sym_mean = sum(sym_ret_window) / len(sym_ret_window)
            cov = sum((sym_ret_window[k] - sym_mean) * (spy_ret_window[k] - spy_mean) for k in range(len(spy_ret_window))) / len(spy_ret_window)
            beta = cov / spy_var
            betas[sym_id] = beta

        if not betas:
            continue

        # Top decile
        sorted_betas = sorted(betas.items(), key=lambda x: x[1], reverse=True)
        decile_cut = max(1, len(sorted_betas) // 10)
        top_decile = set(sym_id for sym_id, _ in sorted_betas[:decile_cut])

        # Check entry conditions for each symbol in top decile
        for sym_id in top_decile:
            # Abstain: same symbol called in prior 15 sessions
            last_call = last_call_session.get(sym_id)
            if last_call is not None:
                last_idx = ts_to_idx.get(last_call)
                curr_idx = idx
                if last_idx is not None and (curr_idx - last_idx) < 15:
                    continue

            sym_prices = price_lookup.get(sym_id, {})
            close_dec = sym_prices.get(decision_ts)
            if close_dec is None:
                continue
            if idx < 5:
                continue
            ts_5d_ago = trading_days[idx - 5]
            close_5d = sym_prices.get(ts_5d_ago)
            if close_5d is None or close_5d == 0:
                continue
            ret_5d = (close_dec / close_5d) - 1.0
            if ret_5d <= 0:
                continue

            sym_rets = returns_lookup.get(sym_id, {})
            # 5-day vol (decision_ts and prior 4 days)
            rets_5d = []
            for j in range(idx - 4, idx + 1):
                ts = trading_days[j]
                r = sym_rets.get(ts)
                if r is not None:
                    rets_5d.append(r)
            if len(rets_5d) < 5:
                continue
            mean_5d = sum(rets_5d) / 5
            vol_5d = math.sqrt(sum((r - mean_5d) ** 2 for r in rets_5d) / 5)

            # 20-day vol
            if idx < 19:
                continue
            rets_20d = []
            for j in range(idx - 19, idx + 1):
                ts = trading_days[j]
                r = sym_rets.get(ts)
                if r is not None:
                    rets_20d.append(r)
            if len(rets_20d) < 20:
                continue
            mean_20d = sum(rets_20d) / 20
            vol_20d = math.sqrt(sum((r - mean_20d) ** 2 for r in rets_20d) / 20)

            if vol_5d >= vol_20d:
                continue

            calls.append((decision_ts, sym_id, cpi_day_ts))
            last_call_session[sym_id] = decision_ts

    if not calls:
        print("INSUFFICIENT=1")
        return

    # 8. Label calls: return from decision_ts to cpi_day_ts
    labeled_calls = []
    for decision_ts, sym_id, cpi_day_ts in calls:
        sym_prices = price_lookup.get(sym_id, {})
        close_entry = sym_prices.get(decision_ts)
        close_exit = sym_prices.get(cpi_day_ts)
        if close_entry is None or close_exit is None or close_entry == 0:
            continue
        fwd_ret = (close_exit / close_entry) - 1.0
        up = 1 if fwd_ret > 0 else 0
        labeled_calls.append((decision_ts, sym_id, up, fwd_ret))

    if not labeled_calls:
        print("INSUFFICIENT=1")
        return

    # 9. Split: most recent 20% by decision_ts as sealed era
    labeled_calls.sort(key=lambda x: x[0])
    n_total = len(labeled_calls)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_calls = labeled_calls[-n_sealed:]
    main_calls = labeled_calls[:-n_sealed]

    # 10. Compute metrics
    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(1 for _, _, up, _ in call_list if up == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued  # base rate of "up" within issued subset
        distinct_days = len(set(decision_ts for decision_ts, _, _, _ in call_list))
        
        # Design effect: cluster by decision_ts
        clusters = defaultdict(list)
        for decision_ts, _, up, _ in call_list:
            clusters[decision_ts].append(up)
        cluster_sizes = [len(v) for v in clusters.values()]
        n_clusters = len(cluster_sizes)
        if n_clusters <= 1:
            deff = 1.0
        else:
            overall_mean = hits / issued
            between_sum = sum(len(v) * ((sum(v)/len(v)) - overall_mean) ** 2 for v in clusters.values())
            between_var = between_sum / (n_clusters - 1) if n_clusters > 1 else 0
            within_sum = sum(sum((up - sum(v)/len(v)) ** 2 for up in v) for v in clusters.values())
            within_var = within_sum / (issued - n_clusters) if issued > n_clusters else 0
            if within_var > 0:
                icc = between_var / (between_var + within_var)
                icc = max(0.0, min(1.0, icc))
            else:
                icc = 0.0
            avg_cluster_size = issued / n_clusters
            deff = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Opportunities: count of decision points considered (CPI events with valid data)
    opportunities = len(cpi_events)

    issued_main, hits_main, precision_main, base_rate_main, distinct_days_main, eff_n_main = compute_metrics(main_calls)
    issued_sealed, hits_sealed, precision_sealed, base_rate_sealed, distinct_days_sealed, eff_n_sealed = compute_metrics(sealed_calls)

    # Total issued across both eras for reporting
    total_issued = issued_main + issued_sealed
    total_hits = hits_main + hits_sealed
    total_precision = total_hits / total_issued if total_issued > 0 else 0.0
    total_base_rate = total_hits / total_issued if total_issued > 0 else 0.0
    total_distinct_days = len(set(decision_ts for decision_ts, _, _, _ in labeled_calls))
    
    # Overall effective N
    clusters_all = defaultdict(list)
    for decision_ts, _, up, _ in labeled_calls:
        clusters_all[decision_ts].append(up)
    cluster_sizes_all = [len(v) for v in clusters_all.values()]
    n_clusters_all = len(cluster_sizes_all)
    if n_clusters_all <= 1:
        deff_all = 1.0
    else:
        overall_mean_all = total_hits / total_issued
        between_sum_all = sum(len(v) * ((sum(v)/len(v)) - overall_mean_all) ** 2 for v in clusters_all.values())
        between_var_all = between_sum_all / (n_clusters_all - 1)
        within_sum_all = sum(sum((up - sum(v)/len(v)) ** 2 for up in v) for v in clusters_all.values())
        within_var_all = within_sum_all / (total_issued - n_clusters_all) if total_issued > n_clusters_all else 0
        if within_var_all > 0:
            icc_all = between_var_all / (between_var_all + within_var_all)
            icc_all = max(0.0, min(1.0, icc_all))
        else:
            icc_all = 0.0
        avg_cluster_size_all = total_issued / n_clusters_all
        deff_all = 1 + (avg_cluster_size_all - 1) * icc_all
    effective_n_all = total_issued / deff_all if deff_all > 0 else total_issued

    # Print required lines
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_distinct_days}")
    print(f"EFFECTIVE_N={effective_n_all:.6f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

if __name__ == "__main__":
    main()