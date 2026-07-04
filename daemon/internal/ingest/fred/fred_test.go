package fred

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// vixclsCSV is a realistic keyless fredgraph.csv?id=VIXCLS body: a header row,
// good rows, a "." missing-observation row, and a trailing blank line.
const vixclsCSV = `observation_date,VIXCLS
2026-06-27,13.42
2026-06-30,.
2026-07-01,12.88
2026-07-02,14.10
`

// dgs10JSON is a realistic FRED JSON observations response (keyed API path).
const dgs10JSON = `{
  "observations": [
    {"date":"2026-06-30","value":"4.28"},
    {"date":"2026-07-01","value":"."},
    {"date":"2026-07-02","value":"4.31"}
  ]
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

// serveCSV returns an httptest server serving body for any path and recording
// the last request seen (for query/UA assertions).
func serve(t *testing.T, status int, body string) (*httptest.Server, func() *http.Request) {
	t.Helper()
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := *r
		seen = &captured
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() *http.Request { return seen }
}

func TestFetchCSV_ParsesAndSkipsMissing(t *testing.T) {
	srv, last := serve(t, 200, vixclsCSV)
	c := New("") // keyless ⇒ CSV path
	c.CSVBase = srv.URL

	pts, err := c.Fetch(context.Background(), "VIXCLS")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	// 4 data rows, one is "." ⇒ 3 real observations.
	if len(pts) != 3 {
		t.Fatalf("want 3 points, got %d: %+v", len(pts), pts)
	}
	if pts[0].Value != 13.42 || pts[2].Value != 14.10 {
		t.Fatalf("unexpected values: %+v", pts)
	}
	// UA must be stamped.
	if ua := last().Header.Get("User-Agent"); ua == "" {
		t.Fatal("no User-Agent header sent")
	}
	// query id must be the series.
	if got := last().URL.Query().Get("id"); got != "VIXCLS" {
		t.Fatalf("id query = %q, want VIXCLS", got)
	}
	// ts must be a UTC-midnight epoch (2026-06-27 00:00 UTC).
	if pts[0].Ts%86400 != 0 {
		t.Fatalf("ts not day-aligned: %d", pts[0].Ts)
	}
}

func TestFetchJSON_WhenKeySet(t *testing.T) {
	srv, last := serve(t, 200, dgs10JSON)
	c := New("secret-key") // key ⇒ JSON path
	c.JSONBase = srv.URL

	pts, err := c.Fetch(context.Background(), "DGS10")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(pts) != 2 { // 3 obs, one "." ⇒ 2
		t.Fatalf("want 2 points, got %d", len(pts))
	}
	q := last().URL.Query()
	if q.Get("series_id") != "DGS10" {
		t.Fatalf("series_id = %q", q.Get("series_id"))
	}
	if q.Get("api_key") != "secret-key" {
		t.Fatalf("api_key not sent")
	}
	if q.Get("file_type") != "json" {
		t.Fatalf("file_type = %q", q.Get("file_type"))
	}
}

func TestIngest_PersistsAndIsIdempotent(t *testing.T) {
	srv, _ := serve(t, 200, vixclsCSV)
	st := openTestStore(t)
	c := New("")
	c.CSVBase = srv.URL

	n, err := c.Ingest(context.Background(), st, []string{"VIXCLS"})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 3 {
		t.Fatalf("ingest count = %d, want 3", n)
	}
	// LatestMacro / LatestVIX must return the newest observation (14.10).
	v, ok, err := st.LatestVIX(context.Background())
	if err != nil || !ok {
		t.Fatalf("latest vix: ok=%v err=%v", ok, err)
	}
	if v != 14.10 {
		t.Fatalf("latest vix = %v, want 14.10", v)
	}
	// Re-ingest is idempotent (INSERT OR IGNORE): the same 3 rows.
	if _, err := c.Ingest(context.Background(), st, []string{"VIXCLS"}); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	all, err := st.MacroSeries(context.Background(), "VIXCLS", 0)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("after re-ingest want 3 rows, got %d", len(all))
	}
	// MacroSeries is oldest-first.
	if all[0].Value != 13.42 || all[len(all)-1].Value != 14.10 {
		t.Fatalf("series not oldest-first: %+v", all)
	}
}

func TestFetch_GracefulUpstreamError(t *testing.T) {
	srv, _ := serve(t, 503, "service unavailable")
	c := New("")
	c.CSVBase = srv.URL
	if _, err := c.Fetch(context.Background(), "VIXCLS"); err == nil {
		t.Fatal("want error on 503, got nil")
	}
}

func TestFetch_EmptySeriesNoRows(t *testing.T) {
	// A header-only CSV ⇒ zero observations, no error (graceful absence).
	srv, _ := serve(t, 200, "observation_date,VIXCLS\n")
	c := New("")
	c.CSVBase = srv.URL
	pts, err := c.Fetch(context.Background(), "VIXCLS")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(pts) != 0 {
		t.Fatalf("want 0 points, got %d", len(pts))
	}
}
