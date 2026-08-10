// HONESTY-GAP WAVE (2026-07-25) — the read surfaces for the four gaps
// PREDICTION_PROCESS.md left open.
//
//	GET /api/return-forecast     the cost-aware conditional return distribution
//	GET /api/feature-redundancy  how many INDEPENDENT inputs the vector has
//	GET /api/canary              whether a new model version may serve
//	GET /api/dataset-versions    which slices a provider has rewritten
//	GET /api/price-validation    where a second source disagrees with ours
//
// Each payload carries its own interpretation: what the numbers mean, and what
// they specifically do NOT license the reader to conclude. That is not padding —
// every one of these surfaces replaces a number that used to be presented with
// more confidence than it had earned.
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
	"github.com/nyaungnicholas-wq/signaldeck/internal/distribution"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// registerHonestyGaps wires the wave's read endpoints.
func (d Deps) registerHonestyGaps(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/return-forecast", d.returnForecast)
	mux.HandleFunc("GET /api/feature-redundancy", d.featureRedundancy)
	mux.HandleFunc("GET /api/canary", d.canaryTrials)
	mux.HandleFunc("GET /api/dataset-versions", d.datasetVersions)
	mux.HandleFunc("GET /api/price-validation", d.priceValidation)
}

const returnForecastHowToRead = "This REPLACES the up/down probability. pUp and pDown are the probabilities of a move that CLEARS the cost band (tau); pInside is the no-trade zone, and on a 1-day horizon it is normally the largest of the three — that majority was previously being forced into a direction. expectedValue is the profit of taking the leaning side once, net of cost: it is the number that decides whether the call is worth making, and it can be negative while the lean is 'correct'. skill is the pinball-loss skill of conditioning on the volatility regime versus the same forecast made unconditionally; at or below zero the conditioning added nothing and only the width should be trusted. The CENTER of this distribution carries no demonstrated edge — direction has been disproven on this platform seven independent ways. The WIDTH is the part that rests on a validated predictor."

func (d Deps) returnForecast(w http.ResponseWriter, r *http.Request) {
	horizon := strings.TrimSpace(r.URL.Query().Get("horizon"))
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	rows, err := d.St.ReturnForecasts(r.Context(), horizon, symbol, 500)
	if err != nil {
		httpInternal(w, err)
		return
	}
	// Descriptive split so a reader can see at a glance how much of the fleet
	// has a distribution whose conditioning actually earned its place.
	var withSkill, positiveSkill int
	for _, f := range rows {
		if f.Skill != nil {
			withSkill++
			if *f.Skill > 0 {
				positiveSkill++
			}
		}
	}
	writeJSON(w, map[string]any{
		"forecasts":     rows,
		"count":         len(rows),
		"graded":        withSkill,
		"positiveSkill": positiveSkill,
		// tau is per-forecast (each row carries the cost band it was built
		// with); minSample is the floor below which a distribution is refused
		// rather than estimated from a handful of points.
		"minSample": distribution.MinSample,
		"howToRead": returnForecastHowToRead,
		"live":      true,
	})
}

const featureRedundancyHowToRead = "fieldCount is how many features the vector carries; effectiveCount is how many INDEPENDENT sources those fields actually represent. The gap is double-counting: the adaptive weighter treats each leg as a separate vote, so a signal rendered four ways is weighted four times. Clusters are groups whose pairwise |correlation| reaches the threshold; the representative is the cluster's most informative member. A pair with too few shared observations is left UNMERGED rather than assumed independent. Electing representatives reads labels, so this is a redundancy report and NOT evidence that any surviving feature predicts anything."

func (d Deps) featureRedundancy(w http.ResponseWriter, r *http.Request) {
	raw, err := d.St.GetMeta(r.Context(), store.MetaFeatureRedundancy)
	if err != nil {
		httpInternal(w, err)
		return
	}
	var payload any
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			payload = nil
		}
	}
	writeJSON(w, map[string]any{
		"horizons":  payload,
		"available": payload != nil,
		"note":      "empty until the feature-redundancy worker's first daily pass over resolved outcomes",
		"howToRead": featureRedundancyHowToRead,
	})
}

const canaryHowToRead = "A new model version does not inherit production. Before any comparison is made, BOTH arms must clear the same floors measured the same way — enough graded observations, falling on enough DISTINCT UTC days, spanning enough calendar days — because an incumbent observed for four hours is not a bar, and grading a challenger against one lets the direction of four hours of noise decide which model serves. When either arm falls short the verdict is 'no comparison possible — holding' with the shortfall named, which is not the same statement as a considered hold. Once both arms qualify, the challenger's own confidence interval must clear BOTH the incumbent's live accuracy and the naive majority-class baseline before it may serve; an interval sitting entirely below the incumbent ends the trial as a rejection; everything else HOLDS, with the challenger recording forecasts it does not serve. Hold is the default and the most common verdict, which is correct — most retrains are not improvements. Version identity here is the feature-vector version, because a changed vector IS a changed model."

func (d Deps) canaryTrials(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.CanaryTrials(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"trials": rows,
		"count":  len(rows),
		"gates": map[string]any{
			"minObservations": canary.MinObservations,
			"minWindowDays":   canary.MinWindowDays,
			"minMarginPp":     canary.MinMarginPp,
			// Which side the floors bind, and what they count, are part of the
			// gate: the same numbers applied to one arm only are a different
			// rule, and that was the defect.
			"appliedTo":       "incumbent and challenger alike",
			"observationUnit": "one graded forecast per (symbol, UTC day)",
			"windowUnit":      "distinct UTC days carrying observations, and the span between the first and last",
		},
		"howToRead": canaryHowToRead,
	})
}

const datasetVersionsHowToRead = "Each row is a content hash of the exact daily bars a measurement would read. When the hash changes inside a range that was already recorded, the provider REWROTE history: any claim measured on that slice is no longer reproducible and has to be re-graded. Appending newer bars is not a revision and is not counted as one. revisions is the running count of genuine rewrites, so the worst-affected symbols sort first."

func (d Deps) datasetVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.DatasetVersions(r.Context(), 500)
	if err != nil {
		httpInternal(w, err)
		return
	}
	var revised int
	for _, v := range rows {
		if v.Revisions > 0 {
			revised++
		}
	}
	writeJSON(w, map[string]any{
		"versions":       rows,
		"count":          len(rows),
		"revisedSymbols": revised,
		"howToRead":      datasetVersionsHowToRead,
	})
}

const priceValidationHowToRead = "Every other data check here is internal consistency, which cannot see a provider that is quietly wrong; only a second source disagreeing can. Deviations are in basis points on daily closes, compared on the intersection of trading days. A one-sided offset on nearly every day is an ADJUSTMENT-CONVENTION difference, not corruption, and is labeled separately — conflating the two teaches an operator to ignore both. A disagreement lowers confidence and raises a data-quality event; it never overwrites a bar, because the second source being different is not evidence of it being right. The second provider's prices are compared and discarded: only derived statistics are stored."

func (d Deps) priceValidation(w http.ResponseWriter, r *http.Request) {
	raw, err := d.St.GetMeta(r.Context(), store.MetaPriceValidation)
	if err != nil {
		httpInternal(w, err)
		return
	}
	var payload any
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			payload = nil
		}
	}
	writeJSON(w, map[string]any{
		"validation": payload,
		"enabled":    payload != nil,
		"note":       "opt-in: set SIGNALDECK_PRICE_VALIDATION_URL (a template containing {symbol} returning daily-bar CSV) to enable the second-source check",
		"howToRead":  priceValidationHowToRead,
	})
}
