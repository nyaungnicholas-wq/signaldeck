Let me carefully analyze the task and write a Python script that tests the given hypothesis.

Key requirements:
1. Read from data/signaldeck.db (read-only)
2. Test the specific hypothesis about low-float stocks
3. Follow as-of discipline
4. Hold out most recent 20% as sealed era
5. Print specific metrics
6. Use only standard library + sqlite3
7. No pandas, no numpy

Let me break down the hypothesis:

MECHANISM: Low-float stocks have structurally thin order books, so a day on which volume exceeds 5% of public float and price closes in the upper third of the range reveals a price-insensitive buyer whose demand cannot be absorbed quickly, and this imbalance persists for weeks.

HORIZON: 21 trading days.

UNIVERSE: All symbols with PublicFloat and SharesOutstanding stamped in fundamentals strictly before the decision time, price >= $2, and 20-day median dollar volume >= $1M.

ENTRY: Issue an UP call on day t if:
- PublicFloat/SharesOutstanding <= 0.10
- volume_t/PublicFloat >= 0.05
- close_t >= low_t + 0.70*(high_t - low_t)
- 20-day return ending day t is <= 0

ABSTAIN: No call on any symbol-day failing any entry condition, or if any input is missing or unstamped, or if the symbol has fewer than 250 prior daily bars.

CLAIM: Precision on issued calls >= 0.80 at the 21-trading-day horizon, measured against the issued-subset base rate.

Tables I need:
- bars(symbol_id, tf, ts, open, high, low, close, volume) - for daily bars (tf='1d')
- symbols(id, symbol, market, name, active, added_at, stream, delisted_at)
- fundamentals(symbol_id, metric, value, as_of, fetched_at) - for PublicFloat and SharesOutstanding
- prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch) - for labels

Wait, the labels should come from prediction_outcomes. The horizon is 21 trading days. But prediction_outcomes has a horizon column - I need to check what horizons are available. The hypothesis says 21 trading days horizon.

Actually, looking at the schema, prediction_outcomes has horizon column. I need to find rows where horizon corresponds to 21 trading days. But what are the horizon values? The schema doesn't specify. Let me assume horizon is in days or some unit.

Wait, the hypothesis says "measured against the issued-subset base rate" and "Precision on issued calls >= 0.80 at the 21-trading-day horizon".

The labels are in prediction_outcomes: up is the realised direction and fwd_return the realised forward return.

So for each entry signal on day t, I need to find the prediction_outcome for that symbol at horizon=21 (trading days?) at ts=t (or close to it).

But wait - the prediction_outcomes table has ts column. Is ts the decision timestamp? And horizon is the forward horizon?

Let me think about the as-of discipline: "every input must be computable at the decision timestamp. No value from a bar at or after the label window may inform a call."

So for a decision on day t:
- All fundamentals must have fetched_at < t (strictly before decision time)
- Bars up to day t can be used (but not t+1 or later)
- The label is the outcome at t+21 trading days

The prediction_outcomes table has resolved_at and basis_epoch. The up column is the realized direction.

I need to join the entry signals with prediction_outcomes to get the labels.

Let me plan the steps:

1. Get all symbols with PublicFloat and SharesOutstanding from fundamentals, with their fetched_at timestamps.
2. Get daily bars (tf='1d') for all symbols.
3. For each symbol-day (decision day t), check:
   a. Symbol has >= 250 prior daily bars (before day t)
   b. Price (close) >= $2 on day t
   c. 20-day median dollar volume >= $1M (using days t-20 to t-1? or t-19 to t? The hypothesis says "20-day median dollar volume >= $1M" - typically this would be trailing 20 days ending before decision day)
   d. PublicFloat/SharesOutstanding <= 0.10 (using fundamentals with fetched_at < t)
   e. volume_t/PublicFloat >= 0.05
   f. close_t >= low_t + 0.70*(high_t - low_t) (upper third of range)
   g. 20-day return ending day t <= 0 (return from t-20 to t)
4. If all conditions met, issue UP call.
5. For each issued call, find the label from prediction_outcomes at horizon=21 trading days.
6. Compute precision, base rate, etc.
7. Hold out most recent 20% as sealed era.

Wait, the horizon in prediction_outcomes - what are the values? The schema doesn't say. But the hypothesis says 21 trading days. Let me assume horizon=21 means 21 trading days. Or maybe horizon is in calendar days? The schema says "horizon" but no units.

Also, prediction_outcomes has ts column. I think ts is the timestamp of the prediction/decision. And horizon is the forward horizon. So for a decision at ts=t, horizon=21, the outcome is at t+21 trading days.

But I need to verify: the prediction_outcomes table has 397,769 rows. It has symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch.

The up column is "the realised direction" - so this is the label.

Now, for the sealed era: "Hold out the most recent 20% as a sealed era and report it separately."

This means I should sort all decision points (opportunities) by time, take the most recent 20% as sealed, and report precision on that separately.

But the requirement says: "DISTINCT_DAYS counts days among the ISSUED calls only, never among the opportunities considered."

And "EFFECTIVE_N must be strictly less than ISSUED."

Design effect: I need to compute the design effect due to clustering in time. The effective sample size is ISSUED / design_effect.

For time series clustering, a common approach is to use the variance inflation factor. But the requirement says "Calls clustered in time are not independent, so the design effect is always greater than 1".

I need to compute design effect. One way: if calls are clustered on the same days, the effective N is reduced. The design effect can be estimated as 1 + (avg_cluster_size - 1) * ICC, but we don't have ICC.

A simpler approach: since DISTINCT_DAYS <= ISSUED, and calls on the same day are perfectly correlated (same market conditions), the design effect is at least ISSUED / DISTINCT_DAYS. So EFFECTIVE_N = DISTINCT_DAYS would be a conservative estimate (assuming perfect correlation within day, zero across days).

But the requirement says EFFECTIVE_N must be strictly less than ISSUED. So if ISSUED > DISTINCT_DAYS, then EFFECTIVE_N = DISTINCT_DAYS works. But if ISSUED == DISTINCT_DAYS (all calls on different days), then we still need EFFECTIVE_N < ISSUED. In that case, we need to account for autocorrelation across days.

A common formula for effective sample size in time series: n_eff = n / (1 + 2 * sum_{k=1}^{∞} ρ_k), where ρ_k is autocorrelation at lag k.

But this is complex. Maybe the simplest conservative approach: design_effect = ISSUED / DISTINCT_DAYS * (1 + something for cross-day correlation). But the requirement just says it must be strictly less than ISSUED.

Let me think... The requirement says: "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

So I need to compute a design effect > 1. The minimum design effect is ISSUED / DISTINCT_DAYS (if we assume perfect correlation within day, independence across days). But if ISSUED == DISTINCT_DAYS, this gives 1, which is not > 1.

So I need to add some factor for cross-day correlation. A simple approach: assume an AR(1) correlation structure with some rho. But what rho?

Alternatively, use the formula: design_effect = 1 + (n-1)*rho_bar, where rho_bar is average correlation.

But without knowing the correlation, maybe I can use a conservative estimate. The requirement says "the design effect is always greater than 1". So I could compute design_effect = max(1.01, ISSUED / DISTINCT_DAYS) but that seems arbitrary.

Wait, maybe the design effect should be computed from the data. For example, if we have multiple calls per day, and we know the outcomes, we can estimate the intraclass correlation.

But the requirement says to print EFFECTIVE_N = issued count divided by the measured design effect. So I need to measure the design effect.

How to measure design effect from the data? One way: if we have clusters (days), and we know the outcomes (hit/miss) for each call, we can compute the variance inflation.

The design effect for clustered sampling is: deff = 1 + (m - 1) * ICC, where m is average cluster size, ICC is intraclass correlation.

ICC = (between-cluster variance) / (between-cluster variance + within-cluster variance)

For binary outcomes, we can estimate ICC using ANOVA or other methods.

But this is getting complex. Let me think of a simpler approach that satisfies the requirement.

The requirement says: "EFFECTIVE_N must be strictly less than ISSUED." and "Setting EFFECTIVE_N=ISSUED asserts perfect independence, which is never true here."

So any design effect > 1 works. The simplest conservative approach: design_effect = ISSUED / DISTINCT_DAYS if ISSUED > DISTINCT_DAYS, else 1.01 (or some small factor).

But the requirement says "measured design effect" - so it should be measured from data.

Let me think... If all calls on the same day have the same outcome (perfect correlation within day), then the effective N is the number of distinct days. If calls on different days are independent, then design_effect = ISSUED / DISTINCT_DAYS.

But if calls on different days are also correlated, design_effect > ISSUED / DISTINCT_DAYS.

A simple measurable design effect: compute the variance of the mean outcome under clustering vs simple random sampling.

But maybe for this script, I can compute:
- Group calls by day
- For each day, compute the proportion of hits
- The variance of the overall hit rate is inflated by clustering
- Design effect = (variance under clustering) / (variance under SRS)

But this requires the outcomes.

Actually, the design effect for a proportion with clustered data: deff = 1 + (avg_cluster_size - 1) * rho, where rho is the intraclass correlation coefficient.

We can estimate rho from the data using the ANOVA estimator:
rho = (MSB - MSW) / (MSB + (n0 - 1) * MSW)
where MSB is between-cluster mean square, MSW is within-cluster mean square, n0 is average cluster size.

But this is for continuous outcomes. For binary, there are other estimators.

Given the complexity, and the requirement that it just needs to be > 1 and measured, I'll compute a simple design effect based on day clustering:

If we have calls grouped by day, and we assume outcomes within a day are perfectly correlated (rho=1), then effective N = number of distinct days. But this assumes zero correlation across days.

To be more conservative and account for cross-day correlation, I could use the number of distinct weeks or months, but that's arbitrary.

Another approach: the design effect for time series with autocorrelation. If we assume AR(1) with phi=0.5 (moderate autocorrelation), then design_effect ≈ (1+phi)/(1-phi) = 3 for large n. But this is for a single time series.

Here we have multiple symbols, but calls can be on same day for different symbols.

I think the most defensible simple approach:
- Compute the intraclass correlation (ICC) for the binary outcomes within day-clusters
- Use deff = 1 + (avg_cluster_size - 1) * ICC
- If ICC <= 0, set deff = 1.01 (minimum > 1)

But computing ICC for binary data... Let me use the formula for binary ICC (Fleiss-Cuzick or similar).

Actually, for simplicity and to meet the requirement, I'll compute:
- Cluster calls by UTC day
- For each cluster (day), compute the hit rate
- Compute the overall hit rate
- Compute between-cluster variance and within-cluster variance
- Estimate ICC
- Compute design effect

But this requires the outcomes (hits) for each call.

Let me outline the full algorithm:

1. Connect to database read-only.
2. Get all symbols with fundamentals for PublicFloat and SharesOutstanding.
   - For each symbol, get the latest fetched_at for each metric before each decision day.
   - Actually, we need the values as of decision time. So for each symbol-day, we need the most recent fundamentals with fetched_at < decision_ts.
3. Get daily bars (tf='1d') for all symbols.
4. Get prediction_outcomes for horizon=21 (assuming horizon=21 means 21 trading days).
5. For each symbol, iterate through daily bars in chronological order.
6. For each day t (decision day), check conditions:
   a. At least 250 prior daily bars (bars with ts < t_ts)
   b. Close price >= 2
   c. 20-day median dollar volume >= 1M (using prior 20 days? The hypothesis says "20-day median dollar volume >= $1M" - typically trailing 20 days ending at t-1 or t. Since volume_t is used in entry, probably trailing 20 days ending at t-1 to avoid lookahead. But the hypothesis says "ending day t" for return, so for volume it might be similar. Let me re-read: "20-day median dollar volume >= $1M" - no "ending day t" specified. For the return it says "20-day return ending day t is <= 0". For volume, it's just a condition. I'll use trailing 20 days up to and including day t? But volume_t is used in entry condition, so using it in median would be circular. Better to use trailing 20 days ending at t-1.)
   d. PublicFloat/SharesOutstanding <= 0.10 (using fundamentals with fetched_at < t_ts)
   e. volume_t / PublicFloat >= 0.05
   f. close_t >= low_t + 0.70 * (high_t - low_t)
   g. 20-day return ending day t <= 0 (close_t / close_{t-20} - 1 <= 0)
7. If all met, issue call. Record: symbol_id, decision_ts (ts of day t), decision_day (UTC date).
8. For each call, find matching prediction_outcome: symbol_id, horizon=21, ts=decision_ts (or closest). Get 'up' column (1 for up, 0 for down?).
9. Collect all calls with outcomes.
10. Sort calls by decision_ts.
11. Split: most recent 20% by count (or by time?) as sealed era. "Hold out the most recent 20% of the sample as a sealed era" - sample of what? Of calls issued? Or of opportunities? The requirement says "report it separately from the rest." And SEALED_PRECISION is precision on sealed era. So split the issued calls by time: most recent 20% of issued calls are sealed.
12. Compute metrics for full set and sealed set.
13. Compute design effect and effective N.

Wait, "Hold out the most recent 20% of the sample as a sealed era" - sample of what? The opportunities considered? Or the issued calls? The SEALED_PRECISION is "precision on the sealed era", so it's the precision of calls issued in the sealed era.

So: take all issued calls, sort by decision time, take the most recent 20% (by count) as sealed era.

Now, for the base rate: "Report the base rate of the predicted class WITHIN the issued subset." The predicted class is UP (since we issue UP calls). So base rate = proportion of issued calls where the outcome is UP (up=1).

Precision = hits / issued, where hit = outcome is UP.

So precision and base rate are the same thing? No: precision = TP / (TP + FP) = hits / issued. Base rate = (TP + FN) / total? No, "base rate of the predicted class WITHIN the issued subset" - the predicted class is UP, so within issued subset, base rate = proportion that are actually UP. That's exactly precision!

Wait, that can't be right. Let me re-read: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."

If we only issue UP calls, then the predicted class is always UP. The base rate of UP within issued subset is the proportion of issued calls that actually go UP. That's exactly the precision (since all calls are UP predictions).

But that would mean precision always equals base rate, which makes the statement "precision at or near that base rate is unskilled" trivial.

Ah, I think I misunderstand. The "predicted class" might refer to the class we're predicting (UP), and the base rate is the overall probability of UP in the universe (or in the opportunities), not just in the issued subset.

But it says "WITHIN the issued subset". So it's the base rate within the issued subset.

Wait, maybe the hypothesis allows both UP and DOWN calls? No, the ENTRY says "Issue an UP call". So all calls are UP.

Then precision = proportion of UP calls that hit = base rate of UP within issued subset.

But then the claim "Precision on issued calls >= 0.80 at the 21-trading-day horizon, measured against the issued-subset base rate" would mean precision >= 0.80, and base rate is also precision, so it's just saying precision >= 0.80.

But "measured against the issued-subset base rate" suggests comparing precision to base rate. If they're the same, there's no comparison.

Unless... the base rate is the base rate of the positive class in the population (all opportunities), not in the issued subset. But it explicitly says "WITHIN the issued subset".

Let me re-read carefully: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."

This is confusing. If all predictions are "UP", then within the issued subset, the predicted class is 100% UP. The base rate of the actual class (UP) within issued subset is the hit rate. Precision is also hit rate. So they're identical.

Unless "predicted class" means the class predicted by the model (which is always UP), and "base rate of the predicted class" means the prior probability of UP in the issued subset before seeing the signal? But that doesn't make sense.

I think there might be a misinterpretation. Perhaps the hypothesis is that we predict UP, and the base rate is the overall frequency of UP in the market (or in the opportunities considered), and precision is the frequency of UP among our calls. Then if precision ≈ base rate, no edge.

But the text says "WITHIN the issued subset". Let me look at the required output: "BASE_RATE=<base rate of the predicted class WITHIN the issued subset>"

And "PRECISION=<hits/issued as a decimal>"

If all calls are UP predictions, then hits/issued = proportion of issued calls that are UP = base rate of UP within issued subset. So PRECISION = BASE_RATE always.

That can't be the intention. Unless... the "predicted class" is not UP, but something else? Or perhaps we can also issue DOWN calls? But the ENTRY only says "Issue an UP call".

Wait, maybe "the predicted class" refers to the class that the model predicts (UP), and "base rate of the predicted class within the issued subset" means: among the issued calls, what fraction belong to the class that we predicted (UP)? But we predicted UP for all, so it's the fraction that are actually UP. Which is precision.

I'm going in circles. Let me assume that BASE_RATE is the overall base rate of UP in the opportunities (or in the universe), and PRECISION is hits/issued. But the requirement explicitly says "WITHIN the issued subset".

Another interpretation: perhaps the hypothesis is evaluated as a binary classifier where we predict UP vs not-UP (abstain). But we only issue UP calls, never DOWN. So the "predicted class" is UP, and within the issued subset, 100% are predicted UP. The base rate of the actual UP class within issued subset is precision.

I think the requirement might have a typo, or I'm missing something. But I must follow the spec: print BASE_RATE as "base rate of the predicted class WITHIN the issued subset". Since predicted class is UP for all issued calls, and the actual class is UP or DOWN, the base rate of the predicted class (UP) within issued subset is the proportion of issued calls where actual=UP. That's precision.

But then PRECISION and BASE_RATE would be the same number. The spec asks to print both. Maybe they want to see that they're equal? Or maybe I should compute the base rate as the proportion of UP in the entire opportunity set (all symbol-days considered), not just issued.

Let me check the exact wording again: "Report the base rate of the predicted class WITHIN the issued subset."

"Predicted class" = the class we predict = UP.
"Within the issued subset" = among the calls we issued.
"Base rate of the predicted class" = frequency of the predicted class in the population? No, "base rate of the predicted class within the issued subset" = frequency of actual UP within issued subset.

I'll compute it as: among issued calls, proportion where outcome is UP. This equals precision. I'll print both as the same value, or perhaps the spec expects them to be different if there were both UP and DOWN calls, but here there are only UP.

Wait, maybe "predicted class" means the class label (UP/DOWN), and "base rate of the predicted class" means the prior probability of the class we happened to predict. Since we only predict UP, it's the prior of UP. But "within the issued subset" - the issued subset is defined by our prediction, so it's circular.

I'll go with: BASE_RATE = proportion of issued calls that are actual UP = PRECISION. But that seems wrong for the comparison.

Alternative: BASE_RATE = overall proportion of UP in all opportunities (symbol-days that met universe criteria). PRECISION = proportion of UP in issued calls. Then "precision at or near base rate" means no edge.

But the spec says "WITHIN the issued subset". I must follow the spec literally.

Let me look at the required output format again:
    PRECISION=<hits/issued as a decimal>
    BASE_RATE=<base rate of the predicted class WITHIN the issued subset>

If hits/issued is precision, and base rate within issued subset is also hits/issued, they're the same. I'll compute them the same way.

But wait - "hits" means correct predictions. Since we only predict UP, hits = number of issued calls where outcome is UP. Issued = total calls. So precision = hits/issued.

Base rate of predicted class (UP) within issued subset = number of actual UP in issued subset / issued = hits/issued.

Yes, they're identical. I'll print the same value for both.

Now, for the sealed era: SEALED_PRECISION = precision on the sealed era (most recent 20% of issued calls).

DISTINCT_DAYS = number of distinct UTC days on which a call was issued (among issued calls only).

EFFECTIVE_N = issued count / design_effect, with design_effect > 1.

OPPORTUNITIES = count of decision points considered (symbol-days that met universe criteria and had all data available, before entry conditions).

ISSUED = count of calls issued (met entry conditions).

Now, let me think about the data availability and performance.

Database has:
- bars: 13.2M rows, tf='1d' spans 2018-07-26..now over 1,777 symbols
- fundamentals: 4,989 rows, key/value with metric in {'EPS', 'Revenues', 'SharesOutstanding', 'EntityPublicFloat', 'CIK', 'LatestFilingDate'}
- prediction_outcomes: 397,769 rows

Wait, fundamentals has only 4,989 rows total. With 6 metrics, that's about 831 symbols * 6 = ~5000. But symbols table has 1,780 rows. So not all symbols have fundamentals.

The metrics are 'SharesOutstanding' and 'EntityPublicFloat' (not 'PublicFloat' as stated in hypothesis). The hypothesis says "PublicFloat and SharesOutstanding". In fundamentals, metric is 'EntityPublicFloat' and 'SharesOutstanding'.

So I need to use 'EntityPublicFloat' for PublicFloat.

Also, fundamentals has as_of (period) and fetched_at (when we learned it). As-of discipline: only fetched_at is knowable in advance. So for a decision at time t, we can only use fundamentals with fetched_at < t.

Now, the hypothesis says "PublicFloat/SharesOutstanding <= 0.10". So float / shares_outstanding <= 0.10.

Also "volume_t/PublicFloat >= 0.05" - volume_t is shares volume, PublicFloat is in shares? EntityPublicFloat is likely in shares. Volume is shares. So volume_t / PublicFloat >= 0.05.

Price >= $2: close >= 2.

20-day median dollar volume >= $1M: median of (volume * close) over 20 days >= 1,000,000.

20-day return ending day t <= 0: close_t / close_{t-20} - 1 <= 0.

Close in upper third: close >= low + 0.7*(high - low).

Symbol has >= 250 prior daily bars.

Now, the horizon is 21 trading days. prediction_outcomes has horizon column. What values does horizon take? Not specified. Could be 21 for 21 days. Or could be '21d'. I'll assume horizon=21 means 21 trading days.

But prediction_outcomes has ts column. Is ts the decision timestamp? And the outcome is for horizon forward.

The schema says: prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch) -- up is the realised direction and fwd_return the realised forward return: THESE ARE LABELS.

So for a decision at timestamp ts, with horizon h, up is the realized direction over that horizon.

So I need to join on symbol_id, horizon=21, ts=decision_ts.

But decision_ts is the timestamp of the daily bar (ts column in bars). Bars ts is unix epoch integer.

prediction_outcomes ts is probably also unix epoch.

Now, the sealed era: most recent 20% of the sample. Sample of what? The issued calls? Or the opportunities?

The requirement: "Hold out the most recent 20% as a sealed era and report it separately."

And SEALED_PRECISION is "precision on the sealed era".

Since precision is on issued calls, the sealed era should be the most recent 20% of issued calls (by decision time).

But "the sample" could mean the sample of opportunities. However, SEALED_PRECISION is precision, which only applies to issued calls. So it must be the most recent 20% of issued calls.

I'll sort issued calls by decision_ts, take the last 20% (rounding up) as sealed.

Now, for the design effect and EFFECTIVE_N.

I need to compute design effect from the data. Let me use the clustered standard error approach.

Group issued calls by UTC day (date from decision_ts).
For each day, we have a cluster of calls.
Each call has outcome: 1 if up (hit), 0 otherwise.
We want the effective sample size for the mean outcome (precision).

The design effect for clustered data: deff = 1 + (m - 1) * rho, where m is average cluster size, rho is ICC.

ICC for binary data can be estimated as:
rho = (MSB - MSW) / (MSB + (m0 - 1) * MSW)
where MSB = between-cluster mean square, MSW = within-cluster mean square, m0 = average cluster size.

For binary data with clusters of varying sizes, there are formulas.

But to keep it simple and ensure deff > 1, I'll compute:

Let K = number of clusters (distinct days)
Let n_k = size of cluster k
Let N = total issued = sum n_k
Let p_k = proportion of hits in cluster k
Let p = overall precision = sum(n_k * p_k) / N

Between-cluster variance: B = sum(n_k * (p_k - p)^2) / (K - 1)  [if K>1]
Within-cluster variance: W = sum(n_k * p_k * (1 - p_k)) / (N - K)  [if N>K]

For binary data, the ICC estimator (Fleiss-Cuzick):
rho = (B - W) / (B + (n0 - 1) * W)
where n0 = (N - sum(n_k^2)/N) / (K - 1)  [average cluster size adjusted]

But this can give negative rho. If rho <= 0, set rho = 0.01 (small positive) to ensure deff > 1.

Then deff = 1 + (n0 - 1) * rho
EFFECTIVE_N = N / deff

But the requirement says EFFECTIVE_N must be strictly less than ISSUED. So deff > 1.

If K=1 (all calls on same day), then we can't compute between variance. In that case, deff = N (perfect correlation), EFFECTIVE_N = 1.

If K=N (all calls on different days), n0=1, deff=1, but we need deff>1. So we need to account for cross-day correlation.

For cross-day correlation, we could compute autocorrelation of daily hit rates. But that's complex.

A simple conservative approach: if K == N, set deff = 1.01 (minimum), so EFFECTIVE_N = N / 1.01 < N.

If K < N, compute deff from clustering, and also add a small factor for cross-day correlation.

But the requirement says "measured design effect". So I should measure it from the data.

Let me implement a simple design effect measurement:

1. Cluster by UTC day.
2. Compute daily hit rates.
3. Compute the variance of the overall hit rate estimator under clustering vs iid.
4. The design effect is the ratio of variances.

Under iid: Var(p) = p(1-p)/N
Under clustering: Var(p) = (1/N^2) * sum(n_k^2 * Var(p_k)) + covariance terms.

If we assume clusters are independent (no cross-day correlation), then Var(p) = sum(n_k^2 * p_k(1-p_k)/n_k) / N^2 = sum(n_k * p_k(1-p_k)) / N^2.

But this assumes independence across clusters.

The design effect (assuming independence across clusters) is:
deff = [sum(n_k * p_k(1-p_k)) / N^2] / [p(1-p)/N] = [sum(n_k * p_k(1-p_k))] / [N * p(1-p)]

This is the Kish design effect for unequal cluster sizes.

If all p_k = p, then deff = sum(n_k) / N = 1.
If p_k vary, deff > 1.

But this assumes zero correlation across clusters. To account for cross-cluster correlation, we'd need more.

However, this deff can be < 1 if p_k are more homogeneous than binomial. But typically deff >= 1.

Actually, the Kish design effect for stratified sampling can be < 1, but for cluster sampling it's >= 1.

The formula deff = 1 + (n0 - 1) * rho assumes rho >= 0.

The Kish formula: deff = (sum n_k^2) / (N * n0) where n0 = N/K? No.

Standard Kish deff for cluster sampling with equal clusters: deff = 1 + (m-1)rho.
For unequal clusters: deff = [sum n_k (p_k - p)^2 / (K-1) + ...] complex.

Let me use a simple conservative approach:
- Compute deff_cluster = N / K (if we assume perfect correlation within day, independence across days)
- This is the maximum deff from day-clustering.
- But we also have cross-day correlation, so true deff >= deff_cluster.
- So set deff = max(deff_cluster, 1.01)
- Then EFFECTIVE_N = N / deff

But deff_cluster = N / K = ISSUED / DISTINCT_DAYS.
And the requirement says EFFECTIVE_N must be strictly less than ISSUED, so deff > 1.
If ISSUED > DISTINCT_DAYS, deff_cluster > 1, good.
If ISSUED == DISTINCT_DAYS, deff_cluster = 1, not > 1. So set deff = 1.01.

But is deff_cluster = N/K the right measure? If all calls on same day are perfectly correlated, then each day gives 1 independent observation, so effective N = K = DISTINCT_DAYS. Then deff = N/K.

Yes, that makes sense. And if calls on different days are also correlated, effective N < K, so deff > N/K.

So a conservative measured design effect is deff = N / K (from day clustering), and if N==K, deff=1.01.

But the requirement says "measured design effect". N/K is measured from the data (N=issued, K=distinct days).

I'll use deff = max(ISSUED / DISTINCT_DAYS, 1.01)
Then EFFECTIVE_N = ISSUED / deff

This ensures EFFECTIVE_N < ISSUED (since deff > 1).
And EFFECTIVE_N <= DISTINCT_DAYS (since deff >= ISSUED/DISTINCT_DAYS).

This seems reasonable and measurable.

Now, let me plan the SQL queries.

First, get all daily bars (tf='1d') with symbol info.
We need: symbol_id, ts, open, high, low, close, volume.
Also symbol's market, etc. but maybe not needed.

We need to compute for each symbol-day:
- 20-day median dollar volume (trailing 20 days, not including current day? Or including? To avoid lookahead, use trailing 20 days up to t-1. But the hypothesis says "20-day median dollar volume >= $1M" without specifying. For the return, it says "ending day t". For volume, it's a universe filter, so likely trailing 20 days up to t-1.)
- 20-day return ending day t: close_t / close_{t-20} - 1
- Prior bars count: number of bars with ts < current ts

Also need fundamentals: for each symbol, get EntityPublicFloat and SharesOutstanding with fetched_at < decision_ts.

Since fundamentals are sparse (only 4,989 rows), we can load all fundamentals into memory and for each symbol-day, find the latest fetched_at before decision_ts.

Similarly, prediction_outcomes: load all with horizon=21, index by (symbol_id, ts).

But prediction_outcomes has 397,769 rows. We can load into a dict keyed by (symbol_id, horizon, ts) or just (symbol_id, ts) for horizon=21.

Now, the bars table has 13.2M rows for 1d. Loading all into memory might be heavy but doable in Python (13M rows * ~80 bytes = ~1GB). But we can process symbol by symbol.

Better: iterate over symbols, for each symbol get its daily