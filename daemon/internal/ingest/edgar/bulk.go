// Stage 4 — SIC/SECTOR COVERAGE: the EDGAR BULK submissions archive client.
//
// SOURCE (chosen after comparing the free options — see the sic-bulk-sync
// worker's doc comment in internal/pipeline/sicbulk.go): SEC's official
// nightly bulk export of the ENTIRE Submissions API, documented on
// "EDGAR Application Programming Interfaces"
// (https://www.sec.gov/search-filings/edgar-application-programming-interfaces):
//
//	https://www.sec.gov/Archives/edgar/daily-index/bulkdata/submissions.zip
//
// "The submission.zip file contains the public EDGAR filing history for all
// filers from the Submissions API … recompiled nightly [~3:00 a.m. ET]."
// Verified live 2026-07-06: HTTP 200, content-length 1,549,810,197 (~1.5 GB).
// Each entry CIK##########.json is the SAME document the per-company
// submissions API serves — including the sic/sicDescription header the
// filings-poller already extracts one company at a time — so ONE download
// yields the industry classification for every registrant in the directory.
//
// Memory discipline: the download STREAMS to a caller-owned temp file (never
// buffered in RAM — the shared get() helper would slurp 1.5 GB); extraction
// walks the zip's central directory ON DISK and fully decodes ONLY the
// entries whose CIK the caller asked for (~10.4k of ~1M), discarding
// everything but the three header fields. The caller deletes the temp file.
package edgar

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// bulkSubmissionsURL is the production nightly bulk archive.
const bulkSubmissionsURL = "https://www.sec.gov/Archives/edgar/daily-index/bulkdata/submissions.zip"

func (c *Client) bulkEndpoint() string {
	if c.BulkURL != "" {
		return c.BulkURL
	}
	return bulkSubmissionsURL
}

// DownloadBulkSubmissions performs exactly ONE paced, UA-stamped GET of the
// bulk archive and streams the body straight to dst (no retries — the worker
// policy is one download attempt per run; a failure degrades to the
// filings-poller rotation and tries again on the gate's next window).
// It deliberately does NOT use the shared get() helper (which buffers whole
// bodies in memory) nor the shared *http.Client (whose 30s Timeout covers the
// full body read — a 1.5 GB transfer needs minutes); cancellation/deadline
// come from ctx. The transport IS shared, and the request still flows through
// the same pace() limiter + ResolveUA header as every other EDGAR call.
// On failure the partial dst is removed. Returns bytes written.
func (c *Client) DownloadBulkSubmissions(ctx context.Context, dst string) (int64, error) {
	if err := c.pace(ctx); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.bulkEndpoint(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.ua())
	req.Header.Set("Accept", "application/zip, */*;q=0.8")
	dl := &http.Client{Transport: c.httpClient().Transport} // no overall Timeout; ctx governs
	resp, err := dl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return 0, fmt.Errorf("edgar: bulk submissions status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	f, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return 0, fmt.Errorf("edgar: bulk submissions download: %w", err)
	}
	return n, nil
}

// BulkSIC is one registrant's industry classification from the bulk archive.
type BulkSIC struct {
	CIK     int64
	SIC     string
	SICDesc string
}

// bulkEntryRE matches ONLY the top-level per-CIK documents. The archive also
// contains pagination pages (CIK##########-submissions-001.json) which carry
// NO sic header and must not match, plus assorted non-CIK files.
var bulkEntryRE = regexp.MustCompile(`^CIK(\d{10})\.json$`)

// bulkHeader is the ONLY part of each ~MB entry we keep; the (large) filings
// history is parsed past and discarded by the decoder.
type bulkHeader struct {
	SIC     string `json:"sic"`
	SICDesc string `json:"sicDescription"`
}

// ExtractBulkSIC scans the bulk submissions zip at zipPath and returns the
// SIC classification of every entry whose CIK is in want (nil want = every
// entry — avoid on the real 1M-entry archive). matched counts the entries in
// want that were found at all; recs carries only those with a non-blank SIC
// (EDGAR genuinely has no classification for some registrants — honest
// absence, never a fabricated ""). One malformed entry is skipped, never
// aborting the sweep. The CIK is taken from the entry NAME (zero-padded,
// unambiguous) rather than the body, whose cik field is string-or-number.
func ExtractBulkSIC(zipPath string, want map[int64]bool) (recs []BulkSIC, matched int, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, 0, fmt.Errorf("edgar: open bulk submissions zip: %w", err)
	}
	defer zr.Close() //nolint:errcheck
	for _, f := range zr.File {
		m := bulkEntryRE.FindStringSubmatch(path.Base(f.Name))
		if m == nil {
			continue
		}
		cik, perr := strconv.ParseInt(m[1], 10, 64)
		if perr != nil || cik <= 0 {
			continue
		}
		if want != nil && !want[cik] {
			continue // never decompress entries nobody asked for
		}
		matched++
		rc, oerr := f.Open()
		if oerr != nil {
			continue // one unreadable entry never aborts the sweep
		}
		var hdr bulkHeader
		derr := json.NewDecoder(rc).Decode(&hdr)
		rc.Close() //nolint:errcheck
		if derr != nil {
			continue
		}
		sic := strings.TrimSpace(hdr.SIC)
		if sic == "" {
			continue // honest absence — a blank SIC is never stored
		}
		recs = append(recs, BulkSIC{CIK: cik, SIC: sic, SICDesc: strings.TrimSpace(hdr.SICDesc)})
	}
	return recs, matched, nil
}
