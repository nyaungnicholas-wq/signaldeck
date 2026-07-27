// GET /api/ev/decisions — the Decision Engine's ledger (ARCHITECTURE_EV.md
// Layer 1), read side.
//
// Every verdict the EV gate rendered on a paper-book candidate — BUY, SELL,
// and every DO_NOTHING refusal — with its enumerated reason, the net EV when
// it was measurable (null when it was not, never a silent zero), the
// candidate's net-EV rank within its pass (the opportunity-cost input), and
// the full inputs snapshot with has-flags. This surface renders exactly what
// is stored — no re-derivation — so "what did refusing cost" is a query.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (d Deps) registerEVDecisions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ev/decisions", d.evDecisions)
}

// evDecisionsNote ships with every payload so the ledger's semantics travel
// with the data.
const evDecisionsNote = "every entry/exit verdict of the EV decision engine on the SIMULATED paper book, including refusals; " +
	"netEV is the cost-adjusted expected value net of tau and modelled execution cost (null = unmeasurable — a required input " +
	"was missing and the gate refused rather than defaulting); rank is the candidate's net-EV position within its pass " +
	"(the opportunity-cost input); inputs is the full assessment snapshot with has-flags. Simulated — not live money, not advice"

func (d Deps) evDecisions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, err := d.St.EVDecisions(r.Context(), q.Get("decision"), q.Get("symbol"), limit)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "ev-decisions: "+err.Error())
		return
	}
	// The stored snapshot is JSON already; re-emit it as an object rather than
	// a double-encoded string so consumers can read the has-flags directly.
	type row struct {
		Seq      int64           `json:"seq"`
		Ts       int64           `json:"ts"`
		Strategy string          `json:"strategy"`
		Symbol   string          `json:"symbol"`
		Horizon  string          `json:"horizon"`
		Decision string          `json:"decision"`
		Reason   string          `json:"reason"`
		NetEV    *float64        `json:"netEV"`
		Rank     int             `json:"rank"`
		RankOf   int             `json:"rankOf"`
		Inputs   json.RawMessage `json:"inputs"`
	}
	out := make([]row, 0, len(rows))
	for _, x := range rows {
		out = append(out, row{
			Seq: x.Seq, Ts: x.Ts, Strategy: x.Strategy, Symbol: x.Symbol,
			Horizon: x.Horizon, Decision: x.Decision, Reason: x.Reason,
			NetEV: x.NetEV, Rank: x.Rank, RankOf: x.RankOf,
			Inputs: json.RawMessage(x.InputsJSON),
		})
	}
	writeJSON(w, map[string]any{
		"decisions": out,
		"count":     len(out),
		"note":      evDecisionsNote,
	})
}
