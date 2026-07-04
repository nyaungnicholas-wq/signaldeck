package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// edgarFixture loads a fixture from the edgar package's testdata (single
// source of truth for real-shaped SEC XML/JSON snippets).
func edgarFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "ingest", "edgar", "testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func openS8Store(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s8p.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// s8Tickers maps AAPL to CIK 320193 (company_tickers.json shape).
const s8Tickers = `{"0": {"cik_str": 320193, "ticker": "AAPL", "title": "Apple Inc."}}`

// s8Submissions: one Form 4 + one 424B5 + one boring CERT (filtered out).
const s8Submissions = `{
  "cik": "320193",
  "filings": {"recent": {
    "accessionNumber": ["0000320193-26-000005", "0000320193-26-000004", "0000320193-26-000003"],
    "filingDate": ["2026-07-01", "2026-06-20", "2026-06-10"],
    "acceptanceDateTime": ["2026-07-01T18:04:25.000Z", "2026-06-20T12:00:00.000Z", "2026-06-10T09:00:00.000Z"],
    "reportDate": ["2026-06-30", "", ""],
    "form": ["4", "424B5", "CERT"],
    "items": ["", "", ""],
    "primaryDocument": ["xslF345X05/wk-form4_1.xml", "prospectus.htm", "cert.pdf"],
    "primaryDocDescription": ["FORM 4", "424B5", "CERT"]
  }}
}`

// newFilingsServer serves the tickers map, the submissions JSON, and the raw
// Form 4 XML fixture from one httptest server.
func newFilingsServer(t *testing.T) (*httptest.Server, *edgar.FilingsClient) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "company_tickers.json"):
			_, _ = w.Write([]byte(s8Tickers))
		case strings.Contains(r.URL.Path, "/submissions/CIK0000320193"):
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
	return srv, c
}

func TestFilingsPoller_EndToEnd(t *testing.T) {
	_, client := newFilingsServer(t)
	st := openS8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")

	// Seed a shares-outstanding history with >2% growth so dilution derivation
	// has evidence B alongside the fresh 424B5 (evidence A) → high.
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: aapl.ID, Metric: "SharesOutstanding", Value: 15.0e9,
		AsOf: now.Add(-300 * 24 * time.Hour).Unix(), FetchedAt: now.Unix()})
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: aapl.ID, Metric: "SharesOutstanding", Value: 15.6e9, // +4%
		AsOf: now.Add(-10 * 24 * time.Hour).Unix(), FetchedAt: now.Unix()})

	w := &FilingsPoller{St: st, Client: client, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}

	// Filings feed: the CERT must be filtered; 4 + 424B5 stored with labels.
	filings, err := st.Filings(ctx, aapl.ID, "", 10)
	if err != nil || len(filings) != 2 {
		t.Fatalf("filings = %+v err=%v", filings, err)
	}
	byForm := map[string]store.FilingRow{}
	for _, f := range filings {
		byForm[f.Form] = f
	}
	if f, ok := byForm["4"]; !ok || f.Label != "Form 4 — insider transaction" {
		t.Errorf("form 4 row = %+v", f)
	}
	if f, ok := byForm["424B5"]; !ok || !strings.Contains(f.Label, "dilution") {
		t.Errorf("424B5 row = %+v", f)
	}
	if !strings.Contains(byForm["4"].URL, "www.sec.gov/Archives/edgar/data/320193/") {
		t.Errorf("public URL wrong: %q", byForm["4"].URL)
	}

	// Insider trade parsed from the new Form 4 (aggregated S row).
	trades, err := st.InsiderTrades(ctx, aapl.ID, "", 10)
	if err != nil || len(trades) != 1 {
		t.Fatalf("trades = %+v err=%v", trades, err)
	}
	tr := trades[0]
	if tr.Insider != "COOK TIMOTHY D" || tr.Code != "S" || tr.Shares != 75000 {
		t.Errorf("trade = %+v", tr)
	}

	// Dilution: fresh 424B5 + shares +4% ⇒ high with two reasons.
	flag, ok, err := st.DilutionFlag(ctx, aapl.ID)
	if err != nil || !ok || flag.Level != "high" {
		t.Fatalf("dilution = %+v ok=%v err=%v", flag, ok, err)
	}
	var reasons []string
	_ = json.Unmarshal([]byte(flag.Reasons), &reasons)
	if len(reasons) != 2 {
		t.Errorf("reasons = %+v", reasons)
	}

	// Idempotency: a second run inserts nothing new and double-parses nothing.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	filings2, _ := st.Filings(ctx, aapl.ID, "", 10)
	trades2, _ := st.InsiderTrades(ctx, aapl.ID, "", 10)
	if len(filings2) != 2 || len(trades2) != 1 {
		t.Errorf("second run not idempotent: filings=%d trades=%d", len(filings2), len(trades2))
	}
}

func TestFilingsPoller_NoClientNoStocks(t *testing.T) {
	st := openS8Store(t)
	w := &FilingsPoller{St: st} // nil client
	if detail, err := w.Run(context.Background()); err != nil || !strings.Contains(detail, "skipped") {
		t.Fatalf("nil client: %q err=%v", detail, err)
	}
	_, client := newFilingsServer(t)
	w2 := &FilingsPoller{St: st, Client: client}
	if detail, err := w2.Run(context.Background()); err != nil || !strings.Contains(detail, "no stocks") {
		t.Fatalf("no stocks: %q err=%v", detail, err)
	}
}

func TestDeriveDilution_Levels(t *testing.T) {
	now := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC).Unix()
	day := int64(86400)
	filing := store.FilingRow{ID: "a", Form: "S-3", FiledTs: now - 30*day}
	flatShares := []store.FundamentalRow{
		{Metric: "SharesOutstanding", Value: 100e6, AsOf: now - 300*day},
		{Metric: "SharesOutstanding", Value: 101e6, AsOf: now - 10*day}, // +1% — under threshold
	}
	grownShares := []store.FundamentalRow{
		{Metric: "SharesOutstanding", Value: 100e6, AsOf: now - 300*day},
		{Metric: "SharesOutstanding", Value: 104e6, AsOf: now - 10*day}, // +4%
	}

	if lvl, reasons := DeriveDilution(nil, flatShares, now); lvl != "low" || len(reasons) != 0 {
		t.Errorf("neither: %s %v", lvl, reasons)
	}
	if lvl, reasons := DeriveDilution([]store.FilingRow{filing}, flatShares, now); lvl != "elevated" || len(reasons) != 1 {
		t.Errorf("filing only: %s %v", lvl, reasons)
	}
	if lvl, reasons := DeriveDilution(nil, grownShares, now); lvl != "elevated" || len(reasons) != 1 {
		t.Errorf("shares only: %s %v", lvl, reasons)
	}
	if lvl, reasons := DeriveDilution([]store.FilingRow{filing}, grownShares, now); lvl != "high" || len(reasons) != 2 {
		t.Errorf("both: %s %v", lvl, reasons)
	}
	// A single observation proves nothing (no growth evidence).
	one := []store.FundamentalRow{{Metric: "SharesOutstanding", Value: 104e6, AsOf: now - day}}
	if lvl, _ := DeriveDilution(nil, one, now); lvl != "low" {
		t.Errorf("single obs: %s", lvl)
	}
	// Stale observations outside the 400d window are ignored.
	stale := []store.FundamentalRow{
		{Metric: "SharesOutstanding", Value: 100e6, AsOf: now - 500*day},
		{Metric: "SharesOutstanding", Value: 104e6, AsOf: now - 450*day},
	}
	if lvl, _ := DeriveDilution(nil, stale, now); lvl != "low" {
		t.Errorf("stale obs: %s", lvl)
	}
}

// ── 13F poller ────────────────────────────────────────────────────────────

// mgrSubmissions returns a 13F-HR submissions payload for one manager.
const mgrSubmissions = `{
  "cik": "1067983",
  "filings": {"recent": {
    "accessionNumber": ["0001067983-26-000012", "0001067983-25-000010"],
    "filingDate": ["2026-05-14", "2026-02-14"],
    "acceptanceDateTime": ["2026-05-14T16:00:00.000Z", "2026-02-14T16:00:00.000Z"],
    "reportDate": ["2026-03-31", "2025-12-31"],
    "form": ["13F-HR", "13F-HR"],
    "items": ["", ""],
    "primaryDocument": ["primary_doc.xml", "primary_doc.xml"],
    "primaryDocDescription": ["13F-HR", "13F-HR"]
  }}
}`

func new13FServer(t *testing.T) *edgar.FilingsClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/submissions/CIK0001067983"):
			_, _ = w.Write([]byte(mgrSubmissions))
		case strings.HasSuffix(r.URL.Path, "/000106798326000012/index.json"):
			_, _ = w.Write(edgarFixture(t, "13f_index.json"))
		case strings.HasSuffix(r.URL.Path, "form13fInfoTable.xml"):
			_, _ = w.Write(edgarFixture(t, "13f_infotable.xml"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := edgar.NewFilings()
	c.SubmissionsBase = srv.URL + "/submissions"
	c.ArchivesBase = srv.URL + "/Archives/edgar/data"
	c.MinInterval = time.Millisecond
	return c
}

func TestThirteenFPoller_EndToEnd(t *testing.T) {
	client := new13FServer(t)
	st := openS8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")

	w := &ThirteenFPoller{St: st, Client: client,
		Managers: []edgar.Manager{{CIK: 1067983, Name: "Berkshire Hathaway"}}}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}

	// Both fixture positions stored for the LATEST period; APPLE INC matched
	// to AAPL by name, OBSCURE WIDGETS honest-NULL.
	byMgr, err := st.InstHoldingsByManager(ctx, "1067983", 10)
	if err != nil || len(byMgr) != 2 {
		t.Fatalf("holdings = %+v err=%v", byMgr, err)
	}
	if byMgr[0].Period != "2026-03-31" {
		t.Errorf("period = %q", byMgr[0].Period)
	}
	if byMgr[0].SymbolID == nil || *byMgr[0].SymbolID != aapl.ID {
		t.Errorf("APPLE INC not matched: %+v", byMgr[0])
	}
	if byMgr[1].SymbolID != nil {
		t.Errorf("unmatched issuer must stay NULL: %+v", byMgr[1])
	}

	// Second run: period already stored ⇒ skip (steady state ~zero work).
	detail2, err := w.Run(ctx)
	if err != nil || !strings.Contains(detail2, "0 positions stored") {
		t.Fatalf("second run: %q err=%v", detail2, err)
	}
}

func TestMatchSymbolByName(t *testing.T) {
	syms := []md.Symbol{
		{ID: 1, Symbol: "AAPL", Name: "Apple Inc."},
		{ID: 2, Symbol: "MSFT", Name: "MICROSOFT CORP"},
		{ID: 3, Symbol: "BRK.B", Name: "Berkshire Hathaway Inc. Class B"},
		{ID: 4, Symbol: "NONAME", Name: ""},
	}
	cases := []struct {
		issuer string
		want   int64
		ok     bool
	}{
		{"APPLE INC", 1, true},
		{"APPLE INC COM", 1, true}, // trailing 13F noise stripped
		{"Microsoft Corporation", 2, true},
		{"BERKSHIRE HATHAWAY INC CL B", 3, true},
		{"TOTALLY DIFFERENT CO", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := MatchSymbolByName(c.issuer, syms)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("MatchSymbolByName(%q) = (%d,%v), want (%d,%v)", c.issuer, got, ok, c.want, c.ok)
		}
	}
}

func TestRotateWindow_CursorRotation(t *testing.T) {
	st := openS8Store(t)
	ctx := context.Background()
	var syms []md.Symbol
	for _, s := range []string{"AAA", "BBB", "CCC", "DDD", "EEE"} {
		sym, _ := st.UpsertSymbol(ctx, s, md.Stocks, s)
		syms = append(syms, sym)
	}
	const key = "test_rotate_cursor"
	w1 := rotateWindow(ctx, st, syms, 2, key)
	if len(w1) != 2 || w1[0].Symbol != "AAA" || w1[1].Symbol != "BBB" {
		t.Fatalf("w1 = %+v", w1)
	}
	w2 := rotateWindow(ctx, st, syms, 2, key)
	if len(w2) != 2 || w2[0].Symbol != "CCC" || w2[1].Symbol != "DDD" {
		t.Fatalf("w2 = %+v", w2)
	}
	w3 := rotateWindow(ctx, st, syms, 2, key)
	if len(w3) != 2 || w3[0].Symbol != "EEE" || w3[1].Symbol != "AAA" {
		t.Fatalf("w3 (wrap) = %+v", w3)
	}
}

func TestParsePublicFilingURL(t *testing.T) {
	cik, doc, ok := parsePublicFilingURL(
		"https://www.sec.gov/Archives/edgar/data/320193/000032019326000005/xslF345X05/wk-form4_1.xml")
	if !ok || cik != 320193 || doc != "xslF345X05/wk-form4_1.xml" {
		t.Fatalf("parse = (%d,%q,%v)", cik, doc, ok)
	}
	if _, _, ok := parsePublicFilingURL("https://example.com/nope"); ok {
		t.Error("bad URL must not parse")
	}
	if _, _, ok := parsePublicFilingURL("https://www.sec.gov/Archives/edgar/data/xyz/acc/doc.xml"); ok {
		t.Error("non-numeric cik must not parse")
	}
}

func TestFilingsPoller_BacklogDrain(t *testing.T) {
	_, client := newFilingsServer(t)
	st := openS8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")

	// First run with a ZERO Form 4 budget… actually MaxForm4 <= 0 means the
	// default, so use a poller whose cap is consumed by a pre-inserted row:
	// simulate an earlier run that stored the filing but failed to parse it —
	// the row exists in filings with a resolvable archive URL but has no
	// insider_trades row.
	_, _ = st.InsertFiling(ctx, store.FilingRow{
		ID: "0000320193-26-000005", SymbolID: aapl.ID, Form: "4",
		FiledTs: 1780000000, Title: "FORM 4",
		URL:   client.FilingDocURL(320193, "0000320193-26-000005", "xslF345X05/wk-form4_1.xml"),
		Label: "Form 4 — insider transaction",
	})
	if got, _ := st.InsiderTrades(ctx, aapl.ID, "", 10); len(got) != 0 {
		t.Fatalf("precondition: trades = %d", len(got))
	}

	// The sweep sees the filing as ALREADY stored (isNew=false), so only the
	// backlog drain can parse it.
	w := &FilingsPoller{St: st, Client: client}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	trades, _ := st.InsiderTrades(ctx, aapl.ID, "", 10)
	if len(trades) != 1 || trades[0].Insider != "COOK TIMOTHY D" {
		t.Fatalf("backlog not drained: %+v", trades)
	}
}

// ── Form 4 sentinel / retry-cap regressions ───────────────────────────────

// derivOnlyForm4 is a real-shaped ownershipDocument with ONLY derivative
// transactions (options/RSUs) — no nonDerivativeTable at all. AggregateForm4
// yields ok=false for it; the poller must treat that as SUCCESS-EMPTY, not an
// error that re-serves the accession from UnparsedForm4s forever.
const derivOnlyForm4 = `<?xml version="1.0"?>
<ownershipDocument>
    <schemaVersion>X0508</schemaVersion>
    <documentType>4</documentType>
    <periodOfReport>2026-06-30</periodOfReport>
    <issuer>
        <issuerCik>0000320193</issuerCik>
        <issuerName>Apple Inc.</issuerName>
        <issuerTradingSymbol>AAPL</issuerTradingSymbol>
    </issuer>
    <reportingOwner>
        <reportingOwnerId>
            <rptOwnerCik>0001214156</rptOwnerCik>
            <rptOwnerName>JONES DERIVATIVE ONLY</rptOwnerName>
        </reportingOwnerId>
        <reportingOwnerRelationship>
            <isDirector>1</isDirector>
        </reportingOwnerRelationship>
    </reportingOwner>
    <derivativeTable>
        <derivativeTransaction>
            <securityTitle><value>Restricted Stock Unit</value></securityTitle>
            <transactionDate><value>2026-06-30</value></transactionDate>
            <transactionCoding><transactionCode>M</transactionCode></transactionCoding>
        </derivativeTransaction>
    </derivativeTable>
</ownershipDocument>`

// backlogServer serves a submissions feed with NO recent filings (so only the
// backlog drain touches documents) and one Form 4 doc; it counts doc fetches.
func backlogServer(t *testing.T, docStatus int, docBody []byte) (*edgar.FilingsClient, *int) {
	t.Helper()
	fetches := 0
	const emptySubs = `{"cik":"320193","filings":{"recent":{
		"accessionNumber": [], "filingDate": [], "acceptanceDateTime": [],
		"reportDate": [], "form": [], "items": [],
		"primaryDocument": [], "primaryDocDescription": []}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "company_tickers.json"):
			_, _ = w.Write([]byte(s8Tickers))
		case strings.Contains(r.URL.Path, "/submissions/CIK0000320193"):
			_, _ = w.Write([]byte(emptySubs))
		case strings.HasSuffix(r.URL.Path, "wk-form4_1.xml"):
			fetches++
			if docStatus != 200 {
				w.WriteHeader(docStatus)
				return
			}
			_, _ = w.Write(docBody)
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
	return c, &fetches
}

// seedUnparsedForm4 stores a Form 4 filing row with NO insider_trades row —
// exactly the state that used to re-serve the accession every run forever.
func seedUnparsedForm4(t *testing.T, st *store.Store, client *edgar.FilingsClient, symbolID int64, accession string) {
	t.Helper()
	_, err := st.InsertFiling(context.Background(), store.FilingRow{
		ID: accession, SymbolID: symbolID, Form: "4",
		FiledTs: 1780000000, Title: "FORM 4",
		URL:   client.FilingDocURL(320193, accession, "xslF345X05/wk-form4_1.xml"),
		Label: "Form 4 — insider transaction",
	})
	if err != nil {
		t.Fatalf("seed filing: %v", err)
	}
}

func TestFilingsPoller_DerivativeOnlyLeavesBacklog(t *testing.T) {
	client, fetches := backlogServer(t, 200, []byte(derivOnlyForm4))
	st := openS8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	const acc = "0000320193-26-000005"
	seedUnparsedForm4(t, st, client, aapl.ID, acc)

	w := &FilingsPoller{St: st, Client: client}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// SUCCESS-EMPTY: the accession must be OUT of the backlog for good…
	backlog, err := st.UnparsedForm4s(ctx, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlog) != 0 {
		t.Fatalf("derivative-only Form 4 still in UnparsedForm4s: %+v", backlog)
	}
	// …and the sentinel must never surface as a visible insider trade.
	trades, _ := st.InsiderTrades(ctx, aapl.ID, "", 10)
	if len(trades) != 0 {
		t.Fatalf("sentinel leaked into InsiderTrades: %+v", trades)
	}

	// A second run must not re-fetch the document (the old infinite-retry bug
	// burned ~300 SEC requests/day on exactly this).
	before := *fetches
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if *fetches != before {
		t.Fatalf("derivative-only Form 4 was re-fetched: fetches %d → %d", before, *fetches)
	}
	if before != 1 {
		t.Fatalf("want exactly 1 document fetch total, got %d", before)
	}
}

func TestFilingsPoller_AttemptCapAbandonsAfterThree(t *testing.T) {
	client, fetches := backlogServer(t, 404, nil) // document permanently unfetchable
	st := openS8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	const acc = "0000320193-26-000005"
	seedUnparsedForm4(t, st, client, aapl.ID, acc)

	w := &FilingsPoller{St: st, Client: client}

	// Runs 1 and 2: genuine failures — still in the backlog, retried.
	for i := 1; i <= 2; i++ {
		if _, err := w.Run(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		backlog, _ := st.UnparsedForm4s(ctx, 25)
		if len(backlog) != 1 {
			t.Fatalf("run %d: want accession still in backlog, got %+v", i, backlog)
		}
	}
	// Run 3: cap reached — abandoned with a sentinel + ONE dq event.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	backlog, _ := st.UnparsedForm4s(ctx, 25)
	if len(backlog) != 0 {
		t.Fatalf("abandoned accession still in backlog: %+v", backlog)
	}
	if *fetches != 3 {
		t.Fatalf("want exactly 3 fetch attempts, got %d", *fetches)
	}
	dq, err := st.RecentDQ(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	abandoned := 0
	for _, e := range dq {
		if e.Kind == "form4_abandoned" {
			abandoned++
		}
	}
	if abandoned != 1 {
		t.Fatalf("want exactly ONE form4_abandoned dq event, got %d (dq=%+v)", abandoned, dq)
	}

	// Run 4: nothing left to fetch — the retry loop has genuinely stopped.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 4: %v", err)
	}
	if *fetches != 3 {
		t.Fatalf("abandoned accession re-fetched after cap: fetches=%d", *fetches)
	}
	// Sentinel never shows up as a visible trade.
	trades, _ := st.InsiderTrades(ctx, aapl.ID, "", 10)
	if len(trades) != 0 {
		t.Fatalf("sentinel leaked into InsiderTrades: %+v", trades)
	}
}
