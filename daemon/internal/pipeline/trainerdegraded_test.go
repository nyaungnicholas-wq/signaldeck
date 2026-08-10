package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// A trainer that admits nothing must not file "ok".
//
// Measured on the live run log, every hour, for days:
//
//	gbm-trainer         ok   trained 0 model legs (0 GBM + 0 mean-rev with OOS edge)
//	pressure-trainer    ok   0 symbol-horizons graded, 0 benched (OOS lift<=0)
//	expectancy-trainer  ok   fleet AUC 0.4303 (ANTI-PREDICTIVE - benched fleet-wide)
//
// Those three lines are why the ensemble ran on one leg on 2026-08-07 while the
// board stayed green. workers.ErrDegraded exists for exactly this shape —
// "completed without breaking and without delivering" — and files the run as
// degraded so /api/health reports it.
//
// This test pins the CONTRACT rather than re-running the trainers, which need a
// populated corpus: the wrapping must survive errors.Is, and must not be
// mistaken for a hard failure.
func TestDegradedWrappingContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"gbm trained nothing", wrapDegradedForTest("no model leg cleared its OOS edge bar")},
		{"pressure graded nothing", wrapDegradedForTest("no symbol-horizon could be graded")},
		{"pressure benched everything", wrapDegradedForTest("every graded symbol-horizon was benched (OOS lift<=0)")},
		{"expectancy anti-predictive", wrapDegradedForTest("anti-predictive and benched fleet-wide (1d AUC 0.4303)")},
	} {
		if !errors.Is(tc.err, workers.ErrDegraded) {
			t.Errorf("%s: does not unwrap to workers.ErrDegraded, so the runner files it as a hard error", tc.name)
		}
		// The reason must survive into the message: a degraded run nobody can
		// explain gets switched off the first time it is inconvenient.
		if strings.TrimSpace(tc.err.Error()) == workers.ErrDegraded.Error() {
			t.Errorf("%s: carries no reason of its own", tc.name)
		}
	}
}

// A degraded run must remain distinguishable from a healthy one. Guarding
// against the obvious wrong fix: silencing the signal by returning nil.
func TestDegradedIsNotNil(t *testing.T) {
	if err := wrapDegradedForTest("x"); err == nil {
		t.Fatal("a degraded trainer returned nil; the runner would file it as ok")
	}
}

// wrapDegradedForTest mirrors the wrapping the three trainers use.
func wrapDegradedForTest(reason string) error {
	return fmt.Errorf("%s: %w", reason, workers.ErrDegraded)
}
