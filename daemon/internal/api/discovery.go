// Universe-discovery endpoints (discovery wave). GET /api/candidates is a
// read (gated like other reads via PublicReads); the two POSTs are
// session-auth + CSRF (enforced centrally in security.go) and mutate through
// the SAME server-side subscribe path /api/subscribe uses.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/discovery"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// candidatesList returns discovered candidates (?status=new|added|dismissed,
// default new) plus the symbol-budget numbers the UI renders ("23/30 active").
func (d Deps) candidatesList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "new":
		status = "new"
	case "added", "dismissed", "all":
	default:
		httpErr(w, 400, "status must be new|added|dismissed|all")
		return
	}
	if status == "all" {
		status = ""
	}
	cands, err := d.St.Candidates(r.Context(), status)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	active, err := d.St.ActiveSymbolCount(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if cands == nil {
		cands = []store.Candidate{}
	}
	writeJSON(w, map[string]any{
		"candidates":        cands,
		"active":            active,
		"cap":               discovery.SymbolCap(),
		"autoAddsToday":     discovery.AutoAddsToday(r.Context(), d.St, time.Now()),
		"autoAddDailyLimit": discovery.DailyAutoAddLimit,
	})
}

// candidateBody is the payload of both candidate POSTs.
type candidateBody struct {
	Symbol string    `json:"symbol"`
	Market md.Market `json:"market"`
}

func decodeCandidateBody(r *http.Request) (candidateBody, error) {
	var body candidateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return body, fmt.Errorf("bad json: %s", err.Error())
	}
	body.Symbol = strings.ToUpper(strings.TrimSpace(body.Symbol))
	if body.Market == "" {
		body.Market = md.Stocks
	}
	if body.Symbol == "" || (body.Market != md.Crypto && body.Market != md.Stocks) {
		return body, fmt.Errorf("need symbol and market=crypto|stocks")
	}
	return body, nil
}

// candidateAdd promotes a candidate: same validate+upsert+activate+backfill
// path as /api/subscribe, added to THE CALLING USER's watchlist, respecting
// the symbol budget (409 with a clear message when at cap).
func (d Deps) candidateAdd(w http.ResponseWriter, r *http.Request) {
	body, err := decodeCandidateBody(r)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if d.Subscribe == nil {
		httpErr(w, 503, "subscribe not wired")
		return
	}
	// Serialize the cap check + subscribe against the worker's autoAdd and
	// other concurrent adds (TOCTOU on the budget — see discovery.BudgetMu).
	discovery.BudgetMu.Lock()
	defer discovery.BudgetMu.Unlock()
	symbolCap := discovery.SymbolCap()
	active, err := d.St.ActiveSymbolCount(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Adding an already-active symbol doesn't grow the universe, so it is
	// always allowed (it only touches this user's watchlist).
	existing, exErr := d.St.GetSymbol(r.Context(), body.Symbol, body.Market)
	alreadyActive := exErr == nil && existing.Active
	if !alreadyActive && active >= symbolCap {
		httpErr(w, http.StatusConflict, fmt.Sprintf(
			"symbol cap reached (%d/%d active) — dismiss candidates, unsubscribe a symbol, or raise SIGNALDECK_SYMBOL_CAP",
			active, symbolCap))
		return
	}
	sym, err := d.Subscribe(r.Context(), body.Symbol, body.Market)
	if err != nil {
		httpErr(w, 422, err.Error())
		return
	}
	if err := d.St.AddUserSymbol(r.Context(), userID(r), sym.ID); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if err := d.St.SetCandidateStatus(r.Context(), body.Symbol, body.Market, "added"); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, sym)
}

// candidateDismiss marks a candidate dismissed (it stays dismissed across
// future sweeps — UpsertCandidate preserves status on conflict).
func (d Deps) candidateDismiss(w http.ResponseWriter, r *http.Request) {
	body, err := decodeCandidateBody(r)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if err := d.St.SetCandidateStatus(r.Context(), body.Symbol, body.Market, "dismissed"); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (d Deps) registerDiscovery(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/candidates", d.candidatesList)
	mux.HandleFunc("POST /api/candidates/add", d.candidateAdd)
	mux.HandleFunc("POST /api/candidates/dismiss", d.candidateDismiss)
}
