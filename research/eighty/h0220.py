Let me carefully analyze this task. I need to write a Python script that tests a specific trading hypothesis using the given database schema. Let me break down the requirements:

1. **Database**: Read-only SQLite at `data/signaldeck.db`
2. **Tables to use**: `bars`, `symbols`, `prediction_outcomes` (for labels)
3. **Hypothesis**: Benchmark-aware institutional flow chases liquid, low-volatility outperformers
4. **Horizon**: T+20 trading days close-to-close
5. **Universe**: All symbols with:
   - At least 504 prior sessions (daily bars)
   - Close >= $5
   - Average daily dollar volume >= $5M over prior 60 sessions
   - Snapshot at T (no lookahead)
   - No survivorship bias

6. **Entry conditions** (all must be true at T):
   - T's close within 5% of 252-session high
   - T's 20-session realized volatility < cross-sectional median
   - T's close-to-close return between 0% and +2%

7. **Abstain conditions** (any true = no call):
   - Price < $5
   - Fewer than 504 prior sessions
   - Any required bar missing for T-252..T
   - Call issued for same symbol in prior 20 trading days
   - Fewer than 30 independent observations remain
   - T's close-to-close return outside [0%, +2%]

8. **Claim**: Precision >= 0.80, abstention rate >= 0.95, precision - base_rate >= 0.10

9. **Output requirements**:
   - ISSUED, OPPORTUNITIES, PRECISION, BASE_RATE, DISTINCT_DAYS, EFFECTIVE_N, SEALED_PRECISION
   - DISTINCT_DAYS <= ISSUED (only count days where calls were issued)
   - EFFECTIVE_N < ISSUED (design effect > 1)

10. **Holdout**: Most recent 20% as sealed era

Let me think about the implementation:

1. First, I need to get all daily bars (tf='1d') with symbol info
2. For each symbol, I need to compute rolling windows
3. For each potential decision point T, check all conditions
4. Track calls issued and their outcomes
5. Use prediction_outcomes for labels (up column is the realized direction)
6. Split into training (80%) and sealed (20%) eras

Key challenges:
- Need to compute 252-session high, 20-session volatility, 60-session avg dollar volume
- Need to handle cross-sectional median volatility at each T
- Need to track cooldown (no call in prior 20 days for same symbol)
- Need to count independent observations (distinct symbol-day pairs)
- Need to compute design effect for EFFECTIVE_N

Let me think about the data flow:
1. Get all daily bars with symbol info (join bars and symbols)
2. Filter to active symbols with enough history
3. For each symbol, sort by ts
4. For each potential T (starting from 504th bar), compute features
5. Check entry conditions
6. If entry conditions met and no abstain conditions, issue call
7. Look up outcome in prediction_outcomes for horizon=20 (T+20 trading days)
8. Track results

Wait, the prediction_outcomes table has horizon, ts, up, fwd_return. The horizon is likely in trading days. So for a call at T, I need to find prediction_outcomes where symbol_id matches, horizon=20, and ts matches T's ts (or close to it).

Actually, prediction_outcomes has ts which is probably the prediction timestamp. So for a call at T (with timestamp ts_T), I'd look for prediction_outcomes with symbol_id, horizon=20, ts=ts_T.

But I need to be careful about as-of discipline. The prediction_outcomes are labels - they have up (realized direction) and fwd_return (realized forward return). These are computed after the fact, so using them for validation is fine as long as I don't use them to make the call decision.

Let me think about the timeline:
- T is the decision day (first trading day where close satisfies conditions)
- Label window is T+1 to T+20 (20 trading days)
- prediction_outcomes at ts=T with horizon=20 should give the label for T+20

Actually, the schema says prediction_outcomes has ts, horizon, up, fwd_return. The ts is likely the timestamp when the prediction was made (i.e., T), and horizon is the forward window. So yes, for a call at T, I look for prediction_outcomes with that symbol_id, ts=T_ts, horizon=20.

But wait - the prediction_outcomes table might not have entries for every possible T. It has 397,769 rows. I need to check if labels exist for my call timestamps.

Alternative: I could compute forward returns directly from bars. But the schema says prediction_outcomes "up is the realised direction and fwd_return the realised forward return: THESE ARE LABELS and are the safest label source." So I should use prediction_outcomes.

Let me design the algorithm:

1. Load all daily bars (tf='1d') with symbol metadata
2. Group by symbol_id, sort by ts
3. For each symbol with >= 504 bars:
   - For each index i from 503 to len(bars)-1 (0-indexed, so i=503 is the 504th bar):
     - T = bars[i]
     - Check if we have bars for T-252 to T (indices i-252 to i)
     - Check if close >= $5
     - Compute 60-session avg dollar volume (indices i-59 to i)
     - Compute 252-session high (indices i-251 to i) - wait, 252-session high including T? "T's close is within 5% of its 252-session high" - this likely means the high over the past 252 sessions including T, or maybe the prior 252 sessions? Usually "252-session high" means the high over the last 252 trading days. Let me re-read: "T's close is within 5% of its 252-session high". I think this means the high over the 252 sessions ending at T (inclusive).
     - Compute 20-session realized volatility (indices i-19 to i) - standard deviation of daily returns
     - Compute T's close-to-close return (from T-1 to T)
     - Check cross-sectional median of 20-session volatility across all symbols at this T
     - Check cooldown: no call for this symbol in prior 20 trading days
4. Collect all decision points (opportunities) and which ones result in calls (issued)
5. For each issued call, get label from prediction_outcomes
6. Split by time: most recent 20% of calls (by T timestamp) are sealed era
7. Compute metrics

Wait, "Hold out the most recent 20% as a sealed era" - 20% of what? The sample. Probably 20% of the issued calls by time, or 20% of the timeline. I think it means: sort all issued calls by T timestamp, take the most recent 20% as sealed.

"Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it." So each call is one observation (symbol, day). DISTINCT_DAYS is distinct UTC days among issued calls.

"Report the base rate of the predicted class WITHIN the issued subset." The predicted class is UP (up=1). So base rate = proportion of issued calls where up=1.

"Precision = hits/issued" where hits = issued calls where up=1.

"EFFECTIVE_N = issued count divided by the measured design effect". Design effect accounts for clustering. Since calls on the same day or same symbol are correlated, effective N < issued. I need to estimate design effect. A simple way: design effect = 1 + (avg cluster size - 1) * ICC. But we don't have ICC. Alternatively, we can use the Kish effective sample size formula: n_eff = (sum w_i)^2 / sum(w_i^2) where w_i are weights. But here, maybe we can cluster by day: if multiple calls on same day, they're correlated. Design effect = 1 + (m-1)*rho where m is avg calls per day, rho is intra-class correlation. Without rho, we can't compute exactly. But the requirement says "EFFECTIVE_N must be strictly less than ISSUED" and "design effect is always greater than 1". So I need to compute some design effect > 1.

A practical approach: cluster by day. Let n_d be number of calls on day d. If we assume rho=0.1 (conservative), design effect = 1 + (avg(n_d)-1)*0.1. But the problem says "measured design effect" - maybe we can measure it from the data? Or use a standard formula.

Actually, for clustered data, a common estimator of design effect is the ratio of the variance under clustering to variance under simple random sampling. But that's complex.

Simpler: The Kish effective sample size for unequal weights: n_eff = (sum w)^2 / sum(w^2). If we weight each call by 1/(number of calls on its day), then sum w = number of days, sum w^2 = sum(1/n_d). Then n_eff = D^2 / sum(1/n_d) where D is distinct days. But this gives effective number of days, not effective number of calls.

Wait, the output says "EFFECTIVE_N=<issued count divided by the measured design effect>". So EFFECTIVE_N = ISSUED / deff. And deff > 1.

A simple measurable design effect: deff = ISSUED / DISTINCT_DAYS * (1 + (avg_calls_per_day - 1) * rho). But we don't know rho.

Perhaps the intended approach: since calls on the same day are perfectly correlated (rho=1), then each day contributes only 1 independent observation regardless of how many calls. So effective N = DISTINCT_DAYS. But then EFFECTIVE_N = DISTINCT_DAYS, and design effect = ISSUED / DISTINCT_DAYS. This would make EFFECTIVE_N < ISSUED as long as there's any day with multiple calls.

But the requirement says "Calls clustered in time are not independent, so the design effect is always greater than 1". If we assume perfect correlation within day, deff = ISSUED / DISTINCT_DAYS. Then EFFECTIVE_N = DISTINCT_DAYS.

But is that "measured"? We can measure ISSUED and DISTINCT_DAYS. So deff = ISSUED / DISTINCT_DAYS (if >1). Then EFFECTIVE_N = DISTINCT_DAYS.

But wait, calls could also be clustered by symbol (same symbol on different days). The cooldown prevents calls within 20 days for same symbol, so symbol-clustering is limited.

I think the simplest measurable design effect that guarantees EFFECTIVE_N < ISSUED is deff = ISSUED / DISTINCT_DAYS (when ISSUED > DISTINCT_DAYS). Then EFFECTIVE_N = DISTINCT_DAYS.

But let me re-read: "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

If there's at least one day with 2+ calls, then DISTINCT_DAYS < ISSUED, so deff = ISSUED/DISTINCT_DAYS > 1, EFFECTIVE_N = DISTINCT_DAYS < ISSUED. Perfect.

If all calls are on distinct days, then DISTINCT_DAYS = ISSUED, deff=1, EFFECTIVE_N=ISSUED which violates the invariant. But the requirement says design effect is ALWAYS >1. So in that case, we'd need another clustering dimension. But with the 20-day cooldown per symbol, it's unlikely to have many calls on the same day for the same symbol. But different symbols on the same day are clustered in time.

I think using day-clustering with rho=1 (perfect correlation within day) is a conservative measurable design effect. deff = ISSUED / DISTINCT_DAYS. EFFECTIVE_N = DISTINCT_DAYS.

But the formula says "EFFECTIVE_N=<issued count divided by the measured design effect>". So if deff = ISSUED / DISTINCT_DAYS, then EFFECTIVE_N = ISSUED / (ISSUED / DISTINCT_DAYS) = DISTINCT_DAYS. Yes.

So I'll compute:
- DISTINCT_DAYS = number of distinct UTC days (from ts) among issued calls
- deff = ISSUED / DISTINCT_DAYS if DISTINCT_DAYS > 0 else 1
- EFFECTIVE_N = ISSUED / deff = DISTINCT_DAYS

But wait, if DISTINCT_DAYS == ISSUED, deff=1, EFFECTIVE_N=ISSUED, violating the invariant. The requirement says "design effect is always greater than 1". So perhaps we should use deff = max(1.001, ISSUED / DISTINCT_DAYS) or something. But that's fabricating.

Alternatively, maybe design effect accounts for both day and symbol clustering. But the cooldown prevents same-symbol calls within 20 days, so symbol clustering is minimal.

I think the intended solution is: since calls on the same day are correlated, the effective N is at most the number of days. So EFFECTIVE_N = DISTINCT_DAYS. And since there will be multiple calls on some days (cross-sectional), DISTINCT_DAYS < ISSUED, so EFFECTIVE_N < ISSUED.

I'll go with EFFECTIVE_N = DISTINCT_DAYS, which equals ISSUED / (ISSUED/DISTINCT_DAYS). So design effect = ISSUED/DISTINCT_DAYS.

Now, let me think about the implementation details.

**Data Loading:**
- Connect to database read-only
- Query bars with tf='1d' joined with symbols
- Need: symbol_id, ts, open, high, low, close, volume, symbol, market, active, delisted_at
- Filter: market='stocks' (probably, but schema says market is 'stocks' | 'crypto'. The hypothesis says "stock", so maybe filter to stocks)
- But universe says "All symbols in the daily-bars table at T" - so both stocks and crypto? The mechanism mentions "stock", but universe says all symbols. I'll include both unless specified.

**Survivorship bias:** "no survivorship bias" - symbols have delisted_at. For a decision at T, we should only include symbols that were active at T (delisted_at is NULL or > T). But the schema says "delisted_at is set only from 2026-07-24; before that the universe is survivor-seeded." So before 2026-07-24, we can't avoid survivorship bias. After that, we can filter by delisted_at. I'll apply the filter where possible.

**Time handling:** ts is unix epoch integer. Need to convert to UTC date for DISTINCT_DAYS. Use datetime.fromtimestamp(ts, tz=timezone.utc).date().

**Trading days vs calendar days:** The data is in trading sessions (bars are per trading day). The 252-session, 20-session, 60-session are trading sessions. The cooldown is 20 trading days. The horizon is 20 trading days. So we work in trading day index, not calendar days.

But DISTINCT_DAYS is "distinct UTC days on which a call was issued" - so calendar days (UTC dates).

**Cross-sectional median volatility:** At each decision point T (which is a specific ts), we need the median of 20-session volatility across all symbols that have a decision point at that same T. But symbols may not all have bars on the same calendar days (holidays, different markets). However, bars are per trading session, and ts is the timestamp of the bar. For daily bars, ts is likely the end-of-day timestamp for that trading day. Different symbols should have bars on the same trading days (US market hours). So we can group by ts (the timestamp of T) to compute cross-sectional median.

**Cooldown:** "a call was issued for the same symbol in the prior 20 trading days". So for each symbol, track the last call's trading day index (or ts), and skip if current T is within 20 trading days. Since we process sequentially per symbol, we can track last_call_index.

**Minimum 30 independent observations:** "fewer than 30 independent observations remain" - this is an abstain condition. But independent observations are (symbol, day) pairs. At the time of decision T, how many independent observations remain in the future? This is confusing. "Remain" suggests looking forward. But we can't know the future. Perhaps it means: at the time of the test (end of data), if the total issued calls would be <30, then abstain from all? Or for each T, if the number of future decision points is <30? That doesn't make sense.

Re-read: "ABSTAIN: No call if ... fewer than 30 independent observations remain". This is a condition checked at each decision point T. "Remain" likely means in the sample from T onward. But that would require knowing the future. Perhaps it's a global condition: if the total number of independent observations (issued calls) in the entire backtest is <30, then the test is insufficient. But it's listed under ABSTAIN conditions for each call.

I think it means: when considering a call at T, if the number of independent observations (symbol-day pairs) that have been or will be issued from T to the end of data is <30, then abstain. But that's lookahead.

Alternative interpretation: The hypothesis test requires at least 30 independent observations to be valid. So if at any point the remaining potential observations are <30, we stop. But "remain" is ambiguous.

Given the context, I think this is a global check: after collecting all issued calls, if ISSUED < 30, then INSUFFICIENT=1. But it's listed as an ABSTAIN condition per call.

Let me look at the claim: "Precision >= 0.80 on issued UP calls at an abstention rate >= 0.95". Abstention rate = 1 - (issued / opportunities). So opportunities are all decision points considered (where universe conditions met), issued are those where entry conditions met and no abstain conditions.

The "fewer than 30 independent observations remain" might mean: at decision point T, if the number of symbols that meet universe conditions at T is <30, then abstain for all symbols at that T. That is, cross-sectional sample size <30. That makes sense: you need a reasonable cross-section to compute cross-sectional median volatility, etc.

Yes! "independent observations" = (symbol, day) pairs. At a given T (trading day), the number of symbols meeting universe conditions is the number of independent observations available at that T. If <30, then the cross-sectional median is unreliable, so abstain for all symbols on that day.

That interpretation fits: at each T, count how many symbols qualify for universe (enough history, price, volume, not delisted). If that count < 30, then no calls issued on that day (abstain for all).

**Algorithm outline:**

1. Load all daily bars for tf='1d' with symbol info.
2. Filter symbols: active, market in ('stocks','crypto'), and for each bar at ts, symbol not delisted (delisted_at is NULL or delisted_at > ts). But delisted_at only reliable after 2026-07-24.
3. Group bars by symbol_id, sort by ts.
4. For each symbol, compute rolling features:
   - For each index i (0-indexed), need at least 504 prior sessions -> i >= 503 (since 0 to 503 is 504 sessions).
   - Actually "at least 504 prior sessions" - prior to T? "Universe: All symbols in the daily-bars table at T with at least 504 prior sessions". So at T, we need 504 sessions before T? Or including T? "Prior sessions" suggests before T. But then we need 504 sessions before T, plus T itself for the close. So total 505 bars? Let's parse: "at least 504 prior sessions" - sessions prior to T. So the earliest T is at index 504 (0-indexed), with indices 0..503 being prior sessions. But then 252-session high would use sessions T-251 to T? That's 252 sessions including T. But we only have 504 prior, so T-251 is at index 504-251=253, which is fine.

   Wait, "252-session high" - typically this means the high over the last 252 trading days including today. So we need 252 sessions ending at T. With 504 prior sessions, we have plenty.

   Let's define: at index i (T = bars[i]), we need i >= 504 (504 prior sessions: indices 0..503, T at 504). But then 252-session high uses indices i-251 to i (252 sessions). i-251 >= 504-251=253 >=0, ok.

   60-session avg dollar volume: indices i-59 to i (60 sessions including T). i-59 >= 504-59=445 >=0.

   20-session volatility: indices i-19 to i (20 sessions). Need i >= 19, which is satisfied.

   So minimum i = 504.

5. We need to process by time (ts) across symbols to compute cross-sectional medians and check the 30-observation minimum per day.

   So better approach: Collect all potential decision points (symbol_id, i, ts, bar data) where symbol has enough history (i >= 504). Then group by ts (trading day). For each ts (day), we have a cross-section of symbols.

   For each day (ts):
   - Filter symbols that meet universe conditions at that day:
     - close >= 5
     - 60-session avg dollar volume >= 5M
     - Not delisted (if delisted_at known and <= ts, exclude)
     - Have all bars for T-252..T (i.e., no gaps in bars? "any required bar missing for T-252..T" - we need continuous bars. Since we have bars table, if a bar is missing for a trading day, it won't be in the table. But we assume bars are present for all trading days for active symbols. We'll check that the index difference matches trading day count? Hard without calendar. We'll assume bars are consecutive trading days. The condition "any required bar missing" - we can check that for the symbol, the bars from i-252 to i exist (they do by construction if we have the array). But if there's a gap in ts (e.g., missing trading day), the index wouldn't correspond. Since we only have bars that exist, and we index by array position, we assume array position = trading day sequence. So if we have 504 prior bars in the array, they are 504 prior trading sessions. So no missing bars in the window.

   - If number of qualifying symbols at this ts < 30: abstain all (no calls this day). But still count as opportunities? "OPPORTUNITIES=<count of decision points considered>" - decision points considered are those where universe conditions met? Or all potential? The abstention rate = 1 - issued/opportunities. Opportunities should be the number of (symbol, T) pairs that met universe conditions (i.e., were eligible for a call). Then issued are those that also met entry conditions and passed abstain conditions (including the 30-obs rule).

   So for each day:
   - Get qualifying symbols (universe conditions)
   - If count < 30: no calls, but these symbols are still "opportunities considered"? The abstain condition says "No call if ... fewer than 30 independent observations remain". So at that T, for those symbols, we abstain. They are still opportunities (decision points considered) but we chose not to call. So opportunities include them.

   - Else (count >= 30):
     - Compute cross-sectional median of 20-session volatility for these qualifying symbols.
     - For each qualifying symbol:
       - Check entry conditions:
         1. Close within 5% of 252-session high: close >= 0.95 * high_252
         2. 20-session vol < cross-sectional median
         3. Close-to-close return in [0%, 2%]: ret = (close - prev_close)/prev_close, 0 <= ret <= 0.02
       - Check abstain conditions (additional):
         - Cooldown: no call for this symbol in prior 20 trading days (track per symbol)
       - If all entry true and no abstain: issue call.
       - Record opportunity (count++), and if issued, record call with ts, symbol_id, etc.

6. After collecting all calls and opportunities:
   - For each call, look up label in prediction_outcomes: symbol_id, horizon=20, ts=call_ts. Get 'up' (1 for up, 0 for down? or -1/1? Schema says 'up' is realised direction. Probably 1 for up, 0 for down. Or boolean. We'll treat as 1/0.)
   - If label not found, what to do? The schema says prediction_outcomes has 397k rows. It might not cover all our call timestamps. If label missing, we cannot evaluate that call. Should we exclude it? The requirement says "Never fabricate. If the data is insufficient, print INSUFFICIENT=1 and exit 0." But missing labels for some calls might be expected. However, the claim is about precision on issued calls. If we can't measure precision for some, we have insufficient data. But maybe prediction_outcomes covers all relevant timestamps. We'll check. If any issued call lacks a label, we might need to print INSUFFICIENT. But let's assume it's covered.

7. Split calls into sealed (most recent 20% by ts) and rest.
   - Sort calls by ts.
   - Sealed count = max(1, int(0.2 * total_calls))? "most recent 20% as a sealed era". So take the last 20% of calls by time.
   - Compute precision on sealed, and on rest (or overall? The output asks for SEALED_PRECISION and PRECISION (overall? or non-sealed?)). The output lines: PRECISION and SEALED_PRECISION. Probably PRECISION is overall (or in-sample), SEALED_PRECISION is on the held-out 20%. But the claim is about the hypothesis performance, likely overall. However, the requirement says "Hold out the most recent 20% as a sealed era and report it separately from the rest." So we report PRECISION on the rest (80%) and SEALED_PRECISION on the 20%. But the output only has PRECISION and SEALED_PRECISION. It doesn't have a separate "in-sample precision". So PRECISION might be overall, and SEALED_PRECISION is the held-out. But "report it separately from the rest" suggests we should report both. The output format only has two precision lines. I think PRECISION is the main precision (maybe on the 80%?), and SEALED_PRECISION is on the 20%. But the claim doesn't specify in-sample vs out-of-sample. The requirement: "PRINT exactly these lines at the end... SEALED_PRECISION=<precision on the sealed era>". So PRECISION is likely the precision on the non-sealed era (or overall?). To be safe, I'll compute PRECISION on the non-sealed (80%) and SEALED_PRECISION on the sealed (20%). But the output doesn't specify. Let me re-read: "Hold out the most recent 20% as a sealed era and report it separately from the rest." So we need to report metrics for the rest and for the sealed. But the print lines only include PRECISION and SEALED_PRECISION. Perhaps PRECISION is for the rest (the 80%), and SEALED_PRECISION for the 20%. That makes sense.

   However, the claim says "Precision >= 0.80 on issued UP calls" - this is likely the overall precision. But the requirement to hold out 20% is for validation. I'll compute PRECISION on the 80% (training) and SEALED_PRECISION on 20% (test). But the output format might expect PRECISION to be overall. The invariant doesn't specify. I'll output PRECISION as overall precision (all issued calls), and SEALED_PRECISION as precision on the sealed 20%. That way both are reported.

   Actually, "report it separately from the rest" implies two numbers: one for sealed, one for rest. But only two precision lines. So PRECISION = precision on rest, SEALED_PRECISION = precision on sealed. I'll do that.

8. Compute base rate within issued subset: for the issued calls (in the non-sealed? or overall?), the proportion where up=1. The requirement: "Report the base rate of the predicted class WITHIN the issued subset." The predicted class is UP. So base rate = (number of issued calls with up=1) / ISSUED. This should be computed on the same set as PRECISION. If PRECISION is on non-sealed, base rate should be on non-sealed. But the output doesn't specify SEALED_BASE_RATE. Only BASE_RATE. So likely BASE_RATE is for the same set as PRECISION (non-sealed). And SEALED_PRECISION is separate.

   But the claim mentions "precision minus issued-subset base rate >= 0.10". This is for the issued subset overall. So maybe BASE_RATE is overall issued subset base rate. And PRECISION is overall precision. Then SEALED_PRECISION is extra.

   I think the safest: compute everything on the full issued set for PRECISION, BASE_RATE, etc. And additionally compute SEALED_PRECISION on the sealed 20%. The requirement says "Hold out the most recent 20% as a sealed era and report it separately from the rest." So we must report sealed precision. The other metrics (ISSUED, OPPORTUNITIES, PRECISION, BASE_RATE, DISTINCT_DAYS, EFFECTIVE_N) are likely for the full sample (or for the non-sealed?). But ISSUED and OPPORTUNITIES are counts of calls and decision points - these are fixed for the full backtest. The sealed era is a subset of the issued calls. So ISSUED is total issued. SEALED_PRECISION is precision on the sealed subset of those issued calls.

   I'll do:
   - ISSUED = total calls issued in full backtest
   - OPPORTUNITIES = total decision points considered (universe-qualified symbol-days)
   - PRECISION = overall precision (hits/issued) on all issued calls
   - BASE_RATE = overall base rate (up=1 proportion) on all issued calls
   - DISTINCT_DAYS = distinct UTC days among all issued calls
   - EFFECTIVE_N = DISTINCT_DAYS (as argued)
   - SEALED_PRECISION = precision on the most recent 20% of issued calls (by ts)

   This satisfies "report it separately from the rest" by having SEALED_PRECISION separate.

9. Check INSUFFICIENT conditions:
   - If ISSUED == 0: INSUFFICIENT=1
   - If DISTINCT_DAYS == 0: INSUFFICIENT=1
   - If any issued call lacks label in prediction_outcomes: INSUFFICIENT=1? Or exclude those calls? The requirement: "Never fabricate. If the data is insufficient, print INSUFFICIENT=1 and exit 0." If labels missing, we can't compute precision. So INSUFFICIENT.
   - If OPPORTUNITIES == 0: INSUFFICIENT=1
   - Also, if fewer than 30 independent observations overall? The abstain condition is per day. But if total issued < 30, maybe insufficient. However, the claim requires precision >=0.80 etc. If ISSUED < 30, statistics unreliable. But not explicitly required to check. The abstain condition "fewer than 30 independent observations remain" is per day. I'll also check if total distinct days < 30 or something. But let's follow the script: if at the end, we have ISSUED calls, we compute. If ISSUED == 0, INSUFFICIENT.

10. Performance: Must run under 10 minutes. Python with sqlite3, 13.2M bars rows. Loading all bars into memory might be heavy but doable (13M rows * ~80 bytes = ~1GB). Might be tight. Better to process in chunks or use SQL for heavy lifting.

   Let's think about SQL approach:
   - We need to compute rolling windows. SQLite has window functions! We can use them.
   - But window functions on 13M rows might be slow but feasible.
   - Alternatively, load bars per symbol and process in Python. 1780 symbols, avg ~7400 bars each (13.2M/1780). That's small per symbol. We can query per symbol or load all and group in Python.

   Loading all bars: SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts. Then iterate in Python, grouping by symbol_id. 13M rows fetched - might take a few seconds but okay.

   Then for each symbol, we have a list of bars. We can compute rolling stats using Python loops. 1780 symbols * 7400 bars = 13M iterations, each doing some calculations. In pure Python, 13M loops might take a few seconds to tens of seconds. Acceptable.

   We also need to join with symbols table for metadata (delisted_at, active, market). And prediction_outcomes for labels.

   Let's design the data loading:
   - Fetch symbols: id, symbol, market, active, delisted_at. Filter active=1? "active" column. Universe: "All symbols in the daily-bars table at T". So if a symbol has bars at T, it's in the daily-bars table at T. But we should respect delisted_at: if delisted_at is set and <= T's date, the symbol is delisted and shouldn't be traded. However, "delisted_at is set only from 2026-07-24; before that the universe is survivor-seeded." So for T before 2026-07-24, we cannot filter by delisted_at. For T after, we can. We'll apply the filter when delisted_at is not null and delisted_at <= ts (converted to date).

   - Fetch bars for tf='1d' with symbol_id, ts, open, high, low, close, volume. Order by symbol_id, ts.

   - Fetch prediction_outcomes for horizon=20: symbol_id, ts, up. We'll store in a dict keyed by (symbol_id, ts) for fast lookup.

   Processing:
   - Group bars by symbol_id in memory (dict of lists).
   - For each symbol, if len(bars) < 505 (504 prior + 1 current), skip.
   - For each index i from 504 to len(bars)-1:
     - T_bar = bars[i]
     - Check universe conditions:
       - close >= 5
       - 60-session avg dollar volume: average of (close * volume) for bars[i-59:i+1] (60 bars including T). Wait, dollar volume = close * volume? Typically dollar volume = close * volume. Or (high+low)/2 * volume. But schema doesn't specify. "average daily dollar volume" - usually close * volume