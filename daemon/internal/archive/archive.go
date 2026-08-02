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
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// DefaultDir is the last-resort archive root, used only when
// SIGNALDECK_ARCHIVE_DIR is unset AND no dbPath is known — Dir otherwise puts
// the archive beside whatever database the daemon actually opened.
//
// This was an absolute path under /Users/natalienyaung, which on any other
// machine named a directory that does not exist and cannot be created, so the
// fallback silently wrote nowhere. Relative to the working directory it lands
// in the repo's own data/archive, which is what the comment always claimed.
const DefaultDir = "data/archive"

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
var anomalyHeader = []string{"id", "symbol_id", "symbol", "ts", "kind", "z", "detail"}

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

// ArchiveAnomalies appends anomalies rows to cold storage, one file per
// symbol_id under archive/anomalies/. Same fail-safe contract as ArchiveBars:
// err != nil ⇒ caller must NOT prune.
func (a *Archiver) ArchiveAnomalies(ctx context.Context, rows []store.AnomalyRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.AnomalyRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, ans := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		// Stored Symbol (from the read's JOIN) wins for the CSV column; the
		// filename fallback keeps archiving from ever blocking on a lookup.
		lo, hi := tsSpanAnomalies(ans)
		path, err := a.open("anomalies", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(anomalyHeader); err != nil {
				return err
			}
			sort.Slice(ans, func(i, j int) bool {
				if ans[i].Ts != ans[j].Ts {
					return ans[i].Ts < ans[j].Ts
				}
				return ans[i].ID < ans[j].ID
			})
			for _, r := range ans {
				sym := r.Symbol
				if sym == "" {
					sym = name
				}
				rec := []string{
					strconv.FormatInt(r.ID, 10),
					strconv.FormatInt(r.SymbolID, 10),
					sym,
					strconv.FormatInt(r.Ts, 10),
					r.Kind,
					f(r.Z),
					r.Detail,
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive anomalies %s: %w", name, err)
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

func tsSpanAnomalies(a []store.AnomalyRow) (lo, hi int64) {
	lo, hi = a[0].Ts, a[0].Ts
	for _, x := range a {
		if x.Ts < lo {
			lo = x.Ts
		}
		if x.Ts > hi {
			hi = x.Ts
		}
	}
	return
}

// ─────────────────────────────────────────────────────────────────────────
// DERIVED-TABLE ARCHIVE (appended block — tiered-storage wave, phase 2). The
// high-volume derived tables (scores / score_outcomes / features) grow
// unbounded; maintain.DerivedRetention exports every row here to gzip-CSV
// BEFORE it prunes, with the identical fail-safe contract as ArchiveBars:
// err != nil ⇒ NOTHING was durably committed for the failing group, so the
// caller must NOT prune. One file per (table, symbol, time-range).
// ─────────────────────────────────────────────────────────────────────────

var scoreHeader = []string{"symbol_id", "symbol", "horizon", "ts", "score", "components"}
var scoreOutcomeHeader = []string{"symbol_id", "symbol", "horizon", "ts", "score", "fwd_return", "resolved_at"}
var featureHeader = []string{"id", "symbol_id", "symbol", "horizon", "ts", "version", "vec"}

// ArchiveScores appends scores rows to cold storage, one file per symbol_id
// under archive/scores/. Same fail-safe contract as ArchiveBars.
func (a *Archiver) ArchiveScores(ctx context.Context, rows []store.ScoreRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.ScoreRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := tsSpanScores(rs)
		path, err := a.open("scores", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(scoreHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool { return rs[i].Ts < rs[j].Ts })
			for _, r := range rs {
				rec := []string{
					strconv.FormatInt(r.SymbolID, 10), name, r.Horizon,
					strconv.FormatInt(r.Ts, 10), f(r.Score), r.Components,
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive scores %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

// ArchiveScoreOutcomes appends score_outcomes rows to cold storage, one file per
// symbol_id under archive/score_outcomes/. Same fail-safe contract as
// ArchiveBars. Null fwd_return/resolved_at are written as empty CSV fields.
func (a *Archiver) ArchiveScoreOutcomes(ctx context.Context, rows []store.ScoreOutcomeRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.ScoreOutcomeRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := tsSpanScoreOutcomes(rs)
		path, err := a.open("score_outcomes", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(scoreOutcomeHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool { return rs[i].Ts < rs[j].Ts })
			for _, r := range rs {
				fwd := ""
				if r.FwdReturn.Valid {
					fwd = f(r.FwdReturn.Float64)
				}
				res := ""
				if r.ResolvedAt.Valid {
					res = strconv.FormatInt(r.ResolvedAt.Int64, 10)
				}
				rec := []string{
					strconv.FormatInt(r.SymbolID, 10), name, r.Horizon,
					strconv.FormatInt(r.Ts, 10), f(r.Score), fwd, res,
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive score_outcomes %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

// ArchiveFeatures appends features rows to cold storage, one file per symbol_id
// under archive/features/. Same fail-safe contract as ArchiveBars. The raw JSON
// vec is carried verbatim so the archived training set round-trips exactly.
func (a *Archiver) ArchiveFeatures(ctx context.Context, rows []store.FeatureArchiveRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.FeatureArchiveRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := tsSpanFeatures(rs)
		path, err := a.open("features", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(featureHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool {
				if rs[i].Ts != rs[j].Ts {
					return rs[i].Ts < rs[j].Ts
				}
				return rs[i].ID < rs[j].ID
			})
			for _, r := range rs {
				rec := []string{
					strconv.FormatInt(r.ID, 10), strconv.FormatInt(r.SymbolID, 10), name,
					r.Horizon, strconv.FormatInt(r.Ts, 10), strconv.Itoa(r.Version), r.Vec,
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive features %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

func tsSpanScores(rs []store.ScoreRow) (lo, hi int64) {
	lo, hi = rs[0].Ts, rs[0].Ts
	for _, x := range rs {
		if x.Ts < lo {
			lo = x.Ts
		}
		if x.Ts > hi {
			hi = x.Ts
		}
	}
	return
}

func tsSpanScoreOutcomes(rs []store.ScoreOutcomeRow) (lo, hi int64) {
	lo, hi = rs[0].Ts, rs[0].Ts
	for _, x := range rs {
		if x.Ts < lo {
			lo = x.Ts
		}
		if x.Ts > hi {
			hi = x.Ts
		}
	}
	return
}

// ─── UNMANAGED-TABLE SWEEP (appended block — tiered-storage wave, phase 3) ───
// Cold sinks for the reaudit's unmanaged tables: filings, insights,
// prediction_postmortems and research_weeks. Identical fail-safe contract as
// ArchiveBars: err != nil ⇒ NOTHING durably committed for the failing group,
// so the caller must NOT prune. One file per (table, symbol, time-range);
// market-scope insights (no symbol) group under the "market" file.

var filingHeader = []string{"id", "symbol_id", "symbol", "form", "filed_ts", "title", "url", "label"}
var insightHeader = []string{"id", "scope", "symbol_id", "symbol", "ts", "headline", "body", "data"}
var postmortemHeader = []string{"symbol_id", "symbol", "horizon", "ts", "prob", "up", "fwd_return",
	"conviction", "magnitude", "primary_reason", "secondary_reason", "reasons", "created_at"}
var researchWeekHeader = []string{"symbol_id", "symbol", "week", "ts", "vec", "fwd_return", "up",
	"era", "high_vol", "created_at"}

// ArchiveFilings appends filings rows to cold storage under archive/filings/.
// The accession id + EDGAR url are carried so every archived row stays a
// working pointer to the primary source.
func (a *Archiver) ArchiveFilings(ctx context.Context, rows []store.FilingArchiveRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.FilingArchiveRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := rs[0].FiledTs, rs[0].FiledTs
		for _, r := range rs {
			if r.FiledTs < lo {
				lo = r.FiledTs
			}
			if r.FiledTs > hi {
				hi = r.FiledTs
			}
		}
		path, err := a.open("filings", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(filingHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool {
				if rs[i].FiledTs != rs[j].FiledTs {
					return rs[i].FiledTs < rs[j].FiledTs
				}
				return rs[i].ID < rs[j].ID
			})
			for _, r := range rs {
				rec := []string{
					r.ID, strconv.FormatInt(r.SymbolID, 10), name, r.Form,
					strconv.FormatInt(r.FiledTs, 10), r.Title, r.URL, r.Label,
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive filings %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

// ArchiveInsights appends insights rows to cold storage under
// archive/insights/. Market-scope rows (NULL symbol_id) file as "market".
func (a *Archiver) ArchiveInsights(ctx context.Context, rows []store.InsightArchiveRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.InsightArchiveRow{}
	for _, r := range rows {
		sid := int64(0) // market scope groups under 0 → "market"
		if r.SymbolID.Valid {
			sid = r.SymbolID.Int64
		}
		bySym[sid] = append(bySym[sid], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := "market"
		if sid != 0 {
			name = symName(symbolName, sid)
		}
		lo, hi := rs[0].Ts, rs[0].Ts
		for _, r := range rs {
			if r.Ts < lo {
				lo = r.Ts
			}
			if r.Ts > hi {
				hi = r.Ts
			}
		}
		path, err := a.open("insights", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(insightHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool {
				if rs[i].Ts != rs[j].Ts {
					return rs[i].Ts < rs[j].Ts
				}
				return rs[i].ID < rs[j].ID
			})
			for _, r := range rs {
				symID := ""
				if r.SymbolID.Valid {
					symID = strconv.FormatInt(r.SymbolID.Int64, 10)
				}
				rec := []string{
					strconv.FormatInt(r.ID, 10), r.Scope, symID, name,
					strconv.FormatInt(r.Ts, 10), r.Headline, r.Body, r.Data,
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive insights %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

// ArchivePostmortems appends prediction_postmortems rows to cold storage
// under archive/prediction_postmortems/. The full ranked reasons JSON is
// carried verbatim so archived failure history round-trips exactly.
func (a *Archiver) ArchivePostmortems(ctx context.Context, rows []store.PostmortemArchiveRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.PostmortemArchiveRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := rs[0].Ts, rs[0].Ts
		for _, r := range rs {
			if r.Ts < lo {
				lo = r.Ts
			}
			if r.Ts > hi {
				hi = r.Ts
			}
		}
		path, err := a.open("prediction_postmortems", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(postmortemHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool {
				if rs[i].Ts != rs[j].Ts {
					return rs[i].Ts < rs[j].Ts
				}
				return rs[i].Horizon < rs[j].Horizon
			})
			for _, r := range rs {
				rec := []string{
					strconv.FormatInt(r.SymbolID, 10), name, r.Horizon,
					strconv.FormatInt(r.Ts, 10), f(r.Prob), strconv.Itoa(r.Up), f(r.FwdReturn),
					f(r.Conviction), f(r.Magnitude), r.PrimaryReason, r.SecondaryReason,
					r.Reasons, strconv.FormatInt(r.CreatedAt, 10),
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive postmortems %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

// ArchiveResearchWeeks appends research_weeks rows to cold storage under
// archive/research_weeks/. The vec JSON is carried verbatim so the archived
// evidence base round-trips exactly into offline research.
func (a *Archiver) ArchiveResearchWeeks(ctx context.Context, rows []store.ResearchWeekArchiveRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.ResearchWeekArchiveRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := rs[0].Ts, rs[0].Ts
		for _, r := range rs {
			if r.Ts < lo {
				lo = r.Ts
			}
			if r.Ts > hi {
				hi = r.Ts
			}
		}
		path, err := a.open("research_weeks", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(researchWeekHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool { return rs[i].Ts < rs[j].Ts })
			for _, r := range rs {
				rec := []string{
					strconv.FormatInt(r.SymbolID, 10), name,
					strconv.FormatInt(r.Week, 10), strconv.FormatInt(r.Ts, 10), r.Vec,
					f(r.FwdReturn), strconv.Itoa(r.Up), r.Era, strconv.Itoa(r.HighVol),
					strconv.FormatInt(r.CreatedAt, 10),
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive research_weeks %s: %w", name, err)
		}
		files++
	}
	return files, nil
}

func tsSpanFeatures(rs []store.FeatureArchiveRow) (lo, hi int64) {
	lo, hi = rs[0].Ts, rs[0].Ts
	for _, x := range rs {
		if x.Ts < lo {
			lo = x.Ts
		}
		if x.Ts > hi {
			hi = x.Ts
		}
	}
	return
}

// ═══ COMPOSITE_SCORES COLD ARCHIVE (scores-compactor wave, appended) ═════════

var compositeHeader = []string{"symbol_id", "symbol", "ts", "horizon", "score", "curve_pct", "edge", "payload"}

// ArchiveComposite cold-stores full composite_scores rows (factor payload JSON)
// before the compactor strips or downsamples them. Same fail-safe contract as
// ArchiveScores: err != nil ⇒ nothing durably committed for the failing group.
func (a *Archiver) ArchiveComposite(ctx context.Context, rows []store.CompositeArchiveRow, symbolName map[int64]string) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	bySym := map[int64][]store.CompositeArchiveRow{}
	for _, r := range rows {
		bySym[r.SymbolID] = append(bySym[r.SymbolID], r)
	}
	files := 0
	for sid, rs := range bySym {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		name := symName(symbolName, sid)
		lo, hi := rs[0].Ts, rs[0].Ts
		for _, r := range rs {
			if r.Ts < lo {
				lo = r.Ts
			}
			if r.Ts > hi {
				hi = r.Ts
			}
		}
		path, err := a.open("composite_scores", name, lo, hi)
		if err != nil {
			return files, err
		}
		wf := func(cw *csv.Writer) error {
			if err := cw.Write(compositeHeader); err != nil {
				return err
			}
			sort.Slice(rs, func(i, j int) bool {
				if rs[i].Ts != rs[j].Ts {
					return rs[i].Ts < rs[j].Ts
				}
				return rs[i].Horizon < rs[j].Horizon
			})
			for _, r := range rs {
				rec := []string{
					strconv.FormatInt(r.SymbolID, 10),
					name,
					strconv.FormatInt(r.Ts, 10),
					r.Horizon,
					strconv.FormatInt(r.Score, 10),
					f(r.CurvePct),
					f(r.Edge),
					r.Payload,
				}
				if err := cw.Write(rec); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeGzCSV(path, wf); err != nil {
			return files, fmt.Errorf("archive composite %s: %w", name, err)
		}
		files++
	}
	return files, nil
}
