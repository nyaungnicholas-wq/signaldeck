package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/fred"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ─────────────────────────────────────────────────────────────────────────
// FREE-DATA WAVE (Stage 2) API handlers: FRED macro series + SEC EDGAR
// fundamentals. Both are read-only and gated like every other read endpoint.
// ─────────────────────────────────────────────────────────────────────────

// macroSeries serves FRED macro data. Without ?series= it returns the latest
// value of every tracked series (an overview). With ?series=VIXCLS it returns a
// chart-ready time series (oldest first), capped by ?limit= (default 400).
func (d Deps) macroSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	series := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("series")))
	if series == "" {
		latest, err := d.St.LatestMacroAll(ctx)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{
			"latest":  latest,
			"tracked": fred.DefaultSeries,
			"note":    "Free macro from FRED (St. Louis Fed): VIXCLS=VIX, DGS10=10y yield, T10Y2Y=10y-2y spread, DFF=fed funds, DGS2=2y yield, T10Y3M=10y-3m spread, BAMLH0A0HYM2=high-yield OAS, NFCI=financial conditions, UNRATE=unemployment, CPIAUCSL=CPI, M2SL=M2. No paid feed; keyless CSV endpoint. Monthly/weekly series carry their latest published observation.",
		})
		return
	}
	limit := 400
	if l := strings.TrimSpace(r.URL.Query().Get("limit")); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	pts, err := d.St.MacroSeries(ctx, series, limit)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	latest, ok, _ := d.St.LatestMacro(ctx, series)
	writeJSON(w, map[string]any{
		"series": series,
		"points": pts,
		"latest": macroLatest(latest, ok),
		"count":  len(pts),
	})
}

// macroLatest returns the latest point or nil (honest absence).
func macroLatest(p any, ok bool) any {
	if !ok {
		return nil
	}
	return p
}

// fundamentals serves SEC EDGAR company-facts for one stock (?symbol=AAPL).
// market defaults to stocks (crypto has no filings). Returns the latest value
// of each metric; ?history=Revenues adds that metric's full time series.
func (d Deps) fundamentals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sym := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	if sym == "" {
		httpErr(w, 400, "need symbol=")
		return
	}
	s, err := d.St.GetSymbol(ctx, sym, md.Stocks)
	if err != nil {
		httpErr(w, 404, "unknown stock symbol (fundamentals cover US equities only)")
		return
	}
	latest, err := d.St.LatestFundamentals(ctx, s.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// attach the resolved symbol string for the UI
	for i := range latest {
		latest[i].Symbol = s.Symbol
	}
	out := map[string]any{
		"symbol":  s.Symbol,
		"metrics": latest,
		"note":    "Free fundamentals from SEC EDGAR (companyfacts XBRL). Dates/CIK are epoch/int values. Refreshed ~daily; a symbol with no rows hasn't been swept yet or doesn't file with the SEC.",
	}
	if metric := strings.TrimSpace(r.URL.Query().Get("history")); metric != "" {
		hist, err := d.St.FundamentalHistory(ctx, s.ID, metric)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		for i := range hist {
			hist[i].Symbol = s.Symbol
		}
		out["history"] = hist
		out["historyMetric"] = metric
	}
	writeJSON(w, out)
}

// registerFreeData wires the Stage-2 free-data read routes.
func (d Deps) registerFreeData(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/macro-series", d.macroSeries)
	mux.HandleFunc("GET /api/fundamentals", d.fundamentals)
}
