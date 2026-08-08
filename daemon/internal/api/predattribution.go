package api

// Prediction attribution (Layer 6) — GET /api/attribution/prediction: the
// "+12% trend / −6% news" breakdown of one LEDGERED prediction's raw blended
// probability, from the persisted top-N parts (see internal/store/predattr.go
// and ensemble.AttributeProbability for the decomposition contract). Read-only
// store query, gated like every other market-data read by d.secure.

import (
	"fmt"
	"net/http"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// predAttrMethodNote ships verbatim in every payload: what the numbers are and
// what they are not.
const predAttrMethodNote = "Parts are probability deltas from a neutral 0.5 prior; the FULL part set sums to " +
	"(raw blended probability - 0.5), and only the top-8 by |contribution| are stored, so displayed parts may " +
	"not sum exactly. GBM feature-level parts (when present) use Saabas path attribution — an approximation " +
	"whose credit is biased toward deeper splits under feature interactions, NOT exact SHAP. Attribution covers " +
	"the RAW blend; calibration is a separate monotone map applied afterward."

// registerPredAttribution wires the Layer-6 prediction-attribution read route.
func (d Deps) registerPredAttribution(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/attribution/prediction", d.predAttribution)
}

// predAttribution serves the persisted attribution parts for one ledgered
// prediction: ?symbol=&market=&horizon=[&seq=]. seq omitted (or 0) resolves to
// the newest attributed ledger entry for the symbol+horizon.
func (d Deps) predAttribution(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	var seq int64
	if q := r.URL.Query().Get("seq"); q != "" {
		n, err := strconv.ParseInt(q, 10, 64)
		if err != nil || n < 0 {
			httpErr(w, 400, "seq must be a non-negative integer")
			return
		}
		seq = n
	}
	if seq == 0 {
		latest, ok, err := d.St.LatestAttributedSeq(r.Context(), s.ID, h)
		if err != nil {
			httpInternal(w, err)
			return
		}
		if !ok {
			writeJSON(w, map[string]any{
				"symbol": s.Symbol, "horizon": h, "available": false,
				"note": "no attributed predictions yet for this symbol+horizon (attribution is written alongside each new ledger append)",
			})
			return
		}
		seq = latest
	}
	parts, err := d.St.PredictionAttributions(r.Context(), seq)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if len(parts) == 0 {
		httpErr(w, 404, fmt.Sprintf("no attribution stored for ledger seq %d", seq))
		return
	}
	// Human-readable "+12.0% trend"-style lines alongside the raw numbers.
	type outPart struct {
		Name         string  `json:"name"`
		Kind         string  `json:"kind"`
		Contribution float64 `json:"contribution"` // probability delta from 0.5
		Display      string  `json:"display"`      // e.g. "+12.0% comp_trend"
	}
	out := make([]outPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, outPart{
			Name: p.Name, Kind: p.Kind, Contribution: p.Contribution,
			Display: fmt.Sprintf("%+.1f%% %s", p.Contribution*100, p.Name),
		})
	}
	writeJSON(w, map[string]any{
		"symbol":    s.Symbol,
		"horizon":   h,
		"seq":       seq,
		"ts":        parts[0].Ts,
		"method":    parts[0].Method,
		"available": true,
		"parts":     out,
		"note":      predAttrMethodNote,
	})
}
