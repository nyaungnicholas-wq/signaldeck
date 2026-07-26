// META-LABELING WAVE (2026-07-25) — the read surface.
//
//	GET /api/metalabel   whether filtering the platform's own calls earns its place
//
// The payload leads with the verdict and its reason, because on this platform
// the interesting result has been a REJECTION with a flattering headline number
// attached: the first live grade showed the filter raising precision by 17.7
// points and it still earned nothing, since the signal it filters loses money
// after cost. A surface that showed the precision without the verdict would be
// the most misleading number on the site.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// registerMetaLabel wires the wave's read endpoint.
func (d Deps) registerMetaLabel(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metalabel", d.metaLabel)
}

const metaLabelHowToRead = "Meta-labeling splits the job in two: the primary model picks the SIDE, and this secondary model decides whether the call is worth taking at all. It is graded on EXPECTANCY per decision offered, never on precision — a filter that is more often right while making less money has earned nothing, which is exactly the trap the trend21 signal fell into (its most accurate band carries a negative forward return). Read the verdict first. 'rejected' with a large precisionLift is the normal, honest outcome while the primary being filtered has no cost-net edge: filtering a signal with no edge yields fewer trades with the same lack of edge. takenDays matters more than takenN — every symbol shares one market move on a given day, so trades concentrated on a few days are not independent evidence however many of them there are. Nothing here gates a live decision; promoting a filter to size real trades is a human decision."

func (d Deps) metaLabel(w http.ResponseWriter, r *http.Request) {
	raw, err := d.St.GetMeta(r.Context(), store.MetaMetaLabel)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// An absent report is an honest "not computed yet", never an empty grade
	// that could read as a passing one.
	if raw == "" {
		writeJSON(w, map[string]any{
			"available":  false,
			"reason":     "the meta-label study has not completed a pass yet",
			"horizons":   []any{},
			"howToRead":  metaLabelHowToRead,
			"gates":      metaLabelGates(),
			"live":       true,
			"gatesGuard": "measurement only — this never sizes a trade",
		})
		return
	}
	var horizons []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &horizons); err != nil {
		httpErr(w, http.StatusInternalServerError, "stored metalabel report is unreadable")
		return
	}
	writeJSON(w, map[string]any{
		"available":  true,
		"horizons":   horizons,
		"howToRead":  metaLabelHowToRead,
		"gates":      metaLabelGates(),
		"live":       true,
		"gatesGuard": "measurement only — this never sizes a trade",
	})
}

// metaLabelGates states the three bars a filter must clear, in the order they
// are applied, so a reader can check the verdict rather than trust it.
func metaLabelGates() []string {
	return []string{
		"the primary signal must have cost-net edge before filtering it means anything",
		"the filter's trades must span enough DISTINCT DAYS to be independent evidence",
		"the filter must improve EXPECTANCY per decision offered, not precision",
	}
}
