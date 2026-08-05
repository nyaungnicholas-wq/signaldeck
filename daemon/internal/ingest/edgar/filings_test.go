package edgar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// ── form labels ───────────────────────────────────────────────────────────

func TestFormLabel(t *testing.T) {
	cases := []struct {
		form, items, want string
	}{
		{"4", "", "Form 4 — insider transaction"},
		{"4/A", "", "Form 4 — insider transaction (amended)"},
		{"8-K", "", "8-K — material event"},
		{"8-K", "2.02,9.01", "8-K — earnings release (Item 2.02)"},
		{"8-K", "1.03", "8-K — bankruptcy (Item 1.03)"},
		{"8-K", "3.02,8.01", "8-K — unregistered equity sale (dilution) (Item 3.02), other material event (Item 8.01)"},
		{"S-8", "", "S-8 — employee stock plan"},
		{"S-8 POS", "", "S-8 — employee stock plan"},
		{"S-1", "", "S-1 — new share registration (dilution watch)"},
		{"S-3", "", "S-3 — shelf registration (dilution watch)"},
		{"S-3ASR", "", "S-3 — shelf registration (dilution watch)"},
		{"424B5", "", "424B5 — offering prospectus (dilution)"},
		{"SC 13D", "", "13D — activist stake >5%"},
		{"SC 13G/A", "", "13G — passive large holder >5% (amended)"},
		{"10-Q", "", "10-Q — quarterly report"},
		{"10-K", "", "10-K — annual report"},
		{"13F-HR", "", "13F — institutional holdings (quarterly, ≤45d lag)"},
		{"DEF 14A", "", "DEF 14A — proxy statement"},
		{"XYZZY", "", "XYZZY — SEC filing"}, // unknown: honest fallback, never invented
	}
	for _, c := range cases {
		if got := FormLabel(c.form, c.items); got != c.want {
			t.Errorf("FormLabel(%q,%q) = %q, want %q", c.form, c.items, got, c.want)
		}
	}
}

func TestInterestingForm(t *testing.T) {
	for _, f := range []string{"4", "4/A", "8-K", "10-K", "S-3ASR", "424B2", "SC 13D", "SC 13G/A", "S-8 POS", "144"} {
		if !InterestingForm(f) {
			t.Errorf("InterestingForm(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"25-NSE", "CERT", "NO ACT", "UPLOAD", ""} {
		if InterestingForm(f) {
			t.Errorf("InterestingForm(%q) = true, want false", f)
		}
	}
}

// ── Form 4 parsing ────────────────────────────────────────────────────────

func TestParseForm4_SaleFixture(t *testing.T) {
	f, err := ParseForm4(readFixture(t, "form4_sale.xml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Insider != "COOK TIMOTHY D" {
		t.Errorf("insider = %q", f.Insider)
	}
	if f.Title != "Chief Executive Officer" {
		t.Errorf("title = %q", f.Title)
	}
	if len(f.Transactions) != 2 {
		t.Fatalf("transactions = %d, want 2", len(f.Transactions))
	}
	tx := f.Transactions[0]
	if tx.Code != "S" || tx.Shares != 50000 || tx.Price != 210.55 || tx.AcqDisp != "D" {
		t.Errorf("tx0 = %+v", tx)
	}
	if tx.PostShares != 3300000 {
		t.Errorf("post shares = %v", tx.PostShares)
	}
	wantTs, _ := parseDay("2026-06-30")
	if tx.TxTs != wantTs {
		t.Errorf("tx ts = %d, want %d", tx.TxTs, wantTs)
	}

	// Aggregation: two S transactions collapse into one weighted row.
	code, shares, price, value, txTs, ok := AggregateForm4(f)
	if !ok || code != "S" {
		t.Fatalf("aggregate code = %q ok=%v", code, ok)
	}
	if shares != 75000 {
		t.Errorf("agg shares = %v, want 75000", shares)
	}
	wantVal := 50000*210.55 + 25000*211.00
	if value != wantVal {
		t.Errorf("agg value = %v, want %v", value, wantVal)
	}
	wantPx := wantVal / 75000
	if price < wantPx-1e-9 || price > wantPx+1e-9 {
		t.Errorf("agg price = %v, want %v", price, wantPx)
	}
	if txTs != wantTs {
		t.Errorf("agg ts = %d, want %d", txTs, wantTs)
	}
}

func TestParseForm4_GrantVsBuy_DominantCode(t *testing.T) {
	f, err := ParseForm4(readFixture(t, "form4_grant_buy.xml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Title != "Director" {
		t.Errorf("title = %q, want Director (isOfficer=0)", f.Title)
	}
	// A: 1200 shares @ $0 (grant, $0 value). P: 500 @ $120 = $60k. Dominant by
	// DOLLAR value must be P — a zero-cost grant never outranks a real buy.
	code, shares, price, value, _, ok := AggregateForm4(f)
	if !ok || code != "P" {
		t.Fatalf("dominant code = %q, want P (ok=%v)", code, ok)
	}
	if shares != 500 || price != 120 || value != 60000 {
		t.Errorf("agg = shares %v price %v value %v", shares, price, value)
	}
}

func TestCodeLabel_HonestClassification(t *testing.T) {
	if got := CodeLabel("P"); got != "buy (open market)" {
		t.Errorf("P label = %q", got)
	}
	if got := CodeLabel("S"); got != "sell (open market)" {
		t.Errorf("S label = %q", got)
	}
	// Non-open-market codes must SAY they're not open-market trades.
	for _, c := range []string{"A", "M", "G", "F", "D", "C", "X"} {
		if !strings.Contains(CodeLabel(c), "not an open-market trade") {
			t.Errorf("CodeLabel(%q) = %q — must flag non-open-market", c, CodeLabel(c))
		}
	}
}

func TestRawXMLDoc(t *testing.T) {
	if got := RawXMLDoc("xslF345X05/wk-form4_1688.xml"); got != "wk-form4_1688.xml" {
		t.Errorf("RawXMLDoc = %q", got)
	}
	if got := RawXMLDoc("plain.xml"); got != "plain.xml" {
		t.Errorf("RawXMLDoc plain = %q", got)
	}
}

// ── 13F parsing ───────────────────────────────────────────────────────────

func TestParse13F_Fixture(t *testing.T) {
	hs, err := Parse13F(readFixture(t, "13f_infotable.xml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hs) != 2 {
		t.Fatalf("holdings = %d, want 2", len(hs))
	}
	if hs[0].Issuer != "APPLE INC" || hs[0].CUSIP != "037833100" {
		t.Errorf("h0 = %+v", hs[0])
	}
	if hs[0].Value != 65000000000 || hs[0].Shares != 300000000 || hs[0].Type != "SH" {
		t.Errorf("h0 amounts = %+v", hs[0])
	}
	if hs[1].Issuer != "OBSCURE WIDGETS LTD" {
		t.Errorf("h1 = %+v", hs[1])
	}
}

func TestFindInfoTableFile(t *testing.T) {
	name, err := FindInfoTableFile(readFixture(t, "13f_index.json"))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if name != "form13fInfoTable.xml" {
		t.Errorf("info table file = %q", name)
	}
	// No infotable name → fall back to a non-primary_doc xml.
	fallback := []byte(`{"directory":{"item":[{"name":"primary_doc.xml"},{"name":"holdings.xml"}]}}`)
	name, err = FindInfoTableFile(fallback)
	if err != nil || name != "holdings.xml" {
		t.Errorf("fallback = %q err=%v", name, err)
	}
	// Nothing usable → explicit error, never a guess.
	if _, err := FindInfoTableFile([]byte(`{"directory":{"item":[{"name":"primary_doc.xml"}]}}`)); err == nil {
		t.Error("want error when no info-table xml exists")
	}
}

// ── submissions API + URL building + pacing ───────────────────────────────

const submissionsJSON = `{
  "cik": "320193",
  "name": "Apple Inc.",
  "filings": {
    "recent": {
      "accessionNumber": ["0000320193-26-000005", "0000320193-26-000004", "0000320193-26-000003"],
      "filingDate": ["2026-07-01", "2026-06-15", "2026-06-01"],
      "acceptanceDateTime": ["2026-07-01T18:04:25.000Z", "2026-06-15T16:31:02.000Z", ""],
      "reportDate": ["2026-06-30", "", "2026-03-28"],
      "form": ["4", "8-K", "10-Q"],
      "items": ["", "2.02,9.01", ""],
      "primaryDocument": ["xslF345X05/wk-form4_1.xml", "aapl-8k.htm", "aapl-10q.htm"],
      "primaryDocDescription": ["FORM 4", "8-K", "10-Q"]
    }
  }
}`

func TestSubmissions_ParsesRows(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		ua = r.Header.Get("User-Agent")
		mu.Unlock()
		if strings.Contains(r.URL.Path, "CIK0000320193") {
			_, _ = w.Write([]byte(submissionsJSON))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := NewFilings()
	c.SubmissionsBase = srv.URL + "/submissions"
	c.MinInterval = 30 * time.Millisecond

	subs, err := c.Submissions(context.Background(), 320193)
	if err != nil {
		t.Fatalf("submissions: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("rows = %d, want 3", len(subs))
	}
	f0 := subs[0]
	if f0.Accession != "0000320193-26-000005" || f0.Form != "4" || f0.Items != "" {
		t.Errorf("row0 = %+v", f0)
	}
	// Acceptance time preferred over date-only filing date.
	acc, _ := time.Parse(time.RFC3339, "2026-07-01T18:04:25Z")
	if f0.FiledTs != acc.Unix() {
		t.Errorf("row0 filedTs = %d, want %d (acceptance time)", f0.FiledTs, acc.Unix())
	}
	// Blank acceptance falls back to the filing date.
	day, _ := parseDay("2026-06-01")
	if subs[2].FiledTs != day {
		t.Errorf("row2 filedTs = %d, want %d (filing date fallback)", subs[2].FiledTs, day)
	}
	if subs[1].Items != "2.02,9.01" {
		t.Errorf("row1 items = %q", subs[1].Items)
	}

	// A second call must be paced by the shared limiter (>=1 interval apart)
	// and carry the descriptive UA required by SEC policy.
	if _, err := c.Submissions(context.Background(), 320193); err != nil {
		t.Fatalf("second call: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != 2 {
		t.Fatalf("requests = %d, want 2", len(times))
	}
	// Floor is two thirds of the interval, not interval-minus-a-few-ms. The
	// times recorded here are SERVER arrivals, but pace() stamps c.last before
	// the request is issued — so the first call's connection setup, plus
	// goroutine scheduling delay under a parallel `go test ./...`, both land
	// between the stamp and the socket write and compress the observed gap.
	// This failed CI at 23.5ms against a 25ms floor while passing 5/5 in
	// isolation. The assertion loses no power: an unpaced path arrives
	// sub-millisecond apart, nowhere near 20ms, so the regression this exists
	// to catch still fails it hard.
	const floor = 30 * time.Millisecond * 2 / 3
	if span := times[1].Sub(times[0]); span < floor {
		t.Errorf("requests not spaced: %v (< %v; limiter must apply to submissions too)", span, floor)
	}
	if !strings.Contains(strings.ToLower(ua), "signaldeck") {
		t.Errorf("User-Agent not descriptive: %q", ua)
	}
}

func TestFilingURLs(t *testing.T) {
	c := NewFilings()
	got := c.FilingDocURL(320193, "0000320193-26-000005", "wk-form4_1.xml")
	want := "https://www.sec.gov/Archives/edgar/data/320193/000032019326000005/wk-form4_1.xml"
	if got != want {
		t.Errorf("FilingDocURL = %q, want %q", got, want)
	}
	if got := c.FilingIndexURL(320193, "0000320193-26-000005"); !strings.HasSuffix(got, "/000032019326000005/index.json") {
		t.Errorf("FilingIndexURL = %q", got)
	}
	if got := PublicFilingURL(320193, "0000320193-26-000005", "xslF345X05/wk.xml"); !strings.HasPrefix(got, "https://www.sec.gov/Archives/edgar/data/320193/") {
		t.Errorf("PublicFilingURL = %q", got)
	}
}

func TestFetch13FHoldings_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "index.json"):
			_, _ = w.Write(readFixture(t, "13f_index.json"))
		case strings.HasSuffix(r.URL.Path, "form13fInfoTable.xml"):
			_, _ = w.Write(readFixture(t, "13f_infotable.xml"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := NewFilings()
	c.ArchivesBase = srv.URL + "/Archives/edgar/data"
	c.MinInterval = time.Millisecond

	hs, err := c.Fetch13FHoldings(context.Background(), 1067983, "0001067983-26-000012")
	if err != nil {
		t.Fatalf("fetch 13F: %v", err)
	}
	if len(hs) != 2 || hs[0].CUSIP != "037833100" {
		t.Fatalf("holdings = %+v", hs)
	}
}

func TestFetchForm4_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The raw XML must be requested WITHOUT the xsl viewer prefix.
		if strings.Contains(r.URL.Path, "xslF345X05") {
			w.WriteHeader(404)
			return
		}
		if strings.HasSuffix(r.URL.Path, "wk-form4_1.xml") {
			_, _ = w.Write(readFixture(t, "form4_sale.xml"))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := NewFilings()
	c.ArchivesBase = srv.URL + "/Archives/edgar/data"
	c.MinInterval = time.Millisecond

	f, err := c.FetchForm4(context.Background(), 320193, "0000320193-26-000005", "xslF345X05/wk-form4_1.xml")
	if err != nil {
		t.Fatalf("fetch form4: %v", err)
	}
	if f.Insider != "COOK TIMOTHY D" || len(f.Transactions) != 2 {
		t.Fatalf("form4 = %+v", f)
	}
}
