// The exposition this server serves.
//
// Everything here is a fixed string. That is a security property as much as an
// editorial one: the advisory tools have no dataset access at all, so their
// extraction risk is not "small", it is structurally zero — there is nothing
// behind them to extract. Anything that must be measured lives in the verdict
// and record tools, which go through the cache and the allowlist.
//
// Numbers appearing in this file are the MEASURED figures of record from
// SHIP_READINESS.md, PREREGISTRATION.md and internal/structregime's package
// documentation. They are quoted with their band, sample and caveat, never
// alone.
package mcp

// methodologyEntry is one topic's exposition.
type methodologyEntry struct {
	Title      string
	Summary    string
	WhyMatters string
	HowApplied string
	Prevents   string
	Related    []string
}

var methodologyContent = map[string]methodologyEntry{
	"matched_nulls": {
		Title: "Matched nulls — comparing a model against the right naive alternative",
		Summary: "A null is the answer you would give with no model at all. For a sticky state " +
			"the right null is PERSISTENCE — \"it continues\" — not a coin flip. Choosing 50% as the " +
			"null for a state that persists 83% of the time manufactures skill out of arithmetic.",
		WhyMatters: "Almost every impressive-looking accuracy on a regime question is the base rate " +
			"in disguise. The question a null answers is not \"is the model good\" but \"is the model " +
			"better than doing nothing\", and only the second one is worth money or attention.",
		HowApplied: "Every structural predictor here carries a frozen baseline field naming its own " +
			"null, and the registry publishes the persistence null measured on the SAME live window " +
			"beside every accuracy. The stricter of the two nulls drives any skill statement. For one " +
			"liquidity predictor the persistence null EQUALS the top-band claim, which is stated " +
			"rather than buried: that predictor's accuracy is real and its novelty is not.",
		Prevents: "Quoting a base rate as skill — the single most common way a research result is " +
			"wrong while every number in it is correct.",
		Related: []string{"conviction_banding", "walk_forward", "pre_registration"},
	},
	"non_overlapping_sampling": {
		Title: "Non-overlapping sampling and the independence unit",
		Summary: "If the forward window is 21 sessions, samples must step 21 sessions. Overlapping " +
			"windows share most of the same future, so they are not independent trials and counting " +
			"them as such inflates the sample size, narrows every interval, and turns noise into " +
			"significance.",
		WhyMatters: "Sample size enters every confidence interval under a square root, so pooling " +
			"correlated rows does not slightly overstate confidence — it overstates it by the square " +
			"root of the design effect, which here has measured as high as ~60x.",
		HowApplied: "One observation per symbol per UTC day is the independence unit, enforced in the " +
			"grading SQL rather than left to discipline. Validation samples step by the horizon, and " +
			"confidence intervals are quarter-block bootstrapped or day-clustered (Wilson on the " +
			"EFFECTIVE sample), so autocorrelated days cannot pretend to be evidence.",
		Prevents: "A health gate that retires and revives a model on nothing at all, because its " +
			"counts were pseudo-replicated.",
		Related: []string{"walk_forward", "matched_nulls"},
	},
	"conviction_banding": {
		Title: "Conviction banding — reporting discrimination instead of an average",
		Summary: "A single accuracy number for a model that answers easy and hard cases alike is an " +
			"average over a population nobody is a member of. Banding sorts calls by the model's own " +
			"stated conviction and reports accuracy WITHIN each band, so a consumer knows which calls " +
			"are reliable rather than what the fleet averages.",
		WhyMatters: "The average is usually the base rate. The band structure is the actual product: " +
			"the measured spread here is 72.9% at low conviction against 97.6% at very-high — 24.7 " +
			"percentage points, holding across 24 quarters on 54,969 independent observations from a " +
			"survivorship-clean universe. Knowing WHICH calls to trust is the useful thing.",
		HowApplied: "Every forecast is stamped at call time with the frozen accuracy for ITS band, so " +
			"later edits to a band table cannot move a claim retroactively. Nothing on this server " +
			"quotes an aggregate accuracy without the band table beside it.",
		Prevents: "Attributing population-average accuracy to an individual call, in both directions: " +
			"overselling the weak calls and underselling the strong ones.",
		Related: []string{"matched_nulls", "survivorship"},
	},
	"walk_forward": {
		Title: "Walk-forward validation and no-lookahead discipline",
		Summary: "Features computed at time t may use data up to and including t and nothing after. " +
			"Validation walks forward through time; it never fits on a period it then scores.",
		WhyMatters: "Lookahead is rarely deliberate and almost always fatal. A trailing median " +
			"computed over the whole sample, a label built from a series that was later revised, or a " +
			"fill assumed at a price nobody could have transacted at, each produce a beautiful curve " +
			"and no out-of-sample edge.",
		HowApplied: "Point-in-time fundamentals, explicit no-lookahead and fill-timing tests, labels " +
			"built against a TRAILING causal median rather than a full-sample one, and a prequential " +
			"live record in which a prediction is graded only against data that arrived after it.",
		Prevents: "The backtest that cannot be reproduced live — and, worse, the one nobody notices " +
			"cannot be, because it was never asked to run forward.",
		Related: []string{"non_overlapping_sampling", "pre_registration"},
	},
	"survivorship": {
		Title: "Survivorship contamination",
		Summary: "A universe of currently-tracked names silently excludes everything that died. " +
			"Studies of persistence are the most exposed: the strongest evidence against \"trends " +
			"persist\" is the trend that persisted all the way to a delisting and is therefore absent.",
		WhyMatters: "The bias is invisible in the output. Nothing about a survivorship-contaminated " +
			"result looks wrong; it just quietly answers a question about companies that made it.",
		HowApplied: "A survivorship-clean research universe with reconstructable point-in-time " +
			"membership, and a re-validation over EVERY symbol ever tracked including the graveyard. " +
			"The measured inflation was +1.0pp overall and the band structure was essentially " +
			"unchanged — dead names were very slightly harder, not dramatically easier. That the " +
			"correction was small is a finding; it was not assumed in advance.",
		Prevents: "A persistence claim that is really a claim about companies that persisted.",
		Related:  []string{"conviction_banding", "matched_nulls"},
	},
	"pre_registration": {
		Title: "Pre-registration and hash chaining",
		Summary: "Write down the claim, the question, the resolution rule, the null and the minimum " +
			"evidence gates BEFORE any of it can resolve, then hash the record into an append-only " +
			"chain so a later edit is detectable by recomputation rather than by trust.",
		WhyMatters: "Without it, the comparison after results arrive is against whatever the code says " +
			"at that time, which is a story rather than a measurement. Chaining turns \"we always said " +
			"that\" into something checkable.",
		HowApplied: "Six structural predictors were frozen twelve days before the first date any " +
			"forecast could resolve, one chained record per claim, plus the grading script itself " +
			"registered by content hash so an edited grader appends a visible amendment. Amendments " +
			"append; they never overwrite. Minimum-evidence gates were fixed in advance, so \"not " +
			"enough evidence yet\" is a pre-committed verdict rather than a retreat.",
		Prevents: "Moving the target after seeing the result, which is the failure mode that no " +
			"amount of statistical care elsewhere can repair.",
		Related: []string{"matched_nulls", "walk_forward", "conviction_banding"},
	},
}

// ── critique_research_design ────────────────────────────────────────────────

// designFinding is one failure mode this platform has actually hit, with the
// fix it adopted. The finding text is FIXED: the tool matches signals in a
// description and returns these, and never reflects the caller's own words
// back, so the parameter cannot become an echo channel.
type designFinding struct {
	Code     string
	Concern  string
	Why      string
	Fix      string
	Triggers []string
}

var designFindings = []designFinding{
	{
		Code:    "overlapping-windows",
		Concern: "Forward windows look like they overlap, so the observations are not independent.",
		Why: "Pooling overlapping windows here inflated the effective sample by roughly 60x and " +
			"produced confident verdicts on nothing. The independence unit adopted was one " +
			"observation per symbol per UTC day.",
		Fix: "Step the sample by the full horizon, or cluster the interval by day and evaluate at the " +
			"effective sample size rather than the row count.",
		Triggers: []string{"rolling", "overlap", "every day", "daily sample", "sliding window",
			"each session", "window of", "21-day", "21 day", "63-day", "moving window"},
	},
	{
		Code:    "unmatched-null",
		Concern: "The comparison baseline may be a coin flip rather than the persistence of the state being predicted.",
		Why: "For sticky states the naive \"it continues\" rule already scores far above 50% — for one " +
			"liquidity predictor it equals the model's own top-band claim. Measured against 50% the " +
			"model looks skilful; measured against persistence it adds nothing.",
		Fix: "State the null explicitly, measure it on the SAME window as the model, and publish both. " +
			"Let the stricter null drive any skill claim.",
		Triggers: []string{"50%", "coin", "random", "baseline", "chance", "null", "versus random",
			"better than guessing", "accuracy of"},
	},
	{
		Code:    "day0-conditioning",
		Concern: "The setup may condition on information from the same bar it is predicting within.",
		Why: "A gap-fill study here was pulled after exactly this: the criterion used the day's own " +
			"range, so the outcome was partially determined by the conditioning variable. The " +
			"result was excellent and meaningless.",
		Fix: "Freeze every conditioning variable at the prior close and re-run. If the effect " +
			"disappears, it was the conditioning.",
		Triggers: []string{"intraday", "same day", "gap", "open to close", "day 0", "day-0",
			"high of the day", "low of the day", "same bar", "session's range"},
	},
	{
		Code:    "population-average-attribution",
		Concern: "A single population accuracy may be attributed to individual calls.",
		Why: "The aggregate here (~83%) is the persistence base rate. The real result is the band " +
			"spread — 72.9% low versus 97.6% very-high — and quoting the average both oversells the " +
			"weak calls and hides the strong ones.",
		Fix: "Report accuracy within conviction bands with a sample size per band, and never quote " +
			"the aggregate on its own.",
		Triggers: []string{"overall accuracy", "average accuracy", "hit rate of", "my model is",
			"accurate", "% accuracy", "aggregate"},
	},
	{
		Code:    "survivorship",
		Concern: "The universe may consist of instruments that still exist today.",
		Why: "Persistence studies are the most exposed to this, because the counter-evidence " +
			"delisted. Re-running over the graveyard here cost 1.0pp overall — small, but the " +
			"smallness was a finding, not an assumption.",
		Fix: "Reconstruct point-in-time membership, include delisted names, and report the clean and " +
			"contaminated numbers side by side.",
		Triggers: []string{"s&p", "current universe", "top 500", "constituents", "index members",
			"stocks i track", "watchlist", "liquid names", "today's universe"},
	},
	{
		Code:    "lookahead",
		Concern: "A feature or label may use information unavailable at decision time.",
		Why: "Full-sample medians, revised fundamentals and assumed fills are the three that recur. " +
			"Each produces a clean backtest and no live edge.",
		Fix: "Compute every statistic from a trailing causal window, use point-in-time fundamentals, " +
			"and test fill timing explicitly.",
		Triggers: []string{"median of the", "z-score", "normalize", "standardi", "full sample",
			"whole period", "fundamental", "earnings data", "revised", "fill", "backtest"},
	},
	{
		Code:    "multiple-testing",
		Concern: "Many variants may be tried with only the winner reported.",
		Why: "Searching a wide space and reporting the best result reports the search, not the effect. " +
			"The defence adopted here was to freeze the claim set before any of it could resolve.",
		Fix: "Pre-register the hypothesis set, report how many were tested, and hold out a period the " +
			"search never touched.",
		Triggers: []string{"parameter", "optimi", "grid", "tuned", "best performing", "swept",
			"tried several", "variants", "hyperparameter"},
	},
	{
		Code:    "accuracy-is-not-return",
		Concern: "A hit rate may be treated as evidence that the position makes money.",
		Why: "Measured here, and inverted at the top band: the most accurate trend band (97%+) has a " +
			"NEGATIVE mean forward 21-day return, because high conviction means price is already far " +
			"from its average, and extended names mean-revert.",
		Fix: "Measure forward return by band alongside accuracy. Treat them as separate claims, " +
			"because they can point in opposite directions.",
		Triggers: []string{"profit", "return", "pnl", "p&l", "make money", "edge", "trade", "strategy",
			"sharpe", "backtested returns"},
	},
}

// ── validated findings ──────────────────────────────────────────────────────

type survivedFinding struct{ Name, Claim, Evidence, Caveat string }
type killedFinding struct{ Name, WhatWasClaimed, WhyKilled, KilledOn string }

var survivedFindings = []survivedFinding{
	{
		Name: "trend21 conviction banding",
		Claim: "Whether a stock is still on its current side of its 200-day average in 21 sessions " +
			"is predictable with accuracy that RISES sharply with conviction: 72.9% low band, 90.5% " +
			"moderate, 95.1% high, 97.6% very-high.",
		Evidence: "54,969 independent observations, 24 quarters, non-overlapping 21-session sampling, " +
			"quarter-block bootstrap intervals, survivorship-clean universe including delisted names. " +
			"Independently re-implemented and replicated within two jackknife standard errors.",
		Caveat: "This is a BACKTEST measurement; no live forecast has resolved yet, and the first " +
			"gradable date is 2026-08-07. Accuracy is not return: at the very-high band the mean " +
			"forward 21-day return is NEGATIVE (-0.39%) while the lower bands are positive. The " +
			"mechanism is trend persistence plus distance, not a discovery.",
	},
	{
		Name:  "survivorship correction is small",
		Claim: "Including delisted names moved overall structural accuracy by only 1.0 percentage point.",
		Evidence: "Re-validation over every symbol ever tracked; delisted-only accuracy 82.7% against " +
			"active-only 83.6%, with the band structure essentially unchanged.",
		Caveat: "Dead names were slightly HARDER, not easier. This validates the published tables; it " +
			"does not mean survivorship is generally a small problem.",
	},
	{
		Name:  "automatic model retirement fires",
		Claim: "The health gate switched off the platform's own flagship directional model on live evidence.",
		Evidence: "18,762 independent symbol-days at 48.0%, below its own majority-class baseline; the " +
			"retirement rule had been chained before the verdict existed.",
		Caveat: "This is evidence about the platform's discipline, not about the market. The correct " +
			"reading is that the model failed, and the machinery noticed.",
	},
	{
		Name:     "liquidity21 accuracy is real, its novelty is not",
		Claim:    "Next-month dollar-volume regime is predictable at 87.6% in the very-high band.",
		Evidence: "Same walk-forward, non-overlapping protocol as trend21.",
		Caveat: "The naive persistence rule scores the SAME. The skill IS liquidity persistence; the " +
			"accuracy claim holds and a novelty claim would not. Stated here rather than omitted.",
	},
}

var killedFindings = []killedFinding{
	{
		Name:           "52-week-high magnet",
		WhatWasClaimed: "Price is drawn toward a nearby 52-week high.",
		WhyKilled: "Tested against a matched null and came out NEGATIVE — the matched control did as " +
			"well or better. There was no effect to explain.",
		KilledOn: "recorded in the research ledger; retained as a rejection with equal weight to the survivals",
	},
	{
		Name:           "gap fill",
		WhatWasClaimed: "Opening gaps fill at a measurably high rate within the session.",
		WhyKilled: "Day-0 conditioning bug: the criterion used the same session's own range, so the " +
			"outcome was partly determined by the conditioning variable. Pulled from the served " +
			"surface rather than caveated, because a broken study with a warning is still a broken study.",
		KilledOn: "pulled before publication; the predictor still exists in code and is deliberately not served",
	},
	{
		Name:           "directional ensemble (1d and 1w)",
		WhatWasClaimed: "Next-day and next-week price direction.",
		WhyKilled: "Live prequential record 46.7% over 8,191 independent symbol-days with NEGATIVE " +
			"Brier skill and IC -0.02 — worse than its own naive baseline. Auto-retired by the health gate.",
		KilledOn: "retired on live evidence; the record remains retrievable through get_track_record",
	},
	{
		Name:           "correlation-rank pairs trade",
		WhatWasClaimed: "Persistent correlation ranking implies a tradeable pairs relationship.",
		WhyKilled: "Correlation rank persisted, but cointegration testing did not support the trade. " +
			"Resolved do-not-ship.",
		KilledOn: "resolved do-not-ship after the cointegration test",
	},
	{
		Name:           "liquidity-curve edge",
		WhatWasClaimed: "Moving down the liquidity curve into thinner names would improve measured edge.",
		WhyKilled: "Tested on the platform's own data and REFUTED. The recommendation came from " +
			"outside review and did not survive measurement.",
		KilledOn: "refuted on own data",
	},
}

// ── track record fallbacks ──────────────────────────────────────────────────
//
// These are the figures of record for the retired directional ensemble. They
// exist as constants so that get_track_record can NEVER return a response
// without the negative live record in it — not when the store is empty, not
// when the health worker has not run, not on a fresh install. A test asserts
// exactly that.

const (
	directionalLiveAccuracy = 0.467
	directionalObservations = 8191
	directionalBrierSkill   = -0.252
	directionalNote         = "Retired on live evidence. Accuracy is below the naive baseline and " +
		"Brier skill is negative, meaning the probability forecasts were worse than uninformative. " +
		"This is the platform's flagship directional model and its record is published rather than " +
		"withdrawn."
)
