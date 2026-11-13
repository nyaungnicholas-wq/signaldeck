package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
)

// publicForecastRow is one call as a visitor sees it: what was called, for
// which symbol, over what horizon, and how far the reading sits from its
// threshold. Nothing else.
type publicForecastRow struct {
	Symbol      string  `json:"symbol"`
	Kind        string  `json:"kind"` // vol63 | vol21 | trend21 | trend63 | liquidity21
	HorizonDays int     `json:"horizonDays"`
	Regime      string  `json:"regime"`
	Conviction  float64 `json:"conviction"`
	Ts          int64   `json:"ts"`
}

// publicForecasts is the ONE forecast read a visitor without an account gets on
// an instance fed by a displayable price source (visitorForecastRoutes).
//
// It is an allowlist by construction. The member and operator forecast routes
// carry accuracy tiers, methodology text and caveats whose figures were
// measured on Alpaca bars; a deny-list over those payloads would miss the next
// field someone adds. This builds its answer from the stored rows and names
// every field it sends.
func (d Deps) publicForecasts(w http.ResponseWriter, r *http.Request) {
	src := d.Cfg.PublicPriceSource
	if !datalicense.PublicPriceCleared(src) {
		// Defence in depth: step 6c already refuses this path on the default
		// instance for every non-operator.
		writeWithheld(w, "alpaca")
		return
	}
	vols, err := d.St.VolForecasts(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	regs, err := d.St.RegimeForecasts(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	rows := make([]publicForecastRow, 0, len(vols)+len(regs))
	var newest int64
	for _, v := range vols {
		if v.Market != "stocks" {
			continue
		}
		rows = append(rows, publicForecastRow{Symbol: v.Symbol, Kind: "vol63", HorizonDays: 63,
			Regime: v.Regime, Conviction: v.Conviction, Ts: v.Ts})
		if v.Ts > newest {
			newest = v.Ts
		}
	}
	for _, f := range regs {
		if f.Market != "stocks" {
			continue
		}
		rows = append(rows, publicForecastRow{Symbol: f.Symbol, Kind: string(f.Kind), HorizonDays: f.HorizonDays,
			Regime: f.Regime, Conviction: f.Conviction, Ts: f.Ts})
		if f.Ts > newest {
			newest = f.Ts
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Symbol != rows[j].Symbol {
			return rows[i].Symbol < rows[j].Symbol
		}
		return rows[i].Kind < rows[j].Kind
	})
	out := map[string]any{
		"source": src,
		"rows":   rows,
		"note": "Each row is a rule-based reading of recent price and volume, not advice and not a " +
			"measured edge. This record is new: no accuracy has been measured on this data yet.",
	}
	if src == "iexhist" {
		out["citation"] = datalicense.IEXCitation
		out["coverage"] = "Prices and volumes are trades on the IEX exchange only, from the previous trading day."
	}
	if newest > 0 {
		out["computedAt"] = time.Unix(newest, 0).UTC().Format(time.RFC3339)
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, out)
}
