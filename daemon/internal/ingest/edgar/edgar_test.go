package edgar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// tickersJSON is a realistic company_tickers.json (object keyed by row index).
const tickersJSON = `{
  "0": {"cik_str": 320193, "ticker": "AAPL", "title": "Apple Inc."},
  "1": {"cik_str": 789019, "ticker": "MSFT", "title": "MICROSOFT CORP"}
}`

// aaplFactsJSON is a trimmed but realistic companyfacts response with revenue,
// EPS, and dei shares-outstanding, spanning two period-ends so "latest" logic
// is exercised.
const aaplFactsJSON = `{
  "cik": 320193,
  "entityName": "Apple Inc.",
  "facts": {
    "dei": {
      "EntityCommonStockSharesOutstanding": {
        "units": {
          "shares": [
            {"end":"2025-01-15","val":15000000000,"form":"10-Q","filed":"2025-01-31"},
            {"end":"2026-01-15","val":14900000000,"form":"10-Q","filed":"2026-02-02"}
          ]
        }
      }
    },
    "us-gaap": {
      "RevenueFromContractWithCustomerExcludingAssessedTax": {
        "units": {
          "USD": [
            {"end":"2025-09-30","val":383000000000,"form":"10-K","filed":"2025-11-01","fp":"FY"},
            {"end":"2026-03-31","val":90000000000,"form":"10-Q","filed":"2026-05-02","fp":"Q2"}
          ]
        }
      },
      "EarningsPerShareDiluted": {
        "units": {
          "USD/shares": [
            {"end":"2025-09-30","val":6.13,"form":"10-K","filed":"2025-11-01"},
            {"end":"2026-03-31","val":1.53,"form":"10-Q","filed":"2026-05-02"}
          ]
        }
      }
    }
  }
}`

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// edgarMux serves both EDGAR endpoints from one httptest server and records the
// timestamps of every request (to assert rate-limit spacing).
type edgarMux struct {
	srv       *httptest.Server
	mu        sync.Mutex
	reqTimes  []time.Time
	factsBody map[string]string // CIK-padded → body; empty ⇒ 404
	uaSeen    string
}

func newEdgarMux(t *testing.T) *edgarMux {
	t.Helper()
	m := &edgarMux{factsBody: map[string]string{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.reqTimes = append(m.reqTimes, time.Now())
		m.uaSeen = r.Header.Get("User-Agent")
		m.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "company_tickers.json"):
			_, _ = w.Write([]byte(tickersJSON))
		case strings.Contains(r.URL.Path, "companyfacts"):
			// path is /.../CIK##########.json
			base := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			cik := strings.TrimSuffix(base, ".json")
			if body, ok := m.factsBody[cik]; ok && body != "" {
				_, _ = w.Write([]byte(body))
				return
			}
			w.WriteHeader(404)
			_, _ = w.Write([]byte("not found"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *edgarMux) client() *Client {
	c := New()
	c.TickersURL = m.srv.URL + "/files/company_tickers.json"
	c.FactsBase = m.srv.URL + "/api/xbrl/companyfacts"
	// Small but non-zero interval so the spacing test is fast yet observable.
	c.MinInterval = 40 * time.Millisecond
	return c
}

func TestCIKMap(t *testing.T) {
	m := newEdgarMux(t)
	c := m.client()
	cm, err := c.CIKMap(context.Background())
	if err != nil {
		t.Fatalf("cikmap: %v", err)
	}
	if cm["AAPL"] != 320193 || cm["MSFT"] != 789019 {
		t.Fatalf("bad cik map: %+v", cm)
	}
}

func TestCompanyFacts_ExtractsLatest(t *testing.T) {
	m := newEdgarMux(t)
	m.factsBody[CIKPadded(320193)] = aaplFactsJSON
	c := m.client()

	f, err := c.CompanyFacts(context.Background(), 320193)
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if f.Revenues == nil || f.Revenues.Value != 90000000000 { // latest = Q2 2026-03-31
		t.Fatalf("revenue = %+v, want latest 90e9", f.Revenues)
	}
	if f.EPS == nil || f.EPS.Value != 1.53 {
		t.Fatalf("eps = %+v, want latest 1.53", f.EPS)
	}
	if f.SharesOutstanding == nil || f.SharesOutstanding.Value != 14900000000 {
		t.Fatalf("shares = %+v, want latest 14.9e9", f.SharesOutstanding)
	}
	// latest filing date = max filed across facts = 2026-05-02
	wantFiled, _ := parseDay("2026-05-02")
	if f.LatestFilingDate != wantFiled {
		t.Fatalf("latest filing = %d, want %d", f.LatestFilingDate, wantFiled)
	}
	// revenue as_of must be the period end 2026-03-31, not the filing date.
	wantAsOf, _ := parseDay("2026-03-31")
	if f.Revenues.AsOf != wantAsOf {
		t.Fatalf("revenue as_of = %d, want %d", f.Revenues.AsOf, wantAsOf)
	}
}

func TestIngest_PersistsRowsAndSpacesRequests(t *testing.T) {
	m := newEdgarMux(t)
	m.factsBody[CIKPadded(320193)] = aaplFactsJSON
	m.factsBody[CIKPadded(789019)] = aaplFactsJSON // reuse body for MSFT
	c := m.client()
	st := openTestStore(t)

	aapl, _ := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	msft, _ := st.UpsertSymbol(context.Background(), "MSFT", md.Stocks, "Microsoft")

	written, missing, err := c.Ingest(context.Background(), st, []md.Symbol{aapl, msft})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if missing != 0 {
		t.Fatalf("missing = %d, want 0", missing)
	}
	// Each symbol yields 5 rows: Revenues, EPS, SharesOutstanding, CIK, LatestFilingDate.
	if written != 10 {
		t.Fatalf("written = %d, want 10", written)
	}
	// Latest fundamentals for AAPL must be readable.
	rows, err := st.LatestFundamentals(context.Background(), aapl.ID)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	got := map[string]float64{}
	for _, r := range rows {
		got[r.Metric] = r.Value
	}
	if got["Revenues"] != 90000000000 || got["EPS"] != 1.53 {
		t.Fatalf("stored fundamentals wrong: %+v", got)
	}
	_ = msft

	// Rate-limit spacing: with 3 requests (1 tickers + 2 facts) at 40ms min
	// interval, the span between first and last must be >= 2*interval.
	m.mu.Lock()
	times := append([]time.Time(nil), m.reqTimes...)
	ua := m.uaSeen
	m.mu.Unlock()
	if len(times) < 3 {
		t.Fatalf("want >=3 requests, got %d", len(times))
	}
	// 3 requests at a 40ms min-interval ⇒ ~2 intervals of enforced spacing.
	// Assert a conservative lower bound (>=1.5 intervals) to prove pacing is on
	// without being brittle to sub-millisecond scheduler jitter in the timers.
	span := times[len(times)-1].Sub(times[0])
	if span < 60*time.Millisecond {
		t.Fatalf("requests not spaced: span=%v want >=60ms (pacing off?)", span)
	}
	// Descriptive User-Agent required by SEC policy.
	if !strings.Contains(strings.ToLower(ua), "signaldeck") {
		t.Fatalf("User-Agent not descriptive: %q", ua)
	}
}

func TestIngest_GracefulMissingSymbol(t *testing.T) {
	m := newEdgarMux(t) // no facts bodies ⇒ every companyfacts is 404
	c := m.client()
	st := openTestStore(t)
	aapl, _ := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")

	written, missing, err := c.Ingest(context.Background(), st, []md.Symbol{aapl})
	if err != nil {
		t.Fatalf("ingest should not error on 404 facts: %v", err)
	}
	if written != 0 || missing != 1 {
		t.Fatalf("want written=0 missing=1, got written=%d missing=%d", written, missing)
	}
}

func TestGet_RetriesOn429(t *testing.T) {
	var n int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		attempt := n
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(tickersJSON))
	}))
	defer srv.Close()

	c := New()
	c.TickersURL = srv.URL + "/files/company_tickers.json"
	c.MinInterval = 5 * time.Millisecond
	cm, err := c.CIKMap(context.Background())
	if err != nil {
		t.Fatalf("cikmap after retry: %v", err)
	}
	if cm["AAPL"] != 320193 {
		t.Fatalf("bad map after retry: %+v", cm)
	}
	mu.Lock()
	defer mu.Unlock()
	if n < 2 {
		t.Fatalf("expected a retry, saw %d requests", n)
	}
}

func TestCIKPadded(t *testing.T) {
	if got := CIKPadded(320193); got != "CIK0000320193" {
		t.Fatalf("CIKPadded = %q", got)
	}
	if got := CIKPadded(789019); got != "CIK0000789019" {
		t.Fatalf("CIKPadded = %q", got)
	}
}
