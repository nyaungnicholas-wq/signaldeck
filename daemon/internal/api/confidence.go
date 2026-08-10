// Confidence API: GET /api/confidence?symbol=AAPL&market=stocks&horizon=1d — the
// unified read on one prediction: how likely, how much, how badly it tends to hurt
// on the way, how much of that is knowable, and why.
//
// This is the read side of internal/confidence. Its reason for existing is that
// the pieces were already computed and never assembled: the calibrated
// probability came from the prediction row, the out-of-sample record from the
// track-record grade, the conditional expected return from stored expectancy, and
// nothing at all answered "how far does this go against me before it resolves".
//
// HONESTY, and it is the whole contract: every derived number is null unless it
// was measurable, and `withheld` names each null with its reason. A consumer that
// sees null asks; one that sees 0 quotes it.
package api

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/confidence"
	"github.com/nyaungnicholas-wq/signaldeck/internal/expectancy"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const confidenceNote = "One assembled read on a single prediction. The probability is the platform's calibrated output; confidence is a SEPARATE judgement about whether the out-of-sample record supports believing it at all, so a high probability from a model with no demonstrated edge reads as low confidence. Expected drawdown is the measured adverse excursion of historical holds of this length on this symbol — UNCONDITIONAL (it does not yet condition on the current market state), which is stated here rather than implied away. Every unmeasurable field is null and named in `withheld`. Not advice."

// confidenceEpisodeCap bounds how many historical holding windows the adverse-
// excursion measurement walks. ~2 years of daily bars is what this platform
// actually stores per symbol, so a larger cap would only ever read the same rows.
const confidenceEpisodeCap = 600

func (d Deps) confidenceRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sym, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}

	pred, okPred, err := d.St.LatestPrediction(ctx, sym.ID, h)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !okPred {
		writeJSON(w, map[string]any{
			"note":     confidenceNote,
			"symbol":   sym.Symbol,
			"horizon":  string(h),
			"assessed": false,
			"reason":   "no prediction stored for this symbol and horizon yet — nothing to assess",
		})
		return
	}

	ev, err := d.confidenceEvidence(ctx, h)
	if err != nil {
		httpInternal(w, err)
		return
	}
	condRet := d.conditionalReturn(ctx, sym.ID, h)
	excursion, holdBars := d.adverseExcursion(ctx, sym.ID, h)

	a := confidence.Assess(pred.CalProb, ev, condRet, excursion)
	writeJSON(w, map[string]any{
		"note":        confidenceNote,
		"symbol":      sym.Symbol,
		"market":      string(sym.Market),
		"horizon":     string(h),
		"assessed":    true,
		"predictionTs": pred.Ts,
		"generatedTs": time.Now().Unix(),
		"holdBars":    holdBars,
		"assessment":  a,
		"excursion":   excursion,
	})
}

// confidenceEvidence reads the platform's own out-of-sample directional record
// for the horizon — the same resolved-outcome accounting the track record uses, so
// the confidence here cannot disagree with the confidence shown there.
//
// The record carries a measured calibration error alongside accuracy, so it is
// passed through and marked KNOWN. Had it not been measured, the flag would stay
// false rather than passing a zero — which would score the model as perfectly
// calibrated on the strength of never having been checked.
func (d Deps) confidenceEvidence(ctx context.Context, h md.Horizon) (confidence.Evidence, error) {
	rec, err := d.St.DirectionalRecord(ctx, h, 0)
	if err != nil {
		return confidence.Evidence{}, err
	}
	// CalibrationErr IS measured by this record, so unlike the note above it is
	// passed through and marked known. What stays unknown is nothing here — the
	// directional record measures both.
	ev := confidence.Evidence{
		N:                rec.N,
		Accuracy:         rec.Accuracy,
		BaseRate:         rec.BaselineAcc,
		CalibrationErr:   rec.CalibrationErr,
		CalibrationKnown: true,
	}
	// The record's rows are symbol-days that share one market move per day, so
	// the uncertainty interval must be evaluated at N over the MEASURED design
	// effect, never raw N (A1). Below clusterstat's day floor the effective N
	// stays 0 and confidence.Assess withholds the uncertainty with the reason.
	if len(rec.Days) >= clusterstat.MinDistinctDays {
		if deff, ok := clusterstat.DesignEffect(rec.Days); ok && deff > 0 {
			ev.EffectiveN = float64(rec.N) / deff
		}
	}
	return ev, nil
}

// conditionalReturn looks up the stored expectancy row for the symbol's CURRENT
// market state, which is the one genuinely state-conditional number in the
// assessment.
//
// It is withheld unless the daemon can tell us what state the symbol is in right
// now (Deps.CurrentState is wired in cmd/signaldeckd) AND a row exists for that
// state. Falling back to an unconditional average here would quietly answer a
// different question than the one the field name asks.
func (d Deps) conditionalReturn(ctx context.Context, symbolID int64, h md.Horizon) confidence.ConditionalReturn {
	if d.CurrentState == nil {
		return confidence.ConditionalReturn{Note: "the live state resolver is not wired, so no state-conditional expectancy can be looked up"}
	}
	keys, err := d.CurrentState(ctx, symbolID)
	if err != nil {
		return confidence.ConditionalReturn{Note: "the symbol's current market state could not be computed"}
	}
	key, ok := keys[h]
	if !ok || key == "" {
		return confidence.ConditionalReturn{Note: "the symbol has no resolvable current state at this horizon"}
	}
	rows, err := d.St.Expectancy(ctx, symbolID, h)
	if err != nil || len(rows) == 0 {
		return confidence.ConditionalReturn{Note: "no expectancy table built for this symbol yet"}
	}
	// Lookup walks the state key's prefix chain, so a thin exact state falls back
	// to its broader parent rather than to nothing.
	row, found := expectancy.Lookup(rows, key)
	if !found {
		return confidence.ConditionalReturn{Note: "no expectancy row matches this symbol's current state or any broader state containing it"}
	}
	return confidence.ConditionalReturn{Mean: row.MeanFwd, N: row.N, Valid: true}
}

// adverseExcursion measures how far historical holds of this horizon's length went
// against the entry on this symbol, from the stored daily bars.
//
// It returns UNCONDITIONAL episodes — every window of the holding length, not only
// the ones resembling today — and the payload's note says so. Conditioning would
// need the historical signal at each bar, which is not stored per bar; measuring
// the unconditional shape is the honest version of the number that is actually
// available, and it is still the answer to "what does a hold of this length on
// this name typically cost you on the way".
//
// The LOW of each bar in the window is used rather than the close: a position that
// closed flat after trading 8% down did hurt by 8%, and a reader deciding whether
// they could sit through it needs the number they would have watched.
func (d Deps) adverseExcursion(ctx context.Context, symbolID int64, h md.Horizon) (confidence.Excursion, int) {
	hold := holdBarsFor(h)
	bars, err := d.St.LastBars(ctx, symbolID, md.TF1d, confidenceEpisodeCap)
	if err != nil || len(bars) < hold+1 {
		return confidence.Excursion{Note: "not enough stored daily bars on this symbol to measure a holding window"}, hold
	}
	eps := make([]confidence.Episode, 0, len(bars))
	// Entry is bar i's OPEN and the window is the following `hold` bars — the same
	// next-bar-entry convention the backtester and the paper book use, so this
	// number describes the trade the platform would actually have taken.
	for i := 0; i+hold < len(bars); i++ {
		entry := bars[i].Open
		if entry <= 0 || math.IsNaN(entry) {
			continue
		}
		lows := make([]float64, 0, hold)
		for j := i; j < i+hold; j++ {
			lows = append(lows, bars[j].Low)
		}
		eps = append(eps, confidence.Episode{
			EntryPx: entry,
			Lows:    lows,
			Fwd:     (bars[i+hold].Close - entry) / entry,
		})
	}
	return confidence.AdverseExcursion(eps), hold
}

// holdBarsFor is the holding length in daily bars each horizon implies. 1d holds
// one bar; 1w holds five sessions.
func holdBarsFor(h md.Horizon) int {
	if h == md.H1w {
		return 5
	}
	return 1
}

// registerConfidence wires the unified per-prediction confidence read.
func (d Deps) registerConfidence(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/confidence", d.confidenceRead)
}
