// Signal8 wave — Stage 1 API: SEC filings feed, Form 4 insider trades, 13F
// institutional holdings, dilution flags. All read-only, gated like every
// other read endpoint. Every payload carries an honest `note` about the
// data's legal/procedural lag — filings intelligence is NOT real-time and the
// UI must say so.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const (
	filingsNote  = "SEC EDGAR filings (public-domain government data). Labels are plain-English readings of the form type. Filings appear when the SEC accepts them — Form 4 lags the trade ~2 business days; 13F lags the quarter by up to 45 days."
	insiderNote  = "Parsed from SEC Form 4 (public domain). Filed ~2 business days AFTER the trade by law. Only P (open-market buy) and S (open-market sale) reflect discretionary conviction; A/M/G/F are grants/exercises/gifts/withholding and are labeled as such."
	instNote     = "Parsed from SEC 13F-HR (public domain). QUARTERLY snapshots filed up to 45 days after quarter end — positions may have changed since. Value is as reported on the filing. symbol matching from issuer names is best-effort; unmatched rows keep symbolId null."
	dilutionNote = "Descriptive evidence, not a prediction: high = dilution-shaped filing (S-1/S-3/424B) in the last 180d AND shares outstanding up >2%; elevated = one of the two; low = neither."
)

// stockFromQuery resolves ?symbol= to a stored STOCK symbol (filings are a
// US-equity concept; crypto has no SEC filings).
func (d Deps) stockFromQuery(r *http.Request) (md.Symbol, bool, error) {
	sym := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	if sym == "" {
		return md.Symbol{}, false, nil
	}
	s, err := d.St.GetSymbol(r.Context(), sym, md.Stocks)
	if err != nil {
		return md.Symbol{}, true, err
	}
	return s, true, nil
}

func limitParam(r *http.Request, def, max int) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > max {
		return def
	}
	return n
}

// filings serves the plain-English SEC filings feed.
// GET /api/filings?symbol=&form=&limit=  (no symbol ⇒ fleet-wide feed)
func (d Deps) filingsFeed(w http.ResponseWriter, r *http.Request) {
	var symbolID int64
	s, given, err := d.stockFromQuery(r)
	if given && err != nil {
		httpErr(w, 404, "unknown stock symbol (filings cover US equities only)")
		return
	}
	if given {
		symbolID = s.ID
	}
	form := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("form")))
	rows, err := d.St.Filings(r.Context(), symbolID, form, limitParam(r, 100, 500))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"filings": rows,
		"count":   len(rows),
		"form":    form,
		"symbol":  s.Symbol,
		"note":    filingsNote,
	})
}

// insiders serves parsed Form 4 insider transactions.
// GET /api/insiders?symbol=&code=&limit=  (no symbol ⇒ fleet-wide recent)
func (d Deps) insiders(w http.ResponseWriter, r *http.Request) {
	var symbolID int64
	s, given, err := d.stockFromQuery(r)
	if given && err != nil {
		httpErr(w, 404, "unknown stock symbol")
		return
	}
	if given {
		symbolID = s.ID
	}
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("code")))
	rows, err := d.St.InsiderTrades(r.Context(), symbolID, code, limitParam(r, 100, 500))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Attach the honest per-code reading so the UI never invents one.
	out := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		out = append(out, map[string]any{
			"accession": t.Accession, "symbolId": t.SymbolID, "symbol": t.Symbol,
			"insider": t.Insider, "title": t.Title, "code": t.Code,
			"codeLabel":  edgar.CodeLabel(t.Code),
			"openMarket": t.Code == "P" || t.Code == "S",
			"shares":     t.Shares, "price": t.Price, "value": t.Value,
			"txTs": t.TxTs, "filedTs": t.FiledTs,
		})
	}
	writeJSON(w, map[string]any{
		"trades": out,
		"count":  len(out),
		"symbol": s.Symbol,
		"note":   insiderNote,
	})
}

// institutions serves 13F holdings.
// GET /api/institutions?symbol=AAPL   → who (in the curated list) holds it
// GET /api/institutions?manager=cik|name-substring → that manager's latest book
// GET /api/institutions               → curated-manager overview
func (d Deps) institutions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if mgr := strings.TrimSpace(r.URL.Query().Get("manager")); mgr != "" {
		cik := resolveManagerCIK(mgr)
		if cik == "" {
			httpErr(w, 404, "unknown manager (use a curated manager name or CIK)")
			return
		}
		rows, err := d.St.InstHoldingsByManager(ctx, cik, limitParam(r, 100, 500))
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		// Resolve ticker strings for matched holdings (cheap: distinct IDs).
		seen := map[int64]string{}
		for i := range rows {
			if rows[i].SymbolID == nil {
				continue
			}
			id := *rows[i].SymbolID
			if sym, ok := seen[id]; ok {
				rows[i].Symbol = sym
				continue
			}
			if ms, gerr := d.St.GetSymbolByID(ctx, id); gerr == nil {
				seen[id] = ms.Symbol
				rows[i].Symbol = ms.Symbol
			}
		}
		writeJSON(w, map[string]any{
			"holdings": rows, "count": len(rows), "cik": cik, "note": instNote,
		})
		return
	}
	s, given, err := d.stockFromQuery(r)
	if given {
		if err != nil {
			httpErr(w, 404, "unknown stock symbol")
			return
		}
		rows, herr := d.St.InstHoldingsBySymbol(ctx, s.ID, limitParam(r, 50, 200))
		if herr != nil {
			httpErr(w, 500, herr.Error())
			return
		}
		for i := range rows {
			rows[i].Symbol = s.Symbol
		}
		writeJSON(w, map[string]any{
			"holdings": rows, "count": len(rows), "symbol": s.Symbol, "note": instNote,
		})
		return
	}
	managers, err := d.St.InstManagers(ctx)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// The curated watchlist (even before any data lands) so the UI can show
	// what WILL be covered.
	curated := make([]map[string]any, 0, len(edgar.NotableManagers))
	for _, m := range edgar.NotableManagers {
		curated = append(curated, map[string]any{"cik": m.CIK, "name": m.Name})
	}
	writeJSON(w, map[string]any{
		"managers": managers, "curated": curated, "note": instNote,
	})
}

// resolveManagerCIK maps a ?manager= value (numeric CIK or name substring
// against the curated list) to a CIK string.
func resolveManagerCIK(q string) string {
	if _, err := strconv.ParseInt(q, 10, 64); err == nil {
		return q
	}
	needle := strings.ToLower(q)
	for _, m := range edgar.NotableManagers {
		if strings.Contains(strings.ToLower(m.Name), needle) {
			return strconv.FormatInt(m.CIK, 10)
		}
	}
	return ""
}

// dilution serves the derived dilution flag.
// GET /api/dilution?symbol=X  → that symbol's flag (level "low" + note when
// never derived — honest absence, not silence). No symbol ⇒ all flagged.
func (d Deps) dilution(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, given, err := d.stockFromQuery(r)
	if given {
		if err != nil {
			httpErr(w, 404, "unknown stock symbol")
			return
		}
		flag, ok, ferr := d.St.DilutionFlag(ctx, s.ID)
		if ferr != nil {
			httpErr(w, 500, ferr.Error())
			return
		}
		out := map[string]any{
			"symbol": s.Symbol, "derived": ok, "note": dilutionNote,
		}
		if ok {
			var reasons []string
			_ = json.Unmarshal([]byte(flag.Reasons), &reasons)
			out["level"] = flag.Level
			out["reasons"] = reasons
			out["updatedTs"] = flag.UpdatedTs
		} else {
			out["level"] = "unknown"
			out["reasons"] = []string{}
		}
		writeJSON(w, out)
		return
	}
	rows, err := d.St.DilutionFlagged(ctx, limitParam(r, 100, 500))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	type flagged struct {
		Symbol    string   `json:"symbol"`
		Level     string   `json:"level"`
		Reasons   []string `json:"reasons"`
		UpdatedTs int64    `json:"updatedTs"`
	}
	out := make([]flagged, 0, len(rows))
	for _, f := range rows {
		var reasons []string
		_ = json.Unmarshal([]byte(f.Reasons), &reasons)
		out = append(out, flagged{Symbol: f.Symbol, Level: f.Level, Reasons: reasons, UpdatedTs: f.UpdatedTs})
	}
	writeJSON(w, map[string]any{"flagged": out, "count": len(out), "note": dilutionNote})
}

// registerSignal8 wires the Stage-1 filings-intelligence read routes.
func (d Deps) registerSignal8(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/filings", d.filingsFeed)
	mux.HandleFunc("GET /api/insiders", d.insiders)
	mux.HandleFunc("GET /api/institutions", d.institutions)
	mux.HandleFunc("GET /api/dilution", d.dilution)
}
