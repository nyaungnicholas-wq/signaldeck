package api

import (
	"context"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Use store package to avoid import not used error.
var _ store.WorkerLastRun

func paperStrategyHorizon(strategy string) md.Horizon {
	if strategy == "flagship-1w" {
		return md.H1w
	}
	return md.H1d
}

// paperProcess reports whether the simulator is healthy-and-abstaining, active,
// stalled, failing or unknown, with the numbers behind the verdict. It never
// returns an error: the page must render even when these reads fail.
func (d Deps) paperProcess(ctx context.Context, strategy string, now int64) map[string]any {
	thresh := papertrade.LongThreshold()
	out := map[string]any{"worker": "paper-trader", "longThresh": thresh, "windowDays": 30}
	run, ok, err := d.St.LastWorkerRun(ctx, "paper-trader")
	var age int64
	if err != nil {
		out["lastRun"] = nil
		out["lastRunError"] = err.Error()
	} else if !ok {
		out["lastRun"] = nil
	} else {
		last := run.FinishedAt
		if run.StartedAt > last {
			last = run.StartedAt
		}
		age = now - last
		out["lastRun"] = run
		out["lastRunAgeS"] = age
	}
	abs, aerr := d.St.PaperAbstentionStats(ctx, strategy, paperStrategyHorizon(strategy), thresh, now-30*86400)
	if aerr != nil {
		out["abstention"] = nil
		out["abstentionError"] = aerr.Error()
	} else {
		out["abstention"] = abs
	}
	var verdict, note string // every switch arm below assigns both
	switch {
	case err != nil || !ok:
		if err != nil {
			note = fmt.Sprintf("Worker status unavailable: %v", err)
		} else {
			note = "Worker status unavailable: no worker_runs row yet"
		}
		verdict = "unknown"
	case age > 3*3600:
		verdict = "stalled"
		note = fmt.Sprintf("STALLED: the paper-trader last ran %ds ago (status %s); it is due hourly. Check /lab/system/agents.", age, run.Status)
	case run.Status == "error" || run.Status == "orphaned":
		verdict = "failing"
		note = fmt.Sprintf("FAILING: last run ended %s: %s", run.Status, run.Detail)
	case aerr == nil && abs.AboveLong == 0:
		verdict = "abstaining"
		note = fmt.Sprintf("Healthy and abstaining: the worker last finished %ds ago (%s). In the last 30 days %d calibrated %s forecasts were published and %d reached the %.2f entry threshold (max %.3f). No trade is the correct output.", age, run.Detail, abs.Forecasts, string(paperStrategyHorizon(strategy)), abs.AboveLong, thresh, abs.MaxCalProb)
	default:
		verdict = "active"
		note = fmt.Sprintf("Worker healthy (last run %ds ago); %d of %d forecasts cleared the %.2f entry threshold in the last 30 days.", age, abs.AboveLong, abs.Forecasts, thresh)
	}
	out["verdict"] = verdict
	out["note"] = note
	return out
}
