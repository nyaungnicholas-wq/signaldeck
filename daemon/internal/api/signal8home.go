// Signal8 wave — Stage 4 API: the signal8-style HOME surfaces, built 100%
// from stored free data. Three read-only endpoints:
//
//   - GET /api/tape      — the ticker-tape strip: index/sector ETFs (stored
//     daily bars) + BTC (crypto daily bars) + VIX (FRED VIXCLS daily close).
//   - GET /api/movers    — gainers/losers over the daily universe, with
//     BEST-EFFORT market cap (EDGAR SharesOutstanding × last close; symbols
//     EDGAR hasn't covered are labeled "mcap unavailable", never fabricated).
//   - GET /api/calendar  — econ prints from the stored FRED series (honest
//     subset: FRED series only, no paid econ-event feed) + an earnings
//     "reports soon" list that is EXPLICITLY AN ESTIMATE (last SEC filing
//     date + ~1 quarter). IPO calendar is deliberately OMITTED: there is no
//     free, redistributable source of IPO pricing/listing dates (EDGAR S-1s
//     show intent, not dates) — fabricating one would violate the honesty
//     doctrine, so the surface simply doesn't exist.
//
// HONESTY: every payload states its cadence and lag. Tape prices are stored
// DAILY closes refreshed on worker cadence (not tick-live quotes); VIX is the
// FRED daily close (~1 trading day behind); mcap and earnings dates carry
// their own provenance notes.
package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/fred"
	"github.com/nyaungnicholas-wq/signaldeck/internal/universe"
)

const (
	tapeNote = "Prices are stored daily closes refreshed on worker cadence — NOT live quotes. VIX is the FRED VIXCLS daily close (lags ~1 trading day). Day % is vs the prior stored close."
	vixNote  = "FRED VIXCLS daily close — updated once per day by FRED, lags ~1 trading day."

	moversNote = "Computed from stored daily universe bars (free IEX feed, worker cadence). Index/sector ETFs are excluded — they are baskets, not single-name moves. Symbols with no bar in the last 5 days are skipped."
	mcapNote   = "Market cap = SEC EDGAR SharesOutstanding × last stored close (best-effort). Symbols EDGAR hasn't covered show 'mcap unavailable' — never a fabricated number. With a min-mcap filter active, unknown-mcap symbols are excluded and counted."

	econNote     = "FRED series prints only (VIX, 10y yield, 10y-2y spread, fed funds) — no paid economic-event feed, and upcoming-release dates aren't available on the keyless FRED endpoint, so this shows the LATEST OBSERVED prints, not a forward calendar."
	earningsNote = "ESTIMATE ONLY: derived as each company's last SEC filing date + ~91 days (quarterly filer heuristic). NOT a confirmed earnings date — there is no free confirmed-date feed. Companies may pre-announce, delay, or file on a different cycle."
)

// econLabels maps the tracked FRED series ids to display names.
var econLabels = map[string]string{
	"VIXCLS": "VIX close",
	"DGS10":  "10y Treasury yield",
	"T10Y2Y": "10y-2y spread",
	"DFF":    "Effective fed funds",
}

// tapeItem is one strip entry.
type tapeItem struct {
	Symbol       string  `json:"symbol"`
	Label        string  `json:"label"`
	Kind         string  `json:"kind"`             // index | sector | crypto | vix
	Market       string  `json:"market,omitempty"` // stocks | crypto ("" for vix)
	Price        float64 `json:"price"`
	DayChangePct float64 `json:"dayChangePct"`
	Ts           int64   `json:"ts"`
	HasData      bool    `json:"hasData"` // false = registered but no bars yet (honest absence)
	Note         string  `json:"note,omitempty"`
}

// (lastTwoCloses moved to dashboard.go as lastTwoClosesCtx — the ctx-based
// form both the tape and the dashboard builders share.)

func pctChange(last, prev float64) float64 {
	if prev == 0 {
		return 0
	}
	return (last/prev - 1) * 100
}

// tape serves the ticker-tape strip in one call. Assembly lives in buildTape
// (dashboard.go) so GET /api/tape and the /api/dashboard tape section can
// never drift apart.
// GET /api/tape
func (d Deps) tape(w http.ResponseWriter, r *http.Request) {
	items := d.buildTape(r.Context())
	writeJSON(w, map[string]any{
		"items": items,
		"count": len(items),
		"note":  tapeNote,
	})
}

// moverRow is one gainers/losers table entry.
type moverRow struct {
	Symbol       string   `json:"symbol"`
	Name         string   `json:"name"`
	Price        float64  `json:"price"`
	DayChangePct float64  `json:"dayChangePct"`
	Mcap         *float64 `json:"mcap"`               // nil = "mcap unavailable" (EDGAR hasn't covered it)
	McapAsOf     int64    `json:"mcapAsOf,omitempty"` // SharesOutstanding as-of date (epoch)
	Ts           int64    `json:"ts"`                 // latest bar ts
}

// movers serves gainers/losers from the daily universe bars.
// GET /api/movers?limit=&minMcap=   (minMcap in dollars; 0 = no filter)
// ?limit bounds for /api/movers. Shared with moversCacheKey (cachekey.go) so
// the cache key and the handler can never resolve the same query differently.
const (
	moversDefaultLimit = 10
	moversMaxLimit     = 50
)

func (d Deps) movers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := limitParam(r, moversDefaultLimit, moversMaxLimit)
	minMcap, _ := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get("minMcap")), 64)
	if minMcap < 0 || minMcap != minMcap { // negative or NaN → no filter
		minMcap = 0
	}

	syms, err := d.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		httpInternal(w, err)
		return
	}
	shares, err := d.St.LatestMetricAll(ctx, "SharesOutstanding")
	if err != nil {
		httpInternal(w, err)
		return
	}
	// ONE batched query for every symbol's last two daily closes — the old
	// per-symbol LastBars walk was ~2 queries per active stock per request.
	closes, err := d.St.LastTwoDailyCloses(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}

	staleCutoff := time.Now().Unix() - 5*86400
	unknownExcluded := 0
	suspectExcluded := 0
	rows := make([]moverRow, 0, len(syms))
	for _, s := range syms {
		if universe.IsTapeETF(s.Symbol) {
			continue // baskets, not single-name moves
		}
		dc, ok := closes[s.ID]
		if !ok || dc.Prev == 0 || dc.Ts < staleCutoff {
			continue
		}
		last, prev, ts := dc.Last, dc.Prev, dc.Ts
		// 2026-07-18 accuracy pass: a >65% one-day close ratio is almost
		// always an UNADJUSTED SPLIT artifact (incremental fetches straddling
		// a reverse split — CELUW "+533%" was the tell), not a real move.
		// Excluded and counted, never ranked as a gainer/loser.
		if c := pctChange(last, prev); c > 65 || c < -65 {
			suspectExcluded++
			continue
		}
		row := moverRow{
			Symbol: s.Symbol, Name: s.Name,
			Price: last, DayChangePct: pctChange(last, prev), Ts: ts,
		}
		if f, has := shares[s.ID]; has && f.Value > 0 {
			mc := f.Value * last
			row.Mcap, row.McapAsOf = &mc, f.AsOf
		}
		if minMcap > 0 {
			if row.Mcap == nil {
				unknownExcluded++
				continue
			}
			if *row.Mcap < minMcap {
				continue
			}
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].DayChangePct > rows[j].DayChangePct })
	gainers := make([]moverRow, 0, limit)
	losers := make([]moverRow, 0, limit)
	for i := 0; i < len(rows) && i < limit; i++ {
		gainers = append(gainers, rows[i])
	}
	for i := len(rows) - 1; i >= 0 && len(losers) < limit; i-- {
		losers = append(losers, rows[i])
	}

	note := moversNote
	if suspectExcluded > 0 {
		note += " " + strconv.Itoa(suspectExcluded) + " symbol(s) excluded for a >65% one-day jump — almost always an unadjusted corporate action in the stored bars, not a real move."
	}
	writeJSON(w, map[string]any{
		"gainers":             gainers,
		"losers":              losers,
		"universeN":           len(rows),
		"minMcap":             minMcap,
		"unknownMcapExcluded": unknownExcluded, // >0 only when a min-mcap filter is active
		"suspectExcluded":     suspectExcluded,
		"note":                note,
		"mcapNote":            mcapNote,
		"asOf":                time.Now().Unix(),
	})
}

// econPrint is one latest-observation row of a tracked FRED series.
type econPrint struct {
	Series string  `json:"series"`
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	Ts     int64   `json:"ts"` // observation day (UTC-midnight epoch)
}

// earningsEst is one "reports soon" ESTIMATE row.
type earningsEst struct {
	Symbol       string `json:"symbol"`
	Name         string `json:"name"`
	LastFilingTs int64  `json:"lastFilingTs"` // last SEC filing date (epoch)
	EstTs        int64  `json:"estTs"`        // ESTIMATED next report date (epoch)
	Estimate     bool   `json:"estimate"`     // always true — rendered as a label
}

// calendar serves the honest free-data calendar card: FRED econ prints +
// filing-derived earnings ESTIMATES. (No IPO section — see package comment:
// no free source exists, so none is faked.)
// GET /api/calendar
func (d Deps) calendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Econ: latest observed print of each tracked FRED series, stable order.
	latest, err := d.St.LatestMacroAll(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}
	econ := make([]econPrint, 0, len(fred.DefaultSeries))
	for _, series := range fred.DefaultSeries {
		p, ok := latest[series]
		if !ok {
			continue // not fetched yet — honest absence
		}
		label := econLabels[series]
		if label == "" {
			label = series
		}
		econ = append(econ, econPrint{Series: series, Label: label, Value: p.Value, Ts: p.Ts})
	}

	// Earnings (EST): last SEC filing date + ~91 days, within a window of
	// [-14d overdue … +21d upcoming]. Quarterly-filer heuristic, labeled.
	filings, err := d.St.LatestMetricAll(ctx, "LatestFilingDate")
	if err != nil {
		httpInternal(w, err)
		return
	}
	now := time.Now().Unix()
	const quarter = 91 * 86400
	var ests []earningsEst
	if len(filings) > 0 {
		syms, serr := d.St.ActiveStockSymbols(ctx, nil)
		if serr != nil {
			httpInternal(w, serr)
			return
		}
		for _, s := range syms {
			f, has := filings[s.ID]
			if !has || f.Value <= 0 {
				continue
			}
			lastFiled := int64(f.Value)
			est := lastFiled + quarter
			if est < now-14*86400 || est > now+21*86400 {
				continue
			}
			ests = append(ests, earningsEst{
				Symbol: s.Symbol, Name: s.Name,
				LastFilingTs: lastFiled, EstTs: est, Estimate: true,
			})
		}
		sort.Slice(ests, func(i, j int) bool { return ests[i].EstTs < ests[j].EstTs })
		if len(ests) > 30 {
			ests = ests[:30]
		}
	}

	writeJSON(w, map[string]any{
		"econ":         econ,
		"econNote":     econNote,
		"earningsEst":  ests,
		"earningsNote": earningsNote,
		"asOf":         now,
	})
}

// registerSignal8Home wires the Stage-4 home-surface read routes. Movers sits
// behind the SHARED response cache (cold-load precompute wave, warm.go) so the
// cache-warmer worker keeps its default entry hot; parameterized calls cache
// per WHITELISTED parameter (cachekey.go). It keyed on the raw query string
// until C6 (2026-07-26), which made every novel query — `?zz=1` included — a
// cold build holding one of four read connections, and leaked the entry
// permanently because nothing evicted the map.
func (d Deps) registerSignal8Home(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tape", d.tape)
	mux.HandleFunc("GET /api/movers", func(w http.ResponseWriter, r *http.Request) {
		sharedMoversCache.serve(moversCacheKey(r), w, r, d.movers)
	})
	mux.HandleFunc("GET /api/calendar", d.calendar)
}
