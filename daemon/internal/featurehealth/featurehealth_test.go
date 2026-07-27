package featurehealth

import (
	"strings"
	"testing"
)

func strongFeature(name string) Inputs {
	return Inputs{
		Name: name, N: 500,
		FullIC: 0.08, RecentIC: 0.075, RecentN: 100, RecentKnown: true,
		BlockICs: []float64{0.07, 0.08, 0.09, 0.06, 0.08},
		Coverage: 0.98,
	}
}

func verdictOf(t *testing.T, in Inputs) Score {
	t.Helper()
	return Grade(in)
}

// A new feature is never retired for being new.
func TestProvisionalFeatureIsKept(t *testing.T) {
	in := strongFeature("fresh")
	in.N = MinObservations - 1
	s := verdictOf(t, in)
	if s.Verdict != VerdictProvisional {
		t.Errorf("want provisional, got %q", s.Verdict)
	}
	if !s.Keep {
		t.Error("a feature below the evidence floor must be KEPT, not dropped")
	}
}

func TestStrongStableFeatureIsHealthy(t *testing.T) {
	s := verdictOf(t, strongFeature("momentum_20d"))
	if s.Verdict != VerdictHealthy {
		t.Errorf("want healthy, got %q (overall %.3f, %v)", s.Verdict, s.Overall, s.Reasons)
	}
	if !s.Keep {
		t.Error("a healthy feature must be kept")
	}
}

// A feature with no measurable relationship is retired.
func TestDeadFeatureIsRetired(t *testing.T) {
	in := strongFeature("dead")
	in.FullIC = 0.0005
	in.RecentIC = 0.0004
	in.BlockICs = []float64{0.001, -0.0008, 0.0002, -0.0004, 0.0001}
	s := verdictOf(t, in)
	if s.Keep {
		t.Errorf("a feature with no IC must be dropped, got %q (overall %.3f)", s.Verdict, s.Overall)
	}
	if s.Verdict != VerdictRetired {
		t.Errorf("want retired, got %q", s.Verdict)
	}
}

// A duplicate is retired no matter how strong it looks — its strength belongs to
// its representative.
func TestRedundantFeatureIsRetiredEvenWhenStrong(t *testing.T) {
	in := strongFeature("dupe")
	in.FullIC = 0.30 // very strong
	in.Redundant = true
	s := verdictOf(t, in)
	if s.Keep {
		t.Error("a clustered duplicate must be dropped even when strong")
	}
	if !strings.Contains(strings.Join(s.Reasons, " "), "double-counts") {
		t.Errorf("the reason should explain the double-count: %v", s.Reasons)
	}
}

// A sign flip is scored as a broken relationship, not as mild weakening.
func TestSignFlipScoresZeroDecay(t *testing.T) {
	in := strongFeature("flipped")
	in.RecentIC = -0.07 // was +0.08 over the record
	s := verdictOf(t, in)
	if got := s.Components["decay"]; got != 0 {
		t.Errorf("a sign flip must score 0 decay, got %v", got)
	}
	joined := strings.Join(s.Reasons, " ")
	if !strings.Contains(joined, "reversed") {
		t.Errorf("the flip should be named plainly: %s", joined)
	}
	// And it must score materially worse than the same feature un-flipped.
	if s.Overall >= verdictOf(t, strongFeature("flipped")).Overall {
		t.Error("a flipped feature must score below its stable twin")
	}
}

// Decaying strength drags the score down without needing a flip.
func TestDecayLowersTheScore(t *testing.T) {
	stable := verdictOf(t, strongFeature("stable"))
	in := strongFeature("decaying")
	in.RecentIC = 0.01 // an eighth of its full-record IC
	decaying := verdictOf(t, in)
	if decaying.Overall >= stable.Overall {
		t.Errorf("a decaying feature must score below a stable one: %.3f vs %.3f",
			decaying.Overall, stable.Overall)
	}
	if !strings.Contains(strings.Join(decaying.Reasons, " "), "decaying") {
		t.Errorf("decay should be stated: %v", decaying.Reasons)
	}
}

// Unmeasured windows score NEUTRAL, not good and not bad, and say so.
func TestUnmeasuredTermsAreNeutralAndDisclosed(t *testing.T) {
	in := strongFeature("partial")
	in.RecentKnown = false
	in.BlockICs = nil
	s := verdictOf(t, in)
	if got := s.Components["decay"]; got != 0.5 {
		t.Errorf("unmeasured decay should be neutral 0.5, got %v", got)
	}
	if got := s.Components["stability"]; got != 0.5 {
		t.Errorf("unmeasured stability should be neutral 0.5, got %v", got)
	}
	joined := strings.Join(s.Reasons, " ")
	if !strings.Contains(joined, "not measured") {
		t.Errorf("unmeasured terms must be disclosed: %s", joined)
	}
}

// Inconsistent block signs are noise, and a coin-flip split must score 0 — not
// the 0.5 that raw agreement would report.
func TestCoinFlipStabilityScoresZero(t *testing.T) {
	got, reason := stabilityScore([]float64{0.05, -0.05, 0.05, -0.05})
	if got != 0 {
		t.Errorf("a 50/50 sign split must score 0 stability, got %v", got)
	}
	if reason == "" {
		t.Error("an unstable feature should explain itself")
	}
	if unan, _ := stabilityScore([]float64{0.05, 0.04, 0.06, 0.05}); unan != 1 {
		t.Errorf("unanimous signs should score 1, got %v", unan)
	}
}

// Drift is a penalty applied only when measured.
func TestDriftPenaltyOnlyWhenMeasured(t *testing.T) {
	base := verdictOf(t, strongFeature("d"))

	unmeasured := strongFeature("d")
	unmeasured.DriftPct = 0.9 // set but not Known
	if verdictOf(t, unmeasured).Overall != base.Overall {
		t.Error("an unmeasured drift must not be applied")
	}

	measured := strongFeature("d")
	measured.DriftPct = 0.9
	measured.DriftKnown = true
	drifted := verdictOf(t, measured)
	if drifted.Overall >= base.Overall {
		t.Errorf("measured drift must lower the score: %.3f vs %.3f", drifted.Overall, base.Overall)
	}
	if !strings.Contains(strings.Join(drifted.Reasons, " "), "distribution has moved") {
		t.Errorf("heavy drift should be stated: %v", drifted.Reasons)
	}
}

// The keep/retire sets are the actionable output.
func TestAnalyzeSplitsKeepAndRetire(t *testing.T) {
	dead := strongFeature("zzz_dead")
	dead.FullIC = 0.0001
	dead.RecentIC = 0.0001
	dead.BlockICs = []float64{0.0001, -0.0001, 0.0002, -0.0002, 0.0001}

	r := Analyze([]Inputs{strongFeature("aaa_good"), strongFeature("bbb_good"), dead})
	if len(r.Keep) != 2 || r.Keep[0] != "aaa_good" || r.Keep[1] != "bbb_good" {
		t.Errorf("keep set wrong: %v", r.Keep)
	}
	if len(r.Retire) != 1 || r.Retire[0] != "zzz_dead" {
		t.Errorf("retire set wrong: %v", r.Retire)
	}
	if r.RetiredPct <= 0 {
		t.Error("retired share should be reported")
	}
}

// THE safety valve: a wholesale retirement points at a broken label, not at every
// feature dying at once, so it is reported and NOT applied.
func TestWholesaleRetirementIsReportedNotApplied(t *testing.T) {
	var ins []Inputs
	for _, n := range []string{"a", "b", "c", "d"} {
		f := strongFeature(n)
		f.FullIC = 0.0001 // everything grades out together
		f.RecentIC = 0.0001
		f.BlockICs = []float64{0.0001, -0.0001, 0.0002, -0.0002, 0.0001}
		ins = append(ins, f)
	}
	r := Analyze(ins)
	if len(r.Retire) != 0 {
		t.Errorf("a wholesale retirement must NOT be applied, got retire=%v", r.Retire)
	}
	if len(r.Keep) != 4 {
		t.Errorf("everything must be kept pending investigation, got keep=%v", r.Keep)
	}
	if !strings.Contains(r.Note, "forward-return label") {
		t.Errorf("the note should name the likely cause: %s", r.Note)
	}
	// The verdicts themselves still stand as a report.
	dropped := 0
	for _, s := range r.Scores {
		if s.Verdict == VerdictRetired {
			dropped++
		}
	}
	if dropped != 4 {
		t.Errorf("the verdicts should still be reported, got %d retired verdicts", dropped)
	}
}

// The bands and evidence floor must match modelhealth's so one word means one
// thing platform-wide.
func TestThresholdsMatchModelHealthVocabulary(t *testing.T) {
	if MinObservations != 30 {
		t.Errorf("evidence floor drifted from modelhealth's 30: %d", MinObservations)
	}
	if RetireBelow != 0.35 || DegradedBelow != 0.55 || WatchBelow != 0.70 {
		t.Error("score bands drifted from modelhealth's")
	}
}
