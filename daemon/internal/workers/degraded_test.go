package workers

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// A source that never delivers must not report success.
//
// congress_trades holds 0 rows while congress-poller returns status=ok every
// run with the detail "congress mirrors unavailable"; edgar-fetcher and
// filings-poller return ok with "skipped: no EDGAR client" on every single run.
// Ninety-four congress_mirror_error dq events accumulated over seven days while
// the fleet view stayed uniformly green, because the ONLY way a worker can
// signal trouble today is to return a non-nil error -- and these pollers
// deliberately do not, since a dead upstream is not a crash and they do not want
// to be alarmed on.
//
// So the honest outcomes are three, not two: the run worked, the run BROKE, or
// the run completed while delivering nothing. The runner already distinguishes
// "timeout" from "error" for exactly this reason -- different causes, different
// fixes. This adds the third.
//
// The contract: a worker returns an error wrapping ErrDegraded, and the run is
// recorded with status "degraded" and the error's message as the detail. It is
// NOT "ok" (a watchdog cannot see it) and NOT "error" (it is not a failure to
// page anyone about).
func TestRunOnce_RecordsDegradedOutcome(t *testing.T) {
	st := openTemp(t)
	r := NewRunner(st)

	w := fakeWorker{name: "degraded-worker", fn: func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("congress mirrors unavailable: %w", ErrDegraded)
	}}
	r.runOnce(context.Background(), w)

	status, detail, finished := lastRun(t, st, "degraded-worker")
	if !finished {
		t.Fatal("degraded run was never finished")
	}
	if status != "degraded" {
		t.Errorf("status = %q, want \"degraded\". A source that delivered nothing was filed as %q, "+
			"which is how congress-poller reported ok for seven days while writing zero rows.", status, status)
	}
	if detail != "congress mirrors unavailable: "+ErrDegraded.Error() {
		t.Errorf("detail = %q, want the wrapped message preserved", detail)
	}
}

// Degradation must not swallow real failures: an ordinary error is still an
// error, or the new status becomes a place to hide crashes.
func TestRunOnce_PlainErrorIsStillError(t *testing.T) {
	st := openTemp(t)
	r := NewRunner(st)

	w := fakeWorker{name: "still-failing", fn: func(ctx context.Context) (string, error) {
		return "", errors.New("edgar: status 403")
	}}
	r.runOnce(context.Background(), w)

	if status, _, _ := lastRun(t, st, "still-failing"); status != "error" {
		t.Errorf("status = %q, want \"error\" -- a genuine failure must not be downgraded", status)
	}
}

// ErrDegraded must be detectable through wrapping, since callers add context.
func TestErrDegradedSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrDegraded))
	if !errors.Is(err, ErrDegraded) {
		t.Error("errors.Is could not see ErrDegraded through two layers of wrapping")
	}
	if errors.Is(errors.New("unrelated"), ErrDegraded) {
		t.Error("an unrelated error matched ErrDegraded")
	}
}
