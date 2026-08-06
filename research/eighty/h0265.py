Let me carefully analyze the task and write a Python script that tests the given hypothesis.

Key requirements:
1. Read from data/signaldeck.db (read-only)
2. Test the specific hypothesis with given mechanism, horizon, universe, entry, abstain conditions
3. Hold out most recent 20% as sealed era
4. Print specific metrics
5. Use only stdlib + sqlite3
6. No pandas, no numpy

Let me break down the hypothesis:

**MECHANISM**: Stocks in confirmed uptrend with short low-volume pullback while news sentiment holds/improves → institutional holders not distributing → dip is liquidity-driven shakeout → reverts as buying resumes.

**HORIZON**: T+10 trading days close-to-close.

**UNIVERSE**: Symbols in both daily-bars and news-sentiment tables at T with:
- >=252 completed prior sessions
- close >= $5
- average daily dollar volume >= $5M over T-60..T-1
- snapshot at T

**ENTRY**: Issue UP call when:
1. close at T-1 > 200-day SMA at T-1
2. 3-day close-to-close return over T-3..T <= -3%
3. each day T-2, T-1, T has dollar volume below its 20-day rolling average
4. 3-day average news sentiment aggregate at T >= its 60-day rolling median for the symbol

**ABSTAIN**: No call if:
- close < $5
- fewer than 252 prior sessions
- not above 200-day SMA at T-1
- 3-day return > -3%
- any of the three days has volume >= its 20-day average
- 3-day average sentiment < 60-day median
- 20-session realized volatility at T in top cross-sectional decile
- a call issued for the same symbol in the prior 20 trading days
- fewer than 30 independent observations remain

**CLAIM**: Precision >= 0.80 on issued UP calls at abstention rate >= 0.95, with precision minus issued-subset base rate >= 0.10.

Now I need to:
1. Get daily bars (tf='1d') from bars table
2. Get news sentiment from sentiment_features table (daily) or news table
3. Compute all indicators
4. Apply entry/abstain rules
5. Get labels from prediction_outcomes (fwd_return for horizon=10? or up for direction)
6. Hold out most recent 20% as sealed era
7. Compute metrics

Let me think about the data sources:
- bars: symbol_id, tf, ts, open, high, low, close, volume
- sentiment_features: symbol_id, day, n_polar, n_all, mean_score, pos, neg, hedged, ver
- prediction_outcomes: symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch

For sentiment, I'll use sentiment_features which has daily aggregates (mean_score). The hypothesis says "3-day average news sentiment aggregate at T" and "60-day rolling median for the symbol".

For labels, prediction_outcomes has `up` (realized direction) and `fwd_return` (realized forward return). Horizon is T+10 trading days. The prediction_outcomes table has a `horizon` column - I need to check what horizons are available. The hypothesis says T+10 trading days close-to-close.

I need to map the decision timestamp T to the prediction_outcomes. The prediction_outcomes has `ts` which is likely the prediction timestamp, and `horizon` which is the horizon. For a T+10 trading day horizon, I need to find rows where horizon corresponds to 10 trading days.

Wait, the schema says prediction_outcomes has `horizon` column. I need to check what values it takes. Could be '10d' or 10 or something else. Since it's not specified, I'll assume horizon=10 means 10 trading days, or maybe horizon='10d'. Let me think... The bars table has tf as '1d' | '1h' | '1m'. The prediction_outcomes horizon might be similar.

Actually, looking at the schema again: "prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch)". The horizon could be an integer (number of days) or a string like '10d'. I'll need to query to see.

But since I can't query interactively, I'll assume horizon is an integer representing trading days, or maybe calendar days. The hypothesis says "T+10 trading days". I'll look for horizon=10.

For the label: `up` is the realized direction (1 for up, 0 for down presumably), and `fwd_return` is the realized forward return. For precision of UP calls, I need to check if `up` = 1 (or > 0) for the horizon=10 at timestamp T.

Now, let me design the algorithm:

1. Load all daily bars (tf='1d') for all symbols
2. Load sentiment_features for all symbols
3. Load prediction_outcomes for horizon=10 (or whatever represents 10 trading days)
4. For each symbol, process chronologically:
   - Compute 200-day SMA
   - Compute 20-day average dollar volume
   - Compute 3-day return (T-3 to T)
   - Compute 3-day average sentiment (T-2, T-1, T)
   - Compute 60-day rolling median of sentiment
   - Compute 20-session realized volatility
   - Check universe criteria (252 prior sessions, close >= $5, avg dollar vol >= $5M over T-60..T-1)
   - Check entry conditions
   - Check abstain conditions
   - If issue call, record it and get label from prediction_outcomes
5. Split into sealed era (most recent 20% of decision points) and rest
6. Compute metrics

Wait, "Hold out the most recent 20% as a sealed era" - 20% of what? The sample of decision points? Or 20% of time? The instruction says "most recent 20% of the sample". I think it means 20% of the decision points (opportunities considered) chronologically.

Also: "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it."

So each (symbol, day) is one observation.

Let me think about the data structures. Since we can't use pandas/numpy, I'll use plain Python with sqlite3.

Steps:

1. Connect to DB
2. Get all symbols that have both daily bars and sentiment_features
3. For each symbol, get daily bars sorted by ts
4. Get sentiment_features sorted by day
5. Get prediction_outcomes for horizon=10 (need to figure out the horizon value)

Actually, I should first explore what horizon values exist. But I can't. Let me assume horizon=10 means 10 trading days. Or maybe horizon='10d'. Let me check the schema again - it doesn't specify the type. I'll query for distinct horizons first in the script.

But the script must be self-contained and run to completion. I'll write it to handle whatever horizon format exists.

Let me think about the timestamp alignment:
- bars.ts is unix epoch integer
- sentiment_features.day is 'YYYY-MM-DD'
- prediction_outcomes.ts is unix epoch integer (probably)

I need to convert between them. For daily bars, ts is likely the epoch for the day (maybe midnight UTC). For sentiment_features, day is 'YYYY-MM-DD'.

For a decision at day T (based on bars), I need:
- Bars up to and including T (ts for T)
- Sentiment up to and including T (day for T)
- Label from prediction_outcomes at ts=T with horizon=10

The prediction_outcomes has `ts` and `horizon`. The `up` and `fwd_return` are the realized values. So for a decision at timestamp T, I look for prediction_outcomes where symbol_id matches, ts = T (or the bar timestamp for T), and horizon = 10 (trading days).

But wait: "No value from a bar at or after the label window may inform a call." The label window is T+1 to T+10. So we can use bars up to T.

Also: "every input must be computable at the decision timestamp. No value from a bar at or after the label window may inform a call."

So at decision time T (end of day T), we have:
- Bars up to T (close of T)
- Sentiment up to T
- We predict T+10 close

The prediction_outcomes at ts=T with horizon=10 should give us the realized up/fwd_return for T+10.

Now, let me think about the 200-day SMA. Need 200 prior sessions. The universe requires >=252 completed prior sessions, so we have enough for 200-day SMA.

20-day rolling average dollar volume: dollar volume = close * volume. 20-day average up to T-1? Or including T? The entry condition says "each day T-2, T-1, T has dollar volume below its 20-day rolling average". The 20-day rolling average for day T should be computed from T-20 to T-1 (20 prior days), not including T, to avoid lookahead. Similarly for T-1: average from T-21 to T-2. For T-2: average from T-22 to T-3.

Wait, "20-day rolling average" typically includes the current day in some contexts, but for as-of discipline, we must use only data available at decision time. At decision time T (after close of T), we know T's volume. But the 20-day average for T should be based on T-19 to T? Or T-20 to T-1?

The condition says "each day T-2, T-1, T has dollar volume below its 20-day rolling average". At decision time T, we know T's dollar volume. The 20-day rolling average for day T could be computed as the average of the prior 20 days (T-20 to T-1) or including T (T-19 to T). Since we're checking if T's volume is below its 20-day average, using T-20 to T-1 makes sense (comparing today's volume to the average of the prior 20 days). Similarly for T-1: compare T-1's volume to average of T-21 to T-2. For T-2: compare T-2's volume to average of T-22 to T-3.

This avoids lookahead.

3-day close-to-close return over T-3..T: (close_T / close_T-3) - 1 <= -0.03

3-day average news sentiment aggregate at T: average of sentiment on T-2, T-1, T. Sentiment from sentiment_features.mean_score.

60-day rolling median for the symbol: median of daily sentiment over prior 60 days (T-60 to T-1? or T-59 to T?). At decision time T, we have sentiment for T. The 60-day rolling median should be based on data available at T. If we include T, it's T-59 to T. If not, T-60 to T-1. The condition says "3-day average news sentiment aggregate at T is >= its 60-day rolling median for the symbol". I think the 60-day rolling median at T should be computed from T-60 to T-1 (prior 60 days), not including T, to be a stable benchmark. But it could include T. Let me think... "rolling median for the symbol" at T typically includes T. But for as-of discipline, at the moment of decision (end of day T), we have T's sentiment. So we could compute median over T-59 to T (60 days including T). Or T-60 to T-1 (60 days prior). I'll use T-60 to T-1 to be safe (60 prior days).

20-session realized volatility at T: standard deviation of daily returns over prior 20 sessions (T-19 to T? or T-20 to T-1?). At decision time T, we have returns up to T (close_T / close_T-1 - 1). So 20-session realized volatility could be std of returns from T-19 to T (20 returns) or T-20 to T-1 (20 prior returns). The condition says "20-session realized volatility at T in top cross-sectional decile". This is cross-sectional: at each T, compute volatility for all symbols, find the top decile (90th percentile), and abstain if the symbol's volatility is in that top decile.

Cross-sectional decile means at each decision date T, across all symbols being considered, compute the 20-day volatility for each, and if a symbol's volatility is in the top 10% (highest volatility), abstain.

"a call issued for the same symbol in the prior 20 trading days" - so we need to track issued calls and enforce a 20-day cooldown per symbol.

"fewer than 30 independent observations remain" - this is an abstain condition. "Independent observations" means (symbol, day) pairs. So if after all other filters, fewer than 30 calls would be issued, we abstain from all? Or is this a global check? The phrasing: "ABSTAIN: No call if ... fewer than 30 independent observations remain." This seems like a global condition - if the total number of issued calls (after all other filters) is < 30, then no calls are issued at all. But that doesn't make sense as a per-call abstain condition. Let me re-read: "ABSTAIN: No call if close < $5; fewer than 252 prior sessions; not above 200-day SMA at T-1; 3-day return > -3%; any of the three days has volume >= its 20-day average; 3-day average sentiment < 60-day median; 20-session realized volatility at T in top cross-sectional decile; a call issued for the same symbol in the prior 20 trading days; or fewer than 30 independent observations remain."

The "fewer than 30 independent observations remain" is listed as one of the abstain conditions. It could mean: at the time of considering a call, if the total number of calls that would be issued (across all symbols and time) is fewer than 30, then don't issue any. But that's a global property, not a per-decision property. Alternatively, it could mean: for a given symbol at time T, if there are fewer than 30 prior independent observations (for that symbol? or globally?) that meet criteria. But "remain" suggests after filtering.

I think it means: after applying all other entry/abstain criteria, if the total number of issued calls across the entire backtest is fewer than 30, then the test is insufficient (INSUFFICIENT=1). But the instruction says "If there is insufficient data, print INSUFFICIENT=1 and exit 0." And "fewer than 30 independent observations remain" is listed under ABSTAIN conditions.

Wait, the ABSTAIN conditions are reasons to not issue a call at a specific decision point. "fewer than 30 independent observations remain" might mean: at decision point T, looking forward, if there are fewer than 30 decision points left in the sample (for sealing?), but that doesn't fit.

Let me re-read the measurement rules: "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it."

And the claim: "Precision >= 0.80 on issued UP calls at abstention rate >= 0.95, with precision minus issued-subset base rate >= 0.10."

The abstention rate >= 0.95 means we issue calls on <= 5% of opportunities.

"fewer than 30 independent observations remain" - I think this is a minimum sample size requirement. If after all filtering, the number of issued calls is < 30, then the test is invalid/insufficient. But it's listed as an ABSTAIN condition, meaning for each potential call, if the total pool of potential calls (or remaining calls) is < 30, abstain.

Given the ambiguity, I'll interpret it as: if the total number of issued calls in the entire backtest (before sealing split) is fewer than 30, then print INSUFFICIENT=1 and exit. But the instruction says "If there is insufficient data, print INSUFFICIENT=1 and exit 0." This is separate from the abstain conditions.

Actually, looking at the ABSTAIN list again, it's a list of conditions where "No call if [condition]". So for each decision point, if any condition is true, no call. "fewer than 30 independent observations remain" - this could mean: at this decision point, the number of decision points remaining in the backtest (including this one) is < 30. But that would only affect the last 30 decision points.

Or it could mean: for this symbol, the number of prior independent observations (decision points where a call could have been issued) is < 30. But "remain" suggests future/remaining.

I think the most reasonable interpretation is: this is a global minimum sample size. If the total number of issued calls across the full backtest is < 30, then the hypothesis test is underpowered and we should return INSUFFICIENT. But the instruction says to check this as an abstain condition per call.

Let me look at the claim again: "Precision >= 0.80 on issued UP calls at abstention rate >= 0.95, with precision minus issued-subset base rate >= 0.10."

The abstention rate >= 0.95 means we only call on <= 5% of opportunities. So if we have many opportunities but few calls, that's fine.

I think "fewer than 30 independent observations remain" is a safeguard: if at any point in the backtest (processing chronologically), the number of remaining decision points (opportunities) is < 30, then stop and don't issue calls for those last few. But that seems odd.

Alternatively, it might mean: for a given symbol at time T, if there are fewer than 30 prior trading days with data (but we already have 252 prior sessions requirement).

I'll interpret it as: after applying all other filters, if the total number of issued calls in the entire dataset is < 30, then print INSUFFICIENT=1 and exit. This aligns with "If there is insufficient data, print INSUFFICIENT=1 and exit 0." And the abstain condition "fewer than 30 independent observations remain" might be a per-symbol minimum, but I think it's global.

Actually, wait. The instruction says: "ABSTAIN: No call if ... fewer than 30 independent observations remain." This is a condition to not issue a call. So for each potential call, we check if "fewer than 30 independent observations remain". If true, we don't issue the call. But what does "remain" mean? Remaining in the backtest? Remaining for that symbol?

Given the confusion, I'll implement it as: if the total number of issued calls (across all symbols and time) in the full backtest is < 30, then INSUFFICIENT=1. But that's not an abstain condition per se, it's a global insufficiency.

Let me re-read the measurement rules: "Never fabricate. If the data is insufficient, print INSUFFICIENT=1 and exit 0."

And the abstain condition is separate. I think the "fewer than 30 independent observations remain" in the abstain list means: at the decision point, if the number of independent observations (symbol-day pairs) that meet all entry criteria up to that point is < 30, then don't issue a call. But "remain" suggests future.

I'll implement a pragmatic approach: track the total number of issued calls. If at the end of the backtest (before sealing), the total issued calls < 30, print INSUFFICIENT=1 and exit. This satisfies the "insufficient data" rule.

Now, for the sealed era: "Hold out the most recent 20% as a sealed era and report it separately." This means take all decision points (opportunities considered), sort by date, take the most recent 20% as sealed. Report metrics on sealed separately.

But the metrics to print include SEALED_PRECISION. So I need to compute precision on the sealed era calls.

Also: "DISTINCT_DAYS counts days among the ISSUED calls only, never among the opportunities considered."

"EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

Design effect = 1 + (avg cluster size - 1) * ICC, but we don't have ICC. A simple approximation: effective_n = issued / design_effect. Design effect for clustered data can be estimated as 1 + (n_cluster - 1) * rho. But we don't know rho.

A common simple approach: effective_n = number of distinct days (if each day is a cluster). But the instruction says DISTINCT_DAYS <= ISSUED, and EFFECTIVE_N < ISSUED. If we set EFFECTIVE_N = DISTINCT_DAYS, that would be < ISSUED if multiple calls per day. But the instruction says "EFFECTIVE_N must be strictly less than ISSUED" and "Setting EFFECTIVE_N=ISSUED asserts perfect independence, which is never true here."

So we need to compute a design effect. The simplest: design_effect = ISSUED / DISTINCT_DAYS (average calls per day). Then EFFECTIVE_N = ISSUED / design_effect = DISTINCT_DAYS. But that would make EFFECTIVE_N = DISTINCT_DAYS, which is <= ISSUED. And if there are multiple calls per day, DISTINCT_DAYS < ISSUED, so EFFECTIVE_N < ISSUED.

But the instruction says "EFFECTIVE_N must be strictly less than ISSUED" and "DISTINCT_DAYS counts days among the ISSUED calls only... can never exceed ISSUED." If all calls are on different days, DISTINCT_DAYS = ISSUED, then EFFECTIVE_N = DISTINCT_DAYS = ISSUED, which violates "strictly less than". So we need a design effect > 1 even when calls are on different days, because calls on adjacent days are also correlated.

A better approach: design effect accounts for autocorrelation. But without a statistical model, we can use a simple heuristic: effective_n = distinct_days * (1 - autocorr) or something. But we don't have autocorr.

The instruction says: "Calls clustered in time are not independent, so the design effect is always greater than 1". This implies that even if calls are on different days, if they're close in time, they're correlated. But the simplest measurable clustering is multiple calls per day.

I think the intended calculation is: design_effect = 1 + (avg_calls_per_day - 1) * rho, but since we don't know rho, maybe they want us to use the number of distinct days as effective_n, but ensure it's strictly less than issued by adding a small fudge factor? No, that's fabrication.

Wait: "EFFECTIVE_N=<issued count divided by the measured design effect>". So we need to measure the design effect. How? The design effect for clustered sampling is 1 + (m - 1) * ICC where m is average cluster size. Clusters could be days. If we have multiple calls per day, m = ISSUED / DISTINCT_DAYS. ICC (intraclass correlation) is unknown but positive. The minimum design effect is 1 (if ICC=0), but the instruction says "design effect is always greater than 1". So we must assume ICC > 0.

But we can't measure ICC without the outcome data. However, we have the outcomes (hits/misses). We could compute the ICC of the outcomes within days. But that's complex.

A simple conservative approach: assume ICC = 0.5 or something? But that's arbitrary.

Another approach: the design effect can be estimated as the ratio of the variance of the mean under clustering to the variance under simple random sampling. For binary outcomes (hit/miss), with clusters of size m_i, the design effect is approximately 1 + (m_avg - 1) * rho, where rho is the intra-cluster correlation.

But we can compute rho from the data: rho = (MSB - MSW) / (MSB + (m_avg - 1) * MSW) where MSB is between-cluster mean square, MSW within-cluster.

This is getting too complex for a script that must run in 10 minutes with stdlib only.

Let me think of a simpler interpretation. The instruction says: "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

Perhaps they just want: EFFECTIVE_N = DISTINCT_DAYS (since each day is a cluster, and calls within a day are perfectly correlated). But if all calls are on distinct days, DISTINCT_DAYS = ISSUED, then EFFECTIVE_N = ISSUED, violating "strictly less than".

Unless... even calls on different days are clustered if they're close. But "DISTINCT_DAYS counts days among the ISSUED calls only". So if we have 10 calls on 10 different days, DISTINCT_DAYS=10, ISSUED=10. Then design effect must be >1, so EFFECTIVE_N < 10.

How to measure design effect? Maybe they want us to compute the effective sample size using the formula for autocorrelated data: n_eff = n / (1 + 2 * sum_{k=1}^{n-1} (1 - k/n) * rho_k). But we don't have a time series of calls; we have a set of call dates.

Given the complexity, I think the intended simple solution is: design_effect = ISSUED / DISTINCT_DAYS (average calls per day). Then EFFECTIVE_N = ISSUED / design_effect = DISTINCT_DAYS. But to ensure EFFECTIVE_N < ISSUED strictly, we can use EFFECTIVE_N = DISTINCT_DAYS - 1 if DISTINCT_DAYS == ISSUED, but that's fabrication.

Wait, the instruction says: "EFFECTIVE_N must be strictly less than ISSUED." and "Setting EFFECTIVE_N=ISSUED asserts perfect independence, which is never true here." So if by chance all calls are on different days, we still need EFFECTIVE_N < ISSUED. This implies that the design effect must account for temporal proximity beyond just same-day clustering.

A practical approach: compute the number of distinct weeks or months? Or use a Newey-West type estimator.

But given the time constraints, I'll use a simple heuristic: design_effect = 1 + (ISSUED / DISTINCT_DAYS - 1) * 0.5, assuming ICC=0.5. But that's arbitrary.

Alternatively, the design effect can be measured as the variance inflation factor. For the precision estimator (proportion), the variance under clustering is p(1-p)/n * design_effect. We can estimate design_effect by comparing the variance of the mean across clusters to the binomial variance.

Let me implement a simple design effect calculation:
- Group issued calls by day.
- For each day, compute the proportion of hits (if multiple calls per day).
- Compute the variance of these daily proportions.
- The design effect = (variance of daily proportions * n_days) / (p * (1-p) / n_calls_per_day_avg) ... this is messy.

Simpler: Kish's effective sample size: n_eff = (sum w_i)^2 / sum(w_i^2) where w_i are weights. For clusters, if each call in a cluster of size m has weight 1/m, then n_eff = number of clusters. But that gives n_eff = DISTINCT_DAYS.

I think the intended answer is EFFECTIVE_N = DISTINCT_DAYS, and the "strictly less than" is ensured because there will always be at least one day with multiple calls in a real backtest. But the instruction says "never true here", implying it's a property of the data. If the data happens to have all calls on different days, we'd have a problem.

To be safe, I'll compute EFFECTIVE_N = DISTINCT_DAYS, and if DISTINCT_DAYS == ISSUED, set EFFECTIVE_N = ISSUED - 1 (but that's fabrication). Or better, compute design effect as 1 + (avg_cluster_size - 1) * min_icc where min_icc = 0.01 or something. But that's arbitrary.

Let me re-read: "EFFECTIVE_N=<issued count divided by the measured design effect>". So I must measure the design effect from the data. The only measurable clustering is same-day calls. So design_effect = 1 + (m - 1) * rho. But rho is not measurable without a model.

Perhaps they want: design_effect = ISSUED / DISTINCT_DAYS (which is the average cluster size). Then EFFECTIVE_N = DISTINCT_DAYS. And the statement "design effect is always greater than 1" means that in this specific hypothesis/test, there will always be at least some days with multiple calls, so ISSUED > DISTINCT_DAYS always. I'll assume that and compute EFFECTIVE_N = DISTINCT_DAYS.

But the instruction says "EFFECTIVE_N must be strictly less than ISSUED" as an invariant that the run must satisfy. So if my computation yields EFFECTIVE_N == ISSUED, the run is discarded. So I must ensure my calculation always gives EFFECTIVE_N < ISSUED.

How about: design_effect = max(1.0, ISSUED / DISTINCT_DAYS) * 1.01? No, that's fabrication.

Another idea: the design effect for time series with autocorrelation. The calls are not randomly distributed in time; they cluster in time (e.g., during market pullbacks). So even if each day has at most one call, consecutive days with calls are correlated. The effective sample size for a time series with autocorrelation rho is n_eff = n * (1 - rho) / (1 + rho). But we don't know rho.

We can estimate rho from the sequence of calls: for the issued calls sorted by date, compute the autocorrelation of the hit/miss indicator at lag 1. But that requires the outcomes.

Let's do this: after we have all issued calls with their outcomes (hit=1 if up, 0 otherwise), sorted by date, we can compute the lag-1 autocorrelation of the hit sequence. Then design_effect = (1 + rho) / (1 - rho) for large n. Then EFFECTIVE_N = ISSUED / design_effect.

This is measurable from the data! And it ensures design_effect > 1 if rho > 0.

But the hit sequence is only for issued calls, which are already selected. The autocorrelation of hits might not reflect the autocorrelation of the calling process.

Better: the design effect for the precision estimator. The precision is the mean of hits. The variance of the mean under autocorrelation is var(mean) = (p(1-p)/n) * (1 + 2 * sum_{k=1}^{n-1} (1 - k/n) * rho_k). For AR(1), this approximates to (p(1-p)/n) * (1+rho)/(1-rho). So design_effect = (1+rho)/(1-rho).

We can estimate rho from the hit sequence of issued calls (which are in chronological order). This seems reasonable and measurable.

Let me implement that.

Now, let's outline the script structure:

1. Connect to DB
2. Query all symbols that have both daily bars and sentiment_features
3. For each symbol, load daily bars (tf='1d') sorted by ts
4. Load sentiment_features sorted by day
5. Load prediction_outcomes for horizon=10 (need to determine horizon value)
6. Process each symbol chronologically:
   - Align bars and sentiment by date
   - For each day T (starting from day 252+), check universe criteria
   - Compute indicators
   - Check entry/abstain
   - If issue call, record (symbol, T, outcome from prediction_outcomes)
7. Collect all decision points (opportunities) and issued calls
8. Sort all decision points by date
9. Split: most recent 20% of decision points = sealed era
10. Compute metrics on full (non-sealed) and sealed
11. Print required lines

But "Hold out the most recent 20% as a sealed era and report it separately." - 20% of what? The sample of decision points (opportunities considered). So we consider all (symbol, day) that meet universe criteria (before entry/abstain). These are the "opportunities". Then we apply entry/abstain to get issued calls. The sealed era is the most recent 20% of opportunities (by date). Then we report SEALED_PRECISION on calls issued in the sealed era.

The metrics:
- ISSUED = total calls issued (across full sample? or non-sealed? The claim is about the full test, but sealed is held out. Typically, you'd report on the non-sealed (training) and sealed (test). But the instruction says "report it separately". The printed metrics include SEALED_PRECISION. The other metrics (ISSUED, PRECISION, etc.) - are they for the full sample or non-sealed?

The claim: "Precision >= 0.80 on issued UP calls at abstention rate >= 0.95..." This is the claim to test. The sealed era is for validation. So likely:
- ISSUED, OPPORTUNITIES, PRECISION, BASE_RATE, DISTINCT_DAYS, EFFECTIVE_N are for the full sample (or non-sealed?)
- SEALED_PRECISION is for the sealed era only.

But the instruction says: "Hold out the most recent 20% as a sealed era and report it separately." And "PRINT exactly these lines at the end... SEALED_PRECISION=<precision on the sealed era>"

It doesn't specify whether the other metrics are for full or non-sealed. Given that sealed is held out, the main metrics should be on the non-sealed (80%) portion. But the claim doesn't mention sealed. The sealed is an additional validation.

I think the standard approach: compute everything on the full sample, but also compute SEALED_PRECISION on the sealed 20%. However, "hold out" suggests the sealed era should not be used for the main metrics. But the main metrics are descriptive of the hypothesis test.

Let me check the measurement rules: "Hold out the most recent 20% as a sealed era and report it separately." This is a rule for the script. So the script must split the data, and the printed metrics (except SEALED_PRECISION) should be for the non-sealed (80%) portion? Or for the full?

The invariant about DISTINCT_DAYS and EFFECTIVE_N applies to the ISSUED calls. If ISSUED is for non-sealed, then DISTINCT_DAYS is for non-sealed issued calls.

I'll assume the split is done on the decision points (opportunities). The most recent 20% of opportunities (by date) are sealed. The entry/abstain logic is applied to all opportunities, but the sealed opportunities are held out for final reporting. The main metrics (ISSUED, PRECISION, etc.) are computed on the non-sealed (80%) opportunities. SEALED_PRECISION is computed on the sealed opportunities.

But the claim is about the overall precision. The sealed era is a holdout test. So the script should report both.

Let me re-read: "Hold out the most recent 20% as a sealed era and report it separately from the rest." So "the rest" (80%) is the main sample. The printed metrics (ISSUED, OPPORTUNITIES, PRECISION, BASE_RATE, DISTINCT_DAYS, EFFECTIVE_N) should be for the main sample (non-sealed). SEALED_PRECISION is for the sealed sample.

Yes, that makes sense.

Now, "OPPORTUNITIES=<count of decision points considered>" - this is the number of (symbol, day) that meet universe criteria