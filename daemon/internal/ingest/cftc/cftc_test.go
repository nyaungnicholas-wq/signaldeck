package cftc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cotFixture mirrors the live Socrata shape (verified 2026-07-10): string
// numbers, ISO-timestamp report dates.
const cotFixture = `[
  {"market_and_exchange_names":"E-MINI S&P 500 - CHICAGO MERCANTILE EXCHANGE",
   "report_date_as_yyyy_mm_dd":"2026-07-07T00:00:00.000",
   "contract_market_name":"E-MINI S&P 500",
   "noncomm_positions_long_all":"398112","noncomm_positions_short_all":"455876",
   "comm_positions_long_all":"901234","comm_positions_short_all":"850001",
   "open_interest_all":"2100345"},
  {"market_and_exchange_names":"BITCOIN - CHICAGO MERCANTILE EXCHANGE",
   "report_date_as_yyyy_mm_dd":"2026-07-07T00:00:00.000",
   "contract_market_name":"BITCOIN",
   "noncomm_positions_long_all":"20123","noncomm_positions_short_all":"22345",
   "comm_positions_long_all":"1500","comm_positions_short_all":"900",
   "open_interest_all":"31000"},
  {"market_and_exchange_names":"BROKEN ROW",
   "report_date_as_yyyy_mm_dd":"2026-07-07T00:00:00.000",
   "contract_market_name":"BROKEN",
   "noncomm_positions_long_all":"not-a-number","noncomm_positions_short_all":"1",
   "comm_positions_long_all":"1","comm_positions_short_all":"1",
   "open_interest_all":"1"}
]`

func TestFetchWindow(t *testing.T) {
	var gotWhere, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWhere, gotUA = r.URL.Query().Get("$where"), r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(cotFixture))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UA: "test-ua"}
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows, skipped, err := c.FetchWindow(context.Background(), since, until)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotUA != "test-ua" {
		t.Errorf("UA = %q", gotUA)
	}
	if !strings.Contains(gotWhere, ">= '2026-06-01'") || !strings.Contains(gotWhere, "< '2026-08-01'") {
		t.Errorf("$where missing bounds: %q", gotWhere)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (BROKEN row)", skipped)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	es := rows[0]
	if es.Contract != "E-MINI S&P 500" || es.ReportDate != "2026-07-07" ||
		es.NoncommLong != 398112 || es.NoncommShort != 455876 ||
		es.CommLong != 901234 || es.CommShort != 850001 || es.OpenInterest != 2100345 {
		t.Errorf("E-mini row parsed wrong: %+v", es)
	}
}

func TestFetchWindowOpenUpperBound(t *testing.T) {
	var gotWhere string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWhere = r.URL.Query().Get("$where")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	if _, _, err := c.FetchWindow(context.Background(),
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.Time{}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if strings.Contains(gotWhere, "<") {
		t.Errorf("zero until must mean no upper bound: %q", gotWhere)
	}
}

func TestFetchWindowNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "throttled", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	if _, _, err := c.FetchWindow(context.Background(), time.Now(), time.Time{}); err == nil {
		t.Error("non-200 must error")
	}
}
