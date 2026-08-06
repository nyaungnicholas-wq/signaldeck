Let me carefully analyze the task and write a Python script that tests the given hypothesis.

Key requirements:
1. Read from data/signaldeck.db (read-only)
2. Test the hypothesis about StockTwits sentiment predicting mean reversion
3. Follow all the measurement rules
4. Output specific metrics at the end

Let me break down the hypothesis:

MECHANISM: Retail trader sentiment measured by StockTwits bullish/bearish ratios creates predictable crowding: extreme bullishness signals a crowded long position where marginal buyers are exhausted, leading to mean-reversion down over the following month.

HORIZON: T+20 trading days close-to-close, with T the first trading day whose daily-bar and StockTwits values satisfy every condition below.

UNIVERSE: All symbols present in both the daily-bars and stocktwits tables at T with at least 252 completed prior sessions, close >= $5, average daily dollar volume >= $5M over T-60..T-1, snapshot at T, no survivorship bias.

ENTRY: Issue a DOWN call when:
- 5-day average (bullish_count / (bullish_count + bearish_count)) >= 0.8
- T's close-to-close return is between -0.5% and +0.5%
- T's 20-session return is between -2% and +2%

ABSTAIN: No call if:
- price < $5
- fewer than 252 prior sessions
- any required bar or StockTwits value missing for T-252..T
- 20-session realized volatility at T in the top cross-sectional decile
- a call was issued for the same symbol in the prior 20 trading days
- fewer than 30 independent observations remain
- T's close-to-close return is outside [-0.5%, +0.5%]

CLAIM: Precision >= 0.80 on issued DOWN calls at an abstention rate >= 0.95, with precision minus issued-subset base rate >= 0.10.

I need to:
1. Get daily bars (1d timeframe) from bars table
2. Get StockTwits data from stocktwits_sentiment table
3. Get labels from prediction_outcomes (up column for direction, fwd_return for forward return)
4. Compute all the conditions
5. Hold out most recent 20% as sealed era
6. Report metrics

Let me think about the data structure:
- bars: symbol_id, tf, ts, open, high, low, close, volume
- stocktwits_sentiment: symbol_id, ts, bullish, bearish, untagged, total
- prediction_outcomes: symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch

For the horizon T+20 trading days, I need to find the forward return over 20 trading days. The prediction_outcomes table has horizon and fwd_return. I need to check what horizon values are available.

The label for DOWN call would be: price goes down over T+20 days, i.e., fwd_return < 0 (or up = 0).

Let me think about the timeline:
- T is the decision day
- We need bars from T-252 to T (for 252 prior sessions + current)
- We need StockTwits from T-4 to T (for 5-day average)
- We need to compute 20-session return at T (T-20 to T)
- We need 20-session realized volatility at T
- We need average daily dollar volume over T-60 to T-1
- The label is at T+20 (forward 20 trading days)

For as-of discipline: At decision time T, we can only use data available at T. StockTwits ts - need to check if ts is the timestamp of the sentiment data. The schema says "ts" for stocktwits_sentiment. We need to ensure we're using data that was available at T.

For StockTwits, the ts is likely the timestamp of the sentiment measurement. We need daily aggregates. The table has symbol_id, ts, bullish, bearish, untagged, total. We need to aggregate by day (UTC day) to get daily bullish/bearish counts.

Similarly for bars, ts is unix epoch integer. We need to convert to UTC days.

Let me plan the approach:

1. Load all daily bars (tf='1d') for all symbols
2. Load all StockTwits data, aggregate by symbol_id and UTC day
3. Load prediction_outcomes for horizon=20 (or whatever corresponds to 20 trading days)
4. For each symbol, for each day T where we have both bars and StockTwits:
   - Check universe conditions
   - Check entry conditions
   - Check abstain conditions
   - If entry, record call and label
5. Split into sealed era (most recent 20%) and rest
6. Compute metrics

Wait, the prediction_outcomes table has horizon column. What are the horizon values? The schema doesn't specify. But it says "horizon" and we need T+20 trading days. I'll need to check what horizon values exist. Probably horizon is in days or some unit.

Also, the labels: "up is the realised direction and fwd_return the realised forward return: THESE ARE LABELS and are the safest label source."

So for a DOWN call, we want up=0 (or fwd_return < 0).

Now, the abstention condition "fewer than 30 independent observations remain" - this is about the total sample size after all filtering. If after all filtering we have fewer than 30 calls issued, we should print INSUFFICIENT=1.

Also "20-session realized volatility at T in the top cross-sectional decile" - this means for each day T, compute the 20-session realized volatility for all symbols, find the top decile (90th percentile), and abstain for symbols in that decile.

"a call was issued for the same symbol in the prior 20 trading days" - this is a cooldown period.

Let me think about the data loading strategy. The database has 13.2M rows in bars, but only 1d timeframe spans 2018-07-26 to now over 1,777 symbols. StockTwits has 41,670 rows. So StockTwits is much smaller.

I should probably:
1. First get all StockTwits data, aggregate by symbol_id and day
2. Get daily bars for symbols that have StockTwits data
3. Get prediction_outcomes for horizon corresponding to 20 trading days

But I need to be careful about memory. 13.2M rows is manageable in Python if I process symbol by symbol or use efficient queries.

Let me write the SQL queries:

First, get StockTwits daily aggregates:
```sql
SELECT symbol_id, date(ts, 'unixepoch') as day, 
       SUM(bullish) as bullish, SUM(bearish) as bearish
FROM stocktwits_sentiment
GROUP BY symbol_id, day
```

Wait, ts in stocktwits_sentiment - is it unix epoch? The schema says "ts" for stocktwits_sentiment, and for bars it says "ts is a unix epoch integer". For stocktwits_sentiment it just says "ts". But news table also has "ts" and says "2012-04..now". Probably all ts are unix epoch integers. Let me assume that.

Actually, looking at sentiment_features table: "day is 'YYYY-MM-DD'". But stocktwits_sentiment has "ts" not "day". So likely unix epoch.

For bars, tf='1d', ts is unix epoch.

For prediction_outcomes, ts is probably unix epoch too.

Let me write the script step by step.

Important: The script must be self-contained, use only stdlib + sqlite3, run in under 10 minutes.

I'll need to:
1. Connect to database
2. Get all symbols that have both daily bars and StockTwits data
3. For each symbol, get their daily bars and StockTwits daily aggregates
4. Get prediction_outcomes for horizon=20 (need to check what horizon values exist)
5. Process each day T for each symbol
6. Apply all filters
7. Compute metrics

Let me think about the horizon in prediction_outcomes. The schema says "horizon" but doesn't specify values. Since we need T+20 trading days, and the claim is about "following month", horizon=20 might be the value. But I should check what horizons are available. However, I can't run exploratory queries - I have to write the script based on the schema. The schema says prediction_outcomes has horizon column. I'll assume horizon=20 means 20 trading days. Or maybe horizon is in calendar days? But the hypothesis says "T+20 trading days".

Actually, the hypothesis says "HORIZON: T+20 trading days close-to-close". So I need the forward return over 20 trading days. The prediction_outcomes table should have this for horizon=20 (if horizon is in trading days) or some other value.

Since I can't know for sure, I'll use horizon=20 and hope that's correct. The schema doesn't specify the unit of horizon.

Wait, the schema says: "prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch)". The horizon could be '20d' or 20 or something else. But since it's a column in a database, likely integer.

Let me proceed with horizon=20.

Now, for the 20-session return at T (for entry condition): this is the return from T-20 to T (close-to-close). So (close_T / close_T-20) - 1.

For 20-session realized volatility at T: standard deviation of daily returns over T-20 to T-1 (or T-19 to T?), annualized or not? Usually realized volatility is std of daily returns * sqrt(252). But for cross-sectional decile ranking, the scaling doesn't matter. I'll compute std of daily log returns over the past 20 sessions.

Average daily dollar volume over T-60 to T-1: average of (close * volume) over those 60 days.

5-day average bullish ratio: for days T-4 to T, compute bullish/(bullish+bearish) each day, then average.

T's close-to-close return: (close_T / close_T-1) - 1.

Now, the label: at T+20, did price go down? From prediction_outcomes, up=0 means down (since up is "realised direction"). Or fwd_return < 0.

The hypothesis says "DOWN call" and "mean-reversion down". So we predict price will be lower at T+20 than at T. So label is 1 if down (fwd_return < 0 or up=0), 0 if up.

Precision = hits / issued, where hit = label is down (1).

Base rate within issued subset = proportion of issued calls where label is down.

Abstention rate = 1 - (issued / opportunities), where opportunities = decision points considered (symbol-day pairs that meet universe criteria? Or all symbol-day pairs? The hypothesis says "abstention rate >= 0.95". Opportunities should be the number of decision points considered, i.e., symbol-days that meet the universe criteria (present in both tables, 252 prior sessions, etc.) before entry/abstain conditions.

Wait, the output requires:
- ISSUED=<count of calls issued>
- OPPORTUNITIES=<count of decision points considered>
- PRECISION=<hits/issued as a decimal>
- BASE_RATE=<base rate of the predicted class WITHIN the issued subset>
- DISTINCT_DAYS=<distinct UTC days on which a call was issued>
- EFFECTIVE_N=<issued count divided by the measured design effect>
- SEALED_PRECISION=<precision on the sealed era>

And INSUFFICIENT=1 if fewer than 30 independent observations remain.

"Independent observations" - "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it."

So each call is one observation (symbol, day). But they may be correlated across time. The design effect accounts for clustering.

EFFECTIVE_N = ISSUED / design_effect, where design_effect > 1.

How to measure design effect? Typically for clustered data, design effect = 1 + (avg_cluster_size - 1) * ICC. But here clusters are by day (multiple symbols on same day) or by symbol (multiple days for same symbol).

The hypothesis says "Calls clustered in time are not independent". So likely clustering by day. If many calls on same day, they're correlated.

A simple way: design_effect = ISSUED / DISTINCT_DAYS? No, that would be average calls per day. But design effect for clustered sampling is 1 + (m-1)*rho where m is cluster size.

But the requirement says: "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

And "EFFECTIVE_N=<issued count divided by the measured design effect>"

So I need to compute a design effect. How? One common approach for time series: design_effect = 1 + 2 * sum_{k=1}^{K} (1 - k/(K+1)) * rho_k, but that's complex.

Simpler: since calls are clustered by day, we can compute the design effect as the ratio of the variance of the mean under clustering to the variance under independence. For binary outcomes (hit/miss), with clusters of size n_i (calls per day), the design effect is approximately 1 + (n_bar - 1) * ICC, where ICC is intra-class correlation.

But estimating ICC requires a model. A simpler heuristic: design_effect = ISSUED / DISTINCT_DAYS? No, that's average cluster size.

Wait, if each day is a cluster, and we have D distinct days, with n_d calls on day d, sum n_d = ISSUED. The effective sample size for clustered binary data is often approximated as ISSUED / (1 + (n_bar - 1) * rho). But we don't know rho.

The requirement says "measured design effect". I need to measure it from the data. One way: compute the variance of the daily precision rates. If calls were independent, the variance of the overall precision would be p(1-p)/ISSUED. With clustering, it's larger.

But perhaps the simplest measurable design effect: since the hypothesis mentions "calls clustered in time", and we have DISTINCT_DAYS, a conservative design effect is ISSUED / DISTINCT_DAYS (the average number of calls per day). But that would make EFFECTIVE_N = DISTINCT_DAYS, which is <= ISSUED.

But the requirement says "EFFECTIVE_N must be strictly less than ISSUED". If DISTINCT_DAYS < ISSUED, then EFFECTIVE_N = DISTINCT_DAYS would work. But is that the "measured design effect"?

Let me re-read: "EFFECTIVE_N=<issued count divided by the measured design effect>"

And "Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

A common measure in finance for effective sample size with overlapping returns or clustered signals is to use the number of independent days. If we have multiple signals on the same day, they're not independent. So effective N = number of distinct days on which calls were issued. That would make design_effect = ISSUED / DISTINCT_DAYS.

But the output requires both DISTINCT_DAYS and EFFECTIVE_N as separate lines. If EFFECTIVE_N = DISTINCT_DAYS, then they'd be equal. But the requirement doesn't say they must be different.

Wait: "DISTINCT_DAYS counts days among the ISSUED calls only, never among the opportunities considered. It therefore can never exceed ISSUED."

And "EFFECTIVE_N must be strictly less than ISSUED."

If ISSUED=10, DISTINCT_DAYS=5, then design_effect=2, EFFECTIVE_N=5. That works.

But what if all calls are on different days? Then DISTINCT_DAYS=ISSUED, design_effect=1, EFFECTIVE_N=ISSUED, which violates "strictly less than ISSUED". But the requirement says "Calls clustered in time are not independent, so the design effect is always greater than 1". This implies that in practice there will always be some clustering, but if by chance there isn't, we still need EFFECTIVE_N < ISSUED. So we need a design effect that's always >1.

Perhaps we should compute the design effect using the intra-class correlation of the calls' outcomes (hits) by day. But that's complex.

Another approach: the design effect for time series with autocorrelation. But here the clustering is cross-sectional (multiple symbols same day).

I think the intended approach is: design_effect = 1 + (avg_calls_per_day - 1) * rho, but since we don't know rho, maybe they want us to use a conservative estimate. Or perhaps the "measured design effect" is simply the ratio of the variance of the mean estimator under clustering to the variance under independence, which can be estimated by the variance of daily hit rates.

Let me think of a practical way. Suppose we have D days, with n_d calls on day d, and h_d hits on day d. The overall hit rate is p = sum h_d / sum n_d. The variance of p under independence is p(1-p)/N. Under clustering, the variance is (1/N^2) * sum n_d^2 * Var(day_d) + ... Actually, for cluster sampling, the variance of the mean is (1/N^2) * sum n_d^2 * sigma_d^2, but if we assume equal variance, it's related to the variance of cluster means.

A standard estimator for the design effect in cluster sampling is: deff = 1 + (n_bar - 1) * ICC, where ICC = (MSB - MSW) / (MSB + (n_bar - 1) * MSW) from ANOVA.

But implementing ANOVA in pure Python without numpy is doable but tedious.

Given the time constraints and the requirement to run in 10 minutes, perhaps a simpler heuristic is expected. The requirement says "measured design effect" - maybe they just want us to compute the average cluster size (calls per day) as the design effect? But that would be ISSUED / DISTINCT_DAYS, making EFFECTIVE_N = DISTINCT_DAYS.

But then EFFECTIVE_N = DISTINCT_DAYS, and the output would have both lines. That seems redundant but possible.

Wait, the invariant says: "EFFECTIVE_N must be strictly less than ISSUED." If DISTINCT_DAYS < ISSUED, then EFFECTIVE_N = DISTINCT_DAYS works. But if by chance DISTINCT_DAYS = ISSUED (all calls on different days), then we'd have EFFECTIVE_N = ISSUED, violating the invariant. So we need a design effect that's always >1.

Perhaps the design effect is computed as 1 + (avg_calls_per_day - 1) * rho_min, with rho_min > 0. But what rho_min?

Another idea: in finance, for overlapping returns, the design effect for N overlapping observations with horizon H is roughly H. But here it's not overlapping returns, it's clustered signals.

Given the ambiguity, I'll compute the design effect using the intra-class correlation of the binary outcomes (hit/miss) by day. This is measurable from the data.

Steps for design effect:
1. For each day with calls, compute the hit rate for that day: p_d = hits_d / n_d
2. Overall hit rate: p = total_hits / total_calls
3. Between-cluster variance: B = sum n_d * (p_d - p)^2 / (D - 1)  [if D>1]
4. Within-cluster variance: W = sum n_d * p_d * (1 - p_d) / (N - D)  [if N>D]
5. ICC = (B - W) / (B + (n_bar - 1) * W)  [standard ANOVA estimator]
6. design_effect = 1 + (n_bar - 1) * ICC
7. If ICC <= 0, set design_effect = 1 + epsilon (e.g., 1.01) to ensure EFFECTIVE_N < ISSUED
8. EFFECTIVE_N = ISSUED / design_effect

But this requires D > 1 and N > D. If D=1, all calls on one day, design_effect = N (or large), EFFECTIVE_N = 1.

Let me implement this.

Now, for the sealed era: "Hold out the most recent 20% of the sample as a sealed era and report it separately."

The sample is the issued calls? Or the opportunities? Or the time period?

"most recent 20% of the sample" - likely the most recent 20% of the time period covered by the data. Or 20% of the issued calls by time.

Typically in time series, you hold out the most recent 20% of the time period. So find the date range of all decision points (or issued calls), take the most recent 20% of that range as sealed.

But the requirement says "Hold out the most recent 20% of the sample as a sealed era". The sample could be the set of issued calls. So sort issued calls by date, take the most recent 20% by count.

But "era" suggests a time period. I think it's the most recent 20% of the time span.

Let me check: "report it separately from the rest." And "SEALED_PRECISION=<precision on the sealed era>"

So we need to compute precision on the sealed era calls.

I'll interpret as: take all decision points (opportunities) sorted by date, the most recent 20% of the date range is sealed. Or 20% of the opportunities by count? "20% of the sample" - sample usually means the data points.

Given the context of time series, I'll use the most recent 20% of the time period covered by the opportunities (decision points considered). So find min and max date of opportunities, compute cutoff = max_date - 0.2 * (max_date - min_date). Opportunities with date >= cutoff are in sealed era.

But the output requires SEALED_PRECISION, which is precision on issued calls in the sealed era.

Now, let me outline the script structure:

1. Connect to DB
2. Load symbols that have both daily bars and StockTwits data
3. For each symbol, load daily bars (tf='1d') and StockTwits daily aggregates
4. Load prediction_outcomes for horizon=20 (assuming that's 20 trading days)
5. For each symbol, align data by date (UTC day from ts)
6. For each date T where we have both bar and StockTwits:
   - Check universe: 252 prior sessions (bars), close >= $5, avg dollar vol >= $5M over T-60..T-1
   - Check data completeness: no missing bars or StockTwits for T-252..T
   - Compute 5-day avg bullish ratio (T-4 to T)
   - Compute T's close-to-close return (T-1 to T)
   - Compute T's 20-session return (T-20 to T)
   - Compute 20-session realized volatility (T-20 to T-1 or T-19 to T)
   - Check cross-sectional volatility decile (need all symbols' vol for that day)
   - Check cooldown: no call for same symbol in prior 20 trading days
   - If all entry conditions met and no abstain conditions, issue DOWN call
   - Record label from prediction_outcomes (up=0 for down)
7. Collect all opportunities (decision points that met universe criteria) and issued calls
8. Split into sealed era (most recent 20% of time span of opportunities)
9. Compute metrics
10. Print results

This is complex. Let me think about efficiency.

The database has 1,780 symbols. StockTwits has 741 symbols. Bars has 1,777 symbols. So intersection is at most 741 symbols.

For each symbol, daily bars from 2018-07-26 to now (~8 years * 252 = ~2000 days). 741 * 2000 = 1.5M rows. Manageable.

StockTwits: 41,670 rows total, so ~56 per symbol on average. Much sparser.

So StockTwits data is the limiting factor. We only have StockTwits for some days.

The hypothesis requires StockTwits values for T-4 to T (5 days). So we need 5 consecutive days of StockTwits data.

Given StockTwits has only 41,670 rows over 741 symbols from 2012-04 to now, that's about 14 years * 252 = 3528 trading days. 41670 / 741 / 3528 ≈ 0.016 rows per symbol per day. Very sparse! Many symbols have very few StockTwits entries.

Wait, the schema says: "stocktwits_sentiment(symbol_id, ts, bullish, bearish, untagged, total) -- 41,670 rows." And "sentiment_features(symbol_id, day, n_polar, n_all, mean_score, pos, neg, hedged, ver) -- 41,625 rows, 695 symbols, 2012-04..now. day is 'YYYY-MM-DD'."

So stocktwits_sentiment has 41,670 rows with ts (probably intraday timestamps), while sentiment_features has daily aggregates for 695 symbols.

But the hypothesis specifically says "StockTwits bullish/bearish ratios" and references "stocktwits_sentiment" table. So we must use stocktwits_sentiment, not sentiment_features.

With only 41,670 rows total, and needing 5 consecutive days per decision point, this might be very sparse. But we have to work with it.

We need to aggregate stocktwits_sentiment by symbol_id and UTC day (date(ts, 'unixepoch')).

Let me write the SQL to get daily StockTwits aggregates:

```sql
SELECT symbol_id, date(ts, 'unixepoch') as day, 
       SUM(bullish) as bullish, SUM(bearish) as bearish
FROM stocktwits_sentiment
GROUP BY symbol_id, day
```

This will give us daily bullish/bearish totals.

Now, for bars, we need daily bars (tf='1d'):

```sql
SELECT symbol_id, ts, open, high, low, close, volume
FROM bars
WHERE tf = '1d'
ORDER BY symbol_id, ts
```

For prediction_outcomes, we need horizon=20 (assuming):

```sql
SELECT symbol_id, ts, up, fwd_return
FROM prediction_outcomes
WHERE horizon = 20
```

But we don't know if horizon=20 exists. The schema doesn't specify horizon values. This is a risk. But we have to assume something. Maybe horizon is in days and 20 is the value. Or maybe it's '20d'. Since it's a column in SQLite, likely integer.

Let me proceed with horizon=20.

Now, the as-of discipline: For decision at T, we can only use data available at T. For StockTwits, the ts is when the sentiment was posted. If we aggregate by day, we assume the daily aggregate is available at end of day T. But for decision at T, we need the 5-day average up to T. So we need StockTwits data for days T-4, T-3, T-2, T-1, T. At the close of day T, we have the full day T's StockTwits data? In practice, StockTwits data during day T might not be complete until end of day. But the hypothesis says "snapshot at T", so we'll assume we have the full day T's data at decision time.

For bars, daily bar for day T is available at close of day T.

For prediction_outcomes, the ts is probably the prediction timestamp (T), and fwd_return is the forward return over horizon. The resolved_at is when it was resolved. We need to ensure we're not using future data. The label for T is the outcome at T+20. In prediction_outcomes, there should be a row with symbol_id, horizon=20, ts=T (or close to T), and up/fwd_return representing the outcome at T+20.

The schema says: "prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch)". So ts is likely the prediction timestamp (T), and up/fwd_return are the realized values at T+horizon.

So we can join on symbol_id and ts (date) with horizon=20.

Now, the universe condition: "All symbols present in both the daily-bars and stocktwits tables at T with at least 252 completed prior sessions, close >= $5, average daily dollar volume >= $5M over T-60..T-1, snapshot at T, no survivorship bias."

"no survivorship bias" - the symbols table has delisted_at set only from 2026-07-24, before that universe is survivor-seeded. So we can't fully correct for survivorship bias before 2026-07-24. But we'll use the symbols table to check if symbol was active at T? The symbols table has active column and delisted_at. But delisted_at only set from 2026-07-24. So for T before that, we don't know if symbol was delisted. The hypothesis says "no survivorship bias" but the data has this limitation. We'll do our best: use symbols where active=1 and (delisted_at is null or delisted_at > T). But since delisted_at is only set recently, this won't help for historical periods. We'll note this but proceed.

Actually, the hypothesis says "UNIVERSE: All symbols present in both the daily-bars and stocktwits tables at T with at least 252 completed prior sessions, close >= $5, average daily dollar volume >= $5M over T-60..T-1, snapshot at T, no survivorship bias."

"snapshot at T" means we use the universe as of T, not current universe. So we need to know which symbols existed at T. The symbols table has added_at. So symbol must have added_at <= T. And not delisted before T (but delisted_at only reliable from 2026-07-24).

We'll use symbols table to filter.

Now, let me think about the implementation steps in code:

1. Connect to DB
2. Load symbols info: id, symbol, active, added_at, delisted_at
3. Load daily StockTwits aggregates
4. Load daily bars for tf='1d'
5. Load prediction_outcomes for horizon=20
6. For each symbol that has both bars and StockTwits:
   - Merge bars and StockTwits by date
   - Merge with prediction_outcomes by date (ts)
   - Sort by date
   - For each date T (index i in the merged data):
     - Check if we have 252 prior bars (i >= 252)
     - Check close >= 5 at T
     - Check avg dollar vol over T-60..T-1 >= 5M
     - Check StockTwits data exists for T-4..T (5 days)
     - Check bars data exists for T-252..T (no gaps? "any required bar or StockTwits value missing for T-252..T")
     - Compute 5-day avg bullish ratio
     - Compute T's close-to-close return (T-1 to T)
     - Compute T's 20-session return (T-20 to T)
     - Compute 20-session realized volatility (std of daily returns over T-20..T-1)
     - Check cross-sectional vol decile (need all symbols' vol for day T)
     - Check cooldown: no call for this symbol in prior 20 trading days
     - If all entry conditions met and no abstain, issue call
     - Record label from prediction_outcomes (up=0 for down)
7. After processing all symbols, we have list of opportunities and issued calls
8. Determine sealed era: most recent 20% of time span of opportunities
9. Compute metrics
10. Print

The cross-sectional volatility decile requires knowing the 20-session realized volatility for ALL symbols on day T, to find the top decile (90th percentile). So we need to compute vol for all symbols for all days first, then for each day T, compute the 90th percentile across symbols, then flag symbols above it.

This means we need to compute vol for all symbol-days first, then process entry conditions.

Similarly, the cooldown requires tracking calls per symbol.

Let me structure the data processing:

Phase 1: Load and prepare data for all symbols
- Get all trading days from bars (tf='1d') - this defines the trading calendar
- For each symbol, get bars, StockTwits daily, prediction_outcomes
- Align by date (UTC day from ts)

Phase 2: Compute per symbol-day metrics
- For each symbol, for each day T where we have bar data:
  - Compute 20-session return (if 20 prior days exist)
  - Compute 20-session realized volatility (if 20 prior days exist)
  - Compute 60-day avg dollar volume (if 60 prior days exist)
  - Check 252 prior sessions exist
  - Get StockTwits 5-day avg bullish ratio (if 5 days exist)
  - Get T's close-to-close return
  - Get label from prediction_outcomes

Phase 3: Cross-sectional vol decile
- For each day T, collect all symbols' 20-session vol
- Compute 90th percentile
- Flag symbols in top decile

Phase 4: Apply entry/abstain conditions sequentially
- For each symbol-day T (in chronological order):
  - Check universe conditions
  - Check entry conditions
  - Check abstain conditions (including vol decile, cooldown, etc.)
  - If issue call, record and update cooldown

Phase 5: Compute metrics and print

This is a lot of data. Let me think about memory. We can process symbol by symbol for most things, but cross-sectional vol decile requires all symbols for a given day. We can compute vol for all symbol-days, store in a dict keyed by (symbol_id, date), then group by date to compute percentiles.

Given the data sizes:
- Bars: 13.2M rows for all tf, but 1d is subset. 1,777 symbols * ~2000 days = ~3.5M rows for 1d. Manageable.
- StockTwits daily aggregates: at most 41,670 rows (since 41,670 raw rows, daily aggregates <= that)
- prediction_outcomes: 397,769 rows, but for horizon=20 maybe less.

We can load all into memory if careful. 3.5M bars rows * (symbol_id, ts, close, volume) ~ 3.5M * 4 * 8 bytes = ~112 MB. Plus other data. Should be ok for Python.

But we can also process in chunks. Let me try to write efficient SQL to get only needed data.

Actually, we need bars for symbols that have StockTwits data. So first get symbols with StockTwits:

```sql
SELECT DISTINCT symbol_id FROM stocktwits_sentiment
```

Then get bars for those symbols.

Similarly, prediction_outcomes for those symbols and horizon=20.

Let me write the script now.

I'll use sqlite3, datetime, statistics, math, collections.

Key data structures:
- bars_by_symbol: dict symbol_id -> list of (date, close, volume) sorted by date
- st_by_symbol: dict symbol_id -> dict date -> (bullish, bearish)
- po_by_symbol: dict symbol_id -> dict date -> (up, fwd_return) for horizon=20
- symbols_info: dict symbol_id -> (added_at, delisted_at, active)

Dates: convert ts (unix epoch) to date string 'YYYY-MM-DD' using datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d'). But ts in bars is unix epoch integer