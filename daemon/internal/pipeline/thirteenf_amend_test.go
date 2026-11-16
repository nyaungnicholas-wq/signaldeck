package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
)

// A NEW HOLDINGS 13F-HR/A adds to the quarter's original filing; reading only
// the newest filing stored the amendment's few lines as the whole quarter.
// A stored quarter is rebuilt once, then left alone until a new filing lands.
func TestThirteenFPoller_NewHoldingsAmendmentAddsToOriginal(t *testing.T) {
	info := func(cusip, name string) string {
		return `<?xml version="1.0"?><informationTable><infoTable><nameOfIssuer>` + name +
			`</nameOfIssuer><titleOfClass>COM</titleOfClass><cusip>` + cusip +
			`</cusip><value>100</value><shrsOrPrnAmt><sshPrnamt>10</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt></infoTable></informationTable>`
	}
	subs := `{"cik": "1067983", "filings": {"recent": {
    "accessionNumber": ["0001067983-26-000020", "0001067983-26-000012"],
    "filingDate": ["2026-06-01", "2026-05-14"],
    "acceptanceDateTime": ["2026-06-01T16:00:00.000Z", "2026-05-14T16:00:00.000Z"],
    "reportDate": ["2026-03-31", "2026-03-31"],
    "form": ["13F-HR/A", "13F-HR"],
    "items": ["", ""],
    "primaryDocument": ["primary_doc.xml", "primary_doc.xml"],
    "primaryDocDescription": ["13F-HR/A", "13F-HR"]}}}`
	index := `{"directory":{"item":[{"name":"primary_doc.xml"},{"name":"infotable.xml"}]}}`
	cover := `<edgarSubmission xmlns="http://www.sec.gov/edgar/thirteenffiler"><formData><coverPage><isAmendment>true</isAmendment><amendmentInfo><amendmentType>NEW HOLDINGS</amendmentType></amendmentInfo></coverPage></formData></edgarSubmission>`
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/submissions/CIK0001067983"):
			_, _ = w.Write([]byte(subs))
		case strings.HasSuffix(p, "/index.json"):
			hits++
			_, _ = w.Write([]byte(index))
		case strings.HasSuffix(p, "/000106798326000012/infotable.xml"):
			_, _ = w.Write([]byte(info("037833100", "APPLE INC")))
		case strings.HasSuffix(p, "/000106798326000020/infotable.xml"):
			_, _ = w.Write([]byte(info("594918104", "MICROSOFT CORP")))
		case strings.HasSuffix(p, "/000106798326000020/primary_doc.xml"):
			_, _ = w.Write([]byte(cover))
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
	rows, err := st.InstHoldingsByManager(ctx, "1067983", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("holdings = %+v err=%v, want the original's line plus the amendment's", rows, err)
	}
	before := hits
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if hits != before {
		t.Fatalf("second run re-fetched an already-built quarter (%d -> %d index fetches)", before, hits)
	}
}

// The newest report PERIOD is built even when the newest FILING is a late
// amendment for an older quarter.
func TestThirteenFPoller_BuildsNewestPeriodNotNewestFiling(t *testing.T) {
	subs := `{"cik": "1067983", "filings": {"recent": {
    "accessionNumber": ["0001067983-26-000020", "0001067983-26-000012"],
    "filingDate": ["2026-08-20", "2026-08-14"],
    "acceptanceDateTime": ["2026-08-20T16:00:00.000Z", "2026-08-14T16:00:00.000Z"],
    "reportDate": ["2026-03-31", "2026-06-30"],
    "form": ["13F-HR/A", "13F-HR"],
    "items": ["", ""],
    "primaryDocument": ["primary_doc.xml", "primary_doc.xml"],
    "primaryDocDescription": ["13F-HR/A", "13F-HR"]}}}`
	info := `<?xml version="1.0"?><informationTable><infoTable><nameOfIssuer>APPLE INC</nameOfIssuer><titleOfClass>COM</titleOfClass><cusip>037833100</cusip><value>100</value><shrsOrPrnAmt><sshPrnamt>10</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt></infoTable></informationTable>`
	cover := `<edgarSubmission><formData><coverPage><amendmentInfo><amendmentType>RESTATEMENT</amendmentType></amendmentInfo></coverPage></formData></edgarSubmission>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/submissions/CIK0001067983"):
			_, _ = w.Write([]byte(subs))
		case strings.HasSuffix(p, "/index.json"):
			_, _ = w.Write([]byte(`{"directory":{"item":[{"name":"primary_doc.xml"},{"name":"infotable.xml"}]}}`))
		case strings.HasSuffix(p, "/infotable.xml"):
			_, _ = w.Write([]byte(info))
		case strings.HasSuffix(p, "/primary_doc.xml"):
			_, _ = w.Write([]byte(cover))
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
	w := &ThirteenFPoller{St: st, Client: c, Managers: []edgar.Manager{{CIK: 1067983, Name: "Berkshire Hathaway"}}}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	periods, err := st.InstPeriods(ctx, "1067983")
	if err != nil || len(periods) != 1 || periods[0] != "2026-06-30" {
		t.Fatalf("periods = %v err=%v, want [2026-06-30]", periods, err)
	}
}
