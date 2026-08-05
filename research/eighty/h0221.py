Let me carefully analyze this task. I need to write a Python script that tests a specific trading hypothesis using the given database schema. Let me break down the requirements:

1. **Mechanism**: Large negative news sentiment shock without large price move → delayed negative price adjustment over following month
2. **Horizon**: T+20 trading days close-to-close
3. **Universe**: Symbols in both daily bars and news sentiment, with 252+ prior sessions, close >= $5, avg daily dollar volume >= $5M over prior 60 sessions
4. **Entry**: DOWN call when:
   - 5-session avg news sentiment at T in bottom cross-sectional decile
   - T's close-to-close return between -1% and +1%
   - T's close above 200-session SMA
   - T's volume not above 1.5x 20-session median volume
5. **Abstain**: No call if:
   - Price < $5
   - < 252 prior sessions
   - Any required bar or news sentiment missing for T-5..T
   - 20-session realized volatility at T in top cross-sectional decile
   - Call issued for same symbol in prior 20 trading days
   - < 30 independent observations remain
   - T's close-to-close return outside [-1%, +1%]
6. **Claim**: Precision >= 0.80 on issued DOWN calls at abstention rate >= 0.95, with precision - base_rate >= 0.10

Key constraints:
- Read-only SQLite database
- As-of discipline (no lookahead)
- Hold out most recent 20% as sealed era
- Count independent observations (one per symbol-day)
- Report base rate within issued subset
- Print specific metrics at end
- Standard library + sqlite3 only
- Run under 10 minutes

Let me design the approach:

1. First, I need to get all trading days from bars table (1d timeframe)
2. For each symbol-day (T), compute all required indicators using only data up to T
3. Check entry conditions
4. Check abstain conditions
5. For issued calls, compute label: T+20 close-to-close return (negative = hit for DOWN call)
6. Split into training (80%) and sealed (20%) eras by time
7. Compute metrics

Important: The label comes from prediction_outcomes table (fwd_return for horizon=20) or I need to compute from bars. The schema says prediction_outcomes has fwd_return and up (realized direction). But I need to be careful about as-of discipline.

Actually, the horizon is T+20 trading days close-to-close. I can compute this from bars directly: (close at T+20 / close at T) - 1. For DOWN call, hit = fwd_return < 0.

But wait - prediction_outcomes has horizon, ts, fwd_return, up. The ts there is probably the prediction timestamp. I should verify if I can use it or need to compute from bars. The schema says "prediction_outcomes... up is the realised direction and fwd_return the realised forward return: THESE ARE LABELS and are the safest label source." So I should use prediction_outcomes for labels.

But I need to match: for a decision at T (a trading day), I need the prediction_outcome with horizon=20 (trading days?) and ts=T. Let me check the schema: prediction_outcomes has (symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch). Horizon is probably in days. ts is unix epoch.

Actually, the horizon in the hypothesis is "T+20 trading days". The prediction_outcomes horizon might be in calendar days or trading days. I think it's safer to compute from bars directly to ensure as-of discipline.

Let me plan the data flow:

1. Get all symbols that have both 1d bars and news sentiment
2. Get all trading days (from bars where tf='1d')
3. For each symbol, get its bar history and news sentiment history
4. For each day T (chronologically), compute indicators using data up to T
5. Check conditions
6. If entry conditions met and not abstained, issue DOWN call
7. Track if same symbol had call in prior 20 trading days
8. For issued calls, compute T+20 forward return from bars
9. Split by time: most recent 20% of decision points (by date) are sealed
10. Compute metrics

Wait, "Hold out the most recent 20% as a sealed era" - 20% of what? The sample of decision points? Or 20% of time? Probably 20% of the timeline (most recent dates).

"Count independent observations, not rows: one (symbol, UTC day) is one observation"

Let me think about the database queries. I need to be efficient - 13.2M bars rows, 322K news rows. Can't load everything into memory naively.

Approach:
- First, get list of symbols that have both 1d bars and news sentiment
- Get all trading days for these symbols
- Process symbol by symbol, day by day
- For each symbol, load its bars and news into memory (per symbol)
- Compute rolling indicators

Actually, 1,780 symbols * ~1,777 days (for 1d) = ~3M symbol-days. But many symbols have fewer days. Processing symbol by symbol might work.

But news sentiment: 322,719 rows across 741 symbols. The news table has ts (unix epoch), sentiment, score, etc. The hypothesis uses "5-session average news sentiment". What column? Probably `score` or `sentiment`. The schema shows: news(id, symbol_id, ts, headline, url, source, sentiment, score, rationale, lex_score, lex_ver, lex_polar, lex_hedged). And sentiment_features has daily aggregates.

The hypothesis says "5-session average news sentiment". Since sessions are trading days, and news has timestamps, I need to aggregate news to daily level first, then compute 5-day average.

But there's also sentiment_features table with daily aggregates: (symbol_id, day, n_polar, n_all, mean_score, pos, neg, hedged, ver). This has `mean_score` per day. That might be easier - it's already daily. But it only has 41,625 rows vs 322,719 news rows. And day is 'YYYY-MM-DD'.

The hypothesis says "news sentiment" - could use either. sentiment_features.mean_score seems like a daily average sentiment score. But the hypothesis specifically says "5-session average news sentiment" - I think using the daily aggregated sentiment_features is appropriate and more efficient.

But wait - sentiment_features only goes to 695 symbols, news goes to 741. And sentiment_features starts 2012-04. Let me check: news is 2012-04..now, sentiment_features is 2012-04..now. So they align.

I'll use sentiment_features for daily sentiment (mean_score), aggregated to 5-day average.

Now, for each symbol-day T, I need:
- Close price at T
- Close-to-close return at T: (close_T / close_T-1) - 1
- 200-session SMA at T: average of close[T-199:T+1] (200 days including T)
- Volume at T
- 20-session median volume: median of volume[T-19:T+1]
- 20-session realized volatility: std of daily returns[T-19:T+1] * sqrt(252) or just std of returns
- 5-session avg news sentiment: average of mean_score[T-4:T+1] (5 days including T)
- Prior 60 sessions avg dollar volume: average of (close * volume)[T-59:T] (60 days up to T-1? Or including T? "prior 60 sessions" suggests T-60 to T-1)
- 252 prior sessions: need at least 252 bars before T (so T index >= 252)
- Cross-sectional deciles: at each T, across all symbols active at T, compute deciles for 5-day avg sentiment (bottom decile) and 20-day volatility (top decile)

Cross-sectional deciles are computed at each decision point T across all symbols that have data at T. This is important.

Also, "T's close is above its 200-session simple moving average" - so close_T > SMA_200_T

"T's volume is not above 1.5x its 20-session median volume" - volume_T <= 1.5 * median_volume_20_T

"20-session realized volatility at T in the top cross-sectional decile" - abstain if volatility is in top decile cross-sectionally at T

"a call was issued for the same symbol in the prior 20 trading days" - track issued calls per symbol

"fewer than 30 independent observations remain" - this is about the overall sample? Or per decision? The abstain condition says "fewer than 30 independent observations remain" - probably means if the total number of issued calls so far (or remaining in backtest) is < 30, abstain. But that seems odd. Maybe it means for the current symbol? Or for the current cross-section? Let me re-read: "ABSTAIN: No call if ... fewer than 30 independent observations remain". This is ambiguous. Could mean: if after applying all other filters, fewer than 30 calls would be issued in total, then don't issue any. But that's a global condition. Or it could mean per symbol? I think it's a global minimum sample size condition - if the strategy would produce fewer than 30 total calls, it's not worth trading. But as an abstain condition per decision point, it might mean: at decision time T, if the number of remaining decision points (including current) in the backtest is < 30, abstain. But that doesn't make sense for a backtest.

Actually, looking at the claim: "Precision >= 0.80 on issued DOWN calls at an abstention rate >= 0.95, with precision minus issued-subset base rate >= 0.10." The abstention rate is the fraction of opportunities where no call is issued. "fewer than 30 independent observations remain" might be a condition to ensure statistical significance - if at any point the remaining potential observations are < 30, stop. But in a backtest, we know the full history.

I think this condition means: when considering a decision at T, if the total number of decision points (opportunities) from T onwards is < 30, then abstain. But that's a lookahead condition (knowing how many future opportunities exist). Alternatively, it could mean: if the number of issued calls so far in the backtest is < 30, don't issue more? No.

Let me think differently. "independent observations" = (symbol, UTC day) pairs. "fewer than 30 independent observations remain" - perhaps at the time of the backtest analysis, if the total sample size of issued calls is < 30, the result is insufficient. But as an abstain condition during the simulation, it's weird.

Maybe it means: for a given symbol at T, if that symbol has fewer than 30 prior trading days with data? But we already have 252 prior sessions requirement.

I'll interpret it as: if at decision point T, the number of symbols that have valid data (passing all filters except the cross-sectional decile ones) is < 30, then abstain for all symbols at that T. Because cross-sectional deciles need a minimum cross-section. That makes sense - you can't compute deciles with < 30 symbols.

Actually, "independent observations" are (symbol, day) pairs. At a given day T, the cross-section of symbols is the independent observations for that day. If fewer than 30 symbols have data at T, you can't reliably compute cross-sectional deciles. So abstain for that entire day T.

That seems reasonable. I'll implement: at each T, count symbols that have all required data (bars for 252 days, news for 5 days, etc.). If < 30, skip this T entirely (abstain for all symbols at this T).

Now, the sealed era: "Hold out the most recent 20% as a sealed era and report it separately." 20% of what? 20% of the timeline (trading days), or 20% of the issued calls? Probably 20% of the decision points (opportunities) by time. So sort all decision points by date, take the most recent 20% as sealed.

But the opportunities are per symbol-day. So I should collect all (symbol, T) that are valid decision points (have enough data), sort by T, split 80/20 by date (not by count, but the most recent 20% of dates). Or 20% of the decision points chronologically.

"most recent 20% of the sample" - sample of what? The opportunities considered. I'll take all valid (symbol, T) pairs, sort by T, take the last 20% as sealed.

Now, metrics to print:
- ISSUED = count of calls issued (in non-sealed + sealed? Or total? The sealed precision is separate. Probably ISSUED is total issued across both eras, but SEALED_PRECISION is only on sealed era. But the invariants mention DISTINCT_DAYS among ISSUED calls only. Let me read carefully.

The print lines:
```
ISSUED=<count of calls issued>
OPPORTUNITIES=<count of decision points considered>
PRECISION=<hits/issued as a decimal>
BASE_RATE=<base rate of the predicted class WITHIN the issued subset>
DISTINCT_DAYS=<distinct UTC days on which a call was issued>
EFFECTIVE_N=<issued count divided by the measured design effect>
SEALED_PRECISION=<precision on the sealed era>
```

And invariants:
- DISTINCT_DAYS counts days among the ISSUED calls only, never among the opportunities considered. It therefore can never exceed ISSUED.
- EFFECTIVE_N must be strictly less than ISSUED. Design effect > 1.

So ISSUED is total calls issued (both eras). SEALED_PRECISION is precision on sealed era only. PRECISION is overall precision? Or non-sealed? The claim says "Precision >= 0.80 on issued DOWN calls" - probably overall. But they want SEALED_PRECISION reported separately.

BASE_RATE: "base rate of the predicted class WITHIN the issued subset". Predicted class is DOWN (negative forward return). So base rate = fraction of issued calls where forward return < 0. But wait, that's the same as precision if all calls are DOWN? No: precision = hits / issued. For DOWN calls, hit = forward return < 0. So precision = fraction of issued calls that are hits. Base rate = overall probability of negative return in the issued subset? But the issued subset is exactly the calls we made. The base rate of the predicted class (negative return) within the issued subset is exactly the precision if we define hit as negative return. That doesn't make sense.

Wait: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."

Ah, I see. The predicted class is "DOWN" (we predict negative return). The base rate is the unconditional probability of negative return in the issued subset (i.e., if we randomly guessed DOWN with the same frequency as our calls, what would precision be?). But since we only issue DOWN calls, the base rate of the predicted class (negative return) in the issued subset is just the fraction of issued calls that actually had negative return. That IS the precision.

No, wait. "Base rate of the predicted class" - the predicted class is "DOWN". The base rate of DOWN in the issued subset is 100% because we only issue DOWN calls. That can't be right.

Let me re-read: "Report the base rate of the predicted class WITHIN the issued subset." The predicted class is the event we're predicting: negative forward return (for DOWN calls). The base rate is the frequency of negative forward returns in the issued subset (i.e., among the cases where we issued a call). But that's exactly the precision if all our calls are DOWN predictions.

Unless... the "predicted class" refers to the class we predict (DOWN), and the base rate is the prior probability of that class in the population. But "WITHIN the issued subset" suggests it's computed on the issued subset.

Actually, I think it means: among the issued calls, what fraction actually had negative return? That's the base rate of the actual outcome (negative return) in the issued subset. And precision is also that fraction (since we predict DOWN for all issued calls). So they'd be equal. That can't be.

Unless the hypothesis allows both UP and DOWN calls, but it says "Issue a DOWN call when..." and doesn't mention UP calls. So all issued calls are DOWN.

Wait, maybe "base rate of the predicted class" means: in the issued subset, what is the base rate of the class we're predicting (negative return)? But that's the same as precision for a pure DOWN strategy.

Let me look at the claim: "precision minus issued-subset base rate >= 0.10". If precision = base rate, difference is 0. So they must be different.

Ah! I think I understand. The "predicted class" is the class predicted by the model (DOWN). But the "base rate" is the unconditional probability of the target event (negative return) in the issued subset. But since we only predict DOWN, precision = P(negative return | call issued). The base rate = P(negative return) in the issued subset? That's the same thing.

Unless... the issued subset includes both DOWN and UP calls? But the mechanism only says "Issue a DOWN call when...". No UP calls.

Wait, maybe "base rate of the predicted class" means the base rate of the predicted class label in the training data? No, "WITHIN the issued subset".

Let me think of it as: we have a binary classification (DOWN vs not-DOWN). We only issue DOWN calls. The precision is P(actual DOWN | predicted DOWN). The base rate of the predicted class (DOWN) within the issued subset is 1.0 (since all issued are DOWN). That doesn't work.

Alternative interpretation: The "predicted class" is the target class (negative return). The base rate is the prevalence of negative returns in the issued subset. Precision is also the prevalence of negative returns in the issued subset (since all predictions are negative). So they're identical.

Unless... the hypothesis is that we issue a call (DOWN) when conditions met, but the "predicted class" for base rate purposes is the market direction (up/down), and the base rate is the overall market base rate of down days in the issued subset? But that's the same.

I think there might be a misunderstanding. Let me read the original instruction again: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."

In classification, if you predict class 1 with precision p, and the base rate of class 1 in the population is b, then p > b indicates skill. But "within the issued subset" - the issued subset is the set of instances where we predicted class 1. In that subset, the base rate of class 1 is exactly the precision.

Unless "issued subset" means something else. Maybe "issued subset" means the set of all decision points where a call COULD have been issued (opportunities), and we issued on some of them. Then base rate = fraction of opportunities where the outcome was negative. Precision = fraction of issued calls where outcome was negative. These can differ.

But the text says "WITHIN the issued subset". "Issued subset" likely means the subset where calls were issued.

Let me check the claim: "precision minus issued-subset base rate >= 0.10". If issued-subset base rate is the base rate of the target class in the issued subset, and precision is also that (for pure DOWN strategy), difference is 0.

Unless the strategy can issue both UP and DOWN calls, but the mechanism only describes DOWN. The abstain says "No call if...". So only DOWN or nothing.

I think there might be an error in my understanding. Let me consider: perhaps "predicted class" refers to the class predicted by some external model, but here we're the model. Or perhaps the base rate is computed on the full opportunity set, not the issued subset. But it explicitly says "WITHIN the issued subset".

Another possibility: "base rate of the predicted class" means the base rate of the class that we predict (DOWN) in the overall population (all opportunities), but measured within the issued subset? That doesn't make sense.

Let me look at similar tasks. In the claim: "Precision >= 0.80 on issued DOWN calls at an abstention rate >= 0.95, with precision minus issued-subset base rate >= 0.10."

Abstention rate = 1 - (issued / opportunities) >= 0.95, so issued/opportunities <= 0.05.

Precision = hits / issued.

Issued-subset base rate: maybe it's the base rate of negative returns in the entire opportunity set? But "issued-subset" suggests issued.

Wait: "issued-subset base rate" could mean the base rate of the predicted class (DOWN) in the issued subset. But since all issued are DOWN, that's 1.0. Precision <= 1.0, so precision - 1.0 <= 0. Can't be >= 0.10.

Unless "predicted class" means the actual outcome class (negative return), and "base rate" means the prior probability of that class in the issued subset. But again, that's precision.

I'm stuck. Let me assume that "base rate of the predicted class within the issued subset" means the fraction of issued calls that would be hits if we predicted randomly with the same class balance as the overall market. But that's not standard.

Perhaps it's a typo and should be "opportunity-subset base rate" or "full-sample base rate". But I must follow the spec.

Let me re-read: "Report the base rate of the predicted class WITHIN the issued subset."

In the context of the claim: "precision minus issued-subset base rate >= 0.10". If precision is 0.80, then issued-subset base rate must be <= 0.70. So base rate is not precision.

What if "predicted class" refers to the class predicted by the hypothesis (DOWN), and "base rate" is the frequency of that class in the issued subset? But we only issue DOWN, so it's 100%.

Unless... the hypothesis issues DOWN calls, but the "predicted class" for base rate is the market regime or something else. No.

Another idea: perhaps "issued subset" means the subset of data where the model issues a prediction (as opposed to abstaining), and "base rate of the predicted class" means the base rate of the target variable (negative return) in that subset. But that's exactly the precision for a model that always predicts negative.

Unless the model predicts a probability, and we threshold to get a class. But here it's a deterministic rule: issue DOWN call when conditions met. So all issued calls are class DOWN.

I think the only logical interpretation is that "base rate of the predicted class within the issued subset" is the base rate of the target event (negative forward return) in the full set of opportunities (or in the universe), but the phrase "issued subset" is confusing.

But the spec says: "Report the base rate of the predicted class WITHIN the issued subset." And the claim uses "issued-subset base rate".

Let me consider: maybe the strategy can issue both UP and DOWN calls, but the mechanism only describes DOWN entry. The abstain says "No call if...". So only DOWN or abstain. No UP.

Perhaps "predicted class" is "DOWN call issued", and base rate is the fraction of decision points where a DOWN call is issued? But that's issued/opportunities, the issuance rate. Precision is hits/issued. These are different. But "base rate of the predicted class within the issued subset" - within issued subset, predicted class is always DOWN, so base rate is 1.

I'm going to assume that "issued-subset base rate" means the base rate of negative returns in the overall opportunity set (all valid decision points), and "issued subset" is a misnomer, or it means "in the subset of data from which calls are issued (i.e., the opportunity set)". But the text says "WITHIN the issued subset".

Let me look at the exact wording again: "Report the base rate of the predicted class WITHIN the issued subset."

Maybe "predicted class" = the class we predict (DOWN), and "base rate" = the accuracy of a naive predictor that always predicts the majority class in the issued subset. But in issued subset, all predictions are DOWN, so majority class is DOWN, accuracy = precision.

I give up. I'll compute base rate as the fraction of issued calls that have negative forward return (which equals precision), but that makes the claim impossible. So that can't be.

Wait! "Base rate of the predicted class" - the predicted class is "DOWN" (the call type). The base rate of DOWN calls within the issued subset is 1.0. But precision is hits/issued. The claim says precision - base_rate >= 0.10. If base_rate=1, impossible.

Unless "predicted class" refers to the outcome class (negative return), and "base rate" is the prior probability of negative return in the full population, but measured within the issued subset? No.

Let me search my memory for similar evaluation frameworks. In finance, for a strategy that goes short when a signal triggers, the "base rate" might be the average return of the market or the unconditional probability of negative return. The "issued subset" might be the set of times the signal triggers. The base rate within the issued subset would be the average return or hit rate during signal times if you didn't condition on the signal? That doesn't make sense.

Perhaps it's: base rate = fraction of all trading days (in the universe) that have negative 20-day forward return. Precision = fraction of signal days that have negative 20-day forward return. Then precision - base_rate >= 0.10 means the signal adds 10% over the unconditional probability.

But the spec says "WITHIN the issued subset". That contradicts.

Another thought: "issued subset" might mean the subset of symbols that ever received a call. But "within the issued subset" still unclear.

I'll go with: base rate = overall fraction of (symbol, day) opportunities that have negative 20-day forward return. Precision = fraction of issued calls that have negative 20-day forward return. The claim is precision - base_rate >= 0.10. This is standard: strategy outperforms unconditional base rate by 10%.

But the spec explicitly says "WITHIN the issued subset". Let me parse: "Report the base rate of the predicted class WITHIN the issued subset." Maybe "predicted class" = negative return, "issued subset" = the set of issued calls. Then base rate = precision. Circular.

Unless "predicted class" is not the outcome but the signal class. No.

I'll implement it as: base rate = proportion of negative forward returns in the full opportunity set (all valid decision points). This is the only interpretation that makes the claim non-trivial. And I'll note that "issued subset" might be a misphrasing for "opportunity set" or "universe".

But the output format says "BASE_RATE=<base rate of the predicted class WITHIN the issued subset>". I must output a number. Let me compute both and see.

Actually, wait. "A precision at or near that base rate is unskilled classification, not an edge." If base rate is the unconditional probability of negative return in the opportunity set, and precision is the conditional probability given the signal, then precision near base rate means no edge. That makes perfect sense.

And "issued subset" might mean "the subset of data where we issue predictions (i.e., the test set)" as opposed to the training set. But we don't have train/test split except sealed era.

I'll compute base rate as the fraction of opportunities (valid decision points) that have negative forward return. This is the unconditional base rate. Precision is conditional on signal. The difference is the edge.

But the phrase "WITHIN the issued subset" is problematic. Let me consider: maybe "issued subset" refers to the sealed era? No.

Another interpretation: In the issued subset (the calls we made), the predicted class is DOWN (100%). The base rate of the predicted class (DOWN) is 1. But the base rate of the *actual* class (negative return) within the issued subset is the precision. The phrase "base rate of the predicted class" is wrong; it should be "base rate of the target class".

I think it's a terminology slip. "Predicted class" should be "target class" or "positive class". The base rate of the positive class (negative return) within the issued subset is the precision. But then precision - base_rate = 0.

Unless... the strategy issues a call, but the call is not a class prediction, it's a trade signal. The "predicted class" might be something else.

I'll go with the standard finance interpretation: base rate = unconditional probability of negative 20-day return in the universe/opportunity set. Precision = conditional probability given signal. Report base rate as that unconditional probability. The "issued subset" might mean "the subset of data from which calls are issued (the opportunity set)".

Let me proceed with that. I'll compute:
- Opportunities: all (symbol, T) that pass the data availability filters (252 prior sessions, news for T-5..T, price >=5, etc.) but before entry/abstain conditions
- Base rate: fraction of opportunities where 20-day forward return < 0
- Issued: subset of opportunities where entry conditions met and not abstained
- Precision: fraction of issued where 20-day forward return < 0
- Claim: precision >= 0.80, abstention rate = 1 - issued/opportunities >= 0.95, precision - base_rate >= 0.10

This makes sense.

Now, design effect for EFFECTIVE_N. "Calls clustered in time are not independent, so the design effect is always greater than 1". Design effect = 1 + (avg_cluster_size - 1) * ICC, but we don't have ICC. Simpler: effective N = issued / design_effect. Design effect can be estimated as issued / distinct_days? No.

Standard way: if you have N observations but they are clustered in G groups (days), with average cluster size m = N/G, and intra-cluster correlation rho, design effect = 1 + (m-1)*rho. Without rho, we can't compute.

But the spec says: "EFFECTIVE_N=<issued count divided by the measured design effect>" and "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1".

So I need to measure design effect. How? Perhaps using the variance of the mean estimator. For a proportion (precision), the design effect for clustered data is the ratio of the actual variance to the variance under simple random sampling.

But we have binary outcomes (hit/miss) clustered by day. Let D be number of distinct days. For each day d, let n_d be calls issued, h_d be hits. Overall precision p = sum(h_d)/sum(n_d).

Under SRS, variance of p = p(1-p)/N.
With clustering, variance = (1/N^2) * sum(n_d^2 * (p_d - p)^2 / (D-1) ) approximately, where p_d = h_d/n_d.

Design effect = actual_var / srs_var.

But this is complex. A simpler approximation: design effect = 1 + (avg_n - 1) * rho, where rho is intraclass correlation.

We can estimate rho from the data: rho = (MSB - MSW) / (MSB + (avg_n - 1)*MSW) for ANOVA, but with binary data.

Given time constraints, maybe the design effect is just issued / distinct_days? But that would make effective_n = distinct_days. The invariant says effective_n < issued, and distinct_days <= issued. If design effect = issued / distinct_days, then effective_n = distinct_days. That satisfies effective_n < issued (unless all calls on distinct days, but then distinct_days = issued, effective_n = issued, violating strict inequality). The invariant says "EFFECTIVE_N must be strictly less than ISSUED". So if all calls on distinct days, design effect = 1, effective_n = issued, violation. But the spec says "design effect is always greater than 1". So they assume clustering always exists.

But what if by chance all calls are on different days? Then distinct_days = issued, and if we set design effect = 1, effective_n = issued, violation. So we must have design effect > 1 always.

Perhaps design effect = max(1.01, issued / distinct_days)? Or compute actual design effect from outcome clustering.

Let me compute the design effect properly for a proportion with clusters (days).

Let there be D days (clusters). For day d, n_d calls, h_d hits, p_d = h_d/n_d.
Overall N = sum n_d, H = sum h_d, p = H/N.

Between-cluster variance: B = sum n_d (p_d - p)^2 / (D-1)
Within-cluster variance: W = sum n_d p_d (1-p_d) / (N-D)

Intraclass correlation: rho = (B - W) / (B + (n0 - 1)W) where n0 = (N - sum n_d^2/N)/(D-1) (average cluster size adjusted)

Design effect = 1 + (n0 - 1) * rho

This is getting very involved for a script that must run in 10 minutes.

Given the constraints, perhaps the intended design effect is simply the ratio of issued to distinct_days, but with a minimum of 1.0001 to ensure effective_n < issued. But the spec says "measured design effect", implying computation from data.

Another simple measure: the design effect for the mean of a clustered sample is approximately 1 + (cv^2) * (avg_n - 1) * rho, but no.

I'll compute the design effect using the formula for clustered binary data: deff = 1 + (n_bar - 1) * rho, where rho is estimated as the intraclass correlation coefficient (ICC) for the binary outcome.

ICC for binary data can be estimated via ANOVA method or Fleiss-Cuzick.

But given the complexity, and that the script must be self-contained and run fast, I'll use a simpler proxy: the design effect is the ratio of the variance of the daily precision to the variance under independence.

Daily precision p_d = h_d / n_d.
Overall precision p = sum h_d / sum n_d.

If independent, variance of p_d around p would be p(1-p)/n_d.
Actual variance: sum n_d (p_d - p)^2 / sum n_d (weighted) or unweighted.

Design effect = (actual weighted variance) / (expected weighted variance under independence).

Expected weighted variance = sum n_d * p(1-p)/n_d / D = p(1-p) * D / D = p(1-p)? No.

Let's do: the estimator p = sum h_d / sum n_d = sum (n_d/N) p_d.
Variance of p = sum (n_d/N)^2 Var(p_d) + covariances.
Under independence, Var(p_d) = p_d(1-p_d)/n_d ≈ p(1-p)/n_d.
So Var_ind(p) = sum (n_d/N)^2 * p(1-p)/n_d = p(1-p)/N * sum n_d/N = p(1-p)/N.

Actual variance can be estimated by the between-cluster variance: Var_actual(p) ≈ (1/(D-1)) sum (n_d/N)^2 (p_d - p)^2 * D? Not quite.

The standard design effect estimator for a ratio estimator in cluster sampling is complex.

Given the time, I'll use: design_effect = (issued / distinct_days) * (1 + variance_of_daily_hit_rates / (p*(1-p))) or something.

Actually, a common simple formula: if you have clusters of size m, deff = 1 + (m-1)*rho. Here m = issued / distinct_days (average calls per day). Rho = correlation between two calls on the same day.

We can estimate rho as the intraclass correlation: rho = (var_between - var_within) / (var_between + (m-1)*var_within)