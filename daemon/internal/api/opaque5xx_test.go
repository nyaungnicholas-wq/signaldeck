package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoRawErrorsIn5xxBodies keeps store and engine errors out of 5xx answers.
// The 2026-10-05 security review found 15 handlers answering
// httpErr(w, 500, "...: "+err.Error()); anonymous /api/prereg returned
// "SQL logic error: no such table: prereg_records". httpInternal logs the
// error and answers "internal error"; a 4xx may still carry a client-safe
// message (symbolFromQuery's errors are written for that).
func TestNoRawErrorsIn5xxBodies(t *testing.T) {
	bad := regexp.MustCompile(`httpErr\(w,\s*(?:5\d\d|http\.StatusInternalServerError|http\.StatusServiceUnavailable|http\.StatusBadGateway)\s*,[^\n]*err\.Error\(\)`)
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found (err %v): the scan would pass vacuously", err)
	}
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for i, line := range strings.Split(string(b), "\n") {
			if bad.MatchString(line) {
				t.Errorf("%s:%d answers a 5xx with a raw error; use httpInternal: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
	if scanned < 20 {
		t.Fatalf("scanned only %d files; the glob is not seeing the package", scanned)
	}
}
