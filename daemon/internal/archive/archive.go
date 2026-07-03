// Package archive is SignalDeck's COLD ARCHIVE: before any hot-store row is
// pruned for retention, it is exported here to a compressed gzip-CSV file so
// the data is never truly deleted — only moved off the fast SQLite path into
// cheap, append-only cold storage that research tools (DuckDB, pandas) can
// read directly.
//
// Design constraints (all satisfied with the standard library only — NO new
// dependencies):
//   - compress/gzip + encoding/csv → files are ordinary gzipped CSV with a
//     header row and typed columns, so `duckdb> SELECT * FROM
//     read_csv_auto('*.csv.gz')` and `pandas.read_csv(path)` both just work.
//   - One file per (table, symbol, time-range): archive/<table>/<symbol>_
//     <fromTs>-<toTs>.csv.gz. Ranges are the actual min/max ts of the rows
//     written, so filenames are self-describing and never collide.
//   - Fail-safe: writes go to a .tmp sibling first and are only os.Rename'd
//     into place on full success (rename is atomic on the same filesystem), so
//     a crash mid-write can never leave a truncated file that a later prune
//     would trust. The caller MUST treat any error as "do NOT prune".
//
// The archive is grow-only; nothing here ever deletes an archive file.
package archive

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// DefaultDir is the archive root used when SIGNALDECK_ARCHIVE_DIR is unset. It
// sits next to the live database (…/signaldeck/data/archive).
const DefaultDir = "/Users/natalienyaung/claude code/signaldeck/data/archive"

// Dir resolves the archive root: SIGNALDECK_ARCHIVE_DIR if set, else the
// data-directory default derived from dbPath (…/<dbdir>/archive), else the
// packaged DefaultDir. dbPath is the live database path so the archive always
// lands beside whatever DB the daemon actually opened (including temp DBs in
// tests, which pass their own dir).
func Dir(dbPath string) string {
	if v := os.Getenv("SIGNALDECK_ARCHIVE_DIR"); v != "" {
		return v
	}
	if dbPath != "" {
		return filepath.Join(filepath.Dir(dbPath), "archive")
	}
	return DefaultDir
}

// Archiver writes rows to the cold archive rooted at Root.
type Archiver struct {
	Root string
}

// New builds an Archiver rooted at dir.
func New(dir string) *Archiver { return &Archiver{Root: dir} }

// barHeader / snapHeader are the typed CSV column orders. Keeping them explicit
// (not reflected) means the on-disk schema is stable and reviewable, and the
// column types are documented for DuckDB/pandas readers.
var barHeader = []string{"symbol_id", "symbol", "tf", "ts", "open", "high", "low", "close", "volume"}
var snapHeader = []string{"symbol_id", "symbol", "ts", "bid", "ask", "mid", "wmid", "imb_signed", "spread", "apply_lat_ns"}

// ArchiveBars appends the given bars to cold storage, one file per symbol_id,
// named <symbol>_<minTs>-<maxTs>.csv.gz under archive/bars_<tf>/. It returns
// the number of files written. Any error means NOTHING was durably committed
// for the failing group (temp files are left for cleanup, never renamed in),
// so the caller must NOT prune when err != nil.
//
// symbolName maps symbol_id → display symbol for the filename + a CSV column;
// unknown ids fall back to "sym<id>" so archiving never blocks on a lookup.
func (a *Archiver) ArchiveBars(ctx context.Context, tf md.Timeframe, rows []md.Bar, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	sub := "bars_" + string(tf)
	// Group by symbol so each file is one symbol's slice (research-friendly).
	bySym := map[int64][]md.Bar{}
	for _, b := range rows {
		bySym[b.SymbolID] = append(bySym[b.SymbolID], b)
	}
	files := 0
	for sid, brs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := tsSpanBars(brs)
		path, err := a.open(sub, name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(barHeader); err != nil {
				return err
			}
			// Deterministic order (ts asc) so re-runs and reads are stable.
			sort.Slice(brs, func(i, j int) bool { return brs[i].Ts < brs[j].Ts })
			for _, b := range brs {
				rec := []string{
					strconv.FormatInt(b.SymbolID, 10),
					name,
					string(tf),
					strconv.FormatInt(b.Ts, 10),
					f(b.Open), f(b.High), f(b.Low), f(b.Close), f(b.Volume),
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive bars %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

// ArchiveSnapshots appends snapshots_1s rows to cold storage, one file per
// symbol_id under archive/snapshots_1s/. Same fail-safe contract as
// ArchiveBars: err != nil ⇒ caller must NOT prune.
func (a *Archiver) ArchiveSnapshots(ctx context.Context, rows []md.Snap1s, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]md.Snap1s{}
	for _, s := range rows {
		bySym[s.SymbolID] = append(bySym[s.SymbolID], s)
	}
	files := 0
	for sid, sns := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := tsSpanSnaps(sns)
		path, err := a.open("snapshots_1s", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(snapHeader); err != nil {
				return err
			}
			sort.Slice(sns, func(i, j int) bool { return sns[i].Ts < sns[j].Ts })
			for _, s := range sns {
				rec := []string{
					strconv.FormatInt(s.SymbolID, 10),
					name,
					strconv.FormatInt(s.Ts, 10),
					f(s.Bid), f(s.Ask), f(s.Mid), f(s.WMid), f(s.ImbSigned), f(s.Spread),
					strconv.FormatInt(s.ApplyLatNs, 10),
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive snapshots %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

// open ensures archive/<sub>/ exists and returns the final file path for the
// (name, span). A filesystem-safe symbol replaces '/' (BTC/USD → BTC-USD).
func (a *Archiver) open(sub, name string, lo, hi int64) (string, error) {
	dir := filepath.Join(a.Root, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	fn := fmt.Sprintf("%s_%d-%d.csv.gz", safeName(name), lo, hi)
	return filepath.Join(dir, fn), nil
}

// writeGzCSV writes a gzip-CSV file atomically: it fills a .tmp sibling, fsyncs
// through Close, and only then renames it into place. On ANY error the tmp file
// is removed and the final path is untouched — so a partial write can never be
// mistaken for a committed archive (the prune fail-safe depends on this).
func writeGzCSV(path string, write func(*csv.Writer) error) (err error) {
	tmp := path + ".tmp"
	fp, err := os.Create(tmp)
	if err != nil {
		return err
	}
	// Guard: on any failure, close+remove the tmp and surface the error.
	committed := false
	defer func() {
		if !committed {
			_ = fp.Close()
			_ = os.Remove(tmp)
		}
	}()
	gz := gzip.NewWriter(fp)
	cw := csv.NewWriter(gz)
	if err := write(cw); err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := fp.Sync(); err != nil {
		return err
	}
	if err := fp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// DirSize walks the archive root and returns the total bytes of all files (for
// the storage report). A missing root is 0 bytes, not an error.
func DirSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return nil
			}
			return werr
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}

// ── helpers ──────────────────────────────────────────────────────────────

// f formats a float without scientific notation and without a trailing ".0"
// noise — 'g' with -1 precision round-trips exactly and stays readable.
func f(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func symName(m map[int64]string, id int64) string {
	if m != nil {
		if s := m[id]; s != "" {
			return s
		}
	}
	return "sym" + strconv.FormatInt(id, 10)
}

// safeName makes a symbol filesystem-safe (BTC/USD → BTC-USD).
func safeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '/', '\\', ':', ' ':
			out = append(out, '-')
		default:
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

func tsSpanBars(b []md.Bar) (lo, hi int64) {
	lo, hi = b[0].Ts, b[0].Ts
	for _, x := range b {
		if x.Ts < lo {
			lo = x.Ts
		}
		if x.Ts > hi {
			hi = x.Ts
		}
	}
	return
}

func tsSpanSnaps(s []md.Snap1s) (lo, hi int64) {
	lo, hi = s[0].Ts, s[0].Ts
	for _, x := range s {
		if x.Ts < lo {
			lo = x.Ts
		}
		if x.Ts > hi {
			hi = x.Ts
		}
	}
	return
}
