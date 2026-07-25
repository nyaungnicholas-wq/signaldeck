// SMART MONEY FACTS wave API: the per-symbol Smart Money Score + leaderboard.
// Read-only, gated like every other read endpoint.
//
// HONESTY, carried VERBATIM in every payload (smartMoneyCaveat): the score is a
// read of what INFORMED PARTICIPANTS ARE DOING — positioning from public
// filings — NOT a price forecast. 13F is quarterly and ~45 days lagged; the
// short-volume ratio is NOT short interest and includes market-maker flow. A
// symbol with no insider/short/institutional data has no score (honest absence,
// never a fabricated neutral).
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/smartmoney"
)

// smartMoneyCaveat ships VERBATIM with every payload and every UI surface.
const smartMoneyCaveat = "Positioning from public filings — what informed participants are DOING, not a price forecast. 13F is quarterly and ~45 days lagged; short-volume ratio is not short interest and includes market-maker flow."

// smartMoneySymbol resolves ?symbol= (with optional &market=) to a tracked
// symbol. The INTEL hub filter passes a bare ticker, so with no explicit market
// we try stocks (the common case for filings/short data) then crypto.
// given=false means no symbol was supplied.
func (d Deps) smartMoneySymbol(r *http.Request) (sym md.Symbol, given bool, err error) {
	s := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	if s == "" {
		return md.Symbol{}, false, nil
	}
	if m := md.Market(r.URL.Query().Get("market")); m == md.Crypto || m == md.Stocks {
		row, err := d.St.GetSymbol(r.Context(), s, m)
		return row, true, err
	}
	if row, err := d.St.GetSymbol(r.Context(), s, md.Stocks); err == nil {
		return row, true, nil
	}
	row, err := d.St.GetSymbol(r.Context(), s, md.Crypto)
	return row, true, err
}

// smartMoney serves one symbol's decomposed Smart Money Score.
// GET /api/smart-money?symbol=[&market=]
func (d Deps) smartMoney(w http.ResponseWriter, r *http.Request) {
	s, given, err := d.smartMoneySymbol(r)
	if !given {
		httpErr(w, 400, "need symbol=")
		return
	}
	if err != nil {
		httpErr(w, 404, "unknown symbol — smart-money scoring is universe-scoped to tracked symbols")
		return
	}
	row, ok, err := d.St.LatestSmartMoneyScore(r.Context(), s.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !ok {
		// Honest absence: no positioning data (or the scorer hasn't run yet).
		writeJSON(w, map[string]any{
			"available": false,
			"symbol":    s.Symbol,
			"market":    s.Market,
			"reason":    "no insider, short, or institutional data for this symbol yet",
			"caveat":    smartMoneyCaveat,
		})
		return
	}
	var pay smartmoney.Payload
	if err := json.Unmarshal([]byte(row.Payload), &pay); err != nil {
		httpErr(w, 500, "stored payload does not parse: "+err.Error())
		return
	}
	factors := pay.Factors
	if factors == nil {
		factors = []smartmoney.Factor{}
	}
	writeJSON(w, map[string]any{
		"available": true,
		"symbol":    s.Symbol,
		"market":    s.Market,
		"ts":        row.Ts,
		"score":     row.Score,
		"label":     row.Label,
		"caveat":    smartMoneyCaveat,
		"factors":   factors,
		"insiderCluster": map[string]any{
			"distinctBuyers": pay.DistinctBuyers,
			"netValue":       pay.InsiderNet,
			"windowDays":     pay.WindowDays,
		},
		"squeeze": map[string]any{
			"daysToCover": pay.DaysToCover, // null when no short-interest row
			"shortVolZ":   pay.ShortVolZ,   // null below the z gate
			"funding":     pay.Funding,     // null unless a fresh crypto perp snapshot
		},
	})
}

// smartMoneyTopRow is one leaderboard row.
type smartMoneyTopRow struct {
	Symbol    string  `json:"symbol"`
	Market    string  `json:"market"`
	Score     float64 `json:"score"`
	Label     string  `json:"label"`
	TopFactor string  `json:"topFactor"` // the largest-contribution factor's label
	Ts        int64   `json:"ts"`
}

// smartMoneyTop serves the ranked Smart Money leaderboard (accumulation first).
// GET /api/smart-money/top?market=&limit=
func (d Deps) smartMoneyTop(w http.ResponseWriter, r *http.Request) {
	market := ""
	if m := md.Market(r.URL.Query().Get("market")); m == md.Crypto || m == md.Stocks {
		market = string(m)
	}
	rows, err := d.St.TopSmartMoneyScores(r.Context(), market, limitParam(r, 50, 500))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if len(rows) == 0 {
		writeJSON(w, map[string]any{
			"available": false,
			"reason":    "no smart-money scores stored yet — need insider/short/institutional data for at least one symbol",
			"caveat":    smartMoneyCaveat,
		})
		return
	}
	out := make([]smartMoneyTopRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, smartMoneyTopRow{
			Symbol: row.Symbol, Market: row.Market, Score: row.Score,
			Label: row.Label, TopFactor: topFactorLabel(row.Payload), Ts: row.Ts,
		})
	}
	writeJSON(w, map[string]any{
		"available": true,
		"caveat":    smartMoneyCaveat,
		"rows":      out,
	})
}

// topFactorLabel returns the label of the factor with the largest ABSOLUTE
// contribution (value*weight) — the one actually driving the score. "" when the
// payload has no factors or does not parse (honest, never a guess).
func topFactorLabel(payload string) string {
	var pay smartmoney.Payload
	if err := json.Unmarshal([]byte(payload), &pay); err != nil {
		return ""
	}
	best, bestMag := "", -1.0
	for _, f := range pay.Factors {
		mag := f.Value * f.Weight
		if mag < 0 {
			mag = -mag
		}
		if mag > bestMag {
			bestMag, best = mag, f.Label
		}
	}
	return best
}

// registerSmartMoney wires the SMART MONEY FACTS read routes.
func (d Deps) registerSmartMoney(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/smart-money", d.smartMoney)
	mux.HandleFunc("GET /api/smart-money/top", d.smartMoneyTop)
}
