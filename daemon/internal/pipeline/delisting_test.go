package pipeline

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newDelistStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "delist.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// fleet builds n stock symbols whose newest daily bar is `staleDays` old, plus
// any explicitly-specified stragglers.
func seedFleet(t *testing.T, st *store.Store, now time.Time, healthy int, stale map[string]int) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	ids := map[string]int64{}
	add := func(sym string, daysOld int) {
		s, err := st.UpsertSymbol(ctx, sym, md.Stocks, sym+" Inc")
		if err != nil {
			t.Fatalf("UpsertSymbol %s: %v", sym, err)
		}
		ids[sym] = s.ID
		ts := now.AddDate(0, 0, -daysOld).Unix()
		ts -= ts % 86400
		if err := st.UpsertBars(ctx, []md.Bar{{
			SymbolID: s.ID, TF: md.TF1d, Ts: ts,
			Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000,
		}}); err != nil {
			t.Fatalf("UpsertBars %s: %v", sym, err)
		}
	}
	for i := 0; i < healthy; i++ {
		add(fmt.Sprintf("LIVE%03d", i), 1)
	}
	for sym, days := range stale {
		add(sym, days)
	}
	return ids
}

// The headline guarantee: a symbol that stopped printing while the fleet kept
// printing is marked, and the recorded date is its LAST BAR — not today.
func TestDelistedIsDatedAtTheLastBarNotDetectionTime(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ids := seedFleet(t, st, now, 60, map[string]int{"DEADCO": 120})

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, err := st.StockLastBars(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.SymbolID != ids["DEADCO"] {
			if r.DelistedAt != 0 {
				t.Fatalf("healthy symbol %s was marked delisted (detail: %s)", r.Symbol, detail)
			}
			continue
		}
		found = true
		if r.DelistedAt == 0 {
			t.Fatalf("DEADCO should be marked delisted (detail: %s)", detail)
		}
		if r.DelistedAt != r.LastTs {
			t.Fatalf("delisted_at = %d, want the last bar %d — dating it 'now' would hide a "+
				"tradable name from every point-in-time universe in between", r.DelistedAt, r.LastTs)
		}
	}
	if !found {
		t.Fatal("DEADCO missing from StockLastBars")
	}
}

// THE guard. Our own ingestion failing looks exactly like the whole market
// delisting at once. If most of the fleet is stale, the fault is ours and
// NOTHING may be marked — otherwise one outage permanently corrupts every
// point-in-time universe built afterwards.
func TestBrokenIngestionMarksNothing(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	// Whole fleet stale — as if the poller had been dead for months.
	stale := map[string]int{}
	for i := 0; i < 80; i++ {
		stale[fmt.Sprintf("OLD%03d", i)] = 200
	}
	seedFleet(t, st, now, 0, stale)

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, _ := st.StockLastBars(ctx)
	for _, r := range rows {
		if r.DelistedAt != 0 {
			t.Fatalf("%s marked delisted during an ingestion outage — the guard failed (detail: %s)",
				r.Symbol, detail)
		}
	}
}

// A warehouse of already-marked dead names is not a broken pipeline. The bulk
// delisted-symbol import made that the normal state — 1,869 imported corpses
// against ~1,050 live names — and because the guard counted every row, raw
// liveness fell to 36% and the detector marked NOTHING for three days while
// ingestion was perfectly healthy. Liveness is judged over the not-yet-delisted
// population for exactly this reason.
func TestImportedDeadNamesDoNotDisableTheDetector(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	// 60 live names plus one straggler that stopped while they kept printing.
	ids := seedFleet(t, st, now, 60, map[string]int{"GONE": 200})
	// 200 already-recorded-dead names, silent for years: the imported cohort.
	dead := map[string]int{}
	for i := 0; i < 200; i++ {
		dead[fmt.Sprintf("DEAD%03d", i)] = 900
	}
	for sym, id := range seedFleet(t, st, now, 0, dead) {
		if err := st.MarkDelisted(ctx, id, now.AddDate(0, 0, -900).Unix()); err != nil {
			t.Fatalf("MarkDelisted %s: %v", sym, err)
		}
	}
	// Over all rows liveness is 60/261 = 23%, well under the 60% floor. Over the
	// not-yet-delisted fleet it is 60/61 = 98%, which is the truth.

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, _ := st.StockLastBars(ctx)
	var deadStillMarked int
	for _, r := range rows {
		if r.SymbolID == ids["GONE"] && r.DelistedAt == 0 {
			t.Fatalf("GONE stopped printing while the fleet kept printing but was not marked — "+
				"imported dead names dragged the liveness guard down (detail: %s)", detail)
		}
		if strings.HasPrefix(r.Symbol, "DEAD") {
			if r.DelistedAt == 0 {
				t.Fatalf("%s lost its delisting marker; a silent dead name must stay marked", r.Symbol)
			}
			deadStillMarked++
		}
	}
	if deadStillMarked != 200 {
		t.Fatalf("expected all 200 imported dead names to stay marked, got %d", deadStillMarked)
	}
}

// The guard must still fire on the failure it exists for. Same shape as the
// test above — a mostly-silent fleet — but the silent names are NOT marked
// dead, which is what an ingestion outage actually looks like.
func TestUnmarkedSilentFleetStillTripsTheGuard(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	stale := map[string]int{}
	for i := 0; i < 200; i++ {
		stale[fmt.Sprintf("SILENT%03d", i)] = 900
	}
	seedFleet(t, st, now, 60, stale) // 60/260 = 23% live, none marked

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, _ := st.StockLastBars(ctx)
	for _, r := range rows {
		if r.DelistedAt != 0 {
			t.Fatalf("%s marked during an ingestion outage — excluding delisted rows from the "+
				"denominator must not weaken the guard (detail: %s)", r.Symbol, detail)
		}
	}
}

// Delisting must be REVERSIBLE. A halted name that resumes printing was never
// delisted, and a one-way marker turns every false positive into a permanent
// market "fact".
func TestResumedSymbolIsUnmarked(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	ids := seedFleet(t, st, now, 60, map[string]int{"HALTED": 120})

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// It comes back: a fresh bar arrives.
	ts := now.AddDate(0, 0, -1).Unix()
	ts -= ts % 86400
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: ids["HALTED"], TF: md.TF1d, Ts: ts,
		Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.StockLastBars(ctx)
	for _, r := range rows {
		if r.SymbolID == ids["HALTED"] && r.DelistedAt != 0 {
			t.Fatal("a symbol that resumed printing must have its delisting marker cleared")
		}
	}
}

// A symbol with NO bars at all is a backfill/subscription state, not evidence of
// delisting. Guessing here would mark every newly-added ticker dead.
func TestSymbolWithNoBarsIsNeverMarked(t *testing.T) {
	st := newDelistStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	seedFleet(t, st, now, 60, nil)
	fresh, err := st.UpsertSymbol(ctx, "BRANDNEW", md.Stocks, "Brand New Inc")
	if err != nil {
		t.Fatal(err)
	}

	w := &DelistingDetector{St: st, Now: func() time.Time { return now }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.StockLastBars(ctx)
	for _, r := range rows {
		if r.SymbolID == fresh.ID && r.DelistedAt != 0 {
			t.Fatal("a symbol with no bars yet must not be marked delisted")
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────
// THE DEAD-CONTROL TEST.
//
// This file's header records the finding that motivated the survivorship
// worker: "the code documented a control that did not run". The same defect
// recurred one level up — PREDICTION_PROCESS.md listed
// `store.ResearchUniverse` / `TradableAt` under "Implemented, with proof"
// while neither had a single non-test caller, so the documented survivorship
// control was inert and the document read as evidence for it anyway.
//
// A prose fix would have decayed the same way. This turns the repo's own
// stated lesson into a checkable invariant: every Go identifier the gate
// section names must be reachable from production code, or the build fails.
//
// Scope is deliberately conservative — a name is only checked once it is
// confirmed to be DECLARED in the Go tree, so ordinary prose in backticks
// (paths, file names, verdict strings, commit hashes) is ignored rather than
// guessed at. It under-checks rather than failing on English.

// docIdentRe matches the backticked spans the gate section uses.
var docIdentRe = regexp.MustCompile("`([^`]+)`")

// goIdentRe accepts `pkg.Ident`, `Ident` and `camelIdent` shapes.
var goIdentRe = regexp.MustCompile(`^(?:[a-z][A-Za-z0-9]*\.)?[A-Za-z][A-Za-z0-9]*$`)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	return root
}

// goSources returns (production sources, test sources) under daemon/.
func goSources(t *testing.T, root string) (prod, tests []string) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(root, "daemon"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			tests = append(tests, p)
		} else {
			prod = append(prod, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk daemon: %v", err)
	}
	return prod, tests
}

// readAll concatenates files, dropping whole-line comments so a MENTION in a
// comment can never be mistaken for a caller — that mistake is the entire bug
// this test exists to catch.
func readAll(t *testing.T, paths []string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestDocumentedControlsHaveNonTestCallers(t *testing.T) {
	root := repoRoot(t)
	doc, err := os.ReadFile(filepath.Join(root, "PREDICTION_PROCESS.md"))
	if err != nil {
		t.Fatalf("read PREDICTION_PROCESS.md: %v", err)
	}
	section := gateSection(t, string(doc))

	prodPaths, testPaths := goSources(t, root)
	prod := readAll(t, prodPaths)
	tests := readAll(t, testPaths)
	all := prod + tests

	checked := 0
	for _, m := range docIdentRe.FindAllStringSubmatch(section, -1) {
		raw := m[1]
		if !goIdentRe.MatchString(raw) {
			continue // a path, a file name, a verdict string — not an identifier claim
		}
		qualified := strings.Contains(raw, ".")
		name := raw
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if len(name) < 4 {
			continue // `go`, `n`, and friends — prose, not a control
		}
		// A test name's "caller" is the test runner; the checkable claim is
		// that the named test exists at all.
		if strings.HasPrefix(name, "Test") {
			if !strings.Contains(tests, "func "+name+"(") {
				t.Errorf("PREDICTION_PROCESS.md cites test %s, which does not exist", name)
			}
			checked++
			continue
		}
		// DECLARED means declared as a callable — `func name(` or a method
		// `) name(`. A local variable that happens to share the name (a JSON
		// key like byRegime colliding with a loop-local in another package) is
		// deliberately NOT a declaration: the gate section talks about
		// controls, and a control is something that can be called.
		declared := strings.Contains(all, "func "+name+"(") || strings.Contains(all, ") "+name+"(")
		if !declared {
			if qualified {
				t.Errorf("PREDICTION_PROCESS.md cites %s under \"Implemented, with proof\", "+
					"but no such function is declared anywhere in the Go tree", raw)
				checked++
			}
			continue // unqualified prose that merely looks like an identifier
		}
		checked++
		// A caller is a USE outside the declaration: any `.name(` selector or a
		// bare `name(` that is not the `func name(` declaration itself.
		uses := strings.Count(prod, "."+name+"(") +
			strings.Count(prod, name+"(") -
			strings.Count(prod, "func "+name+"(") -
			strings.Count(prod, ") "+name+"(")
		if uses <= 0 {
			t.Errorf("PREDICTION_PROCESS.md lists %s under \"Implemented, with proof\", "+
				"but it has no non-test caller — a documented control that does not run "+
				"is worse than an undocumented one, because the document reads as evidence",
				raw)
		}
	}
	if checked == 0 {
		t.Fatal("parsed no identifiers out of the gate section — the parser, not the repo, is broken")
	}
}

// gateSection extracts the "Implemented, with proof" block: the claims that
// assert something RUNS. The sections after it describe known gaps and must
// not be held to the same standard.
func gateSection(t *testing.T, doc string) string {
	t.Helper()
	const head = "### Implemented, with proof"
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatal("PREDICTION_PROCESS.md has no \"Implemented, with proof\" section")
	}
	rest := doc[i+len(head):]
	if j := strings.Index(rest, "\n### "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestCoverageWeekBucketMatchesHistfeat pins store's duplicated week-bucket
// literal to the feature package's constant. The coverage ratio divides a
// corpus count by a bar count bucketed with this literal; if the two ever
// drift the figure silently compares different weeks.
func TestCoverageWeekBucketMatchesHistfeat(t *testing.T) {
	if store.WeekBucketSecs != histfeat.WeekSecs {
		t.Fatalf("week bucket drift: store %d vs histfeat %d — the coverage ratio "+
			"would divide corpus counts by bar counts bucketed differently",
			store.WeekBucketSecs, histfeat.WeekSecs)
	}
}
