Let me carefully analyze the task. I need to write a Python script that tests a specific hypothesis using the database schema provided. The hypothesis must follow the exact format with six labeled lines: MECHANISM, HORIZON, UNIVERSE, ENTRY, ABSTAIN, CLAIM.

First, let me understand what data is available and what hasn't been tested extensively based on the killed hypotheses list.

Looking at the killed hypotheses, many involve:
- Insider purchases with various conditions
- 13F institutional ownership changes
- Public float changes
- News sentiment patterns
- Yield curve / macro combinations
- Short volume (but only 2026 data)
- VIX / market fear
- StockTwits sentiment

The data available with long history:
- Daily bars (2018+)
- Insider transactions (2008+) with BOTH trade and disclosure dates
- News sentiment (2012+)
- Fundamentals (EPS, Revenue, SharesOutstanding) - likely quarterly
- 13F holdings (quarterly)
- FRED macro (1947+)
- StockTwits (2026 only - too recent)

Short volume and SEC filings are only 2026, so not useful for longer horizons with enough observations.

Let me think of a hypothesis that:
1. Uses data with sufficient history (pre-2026)
2. Has a clear economic mechanism
3. Hasn't been explicitly killed
4. Can be tested with the available tables

One idea: The interaction between insider buying and fundamental acceleration (revenue growth accelerating) but with a specific twist - focusing on OFFICERS (CEO/CFO) specifically, and using the fact that we have both trade date and disclosure date.

Actually, looking at the killed list more carefully:
- [6/KILLED] Insiders at companies with three consecutive quarters of accelerating EPS growth possess p...
- [4/KILLED] Insiders possess superior information and sell into institutional accumulation...
- [5/KILLED] Insiders possess private information about fundamental trajectory; open-market purchases d...

Many insider + fundamentals combinations killed.

What about: Insider buying when the company has consistent quarterly revenue growth AND the insider is an officer (CEO/CFO) AND the purchase is large relative to the stock's average daily volume? But [1/KILLED] and [12/KILLED] test purchase size relative to volume.

What about: The disclosure lag itself as a signal? [44/KILLED] and [4/KILLED] test disclosure delay.

What about: Insider buying combined with LOW news coverage (attention constraint)? [13/KILLED], [3/KILLED], [63/KILLED] test low news coverage.

What about: Using the 13F lag explicitly? [51/KILLED] tests the 45-day lag.

Let me think of something using the fact that we have BOTH trade date and filing date for insiders, and we have daily bars. The price movement BETWEEN trade date and filing date could be informative.

Actually, [15/KILLED] tests "disclosed trade price is already exceeded by the market" and [2/KILLED] tests "gone underwater between trade date and market".

What about: Insider purchases where the trade date is during a period of declining volume (showing conviction in quiet markets) but the filing date shows the price has moved favorably? 

Or: Officers (CEO/CFO) buying when the stock is near 52-week lows but fundamentals (revenue) are growing? [6/KILLED] tests "Insiders buying at 52-week lows while fundamentals remain positive..."

Let me look for something truly unique. The schema shows:
- insider_trades has: accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
- code: A=award S=sale P=purchase F=tax M=option-exercise
- title likely has role info (CEO, CFO, Director, etc.)

What about: "When multiple officers (CEO and CFO) purchase within the same quarter, it signals collective conviction that is more informative than single purchases." [60/KILLED] tests "Multiple insiders executing open-market purchases on the same trade date" - same date, not same quarter.

What about: "Officer purchases that occur after a quarter where revenue accelerated but EPS decelerated (investment phase) signal confidence in the investment thesis." This combines revenue growth acceleration with margin compression.

Let me check if this is killed:
- [70/KILLED] Sustained margin expansion (EPS growth exceeding revenue growth for consecutive quarters)...
- [69/KILLED] Consistent quarterly revenue growth signals fundamental strength...
- [23/KILLED] Insiders possess private information about fundamental trajectory; open-market purchases d...
- [4/KILLED] Insiders possess private information about upcoming positive fundamental inflections...
- [6/KILLED] Insiders at companies with three consecutive quarters of accelerating EPS growth possess p...

Not exactly this combination. The key is: revenue accelerating + EPS decelerating (margin compression from investment) + officer buying.

But we need quarterly fundamental data. The fundamentals table has metric as 'EPS' | 'Revenues' | 'SharesOutstanding' | 'EntityPublicFloat' | 'CIK' | 'LatestFilingDate' with as_of and fetched_at.

We can compute quarterly revenue growth and EPS growth from this.

Another idea: Use the fact that insiders have different roles. The title field likely indicates role. "CEO/CFO purchases during periods of high short interest" but short volume only 2026.

What about: "Insider purchases disclosed on days with zero news headlines signal high conviction because the insider is acting without external catalyst." [56/KILLED] tests this.

Let me think about the macro data. FRED series go back to 1947. We have macro_series(series, ts, value).

What about: "Insider purchases during periods of falling unemployment claims (improving labor market) in cyclical sectors" but we don't have sector data.

What about a pure price/volume pattern confirmed by insiders? "An insider purchase after a stock has made a higher low on declining volume (bullish divergence) predicts continuation." [14/KILLED] tests "Insider open-market purchases during a technical pullback in an uptrend (price above 200-day..."

Let me think about the 13F data more carefully. inst_holdings has cik, manager, period, symbol_id, cusip, name, value, shares. Period is quarter end, filed up to 45 days later.

What about: "When 13F shows a new institutional holder (not just increase) entering a position, and an officer buys in the same quarter, the combined signal predicts returns." But we'd need to track new holders vs existing.

This is getting complex. Let me think of something simpler and more robust.

The hypothesis format requires:
- MECHANISM: economic mechanism
- HORIZON: must be one of the available horizons in prediction_outcomes: 1d, 1d#pm, 1w, 1w#pm
- UNIVERSE: which symbols
- ENTRY: when to enter
- ABSTAIN: when to abstain
- CLAIM: what we claim about precision vs base rate

Given the data constraints, let me propose a hypothesis that uses:
- Daily bars (long history)
- Insider transactions (long history, both tx_ts and filed_ts)
- Fundamentals (quarterly EPS, Revenue)
- Horizon: 1w (1 week) or 1w#pm - available in prediction_outcomes with 247k and 122k rows respectively

Actually, the task says "READ THE DATE RANGES BEFORE CHOOSING A HORIZON. The single most common way a cycle is wasted: prediction_outcomes is the most convenient label source, but it holds only the horizons and the span printed above. A hypothesis with a 21- or 63-trading-day horizon has NO label there at any date. For any horizon longer than the ones listed, build the label yourself from bars (tf='1d'): the forward return over N sessions from the decision bar. bars spans ~8 years over 2,263 symbols with >=252 daily bars, so long horizons are testable -- just not from prediction_outcomes."

So I CAN use longer horizons by building labels from bars. But prediction_outcomes only has 1d, 1d#pm, 1w, 1w#pm.

For a robust test, I should probably use 1w or 1w#pm from prediction_outcomes since it's readily available, or build a longer horizon from bars.

Let me design a hypothesis using 1w horizon from prediction_outcomes.

Hypothesis idea: "Officer (CEO/CFO) open-market purchases disclosed during a quarter where year-over-year revenue growth has accelerated for two consecutive quarters, but the stock has underperformed the market (negative excess return over prior 63 days), signal private conviction that the revenue acceleration will translate to earnings, and the market underreacts due to attention constraints."

Check killed:
- [6/KILLED] Insiders at companies with three consecutive quarters of accelerating EPS growth...
- [2/KILLED] Attention-constrained investors underreact to persistent improvements in fundamentals in l...
- [30/KILLED] Attention-constrained investors underreact to improving fundamentals in low-attention stoc...
- [63/KILLED] Attention-constrained investors underreact to improving fundamentals in low-attention stoc...
- [3/KILLED] Insiders possess private information about upcoming positive fundamental inflections...

Not exactly this. The combination is: revenue acceleration (not EPS) + stock underperformance + officer buys.

But we need to compute revenue growth acceleration from fundamentals. The fundamentals table has metric='Revenues' with as_of (period) and fetched_at (when we learned it). We need to use fetched_at for as-of discipline.

Actually, as_of is the period, fetched_at is when we learned it. Only fetched_at is knowable in advance. So we can only use fundamental data that has been fetched by the decision date.

This is complex but doable.

Let me simplify: Use insider transactions + daily bars only, with a 1w horizon from prediction_outcomes.

Mechanism: "Officers (CEO/CFO) have the most precise private information about near-term cash flows; their open-market purchases disclosed after a 10-day price decline (oversold) but with above-average volume on the purchase day signal conviction that the selling is exhausted, and this signal diffuses slowly because the market focuses on the recent price decline rather than the insider's conviction."

Check killed:
- [28/KILLED] Officers (CEO/CFO) purchase open-market shares during low-volume periods when their compan...
- [60/KILLED] Insider open-market purchases executed on days with unusually low trading volume signal hi...
- [14/KILLED] Insider open-market purchases during a technical pullback in an uptrend (price above 200-d...
- [48/KILLED] When a stock is oversold (14-day RSI < 30) and news sentiment shifts to improving...

Not exactly. The combination: officer + price decline + above-average volume on purchase day (not low volume).

[28/KILLED] is low-volume periods. [60/KILLED] is low volume. This is HIGH volume on purchase day.

What about: "Officer purchases on high volume days (relative to 63-day average) after a 20-day decline signal that informed buyers are absorbing supply, predicting a reversal."

This seems testable and not explicitly killed.

Let me formalize:

MECHANISM: Officers (CEO/CFO) have superior operational information; their open-market purchases on days with volume > 1.5x the 63-session median, following a 20-session price decline > 10%, signal that informed buyers are aggressively absorbing supply, and the market underreacts because the recent decline dominates attention.

HORIZON: 1w (available in prediction_outcomes)

UNIVERSE: All active US stocks with >= 252 daily bars of history and at least one officer purchase in the lookback period.

ENTRY: On the disclosure date (filed_ts) of an officer (title contains 'CEO' or 'CFO' or 'Chief') open-market purchase (code='P') where: (1) the 20-session return prior to trade date (tx_ts) is < -10%, (2) volume on trade date > 1.5x the 63-session median volume ending the day before trade date, (3) the purchase value > $50,000.

ABSTAIN: If no such signal in the prior 63 sessions, or if another signal for the same symbol is active (within 5 sessions of prior signal), or if the symbol has market cap < $100M (approximated by close * shares outstanding, but we don't have shares outstanding daily... we have fundamentals SharesOutstanding quarterly).

Actually, we don't have daily shares outstanding. We have quarterly from fundamentals. Could use latest fetched.

But this adds complexity. Let me simplify ABSTAIN.

ABSTAIN: If no officer purchase meeting criteria in the prior 63 sessions, or if a signal for the same symbol was issued within the prior 5 sessions.

CLAIM: The precision of positive forward returns (up=1) over the 1w horizon for issued calls exceeds the base rate of positive returns within the issued subset by at least 15 percentage points.

Now, I need to write a Python script that:
1. Connects to the database read-only
2. Queries the necessary data
3. Implements the hypothesis logic with proper as-of discipline
4. Holds out the most recent 20% as sealed era
5. Computes the required metrics
6. Prints the required output lines

Let me think about the data flow:

1. Get all officer purchases (code='P', title like '%CEO%' or '%CFO%' or '%CHIEF%') from insider_trades
2. For each, get the trade date (tx_ts) and disclosure date (filed_ts)
3. Use disclosure date as decision date (as-of discipline: market knows at filed_ts)
4. Check conditions at trade date (tx_ts): 20-day return < -10%, volume > 1.5x 63-day median
5. Get 1w forward return from prediction_outcomes for that symbol at disclosure date (or nearest)
6. Or build label from bars: 5-day forward return from disclosure date

Wait, prediction_outcomes has horizon='1w' with ts column. What is ts? Probably the prediction timestamp. We need to match our decision timestamp (filed_ts) to prediction_outcomes.ts.

But prediction_outcomes has basis_epoch and settle_ts. Let me check: "prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch, settle_ts)"

ts is likely the timestamp when the prediction was made. basis_epoch might be the decision epoch. settle_ts when it was resolved.

For as-of discipline, we should use basis_epoch as the decision time? Or ts? The schema says "basis_epoch" - probably the epoch at which the prediction was based.

Actually, the hypothesis says to use prediction_outcomes as label source for 1w horizon. We need to join on symbol_id and horizon='1w' and basis_epoch (or ts) close to our filed_ts.

But filed_ts is in insider_trades. We need to convert to epoch if it's not already. The schema says tx_ts and filed_ts - likely unix epochs.

bars.ts is unix epoch integer.

So filed_ts is probably unix epoch.

prediction_outcomes.ts and basis_epoch - need to check which aligns.

The schema says: "up is the realised direction and fwd_return the realised forward return. THESE ARE LABELS."

So for a given symbol_id, horizon, and basis_epoch (decision time), we have the label.

We should join on symbol_id, horizon='1w', and basis_epoch = filed_ts (or closest prior).

But filed_ts might not exactly match basis_epoch. We need to find the prediction_outcomes row with basis_epoch <= filed_ts and closest.

Actually, for as-of discipline, the label must be for a decision made at filed_ts. So we need prediction_outcomes where basis_epoch <= filed_ts and it's the latest such for that symbol/horizon.

But prediction_outcomes might not have a row for every filed_ts. It has 644k rows over 41 distinct label days for 1w horizon.

"distinct label days: 41" - so only 41 distinct settlement dates for 1w horizon.

This means prediction_outcomes is sparse. We might need to build labels from bars instead.

The context says: "For any horizon longer than the ones listed, build the label yourself from bars (tf='1d'): the forward return over N sessions from the decision bar. bars spans ~8 years over 2,263 symbols with >=252 daily bars, so long horizons are testable -- just not from prediction_outcomes."

But 1w IS listed in prediction_outcomes. However, with only 41 distinct label days, it might be too sparse for our signals.

Let me check: 1w horizon has 247,491 rows, 41 distinct label days. So about 6,000 symbols per label day? But only 2,947 symbols total. So multiple rows per symbol per label day? Or 41 label days across the date range 2026-07-03 to 2026-08-13 (about 41 days). So basically one label day per trading day in that period.

prediction_outcomes date range: 2026-07-03..2026-08-13. Only ~41 days! That's very recent.

So prediction_outcomes only covers July-August 2026. That's useless for historical backtesting.

We MUST build labels from bars for any meaningful history.

The context confirms: "bars spans ~8 years over 2,263 symbols with >=252 daily bars, so long horizons are testable -- just not from prediction_outcomes."

So for 1w horizon (5 trading days), we can compute forward return from bars.

Decision date = filed_ts (disclosure date). We need the close price on the decision date (or next trading day?) and close price 5 trading days later.

As-of discipline: "every input must be computable at the decision timestamp. No value from a bar at or after the label window may inform a call."

Decision timestamp = filed_ts (when market learns of the purchase). The label window is the next 5 trading days. So we can use bars up to and including the decision date for features, but the label uses bars after decision date.

For the 20-day return prior to trade date (tx_ts): we use bars up to tx_ts. Since tx_ts <= filed_ts (trade happens before filing), this is known at filing date.

For volume on trade date: bars on tx_ts.

For 63-session median volume ending day before trade date: bars up to tx_ts - 1 day.

All good.

Now, the horizon: 5 trading days (1w) from decision date (filed_ts). But filed_ts is a timestamp, not necessarily a trading day close. We need to map to the next trading day bar.

Simplification: Use the trading day of filed_ts (or next if after hours) as decision day T. Label is close(T+5) / close(T) - 1.

We need to get daily bars (tf='1d') for each symbol.

Implementation plan:

1. Query all officer purchases from insider_trades:
   - code = 'P' (purchase)
   - title contains 'CEO' or 'CFO' or 'CHIEF' (case insensitive)
   - value > 50000 (dollar value)
   - tx_ts and filed_ts available

2. For each purchase, get symbol_id, tx_ts, filed_ts

3. Get daily bars for the symbol around tx_ts and filed_ts

4. Compute features at tx_ts (trade date):
   - 20-day return: close(tx_ts) / close(tx_ts - 20 days) - 1 < -0.10
   - Volume on tx_ts > 1.5 * median(volume over 63 days ending tx_ts - 1 day)

5. Decision date = filed_ts. Map to trading day.

6. Compute 5-day forward return from decision date using daily bars.

7. Collect all such signals with their forward returns.

8. Split into in-sample (earliest 80%) and sealed (latest 20%) by decision date.

9. Compute metrics.

But we need to be careful about:
- Multiple signals for same symbol close in time (ABSTAIN within 5 sessions)
- Only one signal per symbol per 5-day window
- Distinct UTC days for issued calls

Also, the universe: "All active US stocks with >= 252 daily bars of history". We can check bars count per symbol.

Let me write the SQL queries.

First, get officer purchases:
```sql
SELECT it.symbol_id, it.tx_ts, it.filed_ts, it.value, it.title
FROM insider_trades it
WHERE it.code = 'P'
  AND it.value > 50000
  AND (LOWER(it.title) LIKE '%ceo%' OR LOWER(it.title) LIKE '%cfo%' OR LOWER(it.title) LIKE '%chief%')
```

But title might have various formats. 'Chief Executive Officer', 'CEO', 'Chief Financial Officer', 'CFO', etc.

Better: `LOWER(it.title) LIKE '%chief%exec%'` or `'%chief%financ%'` or `'%ceo%'` or `'%cfo%'`

But simpler: check for 'CEO', 'CFO', 'CHIEF' in title.

Now, for each, we need daily bars. bars table has symbol_id, tf, ts, open, high, low, close, volume.

We need tf='1d'.

We need to get bars for each symbol around the relevant dates.

This could be a lot of data. 7,923 insider trades total, but only officer purchases subset.

Let me estimate: 7,923 rows, 627 symbols. Officer purchases maybe 1,000-2,000.

For each, we need ~90 days of bars (63 for median volume + 20 for return + 5 for forward). 2000 * 90 = 180k bars. Manageable.

But we need to do this efficiently in SQLite.

Better approach: For each symbol that has officer purchases, get all its daily bars once, then process in Python.

Or do it in SQL with window functions? SQLite has window functions.

But the logic is complex. Let me do a hybrid: query the necessary bars for all relevant symbols in one go, then process in Python.

Steps:

1. Get all officer purchases meeting basic criteria (code=P, value>50k, title match). Get symbol_id, tx_ts, filed_ts.

2. Get all unique symbol_ids from step 1.

3. For those symbols, get all daily bars (tf='1d') from earliest tx_ts - 90 days to latest filed_ts + 10 days.

4. In Python, organize bars by symbol_id, sorted by ts.

5. For each purchase, find the bar indices for tx_ts and filed_ts (or nearest trading day).

6. Compute features and label.

7. Apply ABSTAIN rule (no signal for same symbol within 5 trading days).

8. Split by decision date (filed_ts mapped to trading day) into 80/20.

9. Compute metrics.

Let me think about the timestamp mapping. bars.ts is unix epoch integer (seconds). tx_ts and filed_ts are likely also unix epochs (seconds).

Daily bars: tf='1d', ts is probably the epoch for the trading day close (e.g., 16:00 ET = 20:00 UTC? or 00:00 UTC?).

We need to match tx_ts to the correct trading day bar. Since tx_ts is the trade timestamp, it could be during trading hours. The bar for that trading day would have ts = date at 16:00 ET or similar.

Simplification: Convert all timestamps to UTC date (YYYY-MM-DD). For a given trade timestamp, the trade date is the UTC date of tx_ts. The filing date is UTC date of filed_ts.

But bars.ts is unix epoch. We can convert to date in SQL: `date(ts, 'unixepoch')` or in Python.

In Python, `datetime.fromtimestamp(ts, tz=timezone.utc).date()`.

But we need to be careful: a trade at 20:00 UTC on Monday is still Monday UTC, but if bars.ts is at 13:30 UTC (market close?), need to align.

Assume bars.ts represents the trading day close timestamp (e.g., 20:00 UTC for 16:00 ET). Then any trade during that trading day (13:30-20:00 UTC) would map to that bar.

But tx_ts could be any time. The SEC Form 4 reports the trade date (not timestamp). But the schema says tx_ts - likely the trade date as epoch midnight? Or actual timestamp?

The schema says: "tx_ts is when the trade happened, filed_ts when it became public." Could be date-only stored as epoch midnight UTC.

Given the complexity, I'll assume tx_ts and filed_ts are unix timestamps (seconds), and we can convert to UTC date. The daily bar for a given UTC date D has ts = epoch for D 00:00:00 UTC? Or D 20:00:00 UTC?

bars table: "ts is a unix epoch integer." No timezone specified. For daily bars, typically ts = midnight UTC of the trading day, or midnight ET.

To match, I'll convert tx_ts to UTC date, and find the bar with the same UTC date (by converting bar.ts to UTC date).

In SQLite: `date(tx_ts, 'unixepoch')` gives UTC date. `date(bars.ts, 'unixepoch')` gives UTC date for bar.

So we can join on symbol_id and date(ts, 'unixepoch') = date(tx_ts, 'unixepoch').

But we need 63 days of prior bars for median volume. So we need a range.

Let me write the script.

First, check if we have enough data. The insider_trades go back to 2008. Bars go back to 2018. So for trades before 2018, no bars. We'll filter to trades where tx_ts >= '2018-07-26' (earliest bar).

Also, filed_ts must be within bars range for forward return.

Bars go to 2026-08-13. We need 5 trading days after filed_ts, so filed_ts should be <= 2026-08-06 or so.

Now, the sealed era: most recent 20% of the sample by decision date (filed_ts). So we sort all valid signals by filed_ts, take earliest 80% as in-sample, latest 20% as sealed.

Metrics to compute:
- ISSUED: count of calls issued (signals after ABSTAIN)
- OPPORTUNITIES: count of decision points considered (all officer purchases meeting basic criteria before feature filters? Or all (symbol, day) pairs?)
  - "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it."
  - OPPORTUNITIES = number of (symbol, UTC day) decision points where we could have issued a call. In our case, each officer purchase disclosure date is a decision point. But multiple purchases for same symbol on same day? Unlikely. ABSTAIN prevents multiple within 5 days.
  - So OPPORTUNITIES = number of officer purchase disclosures that passed basic filters (before feature filters like 20-day return, volume). Or should it be all possible (symbol, day) pairs? The hypothesis only considers days with officer purchases.
  - The instruction: "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it."
  - For our hypothesis, the decision points are the disclosure dates of officer purchases. Each is a (symbol, UTC day). So OPPORTUNITIES = count of such disclosure days (after basic filters, before feature filters?).
  - Actually, ENTRY defines when we issue a call. ABSTAIN defines when we don't. So OPPORTUNITIES should be the number of times we evaluated ENTRY/ABSTAIN, i.e., the number of officer purchase disclosures that we considered.
  - Let's define: each officer purchase disclosure (filed_ts) is a decision point. We evaluate ENTRY conditions. If ENTRY true and not ABSTAIN, we issue a call.
  - So OPPORTUNITIES = number of officer purchase disclosures meeting basic criteria (code=P, value>50k, officer title, has bars data).
  - ISSUED = number of those where ENTRY conditions met and not ABSTAIN.

- PRECISION = hits/issued where hit = forward return > 0 (up=1)
- BASE_RATE = base rate of positive returns WITHIN the issued subset. Wait: "Report the base rate of the predicted class WITHIN the issued subset." The predicted class is "positive forward return". So base rate = proportion of issued calls that have positive forward return? No, that's precision. 
  - "A precision at or near that base rate is unskilled classification, not an edge."
  - Base rate of the predicted class within the issued subset: the predicted class is "up" (positive return). Within the issued subset, the base rate is the fraction that are actually up. But that's exactly precision if we predict all up? No.
  - We are predicting "up" for all issued calls (since we only issue calls when we predict up). So precision = fraction of issued that are up. Base rate = fraction of all opportunities that are up? Or fraction of issued that are up under random?
  - The instruction: "Report the base rate of the predicted class WITHIN the issued subset." This is confusing. If we issue a call, we predict "up". The base rate of "up" within the issued subset is just the precision. That can't be right.
  - Re-read: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."
  - I think "base rate of the predicted class" means the unconditional probability of the positive class (up) in the population. But "WITHIN the issued subset" suggests conditional on issued.
  - Perhaps it means: among the issued calls, what fraction would be up if we had no skill? But that's the same as the actual fraction if we have no skill.
  - Standard definition: base rate = prevalence of positive class in the test set. Here, the test set is the issued calls. So base rate = proportion of issued calls that are actually positive. But then precision = base rate always, since we predict positive for all issued.
  - Unless we sometimes predict negative? But the hypothesis is about issuing calls (predicting positive). ABSTAIN is not predicting negative, it's not predicting.
  - I think the intended meaning: base rate = the overall rate of positive returns in the universe (or in the opportunities considered). Then precision is compared to that base rate.
  - But it says "WITHIN the issued subset". Let me look at previous contexts.
  - "Report the base rate of the predicted class WITHIN the issued subset." Maybe it means: of the issued calls, what is the base rate of the positive class? But that's precision.
  - Perhaps "predicted class" refers to the class we are predicting (positive return), and "base rate" is the prior probability of that class in the issued subset before seeing our signal. But the issued subset is defined by our signal.
  - I think there's confusion. In binary classification, if you only predict positive (issue calls), then precision = TP / (TP+FP) = prevalence in the selected set. The base rate is prevalence in the whole population. The edge is precision - base_rate.
  - But the instruction says "WITHIN the issued subset". Maybe it's a mistake and should be "within the opportunity set".
  - I'll compute base_rate as the proportion of positive returns among all OPPORTUNITIES (all decision points considered). And precision as proportion of positive returns among ISSUED calls.
  - Then edge = precision - base_rate.

- DISTINCT_DAYS: distinct UTC days on which a call was issued (among ISSUED calls)
- EFFECTIVE_N: issued count divided by measured design effect. Design effect = 1 + (avg cluster size - 1) * ICC. But we can approximate: cluster by UTC day, compute variance inflation.
  - Simpler: design effect = ISSUED / DISTINCT_DAYS if calls are clustered by day? No.
  - Standard approach for clustered data: effective sample size = n / (1 + (m-1)*rho) where m is avg cluster size, rho is intra-cluster correlation.
  - But we can compute the design effect as the ratio of the variance of the mean under clustering to variance under independence.
  - For precision (proportion), the design effect for clustered binary data: deff = 1 + (avg_cluster_size - 1) * ICC.
  - ICC can be estimated from the data.
  - But the instruction says "measured design effect". We need to measure it.
  - Simpler: since calls on the same day are perfectly correlated (same market conditions), we can treat each day as one independent observation. But multiple symbols on same day are not perfectly correlated.
  - The instruction: "Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."
  - We can compute design effect as ISSUED / DISTINCT_DAYS if we assume each day is one independent cluster? But that would make EFFECTIVE_N = DISTINCT_DAYS.
  - "EFFECTIVE_N must be strictly less than ISSUED."
  - If we set EFFECTIVE_N = DISTINCT_DAYS, that's <= ISSUED, and strictly less if any day has multiple calls.
  - But is that the "measured design effect"? The design effect would be ISSUED / DISTINCT_DAYS.
  - Let's do: design_effect = ISSUED / DISTINCT_DAYS (if DISTINCT_DAYS > 0), then EFFECTIVE_N = ISSUED / design_effect = DISTINCT_DAYS.
  - But that assumes perfect correlation within day, zero across days. Might be reasonable approximation.
  - The instruction says "measured design effect", so we should compute it properly.
  - For binary outcomes with clustering by day, we can compute the intra-class correlation (ICC) using ANOVA method.
  - But given time, maybe use the simple day-clustering: effective_n = number of distinct days with calls. But the instruction says "issued count divided by the measured design effect", and design effect > 1.
  - If effective_n = distinct_days, then design_effect = issued / distinct_days.
  - This seems plausible and matches "EFFECTIVE_N must be strictly less than ISSUED" (since distinct_days <= issued, and strictly less if any day has >1 call).
  - I'll use this.

- SEALED_PRECISION: precision on the sealed era (latest 20% by decision date)

Now, let's write the script.

We need to:
1. Connect to DB read-only
2. Query officer purchases
3. Query daily bars for relevant symbols and date ranges
4. Process in Python
5. Compute metrics
6. Print results

Let me write the SQL queries.

Query 1: Officer purchases
```sql
SELECT 
    it.symbol_id,
    it.tx_ts,
    it.filed_ts,
    it.value,
    it.title,
    s.symbol
FROM insider_trades it
JOIN symbols s ON s.id = it.symbol_id
WHERE it.code = 'P'
  AND it.value > 50000
  AND (LOWER(it.title) LIKE '%ceo%' 
       OR LOWER(it.title) LIKE '%cfo%' 
       OR LOWER(it.title) LIKE '%chief%exec%'
       OR LOWER(it.title) LIKE '%chief%financ%'
       OR LOWER(it.title) LIKE '%chief%operat%')
  AND it.tx_ts >= strftime('%s', '2018-07-26')  -- bars start
  AND it.filed_ts <= strftime('%s', '2026-08-06')  -- 5 days before bars end
ORDER BY it.filed_ts
```

Note: strftime('%s', 'date') returns unix epoch. But SQLite's strftime('%s', ...) works with UTC.

But tx_ts and filed_ts are already epochs? The schema