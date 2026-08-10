Let me carefully analyze the task and write a Python script that tests the given hypothesis.

The hypothesis is about liquidity providers absorbing informed selling during low-volume pullbacks within established uptrends. Let me break down the requirements:

1. **Data Source**: SQLite database at `data/signaldeck.db` (read-only)
2. **Tables to use**: 
   - `bars(symbol_id, tf, ts, open, high, low, close, volume)` - 1d timeframe
   - `symbols(id, symbol, market, name, active, added_at, stream, delisted_at)`
   - `prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch)` - for labels

3. **Horizon**: T+5 trading days close-to-close
4. **Universe**: Symbols in daily-bars at T with >=252 completed prior sessions, close >=$5, and average daily dollar volume >=$5M over T-60..T-1, snapshot at T.

5. **ENTRY Conditions** (all must be true at decision time T):
   - (1) 20-day SMA slope at T-1 is positive
   - (2) T's close-to-close return is in [-2%, -0.3%]
   - (3) T's volume <= 60th percentile of its 20-day volume distribution
   - (4) T's close > 20-day SMA at T-1
   - (5) T's close > 50-day SMA at T-1

6. **ABSTAIN Conditions** (no call if any true):
   - close < $5
   - fewer than 252 prior sessions
   - 20-day SMA slope <= 0
   - return < -2% or > -0.3%
   - volume > 60th percentile
   - close <= 20-day SMA or <= 50-day SMA
   - 20-session realized volatility at T in top cross-sectional decile
   - a call issued for the same symbol in the prior 20 trading days
   - fewer than 30 independent observations remain

7. **Claim**: Precision >= 0.80 on issued UP calls at abstention rate >= 0.95, with precision minus issued-subset base rate >= 0.10.

8. **Output Requirements**:
   - ISSUED=<count>
   - OPPORTUNITIES=<count>
   - PRECISION=<decimal>
   - BASE_RATE=<decimal>
   - DISTINCT_DAYS=<count>
   - EFFECTIVE_N=<decimal>
   - SEALED_PRECISION=<decimal>

9. **Holdout**: Most recent 20% as sealed era

10. **As-of discipline**: Every input computable at decision timestamp T. No lookahead.

Let me think about the implementation:

1. First, I need to get all symbols with sufficient daily bars data
2. For each symbol, get daily bars (tf='1d')
3. Compute all required indicators at each decision point T
4. Check entry/abstain conditions
5. Get labels from prediction_outcomes for T+5 horizon (or compute from bars)
6. Split into training (80%) and sealed (20%) eras
7. Compute metrics

Wait, the labels should come from `prediction_outcomes` table which has `up` (realized direction) and `fwd_return` (realized forward return). The horizon is T+5 trading days. Let me check the schema: `prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch)`. The `horizon` column likely indicates the horizon in days. So for T+5, we'd look for horizon=5.

But wait - the hypothesis says "forecasting trend continuation over the following week" and "HORIZON: T+5 trading days close-to-close". So we need to check if price at T+5 > price at T (up direction).

Actually, the `prediction_outcomes` table has `up` which is "the realised direction" and `fwd_return` which is "the realised forward return". These are labels. So we can use this table directly.

But we need to be careful about as-of discipline. The `prediction_outcomes` table has `ts` (decision timestamp), `horizon`, and `resolved_at`. We should only use outcomes where `resolved_at` is not null (meaning they've resolved).

Let me think about the data flow:

1. Get all daily bars for tf='1d'
2. For each symbol, sort by ts
3. For each potential decision point T (which is a bar's ts), check if we have enough history (252 prior sessions)
4. Compute all indicators using data up to T (inclusive for T's bar, but for SMA slope at T-1 we use up to T-1)
5. Check entry conditions
6. Check abstain conditions
7. If issue call, record it and get the label from prediction_outcomes for same symbol_id, horizon=5, ts=T
8. Split by time: most recent 20% of decision points (or issued calls?) as sealed era

The holdout says "Hold out the most recent 20% of the sample as a sealed era". Sample likely means the decision points considered (opportunities) or the issued calls. I think it means the issued calls - we hold out the most recent 20% of issued calls by time.

Actually, re-reading: "Hold out the most recent 20% as a sealed era and report it separately from the rest." This likely refers to the sample of issued calls, sorted by decision timestamp.

Now, for the base rate: "Report the base rate of the predicted class WITHIN the issued subset." The predicted class is UP (price goes up over T+5). So base rate = proportion of issued calls where the label is UP (up=1).

Precision = hits / issued, where hits = issued calls where label is UP.

DISTINCT_DAYS = distinct UTC days on which a call was issued (among issued calls only).

EFFECTIVE_N = issued count / design effect. Design effect accounts for clustering. Since calls clustered in time are not independent, we need to measure the design effect. A simple way: design effect = 1 + (avg_cluster_size - 1) * ICC, but we don't have ICC. Alternatively, we can use the Kish design effect: deff = (sum(w_i)^2) / sum(w_i^2) where w_i are weights. But simpler: if we group by day, and compute the variance inflation due to clustering.

Actually, a common approach for time series: effective_n = n / (1 + 2*sum_{k=1}^{K} rho_k) where rho_k is autocorrelation at lag k. But that's complex.

Simpler: Since "calls clustered in time are not independent", we can compute the design effect as the ratio of the variance of the mean under clustering to the variance under independence. For binary outcomes, if we have clusters (days), and within each day we have m_d calls, with hit rate p_d, then the design effect can be approximated by 1 + (m_avg - 1) * rho, where rho is the intraclass correlation.

But the requirement says: "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

So we just need to compute some reasonable design effect > 1. A simple way: group issued calls by UTC day, compute the proportion of calls per day, then design effect = 1 + (avg_calls_per_day - 1) * ICC. But we don't know ICC.

Alternative: Use the Kish effective sample size formula: n_eff = (sum n_d)^2 / sum(n_d^2) where n_d is number of calls on day d. This is the design effect for unequal weighting, but here it measures clustering - if all calls are on one day, n_eff = 1; if one per day, n_eff = n.

Yes! The Kish effective sample size: n_eff = (Σ n_i)² / Σ n_i² where n_i is the size of cluster i. This is always ≤ n, with equality only when all n_i = 1 (one per day). This perfectly captures "calls clustered in time are not independent".

So EFFECTIVE_N = (total_issued)² / Σ(day_counts²)

This will always be < ISSUED unless every call is on a distinct day.

Perfect.

Now, let's plan the SQL queries.

First, get all symbols with daily bars:
```sql
SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'
```

But we need to join with symbols to get symbol info, and filter by active, etc.

Actually, we need daily bars for each symbol with tf='1d', ordered by ts.

Let me think about the computation steps:

For each symbol:
1. Get all daily bars (tf='1d') ordered by ts
2. For each bar at index i (ts = T), we need:
   - Close at T (current bar)
   - Volume at T
   - Return at T: (close_T - close_{T-1}) / close_{T-1}
   - 20-day SMA at T-1: average of close_{T-20} to close_{T-1}
   - 20-day SMA slope at T-1: slope of SMA over last 20 days? Or slope of the SMA line? The hypothesis says "20-day SMA slope at T-1 is positive". This likely means the slope of the 20-day SMA line, i.e., (SMA_{T-1} - SMA_{T-21}) / 20? Or more precisely, the slope of a linear regression on the last 20 SMA values? But typically "SMA slope" means the difference between current SMA and previous SMA: SMA_{T-1} - SMA_{T-2} > 0? Or over a window.

   Actually, "20-day SMA slope at T-1" - the SMA is a series. The slope at point T-1 could be approximated as (SMA_{T-1} - SMA_{T-2}) or (SMA_{T-1} - SMA_{T-21})/20. I think the most standard is the daily change in SMA: SMA_{T-1} - SMA_{T-2} > 0. But let's think: SMA at T-1 uses closes T-20 to T-1. SMA at T-2 uses closes T-21 to T-2. The difference is (close_{T-1} - close_{T-21})/20. So slope > 0 iff close_{T-1} > close_{T-21}. That's a 20-day momentum.

   But the hypothesis says "20-day SMA slope at T-1 is positive" - this is a trend filter. I'll interpret as SMA_{T-1} > SMA_{T-2} (i.e., the SMA is rising day over day).

3. 50-day SMA at T-1: average of close_{T-50} to close_{T-1}
4. 20-day volume percentile at T: volume_T <= 60th percentile of volume_{T-19} to volume_T (20-day window including T? Or up to T-1? The condition says "T's volume is <= 60th percentile of its 20-day volume distribution". The 20-day volume distribution at T likely means the last 20 days including T? Or up to T-1? Since volume at T is known at close of T, and we're making decision at close of T, we can include T's volume in the distribution. But typically you'd use the distribution up to T-1 to avoid lookahead. However, the condition is about T's volume relative to its recent distribution. Since we observe T's volume at decision time, we can rank it against the prior 20 days (T-19 to T) or prior 20 days excluding T (T-20 to T-1).

   The hypothesis says "declining volume on down-days signals absorption". So we're looking at volume on day T (a down day) being low relative to recent volume. The 20-day volume distribution should be the recent 20 days. Since we're at close of T, we know T's volume. The distribution could be the last 20 days including T (T-19 to T) or the 20 days prior to T (T-20 to T-1). I think using T-20 to T-1 (20 days prior) is safer for as-of discipline, because T's volume is the observation we're testing, not part of the reference distribution. But the condition says "20-day volume distribution" without specifying. Let me re-read: "T's volume is <= 60th percentile of its 20-day volume distribution". This implies the distribution is computed as of T, so likely includes T. But for as-of discipline, at decision time T (close), we know T's volume, so we can compute the percentile including T. However, if we include T, then by definition T's volume is at some percentile of the 20-day window ending at T. Using T-20 to T-1 means the reference window doesn't include the test day.

   I'll use the 20 days prior to T (T-20 to T-1) as the reference distribution for volume percentile. This is cleaner: we're comparing today's volume to the previous 20 days' distribution.

5. 20-session realized volatility at T: standard deviation of daily returns over last 20 sessions (T-19 to T) or (T-20 to T-1)? "at T" suggests including T. But return at T requires close_T and close_{T-1}. So 20-session realized volatility at T would use returns from T-19 to T (20 returns). This is known at close of T.

   "in top cross-sectional decile" - this means across all symbols at time T, the volatility is in the top 10%. So we need to compute cross-sectional decile at each T.

6. "a call issued for the same symbol in the prior 20 trading days" - we need to track issued calls per symbol and ensure 20-day cooldown.

7. "fewer than 30 independent observations remain" - this is an abstain condition. "Independent observations" likely means distinct (symbol, day) pairs that meet all other criteria. But this is a global condition? Or per decision point? The phrasing: "or fewer than 30 independent observations remain" - this seems like a global stopping rule: if at any point the remaining pool of potential observations (after all other filters) is < 30, we abstain. But that doesn't make sense as a per-decision condition. More likely, it's a minimum sample size requirement for the test overall. But it's listed under ABSTAIN conditions. Let me re-read: "ABSTAIN: No call if ... or fewer than 30 independent observations remain". This is ambiguous. It could mean: at the time of decision, if the number of qualified opportunities remaining in the universe is < 30, don't issue a call. But that's a global condition.

   Actually, "independent observations" in the measurement rules: "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it." So an independent observation is a (symbol, day) pair.

   The abstain condition "fewer than 30 independent observations remain" likely means: if after applying all other filters, the number of qualifying (symbol, day) pairs in the current era (or overall) is < 30, then abstain from issuing any calls. But this is a meta-condition.

   Given the complexity, and that this is likely a minimum sample size check for the sealed era or overall, I'll interpret it as: if the total number of issued calls (opportunities that meet entry criteria) in the current era (training or sealed) is < 30, then we don't issue calls (or we report INSUFFICIENT). But the requirement says to print INSUFFICIENT=1 if data is insufficient.

   Actually, the measurement rules say: "Never fabricate. If the data is insufficient, print INSUFFICIENT=1 and exit 0." And the abstain condition includes "fewer than 30 independent observations remain". I think this means if at any decision point, the number of remaining qualified decision points (looking forward?) is < 30, but that's lookahead.

   Let me think differently: perhaps "remain" means in the holdout/sealed era? Or it's a condition checked at the start: if the total sample size (qualified opportunities) is < 30, then INSUFFICIENT.

   Given the ambiguity, and since the measurement rules say to print INSUFFICIENT=1 if data insufficient, I'll check at the end: if total issued calls < 30, print INSUFFICIENT=1. But the abstain condition is per call.

   Wait, the abstain conditions are reasons to not issue a call at a specific decision point. "fewer than 30 independent observations remain" - at decision point T, how many independent observations remain in the future? That's lookahead. How many in the past? That doesn't make sense.

   Perhaps it means: for the current symbol, if there are fewer than 30 qualified decision points in its history? No.

   I think the most reasonable interpretation is: this is a global minimum sample size requirement. If the total number of independent observations (qualified decision points across all symbols and time) is less than 30, then the test is insufficient. But that would be checked once, not per decision.

   Given the confusion, and since the measurement rules explicitly say "If the data is insufficient, print INSUFFICIENT=1 and exit 0", I'll compute all qualified opportunities, and if the total count (before holdout split) is < 30, print INSUFFICIENT=1. But the abstain condition is listed as a per-decision abstain reason.

   Let me look at the exact wording again: "ABSTAIN: No call if ... or fewer than 30 independent observations remain"

   Perhaps "remain" means after applying all other abstain conditions, if the number of calls we would issue is < 30, then we abstain from issuing any? That doesn't make sense per decision.

   I'll treat this as: if at the time of decision T, the number of qualified decision points (symbol, day) that are available for testing (i.e., in the training era, or overall) is < 30, then don't issue a call. But this requires knowing the future or the full sample.

   Given the time constraints, I'll implement a simplified version: after computing all opportunities that meet entry criteria (ignoring this abstain condition), if the total count is < 30, print INSUFFICIENT=1. Otherwise, proceed. And for the per-decision abstain, I'll skip this condition as it's not well-defined for point-in-time.

   Actually, re-reading the measurement rules: "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it." And "Report the base rate of the predicted class WITHIN the issued subset."

   The "fewer than 30 independent observations remain" might refer to the sealed era: if the sealed era has < 30 issued calls, then we can't reliably measure SEALED_PRECISION. But the abstain condition is for issuing calls.

   I'll make a pragmatic decision: ignore this specific abstain condition for per-decision logic, but at the end, if total issued calls < 30 or sealed issued calls < 30, print INSUFFICIENT=1. But the requirement says to print INSUFFICIENT=1 and exit 0 if data insufficient. The abstain condition is separate.

   Let me check the claim: "Precision >= 0.80 on issued UP calls at abstention rate >= 0.95". Abstention rate = 1 - (issued / opportunities). So we need opportunities count.

   Okay, I'll proceed with the implementation, and for the "30 independent observations" abstain condition, I'll interpret it as a global check: if the number of qualified opportunities (after all other filters) in the training era is < 30, then INSUFFICIENT. But the hypothesis says it's an abstain condition, not an INSUFFICIENT condition.

   Let me just implement the other abstain conditions and skip this one, but add a check at the end that if issued < 30, print INSUFFICIENT=1. The measurement rules say "If the data is insufficient, print INSUFFICIENT=1 and exit 0." So if we have fewer than 30 issued calls total, that's insufficient data.

   Actually, the abstention rate requirement is >= 0.95, meaning we should issue calls on at most 5% of opportunities. So opportunities should be much larger than issued.

   Let me start coding.

   Steps:
   1. Connect to DB read-only
   2. Get all symbols with daily bars (tf='1d')
   3. For each symbol, get daily bars ordered by ts
   4. Compute indicators for each potential decision point
   5. Apply entry and abstain conditions (except the 30-obs one)
   6. Collect issued calls with decision timestamp, symbol_id, and get label from prediction_outcomes
   7. Split into training (80%) and sealed (20%) by decision timestamp
   8. Compute metrics
   9. Print required output

   For the label: we need to know if price went up over T+5 trading days. The prediction_outcomes table has horizon, ts, up, fwd_return. We can join on symbol_id, horizon=5, ts=decision_ts. But we must ensure resolved_at is not null (outcome known).

   However, the prediction_outcomes table might not have entries for all symbol/ts/horizon combinations. We might need to compute the label from bars directly: get close at T and close at T+5 (5 trading days later). But trading days - the bars are daily, so we need the 5th next bar. Since bars are only for trading days (assuming), we can just take the bar at index i+5.

   But the schema says bars has tf='1d' and ts is unix epoch. The bars are likely only for trading days. So we can compute forward return from bars directly: fwd_return = close_{i+5} / close_i - 1, and up = 1 if fwd_return > 0 else 0.

   This avoids relying on prediction_outcomes which might be sparse or have its own issues. And it's cleaner for as-of discipline: we only use bars data.

   The hypothesis says "forecasting trend continuation over the following week" and HORIZON: T+5 trading days close-to-close. So using bars to compute the 5-day forward return is appropriate.

   Let's use bars for labels.

   Implementation details:

   - Need to convert ts (unix epoch) to UTC date for DISTINCT_DAYS. ts is integer unix epoch. UTC day = date from ts (assuming ts is at market close, e.g., 20:00 UTC or similar). We can use datetime.utcfromtimestamp(ts).date().

   - For cross-sectional volatility decile: at each decision timestamp T, we need the 20-day realized volatility for all symbols that have a decision point at T, then find the 90th percentile, and abstain if a symbol's vol > 90th percentile.

   This means we need to process all symbols in parallel by time, or collect all decision points first, then compute cross-sectional stats per day.

   Approach:
   1. For each symbol, compute all potential decision points with all indicators (except cross-sectional vol decile and 20-day cooldown)
   2. Collect all decision points across symbols
   3. Group by decision date (UTC day)
   4. For each day, compute 90th percentile of 20-day realized vol among decision points that day
   5. Mark decision points where vol > 90th percentile as abstain
   6. Apply 20-day cooldown per symbol: sort decision points by time, only keep first if multiple within 20 trading days
   7. Apply the "30 independent observations" check? Still unclear.

   Let's define a decision point as a (symbol_id, ts) where ts is the bar's timestamp (close of day T).

   Universe filters at T:
   - Symbol has >=252 completed prior sessions (i.e., at least 252 bars before T)
   - Close at T >= $5
   - Average daily dollar volume over T-60 to T-1 >= $5M
     Dollar volume = close * volume. Average over 60 days prior to T (T-60 to T-1 inclusive? 60 days).

   Entry conditions at T:
   1. 20-day SMA slope at T-1 > 0
   2. Return at T in [-0.02, -0.003] (i.e., -2% to -0.3%)
   3. Volume at T <= 60th percentile of volume over T-20 to T-1 (20 days prior)
   4. Close at T > 20-day SMA at T-1
   5. Close at T > 50-day SMA at T-1

   Abstain conditions at T (additional):
   - 20-session realized volatility at T in top cross-sectional decile (at that T)
   - Call issued for same symbol in prior 20 trading days
   - (Fewer than 30 independent observations remain - skip for now)

   Label: up = 1 if close at T+5 > close at T, else 0. (T+5 means 5 bars later in the daily bars series)

   Now, the holdout: "Hold out the most recent 20% of the sample as a sealed era". Sample = issued calls? Or opportunities? The measurement rules say "Hold out the most recent 20% as a sealed era and report it separately from the rest." And SEALED_PRECISION is precision on sealed era. So we split the issued calls by decision time: most recent 20% by count are sealed.

   Let's say we have N issued calls sorted by decision_ts. Sealed = last ceil(0.2*N) calls. Training = first floor(0.8*N).

   But the abstention rate is computed on the full sample? The claim says "at abstention rate >= 0.95". Abstention rate = 1 - issued/opportunities. Opportunities are all decision points considered (that meet universe filters). This should be computed on the full sample (training + sealed) or just training? Probably full sample.

   The output requires:
   - ISSUED = total issued calls (training + sealed)
   - OPPORTUNITIES = total decision points that met universe filters (i.e., were considered for entry)
   - PRECISION = hits/issued on full issued set? Or training only? The claim says "Precision >= 0.80 on issued UP calls" - likely on the test (sealed) or full? But SEALED_PRECISION is reported separately. The main PRECISION is probably on the training era (or full non-sealed). But the output doesn't specify. Let's assume PRECISION is on the training era (non-sealed), and SEALED_PRECISION on sealed.

   Actually, the output lines: "PRECISION=<hits/issued as a decimal>" and "SEALED_PRECISION=<precision on the sealed era>". So PRECISION is on the non-sealed (training) era, SEALED_PRECISION on sealed.

   But the claim says "Precision >= 0.80 on issued UP calls at abstention rate >= 0.95". This might refer to the sealed era performance. However, the output format distinguishes them.

   I'll compute:
   - For training era (80% oldest issued calls): PRECISION, BASE_RATE, etc.
   - For sealed era (20% newest issued calls): SEALED_PRECISION
   - ISSUED = total issued (training + sealed)
   - OPPORTUNITIES = total opportunities (universe-qualified decision points) across full timeline
   - DISTINCT_DAYS = distinct UTC days among all issued calls
   - EFFECTIVE_N = ISSUED^2 / sum(day_counts^2) where day_counts are issued calls per UTC day

   BASE_RATE = base rate of predicted class (UP) WITHIN the issued subset. Which subset? The issued subset for which PRECISION is computed? The output says "BASE_RATE=<base rate of the predicted class WITHIN the issued subset>". Since PRECISION is for training era, BASE_RATE should be for training era issued calls. But it says "the issued subset" - could be all issued. Let's compute for training era to match PRECISION.

   Actually, the claim: "precision minus issued-subset base rate >= 0.10". This is the edge over base rate. So for the evaluation era (probably sealed), precision - base_rate >= 0.10. But we report both.

   I'll report BASE_RATE for the training era issued calls (since PRECISION is for training), and SEALED_PRECISION for sealed. The user can compare.

   Now, let's code.

   Performance considerations: 13.2M rows in bars. We can't load all into memory for all symbols at once? 13.2M rows * ~80 bytes = ~1GB, might be okay but we should be efficient. We can process symbol by symbol.

   But for cross-sectional volatility decile, we need all symbols' vol at each decision date. So we need to collect decision points per date.

   Plan:
   1. Query all distinct symbol_ids that have daily bars and are in symbols table (active, etc.)
   2. For each symbol, fetch its daily bars (tf='1d') ordered by ts. This could be done in a single query with window functions? SQLite supports window functions.

   Actually, we can do most computations in SQL using window functions! That would be much faster.

   Let me think about SQL approach.

   We need for each bar (symbol_id, ts) where tf='1d':
   - close, volume
   - return = (close - lag(close)) / lag(close)
   - sma_20 = avg(close) over (partition by symbol_id order by ts rows between 19 preceding and current row) -> this is SMA at T using T-19 to T. But we need SMA at T-1 for slope and comparison.
   - sma_20_prev = avg(close) over (partition by symbol_id order by ts rows between 20 preceding and 1 preceding) -> SMA at T-1 (uses T-20 to T-1)
   - sma_50_prev = avg(close) over (partition by symbol_id order by ts rows between 50 preceding and 1 preceding) -> SMA at T-1 (uses T-50 to T-1)
   - sma_20_slope = sma_20_prev - lag(sma_20_prev) over (partition by symbol_id order by ts) -> slope at T-1 (change from T-2 to T-1)
   - vol_20d = stddev of returns over 20 days. SQLite doesn't have stddev window function. We can compute variance = avg(x^2) - avg(x)^2.
     Returns: ret = (close - lag(close))/lag(close)
     Then vol_20 = sqrt(avg(ret^2) over 20 days - avg(ret)^2 over 20 days)
   - vol_20 at T: using returns from T-19 to T (20 returns). So window: 19 preceding to current row for returns.
   - volume_20d_percentile: we need the 60th percentile of volume over T-20 to T-1. SQLite doesn't have percentile window function easily. We'd need to compute rank. This is hard in SQL.

   Given the complexity of percentile and cross-sectional decile, it's easier to fetch the raw data per symbol and compute in Python.

   Let's fetch for each symbol: ts, close, volume for tf='1d' ordered by ts.

   Number of symbols: 1,780. Average bars per symbol: 13.2M / 1780 ≈ 7,400. Total data: 13.2M rows. Fetching all at once: SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts. This is 13.2M rows, 4 columns. In Python, this might take memory but should be okay (13.2M * ~100 bytes = 1.3GB, might be high). We can fetch in chunks or per symbol.

   Better: iterate over symbols, fetch bars for one symbol at a time, compute decision points, store them in a list. Decision points per symbol: at most number of bars. Total decision points << total bars because of universe filters (252 prior sessions, etc.). But initially we need to compute for all bars after 252.

   Let's estimate: 1780 symbols * (7400 - 252) ≈ 12.7M potential decision points. Still large but we can filter early.

   We can do the universe filter in SQL: only get symbols with enough bars. But we need the bars to compute indicators.

   Alternative: Use SQL to pre-filter symbols that ever meet the universe criteria? The universe criteria are at T: close >=5, avg dollar vol 60d >=5M, 252 prior sessions. These are time-varying.

   Given time, I'll write a Python script that:
   - Connects to DB
   - Gets list of symbol_ids from symbols table (maybe filter active=1, market='stocks'? The universe doesn't specify market, but bars has both stocks and crypto. The hypothesis doesn't restrict. We'll use all symbols with daily bars.)
   - For each symbol_id, fetch daily bars (ts, close, volume) as list of tuples
   - Compute indicators in Python
   - Collect decision points that meet universe filters
   - Then process cross-sectional vol decile and cooldown
   - Then get labels
   - Split and compute metrics

   To manage memory, we can fetch bars for one symbol at a time.

   Let's write the SQL for fetching bars for one symbol:
   ```sql
   SELECT ts, close, volume FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts
   ```

   We also need symbol info: from symbols table, but we mainly need to check delisted_at? The universe says "delisted_at is set only from 2026-07-24; before that the universe is survivor-seeded." So we should probably exclude symbols that are delisted before T? But the bars table likely only has bars up to delisting. The universe filter is at T: we just need the bars to exist. The survivor bias is acknowledged but we can't fix it. We'll just use available bars.

   Let's start coding.

   Steps in code:

   1. Connect to DB
   2. Get all symbol_ids that have tf='1d' bars: `SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'`
   3. For each symbol_id:
        a. Fetch bars: ts, close, volume ordered by ts
        b. If len(bars) < 252 + 60 + 50 + 20? Need at least 252 prior for first decision, plus 60 for dollar vol, 50 for SMA50, 20 for SMA20/vol. So min bars = 252 + 60 = 312? Actually, at decision index i (0-based), we need i >= 252 (252 prior sessions). For dollar vol avg over T-60..T-1, we need i >= 60. For SMA50 at T-1, need i-1 >= 50 => i >= 51. For SMA20 at T-1, need i >= 21. For vol20 at T, need i >= 19. So the binding constraint is i >= 252.
        c. Convert to arrays for easier computation
        d. Precompute:
           - returns: ret[i] = (close[i] - close[i-1]) / close[i-1] for i>=1
           - sma_20[i] = mean(close[i-19:i+1]) for i>=19 (