package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
)

// RVMinDistinctDays is the minimum number of distinct trading days required
// to consider a volatility forecast's evidence sufficient. Days are the
// independent observations because many symbols resolving on the same day
// share a single market shock, so a row count of thousands can still be
// a handful of real observations.
const RVMinDistinctDays = 60

// RVRecordCaveat is the verbatim caveat string that must accompany every
// live volatility record. It appears in every response to ensure readers
// never mistake its absence for a claim of safety or validity.
const RVRecordCaveat = "LIVE RECORD, ACCRUING. This is not a claim of skill. The comparison is against a random walk and RiskMetrics EWMA(0.94), lower loss is better, and no verdict is published until the pre-registered minimum evidence is met. Backtest figures are reported separately and are never mixed with these."

// volForecastRecord returns the live volatility forecast record for horizons
// 1 and 5 sessions. It reports only the accumulating evidence and never a
// verdict of skill, because that must come from the pre-registered grader.
// sharedVolRecordSWR fronts GET /api/vol-forecast/record. Ten minutes, not the
// 60s respCacheTTL: the underlying rows change once per trading day, and a
// rebuild costs ~25s of read-pool time, so a 60s TTL would spend a quarter of
// every minute rebuilding an answer that had not changed.
var sharedVolRecordSWR = newSWRBodyCache(10 * time.Minute)

func (d Deps) volForecastRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	type qlikePoint struct {
		Har  *float64 `json:"har"`
		Rw   *float64 `json:"rw"`
		Ewma *float64 `json:"ewma"`
	}

	type horizonResult struct {
		Horizon       int        `json:"horizon"`
		N             int        `json:"n"`
		DistinctDays  int        `json:"distinctDays"`
		Ungradable    int        `json:"ungradable"`
		Sufficient    bool       `json:"sufficient"`
		Verdict       string     `json:"verdict"`
		VerdictReason string     `json:"verdictReason"`
		MeanQlike     qlikePoint `json:"meanQlike"`
		VsEwma        *float64   `json:"vsEwma"`
		VsRandomWalk  *float64   `json:"vsRandomWalk"`
		LowerIsBetter bool       `json:"lowerIsBetter"`
	}

	type response struct {
		AsOf            int64           `json:"asOf"`
		Evidence        string          `json:"evidence"`
		MinDistinctDays int             `json:"minDistinctDays"`
		Horizons        []horizonResult `json:"horizons"`
		Caveat          string          `json:"caveat"`
	}

	// The registered horizons live in ONE place. Re-listing them here would
	// let this endpoint drift out of step with what the workers actually
	// freeze, and report a horizon nobody is forecasting.
	results := make([]horizonResult, 0, len(pipeline.RVHorizons))

	for _, hz := range pipeline.RVHorizons {
		h := int(hz)
		rec, err := d.St.RVLiveRecord(r.Context(), h)
		if err != nil {
			httpInternal(w, err)
			return
		}

		sufficient := rec.DistinctDays >= RVMinDistinctDays
		verdict := "ACCRUING"
		// Only the pre-registered grader may declare skill; we just accrue.
		reason := ""
		var meanQlike qlikePoint
		var vsEwma *float64
		var vsRandomWalk *float64

		if sufficient {
			reason = fmt.Sprintf(
				"%d distinct trading days, at or above the %d-day floor. The record is "+
					"reported; the verdict is not. Only the pre-registered grader may say "+
					"whether this is skill.",
				rec.DistinctDays, RVMinDistinctDays)
			// Negative means HAR model has lower loss, which is better.
			vsEwmaVal := rec.MeanQLIKEHAR - rec.MeanQLIKEEW
			vsRWVal := rec.MeanQLIKEHAR - rec.MeanQLIKERW
			har, rw, ew := rec.MeanQLIKEHAR, rec.MeanQLIKERW, rec.MeanQLIKEEW
			meanQlike = qlikePoint{Har: &har, Rw: &rw, Ewma: &ew}
			vsEwma = &vsEwmaVal
			vsRandomWalk = &vsRWVal
		} else {
			verdict = "INSUFFICIENT"
			// Built from the ACTUAL count. The literal "0 of the 60" this
			// replaced would have kept saying zero after the first day
			// resolved, which is a false statement on the one surface whose
			// argument is that its numbers are true.
			reason = fmt.Sprintf(
				"%d of the %d distinct trading days required; no verdict either way.",
				rec.DistinctDays, RVMinDistinctDays)
			// The rows-are-not-observations clause is only worth saying once
			// there ARE rows. At zero it reads as "0 rows is not 0
			// observations", which is true and silly.
			if rec.N > 0 {
				reason += fmt.Sprintf(
					" Days are counted, not rows: forecasts resolving on one day share a "+
						"single market shock, so %d resolved row(s) is not %d independent "+
						"observations.", rec.N, rec.N)
			}
		}

		results = append(results, horizonResult{
			Horizon:       h,
			N:             rec.N,
			DistinctDays:  rec.DistinctDays,
			Ungradable:    rec.Ungradable,
			Sufficient:    sufficient,
			Verdict:       verdict,
			VerdictReason: reason,
			MeanQlike:     meanQlike,
			VsEwma:        vsEwma,
			VsRandomWalk:  vsRandomWalk,
			LowerIsBetter: true,
		})
	}

	resp := response{
		AsOf:            time.Now().Unix(),
		Evidence:        "LIVE",
		MinDistinctDays: RVMinDistinctDays,
		Horizons:        results,
		Caveat:          RVRecordCaveat,
	}
	writeJSON(w, resp)
}
