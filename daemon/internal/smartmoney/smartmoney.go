// Package smartmoney is the PURE "Smart Money Score" engine (SMART MONEY FACTS
// wave). It turns ALREADY-INGESTED positioning data — SEC Form 4 open-market
// insider trades, FINRA short-interest / Reg SHO short-volume, crypto perp
// funding, and SEC 13F institutional holdings — into ONE transparent,
// decomposed per-symbol score.
//
// HONESTY (the whole point of this package): the score is a read of what
// INFORMED PARTICIPANTS ARE DOING, NOT a price forecast. Every component is a
// bounded [-1,1] factor with an explicit English line and its source, the base
// weights are RENORMALIZED over only the components actually present (absence
// of a source is information — it drops out, it is never imputed as 0), and a
// symbol with no positioning data at all returns Available:false rather than a
// fabricated "neutral". No store or http imports live here on purpose: the
// engine is pure and fully unit-tested, so the number can be audited in
// isolation.
package smartmoney

import (
	"fmt"
	"math"
	"strings"
)

// Base component weights. RENORMALIZED over the present components only (see
// Score) so a symbol missing a source is scored on what IS known, never on an
// imputed zero.
const (
	weightInsider       = 0.40
	weightSqueeze       = 0.35
	weightInstitutional = 0.25
)

// Factor is one decomposed component of the score. Value is bounded to [-1,1];
// Weight is the RENORMALIZED weight actually applied to Value in the final
// score (so Σ Value*Weight over the factors equals Score exactly). Line is the
// plain-English evidence and Source names the public filing behind it.
type Factor struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Line   string  `json:"line"`
	Source string  `json:"source"`
	Value  float64 `json:"value"`
	Weight float64 `json:"weight"`
}

// Inputs is the raw positioning evidence for ONE symbol. Presence is explicit:
// insider is present iff there is any open-market $ activity in the window;
// each squeeze sub-part carries its own gate (DaysToCover<0 or an OK=false
// flag means absent); institutional is present iff InstManagers>0. Absent
// sub-parts drop out of the score rather than counting as zero.
type Inputs struct {
	// Insider — SEC Form 4 OPEN-MARKET (code P/S) over the 90d window.
	InsiderBuys           float64 // dollar open-market buys (code P)
	InsiderSells          float64 // dollar open-market sells (code S)
	InsiderDistinctBuyers int     // distinct insiders who bought in the window
	InsiderSellTx         int     // count of open-market sell transactions

	// Squeeze fuel — one-directional latent upside IF a catalyst hits.
	DaysToCover float64 // FINRA short-interest days-to-cover; <0 = none stored
	ShortVolZ   float64 // Reg SHO short-volume ratio z vs the symbol's own 30d
	ShortVolZOK bool    // false = below the z gate (thin/flat baseline) = absent
	Funding     float64 // crypto perp funding rate (hourly, decimal) — crypto only
	FundingOK   bool    // false = no fresh perp snapshot = absent

	// Institutional — SEC 13F (quarterly, ~45d lagged).
	InstManagers int     // count of notable 13F managers holding it
	InstNotional float64 // summed reported position value (USD)
}

// Result is the scored output. Available is false when NO component was present
// (honest absence — the caller stores/serves nothing rather than a fake score).
type Result struct {
	Score     float64  `json:"score"`
	Label     string   `json:"label"`
	Factors   []Factor `json:"factors"`
	Available bool     `json:"available"`
}

// Payload is the stored evidence blob: the worker marshals it into the score
// row and the API re-serves it typed. Pointers / the DaysToCover<0 sentinel
// carry HONEST ABSENCE of a sub-source (null in the API, never an imputed 0).
type Payload struct {
	Factors        []Factor `json:"factors"`
	DistinctBuyers int      `json:"distinctBuyers"`
	InsiderNet     float64  `json:"insiderNet"`  // open-market buys − sells, USD
	WindowDays     int      `json:"windowDays"`  // insider lookback (90)
	DaysToCover    *float64 `json:"daysToCover"` // nil = no short-interest row
	ShortVolZ      *float64 `json:"shortVolZ"`   // nil = below the z gate
	Funding        *float64 `json:"funding"`     // nil = no fresh perp (crypto only)
	InstManagers   int      `json:"instManagers"`
	InstNotional   float64  `json:"instNotional"`
}

// Score decomposes the inputs into present factors, renormalizes their base
// weights over only what is present, and returns the weighted-mean score with
// its band label. It is a read of POSITIONING, not a forecast.
func Score(in Inputs) Result {
	// built holds a present component before renormalization.
	type built struct {
		key, label, line, source string
		value, baseWeight        float64
	}
	var comps []built

	// ── INSIDER (present iff any open-market $ activity in the window) ──────
	if in.InsiderBuys+in.InsiderSells > 0 {
		netRatio := (in.InsiderBuys - in.InsiderSells) / (in.InsiderBuys + in.InsiderSells)
		value := netRatio
		// A cluster of DISTINCT buyers is stronger evidence than one insider
		// buying the same dollars — but only amplifies genuine net BUYING; it
		// never rescues net selling, and the magnitude stays clamped to 1.
		if netRatio > 0 {
			clusterMult := 1 + 0.25*float64(max(0, in.InsiderDistinctBuyers-1))
			if clusterMult > 1.75 {
				clusterMult = 1.75
			}
			value = clamp(netRatio*clusterMult, -1, 1)
		}
		comps = append(comps, built{
			key: "insider", label: "Insider conviction",
			line: insiderLine(in, netRatio), source: "SEC Form 4",
			value: value, baseWeight: weightInsider,
		})
	}

	// ── SQUEEZE (present iff any short/funding sub-part is available) ───────
	// One-directional fuel in [0,1]: elevated short crowding = latent upside
	// IF a catalyst hits. It is NOT a directional call and never goes negative
	// (a LOW days-to-cover or negative short-volume z is simply "no fuel", not
	// a bearish signal).
	if in.DaysToCover >= 0 || in.ShortVolZOK || in.FundingOK {
		var parts []float64
		if in.DaysToCover >= 0 {
			parts = append(parts, clamp((in.DaysToCover-3)/7, 0, 1))
		}
		if in.ShortVolZOK {
			parts = append(parts, clamp(in.ShortVolZ/3, 0, 1))
		}
		if in.FundingOK {
			// Very NEGATIVE funding = crowded shorts paying to be short.
			parts = append(parts, clamp(-in.Funding*200, 0, 1))
		}
		comps = append(comps, built{
			key: "squeeze", label: "Squeeze fuel",
			line: squeezeLine(in), source: "FINRA Reg SHO + short interest",
			value: mean(parts), baseWeight: weightSqueeze,
		})
	}

	// ── INSTITUTIONAL (present iff any notable 13F manager holds it) ───────
	// Deliberately SMALL and bounded (≤0.4): holding is not buying, and 13F is
	// quarterly and ~45 days lagged — it is slow context, not a live signal.
	if in.InstManagers > 0 {
		value := clamp(0.15*math.Log1p(float64(in.InstManagers)), 0, 0.4)
		comps = append(comps, built{
			key: "institutional", label: "Institutional ownership",
			line: instLine(in), source: "SEC 13F",
			value: value, baseWeight: weightInstitutional,
		})
	}

	if len(comps) == 0 {
		return Result{Available: false}
	}

	totalBase := 0.0
	for _, c := range comps {
		totalBase += c.baseWeight
	}
	score := 0.0
	factors := make([]Factor, 0, len(comps))
	for _, c := range comps {
		w := c.baseWeight / totalBase // renormalized over present components only
		score += c.value * w
		factors = append(factors, Factor{
			Key: c.key, Label: c.label, Line: c.line, Source: c.source,
			Value: c.value, Weight: w,
		})
	}
	return Result{Score: score, Label: label(score), Factors: factors, Available: true}
}

// label maps a score to its descriptive band. These name POSITIONING behavior
// (accumulation/distribution), never a price prediction.
func label(score float64) string {
	switch {
	case score >= 0.5:
		return "strong_accumulation"
	case score >= 0.2:
		return "accumulation"
	case score > -0.2:
		return "neutral"
	case score > -0.5:
		return "distribution"
	default:
		return "strong_distribution"
	}
}

func insiderLine(in Inputs, netRatio float64) string {
	switch {
	case netRatio > 0:
		return fmt.Sprintf("%d distinct insider%s net-bought %s over 90d (%d sold)",
			in.InsiderDistinctBuyers, plural(in.InsiderDistinctBuyers),
			usd(in.InsiderBuys-in.InsiderSells), in.InsiderSellTx)
	case netRatio < 0:
		return fmt.Sprintf("net insider SELLING %s over 90d (%d sell transaction%s)",
			usd(in.InsiderSells-in.InsiderBuys), in.InsiderSellTx, plural(in.InsiderSellTx))
	default:
		return "insider open-market buying and selling balanced over 90d"
	}
}

func squeezeLine(in Inputs) string {
	var parts []string
	if in.DaysToCover >= 0 {
		parts = append(parts, fmt.Sprintf("days-to-cover %.1f", in.DaysToCover))
	}
	if in.ShortVolZOK {
		parts = append(parts, fmt.Sprintf("short-volume %+.1fσ vs own 30d", in.ShortVolZ))
	}
	if in.FundingOK {
		parts = append(parts, fmt.Sprintf("perp funding %+.4f%%/hr", in.Funding*100))
	}
	return strings.Join(parts, ", ") + " — latent squeeze fuel (needs a catalyst; not a directional call)"
}

func instLine(in Inputs) string {
	return fmt.Sprintf("held by %d notable 13F manager%s, %s notional (quarterly, ~45d lagged)",
		in.InstManagers, plural(in.InstManagers), usd(in.InstNotional))
}

// clamp constrains v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// usd formats a dollar amount compactly ($1.2B / $3.4M / $56K / $789).
func usd(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e9:
		return fmt.Sprintf("$%.1fB", v/1e9)
	case a >= 1e6:
		return fmt.Sprintf("$%.1fM", v/1e6)
	case a >= 1e3:
		return fmt.Sprintf("$%.0fK", v/1e3)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}
