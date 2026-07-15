// Package recommendation turns SignalDeck's already-stored evidence into ONE
// plain-English, explainable recommendation per symbol — the "Explain Every
// Recommendation" + deterministic multi-agent-views engine.
//
// # Doctrine (inherited from internal/composite + internal/adaptive)
//
// This package is PURE: it takes already-read inputs (the composite score, its
// factor tiles, the conviction assessment, price/EPS, and the measured forward-
// return distribution) and returns a structured Recommendation. It performs NO
// storage, NO model calls, NO I/O — loading lives in the pipeline/store, the
// same split composite.go documents. It NEVER invents a number: a value that
// isn't sourced is marked unavailable with the reason, not faked.
//
//   - The Decision is a RELATIVE-RANK framing (Buy/Accumulate/Hold/Reduce/
//     Avoid/Watch), never "advice" and never a probability of profit. It is a
//     deterministic function of the edge sign and the conviction band.
//   - Confidence is the conviction band + the MEASURED live accuracy, never a
//     fabricated precise percentage.
//   - Fair value is an explicit multiples HEURISTIC on the latest EDGAR EPS —
//     labeled as such, never a valuation model or a price target.
//   - Bull/base/bear come from N MEASURED historical analogous setups
//     (expectancy), never a synthesized point forecast.
//   - The nine agent views are DETERMINISTIC lenses over the same inputs — each
//     cites real numbers and takes an honest stance; none are LLM-generated.
package recommendation

import (
	"fmt"
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Inputs is everything Build needs, all already read by the caller. Absence is
// signalled by the Has* flags / empty slices so a real zero is never mistaken
// for missing data.
type Inputs struct {
	Symbol, Market string
	// composite score (HasScore=false when the symbol has no stored score).
	HasScore            bool
	Score               int     // 1..10 forced-curve rank
	Edge                float64 // calProb − 0.5
	CalProb             float64
	CurvePct            float64
	NUsed               int
	Factors             []composite.Factor
	Conviction          composite.ConvictionResult
	MeasuredAccuracyPct float64 // fleet live win rate as a percent (0 if unknown)
	EdgeProvenLive      bool
	// price + fundamentals.
	HasPrice bool
	Price    float64
	HasEPS   bool
	EPS      float64
	PeerPE   float64 // P/E multiple assumption for fair value (caller supplies, e.g. 20)
	// distribution source.
	Expectancy []md.Expectancy // may be empty
	// context.
	Regime       string // "" if unknown
	HasRank      bool
	RankPct      float64 // 0..100
	MacroSummary string  // one-line live macro state for the Economist view ("" ok)
}

// FairValue is a rough multiples heuristic, never a valuation model.
type FairValue struct {
	Available bool    `json:"available"`
	Value     float64 `json:"value"`
	Method    string  `json:"method"` // e.g. "EPS 12.90 × 20.0 peer P/E (heuristic)"
	Note      string  `json:"note"`   // honesty caveat
}

// Scenario is one leg of the bull/base/bear distribution. Ret is a % return.
type Scenario struct {
	Prob float64 `json:"prob"`
	Ret  float64 `json:"ret"`
}

// Distribution is the measured bull/base/bear forward-return spread.
type Distribution struct {
	Available bool     `json:"available"`
	Bull      Scenario `json:"bull"`
	Base      Scenario `json:"base"`
	Bear      Scenario `json:"bear"`
	Source    string   `json:"source"` // e.g. "N=42 analogous historical setups (expectancy 1d)"
}

// AgentView is one deterministic lens over the inputs, citing real numbers.
type AgentView struct {
	Role   string `json:"role"`   // one of the nine fixed roles, in Build order
	Stance string `json:"stance"` // "bullish"|"bearish"|"neutral"
	View   string `json:"view"`   // grounded plain-English reasoning
}

// Recommendation is the full explainable read for one symbol.
type Recommendation struct {
	Symbol, Market      string       `json:"-"`
	Decision            string       `json:"decision"`        // "Buy"|"Accumulate"|"Hold"|"Reduce"|"Avoid"|"Watch"
	ConfidenceLabel     string       `json:"confidenceLabel"` // from Conviction ("MODERATE conviction")
	MeasuredAccuracyPct float64      `json:"measuredAccuracyPct"`
	FairValue           FairValue    `json:"fairValue"`
	HasPrice            bool         `json:"hasPrice"`
	CurrentPrice        float64      `json:"currentPrice"`
	ExpectedReturnPct   float64      `json:"expectedReturnPct"` // (fairValue-price)/price*100, only when both available
	HasExpectedReturn   bool         `json:"hasExpectedReturn"`
	KeyDrivers          []string     `json:"keyDrivers"`
	Risks               []string     `json:"risks"`
	Distribution        Distribution `json:"distribution"`
	Agents              []AgentView  `json:"agents"` // EXACTLY 9, in the fixed role order
	Assumptions         []string     `json:"assumptions"`
	Disclaimer          string       `json:"disclaimer"`
}

// Decision labels — a relative-rank action framing, never advice.
const (
	DecisionBuy        = "Buy"
	DecisionAccumulate = "Accumulate"
	DecisionHold       = "Hold"
	DecisionReduce     = "Reduce"
	DecisionAvoid      = "Avoid"
	DecisionWatch      = "Watch"
)

// Stance labels for the agent views.
const (
	stanceBull    = "bullish"
	stanceBear    = "bearish"
	stanceNeutral = "neutral"
)

// disclaimer rides with every recommendation — the persistent not-advice caveat.
const disclaimer = "SignalDeck measures and stores; it does not advise. Relative ranks + backtested calibration, not a guarantee. Not financial advice."

// Build renders the full recommendation from already-read inputs. Pure and
// deterministic: same inputs → byte-identical output.
func Build(in Inputs) Recommendation {
	rec := Recommendation{
		Symbol:              in.Symbol,
		Market:              in.Market,
		Decision:            decide(in),
		ConfidenceLabel:     in.Conviction.Label,
		MeasuredAccuracyPct: in.MeasuredAccuracyPct,
		HasPrice:            in.HasPrice,
		FairValue:           fairValue(in),
		Distribution:        distribution(in),
		KeyDrivers:          keyDrivers(in.Factors),
		Risks:               risks(in),
		Disclaimer:          disclaimer,
	}
	if in.HasPrice {
		rec.CurrentPrice = in.Price
	}
	// Expected return is only defensible when BOTH a heuristic fair value and a
	// live price exist — otherwise it stays unavailable, never zero-as-fact.
	if rec.FairValue.Available && in.HasPrice && in.Price > 0 {
		rec.ExpectedReturnPct = (rec.FairValue.Value - in.Price) / in.Price * 100
		rec.HasExpectedReturn = true
	}
	rec.Agents = agentViews(in, rec)
	rec.Assumptions = assumptions(in, rec)
	return rec
}

// decide maps the edge sign and conviction band to a relative-rank action.
// !HasScore means there is no read yet (Watch); an edge inside coin-flip range
// (±composite.EdgeSlight) is a Hold no matter how the band reads.
func decide(in Inputs) string {
	if !in.HasScore {
		return DecisionWatch
	}
	if math.Abs(in.Edge) < composite.EdgeSlight {
		return DecisionHold
	}
	high := in.Conviction.Band == composite.BandHigh
	moderate := in.Conviction.Band == composite.BandModerate
	if in.Edge > 0 { // bullish lean
		switch {
		case high:
			return DecisionBuy
		case moderate:
			return DecisionAccumulate
		default:
			return DecisionWatch
		}
	}
	switch { // bearish lean
	case high:
		return DecisionAvoid
	case moderate:
		return DecisionReduce
	default:
		return DecisionWatch
	}
}

// fairValue is a rough EPS×peer-P/E heuristic, available only for a symbol with
// a positive latest EDGAR EPS and a supplied peer multiple. ETFs, crypto, and
// unfiled names have no EPS, so no fair value is shown — the reason is stated,
// never a fabricated target.
func fairValue(in Inputs) FairValue {
	switch {
	case in.HasEPS && in.EPS > 0 && in.PeerPE > 0:
		return FairValue{
			Available: true,
			Value:     in.EPS * in.PeerPE,
			Method:    fmt.Sprintf("EPS %.2f × %.1f peer P/E (heuristic)", in.EPS, in.PeerPE),
			Note:      "rough multiples heuristic on the latest EDGAR EPS — NOT a valuation model or a price target",
		}
	case in.HasEPS && in.EPS <= 0:
		return FairValue{
			Note: fmt.Sprintf("no fair value: latest EPS is %.2f (non-positive) — a P/E multiple is meaningless on negative earnings", in.EPS),
		}
	default:
		return FairValue{
			Note: "no fair value: no latest EDGAR EPS on file (ETFs, crypto, and unfiled names have none) or no peer P/E supplied",
		}
	}
}

// distribution derives a bull/base/bear spread from the MEASURED expectancy.
//
// It selects ONE real expectancy row (see pickExpectancy) rather than
// synthesizing a blend, so every reported number is a genuine historical
// statistic. The stored forward returns are FRACTIONS (0.012 = +1.2%), so they
// are ×100 to percents — documented as an assumption. Base is the measured
// median; bull/bear are mean ± one stdev. Probabilities: Base holds a fixed 50%
// of the mass and the remaining 50 is split by the measured hit rate (fraction
// of positive forward returns), so the three always sum to exactly 100 and the
// bull/bear split reflects real history, not a guess.
func distribution(in Inputs) Distribution {
	e, ok := pickExpectancy(in.Expectancy)
	if !ok {
		return Distribution{Source: "no analogous historical setups stored yet"}
	}
	hr := clamp01(e.HitRate)
	bull := math.Round(50 * hr)
	bear := 50 - bull
	return Distribution{
		Available: true,
		Bull:      Scenario{Prob: bull, Ret: (e.MeanFwd + e.Stdev) * 100},
		Base:      Scenario{Prob: 50, Ret: e.MedianFwd * 100},
		Bear:      Scenario{Prob: bear, Ret: (e.MeanFwd - e.Stdev) * 100},
		Source:    fmt.Sprintf("N=%d analogous historical setups (expectancy %s)", e.N, e.Horizon),
	}
}

// pickExpectancy chooses the single most-supported distribution: the highest-N
// 1-day expectancy if any 1d rows exist, otherwise the highest-N row of any
// horizon. Rows with N≤0 are ignored (no sample). Ties in N break by StateKey
// for deterministic output.
func pickExpectancy(xs []md.Expectancy) (md.Expectancy, bool) {
	if best, ok := bestByN(xs, true); ok {
		return best, true
	}
	return bestByN(xs, false)
}

func bestByN(xs []md.Expectancy, oneDayOnly bool) (md.Expectancy, bool) {
	var best md.Expectancy
	found := false
	for _, x := range xs {
		if x.N <= 0 {
			continue
		}
		if oneDayOnly && x.Horizon != md.H1d {
			continue
		}
		if !found || x.N > best.N || (x.N == best.N && x.StateKey < best.StateKey) {
			best, found = x, true
		}
	}
	return best, found
}

// keyDrivers surfaces the bullish, ungated factor tiles' raw evidence lines
// (the SAME lines the composite card shows), capped at four.
func keyDrivers(factors []composite.Factor) []string {
	out := []string{}
	for _, f := range factors {
		if f.Verdict > 0 && !f.Gated && f.Evidence != "" {
			out = append(out, f.Evidence)
			if len(out) == 4 {
				break
			}
		}
	}
	return out
}

// risks collects the bearish factor evidence, the regime / short-volume context
// caveats (never directional themselves), and ALWAYS the persistent conviction
// risk note — a score is a relative rank, never a certainty.
func risks(in Inputs) []string {
	out := []string{}
	for _, f := range in.Factors {
		if f.Verdict < 0 && !f.Gated && f.Evidence != "" {
			out = append(out, f.Evidence)
			if len(out) == 4 {
				break
			}
		}
	}
	for _, key := range []string{composite.FactorRegime, composite.FactorShortVol} {
		if f, ok := factorByKey(in.Factors, key); ok && !f.Gated && f.Evidence != "" {
			out = append(out, f.Evidence)
		}
	}
	if in.Conviction.RiskNote != "" {
		out = append(out, in.Conviction.RiskNote)
	}
	return out
}

// assumptions spells out the heuristics baked into the read so nothing hides.
func assumptions(in Inputs, rec Recommendation) []string {
	out := []string{}
	if rec.FairValue.Available {
		out = append(out, fmt.Sprintf("fair value uses a flat %.1f× peer P/E on the latest EDGAR EPS (a heuristic, not a valuation model)", in.PeerPE))
	}
	if rec.Distribution.Available {
		out = append(out, fmt.Sprintf("bull/base/bear come from %s, with stored forward returns treated as fractions", rec.Distribution.Source))
	}
	out = append(out, "scores are a relative cross-sectional rank of today's universe, not a probability of profit")
	out = append(out, "confidence is the conviction band plus the measured live accuracy — never a fabricated precise percentage")
	return out
}

// ── small helpers ──────────────────────────────────────────────────────────

func factorByKey(factors []composite.Factor, key string) (composite.Factor, bool) {
	for _, f := range factors {
		if f.Key == key {
			return f, true
		}
	}
	return composite.Factor{}, false
}

// stanceFromVerdict turns a factor verdict (±1/0) into a stance label.
func stanceFromVerdict(v int) string {
	switch {
	case v > 0:
		return stanceBull
	case v < 0:
		return stanceBear
	default:
		return stanceNeutral
	}
}

// stanceFromEdge reads the composite edge as a stance, neutral inside coin-flip
// range or when there is no stored score.
func stanceFromEdge(edge float64, hasScore bool) string {
	if !hasScore || math.Abs(edge) < composite.EdgeSlight {
		return stanceNeutral
	}
	if edge > 0 {
		return stanceBull
	}
	return stanceBear
}

// overallStance is the Portfolio Manager's net stance, from the final decision.
func overallStance(decision string) string {
	switch decision {
	case DecisionBuy, DecisionAccumulate:
		return stanceBull
	case DecisionAvoid, DecisionReduce:
		return stanceBear
	default:
		return stanceNeutral
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
