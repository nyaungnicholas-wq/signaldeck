// Stage 4 tests — the EDGAR bulk submissions client: fixture-zip extraction
// (want-set filtering, pagination-decoy + blank-SIC skipping) and the
// streaming downloader (UA stamped, bytes on disk, non-200 errors, partial
// cleanup). httptest + testdata only — no real SEC traffic.
package edgar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const bulkFixture = "testdata/bulk_submissions.zip"

// Fixture contents (see testdata): CIK 320193 (sic 3571), CIK 1045810
// (sic 3674), CIK 19617 (BLANK sic), a CIK0000320193-submissions-001.json
// pagination decoy, and a non-CIK placeholder.txt.

func TestExtractBulkSIC_WantSet(t *testing.T) {
	want := map[int64]bool{320193: true, 1045810: true, 19617: true}
	recs, matched, err := ExtractBulkSIC(bulkFixture, want)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	// All 3 top-level entries match the want set; the pagination decoy and the
	// placeholder must not count even though 320193 is wanted.
	if matched != 3 {
		t.Fatalf("matched = %d, want 3", matched)
	}
	// Only 2 carry a SIC — the blank one is honest absence, never emitted.
	if len(recs) != 2 {
		t.Fatalf("recs = %+v, want 2", recs)
	}
	got := map[int64]BulkSIC{}
	for _, r := range recs {
		got[r.CIK] = r
	}
	if r := got[320193]; r.SIC != "3571" || r.SICDesc != "Electronic Computers" {
		t.Fatalf("AAPL rec = %+v", r)
	}
	if r := got[1045810]; r.SIC != "3674" || r.SICDesc != "Semiconductors & Related Devices" {
		t.Fatalf("NVDA rec = %+v", r)
	}
}

func TestExtractBulkSIC_FiltersToWant(t *testing.T) {
	recs, matched, err := ExtractBulkSIC(bulkFixture, map[int64]bool{1045810: true})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if matched != 1 || len(recs) != 1 || recs[0].CIK != 1045810 {
		t.Fatalf("matched=%d recs=%+v — entries outside want must never be decoded", matched, recs)
	}
}

func TestExtractBulkSIC_NilWantScansAll(t *testing.T) {
	recs, matched, err := ExtractBulkSIC(bulkFixture, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if matched != 3 || len(recs) != 2 {
		t.Fatalf("matched=%d recs=%d, want 3/2", matched, len(recs))
	}
}

func TestExtractBulkSIC_CorruptZip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "corrupt.zip")
	if err := os.WriteFile(p, []byte("this is not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExtractBulkSIC(p, nil); err == nil {
		t.Fatal("corrupt zip must error (the worker degrades on it)")
	}
}

func TestDownloadBulkSubmissions_StreamsToFile(t *testing.T) {
	body, err := os.ReadFile(bulkFixture)
	if err != nil {
		t.Fatal(err)
	}
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	c := New()
	c.UA = "test t@e.c"
	c.BulkURL = srv.URL + "/Archives/edgar/daily-index/bulkdata/submissions.zip"
	c.MinInterval = time.Millisecond

	dst := filepath.Join(t.TempDir(), "bulk.zip")
	n, err := c.DownloadBulkSubmissions(context.Background(), dst)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("bytes = %d, want %d", n, len(body))
	}
	if gotUA != "test t@e.c" {
		t.Fatalf("UA = %q — SEC requires the declarative User-Agent on EVERY request", gotUA)
	}
	// The downloaded file must be the archive verbatim (extractable).
	recs, matched, err := ExtractBulkSIC(dst, nil)
	if err != nil || matched != 3 || len(recs) != 2 {
		t.Fatalf("round-trip extract: recs=%d matched=%d err=%v", len(recs), matched, err)
	}
}

func TestDownloadBulkSubmissions_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := New()
	c.UA = "test t@e.c"
	c.BulkURL = srv.URL + "/submissions.zip"
	c.MinInterval = time.Millisecond

	dst := filepath.Join(t.TempDir(), "bulk.zip")
	_, err := c.DownloadBulkSubmissions(context.Background(), dst)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want a 403 status error (one attempt, no retries)", err)
	}
	if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
		t.Fatalf("no file may be left behind on a failed download: %v", serr)
	}
}
