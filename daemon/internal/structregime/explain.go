// Explainable regime forecasts (2026-07-24).
//
// A forecast you cannot audit is a forecast you cannot improve or trust. This
// decomposes a trend21 call into the measured quantities that produced it, so
// every number on the page traces back to a computation rather than a vibe.
//
// SCOPE, deliberately narrow
// --------------------------
// Explanations are built ONLY for the structural regime forecasts. The obvious
// thing to build — "BUY NVDA · 82.7% · +3.4% expected" — cannot be built
// honestly here: the directional model measured 48.0% against a 54.4%
// baseline and was auto-retired, so dressing it in contributors and confidence
// intervals would make a model the evidence rejected look MORE credible. The
// format is only as honest as the number it decorates.
//
// The analog is a real lookup, not an illustration: the historical bar whose
// state most resembles today, together with what actually happened next. When
// no comparable state exists in the history, that is reported as absent rather
// than filled with the nearest bad match.
package structregime

import (
	"fmt"
	"math"
)

// Contribution is one factor's signed push toward or against the call.
type Contribution struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`  // the measured quantity, in its own units
	Weight float64 `json:"weight"` // signed: + supports the call, - opposes it
	Detail string  `json:"detail"` // plain English, no jargon
}

// Analog is the closest historical precedent and its realized outcome.
type Analog struct {
	Found        bool    `json:"found"`
	Index        int     `json:"-"`
	Distance     float64 `json:"distance"`     // state distance, 0 = identical
	PriorDistPct float64 `json:"priorDistPct"` // its distance-from-SMA200 then
	FwdReturnPct float64 `json:"fwdReturnPct"` // what happened over the horizon
	HeldSide     bool    `json:"heldSide"`     // did it stay the same side of SMA200
	Note         string  `json:"note"`
}

// Explanation is the full auditable record behind one trend21 forecast.
type Explanation struct {
	Kind      Kind           `json:"kind"`
	Regime    string         `json:"regime"`
	Horizon   int            `json:"horizonDays"`
	Accuracy  float64        `json:"bandedAccuracy"`
	Tier      string         `json:"tier"`
	Conviction float64       `json:"conviction"`
	Supports  []Contribution `json:"supports"`
	Opposes   []Contribution `json:"opposes"`
	Analog    Analog         `json:"analog"`
	Summary   string         `json:"summary"`
	Caveat    string         `json:"caveat"`
}

// AuditTrend decomposes the trend21 call into weighted contributors plus a
// historical analog. Distinct from ExplainTrend (report.go), which returns the
// raw scalar inputs — this is the human-facing audit surface built on top.
// Returns ok=false whenever PredictTrend itself would refuse.
func AuditTrend(closes []float64) (Explanation, bool) {
	f, ok := PredictTrend(closes)
	if !ok {
		return Explanation{}, false
	}
	sma := rollMean(closes, 200)
	n := len(closes)
	dist := closes[n-1]/sma[n-1] - 1 // signed distance from the 200-day average

	var sup, opp []Contribution
	add := func(c Contribution) {
		if c.Weight >= 0 {
			sup = append(sup, c)
		} else {
			opp = append(opp, c)
		}
	}

	// 1. DISTANCE — the primary driver. Being far from the average is what
	//    makes crossing back within the horizon unlikely.
	up := f.Regime == "uptrend"
	add(Contribution{
		Name:   "distance from 200-day average",
		Value:  dist * 100,
		Weight: f.Conviction, // conviction IS the percentile of this distance
		Detail: fmt.Sprintf("price is %.1f%% %s its 200-day average — a %s call has to "+
			"cross that gap within %d sessions to be wrong",
			math.Abs(dist)*100, sideWord(up), oppositeWord(up), f.HorizonDays),
	})

	// 2. PERSISTENCE — how long it has already held this side. A regime that
	//    has survived many sessions is likelier to survive more.
	held := sessionsHeldSide(closes, sma, up)
	pw := math.Min(float64(held)/float64(horizon*2), 1.0) * 0.5
	add(Contribution{
		Name:   "regime persistence",
		Value:  float64(held),
		Weight: pw,
		Detail: fmt.Sprintf("has stayed %s the average for %d consecutive sessions",
			sideWord(up), held),
	})

	// 3. SLOPE — an average moving TOWARD price shrinks the gap and works
	//    against the call, so this contribution can be negative.
	slope := smaSlopePct(sma)
	sw := 0.0
	switch {
	case up && slope > 0, !up && slope < 0:
		sw = 0.25 // the average is moving away from a crossing
	case up && slope < 0, !up && slope > 0:
		sw = -0.25
	}
	add(Contribution{
		Name:   "200-day average slope",
		Value:  slope * 100,
		Weight: sw,
		Detail: fmt.Sprintf("the average itself is %s at %.2f%%/session",
			riseFall(slope), math.Abs(slope)*100),
	})

	// 4. VOLATILITY — high recent volatility makes any boundary easier to
	//    cross, so it always opposes a persistence call.
	vol := recentVolPct(closes, 21)
	gap := math.Abs(dist)
	vw := 0.0
	if vol > 0 {
		// How many sessions of typical movement would close the gap? Fewer
		// than the horizon means volatility is a genuine threat.
		sessionsToCross := gap / vol
		if sessionsToCross < float64(f.HorizonDays) {
			vw = -0.35
		} else {
			vw = 0.15
		}
	}
	add(Contribution{
		Name:   "recent volatility",
		Value:  vol * 100,
		Weight: vw,
		Detail: fmt.Sprintf("typical daily move is %.2f%%; at that pace the gap to the "+
			"average is about %.0f sessions wide", vol*100, safeDiv(gap, vol)),
	})

	an := findAnalog(closes, sma, dist, f.HorizonDays)

	ex := Explanation{
		Kind: f.Kind, Regime: f.Regime, Horizon: f.HorizonDays,
		Accuracy: f.HistoricalAccuracy, Tier: f.Tier, Conviction: f.Conviction,
		Supports: sup, Opposes: opp, Analog: an,
		Summary: fmt.Sprintf(
			"Still in an %s in %d sessions — %.0f%% of similar %s calls were right",
			f.Regime, f.HorizonDays, f.HistoricalAccuracy*100, f.Tier),
		Caveat: "This is a call about market STRUCTURE (which side of a moving average " +
			"price sits on), not a price-direction forecast. The accuracy shown is for " +
			"this conviction band specifically, measured walk-forward — it is not a " +
			"live track record until the outcome resolver grades it.",
	}
	return ex, true
}

func sideWord(up bool) string {
	if up {
		return "above"
	}
	return "below"
}

func oppositeWord(up bool) string {
	if up {
		return "downtrend"
	}
	return "uptrend"
}

func riseFall(s float64) string {
	if s > 0 {
		return "rising"
	}
	if s < 0 {
		return "falling"
	}
	return "flat"
}

func safeDiv(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b
}

// sessionsHeldSide counts consecutive sessions on the current side.
func sessionsHeldSide(closes, sma []float64, up bool) int {
	held := 0
	for i := len(closes) - 1; i >= 0; i-- {
		if sma[i] <= 0 || math.IsNaN(sma[i]) {
			break
		}
		above := closes[i] > sma[i]
		if above != up {
			break
		}
		held++
	}
	return held
}

// smaSlopePct is the 200-day average's per-session change over 21 sessions.
func smaSlopePct(sma []float64) float64 {
	n := len(sma)
	if n < 22 {
		return 0
	}
	a, b := sma[n-22], sma[n-1]
	if a <= 0 || math.IsNaN(a) || math.IsNaN(b) {
		return 0
	}
	return (b/a - 1) / 21
}

// recentVolPct is the mean absolute daily return over the last n sessions.
func recentVolPct(closes []float64, n int) float64 {
	if len(closes) < n+1 {
		return 0
	}
	sum, cnt := 0.0, 0
	for i := len(closes) - n; i < len(closes); i++ {
		if closes[i-1] <= 0 {
			continue
		}
		sum += math.Abs(closes[i]/closes[i-1] - 1)
		cnt++
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// findAnalog locates the historical bar whose distance-from-average most
// resembles today's, on the SAME side, with a full horizon of future data —
// and reports what actually happened next.
func findAnalog(closes, sma []float64, dist float64, horizonDays int) Analog {
	n := len(closes)
	best, bestDist := -1, math.Inf(1)
	// Skip the most recent horizon (no realized outcome yet) and the warmup.
	for i := 200; i < n-horizonDays-1; i++ {
		if sma[i] <= 0 || math.IsNaN(sma[i]) {
			continue
		}
		d := closes[i]/sma[i] - 1
		if (d > 0) != (dist > 0) {
			continue // must be the same regime side to be comparable
		}
		if gap := math.Abs(d - dist); gap < bestDist {
			best, bestDist = i, gap
		}
	}
	// A match further than 2pp of distance away is not a precedent; saying so
	// beats presenting a bad match as evidence.
	if best < 0 || bestDist > 0.02 {
		return Analog{Found: false,
			Note: "no comparable historical state in this symbol's stored history"}
	}
	j := best + horizonDays
	if j >= n || closes[best] <= 0 {
		return Analog{Found: false, Note: "closest match lacks a full forward window"}
	}
	fwd := closes[j]/closes[best] - 1
	heldSide := false
	if sma[j] > 0 && !math.IsNaN(sma[j]) {
		heldSide = (closes[j] > sma[j]) == (dist > 0)
	}
	return Analog{
		Found: true, Index: best, Distance: bestDist,
		PriorDistPct: (closes[best]/sma[best] - 1) * 100,
		FwdReturnPct: fwd * 100,
		HeldSide:     heldSide,
		Note: fmt.Sprintf("closest prior state sat %.1f%% from its average; "+
			"%d sessions later it had moved %+.1f%% and %s the same side",
			(closes[best]/sma[best]-1)*100, horizonDays, fwd*100,
			map[bool]string{true: "held", false: "left"}[heldSide]),
	}
}
