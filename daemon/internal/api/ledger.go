package api

import (
	"net/http"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
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

// ledgerVerify reports the chain's integrity via the incremental checkpoint
// path (cold-load precompute wave): only the suffix since the last intact
// checkpoint is re-hashed unless the checkpoint anchor mismatches — the
// tamper signal that forces (and fails) a full walk. Same answer as a full
// recomputation on an untampered chain, ~12s cheaper at 200k rows.
//
// ?full=1 forces the complete genesis walk — the deliberate auditor's path.
// The disclosed limit of the fast path: a payload mutated strictly BEFORE the
// checkpoint that leaves every stored hash untouched is internally
// inconsistent but only caught by the full walk (any rewrite that keeps the
// chain consistent must change the anchor hash and IS caught).
func (d Deps) ledgerVerify(w http.ResponseWriter, r *http.Request) {
	var v store.LedgerVerification
	var err error
	fullWalk := true
	if r.URL.Query().Get("full") == "1" {
		v, err = d.St.VerifyLedger(r.Context())
	} else {
		v, fullWalk, err = d.St.VerifyLedgerCached(r.Context())
	}
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"intact":       v.Intact,
		"count":        v.Count,
		"head":         v.HeadHash,
		"incremental":  !fullWalk,
		"verifiedNote": "verified incrementally from the last intact checkpoint (hash chains verify incrementally by design); a checkpoint-anchor mismatch forces a full walk and reads as tamper; ?full=1 forces the complete genesis walk, the only path that catches a stored-hash-preserving payload mutation before the checkpoint",
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
