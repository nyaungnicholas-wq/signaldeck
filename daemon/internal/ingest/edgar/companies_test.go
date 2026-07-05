// Signal8 wave — Stage 5 tests: the company_tickers_exchange.json parser +
// fetcher (companies directory source), the submissions company-profile (SIC)
// extraction, and the EntityPublicFloat company-fact. Fixtures only; the one
// HTTP test runs against httptest with the client's URL overrides.
package edgar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func exchangeFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "company_tickers_exchange.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return b
}

func TestParseCompanyTickersExchange(t *testing.T) {
	rows, err := ParseCompanyTickersExchange(exchangeFixture(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 9 fixture rows − zero-CIK − blank-ticker = 7 stored rows.
	if len(rows) != 7 {
		t.Fatalf("rows = %d, want 7 (%+v)", len(rows), rows)
	}
	byTicker := map[string]int{}
	for i, r := range rows {
		byTicker[r.Ticker] = i
	}
	// Lowercase source ticker is uppercased; null exchange becomes ''.
	cfnb, ok := byTicker["CFNB"]
	if !ok {
		t.Fatalf("CFNB missing (lowercase source ticker not uppercased?): %+v", byTicker)
	}
	if rows[cfnb].Exchange != "" {
		t.Fatalf("null exchange stored as %q, want ''", rows[cfnb].Exchange)
	}
	if rows[cfnb].CIK != 803016 {
		t.Fatalf("CFNB cik = %d", rows[cfnb].CIK)
	}
	// Share classes: one CIK under two tickers, both kept.
	if _, ok := byTicker["BRK-A"]; !ok {
		t.Fatalf("BRK-A missing")
	}
	if _, ok := byTicker["BRK-B"]; !ok {
		t.Fatalf("BRK-B missing")
	}
	// Skips: zero CIK and blank ticker never stored.
	if _, ok := byTicker["ZCIK"]; ok {
		t.Fatalf("zero-CIK row was stored")
	}
	if nv := rows[byTicker["NVDA"]]; nv.Name != "NVIDIA CORP" || nv.Exchange != "Nasdaq" || nv.CIK != 1045810 {
		t.Fatalf("NVDA row = %+v", nv)
	}
}

func TestParseCompanyTickersExchange_FieldOrderIndependent(t *testing.T) {
	// Column order resolved from "fields", not assumed.
	body := []byte(`{"fields":["ticker","exchange","cik","name"],
		"data":[["aapl","Nasdaq",320193,"Apple Inc."]]}`)
	rows, err := ParseCompanyTickersExchange(body)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	r := rows[0]
	if r.Ticker != "AAPL" || r.CIK != 320193 || r.Name != "Apple Inc." || r.Exchange != "Nasdaq" {
		t.Fatalf("row = %+v", r)
	}
}

func TestParseCompanyTickersExchange_Malformed(t *testing.T) {
	if _, err := ParseCompanyTickersExchange([]byte(`{"fields":["cik","name"],"data":[]}`)); err == nil {
		t.Fatalf("missing required fields must error")
	}
	if _, err := ParseCompanyTickersExchange([]byte(`not json`)); err == nil {
		t.Fatalf("malformed JSON must error")
	}
	if _, err := ParseCompanyTickersExchange([]byte(`{"fields":["cik","name","ticker","exchange"],"data":[]}`)); err == nil {
		t.Fatalf("zero rows must error (a truncated file must not wipe context)")
	}
}

func TestCompanyTickersExchange_FetchesWithUAAndLimiter(t *testing.T) {
	gotUA := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write(exchangeFixture(t))
	}))
	t.Cleanup(srv.Close)

	c := New()
	c.UA = "test-suite test@example.com"
	c.ExchangeURL = srv.URL + "/files/company_tickers_exchange.json"
	c.MinInterval = time.Millisecond

	rows, err := c.CompanyTickersExchange(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(rows))
	}
	// SEC policy: every request carries the descriptive UA (shared get path).
	if gotUA != "test-suite test@example.com" {
		t.Fatalf("UA = %q", gotUA)
	}
}

// subsWithSIC is a submissions response carrying the company profile header
// (name + sic + sicDescription) alongside one boring filing.
const subsWithSIC = `{
  "cik": "320193",
  "name": "Apple Inc.",
  "sic": "3571",
  "sicDescription": "Electronic Computers",
  "filings": {"recent": {
    "accessionNumber": ["0000320193-26-000001"],
    "filingDate": ["2026-06-01"],
    "acceptanceDateTime": ["2026-06-01T12:00:00.000Z"],
    "reportDate": ["2026-03-31"],
    "form": ["10-Q"],
    "items": [""],
    "primaryDocument": ["aapl-10q.htm"],
    "primaryDocDescription": ["10-Q"]
  }}
}`

func TestSubmissionsWithProfile_SICExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(subsWithSIC))
	}))
	t.Cleanup(srv.Close)

	c := &FilingsClient{Client: &Client{UA: "t t@e.c", MinInterval: time.Millisecond}}
	c.SubmissionsBase = srv.URL

	subs, prof, err := c.SubmissionsWithProfile(context.Background(), 320193)
	if err != nil {
		t.Fatalf("submissions: %v", err)
	}
	if prof.SIC != "3571" || prof.SICDesc != "Electronic Computers" || prof.Name != "Apple Inc." {
		t.Fatalf("profile = %+v", prof)
	}
	if len(subs) != 1 || subs[0].Form != "10-Q" {
		t.Fatalf("subs = %+v", subs)
	}
}

// floatFactsJSON: a companyfacts response with EntityPublicFloat present (two
// period-ends so "latest wins" is exercised) alongside shares outstanding.
const floatFactsJSON = `{
  "cik": 320193,
  "facts": {
    "dei": {
      "EntityCommonStockSharesOutstanding": {
        "units": {"shares": [
          {"end":"2026-01-15","val":14900000000,"form":"10-Q","filed":"2026-02-02"}
        ]}
      },
      "EntityPublicFloat": {
        "units": {"USD": [
          {"end":"2025-03-28","val":2600000000000,"form":"10-K","filed":"2025-11-01"},
          {"end":"2026-03-27","val":2900000000000,"form":"10-K","filed":"2026-06-15"}
        ]}
      }
    },
    "us-gaap": {}
  }
}`

func TestCompanyFacts_EntityPublicFloat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(floatFactsJSON))
	}))
	t.Cleanup(srv.Close)

	c := &Client{UA: "t t@e.c", FactsBase: srv.URL, MinInterval: time.Millisecond}
	f, err := c.CompanyFacts(context.Background(), 320193)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if f.EntityPublicFloat == nil {
		t.Fatalf("EntityPublicFloat not extracted")
	}
	if f.EntityPublicFloat.Value != 2.9e12 {
		t.Fatalf("float = %v, want latest period-end 2.9e12", f.EntityPublicFloat.Value)
	}
	// And it lands in the stored metric rows.
	rows := factRows(1, 320193, f, 42)
	found := false
	for _, r := range rows {
		if r.Metric == "EntityPublicFloat" {
			found = true
			if r.Value != 2.9e12 {
				t.Fatalf("row value = %v", r.Value)
			}
		}
	}
	if !found {
		t.Fatalf("no EntityPublicFloat row in %+v", rows)
	}
}
