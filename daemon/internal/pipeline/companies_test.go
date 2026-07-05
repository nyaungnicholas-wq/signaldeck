// Signal8 wave — Stage 5 tests: the companies-sync worker (one-request
// directory refresh, idempotent re-runs, nil-client no-op) and the
// filings-poller's SIC enrichment (rides the existing submissions fetch —
// including the minimal-fallback-row path when the directory has no row for
// the CIK yet). t.TempDir stores + httptest fixtures only.
package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newCompaniesServer(t *testing.T) *edgar.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "company_tickers_exchange.json") {
			_, _ = w.Write(edgarFixture(t, "company_tickers_exchange.json"))
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	c := edgar.New()
	c.UA = "test t@e.c"
	c.ExchangeURL = srv.URL + "/files/company_tickers_exchange.json"
	c.MinInterval = time.Millisecond
	return c
}

func TestCompaniesSync_EndToEnd(t *testing.T) {
	st := openS8Store(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	w := &CompaniesSync{St: st, Client: newCompaniesServer(t), Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}
	// The fixture parses to 7 rows (see edgar's parser test).
	n, err := st.CompanyCount(ctx)
	if err != nil || n != 7 {
		t.Fatalf("count = %d err=%v (detail=%s)", n, err, detail)
	}
	// Freshness cursor set for the API's lastSyncTs.
	if v, _ := st.GetMeta(ctx, "companies_sync_ts"); v == "" {
		t.Fatalf("companies_sync_ts not recorded")
	}

	// Second run: idempotent upsert, same count, no dupes.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if n, _ := st.CompanyCount(ctx); n != 7 {
		t.Fatalf("re-run count = %d", n)
	}

	// Null-exchange registrant stored honestly with ''.
	rows, _ := st.ListCompanies(ctx, "CFNB", "", "")
	if len(rows) != 1 || rows[0].Exchange != "" {
		t.Fatalf("CFNB = %+v", rows)
	}
}

func TestCompaniesSync_NilClientNoop(t *testing.T) {
	st := openS8Store(t)
	w := &CompaniesSync{St: st}
	detail, err := w.Run(context.Background())
	if err != nil || !strings.Contains(detail, "skipped") {
		t.Fatalf("nil client: detail=%q err=%v", detail, err)
	}
}

// TestFilingsPoller_SICEnrichment: the poller sweeps AAPL's submissions (the
// SIC-bearing fixture below) and must store the classification —
//  1. onto an existing directory row (UpdateCompanySICByCIK path), and
//  2. as a minimal fallback row when the directory doesn't know the CIK yet.
func TestFilingsPoller_SICEnrichment(t *testing.T) {
	// Serve tickers + a submissions doc carrying sic/sicDescription. No Form 4
	// fetches (the one filing is a 10-Q), so no archives routes needed.
	subs := `{
	  "cik": "320193",
	  "name": "Apple Inc.",
	  "sic": "3571",
	  "sicDescription": "Electronic Computers",
	  "filings": {"recent": {
	    "accessionNumber": ["0000320193-26-000101"],
	    "filingDate": ["2026-06-01"],
	    "acceptanceDateTime": ["2026-06-01T12:00:00.000Z"],
	    "reportDate": ["2026-03-31"],
	    "form": ["10-Q"],
	    "items": [""],
	    "primaryDocument": ["aapl-10q.htm"],
	    "primaryDocDescription": ["10-Q"]
	  }}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "company_tickers.json"):
			_, _ = w.Write([]byte(s8Tickers))
		case strings.Contains(r.URL.Path, "/submissions/CIK0000320193"):
			_, _ = w.Write([]byte(subs))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	client := edgar.NewFilings()
	client.TickersURL = srv.URL + "/files/company_tickers.json"
	client.SubmissionsBase = srv.URL + "/submissions"
	client.MinInterval = time.Millisecond

	st := openS8Store(t)
	ctx := context.Background()
	_, _ = st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	// Case 2 first: EMPTY directory — the poller must insert a fallback row.
	w := &FilingsPoller{St: st, Client: client, Now: func() time.Time { return now }}
	if detail, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}
	rows, err := st.ListCompanies(ctx, "AAPL", "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("fallback row: %+v err=%v", rows, err)
	}
	r := rows[0]
	if r.SIC != "3571" || r.SICDesc != "Electronic Computers" || r.CIK != 320193 {
		t.Fatalf("fallback row = %+v", r)
	}
	if r.Name != "Apple Inc." {
		t.Fatalf("fallback name = %q (want the EDGAR profile name)", r.Name)
	}

	// Case 1: pre-seeded directory row (fresh store) — updated in place.
	st2 := openS8Store(t)
	_, _ = st2.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	if err := st2.UpsertCompanies(ctx, []store.CompanyRow{
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc.", Exchange: "Nasdaq", UpdatedTs: 1},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w2 := &FilingsPoller{St: st2, Client: client, Now: func() time.Time { return now }}
	if detail, err := w2.Run(ctx); err != nil {
		t.Fatalf("run2: %v (%s)", err, detail)
	}
	rows, _ = st2.ListCompanies(ctx, "AAPL", "", "")
	if len(rows) != 1 || rows[0].SIC != "3571" || rows[0].Exchange != "Nasdaq" {
		t.Fatalf("in-place update = %+v (exchange must survive)", rows)
	}
}
