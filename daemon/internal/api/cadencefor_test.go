package api

import (
	"testing"
	"time"
)

// TestCadenceFor_DeclaredIntervalWinsOverObservedGaps verifies that a declared
// interval overrides observed gaps, preventing a daemon restart from collapsing
// the cadence to the boot spacing and causing false staleness.
func TestCadenceFor_DeclaredIntervalWinsOverObservedGaps(t *testing.T) {
	name := "gbm-trainer"
	declared := map[string]time.Duration{name: 6 * time.Hour}
	newestFirst := []int64{5000, 4940, 4880, 4820, 4760}
	got, ok := cadenceFor(name, declared, newestFirst)
	want := int64(6 * time.Hour / time.Second) // 21600
	if !ok || got != want {
		t.Fatalf("declared interval should win: expected %d, true; got %d, %v", want, got, ok)
	}
}

// TestCadenceFor_FallsBackToInferenceWhenNotDeclared ensures that when no
// interval is declared, the function falls back to inferring cadence from
// historical run timestamps, allowing decommissioned workers with history to
// be judged.
func TestCadenceFor_FallsBackToInferenceWhenNotDeclared(t *testing.T) {
	declared := make(map[string]time.Duration)
	newestFirst := []int64{1000, 700, 400, 100}
	got, ok := cadenceFor("any", declared, newestFirst)
	want := int64(300)
	if !ok || got != want {
		t.Fatalf("expected %d, true; got %d, %v", want, got, ok)
	}
}

// TestCadenceFor_NilDeclaredMapBehavesLikeUnwired confirms that a nil
// declared map is treated as absent, preserving existing behaviour for tests
// and minimal configurations that do not provide an interval map.
func TestCadenceFor_NilDeclaredMapBehavesLikeUnwired(t *testing.T) {
	var declared map[string]time.Duration // nil
	newestFirst := []int64{1000, 700, 400, 100}
	got, ok := cadenceFor("any", declared, newestFirst)
	want := int64(300)
	if !ok || got != want {
		t.Fatalf("expected %d, true; got %d, %v", want, got, ok)
	}
}

// TestCadenceFor_LongRunningWorkerIsSkippedNotInferred pins the meaning of a
// declared interval of 0.
//
// This test previously asserted the OPPOSITE — that a zero interval falls back
// to inference — on the reasoning that "zero is no promise to judge against".
// That premise was wrong. The Worker interface defines 0 as LONG-RUNNING: Run
// is called once and blocks until the daemon exits, which is how the stream
// ingestors work. Such a worker cannot be late, and inferring a period from its
// sparse boot-time runs would flag a stream that is healthily blocked inside its
// single Run. internal/health.StaleWorkers skips on exactly this condition, and
// the two staleness surfaces disagreeing is the defect being repaired here.
func TestCadenceFor_LongRunningWorkerIsSkippedNotInferred(t *testing.T) {
	declared := map[string]time.Duration{"crypto-live": 0}
	// Gaps that WOULD yield a confident inferred cadence of 300s if consulted.
	newestFirst := []int64{1000, 700, 400, 100}
	got, ok := cadenceFor("crypto-live", declared, newestFirst)
	if ok {
		t.Fatalf("a long-running worker must be left UNJUDGED; got cadence %d, ok=true", got)
	}
}

// TestCadenceFor_UnjudgeableWhenNeitherSourceWorks validates that when
// neither a declared interval nor sufficient history exists, the function
// returns ok=false, leaving the worker unjudged rather than inventing a
// staleness verdict from insufficient data.
func TestCadenceFor_UnjudgeableWhenNeitherSourceWorks(t *testing.T) {
	declared := make(map[string]time.Duration)
	newestFirst := []int64{1000, 900} // only two points, < minRunsForCadence
	got, ok := cadenceFor("any", declared, newestFirst)
	if ok || got != 0 {
		t.Fatalf("expected false, 0; got %v, %d", ok, got)
	}
}
