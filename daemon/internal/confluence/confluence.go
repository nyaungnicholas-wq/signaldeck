// Package confluence is the PURE "confluence gate" engine (CONFLUENCE GATE
// wave). A trade SETUP is only flagged when several INDEPENDENT signal FAMILIES
// AGREE on a direction — and the whole vote is shown transparently so the user
// sees exactly which families agreed, which dissented, and why.
//
// HONESTY (the whole point of this package): confluence manufactures no edge. It
// is a strict AND over families that already exist elsewhere in the system, each
// deliberately kept DISTINCT so agreement actually means something (a prediction
// echoing a trend that echoes relative strength would be one signal wearing five
// hats). Every family emits a vote in {-1,0,+1} with a plain-English reason and
// its name; an ABSENT family drops out of the count entirely (it is never imputed
// as a neutral 0), and a near-coin-flip prediction is honestly one weak,
// non-decisive vote rather than a tie-breaker. No store or http imports live here
// on purpose: the gate is pure and fully unit-tested, so the decision can be
// audited in isolation.
package confluence

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// predEdgeMin is how far the calibrated P(up) must sit from 0.5 before the
	// prediction family votes a direction at all. Below it the model is a
	// coin-flip and honestly casts a present-but-neutral (weak) vote — never a
	// decisive one.
	predEdgeMin = 0.03
	// breakoutFreshDays bounds how old a trend-creation breakout may be and still
	// count. An older breakout is stale context, not a live signal, so the family
	// drops out (absent) rather than voting on ancient news.
	breakoutFreshDays = 5.0
	// relStrongPct / relWeakPct are the cross-sectional ranking percentile bands
	// that turn relative strength into a directional vote.
	relStrongPct = 0.70
	relWeakPct   = 0.30
	// maxDissent is the most opposing votes a setup may carry and still flag: a
	// setup is agreement, so more than one family pointing the other way vetoes it.
	maxDissent = 1
	// defaultMinAgree is the floor of AGREEING families for a setup, overridable
	// via SIGNALDECK_CONFLUENCE_MIN. With five families, 3 means a clear majority
	// with room for at most one dissenter.
	defaultMinAgree = 3
)

// familyOrder is the fixed display order of the five INDEPENDENT families. Kept
// stable so the transparency panel always reads the same way.
var familyOrder = []string{"smart_money", "trend", "prediction", "rel_strength", "breakout"}

// Vote is one family's contribution: its direction in {-1,0,+1}, the family
// name, and a plain-English reason. Every PRESENT family produces exactly one.
type Vote struct {
	Family string `json:"family"`
	Reason string `json:"reason"`
	Dir    int    `json:"dir"`
}

// Setup is the assessed confluence for one symbol. Direction is the net call
// (sign of long−short); Agree/Dissent/Present count the PRESENT families;
// Score = (long−short)/max(1,Present) ∈ [-1,1]; Votes lists every present
// family (the whole point — full transparency); IsSetup is the strict AND gate.
type Setup struct {
	Direction int     `json:"direction"`
	Agree     int     `json:"agree"`
	Dissent   int     `json:"dissent"`
	Present   int     `json:"present"`
	Score     float64 `json:"score"`
	Votes     []Vote  `json:"votes"`
	IsSetup   bool    `json:"isSetup"`
}

// Inputs is the raw evidence for ONE symbol, one field group per family, each
// with an explicit present flag. An absent family is simply excluded — it never
// counts as a neutral vote. The five families are deliberately DISTINCT so that
// agreement across them is genuine independent confirmation.
type Inputs struct {
	// smart_money — the SMART MONEY wave's positioning score band.
	SmartMoneyPresent bool
	SmartMoneyLabel   string  // strong_accumulation … neutral … strong_distribution
	SmartMoneyScore   float64 // [-1,1], shown in the reason

	// trend — the regime classifier's label for this symbol.
	RegimePresent bool
	RegimeLabel   string // uptrend | downtrend | range | squeeze

	// prediction — the flagship calibrated P(up).
	PredictionPresent bool
	CalProb           float64

	// rel_strength — the cross-sectional ranking percentile in [0,1].
	RankPresent bool
	RankPct     float64

	// breakout — the freshest trend-creation event (Donchian / squeeze release).
	BreakoutPresent bool
	BreakoutKind    string  // donchian_up | donchian_down | squeeze_release | volume_spike
	BreakoutAgeDays float64 // age of that event in days (freshness gate)
	BreakoutDir     int     // -1/0/+1 raw direction (donchian by kind; squeeze by release-bar body)
}

// Assess tallies the present families' votes and applies the confluence gate.
// It is a strict AND over INDEPENDENT signals — never a manufactured edge.
func Assess(in Inputs) Setup {
	votesByFamily := map[string]Vote{}
	add := func(family string, dir int, present bool, reason string) {
		if !present {
			return // absent family: excluded from Present entirely (never a neutral 0)
		}
		votesByFamily[family] = Vote{Family: family, Reason: reason, Dir: dir}
	}

	d, p, reason := smartMoneyVote(in)
	add("smart_money", d, p, reason)
	d, p, reason = trendVote(in)
	add("trend", d, p, reason)
	d, p, reason = predictionVote(in)
	add("prediction", d, p, reason)
	d, p, reason = relStrengthVote(in)
	add("rel_strength", d, p, reason)
	d, p, reason = breakoutVote(in)
	add("breakout", d, p, reason)

	// Tally long/short/neutral over PRESENT families, in the fixed family order
	// so Votes reads consistently.
	long, short := 0, 0
	votes := make([]Vote, 0, len(votesByFamily))
	for _, family := range familyOrder {
		v, ok := votesByFamily[family]
		if !ok {
			continue
		}
		votes = append(votes, v)
		switch {
		case v.Dir > 0:
			long++
		case v.Dir < 0:
			short++
		}
	}
	present := len(votes)

	agree := long
	if short > agree {
		agree = short
	}
	dissent := long
	if short < dissent {
		dissent = short
	}
	direction := isign(long - short)
	denom := present
	if denom < 1 {
		denom = 1
	}
	score := float64(long-short) / float64(denom)

	isSetup := agree >= minAgree() && dissent <= maxDissent && direction != 0

	return Setup{
		Direction: direction,
		Agree:     agree,
		Dissent:   dissent,
		Present:   present,
		Score:     score,
		Votes:     votes,
		IsSetup:   isSetup,
	}
}

// ── families (each returns dir, present, reason) ─────────────────────────────

// smartMoneyVote reads the SMART MONEY positioning band: accumulation is a long
// vote, distribution a short vote, an explicit neutral a present-but-neutral 0.
func smartMoneyVote(in Inputs) (int, bool, string) {
	if !in.SmartMoneyPresent {
		return 0, false, ""
	}
	switch {
	case strings.Contains(in.SmartMoneyLabel, "accumulation"):
		return +1, true, fmt.Sprintf("smart-money %s (score %+.2f) — informed participants accumulating",
			bandWords(in.SmartMoneyLabel), in.SmartMoneyScore)
	case strings.Contains(in.SmartMoneyLabel, "distribution"):
		return -1, true, fmt.Sprintf("smart-money %s (score %+.2f) — informed participants distributing",
			bandWords(in.SmartMoneyLabel), in.SmartMoneyScore)
	default:
		return 0, true, fmt.Sprintf("smart-money neutral (score %+.2f) — no clear positioning", in.SmartMoneyScore)
	}
}

// trendVote reads the regime classifier: uptrend long, downtrend short, a range
// or squeeze a present-but-neutral 0 (no directional trend to confirm).
func trendVote(in Inputs) (int, bool, string) {
	if !in.RegimePresent {
		return 0, false, ""
	}
	switch in.RegimeLabel {
	case "uptrend":
		return +1, true, "regime is an uptrend"
	case "downtrend":
		return -1, true, "regime is a downtrend"
	default:
		return 0, true, fmt.Sprintf("regime is %q — no directional trend", in.RegimeLabel)
	}
}

// predictionVote reads the flagship calibrated P(up). Only an edge of at least
// predEdgeMin from 0.5 votes a direction; inside that band the model is a
// coin-flip and casts ONE honest, non-decisive, present-but-neutral vote.
func predictionVote(in Inputs) (int, bool, string) {
	if !in.PredictionPresent {
		return 0, false, ""
	}
	edge := in.CalProb - 0.5
	if edge >= predEdgeMin {
		return +1, true, fmt.Sprintf("calibrated P(up) %.2f (edge %+.2f)", in.CalProb, edge)
	}
	if edge <= -predEdgeMin {
		return -1, true, fmt.Sprintf("calibrated P(up) %.2f (edge %+.2f)", in.CalProb, edge)
	}
	return 0, true, fmt.Sprintf("calibrated P(up) %.2f is near a coin-flip — one weak, non-decisive vote", in.CalProb)
}

// relStrengthVote reads the cross-sectional ranking percentile: top decile-ish
// (≥relStrongPct) is a long vote, bottom (≤relWeakPct) a short vote, the middle
// a present-but-neutral 0.
func relStrengthVote(in Inputs) (int, bool, string) {
	if !in.RankPresent {
		return 0, false, ""
	}
	pctile := in.RankPct * 100
	switch {
	case in.RankPct >= relStrongPct:
		return +1, true, fmt.Sprintf("relative strength in the top of the cross-section (%.0fth pct)", pctile)
	case in.RankPct <= relWeakPct:
		return -1, true, fmt.Sprintf("relative strength in the bottom of the cross-section (%.0fth pct)", pctile)
	default:
		return 0, true, fmt.Sprintf("relative strength mid-pack (%.0fth pct)", pctile)
	}
}

// breakoutVote reads the freshest trend-creation event. Only a breakout no older
// than breakoutFreshDays counts; a stale or absent one drops the family out.
// Donchian breaks and squeeze releases carry a direction (via BreakoutDir);
// a directionless kind (e.g. a volume spike) is a present-but-neutral 0.
func breakoutVote(in Inputs) (int, bool, string) {
	if !in.BreakoutPresent || in.BreakoutAgeDays > breakoutFreshDays {
		return 0, false, "" // no FRESH breakout → the family is absent, not a fake neutral
	}
	eligible := in.BreakoutKind == "donchian_up" ||
		in.BreakoutKind == "donchian_down" ||
		in.BreakoutKind == "squeeze_release"
	if !eligible {
		return 0, true, fmt.Sprintf("fresh %s (%.0fd) — not a directional trend-creation event",
			in.BreakoutKind, in.BreakoutAgeDays)
	}
	d := isign(in.BreakoutDir)
	if d == 0 {
		return 0, true, fmt.Sprintf("fresh %s (%.0fd), direction unresolved", in.BreakoutKind, in.BreakoutAgeDays)
	}
	return d, true, fmt.Sprintf("fresh %s (%.0fd ago)", in.BreakoutKind, in.BreakoutAgeDays)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// minAgree is the setup's agreeing-family floor, read fresh from the env each
// call (like the papertrade thresholds) so an operator override takes effect
// without a rebuild. A missing/invalid/non-positive value keeps the default.
func minAgree() int {
	if v := os.Getenv("SIGNALDECK_CONFLUENCE_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMinAgree
}

// isign is the integer sign function: -1, 0, or +1.
func isign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}

// bandWords humanizes a smart-money band label for a reason string.
func bandWords(label string) string {
	return strings.ReplaceAll(label, "_", " ")
}

// DirectionWord renders a setup direction as LONG / SHORT / NONE — used by the
// worker and API for event detail strings.
func DirectionWord(dir int) string {
	switch {
	case dir > 0:
		return "LONG"
	case dir < 0:
		return "SHORT"
	default:
		return "NONE"
	}
}
