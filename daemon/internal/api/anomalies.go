// Signal8 wave — Stage 3 API: the anomaly layer (trade imbalance, unusual
// volatility, unusual volume). Read-only, gated like every other read
// endpoint. HONESTY: every payload carries `note` — the rows are DESCRIPTIVE
// z-scores against the symbol's own trailing baseline (each row's detail
// states the window + baseline), NOT predictions; stock "imbalance" is a
// volume-side proxy because free stock data has no order book, and
// `proxyNote` states that separately so the UI can badge it.
package api

import (
	"net/http"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/anomaly"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const (
	anomalyNote = "Descriptive statistics, not predictions: each row is a z-score of recent activity vs the SAME symbol's own trailing baseline (each detail states the window and baseline it was measured over). A high |z| means 'statistically unusual vs this symbol's recent past' — it implies nothing about what happens next."
	anomalyProxyNote = "Stock 'imbalance' rows are a volume-side PROXY (up-volume vs down-volume on 1m bars) — free stock data has no order book. Crypto imbalance rows are real order-book imbalance from 1Hz snapshots."
)

// anomaliesList serves stored anomalies, newest first.
// GET /api/anomalies?symbol=&market=&kind=&limit=
//   - no symbol ⇒ fleet-wide feed;
//   - symbol with market ⇒ that exact symbol; symbol without market tries
//     stocks first then crypto (symbols are unambiguous across the two in
//     practice; BTC/USD-style pairs only exist in crypto);
//   - kind ∈ {anomaly_imbalance, anomaly_vol, anomaly_volume} filters.
func (d Deps) anomaliesList(w http.ResponseWriter, r *http.Request) {
	var symbolID int64
	symName := ""
	if sym := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol"))); sym != "" {
		mkt := md.Market(r.URL.Query().Get("market"))
		var (
			s   md.Symbol
			err error
		)
		if mkt == md.Crypto || mkt == md.Stocks {
			s, err = d.St.GetSymbol(r.Context(), sym, mkt)
		} else {
			s, err = d.St.GetSymbol(r.Context(), sym, md.Stocks)
			if err != nil {
				s, err = d.St.GetSymbol(r.Context(), sym, md.Crypto)
			}
		}
		if err != nil {
			httpErr(w, 404, "unknown symbol")
			return
		}
		symbolID = s.ID
		symName = s.Symbol
	}

	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	switch kind {
	case "", anomaly.KindImbalance, anomaly.KindVol, anomaly.KindVolume:
	default:
		httpErr(w, 400, "kind must be one of anomaly_imbalance|anomaly_vol|anomaly_volume")
		return
	}

	rows, err := d.St.Anomalies(r.Context(), symbolID, kind, limitParam(r, 50, 500))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"anomalies": rows,
		"count":     len(rows),
		"symbol":    symName,
		"kind":      kind,
		"note":      anomalyNote,
		"proxyNote": anomalyProxyNote,
	})
}

func (d Deps) registerAnomalies(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/anomalies", d.anomaliesList)
}
