package pipeline

// Re-admission of a retired directional model, PERSISTED.
//
// Until 2026-09-20 the coded re-admission threshold (canary.Readmit) was
// evaluated only inside the /api/model-health handler, which rewrote the JSON
// it was about to send and wrote nothing back. The prediction path
// (ModelEmitting) and the forecast monitor both read the STORED record under
// meta "model_health:<model>", so a model that had earned its way back showed
// "readmitted" on the page while every one of its forecasts was still withheld.
// The hourly ModelHealthWorker is the only writer of that record, so the
// threshold has to be applied there. The API keeps publishing the same
// readmission block; it just no longer decides anything.

import (
	"context"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/modelhealth"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// DirectionalShadow rebuilds a retired directional model's LIVE shadow record
// from prediction_outcomes — the rows keep accruing after retirement precisely
// so re-admission can be a measurement. One observation per (symbol, UTC day),
// tallied per day, with the prequential null replayed in day order over the
// same tallies. Bounded at the GRADING epoch, the same boundary the accuracy
// registry grades on (re-registered 2026-09-20; before that the survivorship
// epoch): measured 2026-07-27 the unbounded window supplied 13,065 rows over
// 24 days where only 21 rows over 3 days were post-epoch, so the 20-day floor
// was being cleared by rows the grader refuses.
func DirectionalShadow(ctx context.Context, st *store.Store, h md.Horizon) (canary.Record, bool) {
	rows, err := st.VersionedOutcomes(ctx, h, 200000, store.GradingEpoch)
	if err != nil || len(rows) == 0 {
		return canary.Record{}, false
	}
	rec := canary.Record{Version: "shadow-" + string(h), FirstTs: rows[0].Ts, LastTs: rows[0].Ts}
	byDay := map[int64]*canary.DayTally{}
	dayUps := map[int64]int{}
	for _, r := range rows {
		rec.N++
		if r.Correct {
			rec.Correct++
		}
		if r.Ts < rec.FirstTs {
			rec.FirstTs = r.Ts
		}
		if r.Ts > rec.LastTs {
			rec.LastTs = r.Ts
		}
		day := md.TradingDay(r.Ts)
		t := byDay[day]
		if t == nil {
			t = &canary.DayTally{Day: day}
			byDay[day] = t
		}
		t.N++
		if r.Correct {
			t.Hits++
		}
		if r.Up {
			dayUps[day]++
		}
	}
	tallies := make([]canary.DayTally, 0, len(byDay))
	for _, t := range byDay {
		tallies = append(tallies, *t)
	}
	sort.Slice(tallies, func(i, j int) bool { return tallies[i].Day < tallies[j].Day })
	rec.DayTallies = tallies
	rec.Days = len(tallies)
	rec.BaselineAccuracy = canary.PrequentialBaseline(tallies, dayUps)
	return rec, true
}

// ApplyReadmission applies the coded threshold to a computed verdict. A retired
// score whose shadow record clears canary.Readmit flips to readmitted and
// emitting; anything else is left exactly as it was. The Readmission block is
// returned either way so the stored record always shows how far the model sits
// from the bar.
func ApplyReadmission(score *modelhealth.Score, shadow canary.Record) canary.Readmission {
	ra := canary.Readmit(shadow)
	if score.Verdict != modelhealth.VerdictRetired || !ra.Eligible {
		return ra
	}
	score.Verdict = modelhealth.VerdictReadmitted
	score.Emitting = true
	score.Reasons = append(score.Reasons, "re-admitted by the coded threshold: "+ra.Reason)
	return ra
}
