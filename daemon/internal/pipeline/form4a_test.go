package pipeline

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A Form 4/A that restates a stored Form 4 replaces it: the trade is counted
// once, at the amended size (audit 11-10: 4/As were never parsed).
func TestFilingsPoller_Form4AmendmentReplacesOriginal(t *testing.T) {
	orig := edgarFixture(t, "form4_sale.xml")
	amended := bytes.Replace(orig, []byte("<documentType>4</documentType>"),
		[]byte("<documentType>4/A</documentType><dateOfOriginalSubmission>2026-07-01</dateOfOriginalSubmission>"), 1)
	amended = bytes.Replace(amended, []byte("<value>50000</value>"), []byte("<value>60000</value>"), 1)
	subs := `{"cik": "320193", "filings": {"recent": {
    "accessionNumber": ["0000320193-26-000009", "0000320193-26-000005"],
    "filingDate": ["2026-07-08", "2026-07-01"],
    "acceptanceDateTime": ["2026-07-08T18:00:00.000Z", "2026-07-01T18:04:25.000Z"],
    "reportDate": ["2026-06-30", "2026-06-30"],
    "form": ["4/A", "4"],
    "items": ["", ""],
    "primaryDocument": ["xslF345X05/wk-form4a.xml", "xslF345X05/wk-form4_1.xml"],
    "primaryDocDescription": ["FORM 4/A", "FORM 4"]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "company_tickers.json"):
			_, _ = w.Write([]byte(s8Tickers))
		case strings.Contains(r.URL.Path, "/submissions/CIK0000320193"):
			_, _ = w.Write([]byte(subs))
		case strings.HasSuffix(r.URL.Path, "wk-form4_1.xml"):
			_, _ = w.Write(orig)
		case strings.HasSuffix(r.URL.Path, "wk-form4a.xml"):
			_, _ = w.Write(amended)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := edgar.NewFilings()
	c.TickersURL = srv.URL + "/files/company_tickers.json"
	c.SubmissionsBase = srv.URL + "/submissions"
	c.ArchivesBase = srv.URL + "/Archives/edgar/data"
	c.MinInterval = time.Millisecond

	st := openS8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	w := &FilingsPoller{St: st, Client: c, Now: func() time.Time { return now }}
	if detail, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}
	trades, err := st.InsiderTrades(ctx, aapl.ID, "", 10)
	if err != nil || len(trades) != 1 {
		t.Fatalf("trades = %+v err=%v, want only the 4/A", trades, err)
	}
	if trades[0].Accession != "0000320193-26-000009" || trades[0].Shares != 85000 {
		t.Fatalf("trade = %+v, want the 4/A at 85000 shares", trades[0])
	}
}
