package fleetmon

import (
	"strings"
	"testing"
)

func liveLayer(name string) Layer {
	return Layer{Name: name, Implemented: true, Live: true, Detail: "d", Evidence: "worker: x"}
}

// THE point of this file: a subsystem that is complete but dormant is a BREACH, not
// a note. Its metrics are simply absent, and absence renders as a quiet gap.
func TestBuiltButNotLiveIsABreach(t *testing.T) {
	layers := []Layer{
		liveLayer("risk engine"),
		{Name: "confidence engine", Implemented: true, Live: false, Detail: "d"},
	}
	cov, breaches := SummarizeLayers(layers)
	if cov.BuiltNotLive != 1 {
		t.Errorf("want 1 built-not-live, got %d", cov.BuiltNotLive)
	}
	if len(breaches) != 1 {
		t.Fatalf("want exactly 1 breach, got %v", breaches)
	}
	if !strings.Contains(breaches[0], "nothing calls it") {
		t.Errorf("the breach should name the problem: %s", breaches[0])
	}
	if !strings.Contains(breaches[0], "absence is not health") {
		t.Errorf("the breach should state why it matters: %s", breaches[0])
	}
}

// A live layer that enforces nothing is the subtler version of the same problem.
func TestLiveButNonEnforcingIsABreach(t *testing.T) {
	l := liveLayer("risk engine")
	l.Enforcing = Bool(false)
	_, breaches := SummarizeLayers([]Layer{l})
	if len(breaches) != 1 || !strings.Contains(breaches[0], "enforces nothing") {
		t.Errorf("want an enforcement breach, got %v", breaches)
	}

	l.Enforcing = Bool(true)
	if _, b := SummarizeLayers([]Layer{l}); len(b) != 0 {
		t.Errorf("an enforcing layer should breach nothing, got %v", b)
	}
}

// A layer with no enforcement dimension (a classifier) must not be judged on one.
func TestNilEnforcingIsNotJudged(t *testing.T) {
	l := liveLayer("regime detection") // Enforcing left nil
	if _, breaches := SummarizeLayers([]Layer{l}); len(breaches) != 0 {
		t.Errorf("a classifier should not be graded on enforcement, got %v", breaches)
	}
}

// A deliberately-unbuilt layer is counted and reported, not hidden. Deliberate
// absence is still absence.
func TestNotImplementedIsCountedNotBreached(t *testing.T) {
	layers := []Layer{
		liveLayer("research engine"),
		{Name: "model zoo", Implemented: false, Live: false, Detail: "deliberately narrow"},
	}
	cov, breaches := SummarizeLayers(layers)
	if cov.NotImplemented != 1 {
		t.Errorf("want 1 not-implemented, got %d", cov.NotImplemented)
	}
	if cov.Implemented != 1 || cov.LiveCount != 1 {
		t.Errorf("counts wrong: implemented=%d live=%d", cov.Implemented, cov.LiveCount)
	}
	// An unbuilt layer is not a breach — a decision is not a defect.
	if len(breaches) != 0 {
		t.Errorf("a deliberately unbuilt layer must not breach, got %v", breaches)
	}
	if !strings.Contains(cov.Note, "1 not implemented") {
		t.Errorf("the note should carry the count: %s", cov.Note)
	}
}

// Layer breaches reach the assembled snapshot at DEGRADED severity — a dormant
// subsystem is a gap in what the platform can see, not the platform actively doing
// the wrong thing.
func TestDormantLayerDegradesTheSnapshot(t *testing.T) {
	layers := []Layer{
		liveLayer("risk engine"),
		{Name: "monitoring", Implemented: true, Live: false, Detail: "d"},
	}
	s := Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, healthySystem(), layers, DefaultThresholds())
	if s.Status != StatusDegraded {
		t.Errorf("want degraded from the dormant layer, got %q: %v", s.Status, s.Breaches)
	}
	if s.Layers.BuiltNotLive != 1 {
		t.Errorf("coverage should reach the snapshot, got %+v", s.Layers)
	}

	// All live: back to healthy.
	s = Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, healthySystem(),
		[]Layer{liveLayer("risk engine"), liveLayer("monitoring")}, DefaultThresholds())
	if s.Status != StatusHealthy {
		t.Errorf("all-live layers should not degrade the fleet, got %q: %v", s.Status, s.Breaches)
	}
	if s.Layers.LiveCount != 2 {
		t.Errorf("want 2 live layers counted, got %d", s.Layers.LiveCount)
	}
}

// A dormant layer must not be able to escalate the fleet to critical on its own.
func TestDormantLayerNeverCritical(t *testing.T) {
	var layers []Layer
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		layers = append(layers, Layer{Name: n, Implemented: true, Live: false, Detail: "d"})
	}
	s := Assemble([]Trading{healthyTrading()}, []Model{healthyModel()}, healthySystem(), layers, DefaultThresholds())
	if s.Status == StatusCritical {
		t.Error("dormant subsystems are degraded, not critical — the platform is still serving")
	}
	if s.Status != StatusDegraded {
		t.Errorf("want degraded, got %q", s.Status)
	}
}
