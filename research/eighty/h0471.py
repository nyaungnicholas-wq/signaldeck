Let me carefully analyze the task. I need to write a Python script that tests a specific hypothesis using the database schema provided. The hypothesis must be in exactly 6 labeled lines: MECHANISM, HORIZON, UNIVERSE, ENTRY, ABSTAIN, CLAIM.

First, I need to propose ONE new hypothesis that can be tested with the actual data available. Let me review what data is available and what hypotheses have been killed.

Available data with long history:
- Daily bars: 2018-07 to now, 1,777 symbols
- Insider transactions: 2008-03 to 2026-07, 609 symbols (with both trade and disclosure dates)
- News sentiment: 2012-04 to now, ~700 symbols
- 13F institutional holdings: quarterly, long history
- Fundamentals: key/value (EPS, Revenues, SharesOutstanding, EntityPublicFloat, CIK, LatestFilingDate)
- FRED macro series: long history
- StockTwits: 41,670 rows (need to check span)

Data with SHORT history (likely insufficient for 20% holdout + observation floor):
- SEC filings: ONLY 2026-02 to now
- Short volume: ONLY 2026-05-20 to 2026-07-31 (10 weeks)
- Hourly bars: from 2025-06
- Minute bars: from 2026-06
- tv_ratings: ONLY 2026-07-11 to now
- regime_outcomes: nothing resolved yet

Killed hypotheses cover many combinations. I need something novel that hasn't been tested.

Let me think about what hasn't been explicitly killed:

Looking at the killed list, many involve:
- Insider purchases + various conditions (news sentiment, drawdowns, disclosure delays, yield curve, etc.)
- Institutional ownership changes + various conditions
- Public float changes + various conditions
- News sentiment extremes + various conditions
- StockTwits sentiment + various conditions
- Macro indicators (yield curve, VIX, unemployment, credit spreads) + various conditions
- Volume/price patterns
- Earnings-related signals

What about: **Insider buying in stocks with HIGH and RISING institutional ownership concentration (few large holders getting larger)**? This would be institutions concentrating their bets while insiders also buy.

Or: **Insider buying when institutional ownership is LOW but a NEW large institution enters** (new 13F filer)?

Or: **Insider buying in stocks with declining share count (buybacks) AND rising EPS** - but #59 and #65 killed similar.

What about: **Insider buying in stocks where the insider is a NEW officer/director (first purchase after appointment)**? #29 killed "first recorded open-market purchase".

What about: **Insider buying in stocks with high short interest relative to float** - but no long short interest data.

What about: **News sentiment improvement from extremely negative to less negative, combined with insider buying, in stocks with high institutional ownership**? Multiple killed hypotheses cover insider buying + negative news sentiment improving.

Let me think about **Form 144 (intent to sell) NOT followed by actual insider selling** - insiders file intent but don't sell, signaling confidence. #22 killed Form 144 filing as signal of near-term selling.

What about **insider exercise-and-hold (code M + not selling)** vs exercise-and-sell? #5 killed exercise-and-hold.

What about **clustered insider buying by DIFFERENT TYPES of insiders (CEO + CFO + director)**? #1, #24, #60 killed clustered insider purchases.

What about **insider buying in stocks with low analyst coverage** - no analyst data.

What about **insider buying in stocks with high retail ownership (low institutional)**? #3 killed low institutional ownership + macro + news.

What about **insider buying in stocks with HIGH institutional ownership**? Not explicitly killed.

Let me check: #38 "When a company's 13F filing reveals a large increase in institutional ownership and an ins..." - cut off but likely insider buying too.

What about **institutional accumulation (13F increase) WITHOUT insider buying, in stocks with improving fundamentals**? #31, #32, #40, #43, #47, #48, #65 killed various institutional accumulation combos.

What about **insider buying in stocks where institutional ownership is DECLINING but fundamentals are IMPROVING**? #52 killed "insiders buying while institutions sell".

What about the **opposite: institutional accumulation while insiders sell**? This would be institutions seeing value that insiders are ignoring (or insiders diversifying). #2 is insider sales + institutional decrease. #38 is institutional increase + insider... (probably buying). So institutional increase + insider selling might be novel.

But insider selling is noisy. Let me think of a cleaner signal.

What about **insider buying in stocks with high and rising short interest** - no long short data.

What about **insider buying in stocks with low volatility (idiosyncratic)**? #8 killed low-vol momentum.

What about **insider buying in stocks with high idiosyncratic volatility**? Not tested.

What about **news sentiment momentum (rate of change) + price momentum divergence**? Not explicitly tested.

What about **insider buying when the stock is at a multi-year low but news sentiment is improving**? #47 killed "clustered open-market insider purchases disclosed after a large drawdown and during ext..." - similar.

What about **insider buying in stocks with high earnings yield (E/P) relative to sector**? #19, #42 killed earnings yield vs Treasury.

What about **insider buying in stocks with high FCF yield** - no FCF data.

What about **insider buying in stocks with declining shares outstanding (buybacks) AND rising institutional ownership**? #59, #65, #71 killed buyback combos.

Let me think about **macro regime + insider behavior in specific sectors**. Many killed.

What about **insider buying in financial stocks when yield curve steepens**? #29, #31, #51 killed yield curve + insider.

What about **insider buying in cyclical stocks when ISM manufacturing improves**? #5, #21 killed.

What about **insider buying when credit spreads narrow**? #13, #23, #28 killed credit spread spikes (widening).

What about **insider buying when VIX is low (complacency)**? #4, #12, #20 killed high VIX + insider.

What about **insider buying when unemployment claims are falling**? #10, #19 killed rising/falling claims + insider.

What about **13F filing timing - late filers vs early filers**? #11 killed fast filers.

What about **institutional ownership concentration (Herfindahl) increasing + insider buying**? Not explicitly tested.

What about **new 13F positions (not additions) by top-tier institutions + insider buying**? Not explicitly tested.

What about **insider buying in stocks where a known "smart money" institution (e.g., Berkshire, Renaissance) takes a new position**? Could be tested if institution names are in 13F data.

The 13F table has: cik, manager, period, symbol_id, cusip, name, value, shares. So manager names are available.

But this might be too specific and data-limited.

Let me think about **news sentiment by topic** - not in data.

What about **the interaction between news sentiment and StockTwits sentiment at different horizons** - killed.

What about **insider buying in stocks with low StockTwits message volume (low retail attention)**? Not explicitly tested. #4, #5 killed insider buying + extreme StockTwits bearish/bullish.

What about **insider buying in stocks with HIGH StockTwits message volume but NEUTRAL sentiment**? Not tested.

What about **insider selling (not buying) when StockTwits is extremely bullish**? #10, #20, #21 killed extreme bullish as contrarian, but not with insider selling.

#37 killed "insiders sell into periods of extreme positive news sentiment".

What about **insider selling when StockTwits is extremely bullish AND news sentiment is extremely positive**? Double extreme.

But insider selling is noisy.

Let me think about **fundamental momentum + price momentum + insider signal**.

#3, #12, #13, #28, #40, #53, #69, #70 killed fundamental momentum variants.

What about **revenue acceleration + insider buying + institutional accumulation**? #58 killed insider buying + negative news + revenue growth. #65 killed institutional accumulation + buybacks.

What about **EPS acceleration + insider buying + institutional accumulation**? #3 killed EPS growth + macro + news.

What about **insider buying in stocks with consistent quarterly revenue growth (4+ quarters) AND institutional accumulation**? #69 killed consistent revenue growth. #43 killed institutional ownership increase + sustained extreme... (cut off). #47 killed clustered insider purchases after drawdown during extreme...

Let me look at #43: "A quarterly increase in institutional ownership (per 13F) combined with sustained extreme..." - probably news sentiment.

#47: "Clustered open-market insider purchases disclosed after a large drawdown and during ext..." - probably extreme news sentiment.

What about **insider buying in stocks with 4+ quarters of revenue growth AND 4+ quarters of EPS growth AND institutional accumulation**? Triple fundamental confirmation.

But many fundamental combos killed.

Let me think about **the disclosure delay of insider trades as a signal of conviction, but for SELLING not buying**. #17, #19, #24, #42, #44, #52 killed disclosure delay for purchases. What about for sales?

#22 killed Form 144 (intent to sell) as signal.

What about **insider sales with SHORT disclosure delays (urgent selling)**? Not explicitly tested. #42 killed short delay for purchases as urgent buying. Short delay for sales could be urgent selling.

Mechanism: Insiders who sell quickly after the trade (short disclosure delay) may have urgent liquidity needs or negative information, while those who delay may be routine diversification.

But #42 says "Insider purchases with unusually short disclosure delays signal urgent private information" - killed. The reverse for sales might be novel.

However, the hypothesis format requires predicting a direction (up/down). Insider selling predicting down? But insider selling is notoriously noisy.

Let me think about **institutional accumulation in stocks with HIGH short interest** - no short data.

What about **stocks entering/exiting the S&P 500** - no index data.

What about **insider buying in stocks with high short interest (from short volume, but only 10 weeks)** - insufficient.

Given the constraints, I need a hypothesis that:
1. Uses long-span data (daily bars, insider, news, 13F, fundamentals, FRED)
2. Hasn't been explicitly killed
3. Can be precisely defined with ENTRY/ABSTAIN
4. Targets a precision band

Let me consider: **Institutional accumulation (13F ownership increase) in stocks with persistently negative news sentiment WHERE the stock price has NOT declined (price resilience)** - this suggests informed accumulation absorbing supply.

But #43 killed "quarterly increase in institutional ownership combined with sustained extreme..." and #47 killed "clustered insider purchases disclosed after large drawdown during extreme..."

What about **institutional accumulation in stocks with IMPROVING news sentiment (from negative to positive) AND price momentum**? #30 killed "sustained divergence between news sentiment and price". #29 killed "reversal from sustained negative to positive news sentiment".

What about **insider buying in stocks where institutional ownership is HIGH and STABLE (not changing), but fundamentals are accelerating**? Institutions holding while insiders buy and fundamentals improve.

#3 killed EPS growth + macro + news. #12 killed fundamental improvements. #53 killed revenue acceleration.

What about **insider buying in stocks with high institutional ownership concentration (top 5 holders own >50%) AND those top holders are INCREASING positions**? Concentrated smart money + insider alignment.

Not explicitly killed.

Let me check the 13F data: we have cik, manager, period, symbol_id, cusip, name, value, shares. We can compute concentration.

But this might be complex to implement in SQL.

Let me think simpler: **Insider open-market purchases (code P) in stocks where the 13F institutional ownership has increased for 2+ consecutive quarters AND news sentiment has been improving for 3+ months**.

#43: institutional ownership increase + sustained extreme... (killed)
#47: clustered insider purchases after drawdown during extreme... (killed)
#58: insiders buying during sustained negative news while revenues grow (killed)
#31: quarterly 13F institutional ownership increase reveals informed accumulation (killed)
#32: quarterly 13F increase + concurrent open-market insider... (killed)

#32 is very close: "A quarterly 13F institutional ownership increase combined with a concurrent open-market insider..." - killed.

What about **TWO consecutive quarters of institutional ownership increase + insider buying**? #31, #32, #40, #43, #47, #48, #65 all touch on institutional increase + insider.

What about **institutional ownership increase + insider buying + POSITIVE news sentiment (not negative)**? Most killed hypotheses combine insider buying with NEGATIVE news sentiment (contrarian). What about CONFIRMATORY: institutions buying, insiders buying, news positive?

#38: "When a company's 13F filing reveals a large increase in institutional ownership and an ins..." - cut off, but likely insider buying too. If it's institutional increase + insider buying, killed regardless of news.

What about **institutional ownership increase + NO insider selling (insiders not selling)**? Not the same as insider buying.

What about **insider buying in stocks where institutional ownership is HIGH (>50%) AND the stock has low volatility**? #8 killed low-vol momentum.

Let me think about **macro: term spread + insider buying in RATE-SENSITIVE sectors (financials, REITs, utilities)**. #29, #31, #51 killed yield curve + insider generally, not sector-specific.

But we don't have sector data in the schema. symbols table has market (stocks/crypto) but not sector.

What about **insider buying in crypto vs stocks**? Market is in symbols.

But crypto data may be shorter.

What about **insider buying in stocks with high public float (EntityPublicFloat) AND institutional accumulation**? #14, #15, #26, #27, #45, #50, #55, #71 killed public float changes.

What about **insider buying in stocks with STABLE public float (no buybacks/dilution) AND institutional accumulation AND improving fundamentals**? Stable float as quality signal.

#55 killed sustained quarterly INCREASE in public float (dilution). #71 killed sustained DECREASE (buybacks). Stable float not explicitly tested.

But this is getting complicated.

Let me think about **news sentiment volatility (standard deviation) as a signal**. High sentiment volatility = uncertainty. Low sentiment volatility = consensus.

Not explicitly tested.

What about **insider buying when news sentiment volatility is LOW (consensus) but sentiment is NEGATIVE**? Consensus negative view, but insiders disagree.

#17, #18, #46 killed insider buying during extreme negative news.

What about **insider buying when news sentiment volatility is HIGH (disagreement) AND sentiment is NEGATIVE**? High disagreement, insiders take a side.

Not tested.

But computing rolling sentiment volatility requires window functions, doable in SQL.

Let me think about **StockTwits sentiment divergence from news sentiment** - #18, #26 killed.

What about **StockTwits message volume (attention) + news sentiment**? #1 killed low news coverage. #39 killed news drought then reappearance.

What about **high StockTwits volume + extreme news sentiment + insider buying**? Attention + sentiment + insider.

#4 killed extreme negative news + insider buying (but not StockTwits volume).

Let me try to formulate a hypothesis that hasn't been killed:

**MECHANISM**: When institutional owners concentrate their holdings (rising Herfindahl index of 13F ownership) while insiders simultaneously make open-market purchases, it signals high-conviction alignment between informed insiders and sophisticated institutions, which diffuses slowly into prices.

**HORIZON**: 63 trading days (~3 months)

**UNIVERSE**: All active US stocks with at least 4 quarters of 13F history and at least one insider open-market purchase in the prior quarter, excluding delisted symbols.

**ENTRY**: Issue a long call when (a) the 13F Herfindahl index of institutional ownership has increased for two consecutive quarters, (b) at least one insider open-market purchase (code P) was disclosed in the most recent quarter, and (c) the stock's 63-day price trend is positive.

**ABSTAIN**: No call if Herfindahl decreased in either of the last two quarters, no insider open-market purchases in the prior quarter, 63-day trend is negative, or fewer than 3 institutional holders reported in the latest 13F.

**CLAIM**: Precision in [0.58, 0.68] for the predicted up direction.

But this uses Herfindahl which requires computing sum of squares of ownership percentages. Doable in SQL but complex.

Also, #32 killed "quarterly 13F institutional ownership increase combined with a concurrent open-market insider..." - very close.

What about **insider buying in stocks where a NEW institutional holder (not previously in 13F) takes a position >1% of shares outstanding**? New smart money + insider.

#11 killed fast-filing institutions with new positions. But not combined with insider buying.

#31 killed quarterly 13F increase revealing informed accumulation.

#32 killed quarterly 13F increase + concurrent insider.

New holder is a subset of increase.

Let me think about **insider buying in stocks with high short interest (from short volume) but only 10 weeks** - no.

What about **insider buying in stocks with low institutional ownership (<20%) BUT a new large institution enters**? Contrarian: retail-dominated but smart money entering + insiders buying.

#3 killed low institutional ownership + macro + news.

What about **insider buying in stocks with HIGH institutional ownership (>60%) AND those institutions are INCREASING**? Confirmatory.

#38 likely killed institutional increase + insider.

Let me check the killed list more carefully for #38: "When a company's 13F filing reveals a large increase in institutional ownership and an ins..." - the "ins..." could be "insider buying" or "insider selling". If it's "insider buying", then killed. If "insider selling", then not killed.

Given the pattern, it's likely "insider buying" since most hypotheses combine institutional increase with insider buying.

What about **institutional increase + insider SELLING**? That would be divergence. #2 is insider sales + institutional decrease. #52 is insider buying + institutional selling. So institutional increase + insider selling might be novel.

Mechanism: Institutions accumulate based on research; insiders sell for diversification/liquidity. When institutions aggressively buy while insiders sell, institutions may have a more informed view.

But insider selling is weak signal.

What about **insider buying in stocks where institutional ownership is HIGH and the top holders are LONG-TERM holders (low turnover)**? Stable smart money + insider alignment.

Not explicitly tested.

But requires tracking holder tenure.

Let me think about **fundamentals: SharesOutstanding decreasing (buybacks) + EPS increasing + insider buying**. #59 killed buybacks + insider buying. #65 killed institutional accumulation + buybacks. #71 killed sustained float reduction.

What about **SharesOutstanding decreasing + Revenue increasing + insider buying**? #58 killed insider buying + negative news + revenue growth. #53 killed revenue acceleration. #69 killed consistent revenue growth.

What about **EntityPublicFloat decreasing (not just SharesOutstanding) + insider buying + institutional accumulation**? #14, #15, #26, #27, #45, #50, #55, #71 killed public float changes.

What about **insider buying in stocks with high earnings yield (EPS/price) relative to 10Y Treasury**? #19, #42 killed.

What about **insider buying in stocks with high earnings yield relative to SECTOR median**? No sector data.

What about **insider buying in stocks with high FCF yield**? No FCF.

Let me think about **news sentiment: sustained improvement over 6 months + insider buying + price momentum**. #7, #10, #13, #18, #42, #46, #47, #58 killed insider buying + improving/negative news.

What about **news sentiment: sustained improvement over 6 months + institutional accumulation + NO insider selling**? Institutions and news agree, insiders not disagreeing.

#30 killed divergence between news sentiment and price. #29 killed reversal from negative to positive news.

What about **news sentiment: sustained POSITIVE (not improving from negative) + institutional accumulation + insider buying**? All three positive.

#38 likely killed institutional increase + insider buying. #43 killed institutional increase + sustained extreme... (probably news). #47 killed insider cluster + drawdown + extreme...

What about **news sentiment: sustained POSITIVE + institutional accumulation + insider buying + price momentum**? Too many conditions, low frequency.

Let me think about **macro: yield curve steepening + insider buying in SMALL CAPS** (more rate-sensitive). #29, #31, #51 killed yield curve + insider generally.

But no market cap data directly. Could use public float or price * shares.

What about **macro: credit spread narrowing + insider buying in HIGH YIELD issuers**? #13, #23, #28 killed credit spread widening.

But we don't have credit ratings or high yield issuer list.

What about **macro: VIX declining (complacency returning) + insider buying**? #4, #12, #20 killed high VIX + insider.

What about **macro: unemployment claims falling + insider buying in CYCLICALS**? #10, #19 killed unemployment + insider. #5, #21 killed ISM + macro.

No sector data.

Let me think about **the "quiet insider" - insiders who rarely trade but suddenly buy**. #73 killed insiders with verified track record. #29 killed first purchase.

What about **insiders who have NOT traded in 2+ years suddenly making an open-market purchase**? Long dormancy then activity.

Not explicitly tested. #29 is first purchase ever. #73 is track record. Dormancy is different.

Mechanism: An insider who breaks a long silence with an open-market purchase sends a costly signal of conviction, as they have no history of frequent trading to obscure the signal.

HORIZON: 63 days

UNIVERSE: Active US stocks with insider transaction history >= 3 years.

ENTRY: Issue a long call when an insider (officer/director) makes an open-market purchase (code P) after at least 2 years (730 days) with no open-market transactions (codes P or S) by that insider, disclosed within the last 5 trading days.

ABSTAIN: No call if the insider has traded in the last 2 years, the purchase is not open-market (code A, M, F), disclosure delay > 5 trading days, or the stock's 20-day trend is down > 20%.

CLAIM: Precision in [0.60, 0.70].

This uses insider transaction history (2008-2026, long span), disclosure date (filed_ts), trade date (tx_ts), and price trend from daily bars.

Check killed list: #29 "An insider's first recorded open-market purchase is a costly participant-behaviour signal..." - killed. But this is FIRST purchase ever. My hypothesis is about DORMANCY (no trades for 2+ years), not first ever. An insider could have traded 3 years ago, then silent, then buys. Different from first purchase.

#73 "Insiders with a verified historical track record of profitable open-market purchases posse..." - killed. This is about track record, not dormancy.

#19 "CEO/CFO open-market purchases disclosed after a 20%+ drawdown..." - killed. Specific to drawdown and CEO/CFO.

#5 "An insider who exercises vested options and retains the exercised shares is making a costl..." - killed. Exercise-and-hold.

#42 "Insider purchases with unusually short disclosure delays signal urgent private information" - killed. Short delay.

#17, #24, #44, #52 - disclosure delay for purchases.

So "dormant insider breaking silence" might be novel.

But need to check: "first recorded open-market purchase" vs "first in 2+ years". The killed #29 says "first recorded", which implies ever. Dormancy is different.

However, the mechanism is similar: costly signal. The judge might consider it too similar.

Let me think of something more distinct.

What about **insider buying in stocks with HIGH short interest (proxied by... nothing)**.

What about **institutional accumulation in stocks with LOW short interest** - no.

What about **news sentiment momentum (3-month slope) + price momentum (3-month slope) DIVERGENCE + insider buying**? When news improves but price doesn't (or vice versa), insiders weigh in.

#30 killed "sustained divergence between news sentiment and price". #29 killed "reversal from negative to positive news".

What about **news sentiment 3-month slope POSITIVE + price 3-month slope NEGATIVE + insider buying**? News improving, price falling, insiders buy the dip.

#47 killed "clustered insider purchases disclosed after a large drawdown and during ext..." - drawdown = price falling, extreme... probably news sentiment.

#18 killed "insider open-market purchases disclosed after a sustained period of negative news sentiment..." - negative news, not improving.

#7 killed "insider open-market purchases disclosed during improving (but still negative) news sentiment..." - killed.

#10 killed "insider open-market purchases during a period of sustained improvement in news sentiment..." - killed.

#13 killed "insider open-market purchases disclosed after a period of sustained negative news sentiment..." - killed.

#42 killed "insider open-market buys are private-information trades whose information becomes public o..." - killed.

#46 killed "insiders purchasing open-market shares when the company's public float has declined for tw..." - killed.

#58 killed "insiders buying during sustained negative news sentiment while quarterly revenues grow sig..." - killed.

So insider buying + improving news sentiment has been killed multiple times (#7, #10, #13, #18, #42, #46, #58).

What about **insider buying + improving news sentiment + institutional accumulation**? #32 killed institutional increase + concurrent insider. #43 killed institutional increase + sustained extreme... #47 killed insider cluster + drawdown + extreme...

What about **institutional accumulation + improving news sentiment + NO insider selling**? Institutions and news agree, insiders not selling.

#31 killed institutional increase reveals informed accumulation. #40 killed institutional increase + revenue growth. #43 killed institutional increase + sustained extreme... #48 killed relative strength + institutional.

What about **institutional accumulation + improving news sentiment + price momentum**? #48 killed relative strength persistence + institutional.

Let me think about **StockTwits sentiment + news sentiment + insider buying** - #18, #26 killed divergence.

What about **StockTwits sentiment EXTREME (bullish or bearish) + news sentiment OPPOSITE + insider buying aligning with news**? Retail vs news disagreement, insiders side with news.

#18: "Bad news diffuses slowly; when the news flow is extremely negative but StockTwits retail s..." - killed. This is negative news + retail bullish (divergence). Insider buying would align with retail (contrarian to news) or news?

#26: "Miller's divergence-of-opinion channel — when retail attention on StockTwits is unusually..." - killed.

What about **StockTwits bearish extreme + news positive + insider buying**? Retail bearish, news positive, insiders buy.

Not explicitly killed.

But StockTwits data span? 41,670 rows. Need to check date range. Not specified in schema. Could be short.

The schema says: "stocktwits_sentiment(symbol_id, ts, bullish, bearish, untagged, total) -- 41,670 rows." No date range given. Could be recent only.

News sentiment: 2012-04 to now.

Insider: 2008-03 to 2026-07.

13F: quarterly, long history.

Fundamentals: long history.

FRED: long history.

Daily bars: 2018-07 to now.

For a 20% holdout and sufficient observations, need several years of data.

StockTwits might be too short. Let me avoid it.

What about **FRED macro series + insider buying in specific industries** - no industry data.

What about **insider buying when the 10Y-2Y spread is INVERTED but STEEPENING (less inverted)**? #31 killed inverted yield curve + insider. #51 killed steepening yield curve + insider cluster.

What about **insider buying when the 10Y-2Y spread is POSITIVE and STEEPENING**? #29 killed positively sloped + insider. #51 killed steepening + insider cluster.

What about **insider buying when the 10Y yield is FALLING (rate cuts expected)**? Not explicitly tested.

#29: positively sloped (10Y>2Y) + insider.
#31: inverted (10Y<2Y) + insider.
#51: steepening (spread increasing) + insider cluster.

What about **10Y yield level + insider buying**? Not tested.

But macro + insider heavily tested.

Let me think about **13F: institutional ownership turnover (churn in holder list) + insider buying**. High turnover = disagreement among institutions. Low turnover = consensus. Insiders buying with low turnover = alignment with stable owners.

Not tested.

But complex to compute.

What about **13F: number of institutional holders INCREASING + insider buying**? Breadth of ownership expanding + insiders.

#31, #32, #40, #43, #47, #48, #65 killed institutional increase variants.

What about **13F: average holding size per institution INCREASING + insider buying**? Existing holders adding + insiders.

Similar to ownership increase.

What about **insider buying in stocks where the largest institutional holder INCREASES position significantly**? Lead holder conviction + insider.

#11 killed fast-filing institutions with new positions. Not exactly largest holder increasing.

What about **insider buying in stocks where a "smart money" institution (e.g., top quartile by historical returns) takes a new position**? Requires computing institution track record.

Too complex.

Let me think about **fundamentals: EPS acceleration + revenue acceleration + insider buying** - double acceleration.

#3 killed EPS growth. #53 killed revenue acceleration. #69 killed consistent revenue growth. #70 killed margin expansion. #12 killed fundamental improvements. #28 killed EPS growth + macro. #40 killed revenue growth + institutional.

Double acceleration (both EPS and revenue accelerating) not explicitly killed.

Mechanism: When both earnings and revenue growth accelerate simultaneously, it signals a fundamental inflection point that is underappreciated by the market; insider buying confirms management conviction.

HORIZON: 63 days

UNIVERSE: Active US stocks with at least 8 quarters of EPS and Revenue data.

ENTRY: Issue a long call when (a) trailing 4-quarter EPS growth rate > trailing 8-quarter EPS growth rate (acceleration), (b) trailing 4-quarter Revenue growth rate > trailing 8-quarter Revenue growth rate (acceleration), (c) at least one insider open-market purchase (code P) disclosed in the most recent quarter, and (d) the stock's 63-day price trend is positive.

ABSTAIN: No call if either growth rate is not accelerating, no insider open-market purchases in the prior quarter, 63-day trend is negative, or fewer than 2 quarters of positive EPS.

CLAIM: Precision in [0.58, 0.68].

But #53: "Revenue-growth acceleration is a slow-diffusing information source..." - killed.
#3: "Fundamental information diffuses slowly — when trailing four-quarter EPS growth is strongl..." - killed.
#69: "Consistent quarterly revenue growth signals fundamental strength..." - killed.
#70: "Sustained margin expansion..." - killed.
#12: "Attention-constrained investors underreact to strong fundamental improvements..." - killed.
#28: "Attention-constrained investors underreact to accelerating EPS growth..." - killed.
#40: "Attention-constrained institutional investors underreact to sustained revenue growth..." - killed.

Double acceleration might be considered covered by these.

What about **EPS acceleration + revenue DECeleration (divergence) + insider buying**? Earnings growing faster than revenue = margin expansion. #70 killed margin expansion.

What about **revenue acceleration + EPS DECeleration + insider buying**? Revenue growing but earnings not = investment phase. Insiders buying signals confidence in future payoff.

Not explicitly tested.

But #53 killed revenue acceleration. #70 killed margin expansion.

Let me think about **public float: STABLE (no change >2% for 4+ quarters) + insider buying + institutional accumulation**. Stable float = no dilution/buybacks, pure business focus.

#14, #15, #26, #27, #45, #50, #55, #71 killed float changes. Stable float not tested.

Mechanism: A stable public float over multiple quarters signals management is neither diluting nor leveraging the balance sheet for buybacks, focusing on operations; combined with insider buying and institutional accumulation, it indicates aligned conviction in the core business.

HORIZON: 63 days

UNIVERSE: Active US stocks with at least 4 quarters of EntityPublicFloat data.

ENTRY: Issue a long call when (a) EntityPublicFloat has changed by less than 2% in each of the last 4 quarters, (b) 13F institutional ownership increased in the latest quarter, (c) at least one insider open-market purchase (code P) disclosed in the latest quarter, and (d) 63-day price trend positive.

ABSTAIN: No call if float changed >2% in any of last 4 quarters, institutional ownership decreased, no insider open-market purchases, or 63-day trend negative.

CLAIM: Precision in [0.58, 0.68].

This uses fundamentals (EntityPublicFloat), 13F, insider, daily bars. All long-span data.

Check killed: #14 "sustained decline in public float over at least two quarters" - killed (decline).
#15 "shrinking public float combined with improving fundamentals" - killed (shrinking).
#26 "sustained quarterly decline in public float combined with institutional accumulation" - killed (decline).
#27 "shrinking public float (two consecutive quarterly declines...)" - killed.
#45 "public float drops sharply" - killed.
#50 "public float decreased by at least 10% year-over-year" - killed.
#55 "sustained quarterly increase in public float reflects persistent dilution" - killed (increase).
#71 "reduced its public float for three or more consecutive quarters" - killed (decline).

STABLE float (no significant change) is NOT tested. This could be novel.

The mechanism is different: