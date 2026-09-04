// Package volprereg is the pre-registration for the HAR volatility forecast.
//
// # Why this is a new package and not a new entry in internal/prereg.Specs()
//
// prereg.Spec encodes a BINARY-ACCURACY claim with conviction bands: "will
// this stock still be above its 200-day average", graded correct or incorrect,
// claimed at 73.1% / 90.0% / 94.6% by band. That shape cannot express what is
// being claimed here, which is a LOSS-FUNCTION comparison against named nulls.
// Forcing it into Spec would mean registering a claim in a form that is not
// the claim, and the whole value of a pre-registration is that the frozen text
// says exactly what will be tested.
//
// So this defines its own byte-stable canonical() and Hash(), exactly the way
// prereg.Protocol, prereg.RetireRule and prereg.NullQuarantine each already do,
// and rides the SAME chain through the unchanged store.AppendPrereg.
// prereg.Specs() is not touched, and neither is anything the seq-87 forward
// test reads.
//
// # What is deliberately fixed here, before the run
//
// Every choice that could otherwise be made after seeing a result: both
// horizons, both outcome proxies, all three nulls, both losses, the unit of
// observation, the test statistic, the minimum evidence, and the four
// verdicts. The one that matters most is the HEADLINE: with two horizons,
// three nulls, two losses and two proxies there are 24 cells, and reporting
// whichever one clears the bar is the selection effect that has retired every
// previous predictor in this repository. One cell is named as the headline
// below and the rest are secondary.
package volprereg

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// RVForecastKind is the chain kind for this registration. A NEW kind, so it can
// never be mistaken for an amendment to an existing claim: the registrar's
// amendment path keys on the latest hash per kind.
const RVForecastKind = "rv-forecast-test-registration"

// LooksAlreadySpent is the number of exploratory looks taken at this question
// BEFORE the harness existed, which must be charged to the multiplicity
// divisor rather than forgotten.
//
// They were: (1) a one-day-ahead HAR against EWMA on 150 symbols, which HAR
// LOST (QLIKE 0.775 vs 0.676, DM t = +2.98 against HAR); and (2) the same with
// a Jensen correction and a 5-session target, which HAR won but not
// significantly (t = -1.24, pooled rather than day-clustered, so optimistic).
// Both are disclosed in the plan file and neither is quietly dropped.
const LooksAlreadySpent = 2

// RVSpec is the frozen statement of what will be tested.
type RVSpec struct {
	TestID string

	// Question is the exact thing being asked, in one sentence.
	Question string

	// Estimand is the target, stated precisely enough to be re-implemented
	// from this text alone.
	Estimand string

	// Horizons are the registered forward windows, in trading sessions.
	Horizons []int

	// Model is the forecasting rule, including every constant.
	Model string

	// Nulls are what it must beat.
	Nulls string

	// Losses are the loss functions, written out.
	Losses string

	// Control is the second, construction-independent outcome proxy and what
	// it is for.
	Control string

	// UnitOfObservation is the single most important line here.
	UnitOfObservation string

	// TestStatistic is the inference, including the correction.
	TestStatistic string

	// Headline names the ONE cell that decides the verdict.
	Headline string

	// FamilySize is the number of cells compared, for the multiplicity charge.
	FamilySize int

	// LooksSpent is the exploratory looks taken before registration.
	LooksSpent int

	// MinDistinctDays is the minimum evidence before any verdict exists.
	MinDistinctDays int

	// MinSymbolsPerDay guards against a day-cluster that is one symbol.
	MinSymbolsPerDay int

	// StartRule fixes when the live window opens.
	StartRule string

	// DecisionRule is the complete verdict map. Four outcomes, all four
	// registered before any data exists.
	DecisionRule string

	// KnownWeakness is what is already known to be wrong or unproven with
	// this design, recorded so it cannot quietly disappear once results
	// arrive.
	KnownWeakness string

	// WhatThisCannotChange names the frozen surfaces this registration must
	// not touch.
	WhatThisCannotChange string

	// BacktestAtFiling is the measured backtest state at the moment of
	// filing, so a later reader can see exactly what was known in advance.
	BacktestAtFiling string
}

// canonical renders an RVSpec byte-stably.
//
// Field order is fixed and every value is written with one formatting rule,
// because a hash over encoding/json output would depend on map iteration and
// struct-tag details rather than on the content. Same reasoning, and the same
// shape, as prereg.Protocol.canonical.
func (s RVSpec) canonical() string {
	var b strings.Builder
	w := func(k, v string) {
		b.WriteString("|")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(v)
	}
	b.WriteString("testId=")
	b.WriteString(s.TestID)
	w("question", s.Question)
	w("estimand", s.Estimand)
	ints := make([]string, 0, len(s.Horizons))
	for _, h := range s.Horizons {
		ints = append(ints, strconv.Itoa(h))
	}
	w("horizons", strings.Join(ints, ","))
	w("model", s.Model)
	w("nulls", s.Nulls)
	w("losses", s.Losses)
	w("control", s.Control)
	w("unitOfObservation", s.UnitOfObservation)
	w("testStatistic", s.TestStatistic)
	w("headline", s.Headline)
	w("familySize", strconv.Itoa(s.FamilySize))
	w("looksSpent", strconv.Itoa(s.LooksSpent))
	w("minDistinctDays", strconv.Itoa(s.MinDistinctDays))
	w("minSymbolsPerDay", strconv.Itoa(s.MinSymbolsPerDay))
	w("startRule", s.StartRule)
	w("decisionRule", s.DecisionRule)
	w("knownWeakness", s.KnownWeakness)
	w("whatThisCannotChange", s.WhatThisCannotChange)
	w("backtestAtFiling", s.BacktestAtFiling)
	return b.String()
}

// Hash is the spec digest that goes on the chain.
func (s RVSpec) Hash() string {
	h := sha256.Sum256([]byte(s.canonical()))
	return hex.EncodeToString(h[:])
}

// Registration returns the spec to be filed. It is a function rather than a
// literal so the constants above are the single source of truth.
func Registration() RVSpec {
	return RVSpec{
		TestID: "har-rv-2026-09",

		Question: "Does a HAR model of log realized variance produce a lower out-of-sample " +
			"QLIKE loss for the next session's realized variance than RiskMetrics EWMA " +
			"(lambda 0.94), measured live, with the trading DAY as the unit of observation?",

		Estimand: "RV_t = ln(O_t/C_{t-1})^2 + [0.5*ln(H_t/L_t)^2 - (2*ln2-1)*ln(C_t/O_t)^2], " +
			"Garman-Klass plus the overnight gap, from daily OHLC. A session is NOT " +
			"estimable and is recorded as a hole (never as zero) when: it has no prior " +
			"close; any of O,H,L,C or C_prev is non-positive; H<L; |ln(O_t/C_{t-1})| > 0.65 " +
			"(the repository's existing wild-move guard, reused verbatim from " +
			"volregime.maxSaneReturn, guarding unadjusted-split contamination); or H==L, " +
			"where a range estimator has nothing to read. Values below 1e-8 are floored so " +
			"ln(RV) stays defined. At horizon h the target is the MEAN RV over sessions " +
			"t+1..t+h and a partial window is refused rather than averaged.",

		Horizons: []int{1, 5},

		Model: "HAR (Corsi 2009) in logs: ln TARGET = b0 + bd*ln RV_t + bw*ln RVbar_5 + " +
			"bm*ln RVbar_22 + e, fitted by ordinary least squares on an expanding window " +
			"using only information available at the call bar, minimum 500 training rows, " +
			"refit every 5 sessions. The published forecast is the LEVEL, so the lognormal " +
			"retransform exp(yhat + s^2/2) is applied with s^2 from the training residuals " +
			"only. There is NO tunable hyperparameter and no grid was searched: 1/5/22 is " +
			"the canonical cascade and 500 is a two-year floor, so probability of " +
			"backtest overfitting is zero by construction rather than by argument.",

		Nulls: "(1) random walk, RVhat = RV_t; (2) RiskMetrics EWMA with lambda 0.94, the " +
			"same constant internal/volregime already uses; (3) a flat 22-session mean. " +
			"All three are computed AT CALL TIME and frozen in the same row as the " +
			"forecast; the store refuses a row whose nulls are absent. A null " +
			"reconstructed after the outcome is known is hindsight.",

		Losses: "QLIKE(a,f) = a/f - ln(a/f) - 1, and squared error (a-f)^2. QLIKE is the " +
			"headline because it is robust to a noisy but conditionally unbiased variance " +
			"proxy and because its asymmetry punishes under-forecasting, which is the " +
			"expensive direction for a risk estimate. MSE is reported because a result " +
			"that holds under only one loss is not a result.",

		Control: "The same forecasts are ALSO graded against RV^CC = ln(C_t/C_{t-1})^2, " +
			"which shares no construction with the headline proxy: two closes only, not " +
			"the range, not the open. It is far noisier but conditionally unbiased, which " +
			"under squared error preserves the ranking of two forecasts in expectation. " +
			"Its purpose is to separate FORECASTING from ESTIMATOR SMOOTHING: a model that " +
			"averages away the proxy's own measurement error beats one that does not, and " +
			"that is estimation, not prediction. Squared error only, because the log loss " +
			"is undefined on a session where the close did not move; those sessions are " +
			"excluded and COUNTED.",

		UnitOfObservation: "The trading DAY, never the (symbol, day) pair. Every symbol " +
			"resolving on one date shares a single market shock, so a panel of hundreds of " +
			"symbols over years is not hundreds of thousands of independent observations. " +
			"Losses are averaged across symbols within a date BEFORE any test is computed. " +
			"Pooling instead would return a t-statistic in the hundreds for any model at " +
			"all, and inflated independence is the defect that retired the earlier " +
			"predictors on this platform.",

		TestStatistic: "Diebold-Mariano on the daily mean loss differential, with a " +
			"Newey-West HAC variance at lag max(floor(4*(T/100)^(2/9)), h) -- the standard " +
			"automatic bandwidth, floored at the horizon because targets at h>1 overlap by " +
			"construction -- and the Harvey-Leybourne-Newbold small-sample correction, " +
			"which only ever widens and is applied unconditionally so it cannot be dropped " +
			"later. A 21-session moving-block bootstrap interval is reported ALONGSIDE and " +
			"is NOT the decision rule: the two can disagree when the loss differential is " +
			"skewed, and choosing whichever clears the bar is the same selection effect as " +
			"choosing a configuration.",

		Headline: "QLIKE, horizon 1, headline proxy RV^GK, HAR versus EWMA(0.94). ONE cell. " +
			"The other 23 combinations of horizon, null, loss and proxy are secondary and " +
			"reported in full, but none of them can produce the verdict.",

		FamilySize: 24,

		LooksSpent: LooksAlreadySpent,

		MinDistinctDays:  60,
		MinSymbolsPerDay: 30,

		StartRule: "The first live forecast frozen STRICTLY AFTER this record's timestamp. " +
			"No backfill, ever. Forecasts already in rv_forecasts at filing time, if any, " +
			"are excluded from the live record; the registrar refuses to file at all if any " +
			"forecast has already RESOLVED, because a forward test whose results can be " +
			"read is not a registration.",

		DecisionRule: "Four outcomes, all registered before any live data exists. " +
			"INSUFFICIENT: fewer than 60 distinct live trading days, or fewer than 30 " +
			"symbols on a day, in which case that day is not counted; no verdict either " +
			"way. NO SKILL DEMONSTRATED: the headline DM p-value, Bonferroni-corrected " +
			"across family size 24 and the looks already spent, is at or above 0.05. " +
			"ESTIMATOR ARTIFACT: the headline clears its bar but the RV^CC control does " +
			"not, meaning the advantage is consistent with smoothing the proxy's " +
			"measurement error rather than forecasting. BEATS THE NULLS: the corrected " +
			"headline p-value is below 0.05 with the sign in HAR's favour AND the RV^CC " +
			"control holds. On NO SKILL or ESTIMATOR ARTIFACT the forecast KEEPS " +
			"publishing its number with the verdict rendered at the same size, and every " +
			"sentence claiming it beats a standard volatility model is withdrawn " +
			"automatically. A variance forecast that merely ties RiskMetrics is still a " +
			"usable risk number; what must be withdrawn is the claim, not the number.",

		KnownWeakness: "The estimator is a daily RANGE estimator and is invalid on names " +
			"that barely trade: 3.44% of sessions in the live table have H==L and are holes, " +
			"and the backtest universe is screened to symbols with under 1% such sessions, " +
			"so this claim does not extend to illiquid names. True intraday realized " +
			"variance is NOT used: only 22 sessions of full minute coverage exist, far " +
			"below what a walk-forward needs, so the proxy is the daily one throughout. " +
			"Expected shortfall is NOT graded anywhere in this registration: ES is not " +
			"elicitable, there is no Kupiec analogue, and attaching a coverage test to it " +
			"would be a fabricated statistic. Two exploratory looks were spent before this " +
			"was written and are charged above; the first of them LOST to EWMA.",

		WhatThisCannotChange: "Pre-registration sequence 87 and everything it grades; " +
			"internal/ensemble; internal/composite; the confluence scorer; " +
			"tools/accuracy_registry.py and its pinned digest; the frozen structural specs " +
			"in internal/prereg.Specs(); and the retirement of the directional ensemble, " +
			"which is sticky and is not reopened by anything here.",

		BacktestAtFiling: "Measured before filing, on 758 survivorship-clean operating " +
			"companies, 971,151 forecasts collapsed to 1,398 day-clusters: headline QLIKE " +
			"HAR vs EWMA mean -0.0469, DM t = -8.79 at h=1 and -8.91 at h=5; RV^CC control " +
			"t = -5.77 and -4.47; stable in every calendar year 2021-2026 with no sign " +
			"flip; Hansen SPA with EWMA as benchmark and HAR as challenger p = 0.0000 over " +
			"5,000 stationary-bootstrap replications; insensitive to refit cadence at 5, 21 " +
			"and 63 sessions; 95% of symbols favour HAR and removing the five most extreme " +
			"strengthens rather than weakens it. THIS IS A BACKTEST. It is recorded here so " +
			"a later reader can see exactly what was known in advance, and it is not " +
			"evidence for the live claim.",
	}
}
