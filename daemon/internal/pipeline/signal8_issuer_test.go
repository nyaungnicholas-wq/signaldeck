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
)

// The swept company (CIK 999999) lists a Form 4 it filed as the REPORTING OWNER
// of Apple (the fixture's issuerCik is 320193). That is not HOLD's insider
// trade and must not be stored as one.
func TestFilingsPoller_Form4OfAnotherIssuerIsNotAnInsiderTrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "company_tickers.json"):
			_, _ = w.Write([]byte(`{"0": {"cik_str": 999999, "ticker": "HOLD", "title": "Holder Corp"}}`))
		case strings.Contains(r.URL.Path, "/submissions/CIK0000999999"):
			_, _ = w.Write([]byte(s8Submissions))
		case strings.HasSuffix(r.URL.Path, "wk-form4_1.xml"):
			_, _ = w.Write(edgarFixture(t, "form4_sale.xml"))
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
	hold, _ := st.UpsertSymbol(ctx, "HOLD", md.Stocks, "Holder Corp")
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	w := &FilingsPoller{St: st, Client: c, Now: func() time.Time { return now }}
	if detail, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}
	trades, err := st.InsiderTrades(ctx, hold.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 {
		t.Fatalf("a Form 4 whose issuer is another company was stored as HOLD's insider trade: %+v", trades)
	}
	// The accession is settled (sentinel row), not left to be re-fetched forever.
	if back, err := st.UnparsedForm4s(ctx, 10); err != nil || len(back) != 0 {
		t.Fatalf("backlog = %+v err=%v, want empty", back, err)
	}
}

// A 13F option line is not shares held and must not be stored as a holding.
func TestThirteenFPoller_SkipsOptionLines(t *testing.T) {
	const table = `<informationTable>
  <infoTable><nameOfIssuer>APPLE INC</nameOfIssuer><titleOfClass>COM</titleOfClass><cusip>037833100</cusip><value>100</value>
    <shrsOrPrnAmt><sshPrnamt>10</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt></infoTable>
  <infoTable><nameOfIssuer>TESLA INC</nameOfIssuer><titleOfClass>COM</titleOfClass><cusip>88160R101</cusip><value>900</value>
    <shrsOrPrnAmt><sshPrnamt>90</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt><putCall>Put</putCall></infoTable>
</informationTable>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/submissions/CIK0001067983"):
			_, _ = w.Write([]byte(mgrSubmissions))
		case strings.HasSuffix(r.URL.Path, "/000106798326000012/index.json"):
			_, _ = w.Write(edgarFixture(t, "13f_index.json"))
		case strings.HasSuffix(r.URL.Path, "form13fInfoTable.xml"):
			_, _ = w.Write([]byte(table))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := edgar.NewFilings()
	c.SubmissionsBase = srv.URL + "/submissions"
	c.ArchivesBase = srv.URL + "/Archives/edgar/data"
	c.MinInterval = time.Millisecond

	st := openS8Store(t)
	ctx := context.Background()
	w := &ThirteenFPoller{St: st, Client: c,
		Managers: []edgar.Manager{{CIK: 1067983, Name: "Berkshire Hathaway"}}}
	if detail, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}
	got, err := st.InstHoldingsByManager(ctx, "1067983", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CUSIP != "037833100" {
		t.Fatalf("holdings = %+v, want only the APPLE share line (the TESLA PUT is not a holding)", got)
	}
}
