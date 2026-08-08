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
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	anomalyNote      = "Descriptive statistics, not predictions: each row is a z-score of recent activity vs the SAME symbol's own trailing baseline (each detail states the window and baseline it was measured over). A high |z| means 'statistically unusual vs this symbol's recent past' — it implies nothing about what happens next."
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
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"anomalies": structuredAnomalies(rows),
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

// ─────────────────────────────────────────────────────────────────────────
// SIGNALS-HUB OVERHAUL — STRUCTURED ANOMALY FIELDS (appended block).
// Each response row now ALSO carries measure ("z" | "ratio"), value, and
// proxy — the machine-readable form of what the web previously string-sniffed
// out of `detail` (UnusualActivityPanel.tsx). Strictly additive: every
// existing field is untouched, and the derivation matches the web's sniffing
// semantics exactly so the two can never disagree:
//   - measure "ratio" ⇔ detail contains "TR/ATR ratio" (the true-range spike
//     form of anomaly_vol stores the TR/ATR RATIO in z — see
//     anomaly.DetectVolatility); everything else is a z-score;
//   - proxy ⇔ detail carries the verbatim stock volume-side proxy label (no
//     order book exists on free stock data);
//   - value is the row's stored statistic verbatim (z or ratio — measure
//     says which).

// anomalyRowOut is one anomaly row plus its structured measure fields.
type anomalyRowOut struct {
	store.AnomalyRow
	Measure string  `json:"measure"` // "z" | "ratio"
	Value   float64 `json:"value"`
	Proxy   bool    `json:"proxy"`
}

// structuredAnomalies derives the structured fields for each row.
func structuredAnomalies(rows []store.AnomalyRow) []anomalyRowOut {
	out := make([]anomalyRowOut, 0, len(rows))
	for _, a := range rows {
		measure := "z"
		if strings.Contains(a.Detail, "TR/ATR ratio") {
			measure = "ratio"
		}
		out = append(out, anomalyRowOut{
			AnomalyRow: a,
			Measure:    measure,
			Value:      a.Z,
			Proxy:      strings.Contains(a.Detail, "volume-side proxy"),
		})
	}
	return out
}
