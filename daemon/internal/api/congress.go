// Signal8 wave — Stage 2 API: congressional stock trades (public STOCK Act
// disclosures via the free Stock Watcher mirrors). Read-only, gated like
// every other read endpoint. The payload carries an EXPLICIT lagNote —
// disclosures lag 30-45 days by law, so this is never real-time — plus the
// last mirror-health status so the UI can say honestly when the free source
// itself is down (which it is, as of 2026-07-04).
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	congressLagNote = "Congressional stock disclosures lag 30-45 days by law (STOCK Act filing windows) — these are never real-time trades."
	congressNote    = "Public-domain congressional financial disclosures (Senate eFD / House Clerk), ingested via the free Stock Watcher community mirrors. Amounts are the RANGES reported on the disclosure, not exact values. Tickers we don't track keep symbolId null. See `source` for current mirror health — history already stored keeps being served even when the mirrors are down."
)

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
		httpErr(w, 500, err.Error())
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
	// Mirror health from the poller's last run (absent before the first run —
	// the UI treats missing status as "not checked yet", not as healthy).
	if raw, gerr := d.St.GetJSONRaw(ctx, "congress_mirror_status"); gerr == nil && raw != "" {
		var status any
		if json.Unmarshal([]byte(raw), &status) == nil {
			out["source"] = status
		}
	}
	writeJSON(w, out)
}

// registerCongress wires the Stage-2 congressional-trades read route.
func (d Deps) registerCongress(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/congress", d.congressTrades)
}
