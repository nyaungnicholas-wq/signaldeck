package main

// Compile-time proof that every calendar worker still implements
// workers.ScheduledWorker.
//
// WHY THIS FILE EXISTS: ScheduledWorker is an OPTIONAL interface, discovered by
// type assertion in the runner. Nothing about registering a worker requires it,
// so the failure mode is silent — change a receiver from *T to T, or drop a
// NextFire during a refactor, and the worker keeps building, keeps running, and
// quietly goes back to ticking every 6h against a source that publishes once a
// day. There is no error to see and no test that fails; the only symptom is a
// worker_runs log slowly refilling with skips.
//
// A var block is the cheapest possible tripwire: the build breaks the moment
// one of these stops being scheduled.
import (
	"github.com/nyaungnicholas-wq/signaldeck/internal/briefing"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

var (
	_ workers.ScheduledWorker = (*pipeline.ShortVolPoller)(nil)      // finra-shorts
	_ workers.ScheduledWorker = (*pipeline.ShortInterestPoller)(nil) // finra-shortint
	_ workers.ScheduledWorker = (*pipeline.COTPoller)(nil)           // cot-poller
	_ workers.ScheduledWorker = (*pipeline.ThirteenFPoller)(nil)     // 13f-poller
	_ workers.ScheduledWorker = (*pipeline.CongressPoller)(nil)      // congress-poller
	_ workers.ScheduledWorker = (*briefing.WeeklyWorker)(nil)        // weekly-report
	_ workers.ScheduledWorker = (*briefing.SignalBTPinWorker)(nil)   // signalbt-weekly
)
