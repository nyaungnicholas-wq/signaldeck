Let me break down this hypothesis test carefully.

**Hypothesis**: Turn-of-month pension and 401(k) inflows create price-insensitive liquidity demand; small, thinly intermediated names absorb this flow slowly, producing a first-week positive drift.

**Horizon**: 5 trading days from the first trading day's close.

**Universe**: All symbols in daily bars with:
- >= 252 trading days of history (trailing)
- Trailing 20-day median dollar volume >= $2M
- Close >= $2 as of signal time

**Entry**: Long call issued for every symbol in universe on the first trading day of each month.

**Abstain**: No call on other calendar days, or for symbols failing screens.

**Claim**: Precision on issued calls >= 0.80, with precision minus issued-subset base rate >= 0.10.

**Data sources available**:
- `bars(symbol_id, tf, ts, open, high, low, close, volume)` - 1d bars from 2018-07-26
- `symbols(id, symbol, market, name, active, added_at, stream, delisted_at)`
- `prediction_outcomes(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch)` - labels with `up` (realized direction) and `fwd_return`

**Key constraints**:
- Read-only SQLite
- As-of discipline: inputs computable at decision timestamp
- Hold out most recent 20% as sealed era
- Count independent observations: one (symbol, UTC day) = one observation
- Report base rate within issued subset
- Print specific metrics
- DISTINCT_DAYS <= ISSUED (invariant)
- EFFECTIVE_N < ISSUED (invariant)
- No pandas/numpy, stdlib only
- Exit with INSUFFICIENT=1 if data insufficient

**Approach**:
1. Load all daily bars (tf='1d') and symbols
2. For each symbol, determine first trading day of each month
3. At each first trading day (signal time), check universe criteria using TRAILING data only (as-of discipline)
4. For qualifying symbols, get label for 5-day forward return (from prediction_outcomes or compute from bars)
5. Compute precision, base rate, etc.
6. Split into 80%/20% by time (most recent 20% sealed)
7. Compute design effect for EFFECTIVE_N

Let me think about the label source. The hypothesis is about 5-day forward return being positive. The `prediction_outcomes` table has `up` (realized direction) and `fwd_return` for various horizons. I need to check if horizon=5 (trading days?) exists there. The schema says `horizon` but doesn't specify units. Could be days.

Actually, I should compute the 5-day forward return from bars directly to be safe, since prediction_outcomes might not have exactly horizon=5 or might be model predictions not ground truth. Wait, the schema says: "up is the realised direction and fwd_return the realised forward return: THESE ARE LABELS and are the safest label source." So prediction_outcomes ARE ground truth labels. But what horizons are available? Not specified. I'll need to check.

But the instruction says: "Never reference a table or column not listed above." So I can use prediction_outcomes. But I need to know what horizon values exist. Since it's not specified, I might need to compute from bars.

Actually, computing from bars is safer and respects as-of discipline better. Let me compute 5-trading-day forward return from daily bars.

**Trading day logic**: 
- First trading day of month: the first row in bars for that symbol for that year-month
- Signal time: close of that first trading day
- Horizon: 5 trading days later (so 5th trading day after signal day)
- Label: whether close on day T+5 > close on day T (signal day)

Wait, "5 trading days from the first trading day's close" - so if signal is at close of day 0, we look at close of day 5 (5 trading days later). That's 5 business days forward.

**Universe criteria at signal time (as-of)**:
- >= 252 trading days of history BEFORE signal day (trailing)
- Trailing 20-day median dollar volume >= $2M (using days -20 to -1 relative to signal day)
- Close >= $2 on signal day

**Dollar volume** = close * volume (for each day), then median over trailing 20 days.

**Implementation plan**:
1. Query all daily bars (tf='1d') joined with symbols, ordered by symbol_id, ts
2. Group by symbol_id
3. For each symbol, iterate through bars chronologically
4. Identify first trading day of each month (group by year-month, take first ts)
5. For each such signal day, check universe criteria using only prior bars
6. If qualifies, compute 5-day forward return (need bars for signal_day through signal_day+5 trading days)
7. Record: signal_date, symbol_id, label (1 if fwd_return > 0 else 0)
8. After collecting all calls, split by time: most recent 20% of signal_dates as sealed
9. Compute metrics

**Design effect for EFFECTIVE_N**: Since calls are clustered in time (all symbols on same first trading day of month), we need to account for temporal clustering. Design effect = 1 + (avg_cluster_size - 1) * ICC. But we don't have ICC. Simpler approach: effective N = number of distinct signal days (since all calls on same day are perfectly correlated in terms of market regime). But the invariant says EFFECTIVE_N < ISSUED, and DISTINCT_DAYS <= ISSUED. If we set EFFECTIVE_N = DISTINCT_DAYS, that would satisfy EFFECTIVE_N < ISSUED (unless only 1 call per day, but typically many symbols per day).

Wait, the invariant says: "EFFECTIVE_N must be strictly less than ISSUED. Calls clustered in time are not independent, so the design effect is always greater than 1 and the effective sample is always smaller than the raw count."

So design_effect = ISSUED / EFFECTIVE_N > 1.

A reasonable design effect for clustered data: if we have D distinct days and N total calls, with roughly N/D calls per day, and assuming intra-day correlation rho, design_effect = 1 + (N/D - 1) * rho. Without rho, we can use a conservative estimate. But the simplest that satisfies the invariant: EFFECTIVE_N = DISTINCT_DAYS (i.e., design_effect = ISSUED / DISTINCT_DAYS). This assumes perfect correlation within day (rho=1), which is conservative and ensures EFFECTIVE_N < ISSUED when multiple calls per day.

But the requirement says "measured design effect" - so we need to compute it. Let me think... The standard formula for design effect with clustering: deff = 1 + (m - 1) * ICC, where m is average cluster size. Here clusters are days. m = ISSUED / DISTINCT_DAYS. ICC (intraclass correlation) for stock returns on same day is typically 0.2-0.5. But we can't assume. 

Actually, the instruction says "EFFECTIVE_N=<issued count divided by the measured design effect>". So we need to measure design effect somehow. Since we only have stdlib, we could compute the variance inflation factor. But that's complex.

Simplest measurable design effect: ratio of total calls to distinct days, i.e., average calls per day. This assumes ICC=1 (perfect correlation within day), giving deff = m, so EFFECTIVE_N = DISTINCT_DAYS. This is a valid upper bound on design effect (since ICC <= 1), so measured design effect = m = ISSUED / DISTINCT_DAYS, then EFFECTIVE_N = ISSUED / deff = DISTINCT_DAYS.

This satisfies EFFECTIVE_N < ISSUED whenever DISTINCT_DAYS < ISSUED (i.e., multiple calls on at least one day).

Let me go with that: design_effect = ISSUED / DISTINCT_DAYS (average cluster size), EFFECTIVE_N = DISTINCT_DAYS.

**Sealed era**: Most recent 20% of the sample. Sample = all issued calls. Sort by signal_date, take most recent 20% of calls (or 20% of days?). "Hold out the most recent 20% as a sealed era" - likely 20% of observations (calls). But since calls are clustered by day, better to hold out 20% of days? The instruction says "most recent 20% of the sample" - sample is the issued calls. So sort calls by signal_date, take last 20% of calls.

But then sealed era might split a day. Better to hold out by day: sort distinct signal_dates, take most recent 20% of days, all calls on those days are sealed. This is cleaner.

The instruction: "Hold out the most recent 20% as a sealed era and report it separately from the rest." - "the sample" refers to the issued calls. But for time series, holding out by time chunks is standard. I'll hold out by signal_date: take distinct signal_dates sorted, most recent 20% of dates (rounding up), all calls on those dates are sealed.

**Base rate within issued subset**: For the issued calls (in the non-sealed or full set?), the claim says "precision minus issued-subset base rate >= 0.10". The base rate is the proportion of positive labels in the issued subset. So for the main (non-sealed) era, base_rate = sum(labels) / ISSUED_main. Precision = hits / ISSUED_main.

Wait, the PRINT lines:
- ISSUED=<count of calls issued> - total across all time? Or just non-sealed?
- SEALED_PRECISION=<precision on the sealed era>

The invariants mention DISTINCT_DAYS among ISSUED calls only. So ISSUED is total issued calls (including sealed?). But then SEALED_PRECISION is separate.

Let me re-read: "Hold out the most recent 20% as a sealed era and report it separately from the rest." So we have:
- Full sample: all issued calls
- Sealed: most recent 20% (by time)
- Main: first 80%

The printed metrics:
- ISSUED: probably total issued calls (full sample) or main sample? The invariant says "DISTINCT_DAYS counts days among the ISSUED calls only". If ISSUED is total, then DISTINCT_DAYS is total distinct days. SEALED_PRECISION is separate.

But the claim is about precision on issued calls >= 0.80. Which issued calls? Probably the main (non-sealed) ones, since sealed is held out for validation.

Actually, typical practice: train/validate on 80%, test on 20% sealed. The claim would be evaluated on the main 80%, and sealed is reported separately.

But the PRINT lines don't specify MAIN_PRECISION, only PRECISION and SEALED_PRECISION. So PRECISION is likely on the main (non-sealed) era.

Let me assume:
- ISSUED = total calls in main era (non-sealed)
- OPPORTUNITIES = total decision points considered in main era (first trading days of months where at least one symbol was evaluated)
- PRECISION = precision on main era
- BASE_RATE = base rate on main era issued calls
- DISTINCT_DAYS = distinct signal days in main era
- EFFECTIVE_N = ISSUED / design_effect (measured on main era)
- SEALED_PRECISION = precision on sealed era

But OPPORTUNITIES: "count of decision points considered". A decision point is a (symbol, signal_date) pair evaluated? Or just signal_date? The hypothesis says "A long call is issued for every symbol in the universe on the first trading day of each month." So decision points are (symbol, first_trading_day_of_month) pairs. But we only issue calls for symbols passing screens. So opportunities = all (symbol, month) pairs where symbol exists and has data for that month's first trading day? Or all symbols that exist at that time?

"ABSTAIN: No call is issued on any other calendar day, or for any symbol failing the history/liquidity/price screen." So opportunities considered = all symbol-month pairs where the symbol has a first trading day that month (i.e., symbol exists and has bars). But we only issue for those passing screens.

I think OPPORTUNITIES = number of (symbol, signal_date) pairs evaluated (i.e., symbols that had a first trading day that month and we checked screens). ISSUED = subset that passed screens.

But the invariant: "DISTINCT_DAYS counts days among the ISSUED calls only, never among the opportunities considered." So DISTINCT_DAYS <= ISSUED.

Let me define:
- For each month (year-month) that has at least one trading day in the data:
  - For each symbol that has a bar on the first trading day of that month:
    - This is an opportunity (we consider whether to issue)
    - Check screens using trailing data
    - If passes, issue call -> count in ISSUED, record label

So OPPORTUNITIES = total (symbol, month) pairs where symbol has first trading day bar.
ISSUED = subset passing screens.

**Data loading**:
- Need daily bars (tf='1d') with symbol info
- Need to know first trading day of each month per symbol
- Since symbols may have different start dates, first trading day of month for a symbol is the earliest bar in that year-month for that symbol.

**As-of discipline**: When checking screens on signal_date (first trading day of month), we can only use bars with ts < signal_date_ts (strictly before). The signal_date bar's close is known at close (so can be used for price screen >=$2). But volume for that day? At close we know the day's volume. But the screen says "trailing 20-day median dollar volume" - trailing 20 days INCLUDING signal day? Or 20 days prior? "Trailing" usually includes current. But as-of: at close of signal day, we know that day's volume. So trailing 20 days = signal day + 19 prior trading days. But the history requirement is ">= 252 trading days of history" - this likely means 252 days BEFORE signal day (not including signal day). Let's assume:
- History: count of trading days with ts < signal_ts >= 252
- Trailing 20-day median dollar volume: median of (close * volume) for the 20 trading days ending on signal day (i.e., signal day and 19 prior)
- Close >= $2: close on signal day

**Forward return**: 5 trading days from signal day's close. So we need close on signal_day (day 0) and close on day 5 (5 trading days later). Label = 1 if close_day5 > close_day0 else 0.

Need to ensure day 5 exists in data.

**Implementation steps**:

1. Connect to DB read-only
2. Query all daily bars with symbol info, ordered by symbol_id, ts
   - SELECT b.symbol_id, b.ts, b.close, b.volume, s.symbol, s.active, s.delisted_at
   - FROM bars b JOIN symbols s ON b.symbol_id = s.id
   - WHERE b.tf = '1d'
   - ORDER BY b.symbol_id, b.ts
3. Process in Python (no pandas):
   - Group by symbol_id
   - For each symbol, collect list of (ts, close, volume)
   - Convert ts to date (UTC): datetime.utcfromtimestamp(ts).date()
   - Group by year-month, find first trading day (min ts in each month)
   - For each first trading day (signal_date):
     - Check if symbol has >= 252 prior trading days (bars with ts < signal_ts)
     - If yes, compute trailing 20-day median dollar volume (using signal day and 19 prior)
     - Check close >= 2 on signal day
     - If all pass, this is an issued call
     - Find close on signal_day (day 0) and close on day 5 (5 trading days after)
     - If day 5 exists, label = 1 if close5 > close0 else 0
     - Record: signal_date, symbol_id, label
     - Also count as opportunity (whether issued or not)
4. After processing all symbols:
   - Have list of opportunities: (signal_date, symbol_id, issued_bool, label_if_issued)
   - Sort by signal_date
   - Split: distinct signal_dates sorted, take most recent 20% as sealed dates
   - Partition opportunities into main and sealed by signal_date
   - Compute metrics for main (issued only) and sealed (issued only)

**Edge cases**:
- Symbols delisted: delisted_at is set only from 2026-07-24. Before that survivor bias. We should probably exclude symbols after delisted_at if present. But delisted_at is a timestamp? Schema says delisted_at in symbols table. We'll check if signal_ts >= delisted_at (if delisted_at not null).
- Bars may have gaps (weekends/holidays not present). Trading days are only days with bars.
- 5 trading days forward: need to count 5 bars after signal bar in the symbol's bar list.

**Performance**: 13.2M bars, 1780 symbols. Processing in Python loops might be slow but should be under 10 minutes if optimized. Use arrays/lists, avoid per-row queries.

**Memory**: 13.2M rows * ~40 bytes = ~500MB, might be high. Better to stream per symbol? But SQLite doesn't easily stream grouped. We can query symbol_ids first, then for each symbol query its bars. 1780 symbols * query each - might be slower but lower memory.

Alternative: Query all bars once, iterate with fetchmany, group by symbol_id in Python using dict of lists. 13.2M rows in memory might be okay for modern machines (13.2M * 3 ints + 1 float ~ 13.2M * 28 bytes ~ 370MB). But we also need to store for processing. Might be tight but acceptable.

Better: Process symbol by symbol. Query distinct symbol_ids from bars (tf='1d'), then for each symbol_id, query its bars ordered by ts. This is 1780 queries, each returning ~7000 rows avg. This is fine.

Let's do symbol-by-symbol to save memory.

**SQL queries**:
1. Get all symbol_ids with 1d bars: SELECT DISTINCT symbol_id FROM bars WHERE tf='1d' ORDER BY symbol_id
2. For each symbol_id: SELECT ts, close, volume FROM bars WHERE tf='1d' AND symbol_id=? ORDER BY ts
3. Get symbol info: SELECT id, symbol, delisted_at FROM symbols WHERE id=?

But we need symbol info for all symbols. Better to load all symbols into dict first.

**Plan**:
- Load symbols dict: id -> (symbol, delisted_at)
- Get list of symbol_ids with 1d bars
- For each symbol_id in that list:
  - Fetch its daily bars
  - Process as above

**Date handling**: ts is unix epoch integer (seconds). Convert to date: datetime.utcfromtimestamp(ts).date(). But careful: ts might be midnight UTC? Bars are daily, ts likely represents the date (e.g., 00:00 UTC). We'll use date() for grouping by month.

**First trading day of month**: For each symbol, group bars by (year, month), take min ts.

**Trailing 252 days**: Count of bars with ts < signal_ts. Since bars are only trading days, count of prior bars.

**Trailing 20-day median dollar volume**: Need 20 bars up to and including signal day. So indices: signal_idx, signal_idx-1, ..., signal_idx-19. Need at least 20 bars up to signal_idx (inclusive). So signal_idx >= 19.

**Forward 5 days**: Need signal_idx + 5 < len(bars). So bars[signal_idx + 5] exists.

**Label**: bars[signal_idx + 5].close > bars[signal_idx].close

**Opportunity count**: For each symbol, each month where it has a first trading day (i.e., each month present in its bars), that's an opportunity. We evaluate screens. If passes, issued++.

**Sealed split**: Collect all issued calls with signal_date. Sort by signal_date. Get distinct signal_dates sorted. sealed_cutoff_idx = int(0.8 * len(distinct_dates)) (80/20 split). Sealed dates = distinct_dates[sealed_cutoff_idx:]. Main dates = distinct_dates[:sealed_cutoff_idx].

Then for each issued call, if signal_date in sealed_dates -> sealed, else main.

**Metrics**:
- ISSUED_main = count of issued calls in main
- OPPORTUNITIES_main = count of opportunities in main (all symbol-month pairs in main dates)
- PRECISION_main = sum(labels_main) / ISSUED_main if ISSUED_main > 0 else 0
- BASE_RATE_main = same as PRECISION_main? Wait, base rate of predicted class within issued subset. Predicted class is "up" (positive return). So base rate = proportion of issued calls where label=1. That's exactly the same as precision if we predict "up" for all issued calls. Since the strategy issues a long call (predicts up) for every issued call, precision = hit rate = base rate. Wait, that can't be right.

Let me re-read: "CLAIM: Precision on issued calls >= 0.80, with precision minus issued-subset base rate >= 0.10."

If the strategy issues a long call for every symbol in universe on first trading day, then every issued call is a prediction of "up". Precision = P(up | call issued) = hit rate. Base rate of predicted class within issued subset = P(up | call issued) = same thing. Then precision - base_rate = 0. That doesn't make sense.

Ah, "base rate of the predicted class WITHIN the issued subset" - the predicted class is "up". The base rate is the unconditional probability of "up" in the issued subset. But since every issued call predicts "up", the precision IS the base rate. Unless... the base rate refers to the overall market base rate? No, "WITHIN the issued subset".

Wait, maybe "predicted class" refers to the class predicted by the model, but here the hypothesis is a simple rule: always predict up on first trading day. So all issued calls predict up. Then precision = fraction of issued calls that are actually up. Base rate within issued subset = fraction of issued calls that are actually up. They are identical.

Unless... the base rate is the base rate of the positive class in the *universe* (all opportunities), not just issued? But it says "WITHIN the issued subset".

Let me re-read the measurement rules: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."

This implies precision and base rate can differ. How? Only if the prediction is not always the same class. But the hypothesis says "A long call is issued for every symbol in the universe on the first trading day of each month." So every issued call is a long call (predict up). There's no variation in prediction.

Unless... "precision" here means something else? In binary classification, if you always predict positive, precision = prevalence (base rate). So precision - base_rate = 0 always.

Perhaps the "predicted class" is not "up" but something else? Or perhaps the hypothesis implies a threshold? No, it says "A long call is issued for every symbol in the universe".

Wait, maybe "precision" is defined as P(up | call issued) and "base rate" is P(up) in the general population (all opportunities)? But it says "WITHIN the issued subset".

Let me think... In the context of the measurement rules: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."

This is a standard warning: if you predict class 1 for a subset, your precision on that subset is just the base rate of class 1 in that subset. To have an edge, you need precision > base rate. But if you predict class 1 for ALL issued calls, precision = base rate by definition.

Unless... the prediction is probabilistic? But the hypothesis is deterministic: issue long call for every symbol in universe.

I think there might be a misunderstanding. Perhaps "precision" here means the forward return being positive, and "base rate" is the base rate of positive returns in the *entire universe of opportunities* (including those not issued)? But the text says "WITHIN the issued subset".

Let me look at the PRINT lines again:
- PRECISION=<hits/issued as a decimal>
- BASE_RATE=<base rate of the predicted class WITHIN the issued subset>

If hits/issued = precision, and base rate within issued subset = hits/issued, they are the same. So the claim "precision minus issued-subset base rate >= 0.10" would be impossible.

Unless... "hits" means something else? "hits/issued" - hits = number of issued calls where label=1. So precision = P(label=1 | issued). Base rate within issued subset = P(label=1 | issued). Same.

Perhaps the hypothesis is that the model predicts "up" with some confidence, but the rule is deterministic. I'm confused.

Wait, maybe "predicted class" refers to a class predicted by some other model, and this hypothesis is testing whether the turn-of-month effect exists? But the task says "tests exactly this hypothesis" with the given mechanism.

Another interpretation: The "call" is the prediction. The predicted class is "positive return". The base rate of the predicted class within the issued subset is the proportion of issued calls that actually have positive return. Precision is... also that. Unless precision is defined as "of the calls that predicted positive, how many were positive" which is the same.

I think there might be an error in the hypothesis specification, or I'm missing something. Let me re-read: "CLAIM: Precision on issued calls >= 0.80, with precision minus issued-subset base rate >= 0.10."

Perhaps "issued-subset base rate" means the base rate of positive returns in the *entire* set of opportunities (all first trading days for all symbols), and "precision" is on the issued subset. Then precision - base_rate >= 0.10 means the screening selects symbols with higher win rate.

But the measurement rules say: "Report the base rate of the predicted class WITHIN the issued subset." That explicitly says within issued subset.

Let me check the measurement rules again: "Report the base rate of the predicted class WITHIN the issued subset. A precision at or near that base rate is unskilled classification, not an edge."

This is a general instruction for all hypothesis tests. For a hypothesis where the prediction varies (e.g., predict up only when some signal > threshold), precision on predicted-up calls vs base rate of up in those calls can differ if the signal has skill. But if you always predict up for all issued calls, they are identical.

Given the hypothesis as stated: "A long call is issued for every symbol in the universe on the first trading day of each month." This means every issued call predicts up. So precision = base rate within issued subset. The claim "precision minus issued-subset base rate >= 0.10" would be 0 >= 0.10, false.

But the claim is what we're testing. Maybe the hypothesis is wrong, and we're to test it and report the numbers. The claim might be false. We just need to compute the metrics as defined.

But the definitions make precision = base_rate. So D = 0. The claim requires D >= 0.10. We'll compute and report.

Perhaps "precision" here is not hits/issued but something else? The PRINT line says "PRECISION=<hits/issued as a decimal>". So yes, precision = hits/issued.

And "BASE_RATE=<base rate of the predicted class WITHIN the issued subset>". Since predicted class is "up" (long call), and all issued calls predict up, base rate within issued subset = hits/issued.

So they will be equal. The claim will fail. But we just report the numbers.

Wait, unless "hits" means something different? "hits/issued" - hits could be number of correct predictions. If prediction is always "up", correct when label=1. So hits = sum(label). Precision = sum(label)/issued. Base rate = sum(label)/issued. Same.

I'll proceed with this understanding. The script will compute them as equal.

**Design effect measurement**: 
- For main era issued calls, group by signal_date.
- Let D = number of distinct signal_dates (DISTINCT_DAYS)
- Let N = ISSUED_main
- Average cluster size m = N / D
- Design effect deff = 1 + (m - 1) * ICC
- We need to measure ICC. How? Intraclass correlation of labels within day.
- ICC = (between_day_var - within_day_var) / (between_day_var + (m-1)*within_day_var) ... standard ANOVA estimator.
- But we can compute the variance of labels within each day and between days.
- Since labels are binary (0/1), we can compute ICC for binary data (Fleiss' kappa or similar).
- Simpler: Use the design effect formula for clustered binary data: deff = 1 + (m - 1) * rho, where rho is the intracluster correlation coefficient.
- Estimate rho = (variance of cluster means) / (overall_mean * (1 - overall_mean)) ? Not exactly.
- Actually, for binary data, ICC = (sigma_b^2) / (sigma_b^2 + sigma_w^2), where sigma_b^2 is between-cluster variance of proportions, sigma_w^2 is within-cluster variance (p(1-p)/m).
- But we can compute the design effect directly as the ratio of the variance of the mean under clustering to variance under simple random sampling.
- Var_clustered(mean) = (1/N^2) * sum_{clusters} m_c^2 * p_c(1-p_c) + ... messy.
- Simpler approach used in practice: deff = 1 + (m - 1) * ICC, with ICC estimated by ANOVA.
- Since we have binary outcomes, we can compute:
  - Overall mean p = sum(y) / N
  - Between-cluster sum of squares: BSS = sum_c m_c (p_c - p)^2
  - Within-cluster sum of squares: WSS = sum_c m_c p_c (1 - p_c)
  - ICC = (BSS / (D-1) - WSS / (N-D)) / (BSS / (D-1) + (m-1) * WSS / (N-D)) ... this is the ANOVA estimator for ICC.
  - But this can be negative.
- Given time constraints, and the requirement that EFFECTIVE_N < ISSUED, we can use a conservative measured design effect: deff = max(1.0, N / D) (i.e., assume ICC=1). Then EFFECTIVE_N = N / deff = D. This satisfies EFFECTIVE_N < ISSUED when D < N.
- The instruction says "measured design effect". Using N/D is a measurement (average cluster size). It's an upper bound on design effect (since ICC <= 1). So it's a valid measured design effect (conservative).
- I'll use deff = N / D (average calls per day), so EFFECTIVE_N = D.

But wait: "EFFECTIVE_N=<issued count divided by the measured design effect>". If deff = N/D, then EFFECTIVE_N = N / (N/D) = D. So EFFECTIVE_N = DISTINCT_DAYS.

This satisfies the invariant EFFECTIVE_N < ISSUED (since D < N when multiple calls per day).

**Sealed era precision**: Compute on sealed issued calls.

**INSUFFICIENT condition**: If ISSUED_main == 0 or OPPORTUNITIES_main == 0 or not enough data to compute (e.g., less than some minimum). The instruction: "If there is insufficient data, print INSUFFICIENT=1 and exit 0." We'll check if we have at least some issued calls in main and sealed.

**Output format**: Exactly the lines:
ISSUED=<count>
OPPORTUNITIES=<count>
PRECISION=<decimal>
BASE_RATE=<decimal>
DISTINCT_DAYS=<count>
EFFECTIVE_N=<decimal>
SEALED_PRECISION=<decimal>

All on separate lines, no extra text.

**Decimal format**: Probably float with several decimals. Use e.g., f"{val:.6f}" or just str(val). The example doesn't specify precision. I'll use 6 decimal places.

**Implementation details**:

```python
import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Load symbols
    symbols = {}
    cur = conn.execute("SELECT id, symbol, delisted_at FROM symbols")
    for row in cur:
        symbols[row['id']] = (row['symbol'], row['delisted_at'])
    
    # Get symbol_ids with 1d bars
    cur = conn.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d' ORDER BY symbol_id")
    symbol_ids = [row['symbol_id'] for row in cur]
    
    all_opportunities = []  # (signal_date, symbol_id, issued, label)
    
    for sym_id in symbol_ids:
        if sym_id not in symbols:
            continue
        sym_info = symbols[sym_id]
        delisted_at = sym_info[1]
        
        # Fetch bars for this symbol
        cur = conn.execute("SELECT ts, close, volume FROM bars WHERE tf='1d' AND symbol_id=? ORDER BY ts", (sym_id,))
        bars = [(row['ts'], row['close'], row['volume']) for row in cur]
        if len(bars) < 252 + 20 + 5:  # rough minimum
            continue
        
        # Group by year-month to find first trading day
        # bars are sorted by ts
        month_first = {}  # (year, month) -> index in bars
        for idx, (ts, close, vol) in enumerate(bars):
            d = ts_to_date(ts)
            key = (d.year, d.month)
            if key not in month_first:
                month_first[key] = idx
        
        # For each month's first trading day (signal_idx)
        for (year, month), signal_idx in month_first.items():
            signal_ts, signal_close, signal_vol = bars[signal_idx]
            signal_date = ts_to_date(signal_ts)
            
            # Check delisted
            if delisted_at is not None and signal_ts >= delisted_at:
                continue
            
            # This is an opportunity
            # Check screens
            # 1. >= 252 trading days history (bars before signal_idx)
            if signal_idx < 252:
                all_opportunities.append((signal_date, sym_id, False, None))
                continue
            
            # 2. Trailing 20-day median dollar volume >= $2M
            # Need 20 days up to signal_idx inclusive
            if signal_idx < 19:
                all_opportunities.append((signal_date, sym_id, False, None))
                continue
            dollar_vols = []
            for i in range(signal_idx - 19, signal_idx + 1):
                _, c, v = bars[i]
                dollar_vols.append(c * v)
            dollar_vols.sort()
            median_dv = dollar_vols[10]  # 20 elements, median at index 10 (0-based, 10th of 20)
            if median_dv < 2_000_000:
                all_opportunities.append((signal_date, sym_id, False, None))
                continue
            
            # 3. Close >= $2
            if signal_close < 2:
                all_opportunities.append((signal_date, sym_id, False, None))
                continue
            
            # All screens passed - issued call