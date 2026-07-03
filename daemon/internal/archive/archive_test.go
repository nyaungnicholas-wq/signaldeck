package archive

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// readGzCSV reads a .csv.gz back into header + string records.
func readGzCSV(t *testing.T, path string) (header []string, records [][]string) {
	t.Helper()
	fp, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fp.Close()
	gz, err := gzip.NewReader(fp)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	cr := csv.NewReader(gz)
	all, err := cr.ReadAll()
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatalf("empty archive %s", path)
	}
	return all[0], all[1:]
}

// findOne returns the single file under dir/sub matching *.csv.gz.
func findOne(t *testing.T, dir string) string {
	t.Helper()
	var matches []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(p) == ".gz" {
			matches = append(matches, p)
		}
		return nil
	})
	if len(matches) != 1 {
		t.Fatalf("want exactly 1 archive file under %s, got %d: %v", dir, len(matches), matches)
	}
	return matches[0]
}

// Round-trip: rows written == rows read back, exact values, header present,
// and the file is a real gzip CSV (DuckDB/pandas readable).
func TestArchiveBarsRoundTrip(t *testing.T) {
	root := t.TempDir()
	a := New(root)
	names := map[int64]string{7: "AAPL"}
	bars := []md.Bar{
		{SymbolID: 7, TF: md.TF1m, Ts: 1000, Open: 100.5, High: 101, Low: 99.25, Close: 100.75, Volume: 1234},
		{SymbolID: 7, TF: md.TF1m, Ts: 1060, Open: 100.75, High: 102, Low: 100, Close: 101.5, Volume: 5678},
	}
	n, err := a.ArchiveBars(context.Background(), md.TF1m, bars, names)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 file (one symbol), got %d", n)
	}

	// Filename encodes symbol + the actual ts span, under bars_1m/.
	sub := filepath.Join(root, "bars_1m")
	path := findOne(t, sub)
	if base := filepath.Base(path); base != "AAPL_1000-1060.csv.gz" {
		t.Fatalf("filename = %q, want AAPL_1000-1060.csv.gz", base)
	}

	header, recs := readGzCSV(t, path)
	wantHeader := []string{"symbol_id", "symbol", "tf", "ts", "open", "high", "low", "close", "volume"}
	if len(header) != len(wantHeader) {
		t.Fatalf("header = %v, want %v", header, wantHeader)
	}
	for i := range wantHeader {
		if header[i] != wantHeader[i] {
			t.Fatalf("header[%d] = %q, want %q", i, header[i], wantHeader[i])
		}
	}
	if len(recs) != len(bars) {
		t.Fatalf("want %d data rows, got %d", len(bars), len(recs))
	}
	// Exact values on the first row.
	r0 := recs[0]
	if r0[0] != "7" || r0[1] != "AAPL" || r0[2] != "1m" || r0[3] != "1000" {
		t.Fatalf("row0 identity wrong: %v", r0)
	}
	mustF := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	if mustF(r0[4]) != 100.5 || mustF(r0[5]) != 101 || mustF(r0[6]) != 99.25 ||
		mustF(r0[7]) != 100.75 || mustF(r0[8]) != 1234 {
		t.Fatalf("row0 values wrong: %v", r0)
	}
}

// Snapshots round-trip: values survive the CSV/gzip path exactly.
func TestArchiveSnapshotsRoundTrip(t *testing.T) {
	root := t.TempDir()
	a := New(root)
	names := map[int64]string{3: "BTC/USD"} // '/' must be made filesystem-safe
	snaps := []md.Snap1s{
		{SymbolID: 3, Ts: 500, Bid: 60000.1, Ask: 60000.5, Mid: 60000.3, WMid: 60000.2, ImbSigned: -0.4, Spread: 0.4, ApplyLatNs: 12345},
	}
	n, err := a.ArchiveSnapshots(context.Background(), snaps, names)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 file, got %d", n)
	}
	sub := filepath.Join(root, "snapshots_1s")
	path := findOne(t, sub)
	if base := filepath.Base(path); base != "BTC-USD_500-500.csv.gz" {
		t.Fatalf("filename = %q, want BTC-USD_500-500.csv.gz (slash sanitized)", base)
	}
	header, recs := readGzCSV(t, path)
	if header[0] != "symbol_id" || header[len(header)-1] != "apply_lat_ns" {
		t.Fatalf("snap header wrong: %v", header)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 data row, got %d", len(recs))
	}
	mustF := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	r := recs[0]
	if r[1] != "BTC/USD" { // the CSV column keeps the real symbol
		t.Fatalf("symbol column = %q, want BTC/USD", r[1])
	}
	if mustF(r[3]) != 60000.1 || mustF(r[7]) != -0.4 || r[9] != "12345" {
		t.Fatalf("snap values wrong: %v", r)
	}
}

// Empty input is a no-op (no file, no error) — an empty prune must not create
// spurious archive files.
func TestArchiveEmptyNoOp(t *testing.T) {
	root := t.TempDir()
	a := New(root)
	n, err := a.ArchiveBars(context.Background(), md.TF1m, nil, nil)
	if err != nil || n != 0 {
		t.Fatalf("empty ArchiveBars: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, "bars_1m")); !os.IsNotExist(err) {
		t.Fatalf("empty archive must not create a directory")
	}
}

// Multiple symbols → one file each.
func TestArchiveBarsPerSymbolFiles(t *testing.T) {
	root := t.TempDir()
	a := New(root)
	names := map[int64]string{1: "AAA", 2: "BBB"}
	bars := []md.Bar{
		{SymbolID: 1, TF: md.TF1h, Ts: 0, Close: 1},
		{SymbolID: 2, TF: md.TF1h, Ts: 0, Close: 2},
		{SymbolID: 1, TF: md.TF1h, Ts: 3600, Close: 3},
	}
	n, err := a.ArchiveBars(context.Background(), md.TF1h, bars, names)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("want 2 files (2 symbols), got %d", n)
	}
}

// DirSize returns 0 for a missing root (not an error) and sums file bytes.
func TestDirSize(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "nope")
	if b, err := DirSize(missing); err != nil || b != 0 {
		t.Fatalf("missing dir: b=%d err=%v, want 0,nil", b, err)
	}
	a := New(root)
	if _, err := a.ArchiveBars(context.Background(), md.TF1d, []md.Bar{{SymbolID: 1, TF: md.TF1d, Ts: 0, Close: 1}}, map[int64]string{1: "X"}); err != nil {
		t.Fatal(err)
	}
	b, err := DirSize(root)
	if err != nil || b <= 0 {
		t.Fatalf("DirSize after write: b=%d err=%v, want >0", b, err)
	}
}

// Dir precedence: SIGNALDECK_ARCHIVE_DIR overrides, else <db-dir>/archive.
func TestDirPrecedence(t *testing.T) {
	t.Setenv("SIGNALDECK_ARCHIVE_DIR", "/custom/arc")
	if got := Dir("/data/x.db"); got != "/custom/arc" {
		t.Fatalf("env override: got %q", got)
	}
	t.Setenv("SIGNALDECK_ARCHIVE_DIR", "")
	if got := Dir("/data/sub/x.db"); got != "/data/sub/archive" {
		t.Fatalf("db-dir default: got %q", got)
	}
}
