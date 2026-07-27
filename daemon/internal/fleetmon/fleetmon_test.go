package fleetmon

import (
	"strings"
	"testing"
)

func healthyTrading() Trading {
	return Trading{
		Strategy:    "flagship-1d",
		TotalReturn: Ptr(0.12), Sharpe: Ptr(1.1), Sortino: Ptr(1.5),
		MaxDrawdown: Ptr(0.08), CurrentDrawdown: Ptr(0.01),
		HitRate: Ptr(0.58), ProfitFactor: Ptr(1.6), Turnover: Ptr(2.0),
		ClosedTrades: 40, EquityMarks: 300,
	}
}

func healthyModel() Model {
	return Model{
		Name: "gbm-1d", Verdict: "healthy", Overall: Ptr(0.82),
		Accuracy: Ptr(0.61), BrierSkill: Ptr(0.04), CalibrationErr: Ptr(0.02),
		IC: Ptr(0.05), FeatureDrift: Ptr(0.05),
		Observations: 400, Emitting: true,
	}
}

func healthySystem() System {
	return System{TotalWorkers: 20, DataAgeSeconds: Ptr(3600)}
}

func breachesJoined(s Snapshot) string { return strings.Join(s.Breaches, " | ") }

func TestHealthyFleetIsHealthy(t *testing.T) {
	s := Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, healthySystem(), nil, DefaultThresholds())
	if s.Status != StatusHealthy {
		t.Errorf("want healthy, got %q: %s", s.Status, breachesJoined(s))
	}
	if len(s.Breaches) != 0 {
		t.Errorf("a healthy fleet should breach nothing, got %v", s.Breaches)
	}
	if len(s.Withheld) != 0 {
		t.Errorf("everything was measured, so nothing should be withheld: %v", s.Withheld)
	}
}

// An empty fleet must read UNKNOWN, never healthy. A monitoring surface that
// renders "no data" as green is the failure mode worth designing against.
func TestNothingMeasuredIsUnknownNotHealthy(t *testing.T) {
	s := Assemble(nil, nil, System{}, nil, DefaultThresholds())
	if s.Status != StatusUnknown {
		t.Errorf("want unknown, got %q", s.Status)
	}
	if s.Status == StatusHealthy {
		t.Fatal("an absent reading must never render as healthy")
	}
	if !strings.Contains(s.Note, "NOT a healthy reading") {
		t.Errorf("the note must say so explicitly: %s", s.Note)
	}
}

// A deep drawdown is critical, and the halt it causes must appear in Disabled so
// auto-disable is visible rather than inferred.
func TestDeepDrawdownIsCriticalAndReportsTheHalt(t *testing.T) {
	tr := healthyTrading()
	tr.CurrentDrawdown = Ptr(0.25)
	s := Assemble([]Trading{tr}, []Model{healthyModel()}, healthySystem(), nil, DefaultThresholds())
	if s.Status != StatusCritical {
		t.Errorf("want critical, got %q", s.Status)
	}
	if len(s.Disabled) == 0 {
		t.Error("a tripped breaker must be listed in Disabled")
	}
	if !strings.Contains(strings.Join(s.Disabled, " "), "halted") {
		t.Errorf("Disabled should name the halt: %v", s.Disabled)
	}
}

// The monitor's red line must equal riskgate's halt, so the two cannot disagree
// in public about what "too deep" means.
func TestCriticalDrawdownMatchesTheRiskGateHalt(t *testing.T) {
	if got := DefaultThresholds().CriticalDrawdown; got != 0.20 {
		t.Errorf("critical drawdown %v drifted from riskgate's 0.20 default halt", got)
	}
}

// A losing book is degraded, not critical — it is still functioning.
func TestLosingBookIsDegraded(t *testing.T) {
	tr := healthyTrading()
	tr.ProfitFactor = Ptr(0.7)
	tr.Sortino = Ptr(-0.4)
	s := Assemble([]Trading{tr}, []Model{healthyModel()}, healthySystem(), nil, DefaultThresholds())
	if s.Status != StatusDegraded {
		t.Errorf("want degraded, got %q: %s", s.Status, breachesJoined(s))
	}
	joined := breachesJoined(s)
	if !strings.Contains(joined, "below break-even") || !strings.Contains(joined, "Sortino") {
		t.Errorf("both breaches should be named: %s", joined)
	}
}

// A retired model is critical and disabled.
func TestRetiredModelIsCriticalAndDisabled(t *testing.T) {
	m := healthyModel()
	m.Verdict = "retired"
	m.Emitting = false
	s := Assemble([]Trading{healthyTrading()}, []Model{m}, healthySystem(), nil, DefaultThresholds())
	if s.Status != StatusCritical {
		t.Errorf("want critical, got %q", s.Status)
	}
	if !strings.Contains(strings.Join(s.Disabled, " "), "no longer emitting") {
		t.Errorf("the retirement should be listed as disabled: %v", s.Disabled)
	}
}

// THE contradiction worth catching: a model graded retired that is still
// emitting. Either state alone is survivable; the disagreement is not.
func TestRetiredButStillEmittingIsCalledOut(t *testing.T) {
	m := healthyModel()
	m.Verdict = "retired"
	m.Emitting = true
	s := Assemble([]Trading{healthyTrading()}, []Model{m}, healthySystem(), nil, DefaultThresholds())
	if s.Status != StatusCritical {
		t.Errorf("want critical, got %q", s.Status)
	}
	if !strings.Contains(breachesJoined(s), "STILL EMITTING") {
		t.Errorf("the contradiction must be named plainly: %s", breachesJoined(s))
	}
}

// Stale data is critical: everything downstream is predicting from history.
func TestStaleDataIsCritical(t *testing.T) {
	sys := healthySystem()
	sys.DataAgeSeconds = Ptr(10 * 24 * 3600) // ten days
	s := Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, sys, nil, DefaultThresholds())
	if s.Status != StatusCritical {
		t.Errorf("want critical, got %q", s.Status)
	}
	if !strings.Contains(breachesJoined(s), "from history") {
		t.Errorf("the consequence should be stated: %s", breachesJoined(s))
	}
}

// A long weekend is not an outage.
func TestNormalMarketClosureIsNotAnOutage(t *testing.T) {
	sys := healthySystem()
	sys.DataAgeSeconds = Ptr(3 * 24 * 3600) // Friday close to Monday morning
	s := Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, sys, nil, DefaultThresholds())
	if s.Status != StatusHealthy {
		t.Errorf("a 3-day gap should not be a breach, got %q: %s", s.Status, breachesJoined(s))
	}
}

// One stale worker is degraded; a third of the fleet is systemic.
func TestStaleWorkerFractionEscalates(t *testing.T) {
	sys := healthySystem()
	sys.StaleWorkers = []string{"paper-trader"}
	s := Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, sys, nil, DefaultThresholds())
	if s.Status != StatusDegraded {
		t.Errorf("one stale worker should be degraded, got %q", s.Status)
	}

	sys.StaleWorkers = make([]string, 8) // 8 of 20 = 40%
	for i := range sys.StaleWorkers {
		sys.StaleWorkers[i] = "w"
	}
	s = Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, sys, nil, DefaultThresholds())
	if s.Status != StatusCritical {
		t.Errorf("40%% of the fleet stale should be critical, got %q", s.Status)
	}
	if !strings.Contains(breachesJoined(s), "the fleet is behind") {
		t.Errorf("the systemic framing should be stated: %s", breachesJoined(s))
	}
}

// Unmeasurable metrics are named, not shown as zero.
func TestUnmeasurableMetricsAreWithheldAndNamed(t *testing.T) {
	tr := Trading{Strategy: "young-book", ClosedTrades: 2}
	s := Assemble([]Trading{tr}, nil, System{}, nil, DefaultThresholds())

	joined := strings.Join(s.Withheld, " | ")
	for _, want := range []string{"currentDrawdown", "profitFactor", "sortino"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s should be named in Withheld: %s", want, joined)
		}
	}
	// And a young book must not be reported as breaching anything — absence of a
	// measurement is not evidence of a problem.
	if len(s.Breaches) != 0 {
		t.Errorf("nothing was measured, so nothing can breach: %v", s.Breaches)
	}
	if s.Status != StatusUnknown {
		t.Errorf("want unknown for a fully unmeasured fleet, got %q", s.Status)
	}
	// The reason must carry the sample size that caused the withholding.
	if !strings.Contains(joined, "2 closed") {
		t.Errorf("the withheld reason should carry the sample size: %s", joined)
	}
}

// Partial thresholds inherit the documented defaults rather than reading as zero.
func TestWithDefaultsFillsUnsetThresholds(t *testing.T) {
	tr := healthyTrading()
	tr.CurrentDrawdown = Ptr(0.25)
	s := Assemble([]Trading{tr}, nil, System{}, nil, Thresholds{})
	if s.Status != StatusCritical {
		t.Error("a zero Thresholds must inherit the default drawdown line, not disable it")
	}
}
