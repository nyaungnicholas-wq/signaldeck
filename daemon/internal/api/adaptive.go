package api

import (
	"encoding/json"
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
)

// adaptiveWeights serves the current learned per-regime ensemble weights —
// the model's SELF-KNOWLEDGE: per regime cell it reports sample count,
// per-leg hit-rates + ICs, the derived weights (nil when the honesty gate
// withheld them), and the fallback semantics, so the UI can show exactly
// what the model currently believes about its own components and why.
func (d Deps) adaptiveWeights(w http.ResponseWriter, r *http.Request) {
	raw, err := d.St.GetMeta(r.Context(), adaptive.MetaKey)
	if err != nil {
		httpInternal(w, err)
		return
	}
	resp := map[string]any{
		"available":  false,
		"minSamples": adaptive.MinCellSamples,
		// Which gate applies per cell is derivable client-side: a cell with
		// weights uses them (learned); a cell without falls back to the
		// "all" cell's weights when THAT cell has them; otherwise the static
		// equal prior — stated here so the UI never has to guess the rules.
		"fallback": "regime cell (n>=30) -> all cell (n>=30) -> static equal prior",
	}
	if raw != "" {
		var weights adaptive.Weights
		if err := json.Unmarshal([]byte(raw), &weights); err == nil {
			resp["available"] = true
			resp["weights"] = weights
		}
	}
	writeJSON(w, resp)
}
