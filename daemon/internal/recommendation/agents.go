// The nine deterministic agent views — a multi-agent PANEL rendered from the
// SAME stored inputs, not from any LLM. Each role is a fixed lens: it cites the
// real numbers it can see and takes an honest stance (data agents by the sign of
// their own signal, the Portfolio Manager by the net decision, and the
// Compliance / Fact-Verification / Economist / Risk roles neutral because their
// job is to check and frame, not to call direction). Build always emits all
// nine, in this order.
package recommendation

import (
	"fmt"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
)

// The fixed agent roles, in the order Build emits them.
const (
	RoleEconomist       = "Economist"
	RoleEquityAnalyst   = "Equity Analyst"
	RoleQuantResearcher = "Quantitative Researcher"
	RoleTechnical       = "Technical Analyst"
	RoleNews            = "News Analyst"
	RoleRiskManager     = "Risk Manager"
	RolePortfolioMgr    = "Portfolio Manager"
	RoleCompliance      = "Compliance Checker"
	RoleFactCheck       = "Fact Verification Agent"
)

// agentViews renders exactly nine views in the fixed role order.
func agentViews(in Inputs, rec Recommendation) []AgentView {
	return []AgentView{
		economistView(in),
		equityAnalystView(in, rec),
		quantView(in),
		technicalView(in),
		newsView(in),
		riskView(in),
		pmView(in, rec),
		complianceView(),
		factCheckView(in, rec),
	}
}

// economistView frames the read with the live macro line and the regime. Macro
// context is not a directional call, so the stance is neutral.
func economistView(in Inputs) AgentView {
	parts := []string{}
	if in.MacroSummary != "" {
		parts = append(parts, in.MacroSummary)
	} else {
		parts = append(parts, "no live macro snapshot on file")
	}
	if in.Regime != "" {
		parts = append(parts, fmt.Sprintf("current regime is %q (context only — it frames the read, it is not a directional call)", in.Regime))
	} else {
		parts = append(parts, "no regime classified yet")
	}
	return AgentView{Role: RoleEconomist, Stance: stanceNeutral, View: join(parts)}
}

// equityAnalystView compares the heuristic fair value to the live price. When
// there is no EPS (ETF/crypto/unfiled) it says so plainly — the name is judged
// on measured signals, not multiples.
func equityAnalystView(in Inputs, rec Recommendation) AgentView {
	if !rec.FairValue.Available {
		reason := "no EPS on file (ETF, crypto, or unfiled) — no fundamental fair value, so this name is judged on measured signals, not multiples"
		if in.HasEPS && in.EPS <= 0 {
			reason = fmt.Sprintf("latest EPS is %.2f (non-positive) — no meaningful P/E fair value; judged on measured signals instead", in.EPS)
		}
		return AgentView{Role: RoleEquityAnalyst, Stance: stanceNeutral, View: reason + "."}
	}
	stance := stanceNeutral
	if rec.HasExpectedReturn {
		if rec.ExpectedReturnPct > 0 {
			stance = stanceBull
		} else if rec.ExpectedReturnPct < 0 {
			stance = stanceBear
		}
	}
	view := fmt.Sprintf("heuristic fair value $%.2f from %s", rec.FairValue.Value, rec.FairValue.Method)
	if in.HasPrice {
		view += fmt.Sprintf("; price is $%.2f", in.Price)
		if rec.HasExpectedReturn {
			view += fmt.Sprintf(" (%+.1f%% vs the heuristic — a rough multiples check, not a price target)", rec.ExpectedReturnPct)
		}
	}
	return AgentView{Role: RoleEquityAnalyst, Stance: stance, View: view + "."}
}

// quantView reads the composite rank, edge, and — crucially — the MEASURED live
// accuracy that ceilings any edge. Its stance is the edge sign (neutral inside
// coin-flip range or with no score).
func quantView(in Inputs) AgentView {
	if !in.HasScore {
		return AgentView{Role: RoleQuantResearcher, Stance: stanceNeutral, View: "no composite score stored for this symbol yet — nothing to rank."}
	}
	view := fmt.Sprintf("composite score %d/10 (curve pct %.0f), edge %+.1fpp (calibrated P(up) %.1f%%)", in.Score, in.CurvePct, in.Edge*100, in.CalProb*100)
	if in.HasRank {
		view += fmt.Sprintf(", relative-strength rank %.0f/100", in.RankPct)
	}
	if !in.EdgeProvenLive {
		view += "; the model's edge is not yet proven on live out-of-sample results"
	}
	if in.MeasuredAccuracyPct > 0 {
		view += fmt.Sprintf("; measured live accuracy is %.1f%%, the honest ceiling on this edge", in.MeasuredAccuracyPct)
	} else {
		view += "; live accuracy is not yet measured, so treat the edge cautiously"
	}
	return AgentView{Role: RoleQuantResearcher, Stance: stanceFromEdge(in.Edge, true), View: view + "."}
}

// technicalView reads the pressure (technical) tile plus any recent breakout;
// its stance is the combined verdict sign (a conflict nets to neutral).
func technicalView(in Inputs) AgentView {
	tech, hasTech := factorByKey(in.Factors, composite.FactorTechnical)
	brk, hasBrk := factorByKey(in.Factors, composite.FactorBreakout)
	verdict := 0
	parts := []string{}
	if hasTech && !tech.Gated {
		verdict += tech.Verdict
		if tech.Evidence != "" {
			parts = append(parts, tech.Evidence)
		}
	}
	if hasBrk && !brk.Gated {
		verdict += brk.Verdict
		if brk.Evidence != "" {
			parts = append(parts, brk.Evidence)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no live pressure or breakout signal")
	}
	return AgentView{Role: RoleTechnical, Stance: stanceFromVerdict(verdict), View: join(parts)}
}

// newsView reads the sentiment tile; absent fresh rated headlines it stays
// neutral and says so.
func newsView(in Inputs) AgentView {
	if f, ok := factorByKey(in.Factors, composite.FactorSentiment); ok && !f.Gated && f.Evidence != "" {
		return AgentView{Role: RoleNews, Stance: stanceFromVerdict(f.Verdict), View: f.Evidence + "."}
	}
	return AgentView{Role: RoleNews, Stance: stanceNeutral, View: "no fresh rated headlines within the sentiment window — no news tilt."}
}

// riskView is the cautionary lens: the conviction band and its every driver
// (which already name overconfidence, staleness, thin blends, and factor
// disagreement), the regime, the short-volume caveat, and the persistent risk
// note. Always neutral — its job is to temper, not to call direction.
func riskView(in Inputs) AgentView {
	parts := []string{in.Conviction.Label}
	parts = append(parts, in.Conviction.Drivers...)
	if in.Regime != "" {
		parts = append(parts, fmt.Sprintf("regime %q frames position sizing", in.Regime))
	}
	if f, ok := factorByKey(in.Factors, composite.FactorShortVol); ok && !f.Gated && f.Evidence != "" {
		parts = append(parts, f.Evidence)
	}
	if in.Conviction.RiskNote != "" {
		parts = append(parts, in.Conviction.RiskNote)
	}
	return AgentView{Role: RoleRiskManager, Stance: stanceNeutral, View: join(parts)}
}

// pmView is the final word: it synthesizes the panel into the decision, the
// conviction label, and the measured accuracy. Its stance is the net decision.
func pmView(in Inputs, rec Recommendation) AgentView {
	acc := "live accuracy not yet measured"
	if in.MeasuredAccuracyPct > 0 {
		acc = fmt.Sprintf("measured live accuracy %.1f%%", in.MeasuredAccuracyPct)
	}
	view := fmt.Sprintf("net call: %s at %s (%s)", rec.Decision, in.Conviction.Label, acc)
	switch {
	case !in.HasScore:
		view += " — no stored read yet, so watch only"
	case rec.Decision == DecisionHold:
		view += fmt.Sprintf(" — edge %+.1fpp is inside coin-flip range", in.Edge*100)
	default:
		view += fmt.Sprintf(" — edge %+.1fpp carried at %s", in.Edge*100, in.Conviction.Label)
	}
	return AgentView{Role: RolePortfolioMgr, Stance: overallStance(rec.Decision), View: view + "."}
}

// complianceView confirms the rec ships the mandatory caveats. Always neutral.
func complianceView() AgentView {
	return AgentView{
		Role:   RoleCompliance,
		Stance: stanceNeutral,
		View:   "verified: this recommendation ships the not-advice disclaimer and the backtested-calibration caveat, and frames the score as a relative rank rather than a guarantee — compliant.",
	}
}

// factCheckView states which numbers are sourced vs heuristic — the audit line.
// Always neutral.
func factCheckView(in Inputs, rec Recommendation) AgentView {
	parts := []string{}
	if rec.FairValue.Available {
		parts = append(parts, "fair value is a HEURISTIC (EPS × peer P/E), not a valuation model")
	} else {
		parts = append(parts, "fair value is unavailable — no EPS on file, so none is shown")
	}
	if rec.Distribution.Available {
		parts = append(parts, fmt.Sprintf("bull/base/bear are MEASURED from %s", rec.Distribution.Source))
	} else {
		parts = append(parts, "no historical distribution stored — none is invented")
	}
	if in.MeasuredAccuracyPct > 0 {
		parts = append(parts, fmt.Sprintf("accuracy %.1f%% is MEASURED live, not modeled", in.MeasuredAccuracyPct))
	} else {
		parts = append(parts, "live accuracy is not yet measured")
	}
	parts = append(parts, "the score is a relative cross-sectional rank, not a probability of profit")
	return AgentView{Role: RoleFactCheck, Stance: stanceNeutral, View: join(parts)}
}

// join renders a list of clauses as one sentence ("a; b; c.").
func join(parts []string) string {
	return strings.Join(parts, "; ") + "."
}
