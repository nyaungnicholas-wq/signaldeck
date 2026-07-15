package recommendation

import (
	"math"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// wantRoles is the exact, ordered nine-agent panel Build must always emit.
var wantRoles = []string{
	"Economist", "Equity Analyst", "Quantitative Researcher", "Technical Analyst",
	"News Analyst", "Risk Manager", "Portfolio Manager", "Compliance Checker",
	"Fact Verification Agent",
}

// bullishHigh is a clean, PROVEN, strong-accuracy bullish read: a top rank with
// four bullish tiles, one bearish, one context regime, EPS + price present.
func bullishHigh() Inputs {
	conv := composite.Assess(composite.ConvictionInputs{
		Edge: 0.06, NUsed: 4, EdgeProvenLive: true, WinRate: 0.60, Bull: 4, Bear: 1,
	})
	return Inputs{
		Symbol: "AAPL", Market: "stocks",
		HasScore: true, Score: 9, Edge: 0.06, CalProb: 0.56, CurvePct: 92, NUsed: 4,
		Factors: []composite.Factor{
			{Key: composite.FactorTechnical, Verdict: 1, Evidence: "pressure score +0.40 (1d) → P(up) leg 70.0%"},
			{Key: composite.FactorExpectancy, Verdict: 1, Evidence: "current state's measured hit rate: 63% positive"},
			{Key: composite.FactorRanking, Verdict: 1, Evidence: "cross-sectional relative-strength percentile 88/100"},
			{Key: composite.FactorSentiment, Verdict: 1, Evidence: "news sentiment +0.30 → P(up) leg 58.0%"},
			{Key: composite.FactorMeanRev, Verdict: -1, Evidence: "mean-reversion model P(up) 40.0% with OOS lift +0.02"},
			{Key: composite.FactorRegime, Verdict: 0, Evidence: `regime "risk_on" — context only, never scored`},
		},
		Conviction:          conv,
		MeasuredAccuracyPct: 58.0,
		EdgeProvenLive:      true,
		HasPrice:            true, Price: 190.0,
		HasEPS: true, EPS: 6.50, PeerPE: 20.0,
		Regime:  "risk_on",
		HasRank: true, RankPct: 88,
		MacroSummary: "Fed on hold; disinflation intact; equities firm",
	}
}

func rolesInOrder(t *testing.T, rec Recommendation) {
	t.Helper()
	if len(rec.Agents) != 9 {
		t.Fatalf("want exactly 9 agents, got %d", len(rec.Agents))
	}
	for i, want := range wantRoles {
		if rec.Agents[i].Role != want {
			t.Fatalf("agent %d: role %q, want %q", i, rec.Agents[i].Role, want)
		}
	}
}

func agentView(rec Recommendation, role string) (AgentView, bool) {
	for _, a := range rec.Agents {
		if a.Role == role {
			return a, true
		}
	}
	return AgentView{}, false
}

// A bullish, high-conviction read is a Buy, its drivers are the bullish tiles,
// the panel is the exact nine, and the PM's net stance is bullish.
func TestBuildBullishHighIsBuy(t *testing.T) {
	rec := Build(bullishHigh())

	if rec.Decision != DecisionBuy {
		t.Fatalf("Decision = %q, want %q", rec.Decision, DecisionBuy)
	}
	if rec.ConfidenceLabel != "HIGH conviction" {
		t.Fatalf("ConfidenceLabel = %q, want HIGH conviction", rec.ConfidenceLabel)
	}
	// Drivers must be exactly the bullish, ungated tiles' evidence.
	if len(rec.KeyDrivers) == 0 {
		t.Fatal("expected key drivers from the bullish tiles, got none")
	}
	bullEvidence := map[string]bool{}
	for _, f := range bullishHigh().Factors {
		if f.Verdict > 0 && !f.Gated {
			bullEvidence[f.Evidence] = true
		}
	}
	for _, d := range rec.KeyDrivers {
		if !bullEvidence[d] {
			t.Fatalf("driver %q is not a bullish-tile evidence line", d)
		}
	}
	rolesInOrder(t, rec)

	pm, ok := agentView(rec, RolePortfolioMgr)
	if !ok || pm.Stance != stanceBull {
		t.Fatalf("PM stance = %q (found=%v), want bullish", pm.Stance, ok)
	}
	if rec.Disclaimer == "" {
		t.Fatal("disclaimer must always be present")
	}
	if rec.MeasuredAccuracyPct != 58.0 {
		t.Fatalf("MeasuredAccuracyPct = %v, want 58 (pass-through)", rec.MeasuredAccuracyPct)
	}
}

// No EPS → no fair value, no expected return, and both the Equity Analyst and
// the Fact Verification Agent say so.
func TestBuildNoEPS(t *testing.T) {
	in := bullishHigh()
	in.HasEPS = false
	in.EPS = 0
	rec := Build(in)

	if rec.FairValue.Available {
		t.Fatalf("FairValue.Available = true, want false with no EPS")
	}
	if rec.HasExpectedReturn {
		t.Fatalf("HasExpectedReturn = true, want false with no fair value")
	}
	ea, _ := agentView(rec, RoleEquityAnalyst)
	if !strings.Contains(ea.View, "no EPS") {
		t.Fatalf("Equity Analyst view should note no EPS, got %q", ea.View)
	}
	fv, _ := agentView(rec, RoleFactCheck)
	if !strings.Contains(fv.View, "no EPS") {
		t.Fatalf("Fact Verification view should note no EPS, got %q", fv.View)
	}
}

// A coin-flip-sized edge is a Hold regardless of conviction band.
func TestBuildCoinFlipIsHold(t *testing.T) {
	in := bullishHigh()
	in.Edge = 0.005 // inside ±composite.EdgeSlight (0.02)
	if got := Build(in).Decision; got != DecisionHold {
		t.Fatalf("Decision = %q, want %q for a coin-flip edge", got, DecisionHold)
	}
}

// A distribution built from a sample expectancy sums its probabilities to 100,
// bases the median, and prefers the 1d row even when a higher-N non-1d exists;
// an empty expectancy leaves the distribution unavailable.
func TestBuildDistribution(t *testing.T) {
	in := bullishHigh()
	in.Expectancy = []md.Expectancy{
		{Horizon: md.H1d, StateKey: "rsi_low|above_200sma", N: 42, MeanFwd: 0.010, MedianFwd: 0.008, HitRate: 0.60, Stdev: 0.020},
		{Horizon: md.H1h, StateKey: "intraday", N: 100, MeanFwd: 0.001, MedianFwd: 0.001, HitRate: 0.50, Stdev: 0.005},
	}
	d := Build(in).Distribution
	if !d.Available {
		t.Fatal("distribution should be available from a sample expectancy")
	}
	if sum := d.Bull.Prob + d.Base.Prob + d.Bear.Prob; sum != 100 {
		t.Fatalf("probabilities sum to %v, want 100", sum)
	}
	if d.Base.Prob != 50 {
		t.Fatalf("base prob = %v, want 50", d.Base.Prob)
	}
	if math.Abs(d.Base.Ret-0.8) > 1e-9 { // 0.008 × 100
		t.Fatalf("base ret = %v, want ≈0.8 (median ×100)", d.Base.Ret)
	}
	// hit rate 0.60 → bull holds 30 of the non-base mass, bear 20.
	if d.Bull.Prob != 30 || d.Bear.Prob != 20 {
		t.Fatalf("bull/bear prob = %v/%v, want 30/20", d.Bull.Prob, d.Bear.Prob)
	}
	if !strings.Contains(d.Source, "N=42") || !strings.Contains(d.Source, "1d") {
		t.Fatalf("source %q should cite the 1d N=42 row, not the higher-N 1h row", d.Source)
	}

	in.Expectancy = nil
	if Build(in).Distribution.Available {
		t.Fatal("empty expectancy → distribution must be unavailable")
	}
}

// No stored score → Watch (there is no read yet).
func TestBuildNoScoreIsWatch(t *testing.T) {
	in := bullishHigh()
	in.HasScore = false
	if got := Build(in).Decision; got != DecisionWatch {
		t.Fatalf("Decision = %q, want %q with no score", got, DecisionWatch)
	}
}

// The agent panel is always the exact nine roles, in order — for a full read
// and for a bare, no-score one alike.
func TestBuildAgentsAlwaysNine(t *testing.T) {
	rolesInOrder(t, Build(bullishHigh()))
	rolesInOrder(t, Build(Inputs{Symbol: "XYZ", Market: "stocks"}))
}

// The edge sign crossed with the conviction band drives the whole action grid.
func TestBuildDecisionGrid(t *testing.T) {
	cases := []struct {
		name    string
		edge    float64
		proven  bool
		winRate float64
		want    string
	}{
		{"bull high → buy", 0.06, true, 0.60, DecisionBuy},
		{"bull moderate → accumulate", 0.06, true, 0.54, DecisionAccumulate},
		{"bull low → watch", 0.06, false, 0.0, DecisionWatch},
		{"bear high → avoid", -0.06, true, 0.60, DecisionAvoid},
		{"bear moderate → reduce", -0.06, true, 0.54, DecisionReduce},
		{"bear low → watch", -0.06, false, 0.0, DecisionWatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conv := composite.Assess(composite.ConvictionInputs{
				Edge: c.edge, NUsed: 4, EdgeProvenLive: c.proven, WinRate: c.winRate,
			})
			in := Inputs{HasScore: true, Edge: c.edge, Conviction: conv}
			if got := Build(in).Decision; got != c.want {
				t.Fatalf("edge %+.2f proven=%v win=%.2f → %q, want %q (band %q)",
					c.edge, c.proven, c.winRate, got, c.want, conv.Band)
			}
		})
	}
}
