package api

import (
	"net/http"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ── STAGE 3: append-only, hash-chained prediction ledger (read routes) ──────
//
// These endpoints expose the tamper-evidence buyers/allocators require:
//   - /api/ledger/verify walks the whole chain and reports whether every
//     recomputed hash still matches (intact), how many entries exist, the head
//     hash, and the first broken seq if the chain was tampered with;
//   - /api/ledger returns the committed entries for one symbol+horizon.
// Both are read-only (public under SIGNALDECK_PUBLIC_READS like the other
// market-data reads).

// ledgerVerify recomputes the entire hash chain and reports its integrity.
func (d Deps) ledgerVerify(w http.ResponseWriter, r *http.Request) {
	v, err := d.St.VerifyLedger(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"intact": v.Intact,
		"count":  v.Count,
		"head":   v.HeadHash,
	}
	if v.BrokenAtSeq != nil {
		out["brokenAtSeq"] = *v.BrokenAtSeq
	}
	writeJSON(w, out)
}

// ledger returns the committed ledger entries for one symbol+horizon (newest
// first). ?symbol=&market= identify the symbol; ?horizon= defaults to 1d;
// ?limit= caps the count (default 100).
func (d Deps) ledger(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := d.St.LedgerFor(r.Context(), s.ID, h, limit)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"symbol":  s.Symbol,
		"horizon": h,
		"count":   len(entries),
		"entries": entries,
	})
}

// registerLedger wires the Stage-3 prediction-ledger read routes.
func (d Deps) registerLedger(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ledger/verify", d.ledgerVerify)
	mux.HandleFunc("GET /api/ledger", d.ledger)
}
