// Stage 5 — FINRA Reg SHO API: daily short sale volume (read-only, gated
// like every other read endpoint). Data source: FINRA's free, registration-
// less Consolidated NMS daily files, ingested universe-scoped by the
// finra-shorts worker.
//
// HONESTY, verbatim in every payload (the classic retail trap): this is the
// daily short sale VOLUME ratio — NOT short interest; it includes market-
// maker activity; a high ratio is NOT directly bearish. The optional z-score
// is DESCRIPTIVE (latest ratio vs the symbol's own trailing baseline) and is
// deliberately NOT fed into the anomalies table: its kind CHECK constraint
// (anomaly_imbalance|anomaly_vol|anomaly_volume) cannot accept a new kind on
// existing live databases without a table rebuild, so the z lives here,
// labeled, instead of pretending to be an anomaly detection.
package api

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const (
	// shortsCaveat ships VERBATIM with every payload and every UI surface.
	shortsCaveat = "short sale volume ratio (Reg SHO daily) — NOT short interest; includes market-maker activity; a high ratio is NOT directly bearish"
	shortsNote   = "Aggregated daily short-sale volume from FINRA's free Reg SHO Consolidated NMS files (cdn.finra.org/equity/regsho/daily, posted by ~6pm ET on the trade date; no key or registration). Universe-scoped: only symbols SignalDeck tracks are stored. Fractional share volumes appear in the source files and are kept as-is."
	shortsZNote  = "z-score of the latest ratio vs this symbol's own trailing days in the window (needs 10+ prior days, stated stddev floor) — descriptive only; the volume-ratio caveat applies"
	// shortsMinTotalVol floors the fleet-wide extremes table: sub-100k-share
	// names produce noise ratios from tiny denominators. The floor is stated
	// in the payload, never applied silently.
	shortsMinTotalVol = 100_000
	shortsFloorNote   = "extremes exclude symbols with total volume below minTotalVol on the day — tiny denominators make noise ratios; the floor is stated, not hidden"
	// shortsZMinPrior: minimum prior days before a z is computed at all.
	shortsZMinPrior = 10
)

// shortsZ computes the z-score of the LAST value vs the mean/stddev of all
// PRIOR values. ok=false below shortsZMinPrior prior points or when the
// baseline stddev is ~0 (a z against a flat baseline is meaningless).
func shortsZ(ratios []float64) (z float64, ok bool) {
	if len(ratios) < shortsZMinPrior+1 {
		return 0, false
	}
	prior := ratios[:len(ratios)-1]
	mean := 0.0
	for _, v := range prior {
		mean += v
	}
	mean /= float64(len(prior))
	varSum := 0.0
	for _, v := range prior {
		varSum += (v - mean) * (v - mean)
	}
	sd := math.Sqrt(varSum / float64(len(prior)))
	if sd < 1e-9 {
		return 0, false
	}
	return (ratios[len(ratios)-1] - mean) / sd, true
}

// shorts serves Reg SHO daily short sale volume.
// GET /api/shorts?symbol=&days=&limit=
//   - with symbol: that stock's series (ASC, default 30 days, cap 365) + a
//     labeled descriptive z of the latest ratio vs its own trailing window;
//   - without: fleet-wide latest-day TOP ratios over tracked symbols (with a
//     stated min-total-volume floor) + a 30d ratio spark per row.
func (d Deps) shorts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}

	out := map[string]any{
		"caveat": shortsCaveat,
		"note":   shortsNote,
	}

	if symbol != "" {
		sym, err := d.St.GetSymbol(ctx, symbol, md.Stocks)
		if err != nil {
			httpErr(w, 404, "symbol not tracked — Reg SHO ingestion is universe-scoped to tracked stocks")
			return
		}
		series, err := d.St.ShortVolumeSeries(ctx, sym.ID, days)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		out["symbol"] = symbol
		out["series"] = series
		out["days"] = days
		ratios := make([]float64, 0, len(series))
		for _, p := range series {
			ratios = append(ratios, p.ShortPct)
		}
		if z, ok := shortsZ(ratios); ok {
			out["latestZ"] = z
		} else {
			out["latestZ"] = nil
		}
		out["zNote"] = shortsZNote
		writeJSON(w, out)
		return
	}

	day, err := d.St.LatestShortVolumeDay(ctx)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	out["day"] = day
	out["minTotalVol"] = shortsMinTotalVol
	out["floorNote"] = shortsFloorNote
	if day == "" {
		// Honest absence: worker hasn't ingested anything yet.
		out["extremes"] = []any{}
		out["emptyNote"] = "no Reg SHO data stored yet — the finra-shorts worker ingests the latest file after ~6:30pm ET and backfills ~30 trading days on first run"
		writeJSON(w, out)
		return
	}
	ext, err := d.St.ShortVolumeExtremes(ctx, day, shortsMinTotalVol, limitParam(r, 20, 100))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	type extreme struct {
		SymbolID    int64     `json:"symbolId"`
		Symbol      string    `json:"symbol"`
		Day         string    `json:"day"`
		ShortVol    float64   `json:"shortVol"`
		ShortExempt float64   `json:"shortExempt"`
		TotalVol    float64   `json:"totalVol"`
		ShortPct    float64   `json:"shortPct"`
		Spark       []float64 `json:"spark"` // last ≤30 daily ratios, ASC
	}
	rows := make([]extreme, 0, len(ext))
	for _, e := range ext {
		row := extreme{SymbolID: e.SymbolID, Symbol: e.Symbol, Day: e.Day,
			ShortVol: e.ShortVol, ShortExempt: e.ShortExempt,
			TotalVol: e.TotalVol, ShortPct: e.ShortPct, Spark: []float64{}}
		if series, serr := d.St.ShortVolumeSeries(ctx, e.SymbolID, 30); serr == nil {
			for _, p := range series {
				row.Spark = append(row.Spark, p.ShortPct)
			}
		}
		rows = append(rows, row)
	}
	out["extremes"] = rows
	writeJSON(w, out)
}

// registerShorts wires the Stage-5 Reg SHO read route.
func (d Deps) registerShorts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/shorts", d.shorts)
}
