package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volprereg"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/rvgrade"
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
// 1 and 5 sessions, graded by internal/rvgrade under the registered rule.
// sharedVolRecordSWR fronts GET /api/vol-forecast/record. Ten minutes, not the
// 60s respCacheTTL: the underlying rows change once per trading day, and a
// rebuild costs ~25s of read-pool time, so a 60s TTL would spend a quarter of
// every minute rebuilding an answer that had not changed.
var sharedVolRecordSWR = newSWRBodyCache(10 * time.Minute)

// volRecordCacheName names the record's persisted last good body (cachepersist.go).
const volRecordCacheName = "vol-record"

// volRecordPersistFormat is the shape of the body volForecastRecord writes, as
// persisted across restarts. BUMP IT whenever that shape changes, or the first
// reads after the deploy serve the previous shape.
const volRecordPersistFormat = 2

// serveVolRecord is GET /api/vol-forecast/record through the shared cache; the
// route and WarmCaches both use this entry and file.
func (d Deps) serveVolRecord(w http.ResponseWriter, r *http.Request) {
	sharedVolRecordSWR.serveAt(d.cacheFile(volRecordCacheName, volRecordPersistFormat), d.St.CacheKey()+"|record", w, r, d.volForecastRecord)
}

// volForecastRecord renders the record (see its comment above). The body's
// shape is volRecordPersistFormat: bump that when you change it.
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
		// Grade is every live cell's Diebold-Mariano statistics, present only
		// at and above the evidence floor.
		Grade *rvgrade.Grade `json:"grade,omitempty"`
		// Readings is the rule evaluated under every open reading (horizon 1).
		Readings []rvgrade.Reading `json:"readings,omitempty"`
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
	start, regErr := d.rvRegistrationStart(r.Context())
	if regErr != nil {
		httpInternal(w, regErr)
		return
	}
	grades := make(map[int]rvgrade.Grade, len(pipeline.RVHorizons))
	for _, hz := range pipeline.RVHorizons {
		g, err := d.gradeRVHorizon(r.Context(), int(hz), start)
		if err != nil {
			httpInternal(w, err)
			return
		}
		grades[int(hz)] = g
	}
	// The study has ONE verdict, decided by the headline cell at horizon 1.
	study := rvgrade.Decide(grades[rvgrade.HeadlineHorizon])
	if start.unregistered {
		study = rvgrade.Ruling{Verdict: rvgrade.Insufficient,
			Reason: "The forward test is not on the pre-registration chain, so no live window exists; no verdict either way."}
	} else if start.specMismatch && study.Verdict != rvgrade.Insufficient {
		study = rvgrade.Ruling{Verdict: rvgrade.Accruing,
			Reason: "The registration on the chain does not hash to the spec this grader was built against; held, not ruled."}
	}
	results := make([]horizonResult, 0, len(pipeline.RVHorizons))

	for _, hz := range pipeline.RVHorizons {
		h := int(hz)
		rec, err := d.St.RVLiveRecord(r.Context(), h)
		if err != nil {
			httpInternal(w, err)
			return
		}
		g := grades[h]
		days := g.Headline.Days

		sufficient := days >= RVMinDistinctDays && !start.unregistered
		var meanQlike qlikePoint
		var vsEwma *float64
		var vsRandomWalk *float64
		var grade *rvgrade.Grade
		ruling := study
		if h != rvgrade.HeadlineHorizon {
			ruling = rvgrade.Decide(g) // INSUFFICIENT text below the floor
			if sufficient {
				ruling = rvgrade.Ruling{Verdict: rvSecondary, Reason: fmt.Sprintf(
					"Secondary under the registration: horizon %d cannot produce the verdict, "+
						"which is decided by the horizon-1 headline cell (currently %s). Its "+
						"cells are reported in full over %d distinct trading days.",
					h, study.Verdict, days)}
			}
		}
		reason := ruling.Reason

		if sufficient {
			// Negative means HAR model has lower loss, which is better.
			vsEwmaVal := rec.MeanQLIKEHAR - rec.MeanQLIKEEW
			vsRWVal := rec.MeanQLIKEHAR - rec.MeanQLIKERW
			har, rw, ew := rec.MeanQLIKEHAR, rec.MeanQLIKERW, rec.MeanQLIKEEW
			meanQlike = qlikePoint{Har: &har, Rw: &rw, Ewma: &ew}
			vsEwma = &vsEwmaVal
			vsRandomWalk = &vsRWVal
			// Test statistics appear only at and above the floor: below it a
			// t-statistic is a look the registration did not budget for.
			grade = &g
		} else if rec.N > 0 {
			// The rows-are-not-observations clause is only worth saying once
			// there ARE rows. At zero it reads as "0 rows is not 0
			// observations", which is true and silly.
			reason += fmt.Sprintf(
				" Days are counted, not rows: forecasts resolving on one day share a "+
					"single market shock, so %d resolved row(s) is not %d independent "+
					"observations.", rec.N, rec.N)
		}

		var readings []rvgrade.Reading
		if h == rvgrade.HeadlineHorizon && sufficient {
			readings = ruling.Readings
		}
		results = append(results, horizonResult{
			Horizon:       h,
			N:             rec.N,
			DistinctDays:  days,
			Ungradable:    rec.Ungradable,
			Sufficient:    sufficient,
			Verdict:       ruling.Verdict,
			VerdictReason: reason,
			MeanQlike:     meanQlike,
			VsEwma:        vsEwma,
			VsRandomWalk:  vsRandomWalk,
			LowerIsBetter: true,
			Grade:         grade,
			Readings:      readings,
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

// rvSecondary is the horizon-5 label once its evidence floor is met. Not one
// of the four registered outcomes on purpose: the registration names horizon 1
// as the ONE headline cell and says no other cell can produce the verdict.
const rvSecondary = "SECONDARY"

// rvStart is where the registered live window opens.
type rvStart struct {
	ts           int64 // forecasts frozen strictly after this second count
	unregistered bool  // no rv-forecast-test-registration record on the chain
	specMismatch bool  // the chain's latest specDigest is not the compiled spec's
}

// rvRegistrationStart reads the start rule ("the first live forecast frozen
// STRICTLY AFTER this record's timestamp") off the pre-registration chain.
func (d Deps) rvRegistrationStart(ctx context.Context) (rvStart, error) {
	recs, err := d.St.PreregRecords(ctx)
	if err != nil {
		return rvStart{}, err
	}
	s := rvStart{unregistered: true}
	for _, rec := range recs {
		if rec.Kind != volprereg.RVForecastKind {
			continue
		}
		if s.unregistered {
			s.ts = rec.Ts // the FIRST registration opens the window
			s.unregistered = false
		}
		// spec_hash is a hash of the JSON body; the registration's own
		// byte-stable digest is carried INSIDE it as specDigest
		// (cmd/prereg-amend/spec_rvforecast.go). That is the one to compare.
		var body struct {
			SpecDigest string `json:"specDigest"`
		}
		_ = json.Unmarshal([]byte(rec.SpecJSON), &body)
		s.specMismatch = body.SpecDigest != volprereg.Registration().Hash()
	}
	return s, nil
}

// gradeRVHorizon grades one horizon's live window. The RV^CC control needs the
// daily bars, so they are read only once the headline cell has met the floor:
// below it nothing about the control is published.
func (d Deps) gradeRVHorizon(ctx context.Context, h int, start rvStart) (rvgrade.Grade, error) {
	if start.unregistered {
		return rvgrade.GradeHorizon(nil, h), nil
	}
	rows, err := d.St.RVGradeRows(ctx, h, start.ts)
	if err != nil {
		return rvgrade.Grade{}, err
	}
	in := make([]rvgrade.Row, len(rows))
	for i, r := range rows {
		in[i] = rvgrade.Row{
			Day: time.Unix(r.Ts, 0).UTC().Format("2006-01-02"),
			HAR: r.RVHat, RW: r.NullRW, EWMA: r.NullEWMA, GK: r.Actual, CC: math.NaN(),
		}
	}
	g := rvgrade.GradeHorizon(in, h)
	if g.Headline.Days < RVMinDistinctDays {
		return g, nil
	}
	// Rows arrive ordered by symbol, so each symbol's bars are read once.
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].SymbolID == rows[i].SymbolID {
			j++
		}
		// Through the last call bar plus the outcome window, with room for
		// weekends and holidays; Bars' upper bound is exclusive.
		to := time.Unix(rows[j-1].Ts, 0).AddDate(0, 0, 2*h+14).Unix()
		bars, err := d.St.Bars(ctx, rows[i].SymbolID, md.TF1d, rows[i].Ts, to, 0)
		if err != nil {
			return rvgrade.Grade{}, err
		}
		for k := i; k < j; k++ {
			in[k].CC = rvgrade.CCTarget(bars, rows[k].Ts, h)
		}
		i = j
	}
	return rvgrade.GradeHorizon(in, h), nil
}
