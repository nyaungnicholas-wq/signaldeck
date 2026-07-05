// Signal8 wave — Stage 5 API tests: /api/companies (tracked-data join, honest
// nulls for untracked rows, filters, pagination) and /api/earnings-est
// (filing-cadence estimate math + labeling). Separate harness file so
// parallel edits never collide. t.TempDir store only.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newCompaniesAPIServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerCompanies(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// companiesResp mirrors the /api/companies payload for assertions.
type companiesResp struct {
	Companies []struct {
		Ticker            string   `json:"ticker"`
		Name              string   `json:"name"`
		Exchange          string   `json:"exchange"`
		CIK               int64    `json:"cik"`
		SICDesc           string   `json:"sicDesc"`
		Tracked           bool     `json:"tracked"`
		Price             *float64 `json:"price"`
		DayChangePct      *float64 `json:"dayChangePct"`
		Volume            *float64 `json:"volume"`
		Mcap              *float64 `json:"mcap"`
		SharesOutstanding *float64 `json:"sharesOutstanding"`
		Float             *float64 `json:"float"`
	} `json:"companies"`
	Total               int    `json:"total"`
	Limit               int    `json:"limit"`
	Offset              int    `json:"offset"`
	TrackedCount        int    `json:"trackedCount"`
	UnknownMcapExcluded int    `json:"unknownMcapExcluded"`
	DirectoryCount      int    `json:"directoryCount"`
	Note                string `json:"note"`
	McapNote            string `json:"mcapNote"`
	Sectors             []struct {
		Value string `json:"value"`
		N     int    `json:"n"`
	} `json:"sectors"`
	Exchanges []struct {
		Value string `json:"value"`
		N     int    `json:"n"`
	} `json:"exchanges"`
}

// seedCompaniesWorld: 4 directory rows; AAPL tracked with bars + shares +
// float, NVDA tracked with bars but NO EDGAR coverage, MSFT + CFNB untracked.
func seedCompaniesWorld(t *testing.T, st *store.Store) (aaplID int64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Unix()

	if err := st.UpsertCompanies(ctx, []store.CompanyRow{
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc.", Exchange: "Nasdaq", SIC: "3571", SICDesc: "Electronic Computers", UpdatedTs: now},
		{CIK: 1045810, Ticker: "NVDA", Name: "NVIDIA CORP", Exchange: "Nasdaq", SIC: "3674", SICDesc: "Semiconductors", UpdatedTs: now},
		{CIK: 789019, Ticker: "MSFT", Name: "MICROSOFT CORP", Exchange: "Nasdaq", UpdatedTs: now},
		{CIK: 803016, Ticker: "CFNB", Name: "CALIFORNIA FIRST LEASING CORP", Exchange: "", UpdatedTs: now},
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}

	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA CORP")
	seedDaily(t, st, aapl.ID, 100, 110, now) // +10%, close 110
	seedDaily(t, st, nvda.ID, 50, 45, now)   // -10%, close 45

	// EDGAR facts for AAPL only: 1e9 shares ⇒ mcap 110e9; float 90e9.
	if err := st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: aapl.ID, Metric: "SharesOutstanding", Value: 1e9, AsOf: now - 86400, FetchedAt: now,
	}); err != nil {
		t.Fatalf("shares: %v", err)
	}
	if err := st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: aapl.ID, Metric: "EntityPublicFloat", Value: 9e10, AsOf: now - 86400, FetchedAt: now,
	}); err != nil {
		t.Fatalf("float: %v", err)
	}
	return aapl.ID
}

func TestCompaniesEndpoint_TrackedJoinAndHonestNulls(t *testing.T) {
	srv, st := newCompaniesAPIServer(t)
	seedCompaniesWorld(t, st)

	var body companiesResp
	if code := s8Get(t, srv.URL+"/api/companies", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Total != 4 || len(body.Companies) != 4 || body.DirectoryCount != 4 {
		t.Fatalf("total=%d rows=%d dir=%d", body.Total, len(body.Companies), body.DirectoryCount)
	}
	if body.TrackedCount != 2 {
		t.Fatalf("trackedCount = %d", body.TrackedCount)
	}
	byT := map[string]int{}
	for i, c := range body.Companies {
		byT[c.Ticker] = i
	}
	// AAPL: fully joined (price/chg/volume/mcap/shares/float).
	a := body.Companies[byT["AAPL"]]
	if !a.Tracked || a.Price == nil || *a.Price != 110 {
		t.Fatalf("AAPL = %+v", a)
	}
	if a.DayChangePct == nil || *a.DayChangePct < 9.9 || *a.DayChangePct > 10.1 {
		t.Fatalf("AAPL chg = %+v", a.DayChangePct)
	}
	if a.Mcap == nil || *a.Mcap != 110e9 || a.Float == nil || *a.Float != 9e10 {
		t.Fatalf("AAPL mcap/float = %+v/%+v", a.Mcap, a.Float)
	}
	if a.SICDesc != "Electronic Computers" {
		t.Fatalf("AAPL sector = %q", a.SICDesc)
	}
	// NVDA: tracked, priced, but NO EDGAR coverage ⇒ mcap/shares/float null.
	n := body.Companies[byT["NVDA"]]
	if !n.Tracked || n.Price == nil || *n.Price != 45 || n.Mcap != nil || n.Float != nil {
		t.Fatalf("NVDA = %+v", n)
	}
	// MSFT: untracked ⇒ EVERY market column null, never fabricated.
	m := body.Companies[byT["MSFT"]]
	if m.Tracked || m.Price != nil || m.DayChangePct != nil || m.Volume != nil || m.Mcap != nil {
		t.Fatalf("untracked MSFT carries market data: %+v", m)
	}
	// CFNB: '' exchange survives to the payload (render "—", never a guess).
	if body.Companies[byT["CFNB"]].Exchange != "" {
		t.Fatalf("CFNB exchange = %q", body.Companies[byT["CFNB"]].Exchange)
	}
	// Order: known-mcap first ⇒ AAPL leads.
	if body.Companies[0].Ticker != "AAPL" {
		t.Fatalf("order[0] = %s", body.Companies[0].Ticker)
	}
	// Facets present.
	if len(body.Sectors) != 2 || len(body.Exchanges) != 1 {
		t.Fatalf("facets: sectors=%+v exchanges=%+v", body.Sectors, body.Exchanges)
	}
	if body.Note == "" || body.McapNote == "" {
		t.Fatalf("honesty notes missing")
	}
}

func TestCompaniesEndpoint_Filters(t *testing.T) {
	srv, st := newCompaniesAPIServer(t)
	seedCompaniesWorld(t, st)

	var body companiesResp
	// q by name substring.
	s8Get(t, srv.URL+"/api/companies?q=micro", &body)
	if body.Total != 1 || body.Companies[0].Ticker != "MSFT" {
		t.Fatalf("q=micro: %+v", body.Companies)
	}
	// sector filter.
	s8Get(t, srv.URL+"/api/companies?sector=Semiconductors", &body)
	if body.Total != 1 || body.Companies[0].Ticker != "NVDA" {
		t.Fatalf("sector: %+v", body.Companies)
	}
	// exchange filter.
	s8Get(t, srv.URL+"/api/companies?exchange=Nasdaq", &body)
	if body.Total != 3 {
		t.Fatalf("exchange: total=%d", body.Total)
	}
	// tracked-only.
	s8Get(t, srv.URL+"/api/companies?tracked=true", &body)
	if body.Total != 2 {
		t.Fatalf("tracked: total=%d", body.Total)
	}
	for _, c := range body.Companies {
		if !c.Tracked {
			t.Fatalf("untracked row leaked: %+v", c)
		}
	}
	// mcap filter: only AAPL (110e9) has a known mcap; the 3 unknowns are
	// excluded AND counted.
	s8Get(t, srv.URL+"/api/companies?mcapMin=100000000000", &body)
	if body.Total != 1 || body.Companies[0].Ticker != "AAPL" || body.UnknownMcapExcluded != 3 {
		t.Fatalf("mcapMin: total=%d unknownExcluded=%d", body.Total, body.UnknownMcapExcluded)
	}
	// mcapMax below AAPL ⇒ zero rows (never a fabricated fit).
	s8Get(t, srv.URL+"/api/companies?mcapMin=1&mcapMax=1000", &body)
	if body.Total != 0 {
		t.Fatalf("mcap band: total=%d", body.Total)
	}
}

func TestCompaniesEndpoint_Pagination(t *testing.T) {
	srv, st := newCompaniesAPIServer(t)
	seedCompaniesWorld(t, st)

	var body companiesResp
	s8Get(t, srv.URL+"/api/companies?limit=2&offset=0", &body)
	if body.Total != 4 || len(body.Companies) != 2 || body.Limit != 2 || body.Offset != 0 {
		t.Fatalf("page1: total=%d rows=%d", body.Total, len(body.Companies))
	}
	first, second := body.Companies[0].Ticker, body.Companies[1].Ticker

	s8Get(t, srv.URL+"/api/companies?limit=2&offset=2", &body)
	if body.Total != 4 || len(body.Companies) != 2 {
		t.Fatalf("page2: total=%d rows=%d", body.Total, len(body.Companies))
	}
	for _, c := range body.Companies {
		if c.Ticker == first || c.Ticker == second {
			t.Fatalf("page overlap: %s", c.Ticker)
		}
	}
	// Offset past the end: empty page, sane total.
	s8Get(t, srv.URL+"/api/companies?limit=2&offset=99", &body)
	if body.Total != 4 || len(body.Companies) != 0 {
		t.Fatalf("past end: total=%d rows=%d", body.Total, len(body.Companies))
	}
	// Limit is capped at 200.
	s8Get(t, srv.URL+"/api/companies?limit=9999", &body)
	if body.Limit != 200 {
		t.Fatalf("limit cap = %d", body.Limit)
	}
}

func TestNextPeriodicEstimate(t *testing.T) {
	// The pure rule: last periodic filing + exactly 91 days.
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC).Unix()
	if got := NextPeriodicEstimate(base); got != base+91*86400 {
		t.Fatalf("estimate = %d, want %d", got, base+91*86400)
	}
}

type earningsEstResp struct {
	Rows []struct {
		Symbol      string `json:"symbol"`
		LastForm    string `json:"lastForm"`
		LastFiledTs int64  `json:"lastFiledTs"`
		EstTs       int64  `json:"estTs"`
		Estimate    bool   `json:"estimate"`
		Overdue     bool   `json:"overdue"`
	} `json:"rows"`
	Count int    `json:"count"`
	Total int    `json:"total"`
	Note  string `json:"note"`
}

func TestEarningsEstEndpoint(t *testing.T) {
	srv, st := newCompaniesAPIServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA CORP")
	msft, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "MICROSOFT")

	ins := func(id, form string, symID, ts int64) {
		t.Helper()
		if _, err := st.InsertFiling(ctx, store.FilingRow{ID: id, SymbolID: symID, Form: form, FiledTs: ts}); err != nil {
			t.Fatalf("filing: %v", err)
		}
	}
	// AAPL filed a 10-Q 30d ago ⇒ est in ~61d, not overdue.
	ins("a-1", "10-Q", aapl.ID, now-30*86400)
	// NVDA filed a 10-K 100d ago ⇒ est 9d in the past ⇒ OVERDUE, sorts first.
	ins("n-1", "10-K", nvda.ID, now-100*86400)
	// MSFT has only an 8-K ⇒ no cadence anchor ⇒ NO row (honest absence).
	ins("m-1", "8-K", msft.ID, now-10*86400)

	var body earningsEstResp
	if code := s8Get(t, srv.URL+"/api/earnings-est", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Count != 2 || body.Total != 2 {
		t.Fatalf("count=%d total=%d rows=%+v", body.Count, body.Total, body.Rows)
	}
	// Soonest estimate first: NVDA (overdue) before AAPL.
	if body.Rows[0].Symbol != "NVDA" || !body.Rows[0].Overdue {
		t.Fatalf("row0 = %+v", body.Rows[0])
	}
	if body.Rows[1].Symbol != "AAPL" || body.Rows[1].Overdue {
		t.Fatalf("row1 = %+v", body.Rows[1])
	}
	for _, r := range body.Rows {
		if !r.Estimate {
			t.Fatalf("row not labeled estimate: %+v", r)
		}
		if r.EstTs != NextPeriodicEstimate(r.LastFiledTs) {
			t.Fatalf("est math off: %+v", r)
		}
	}
	// The honesty label is IN the payload, verbatim-renderable.
	if !strings.Contains(strings.ToLower(body.Note), "not a confirmed date") {
		t.Fatalf("note = %q", body.Note)
	}
	// Limit respected.
	s8Get(t, srv.URL+"/api/earnings-est?limit=1", &body)
	if body.Count != 1 || body.Total != 2 {
		t.Fatalf("limited: count=%d total=%d", body.Count, body.Total)
	}
}
