# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 344
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3, sys
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    try:
        cur = conn.cursor()
        
        # Get all symbols with daily bars
        cur.execute("SELECT symbol_id FROM bars WHERE tf='1d' GROUP BY symbol_id HAVING COUNT(*)>=252")
        symbols_with_bars = {row[0] for row in cur.fetchall()}
        if not symbols_with_bars:
            print("INSUFFICIENT=1")
            return

        # Get symbols with at least 4 quarters of EPS data
        cur.execute("SELECT symbol_id, COUNT(DISTINCT as_of) FROM fundamentals WHERE metric='EPS' GROUP BY symbol_id HAVING COUNT(DISTINCT as_of)>=4")
        symbols_with_eps = {row[0] for row in cur.fetchall()}
        if not symbols_with_eps:
            print("INSUFFICIENT=1")
            return

        # Get symbols with at least 60 days of news sentiment
        cur.execute("SELECT symbol_id, COUNT(DISTINCT day) FROM sentiment_features GROUP BY symbol_id HAVING COUNT(DISTINCT day)>=60")
        symbols_with_sentiment = {row[0] for row in cur.fetchall()}
        if not symbols_with_sentiment:
            print("INSUFFICIENT=1")
            return

        # Universe = intersection
        universe = symbols_with_bars & symbols_with_eps & symbols_with_sentiment
        if len(universe) < 2:
            print("INSUFFICIENT=1")
            return

        # Get all prediction outcomes for horizon=21
        cur.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=21")
        all_labels = {}
        for sym, ts, up in cur.fetchall():
            if sym in universe:
                all_labels[(sym, ts)] = up

        # Get all daily bars for universe
        cur.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
        bars_data = defaultdict(list)
        for sym, ts, close, volume in cur.fetchall():
            if sym in universe:
                bars_data[sym].append((ts, close, volume))

        # Get news sentiment
        cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features ORDER BY symbol_id, day")
        sentiment_data = defaultdict(list)
        for sym, day, score in cur.fetchall():
            if sym in universe:
                sentiment_data[sym].append((day, score))

        # Get EPS data
        cur.execute("SELECT symbol_id, as_of, value, fetched_at FROM fundamentals WHERE metric='EPS' ORDER BY symbol_id, as_of")
        eps_data = defaultdict(list)
        for sym, as_of, value, fetched_at in cur.fetchall():
            if sym in universe:
                eps_data[sym].append((as_of, value, fetched_at))

        # Get short volume for volatility calculation (optional, but need for top decile check)
        cur.execute("SELECT symbol_id, day, short_vol, total_vol FROM short_volume ORDER BY symbol_id, day")
        short_volume_data = defaultdict(list)
        for sym, day, short_vol, total_vol in cur.fetchall():
            if sym in universe:
                short_volume_data[sym].append((day, short_vol, total_vol))

        # Precompute for each symbol: daily data, sentiment by day, EPS by quarter, etc.
        # We'll process each symbol separately and then combine all decision points

        decision_points = []  # (date, symbol, label, is_issued, base_date for label)
        all_dates = set()
        
        for sym in universe:
            # Convert bars to lists sorted by time
            sym_bars = bars_data[sym]
            if len(sym_bars) < 252:
                continue
            bar_dates = [ts for ts, _, _ in sym_bars]
            bar_closes = {ts: close for ts, close, _ in sym_bars}
            bar_volumes = {ts: volume for ts, _, volume in sym_bars}

            # Convert sentiment to list sorted by day
            sym_sentiment = sentiment_data[sym]
            sent_by_day = {day: score for day, score in sym_sentiment}

            # Convert EPS to list sorted by as_of, keep fetched_at
            sym_eps = eps_data[sym]
            eps_by_asof = defaultdict(list)
            for asof, value, fetched_at in sym_eps:
                eps_by_asof[asof].append((value, fetched_at))

            # Short volume for volatility
            sym_short = short_volume_data[sym]
            short_by_day = {day: (short_vol, total_vol) for day, short_vol, total_vol in sym_short}

            # For each possible decision date (using bar dates as trading days)
            for i in range(252, len(sym_bars)):
                decision_ts = bar_dates[i]
                decision_date = datetime.utcfromtimestamp(decision_ts).date()
                
                # Ensure we have at least 200 trading days before for 200-day MA
                if i < 200:
                    continue

                # Compute 200-day moving average
                closes_200 = [bar_closes[bar_dates[j]] for j in range(i-200, i)]
                ma200 = sum(closes_200) / 200.0

                # Compute 20-day average daily dollar volume
                volumes_20 = [bar_volumes[bar_dates[j]] * bar_closes[bar_dates[j]] for j in range(i-20, i)]
                avg_dollar_volume = sum(volumes_20) / 20.0

                # Check abstention condition (a)
                if avg_dollar_volume < 1e6:
                    continue

                # Check abstention condition (b): earnings announcement in past 5 days
                # We don't have earnings announcement data, so we cannot check.
                # Since the data is insufficient for this condition, we must exit.
                print("INSUFFICIENT=1")
                conn.close()
                return

                # Check abstention condition (c): 20-day historical volatility in top decile
                # We'll compute this later after we have all decision points' volatilities
                # For now, skip and later compute percentiles

                # Compute trailing 4-quarter EPS growth (YoY)
                # Find the most recent 4 quarters of EPS data before decision_ts
                # We need to consider only EPS values whose fetched_at <= decision_ts
                current_quarter = None
                quarters = []
                for asof, value, fetched_at in eps_by_asof.items():
                    if fetched_at <= decision_ts:
                        # We have the data as of decision_ts
                        quarters.append((asof, value))
                # Sort by asof
                quarters.sort(key=lambda x: x[0])
                if len(quarters) < 8:  # need at least 8 quarters to compute YoY for latest 4
                    continue
                # Take last 8 quarters
                last_8 = quarters[-8:]
                # EPS growth YoY = (sum of last 4) / (sum of previous 4) - 1
                recent_4 = [v for _, v in last_8[4:]]
                prev_4 = [v for _, v in last_8[:4]]
                if sum(prev_4) == 0:
                    continue
                eps_growth = (sum(recent_4) / sum(prev_4)) - 1.0

                # Compute 20-day average news sentiment
                # We need sentiment for days up to decision_date
                sentiment_scores = []
                for day in range(20):
                    target_date = decision_date - timedelta(days=day)
                    if target_date in sent_by_day:
                        sentiment_scores.append(sent_by_day[target_date])
                if len(sentiment_scores) < 10:  # require at least 10 days of sentiment
                    continue
                avg_sentiment = sum(sentiment_scores) / len(sentiment_scores)

                # Check entry conditions:
                # We'll compute percentiles across the universe at this decision_ts
                # But we cannot compute percentiles without all symbols at this time.
                # We'll collect all symbols' features for each decision_ts and then compute percentiles.
                # We'll store: (decision_ts, sym, eps_growth, avg_sentiment, close, ma200, volatility_data)
                # Later we'll group by decision_ts and compute percentiles.

                # For volatility, we'll collect 20-day returns
                returns = []
                for j in range(i-20, i+1):  # we need 21 returns for 20 days?
                    if j > 0:
                        ret = (bar_closes[bar_dates[j]] / bar_closes[bar_dates[j-1]]) - 1
                        returns.append(ret)
                if len(returns) < 20:
                    continue
                # Compute historical volatility as std of returns
                import math
                mean_ret = sum(returns) / len(returns)
                var_ret = sum((r - mean_ret)**2 for r in returns) / (len(returns)-1)
                hist_vol = math.sqrt(var_ret)

                # Store for this decision point
                decision_points.append({
                    'ts': decision_ts,
                    'date': decision_date,
                    'sym': sym,
                    'eps_growth': eps_growth,
                    'avg_sentiment': avg_sentiment,
                    'close': bar_closes[decision_ts],
                    'ma200': ma200,
                    'hist_vol': hist_vol,
                    'dollar_vol': avg_dollar_volume
                })
                all_dates.add(decision_date)

        if not decision_points:
            print("INSUFFICIENT=1")
            return

        # Group decision points by timestamp
        by_ts = defaultdict(list)
        for dp in decision_points:
            by_ts[dp['ts']].append(dp)

        # For each timestamp, compute percentiles across symbols for eps_growth, sentiment, volatility
        issued_calls = []
        opportunities = 0
        for ts, points in sorted(by_ts.items()):
            opportunities += len(points)
            # Compute percentiles
            eps_vals = [p['eps_growth'] for p in points]
            sent_vals = [p['avg_sentiment'] for p in points]
            vol_vals = [p['hist_vol'] for p in points]

            # Sort to find quartiles and deciles
            eps_vals_sorted = sorted(eps_vals)
            sent_vals_sorted = sorted(sent_vals)
            vol_vals_sorted = sorted(vol_vals)

            # Top quartile for eps_growth: >= 75th percentile
            q75_eps = eps_vals_sorted[int(len(eps_vals_sorted)*0.75)] if len(eps_vals_sorted) >= 4 else None
            # Bottom quartile for sentiment: <= 25th percentile
            q25_sent = sent_vals_sorted[int(len(sent_vals_sorted)*0.25)] if len(sent_vals_sorted) >= 4 else None
            # Top decile for volatility: >= 90th percentile
            q90_vol = vol_vals_sorted[int(len(vol_vals_sorted)*0.9)] if len(vol_vals_sorted) >= 10 else None

            if q75_eps is None or q25_sent is None or q90_vol is None:
                continue

            for p in points:
                # Check entry conditions
                if p['eps_growth'] >= q75_eps and p['avg_sentiment'] <= q25_sent and p['close'] < p['ma200']:
                    # Check abstention condition (c)
                    if p['hist_vol'] >= q90_vol:
                        continue
                    # Condition (a) already checked (dollar volume)
                    # Condition (b) we skipped due to insufficient data, but we must have it
                    # Since we cannot check condition (b), we must exit as insufficient.
                    print("INSUFFICIENT=1")
                    conn.close()
                    return

                    # If we get here, we issue a call
                    # Find the label for this symbol at this ts + 21 days horizon
                    # The label in prediction_outcomes is for horizon=21, and the ts in prediction_outcomes is the forecast time?
                    # We need to find the label for the outcome 21 days after this decision_ts.
                    # However, the prediction_outcomes table has (symbol_id, horizon, ts, up, ...)
                    # The ts in prediction_outcomes is the time of the forecast? Or the time of the outcome?
                    # From schema: prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch)
                    # We assume ts is the forecast time (decision time) and horizon is the forecast horizon.
                    # So we look for (sym, 21, decision_ts) in prediction_outcomes.
                    label_key = (p['sym'], ts)
                    if label_key in all_labels:
                        label = all_labels[label_key]
                        issued_calls.append({
                            'ts': ts,
                            'date': p['date'],
                            'sym': p['sym'],
                            'label': label
                        })

        conn.close()

        if not issued_calls:
            print("INSUFFICIENT=1")
            return

        # Split into train and sealed (most recent 20% of dates)
        all_call_dates = sorted(set(call['date'] for call in issued_calls))
        split_index = int(len(all_call_dates) * 0.8)
        train_dates = set(all_call_dates[:split_index])
        sealed_dates = set(all_call_dates[split_index:])

        # Count metrics
        issued_count = len(issued_calls)
        distinct_days = len(set(call['date'] for call in issued_calls))
        # Effective N: we need to compute design effect
        # Group by date and compute proportion of calls per date
        calls_by_date = defaultdict(int)
        for call in issued_calls:
            calls_by_date[call['date']] += 1
        total_calls = issued_count
        # Design effect = 1 + (intracluster correlation) * (average cluster size - 1)
        # We'll estimate intracluster correlation as variance of label across clusters / total variance
        # Labels are binary (up or down)
        labels = [call['label'] for call in issued_calls]
        overall_mean = sum(labels) / len(labels)
        total_var = sum((l - overall_mean)**2 for l in labels) / len(labels)
        
        # Cluster means
        cluster_means = []
        cluster_sizes = []
        for date, count in calls_by_date.items():
            cluster_labels = [call['label'] for call in issued_calls if call['date'] == date]
            cluster_mean = sum(cluster_labels) / count
            cluster_means.append(cluster_mean)
            cluster_sizes.append(count)
        # Intracluster correlation (ICC)
        between_cluster_var = sum((m - overall_mean)**2 for m in cluster_means) / len(cluster_means)
        avg_cluster_size = sum(cluster_sizes) / len(cluster_sizes)
        # ICC = (between_cluster_var - overall_mean*(1-overall_mean)/avg_cluster_size) / (overall_mean*(1-overall_mean))
        # But careful: overall_mean*(1-overall_mean) is binomial variance if labels were independent
        binomial_var = overall_mean * (1 - overall_mean)
        if binomial_var == 0:
            icc = 0
        else:
            icc = (between_cluster_var - binomial_var/avg_cluster_size) / binomial_var
            icc = max(0, min(icc, 1))  # clamp between 0 and 1
        design_effect = 1 + icc * (avg_cluster_size - 1)
        effective_n = issued_count / design_effect

        # Precision
        hits = sum(1 for call in issued_calls if call['label'])
        precision = hits / issued_count if issued_count > 0 else 0

        # Base rate within issued subset
        base_rate = precision  # because the predicted class is "up" and we are counting hits as up

        # Sealed precision
        sealed_calls = [call for call in issued_calls if call['date'] in sealed_dates]
        sealed_hits = sum(1 for call in sealed_calls if call['label'])
        sealed_precision = sealed_hits / len(sealed_calls) if sealed_calls else 0

        # Print required outputs
        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_precision}")

    except Exception as e:
        print("INSUFFICIENT=1")
        try:
            conn.close()
        except:
            pass

if __name__ == "__main__":
    main()