// Universe-discovery endpoints (discovery wave). GET /api/candidates is a
// read (gated like other reads via PublicReads); the POSTs are session-auth +
// CSRF (enforced centrally in security.go) and mutate through the SAME
// server-side subscribe/monitor paths /api/subscribe uses.
//
// MONITORING IS NOT STREAM-CAPPED. The small SIGNALDECK_STREAM_CAP governs only
// the live-websocket HOT SET (Alpaca's free stock-ws concurrent-symbol limit).
// When it is full, adding a candidate no longer fails — it is MONITORED via the
// broad polled universe (stream=0: daily+minute bars, full scoring/prediction,
// just not real-time ws tick), bounded only by the much larger
// SIGNALDECK_UNIVERSE_CAP. So every discovered symbol can be monitored.
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
	"github.com/nyaungnicholas-wq/signaldeck/internal/universe"
)

// candidatesList returns discovered candidates (?status=new|added|dismissed,
// default new) plus BOTH symbol budgets the UI renders: the streamed hot set
// ("25/30 streaming") and the broad monitored universe ("512/1000 monitored").
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
	// Broad-universe wave: the STREAM cap governs the streamed hot set only.
	active, err := d.St.StreamedSymbolCount(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// The MONITOR budget: streamed + daily-only universe, bounded by UniverseCap.
	universeActive, err := d.St.DailyUniverseCount(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if cands == nil {
		cands = []store.Candidate{}
	}
	writeJSON(w, map[string]any{
		"candidates": cands,
		// streamed hot set ("active/cap" kept for backward compatibility)
		"active":    active,
		"cap":       discovery.SymbolCap(),
		"streamCap": discovery.SymbolCap(),
		// broad monitored universe (the budget that actually limits monitoring)
		"monitored":         active + universeActive,
		"monitorCap":        universe.UniverseCap(),
		"autoAddsToday":     discovery.AutoAddsToday(r.Context(), d.St, time.Now()),
		"autoAddDailyLimit": discovery.DailyAutoAddLimit,
	})
}

// candidateBody is the payload of the candidate POSTs.
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

// candidateAdd promotes a candidate onto THE CALLING USER's watchlist. It NEVER
// fails on the stream cap: when a live-ws slot is free the symbol is STREAMED
// (best data); when the hot set is full it is MONITORED via the broad polled
// universe instead (still fully scored/predicted, bounded by the much larger
// universe cap). Same validate+upsert+backfill path as /api/subscribe.
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
	// Serialize the budget checks + register against the worker's autoAdd and
	// other concurrent adds (TOCTOU on the budgets — see discovery.BudgetMu).
	discovery.BudgetMu.Lock()
	defer discovery.BudgetMu.Unlock()

	streamCap := discovery.SymbolCap()
	active, err := d.St.StreamedSymbolCount(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Adding an already-streamed symbol doesn't grow the hot set (only touches
	// this user's watchlist), so it always takes the stream path.
	existing, exErr := d.St.GetSymbol(r.Context(), body.Symbol, body.Market)
	alreadyStreamed := exErr == nil && existing.Active && existing.Stream

	var sym md.Symbol
	streamed := alreadyStreamed || active < streamCap
	if streamed {
		// A live-ws slot is free (or the symbol is already streamed): stream it.
		sym, err = d.Subscribe(r.Context(), body.Symbol, body.Market)
	} else {
		// Hot set full → MONITOR via polling instead of refusing. Bounded by the
		// universe cap (large) rather than the stream cap (small).
		if d.Monitor == nil {
			httpErr(w, 503, "monitor not wired")
			return
		}
		if body.Market == md.Stocks {
			uCount, uErr := d.St.DailyUniverseCount(r.Context())
			if uErr != nil {
				httpErr(w, 500, uErr.Error())
				return
			}
			if active+uCount >= universe.UniverseCap() {
				httpErr(w, http.StatusConflict, fmt.Sprintf(
					"monitor cap reached (%d/%d) — dismiss a symbol or raise SIGNALDECK_UNIVERSE_CAP",
					active+uCount, universe.UniverseCap()))
				return
			}
		}
		sym, err = d.Monitor(r.Context(), body.Symbol, body.Market)
	}
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
	// streaming = it got a live-ws tick slot (crypto always streams via Kraken).
	streaming := sym.Stream || body.Market == md.Crypto
	writeJSON(w, map[string]any{
		"id":        sym.ID,
		"symbol":    sym.Symbol,
		"market":    sym.Market,
		"streaming": streaming,
		"monitored": true,
	})
}

// candidateMonitorAll promotes EVERY new candidate onto the caller's watchlist
// as a MONITORED (polled, stream=0) symbol in one action — "take all the
// symbols and let me monitor them." Streaming slots aren't touched (the free-ws
// cap is respected); each symbol is monitored via the broad universe, bounded
// by the universe cap. Partial success is honest: it reports how many were
// added, how many were skipped, and why.
func (d Deps) candidateMonitorAll(w http.ResponseWriter, r *http.Request) {
	if d.Monitor == nil {
		httpErr(w, 503, "monitor not wired")
		return
	}
	discovery.BudgetMu.Lock()
	defer discovery.BudgetMu.Unlock()

	cands, err := d.St.Candidates(r.Context(), "new")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	streamed, err := d.St.StreamedSymbolCount(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	uCount, err := d.St.DailyUniverseCount(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	cap := universe.UniverseCap()
	monitored := streamed + uCount // current stock monitor headroom base

	added, skipped := 0, 0
	var firstErr string
	uid := userID(r)
	for _, c := range cands {
		// Bound the stock universe; crypto streams via Kraken (unbounded here).
		if c.Market == md.Stocks && monitored >= cap {
			skipped++
			continue
		}
		sym, err := d.Monitor(r.Context(), c.Symbol, c.Market)
		if err != nil {
			skipped++
			if firstErr == "" {
				firstErr = fmt.Sprintf("%s: %s", c.Symbol, err.Error())
			}
			continue
		}
		if err := d.St.AddUserSymbol(r.Context(), uid, sym.ID); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if err := d.St.SetCandidateStatus(r.Context(), c.Symbol, c.Market, "added"); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if c.Market == md.Stocks {
			monitored++
		}
		added++
	}
	resp := map[string]any{
		"added":      added,
		"skipped":    skipped,
		"candidates": len(cands),
		"monitorCap": cap,
	}
	if skipped > 0 {
		note := fmt.Sprintf("%d skipped", skipped)
		if monitored >= cap {
			note += " — monitor cap reached (raise SIGNALDECK_UNIVERSE_CAP for more)"
		}
		if firstErr != "" {
			note += "; first error " + firstErr
		}
		resp["note"] = note
	}
	writeJSON(w, resp)
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
	mux.HandleFunc("POST /api/candidates/monitor-all", d.candidateMonitorAll)
	mux.HandleFunc("POST /api/candidates/dismiss", d.candidateDismiss)
}
