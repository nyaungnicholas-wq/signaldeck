Let me analyze the task carefully. I need to write a Python script that tests a specific hypothesis using the database schema provided. The hypothesis must be in exactly 6 labeled lines: MECHANISM, HORIZON, UNIVERSE, ENTRY, ABSTAIN, CLAIM.

First, I need to understand what data is available and what hasn't been tested yet (based on the killed hypotheses list). Let me think about a novel hypothesis that uses the available data.

Available long-span data:
- Daily bars: 2018-07 to now, 1,777 symbols
- Insider transactions: 2008-03 to 2026-07, 609 symbols (both trade and disclosure dates)
- News sentiment: 2012-04 to now, ~700 symbols
- 13F institutional holdings: quarterly, long span
- Fundamentals: key/value (EPS, Revenues, SharesOutstanding, public float)
- FRED macro series: long span
- StockTwits: need to check span

Killed hypotheses cover many combinations. Let me think of something novel.

Looking at the killed list, many involve:
- Insider purchases/sales with various conditions
- 13F institutional ownership changes
- News sentiment extremes
- StockTwits sentiment
- Public float changes
- Yield curve/macro indicators
- Volume/price patterns

What hasn't been explicitly tested? Let me think...

One angle: **Insider buying during periods of high short interest** - but short volume only 10 weeks (2026-05-20 to 2026-07-31), too short.

Another: **Institutional accumulation (13F) in stocks with declining analyst coverage** - no analyst data.

**Insider buying when the stock is heavily shorted by institutions** - 13F is long only.

**News sentiment momentum (rate of change) + price momentum divergence** - not explicitly tested.

**Insider buying in stocks with high idiosyncratic volatility** - not tested.

**Insider buying in stocks with low institutional ownership (retail-dominated)** - #3/KILLED covers low institutional ownership but with macro.

**Insider buying in stocks with HIGH institutional ownership** - not explicitly tested.

**Multiple insiders buying on the SAME DAY (trade date)** - #60/KILLED covers this.

**Insider buying relative to their historical trading pattern** - #73/KILLED covers track record.

**Insider buying by new insiders (recently appointed)** - not tested.

**Form 144 (intent to sell) + actual insider selling** - #22/KILLED covers Form 144.

**8-K filings** - only 2026-02 onward, too short.

**Short volume** - only 10 weeks, too short for 21-day horizon.

**StockTwits volume as attention proxy** - not explicitly tested.

**News volume as attention proxy** - #1/KILLED covers low news coverage.

**Institutional ownership concentration changes (Herfindahl index)** - not explicitly tested.

**New 13F filers entering a stock** - not explicitly tested.

**Institutions buying while insiders selling** - #52/KILLED is opposite (insiders buying, institutions selling). #2/KILLED is both selling. #38/KILLED is institutional increase + insider... (cut off). Let me check if institutional buying + insider selling was tested.

Looking at #38: "When a company's 13F filing reveals a large increase in institutional ownership and an ins..." - probably insider buying.

#52: "Insiders buying while institutions sell reveals a conviction divergence" - killed.

So institutional buying + insider selling might be novel. But insider selling is noisy (diversification).

**Insider buying in stocks with high and rising short interest** - no long-term short interest.

**Insider buying when options put/call ratio is high** - no options data.

**The interaction between insider trade size and disclosure delay** - heavily tested (#17, #19, #24, #42, #44, #52).

**Insider buying in stocks with low StockTwits attention** - not tested.

**Insider buying in stocks with high StockTwits bearishness** - #4/KILLED and #5/KILLED cover this.

**News sentiment extreme + low volume + insider buying** - #4, #5, #27, #28 killed.

**Positive news sentiment + high volume + insider selling** - #2, #8, #13, #16, #37 killed.

**Fundamental valuation + insider buying + institutional accumulation** - many killed.

**Insider buying in stocks with declining share count (buybacks) + rising EPS** - #59, #65 killed.

**Institutional ownership turnover (churn in holder list)** - not tested.

**13F new positions vs additions to existing positions** - #11/KILLED covers fast-filing new positions.

**Institutional ownership concentration changes** - not tested.

Let me think of something using **news sentiment volatility** (not just level) - not explicitly tested.

Or **insider buying when news sentiment is negative but IMPROVING** - #7, #10, #13, #18, #42, #46, #47, #58 killed.

What about **news sentiment momentum (rate of change) + price momentum divergence**? Not explicitly.

What about **insider buying in stocks with high idiosyncratic volatility**? Not tested.

What about **insider buying in stocks with low idiosyncratic volatility**? #8/KILLED covers low-vol momentum.

Let me think about **macro: term spread + insider buying in rate-sensitive sectors** - #29, #31, #51 killed.

**Credit spread narrowing + insider buying in high-yield issuers** - #13, #23, #28, #34 killed.

**VIX regime + insider buying** - #4, #12, #20 killed.

**Unemployment claims + insider buying** - #10, #19 killed.

**ISM manufacturing + insider buying in cyclicals** - #5, #21 killed.

**Financial stress index** - #59 killed.

**Dollar index / FX** - no data.

**Commodity prices** - no data.

**News sentiment by topic/sector** - not in data.

**StockTwits sentiment vs news sentiment at different horizons** - #18, #26 killed.

**Insider buying in low-news-coverage stocks** - #1/KILLED covers low news coverage.

**Insider buying in stocks with high retail ownership (low institutional)** - related to #3/KILLED.

**Insider buying in stocks with HIGH institutional ownership** - not explicitly tested.

Wait, let me check #3: "Low institutional ownership combined with a steepening yield curve and positive news senti..." - killed. But that's low institutional + macro + news.

What about **high institutional ownership + insider buying** without macro? Not explicitly.

But institutional ownership from 13F is quarterly with 45-day lag. Need to lag it >=45 days.

Let me think of a hypothesis that uses:
- Daily bars (long history)
- Insider transactions (long history, both trade and disclosure dates)
- News sentiment (long history)
- 13F quarterly (long history, with 45-day lag)
- Fundamentals (long history)
- FRED macro (long history)

A novel combination: **Institutional accumulation (13F ownership increase) in stocks with persistently negative news sentiment WHERE insiders are ALSO buying** - triple confirmation. But #47/KILLED: "Clustered open-market insider purchases disclosed after a large drawdown and during ext..." and #43/KILLED: "A quarterly increase in institutional ownership (per 13F) combined with sustained extreme..." and #58/KILLED: "Insiders buying during sustained negative news sentiment while quarterly revenues grow sig..." - close but not exactly this triple.

What about: **Institutional accumulation (13F) + insider buying + positive fundamental momentum (EPS/revenue acceleration)**? #58/KILLED has insider buying + negative news + revenue growth. #69/KILLED has revenue growth. #70/KILLED has margin expansion. #53/KILLED has revenue acceleration. #3/KILLED has EPS growth. #12/KILLED has fundamental improvements. #40/KILLED has revenue growth.

What about: **Insider buying in stocks where institutional ownership is HIGH but DECLINING** (smart money leaving, but insiders buying)? #52/KILLED is opposite (insiders buying, institutions selling). Wait, #52 says "Insiders buying while institutions sell reveals a conviction divergence". That's exactly this! Killed.

What about: **Insider SELLING in stocks where institutional ownership is HIGH and INCREASING** (institutions buying, insiders selling)? This is the reverse divergence. Not explicitly killed. #2/KILLED is insider sales + institutional decrease (both selling). #38/KILLED is institutional increase + insider... (probably buying). So institutional increase + insider selling might be novel.

Mechanism: Institutions have research capacity and long horizons; insiders sell for diversification/liquidity not just negative views. When institutions aggressively accumulate while insiders sell, it may indicate institutions see value insiders are ignoring or insiders are merely diversifying.

But insider selling is noisy. The mechanism is weaker.

What about: **Insider buying in stocks with high and rising short interest (proxied by... nothing long-term)**.

Given the constraints, I need something that:
1. Uses long-span data
2. Has a clear economic mechanism
3. Hasn't been explicitly killed
4. Can be precisely defined with ENTRY/ABSTAIN conditions
5. Targets a precision band

Let me consider: **Institutional accumulation (13F ownership increase) in stocks with persistently negative news sentiment WHERE insiders are ALSO buying** - but this might be too similar to killed ones.

Another idea: **Insider buying when the stock's correlation with the market has structurally declined** - #68/KILLED: "Insiders purchase when their stock's correlation with the market has structurally declined" - killed.

What about **insider buying in stocks with high short interest relative to float (using short volume as proxy, but only 10 weeks)** - can't meet observation floor.

What about **news sentiment dispersion** (if multiple sources) - not in data.

What about **the "quiet period" before earnings** - no earnings dates.

What about **insider buying during blackout periods** - not tracked.

What about **Form 4 filing clustering in time** - #1/KILLED covers cluster in disclosure window.

What about **insider buying in stocks with high retail ownership (low institutional)** - related to #3/KILLED.

What about **insider buying in stocks with HIGH institutional ownership** - not explicitly tested without macro.

Let me check the killed list again for "high institutional ownership + insider buying"...

#13/KILLED: "A disclosed jump in aggregate 13F institutional ownership is a slow-diffusing positive sig..." - killed.
#31/KILLED: "Quarterly 13F institutional ownership increases reveal informed accumulation that diffuses..." - killed.
#32/KILLED: "A quarterly 13F institutional ownership increase combined with a concurrent open-market in..." - killed (this is institutional increase + insider buying).
#38/KILLED: "When a company's 13F filing reveals a large increase in institutional ownership and an ins..." - killed (institutional increase + insider...).

So institutional increase + insider buying has been tested multiple ways (#32, #38).

What about **institutional DECREASE + insider buying**? #52/KILLED: "Insiders buying while institutions sell reveals a conviction divergence" - killed.

What about **institutional increase + insider SELLING**? Not explicitly. #2 is both selling. #38 is institutional increase + insider... (cut off, but likely buying).

Let me assume #38 is institutional increase + insider buying. Then institutional increase + insider selling might be novel.

But the mechanism is weak because insider selling is often for diversification.

What about **multiple insiders buying on the SAME TRADE DATE (not disclosure date) in stocks with low institutional ownership**? #60/KILLED: "Multiple insiders executing open-market purchases on the same trade date reveals coordinat..." - killed.

What about **insider buying by CEOs/CFOs specifically (not just any insider) after large drawdowns**? #19/KILLED: "CEO/CFO open-market purchases disclosed after a 20%+ drawdown..." - killed.

What about **insider buying by outside directors**? #53/KILLED: "Outside directors purchase only when they perceive significant undervaluation..." - killed.

What about **insider buying in stocks with high short interest (from 13F? No, 13F is long only)**.

Let me think about **the interaction between news sentiment and StockTwits sentiment at different horizons** - #18, #26 killed.

What about **insider buying when news sentiment is negative but StockTwits is bullish** (retail optimism vs institutional pessimism)? #18/KILLED: "Bad news diffuses slowly; when the news flow is extremely negative but StockTwits retail s..." - killed.

What about **insider buying when news sentiment is positive but StockTwits is bearish**? Not explicitly.

What about **StockTwits sentiment extremes + news sentiment divergence as a contrarian indicator** - #18, #26 killed.

What about **news volume (number of headlines) as attention proxy + insider buying**? #1/KILLED: "Bad news travels slowly (Hong, Lim & Stein): stocks with little news coverage attract less..." - killed. #39/KILLED: "A stock that reappears in the news after a long drought..." - killed.

What about **insider buying in stocks with low news coverage**? #1/KILLED covers low news coverage generally.

Let me think about **fundamental momentum + price momentum divergence**.

#3/KILLED: "Fundamental information diffuses slowly — when trailing four-quarter EPS growth is strongl..." - killed.
#69/KILLED: "Consistent quarterly revenue growth signals fundamental strength..." - killed.

What about **accelerating fundamentals + decelerating price** (or vice versa)? Not explicitly.

What about **earnings yield (E/P) > 10Y yield + insider buying**? #42/KILLED: "A large spread between trailing earnings yield and the 10-year Treasury yield is a slow-di..." - killed. #19/KILLED: "A stock with an earnings yield more than 200 basis points above the 10-year Treasury yield..." - killed. #42/KILLED again: "A stock's earnings yield exceeding the 10-year Treasury yield by 200+ bps signals a valuat..." - killed.

What about **buyback yield (float reduction) + insider buying**? #59/KILLED: "When a company executes share buybacks (SharesOutstanding falls QoQ) and insiders simultan..." - killed. #65/KILLED: "Concurrent institutional accumulation (13F ownership increase >5% QoQ) and corporate buyba..." - killed. #71/KILLED: "A company that has reduced its public float for three or more consecutive quarters..." - killed.

What about **insider buying in stocks with high short interest (from short volume, but only 10 weeks)**.

Given the difficulty finding a completely novel hypothesis, let me think of one that combines elements in a way not explicitly killed.

How about: **Insider open-market purchases (code P) disclosed during periods of extreme negative news sentiment (bottom decile) in stocks where institutional ownership (13F, lagged 45+ days) has increased for two consecutive quarters, predicting positive 21-day forward returns.**

Check killed list:
- #46/KILLED: "Insider open-market purchases disclosed during extreme negative news signal insiders view..." - killed (but no institutional condition)
- #47/KILLED: "Clustered open-market insider purchases disclosed after a large drawdown and during ext..." - killed (clustered + drawdown + extreme negative news)
- #43/KILLED: "A quarterly increase in institutional ownership (per 13F) combined with sustained extreme..." - killed (institutional increase + extreme negative news)
- #58/KILLED: "Insiders buying during sustained negative news sentiment while quarterly revenues grow sig..." - killed (insider buying + negative news + revenue growth)
- #32/KILLED: "A quarterly 13F institutional ownership increase combined with a concurrent open-market in..." - killed (institutional increase + insider buying)
- #38/KILLED: institutional increase + insider...

So this triple combination (insider buying + extreme negative news + institutional increase for 2 quarters) might be novel. But #43 is institutional increase + sustained extreme negative news. #46 is insider buying + extreme negative news. #32 is institutional increase + insider buying. The triple might not be explicitly tested.

But the mechanism: "Institutions accumulate despite negative news, insiders confirm with purchases, both see value others miss." This is plausible.

However, I need to check if "two consecutive quarters of institutional ownership increase" was tested. #71/KILLED: "A company that has reduced its public float for three or more consecutive quarters..." - killed (float reduction). #15/KILLED: "Shrinking public float combined with improving fundamentals..." - killed. #26/KILLED: "A sustained quarterly decline in public float combined with institutional accumulation..." - killed. #27/KILLED: "A shrinking public float (two consecutive quarterly declines...)" - killed.

What about "two consecutive quarters of institutional ownership increase"? #31/KILLED: "Quarterly 13F institutional ownership increases reveal informed accumulation that diffuses..." - killed (plural "increases" but not necessarily consecutive). #13/KILLED: "A disclosed jump in aggregate 13F institutional ownership..." - killed (single jump).

Two consecutive quarters might be novel.

But let me think of something simpler and more clearly novel.

How about: **Insider open-market purchases (code P) by CEOs/CFOs only, disclosed when the stock's 21-day realized volatility is in the bottom quartile (low volatility regime), predicting positive 21-day forward returns.**

Check killed:
- #8/KILLED: "Low-volatility momentum persistence..." - killed (low vol momentum, not insider)
- #12/KILLED: "Insiders who buy on the open market during periods of elevated market fear (high VIX) trad..." - killed (high VIX, not low vol)
- #20/KILLED: "Insider open-market purchases disclosed during periods of high market volatility and risin..." - killed (high vol)
- #19/KILLED: "CEO/CFO open-market purchases disclosed after a 20%+ drawdown..." - killed (drawdown, not low vol)
- #53/KILLED: "Outside directors purchase only when they perceive significant undervaluation..." - killed (outside directors)
- #73/KILLED: "Insiders with a verified historical track record..." - killed (track record)

CEO/CFO + low volatility regime not explicitly tested. Mechanism: In low volatility regimes, insider purchases are more deliberate and less driven by panic/euphoria, signaling genuine conviction.

But need to define "low volatility regime" precisely. 21-day realized volatility bottom quartile relative to stock's own history? Or cross-sectional?

Let me think of another: **Insider open-market purchases (code P) disclosed when the stock's price is within 5% of its 52-week low AND news sentiment is in the bottom decile, predicting positive 21-day forward returns.**

Check killed:
- #1/KILLED: "A stock making a 252-session low on a day the broad market is strongly up..." - killed (52-week low + market up)
- #46/KILLED: "Insider open-market purchases disclosed during extreme negative news signal insiders view..." - killed (extreme negative news)
- #47/KILLED: "Clustered open-market insider purchases disclosed after a large drawdown and during ext..." - killed (drawdown + extreme negative news)
- #18/KILLED: "Insider open-market purchases disclosed after a sustained period of negative news sentimen..." - killed (sustained negative news)
- #10/KILLED: "Insider open-market purchases during a period of sustained improvement in news sentiment s..." - killed (improving news)
- #13/KILLED: "Insider open-market purchases disclosed after a period of sustained negative news sentimen..." - killed (sustained negative news)
- #58/KILLED: "Insiders buying during sustained negative news sentiment while quarterly revenues grow sig..." - killed (negative news + revenue growth)

52-week low + extreme negative news + insider buying - #47 is close (drawdown + extreme negative news + clustered insider buying). But "52-week low" vs "large drawdown" and "clustered" vs single insider.

#1 is 52-week low + market up (no insider).

So 52-week low + extreme negative news + insider buying (not necessarily clustered) might be novel.

But the mechanism: "Insiders buying at 52-week lows during extreme pessimism signal capitulation is over." Plausible.

Let me check if "52-week low" is explicitly tested with insider buying. #1 is 52-week low but with market up, no insider. #47 is large drawdown (could be 52-week low) + extreme negative news + clustered insider buying. So single insider at 52-week low + extreme negative news might be a subset not explicitly tested.

But the killed list says "Clustered open-market insider purchases disclosed after a large drawdown and during ext..." - "clustered" is key. Single insider might be different.

However, the hypothesis should be precise. Let me define:

MECHANISM: Insiders buying at 52-week lows during extreme negative news sentiment signal that informed participants view the pessimism as overdone, leading to mean reversion.
HORIZON: 21 calendar days
UNIVERSE: All active common stocks with daily bars, insider transaction history, and news sentiment coverage, excluding the most recent 20% of time as a sealed era.
ENTRY: An open-market insider purchase (code P) is disclosed (filed_ts) when the stock's close is within 5% of its 52-week low (computed from daily bars up to filed_ts) AND the 5-day average news sentiment score (from sentiment_features, lagged 1 day) is in the bottom decile of its trailing 252-day distribution.
ABSTAIN: No call if fewer than 3 such signals occur in a rolling 63-day window (to avoid isolated noise), or if the stock has a delisted_at date within the horizon.
CLAIM: Precision in [0.58, 0.68] for positive forward returns (up=1) in prediction_outcomes at 21-day horizon.

Wait, need to check prediction_outcomes: it has horizon, ts, up, fwd_return. Horizon is probably in days? The schema says "horizon" but not units. Likely days.

Also need to check: prediction_outcomes has symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch. So we can get labels for 21-day horizon.

But the ENTRY condition uses filed_ts (disclosure date) as decision timestamp. Need to ensure as-of discipline: all inputs computable at filed_ts.

- 52-week low: computed from bars up to filed_ts (ts <= filed_ts). Good.
- 5-day average news sentiment: from sentiment_features, day is 'YYYY-MM-DD'. Need to convert filed_ts (unix epoch) to date, then get sentiment_features for prior 5 days (lagged 1 day to avoid lookahead). Good.
- Bottom decile of trailing 252-day distribution: computed from sentiment_features up to filed_ts - 1 day. Good.
- Insider purchase code P: from insider_trades, filed_ts is knowable. Good.
- Delisted_at: from symbols, knowable at filed_ts. Good.

UNIVERSE: "All active common stocks with daily bars, insider transaction history, and news sentiment coverage" - need to define without hindsight. At each decision point, the universe is symbols that have data up to that point.

But the script needs to test this hypothesis. Let me think about the implementation.

The script must:
1. Connect to data/signaldeck.db read-only
2. Define the hypothesis precisely
3. Compute calls (ENTRY conditions met)
4. Get labels from prediction_outcomes for 21-day horizon
5. Hold out most recent 20% as sealed era
6. Report metrics

But wait - the task says "Write a SELF-CONTAINED Python 3 script that tests exactly this hypothesis" and the hypothesis must be in exactly 6 labeled lines. But the user didn't provide a specific hypothesis - they said "Let me analyze the task carefully. I need to propose ONE new hypothesis..."

So I need to PROPOSE a hypothesis (in 6 lines) AND write the script to test it.

The hypothesis must be novel (not in killed list), testable with available data, and meet all constraints.

Let me propose a hypothesis that I believe is novel:

**Hypothesis: Insider open-market purchases (code P) by CEOs/CFOs disclosed when the stock's 21-day realized volatility is in the bottom quartile of its trailing 252-day distribution AND news sentiment is in the bottom decile, predicting positive 21-day forward returns.**

Check killed list:
- #8/KILLED: Low-volatility momentum persistence (no insider)
- #12/KILLED: Insiders buying during high VIX (high vol, not low vol)
- #20/KILLED: Insider purchases during high market volatility (high vol)
- #19/KILLED: CEO/CFO purchases after 20%+ drawdown (drawdown, not low vol)
- #53/KILLED: Outside directors (not CEO/CFO)
- #73/KILLED: Insiders with track record (not role-specific)
- #46/KILLED: Insider purchases during extreme negative news (no low vol condition)
- #47/KILLED: Clustered insider purchases after drawdown during extreme negative news (clustered + drawdown, not low vol)
- #58/KILLED: Insiders buying during negative news while revenues grow (fundamentals, not low vol)

CEO/CFO + low volatility regime + extreme negative news - this triple combination doesn't appear to be explicitly killed.

Mechanism: In low volatility regimes, CEO/CFO purchases during extreme pessimism represent high-conviction value signals rather than reactive trades, as low vol indicates stable expectations making insider action more deliberate.

Let me formalize:

MECHANISM: CEO/CFO open-market purchases during low-volatility regimes with extreme negative news sentiment signal high-conviction value recognition because low volatility reflects stable expectations, making insider action more deliberate than reactive.
HORIZON: 21 calendar days
UNIVERSE: Active common stocks (market='stocks', active=1, delisted_at is NULL or > decision date) with at least 252 days of daily bar history and news sentiment coverage at the decision timestamp, excluding the most recent 20% of the sample period as a sealed era.
ENTRY: An open-market purchase (code='P') by an insider with title containing 'CEO' or 'CFO' (case-insensitive) is disclosed at filed_ts when: (1) the stock's 21-day realized volatility (stddev of daily log returns from bars up to filed_ts) is in the bottom quartile of its trailing 252-day distribution, AND (2) the 5-day average news sentiment mean_score (from sentiment_features, days filed_ts-6 to filed_ts-2 inclusive) is in the bottom decile of its trailing 252-day distribution.
ABSTAIN: No call if the stock has delisted_at within 21 days of filed_ts, or if fewer than 2 such signals exist in the prior 63 days (to ensure sufficient regime context), or if the 21-day forward return label is unavailable in prediction_outcomes.
CLAIM: Precision in [0.55, 0.65] for positive forward returns (up=1) at 21-day horizon in prediction_outcomes.

Wait, "fewer than 2 such signals exist in the prior 63 days" - this is to avoid isolated noise? But the hypothesis is about each signal. Maybe better: "No call if the stock has delisted_at within 21 days of filed_ts, or if the 21-day forward return label is unavailable in prediction_outcomes." Keep ABSTAIN minimal.

But the requirement says "ABSTAIN: exact condition for no call (stated before touching data)". So I need to state it clearly.

Let me refine:

MECHANISM: CEO/CFO open-market purchases during low-volatility regimes with extreme negative news sentiment signal high-conviction value recognition because low volatility reflects stable expectations, making insider action more deliberate than reactive.
HORIZON: 21 calendar days
UNIVERSE: Active common stocks (market='stocks', active=1, delisted_at is NULL or > decision date) with at least 252 days of daily bar history and news sentiment coverage at the decision timestamp, excluding the most recent 20% of the sample period as a sealed era.
ENTRY: An open-market purchase (code='P') by an insider with title containing 'CEO' or 'CFO' (case-insensitive) is disclosed at filed_ts when: (1) the stock's 21-day realized volatility (stddev of daily log returns from bars up to filed_ts) is in the bottom quartile of its trailing 252-day distribution, AND (2) the 5-day average news sentiment mean_score (from sentiment_features, days filed_ts-6 to filed_ts-2 inclusive) is in the bottom decile of its trailing 252-day distribution.
ABSTAIN: No call if the stock has delisted_at within 21 days of filed_ts, or if the 21-day forward return label is unavailable in prediction_outcomes for that symbol_id and filed_ts.
CLAIM: Precision in [0.55, 0.65] for positive forward returns (up=1) at 21-day horizon in prediction_outcomes.

Now I need to write the Python script to test this.

The script must:
1. Open database read-only
2. Get all insider trades with code='P' and title like '%CEO%' or '%CFO%'
3. For each, compute decision timestamp = filed_ts
4. Check universe conditions at that timestamp
5. Compute 21-day realized volatility up to filed_ts
6. Compute its percentile in trailing 252-day distribution
7. Compute 5-day avg news sentiment (mean_score from sentiment_features) for days filed_ts-6 to filed_ts-2
8. Compute its percentile in trailing 252-day distribution
9. If both in bottom quartile/decile, issue call
10. Get label from prediction_outcomes for horizon=21 (assuming horizon is in days), symbol_id, ts=filed_ts (or closest?)
11. Hold out most recent 20% of time as sealed era
12. Compute metrics

But prediction_outcomes has ts column - what is ts? Probably the prediction timestamp. We need to match on symbol_id, horizon=21, and ts close to filed_ts.

The schema: prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch). So ts is likely the timestamp when the prediction was made (or label timestamp). We need to find the row where symbol_id matches, horizon=21, and ts is the decision timestamp (filed_ts). But filed_ts is unix epoch, ts might be too. Need to check.

Also, the horizon in prediction_outcomes - is it 21 for 21 days? The schema doesn't specify units. But given the context, likely days.

Now, the sealed era: "Hold out the most recent 20% as a sealed era and report it separately." Most recent 20% of what? Of the sample period. The sample period is the range of decision timestamps (filed_ts) where we have data. So we need to find all potential decision timestamps, sort them, take the most recent 20% as sealed.

But "most recent 20% of the sample" - sample of what? Of the calls issued? Or of the time period? The instruction: "Hold out the most recent 20% as a sealed era and report it separately." And "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it."

So we should consider the time period of the data, split at 80th percentile of time, use first 80% for training/main, last 20% for sealed.

But the hypothesis test is not training - it's just testing. So we compute all calls, then split by time: calls with decision timestamp in the most recent 20% of the time range are sealed.

"most recent 20% of the sample" - sample likely means the time period covered by the data.

Let me define: find min and max filed_ts from insider_trades (or from the calls we generate). The time range is [min_ts, max_ts]. The cutoff is min_ts + 0.8 * (max_ts - min_ts). Calls with filed_ts >= cutoff are sealed.

But the instruction says "Hold out the most recent 20% as a sealed era". So yes, by time.

Now, metrics:
- ISSUED: total calls issued (both main and sealed)
- OPPORTUNITIES: count of decision points considered (how many insider trades we evaluated)
- PRECISION: hits/issued for main era (non-sealed)
- BASE_RATE: base rate of predicted class (up=1) WITHIN the issued subset (for main era)
- DISTINCT_DAYS: distinct UTC days on which a call was issued (among issued calls only)
- EFFECTIVE_N: issued count divided by measured design effect (design effect > 1)
- SEALED_PRECISION: precision on sealed era

Design effect: need to measure clustering. "Calls clustered in time are not independent, so the design effect is always greater than 1". How to measure? One way: compute the variance inflation factor due to clustering. For binary outcomes, design effect = 1 + (avg_cluster_size - 1) * ICC. But we don't have clusters defined.

Simpler: group calls by UTC day. If multiple calls on same day, they're correlated. Design effect ≈ 1 + (avg_calls_per_day - 1) * rho. But rho unknown.

The instruction says "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

We can estimate design effect as: for each day with calls, compute the proportion of hits. Then the variance of the mean is inflated by design effect. A simple estimator: design_effect = 1 + (n_avg - 1) * ICC, but ICC unknown.

Alternative: use the Kish effective sample size formula: n_eff = (sum w_i)^2 / sum(w_i^2) where w_i are weights. If we weight each day equally, and each call on a day gets weight 1/n_day, then sum w_i = number of days, sum w_i^2 = sum(1/n_day). So n_eff = (D)^2 / sum(1/n_d) where D is number of days. This is the effective number of independent days.

But the instruction says "EFFECTIVE_N=<issued count divided by the measured design effect>". So design_effect = ISSUED / EFFECTIVE_N.

If we define clusters as days, and assume calls within a day are perfectly correlated (rho=1), then design_effect = avg_calls_per_day. Then EFFECTIVE_N = ISSUED / avg_calls_per_day = DISTINCT_DAYS.

But the instruction says "EFFECTIVE_N must be strictly less than ISSUED" and "DISTINCT_DAYS counts days among the ISSUED calls only, never among the opportunities considered. It therefore can never exceed ISSUED."

If design_effect = avg_calls_per_day, then EFFECTIVE_N = DISTINCT_DAYS. But is that the "measured design effect"? The instruction says "measured design effect", implying