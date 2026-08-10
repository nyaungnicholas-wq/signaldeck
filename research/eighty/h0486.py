# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 485
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
from collections import defaultdict
from math import sqrt
import bisect

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
    except:
        print("INSUFFICIENT=1")
        return

    # Universe: symbols with at least 252 daily bars AND insider trades AND news sentiment
    c.execute("""
        SELECT symbol_id, COUNT(DISTINCT ts) as cnt
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING cnt >= 252
    """)
    symbols_with_bars = {row[0]: row[1] for row in c.fetchall()}

    c.execute("""
        SELECT DISTINCT symbol_id
        FROM insider_trades
        WHERE code = 'P'
    """)
    symbols_with_insider = {row[0] for row in c.fetchall()}

    c.execute("""
        SELECT DISTINCT symbol_id
        FROM sentiment_features
    """)
    symbols_with_sentiment = {row[0] for row in c.fetchall()}

    universe = symbols_with_bars.keys() & symbols_with_insider & symbols_with_sentiment
    if not universe:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Fetch all daily bars for universe symbols
    symbol_bars = {}
    for sid in universe:
        c.execute("""
            SELECT ts, open, high, low, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sid,))
        bars = c.fetchall()
        if len(bars) >= 252:
            symbol_bars[sid] = bars

    universe = set(symbol_bars.keys())
    if not universe:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get insider purchase dates (filed_ts)
    insider_dates = {}
    for sid in universe:
        c.execute("""
            SELECT DISTINCT filed_ts
            FROM insider_trades
            WHERE symbol_id = ? AND code = 'P'
        """, (sid,))
        dates = set(row[0] for row in c.fetchall())
        insider_dates[sid] = dates

    # Get news sentiment per day per symbol
    sentiment_data = {}
    for sid in universe:
        c.execute("""
            SELECT day, mean_score
            FROM sentiment_features
            WHERE symbol_id = ?
            ORDER BY day
        """, (sid,))
        sentiment_data[sid] = c.fetchall()

    # Get labels from prediction_outcomes for horizon=21
    labels = {}  # (symbol_id, ts) -> up (0/1)
    c.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    for row in c.fetchall():
        labels[(row[0], row[1])] = row[2]

    conn.close()

    all_decisions = []  # (ts, symbol_id, hit)

    for sid in universe:
        bars = symbol_bars[sid]
        n = len(bars)
        if n < 252:
            continue

        # Compute daily returns
        returns = []
        for i in range(1, n):
            prev_close = bars[i-1][4]
            curr_close = bars[i][4]
            if prev_close > 0:
                ret = (curr_close / prev_close) - 1
            else:
                ret = 0
            returns.append((bars[i][0], ret))

        # 10-day trailing volatility (sample std of last 10 returns)
        vol_series = []  # (ts, vol)
        for i in range(9, len(returns)):
            window_returns = [returns[j][1] for j in range(i-9, i+1)]
            mean_r = sum(window_returns) / 10
            var = sum((r - mean_r)**2 for r in window_returns) / 9
            vol = sqrt(var)
            vol_series.append((returns[i][0], vol))

        # 252-day trailing median volatility
        # For each day in vol_series, compute median of previous 252 vol values
        vol_ts = [v[0] for v in vol_series]
        vol_vals = [v[1] for v in vol_series]
        median_vol = {}  # ts -> median_vol
        for i in range(251, len(vol_series)):
            window = vol_vals[i-251:i+1]
            sorted_window = sorted(window)
            median = sorted_window[len(sorted_window)//2]
            median_vol[vol_ts[i]] = median

        # 10-day rolling average of news sentiment mean_score
        sent = sentiment_data.get(sid, [])
        if not sent:
            continue
        sent_days = [row[0] for row in sent]
        sent_scores = [row[1] for row in sent]
        sent_avg10 = {}  # day -> avg
        for i in range(9, len(sent)):
            window = sent_scores[i-9:i+1]
            avg = sum(window) / 10
            sent_avg10[sent_days[i]] = avg

        # For each day, need bottom 20% threshold of sentiment history up to t-1
        # Precompute expanding 20th percentile
        sent_pct20 = {}  # day -> 20th percentile of history up to that day
        for i in range(1, len(sent)):
            history = sent_scores[:i]
            if len(history) >= 10:
                sorted_hist = sorted(history)
                idx = int(0.2 * (len(sorted_hist) - 1))
                sent_pct20[sent_days[i]] = sorted_hist[idx]

        # Now iterate over decision days (days with bars data)
        # Decision day t corresponds to bars index i (ts = bars[i][0])
        # We need: insider purchase disclosed on day t, sentiment condition, vol condition, |return| <= 5%
        # Label is prediction_outcomes at (symbol_id, ts) for horizon=21
        insider_set = insider_dates.get(sid, set())
        
        for i in range(252, n):  # need 252 days history for median vol
            ts = bars[i][0]
            # Check insider purchase disclosed on this day
            if ts not in insider_set:
                continue
            
            # Check daily absolute return <= 5%
            if i == 0:
                continue
            daily_ret = (bars[i][4] / bars[i-1][4]) - 1 if bars[i-1][4] > 0 else 0
            if abs(daily_ret) > 0.05:
                continue
            
            # Check sentiment: 10-day avg sentiment at t-1 is in bottom 20% of history up to t-1
            # sentiment_features day is 'YYYY-MM-DD', bars ts is epoch
            # Need to map ts to day string
            day_str = datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')
            # We need sentiment at t-1 (previous day)
            prev_ts = bars[i-1][0]
            prev_day = datetime.utcfromtimestamp(prev_ts).strftime('%Y-%m-%d')
            
            avg10 = sent_avg10.get(prev_day)
            pct20 = sent_pct20.get(prev_day)
            if avg10 is None or pct20 is None:
                continue
            if avg10 > pct20:  # not in bottom 20%
                continue
            
            # Check volatility: 10-day trailing vol at t-1 > 252-day median vol at t-1
            # vol_series corresponds to returns indices, which correspond to bars indices 1..n-1
            # vol at returns index j corresponds to bars index j+1
            # We need vol at bars index i-1 (previous day)
            vol_idx = i - 2  # because returns index = bars index - 1
            if vol_idx < 251 or vol_idx >= len(vol_series):
                continue
            vol_ts_val = vol_ts[vol_idx]
            vol_val = vol_vals[vol_idx]
            med_vol = median_vol.get(vol_ts_val)
            if med_vol is None:
                continue
            if vol_val <= med_vol:
                continue
            
            # All entry conditions met - issue call
            # Get label
            hit = labels.get((sid, ts))
            if hit is not None:
                all_decisions.append((ts, sid, hit))

    if not all_decisions:
        print("INSUFFICIENT=1")
        return

    # Sort by timestamp
    all_decisions.sort(key=lambda x: x[0])
    
    # Hold out most recent 20% as sealed era
    n_total = len(all_decisions)
    split_idx = int(n_total * 0.8)
    main_decisions = all_decisions[:split_idx]
    sealed_decisions = all_decisions[split_idx:]

    def compute_metrics(decisions):
        if not decisions:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(decisions)
        hits = sum(d[2] for d in decisions)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate within issued subset
        distinct_days = len(set(d[0] for d in decisions))
        # Design effect: cluster by day, compute effective N
        # Simple approach: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use Kish's effective sample size: n_eff = (sum w)^2 / sum(w^2) where w=1
        # For clustered data, approximate by number of clusters if high correlation
        # Here we use: design_effect = 1 + (avg_per_day - 1) * ICC
        # Estimate ICC conservatively as 0.5 for same-day calls
        day_counts = defaultdict(int)
        for d in decisions:
            day_counts[d[0]] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        icc = 0.5
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_main, hits_main, prec_main, base_main, distinct_main, eff_main = compute_metrics(main_decisions)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed, eff_sealed = compute_metrics(sealed_decisions)

    # Overall metrics (for reporting)
    issued_all = issued_main + issued_sealed
    hits_all = hits_main + hits_sealed
    precision_all = hits_all / issued_all if issued_all > 0 else 0.0
    base_rate_all = hits_all / issued_all if issued_all > 0 else 0.0
    distinct_all = len(set(d[0] for d in all_decisions))
    # Effective N for all
    day_counts_all = defaultdict(int)
    for d in all_decisions:
        day_counts_all[d[0]] += 1
    avg_cluster_all = sum(day_counts_all.values()) / len(day_counts_all) if day_counts_all else 1
    design_effect_all = 1 + (avg_cluster_all - 1) * 0.5
    effective_n_all = issued_all / design_effect_all if design_effect_all > 0 else issued_all

    # Opportunities: total decision points considered (days with enough history for each symbol)
    # This is the number of (symbol, day) pairs where we evaluated entry conditions
    # We need to count this during the loop. Let's recompute or track.
    # Since we didn't track, we'll compute: for each symbol, days from index 252 to n-1
    opportunities = 0
    for sid in universe:
        bars = symbol_bars[sid]
        n = len(bars)
        if n > 252:
            opportunities += (n - 252)

    print(f"ISSUED={issued_all}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_all:.6f}")
    print(f"BASE_RATE={base_rate_all:.6f}")
    print(f"DISTINCT_DAYS={distinct_all}")
    print(f"EFFECTIVE_N={effective_n_all:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

if __name__ == "__main__":
    from datetime import datetime
    main()