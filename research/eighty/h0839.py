Let me analyze this task carefully. I need to write a Python script that tests a specific hypothesis using the database schema provided. The hypothesis should:

1. Use data that actually exists in the database
2. Have a clear economic mechanism
3. Have a reasonable horizon given the data availability
4. Define entry and abstention conditions precisely
5. State a precision claim in advance
6. NOT be already in the killed list

Let me review the killed hypotheses to avoid duplication, and then design a new one.

Looking at the killed list, many involve:
- Insider purchases + various conditions (news sentiment, volume, VIX, float, etc.)
- 13F institutional accumulation + various conditions
- News sentiment patterns
- Float changes
- Form 144 filings
- Officer/CEO/CFO specific trades
- Macro + micro combinations
- Volatility compression
- Retail sentiment (StockTwits)
- Disclosure delay/urgency
- Corporate dormancy
- Track records
- 52-week high/low
- Round numbers
- Gap downs
- Earnings (but no earnings data)
- Short interest (but short volume only 10 weeks)
- Options (no options data)
- Index membership (no data)
- Bid-ask spreads (no intraday)
- Borrow fees (no data)
- Sector data (no sector data)

Available data with good history:
- Daily bars 2018-now (8 years)
- Insider trades 2008-2026 (18 years, 627 symbols)
- News sentiment 2012-2026 (14 years, 825 symbols)
- StockTwits sentiment (date range not specified but likely recent)
- Fundamentals (key/value: EPS, Revenues, SharesOutstanding, EntityPublicFloat, CIK, LatestFilingDate)
- 13F institutional holdings 2023-2026 (quarterly, 103 symbols)
- Macro series (FRED, 1947-now)
- Short volume (only 10 weeks - too short)
- SEC filings (only 2026-02 to now - very recent)
- Anomalies (2026-07 to now - very recent)
- Prediction outcomes (2026-07 to now - very recent, only 44 label days)

Key constraints:
- Horizon: prediction_outcomes only has 1d, 1d#pm, 1w, 1w#pm horizons (44 label days). For longer horizons, must build from bars (tf='1d').
- Bars span ~8 years over 2,263 symbols with >=252 daily bars.
- Hold out most recent 20% as sealed era.
- Count independent observations: one (symbol, UTC day) = one observation.
- Report base rate within issued subset.
- Precision claim must be stated in advance.

Let me think of a hypothesis not in the killed list...

Looking at the killed hypotheses, I see many combinations but some gaps:

What about: **Insider purchases when the stock has experienced a prolonged period of declining volume (liquidity drought) combined with stable/improving fundamentals** - but #28/KILLED covers "Insider open-market purchases during low-volume periods signal conviction..."

What about: **Insider purchases by 10% owners specifically (not officers/directors) when they increase their stake above a threshold** - #66/KILLED covers "Different insider roles (CEO, CFO, other officers, directors, 10% owners) possess distinct..."

What about: **Insider purchases when the stock's price is near a multi-year low but fundamentals (revenue/EPS) are at multi-year highs** - #6/KILLED covers "Insiders buying at 52-week lows while fundamentals remain positive..."

What about: **Insider purchases after a prolonged period of NO insider selling (corporate selling dormancy)** - #7/KILLED (later) covers "Insiders with long holding periods only break personal selling dormancy when private infor..."

What about: **Insider purchases when the company has consistent share buybacks (float reduction) AND insiders are buying at prices BELOW the company's buyback price** - #48/KILLED (later) covers "Companies actively reducing share count via buybacks while insiders buy at trade dates dur..."

What about: **Insider purchases when the stock has high short interest but the short interest is DECLINING** - short volume only 10 weeks.

What about: **Insider purchases when the stock's correlation with its sector/peers has broken down** - no sector data.

What about: **Insider purchases when the stock has had a large price drop on NO news (no news that day) and insiders step in** - #17/KILLED, #33/KILLED, #65/KILLED cover forced liquidity-driven selling.

What about: **Insider purchases when the stock's revenue growth is accelerating but the stock has underperformed the market** - #59/KILLED covers "Insiders accumulate when their company's fundamentals improve steadily but the stock expe..."

What about: **Insider purchases by officers (CEO/CFO) when they have a long tenure at the company** - not explicitly covered.

What about: **Insider purchases when the stock's public float is very low (microcap) and insiders buy** - #3/KILLED, #10/KILLED, #13/KILLED, #24/KILLED, #61/KILLED cover neglected/low-attention stocks.

What about: **Insider purchases when the stock has zero analyst coverage** - no analyst data.

What about: **Insider purchases when the stock's options put/call ratio is extreme** - no options data.

What about: **Insider purchases when the company announces a buyback AND insiders buy shortly after** - #2/KILLED (later) covers "Officers (CEO/CFO) buying during consistent corporate buybacks in attention-drought stocks"

What about: **Insider purchases when the stock has had a series of higher lows (uptrend forming) but is still below its 200-day MA** - #28/KILLED covers "The first 50-day/200-day moving-average crossover after a prolonged 250-session downtrend..."

What about: **Insider purchases when the stock's revenue per share is accelerating** - #20/KILLED covers "When revenue growth decelerates but EPS growth accelerates (margin expansion), and an offi..."

What about: **Insider purchases when the company has negative enterprise value (cash > market cap)** - no enterprise value data directly, but have SharesOutstanding and could compute from close price.

What about: **Insider purchases when the stock is at a 52-week high AND the insider is a 10% owner** - #4/KILLED covers "Insiders buying at a 52-week high is a costly signal..." and #66/KILLED covers roles.

What about: **Insider purchases when the stock has had a large increase in institutional ownership (13F) but the stock price has been flat/declining** - #30/KILLED (later) covers "Institutions accumulate gradually (visible in 13F with 45-day lag) while insiders confirm..."

What about: **Insider purchases when the stock's short volume ratio is high but declining** - short volume only 10 weeks.

Let me think about something using **StockTwits sentiment** which seems less explored in combination with insiders:

#54/KILLED: "Professional news sentiment and retail social sentiment diverge when retail flow detects f..." - news vs retail divergence.
#15/KILLED: "Retail attention-driven herding on StockTwits creates transient liquidity-demand imbalance" - StockTwits herding alone.
#24/KILLED (later): "Retail attention (StockTwits message volume) chronically neglects certain stocks, creating..." - StockTwits volume neglect.
#8/KILLED (later): "Attention-constrained retail traders abandon posting (bullish+bearish counts collapse) whe..." - posting collapse.

What about: **Insider open-market purchases when StockTwits message volume collapses (attention drought) BUT news sentiment is improving** - combination of retail neglect + professional optimism + insider confirmation.

Or: **Insider purchases when StockTwits bearish/bullish ratio is at extreme (high bearish) AND news sentiment is improving** - retail panic + professional improvement + insider buying.

But #9/KILLED: "Insider open-market purchases disclosed during periods of unusual retail pessimism signal..." - this is close.

#40/KILLED: "Retail investors underreact to improving news sentiment when institutional accumulation is..." - not insiders.

#54/KILLED: "Professional news sentiment and retail social sentiment diverge when retail flow detects..." - divergence but not with insiders.

So the combination of: extreme retail pessimism (StockTwits) + improving professional news sentiment + insider buying - might be novel.

But wait, the prompt says "Do NOT repeat a hypothesis already present in this journal tail". #9/KILLED is very similar - "Insider open-market purchases disclosed during periods of unusual retail pessimism signal..." - if "unusual retail pessimism" means StockTwits bearish extreme, then adding "news sentiment improving" might be considered a variant.

Let me think of something more distinct using **fundamentals** data which is key/value (EPS, Revenues, SharesOutstanding, EntityPublicFloat, CIK, LatestFilingDate).

Many killed hypotheses use "fundamental acceleration" or "improving fundamentals" but often combined with other things.

What about: **Insider purchases when the company's revenue per share (Revenues/SharesOutstanding) has accelerated for 3+ consecutive quarters AND the stock has underperformed the market** - but #59/KILLED covers "Insiders accumulate when their company's fundamentals improve steadily but the stock expe..."

What about: **Insider purchases when the company has consistent quarterly revenue growth (4+ quarters) AND insiders buy at a discount to revenue per share** - not sure.

What about: **Insider purchases when the company's public float has been stable (no dilution) for 8+ quarters AND revenue is growing** - #39/KILLED, #60/KILLED, #46/KILLED, #11/KILLED (later) cover non-diluting companies.

What about: **Insider purchases by officers (CEO/CFO) when the company has high insider ownership already (alignment)** - not explicitly covered.

What about: **Insider purchases when the stock's price-to-revenue ratio is at a multi-year low** - would need to compute from price and revenue.

What about: **Insider purchases when the company has zero debt (but no debt data)** - #25/KILLED (later) covers "High-quality companies (stable/improving margins, low leverage, consistent earnings) that..."

What about: **Insider purchases when the stock has had a large gap down on earnings (but no earnings dates)** - no earnings data.

Let me look at **macro_series** - FRED data. Many killed hypotheses use FRED:
#13/KILLED, #18/KILLED, #19/KILLED, #42/KILLED, #55/KILLED, #42/KILLED (later), #55/KILLED (later).

What about: **Insider purchases when the yield curve (10Y-2Y) is steepening AND the stock is a financial** - but no sector data.

What about: **Insider purchases when the VIX is elevated** - #12/KILLED covers "Insider open-market purchases when the VIX term structure is in backwardation..." and #14/KILLED (later) covers "Insider open-market purchases during a shift from negative to positive news sentiment indi..." wait no, #12/KILLED is "Insider open-market purchases when the VIX term structure is in backwardation..." but VIX term structure not in FRED? FRED has VIX but not term structure.

Actually #12/KILLED: "Insider open-market purchases when the VIX term structure is in backwardation signal..." - but we only have macro_series which are FRED series. VIX is available (VIXCLS), but term structure needs multiple tenors.

What about: **Insider purchases when the unemployment rate is falling (initial claims declining)** - #25/KILLED, #26/KILLED cover this.

What about: **Insider purchases when the Fed balance sheet is expanding** - #42/KILLED covers this.

What about: **Insider purchases when the dollar index (DXY) is weakening** - not explicitly covered but macro.

Let me think about **anomalies** table - 5,371 rows, 636 symbols, 2026-07-02..2026-08-16. Very recent, only ~1.5 months. Not enough for 21-day horizon with 20% holdout.

**prediction_outcomes** - only 44 label days, 2026-07-03..2026-08-16. Very recent.

**short_volume** - only 10 weeks.

**filings** - only 2026-02 to now.

**stocktwits_sentiment** - 2026-07-11..2026-08-16. Very recent.

So the only tables with long history are:
- bars (2018-now)
- symbols (2018-now)
- insider_trades (2008-now)
- news (2012-now)
- sentiment_features (2012-now)
- macro_series (1947-now)
- fundamentals (2026-07-06..2026-08-15 - wait, this says 2026-07-06..2026-08-15? That's only ~1 month! But 5,959 rows, 852 symbols. Hmm, "as_of" is the period, "fetched_at" is when we learned it. So as_of could be historical periods. The date range shown is for fetched_at probably. So fundamentals could have historical as_of dates.)

Wait, the schema says: "fundamentals(symbol_id, metric, value, as_of, fetched_at) -- 5,959 rows, 852 symbols, 2026-07-06..2026-08-15." This date range is likely for fetched_at. The as_of could be older periods. But we only know fetched_at at decision time (as-of discipline). So we can only use fundamentals where fetched_at <= decision timestamp.

Similarly, inst_holdings: "period is the quarter END; 13Fs are filed up to 45 days later, and this table does NOT record the filing date. Treating period as knowable is a 45-day lookahead. Prefer another input unless you lag it >=45d."

So for 13F, we need to lag by 45 days.

Now, for the hypothesis, I need something testable with sufficient data. Let me design one that uses:
- insider_trades (long history, 2008-2026)
- bars (long history, 2018-now)
- news/sentiment_features (2012-now)
- fundamentals (if as_of historical, but fetched_at recent - tricky)
- macro_series (long history)

Horizon: 21 trading days (about 1 month). Since prediction_outcomes only has up to 1w, I'll need to build labels from bars (tf='1d').

Let me propose: **Insider open-market purchases (code='P') by officers (CEO/CFO) when the stock has experienced a prolonged period of low news sentiment volatility (calm) AND the company's revenue per share has been accelerating, AND the purchase occurs at a price below the 200-day moving average.**

Wait, #56/KILLED: "Volatility compression indicates a liquidity vacuum where informed insiders accumulate bef..." - vol compression + insiders.
#4/KILLED: "Dual compression of price volatility and news sentiment volatility during accelerating fun..." - dual compression + fundamentals.
#21/KILLED (later): "When news sentiment volatility compresses to a 252-day low while realized price volatility..." - sentiment vol compression.
#45/KILLED (later): "Informed insiders accumulate when their company's fundamentals accelerate persistently bu..." - fundamentals accelerate + insiders.
#20/KILLED (later): "When revenue growth decelerates but EPS growth accelerates (margin expansion), and an offi..." - margin expansion + officers.

So vol compression + fundamentals + insiders is covered.

What about: **Insider purchases when the stock's price has been in a tight range (low volatility) for an extended period, news sentiment is improving from negative to neutral, AND the insider is a 10% owner (not officer/director)** - #66/KILLED covers roles, #56/KILLED covers vol compression + insiders, #14/KILLED covers sentiment shift + insiders.

What about: **Insider purchases when the company has consistent share buybacks (float declining) AND insiders buy at prices BELOW the volume-weighted average price of the company's buybacks** - no buyback price data.

What about: **Insider purchases after a prolonged period of NO insider activity at the corporate level (corporate dormancy) AND the stock has high short interest** - short data too short.

What about: **Insider purchases when the stock's correlation with the market (SPY) has broken down** - no SPY data in macro_series? Could use a macro series as proxy but not direct.

What about: **Insider purchases by officers when they have NOT sold in the past 2 years AND the stock is at a 52-week low** - #32/KILLED (later): "Officers (CEO/CFO) who have not sold a single share for 730+ sessions demonstrate exceptio..." - not selling. #6/KILLED: "Insiders buying at 52-week lows while fundamentals remain positive..." - 52-week low + fundamentals. But not combined with not-selling.

Actually #32/KILLED (later) is about not selling, not about buying. #7/KILLED (later): "Insiders with long holding periods only break personal selling dormancy when private infor..." - selling dormancy. #9/KILLED (later): "An insider breaking a multi-year personal buying dormancy in a stock with a stable public..." - buying dormancy.

What about: **Insider purchases when the stock has had a large price drop on high volume with NO insider selling (insiders not fleeing)** - #17/KILLED, #33/KILLED, #65/KILLED cover forced selling + insiders buying.

Let me think about **officer vs non-officer** distinction. #66/KILLED: "Different insider roles (CEO, CFO, other officers, directors, 10% owners) possess distinct..." - roles have distinct signals.

What about: **Insider purchases by 10% owners specifically when they are increasing their stake above 10% (crossing threshold)** - not explicitly covered.

What about: **Insider purchases when the stock's short interest is high but the stock has positive news sentiment** - short data too short.

What about: **Insider purchases when the company's revenue growth is accelerating but EPS growth is decelerating (margin compression) AND insiders buy** - #20/KILLED covers opposite (margin expansion).

What about: **Insider purchases when the stock is in a "neglected" state: low institutional ownership (13F), low news coverage, low volume, AND insiders buy** - #3/KILLED, #10/KILLED, #13/KILLED, #24/KILLED, #61/KILLED cover neglected stocks.

What about: **Insider purchases when the stock's price is near a round number (psychological level) AND it's a Friday** - #3/KILLED (later): "Retail investors anchor to round-number price levels, so insider open-market purchases nea..." and #31/KILLED (later): "Insiders strategically time Form 4 disclosures for Fridays to bury news over the weekend w..." - Friday disclosure.

What about: **Insider purchases when the stock has had a series of higher highs and higher lows (uptrend) but news sentiment is still negative** - not explicitly covered.

What about: **Insider purchases when the company's public float has been reduced consistently (buybacks) AND the insider is an officer buying at a price below the 52-week high** - #48/KILLED (later) covers buybacks + insiders, #4/KILLED covers 52-week high.

Let me try to find a genuinely novel combination. Looking at the data:

We have **sentiment_features** table with daily aggregates: n_polar, n_all, mean_score, pos, neg, hedged, ver. This is daily news sentiment.

We have **news** table with individual articles.

We have **stocktwits_sentiment** with bullish/bearish/untagged/total - but only 2026-07-11 to now (very recent).

We have **insider_trades** with code (A=ward, S=sale, P=purchase, F=tax, M=option-exercise), title, insider name.

We have **bars** with daily data.

We have **fundamentals** with EPS, Revenues, SharesOutstanding, EntityPublicFloat, CIK, LatestFilingDate - but fetched_at only recent.

We have **macro_series** with FRED data.

We have **inst_holdings** with 13F data (quarterly, 45-day lag).

What about: **Insider open-market purchases (code='P') by officers (title contains 'CEO' or 'CFO') when the 252-day news sentiment volatility is at a 3-year low (extreme calm) AND the stock's 252-day realized volatility is also at a 3-year low (dual compression) AND the company's revenue per share (Revenues/SharesOutstanding) has accelerated for 2+ consecutive quarters (using fundamentals with fetched_at lagged appropriately)**.

But #4/KILLED: "Dual compression of price volatility and news sentiment volatility during accelerating fun..." - this is very close! Dual compression + accelerating fundamentals. And #56/KILLED: "Volatility compression indicates a liquidity vacuum where informed insiders accumulate bef..." - vol compression + insiders. #45/KILLED (later): "Informed insiders accumulate when their company's fundamentals accelerate persistently bu..." - fundamentals accelerate + insiders.

So this combination is covered.

What about: **Insider purchases when the stock has had ZERO news coverage for 20+ consecutive trading days (attention drought) AND the company's fundamentals are improving (revenue accelerating) AND insiders buy** - #26/KILLED: "Insiders buying after a prolonged news-sentiment drought signals private knowledge of an u..." - news-sentiment drought + insiders. #24/KILLED (later): "On days with zero professional news coverage, attention-constrained investors completely n..." - zero coverage. #10/KILLED (later): "In chronically neglected stocks (persistently low institutional ownership, minimal news co..." - neglected + low news. #12/KILLED (later): "Attention-constrained investors systematically neglect low-news-coverage stocks, allowing..." - low news coverage. #51/KILLED (later): "Attention-constrained investors overreact to volatile news narratives while ignoring quiet..." - quiet stocks.

So "news drought + insiders" is covered by #26/KILLED.

What about: **Insider purchases when the stock's price has declined for 5+ consecutive days on increasing volume (capitulation) AND news sentiment is improving AND insiders buy** - #27/KILLED: "Capitulation selling creates a transient liquidity vacuum (long lower wick on high volume)" - capitulation. #41/KILLED: "Informed liquidity provision during transient liquidity vacuums — when a stock's daily rang..." - liquidity vacuums. #37/KILLED: "Insiders provide informed liquidity during transient single-stock volatility spikes (wide..." - volatility spikes + insiders. #62/KILLED (later): "Officers with proven track records provide informed liquidity during transient single-stoc..." - officers + track record + volatility. #65/KILLED (later): "Insiders provide informed liquidity during stock-specific retail capitulation — when quart..." - retail capitulation + insiders. #58/KILLED (later): "Insiders accumulate during transient sentiment downturns that precede fundamental inflecti..." - sentiment downturns + insiders.

So capitulation + insiders covered.

What about: **Insider purchases when the company has a new 13F holder taking a >5% stake (revealed with 45-day lag) AND the stock price has been flat AND insiders buy** - #68/KILLED (later): "A new institutional holder taking a >5% stake (revealed in 13F with 45-day lag) represents..." - new 13F holder >5%. #71/KILLED (later): "Institutions are selling (visible in prior 13F), but multiple insiders buy on clustered tr..." - institutions selling + multiple insiders. #30/KILLED (later): "Institutions accumulate gradually (visible in 13F with 45-day lag) while insiders confirm..." - 13F accumulation + insiders.

So 13F + insiders covered.

What about: **Insider purchases when the stock's short volume ratio is high but the short volume is DECLINING for 5+ days** - short volume only 10 weeks.

What about: **Insider purchases when the stock's options implied volatility rank is high** - no options.

What about: **Insider purchases when the stock's borrow fee is high** - no borrow data.

What about: **Insider purchases when the company announces a dividend increase** - no dividend data.

What about: **Insider purchases when the stock is added to a major index** - no index data.

What about: **Insider purchases when the company has a stock split** - no split data.

What about: **Insider purchases when the company has a spin-off** - no spin-off data.

What about: **Insider purchases when the company's CEO has recently changed** - no CEO change data.

What about: **Insider purchases when the company has a new product launch** - no product data.

OK, I need to think of something that uses the data in a way not covered. Let me look at the **title** field in insider_trades. It has the insider's title. We can distinguish CEO, CFO, COO, Director, 10% Owner, etc.

Many killed hypotheses use "officers (CEO/CFO)" specifically.

What about: **Insider purchases by DIRECTORS (not officers, not 10% owners) when they buy in clusters (multiple directors within 5 trading days) AND the stock is at a 52-week low** - #64/KILLED: "When multiple insiders execute open-market purchases on the identical trade date, they are..." - same date. #6/KILLED (later): "Insiders who buy on trade dates that cluster in a narrow window, but disclosures are sta..." - cluster in narrow window. #71/KILLED (later): "Institutions are selling (visible in prior 13F), but multiple insiders buy on clustered tr..." - multiple insiders clustered. But not specifically directors at 52-week low.

#6/KILLED: "Insiders buying at 52-week lows while fundamentals remain positive..." - 52-week low + fundamentals.

What about: **Insider purchases by 10% owners when they increase their stake by >1% of shares outstanding in a single quarter** - not explicitly covered.

What about: **Insider purchases when the stock's price is below its 200-day MA but above its 50-day MA (golden cross forming) AND insiders buy** - #28/KILLED covers 50/200 MA crossover.

What about: **Insider purchases when the stock's RSI(14) < 30 (oversold) AND news sentiment is improving AND the insider is an officer** - #48/KILLED: "Insiders with urgent information trade and disclose quickly. The market underreacts to the..." wait #48/KILLED (earlier): "Insiders purchase when their company's stock has experienced a sustained decline in news c..." no, #48/KILLED (earlier) is "Insiders purchase when their company's stock has experienced a sustained decline in news c..." and #48/KILLED (later): "Companies actively reducing share count via buybacks while insiders buy at trade dates dur..."

Wait, let me check #48/KILLED in the first list: "Insiders purchase when their company's stock has experienced a sustained decline in news c..." - decline in news coverage.

And #48/KILLED in the second list: "Companies actively reducing share count via buybacks while insiders buy at trade dates dur..." - buybacks + insiders.

What about #9/KILLED in first list: "Insider open-market purchases disclosed during periods of unusual retail pessimism signal..." - retail pessimism.

#9/KILLED in second list: "An insider breaking a multi-year personal buying dormancy in a stock with a stable public..." - buying dormancy.

Let me think about **disclosure delay**. The insider_trades table has both tx_ts (trade date) and filed_ts (filing date). The market only knows at filed_ts. Many killed hypotheses use this:
#38/KILLED: "Insiders act on private information at the trade date; the market underreacts to the information"
#44/KILLED: "Insiders with urgent information minimize disclosure delay; the market underreacts to fa..."
#48/KILLED (first list): "Insiders with urgent information trade and disclose quickly. The market underreacts to the..."
#12/KILLED (later): "Insiders who buy on trade dates that cluster in a narrow window, but disclosures are sta..."
#44/KILLED (later): "Insiders with urgent information minimize disclosure delay; the market underreacts to fa..."
#47/KILLED (later): "Insiders with urgent information trade and disclose quickly. The market underreacts to the..."

So disclosure delay/urgency is well covered.

What about: **Insider purchases when the disclosure delay is LONG (not urgent) AND the stock price has not moved much since the trade date** - opposite of urgency. Not covered explicitly.

But the hypothesis should have a clear economic mechanism. Long disclosure delay might indicate non-urgent, routine buying, which could be less informative.

What about: **Insider purchases when the stock's volume is extremely low (bottom decile) AND the bid-ask spread is wide** - no intraday spread data.

What about: **Insider purchases when the stock has a high "short interest ratio" (days to cover)** - short volume only 10 weeks, no shares outstanding for ratio.

What about: **Insider purchases when the company's free cash flow yield is high** - no cash flow data.

What about: **Insider purchases when the company's revenue growth > 20% YoY AND stock is down > 20% from high** - #59/KILLED covers fundamentals improve + stock down.

Let me try to construct a hypothesis that uses **multiple data sources in a novel way**:

**Hypothesis: "Informed Accumulation During Quiet Fundamentals Improvement"**

Mechanism: When a company's fundamentals (revenue per share) are improving steadily but quietly (low news sentiment volatility, low news volume), and officers (CEO/CFO) make open-market purchases, the market underreacts because attention-constrained investors overlook quiet fundamental improvement.

Entry conditions:
1. Officer (CEO/CFO) open-market purchase (code='P', title contains 'CEO' or 'CFO')
2. News sentiment volatility (252-day std of daily mean_score) is in bottom quartile of its 3-year history
3. News volume (252-day avg of n_all) is in bottom quartile of its 3-year history
4. Revenue per share (Revenues/SharesOutstanding) has increased for 3+ consecutive quarters (using fundamentals, with fetched_at lagged by 45 days to avoid lookahead)
5. Stock price is below its 252-day high (not at highs)
6. Horizon: 21 trading days

Abstention: If any condition not met, or if fewer than 3 qualifying officers in past quarter.

Precision claim: >= 65% (since base rate for 21-day forward return > 0 is ~50-55%)

But wait, #45/KILLED (later): "Informed insiders accumulate when their company's fundamentals accelerate persistently bu..." - fundamentals accelerate + insiders.
#51/KILLED (later): "Attention-constrained investors overreact to volatile news narratives while ignoring quiet..." - quiet fundamentals.
#4/KILLED: "Dual compression of price volatility and news sentiment volatility during accelerating fun..." - dual compression + accelerating fundamentals.

So "quiet fundamentals improvement + insiders" is covered by #45/KILLED and #51/KILLED.

What about: **Insider purchases when the stock has had a large increase in 13F institutional ownership (lagged 45 days) BUT the stock price has declined, AND officers buy** - #30/KILLED (later): "Institutions accumulate gradually (visible in 13F with 45-day lag) while insiders confirm..." - 13F accumulation + insiders. #71/KILLED (later): "Institutions are selling (visible in prior 13F), but multiple insiders buy on clustered tr..." - institutions selling + insiders buying.

What about: **Insider purchases when the company's public float has been stable (no dilution) for 12+ quarters AND revenue is growing AND officers buy** - #39/KILLED, #60/KILLED, #46/KILLED, #11/KILLED (later) cover non-diluting + fundamentals + officers.

What about: **Insider purchases when the stock's correlation with oil prices (or other macro) has broken down** - macro_series has many series, could compute rolling correlation.

But no sector data to know which stocks are oil-sensitive.

What about: **Insider purchases when the yield curve (10Y-2Y) is inverted AND the stock is a financial** - no sector data.

What about: **Insider purchases when the VIX is above its 200-day MA (fear) AND the stock is not a financial** - #12/KILLED covers VIX term structure backwardation.

Let me think about **StockTwits** again. The stocktwits_sentiment table has data from 2026-07-11 to 2026-08-16 (only ~1 month). Too short for 21-day horizon with 20% holdout. The schema says "stocktwits_sentiment(symbol_id, ts, bullish, bearish, untagged, total) -- 81,174 rows, 75 symbols, 2026-07-11..2026-08-16." So only 75 symbols, 1 month. INSUFFICIENT for our purposes.

**sentiment_features** has 42,105 rows, 699 symbols, 2012-04-17..2026-08-17. This is daily news sentiment aggregates. Good history.

**news** has 432,701 rows, 825 symbols, 2012-04-17..2026-08-15. Individual articles.

**insider_trades** has 8,211 rows, 627 symbols, 2008-03-21..2026-08-14. Good history.

**bars** has 15M rows, 2,947 symbols, 2018-07-26..2026-08-16. Good history.

**fundamentals** has 5,959 rows, 852 symbols, fetched_at