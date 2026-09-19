// Signal8 wave — Stage 5 API: the COMPANIES DIRECTORY + the filing-cadence
// earnings-ESTIMATE calendar. Two read-only endpoints:
//
//   - GET /api/companies — the full SEC-registered company table (free EDGAR
//     company_tickers_exchange.json, synced daily) JOINED to our own tracked
//     data where present: price/chg%/volume from stored daily bars, mcap =
//     EDGAR SharesOutstanding × last close, float = EDGAR EntityPublicFloat.
//     Rows we do NOT track show name/exchange/SIC only — every market column
//     is null (rendered "—"), NEVER a fabricated number. Batched SQL only:
//     one directory query + three fleet-wide maps per request, no per-company
//     queries over the ~10.4k rows.
//   - GET /api/earnings-est — per universe symbol, the next 10-Q/10-K due
//     ESTIMATE: last periodic filing date + ~91 days (quarterly-filer
//     heuristic). EXPLICITLY LABELED an estimate — there is no free
//     confirmed-earnings-date feed, and the payload says so.
//
// HONESTY: prices are stored daily closes on worker cadence (not live
// quotes); sector = the SEC's own SIC industry description, filled in as the
// filings-poller sweeps (absent = not yet classified, not "unknown sector");
// mcap/float are best-effort EDGAR facts with their as-of dates.
package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const (
	companiesNote     = "Directory = SEC EDGAR company_tickers_exchange.json (synced daily, one free request). Price/chg/volume are OUR stored daily closes (worker cadence, not live quotes) and exist only for tracked symbols; untracked rows show '—', never a fabricated number. Sector is the SEC SIC industry description, filled in as the filings-poller sweeps."
	companiesMcapNote = "mcap = SEC EDGAR SharesOutstanding × last stored close; float = EDGAR EntityPublicFloat (USD, as filed). Best-effort: symbols EDGAR hasn't covered have null mcap/float. With a mcap filter active, unknown-mcap rows are excluded and counted."
	earningsEstNote   = "ESTIMATED FROM FILING CADENCE — NOT A CONFIRMED DATE. Next report ≈ last 10-Q/10-K filing date + ~91 days (quarterly-filer heuristic; amendments excluded). Companies pre-announce, delay, and shift cycles; there is no free confirmed-earnings-date feed, so none is faked."
)

// estQuarter is the quarterly-filer cadence the estimate assumes (~91 days).
const estQuarter = 91 * 86400

// NextPeriodicEstimate is the PURE earnings-estimate rule: the next periodic
// report is estimated at the last 10-Q/10-K filing date + ~91 days.
func NextPeriodicEstimate(lastFiledTs int64) int64 { return lastFiledTs + estQuarter }

// companyDirRow is one directory table row. Pointer fields are null when we
// have no real data (untracked symbol, or EDGAR hasn't covered it) — the UI
// renders "—" for null, per the honesty doctrine.
type companyDirRow struct {
	Ticker   string `json:"ticker"`
	Name     string `json:"name"`
	Exchange string `json:"exchange"` // "" = SEC lists no exchange (render "—")
	CIK      int64  `json:"cik"`
	SIC      string `json:"sic"`
	SICDesc  string `json:"sicDesc"` // "" = not yet classified by a sweep
	Tracked  bool   `json:"tracked"`

	Price             *float64 `json:"price"`             // last stored daily close
	DayChangePct      *float64 `json:"dayChangePct"`      // vs prior stored close
	Volume            *float64 `json:"volume"`            // latest daily bar volume
	Mcap              *float64 `json:"mcap"`              // shares × price (EDGAR best-effort)
	SharesOutstanding *float64 `json:"sharesOutstanding"` // EDGAR dei fact
	Float             *float64 `json:"float"`             // EDGAR EntityPublicFloat (USD)
	BarTs             int64    `json:"barTs,omitempty"`   // ts of the close shown
}

// companies serves the directory.
// GET /api/companies?q=&sector=&exchange=&mcapMin=&mcapMax=&tracked=&limit=&offset=
func (d Deps) companies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	qp := r.URL.Query()

	limit, _ := strconv.Atoi(qp.Get("limit"))
	if limit <= 0 || limit > 200 {
		if limit > 200 {
			limit = 200
		} else {
			limit = 50
		}
	}
	offset, _ := strconv.Atoi(qp.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	mcapMin, _ := strconv.ParseFloat(strings.TrimSpace(qp.Get("mcapMin")), 64)
	mcapMax, _ := strconv.ParseFloat(strings.TrimSpace(qp.Get("mcapMax")), 64)
	if mcapMin < 0 || mcapMin != mcapMin {
		mcapMin = 0
	}
	if mcapMax < 0 || mcapMax != mcapMax {
		mcapMax = 0
	}
	trackedOnly := qp.Get("tracked") == "true" || qp.Get("tracked") == "1"

	// 1. Directory rows (SQL-pushable filters; bounded by the ~10.4k-row map).
	comps, err := d.St.ListCompanies(ctx, qp.Get("q"), qp.Get("sector"), qp.Get("exchange"))
	if err != nil {
		httpInternal(w, err)
		return
	}

	// 2. Tracked-data maps — built ONCE per request (no N+1 over companies).
	syms, err := d.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		httpInternal(w, err)
		return
	}
	symByTicker := make(map[string]md.Symbol, len(syms))
	for _, s := range syms {
		symByTicker[strings.ToUpper(s.Symbol)] = s
	}
	daily, err := d.St.LatestDailyAll(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}
	shares, err := d.St.LatestMetricAll(ctx, "SharesOutstanding")
	if err != nil {
		httpInternal(w, err)
		return
	}
	floats, err := d.St.LatestMetricAll(ctx, "EntityPublicFloat")
	if err != nil {
		httpInternal(w, err)
		return
	}

	// 3. Join + Go-side filters (mcap needs bars×fundamentals context).
	unknownMcapExcluded := 0
	trackedCount := 0
	rows := make([]companyDirRow, 0, len(comps))
	for _, c := range comps {
		row := companyDirRow{
			Ticker: c.Ticker, Name: c.Name, Exchange: c.Exchange,
			CIK: c.CIK, SIC: c.SIC, SICDesc: c.SICDesc,
		}
		if s, ok := symByTicker[c.Ticker]; ok {
			row.Tracked = true
			if dc, has := daily[s.ID]; has {
				p, v := dc.Last, dc.Volume
				row.Price, row.Volume, row.BarTs = &p, &v, dc.Ts
				if dc.Prev != 0 {
					chg := pctChange(dc.Last, dc.Prev)
					row.DayChangePct = &chg
				}
				if f, hasSh := shares[s.ID]; hasSh && f.Value > 0 && dc.Last > 0 {
					sh := f.Value
					mc := sh * dc.Last
					row.SharesOutstanding, row.Mcap = &sh, &mc
				}
			}
			if f, hasFl := floats[s.ID]; hasFl && f.Value > 0 {
				fl := f.Value
				row.Float = &fl
			}
		}
		if trackedOnly && !row.Tracked {
			continue
		}
		if mcapMin > 0 || mcapMax > 0 {
			if row.Mcap == nil {
				unknownMcapExcluded++
				continue
			}
			if mcapMin > 0 && *row.Mcap < mcapMin {
				continue
			}
			if mcapMax > 0 && *row.Mcap > mcapMax {
				continue
			}
		}
		if row.Tracked {
			trackedCount++
		}
		rows = append(rows, row)
	}

	// 4. Deterministic order: known-mcap first (largest cap down), then
	// tracked-without-mcap, then untracked — ticker breaks every tie.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case (a.Mcap != nil) != (b.Mcap != nil):
			return a.Mcap != nil
		case a.Mcap != nil && b.Mcap != nil && *a.Mcap != *b.Mcap:
			return *a.Mcap > *b.Mcap
		case a.Tracked != b.Tracked:
			return a.Tracked
		default:
			return a.Ticker < b.Ticker
		}
	})

	// 5. Paginate AFTER filtering so total is the real filtered count.
	total := len(rows)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := rows[offset:end]

	// 6. Facets (filter options) + directory freshness — cheap single queries.
	sectors, _ := d.St.CompanySectors(ctx, 200)
	exchanges, _ := d.St.CompanyExchanges(ctx)
	// The only bare `_` in this file, and it produced the most reassuring
	// possible sentence from a failed read: dirCount 0 renders as "Directory not
	// synced yet - usually within seconds of daemon start", stamped asOf now.
	// A count that could not be taken is not a count of zero.
	dirCount, dirCountErr := d.St.CompanyCount(ctx)
	// nil, not 0, when the count could not be taken -- so a consumer branching
	// on "0 means not synced yet" cannot be handed a failed read instead.
	var directoryCount any = dirCount
	if dirCountErr != nil {
		directoryCount = nil
	}
	lastSync := int64(0)
	if v, _ := d.St.GetMeta(ctx, "companies_sync_ts"); v != "" {
		lastSync, _ = strconv.ParseInt(v, 10, 64)
	}

	writeJSON(w, map[string]any{
		"companies":           page,
		"total":               total,
		"limit":               limit,
		"offset":              offset,
		"trackedCount":        trackedCount,
		"unknownMcapExcluded": unknownMcapExcluded, // >0 only with a mcap filter active
		"directoryCount":      directoryCount,      // nil = unreadable; 0 = companies-sync hasn't completed a run yet
		"lastSyncTs":          lastSync,
		"sectors":             sectors,
		"exchanges":           exchanges,
		"note":                companiesNote,
		"mcapNote":            companiesMcapNote,
		"asOf":                time.Now().Unix(),
	})
}

// earningsEstRow is one estimated next-report row.
type earningsEstRow struct {
	Symbol      string `json:"symbol"`
	Name        string `json:"name"`
	LastForm    string `json:"lastForm"`    // 10-Q | 10-K (the cadence anchor)
	LastFiledTs int64  `json:"lastFiledTs"` // when it was filed (epoch)
	EstTs       int64  `json:"estTs"`       // ESTIMATED next report date (epoch)
	Estimate    bool   `json:"estimate"`    // always true — rendered as a label
	Overdue     bool   `json:"overdue"`     // estTs already passed (cadence slipped)
}

// earningsEstimates serves the filing-cadence earnings-estimate calendar.
// GET /api/earnings-est?limit=   (rows sorted soonest-estimate first)
func (d Deps) earningsEstimates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		if limit > 500 {
			limit = 500
		} else {
			limit = 100
		}
	}

	// Batched: one fleet-wide latest-periodic-filing map + one symbol list.
	periodic, err := d.St.LatestPeriodicFilingAll(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}
	syms, err := d.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		httpInternal(w, err)
		return
	}

	now := time.Now().Unix()
	rows := make([]earningsEstRow, 0, len(periodic))
	for _, s := range syms {
		p, ok := periodic[s.ID]
		if !ok || p.FiledTs <= 0 {
			continue // no periodic filing stored yet — honest absence, no row
		}
		est := NextPeriodicEstimate(p.FiledTs)
		rows = append(rows, earningsEstRow{
			Symbol: s.Symbol, Name: s.Name,
			LastForm: p.Form, LastFiledTs: p.FiledTs,
			EstTs: est, Estimate: true, Overdue: est < now,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].EstTs != rows[j].EstTs {
			return rows[i].EstTs < rows[j].EstTs
		}
		return rows[i].Symbol < rows[j].Symbol
	})
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}

	writeJSON(w, map[string]any{
		"rows":  rows,
		"count": len(rows),
		"total": total,
		"note":  earningsEstNote,
		"asOf":  now,
	})
}

// registerCompanies wires the Stage-5 directory + earnings-estimate reads.
func (d Deps) registerCompanies(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/companies", d.companies)
	mux.HandleFunc("GET /api/earnings-est", d.earningsEstimates)
}
