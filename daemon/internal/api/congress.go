// Signal8 wave — Stage 2 API: congressional stock trades (STOCK Act
// disclosures). Read-only. The payload carries an EXPLICIT lagNote —
// disclosures lag 30-45 days by law, so this is never real-time.
//
// RETIRED 23 Oct 2026. Every stored row came from Kadoa's daily JSON: the free
// Stock Watcher mirrors had answered 403 since 2026-07, so the poller's Kadoa
// fallback was the only path that ever wrote a row. Kadoa's terms (s.4, s.8)
// bar automated access, reproduction and sharing the compiled rows without
// written permission, so the poller is unwired (cmd/signaldeckd congressWorkers),
// the stored rows are kept, and on a published deployment only the operator is
// served them (the 451 guard, datalicense.NonOperatorSource).
package api

import (
	"net/http"
	"strings"
	"time"
)

const (
	congressLagNote = "Congressional stock disclosures lag 30-45 days by law (STOCK Act filing windows) — these are never real-time trades."
	congressNote    = "Congressional financial disclosures (Senate eFD / House Clerk) as compiled by Kadoa's congress trades dataset; the Stock Watcher mirrors never supplied a stored row (403 since 2026-07). Collection stopped 23 Oct 2026 because Kadoa's terms do not permit sharing the compiled rows; the rows stored before then are shown to the operator only. Amounts are the RANGES reported on the disclosure, not exact values. Tickers we don't track keep symbolId null."
)

// congressRetiredStatus is the source status /api/congress reports since the
// poller was retired: both chambers not ok, with the reason, in the shape of
// pipeline.CongressMirrorStatus so existing readers keep parsing it.
var congressRetiredStatus = map[string]any{
	"retired":   true,
	"retiredOn": "2026-10-23",
	"detail":    congressRetiredDetail,
	"senate":    map[string]any{"ok": false, "fetched": 0, "new": 0, "detail": congressRetiredDetail},
	"house":     map[string]any{"ok": false, "fetched": 0, "new": 0, "detail": congressRetiredDetail},
}

const congressRetiredDetail = "retired 23 Oct 2026: Kadoa's terms do not permit automated collection " +
	"or sharing of its rows; the Stock Watcher mirrors answer 403. No collection runs."

// congressActivityDays is the symbol-page chip window (last 90 days of
// transaction dates).
const congressActivityDays = 90

// congressTrades serves disclosed congressional stock transactions.
// GET /api/congress?symbol=&member=&chamber=&limit=
//   - symbol matches the DISCLOSED ticker text (works even for tickers we
//     don't track — the data is about Congress, not about our universe);
//   - member is a case-insensitive substring; chamber is senate|house;
//   - with a symbol, recent90d/lastTxTs report the chip window.
func (d Deps) congressTrades(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	member := strings.TrimSpace(r.URL.Query().Get("member"))
	chamber := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chamber")))
	if chamber != "" && chamber != "senate" && chamber != "house" {
		httpErr(w, 400, "chamber must be senate or house")
		return
	}
	rows, err := d.St.CongressTrades(ctx, symbol, member, chamber, limitParam(r, 100, 500))
	if err != nil {
		httpInternal(w, err)
		return
	}
	out := map[string]any{
		"trades":  rows,
		"count":   len(rows),
		"symbol":  symbol,
		"member":  member,
		"chamber": chamber,
		"lagNote": congressLagNote,
		"note":    congressNote,
	}
	if symbol != "" {
		since := time.Now().Add(-congressActivityDays * 24 * time.Hour).Unix()
		n, lastTx, aerr := d.St.CongressActivitySince(ctx, symbol, since)
		if aerr == nil {
			out["recent90d"] = n
			out["lastTxTs"] = lastTx
		}
	}
	// The poller is retired, so its last stored status (the Kadoa fallback's
	// "ok") would read as a live source forever. Say retired instead.
	out["source"] = congressRetiredStatus
	writeJSON(w, out)
}

// registerCongress wires the Stage-2 congressional-trades read route.
func (d Deps) registerCongress(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/congress", d.congressTrades)
}
