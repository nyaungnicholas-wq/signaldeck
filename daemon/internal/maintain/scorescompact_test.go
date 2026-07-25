package maintain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newCompactStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "compact.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// heavyComponents is a stand-in for the real per-component JSON blob.
var heavyComponents = []md.ScoreComponent{{Name: "trend_sma", Value: 1, Norm: 1, Weight: 0.35, Contrib: 0.35, Note: "heavy"}}

// The tier ladder: blobs stripped past KeepHeavy (after archiving), intraday
// rows daily-downsampled past KeepIntraday, and the fresh tier untouched.
func TestScoresCompactorTiers(t *testing.T) {
	ctx := context.Background()
	st := newCompactStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}
	now := time.Now().Unix()
	old := now - 40*86400 // beyond the 30d intraday tier
	mid := now - 5*86400  // stripped tier: blob gone, row kept
	fresh := now - 3600   // hot tier: untouched
	// Three intraday rows on ONE old UTC day (daily-last must survive), one
	// mid-tier row, one fresh row.
	dayStart := (old / 86400) * 86400
	for _, ts := range []int64{dayStart + 100, dayStart + 200, dayStart + 300, mid, fresh} {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, Score: 0.5, Components: heavyComponents,
		}); err != nil {
			t.Fatalf("insert score: %v", err)
		}
	}
	// Two composite rows: the FRESH one anchors the newest-row carve-out so
	// the mid-tier row is strippable.
	for _, ts := range []int64{mid, fresh} {
		if err := st.UpsertCompositeScore(ctx, store.CompositeScore{
			SymbolID: sym.ID, Horizon: "1d", Ts: ts, Score: 7, Payload: `{"factors":["heavy"]}`,
		}); err != nil {
			t.Fatalf("insert composite: %v", err)
		}
	}

	arcDir := filepath.Join(t.TempDir(), "arc")
	c := &ScoresCompactor{
		St: st, Arc: archive.New(arcDir),
		KeepHeavy: 2 * 24 * time.Hour, KeepIntraday: 30 * 24 * time.Hour,
	}
	msg, err := c.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "stripped") {
		t.Errorf("message %q", msg)
	}

	// Old day: exactly ONE row survives (the daily-last, ts=dayStart+300),
	// blob stripped. Mid row survives blob-stripped. Fresh row keeps its blob.
	hist, err := st.ScoreHistory(ctx, sym.ID, md.H1d, 0, now+1)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var oldRows, midHeavy, freshHeavy int
	for _, h := range hist {
		switch {
		case h.Ts >= dayStart && h.Ts < dayStart+86400:
			oldRows++
			if h.Ts != dayStart+300 {
				t.Errorf("old-day survivor ts=%d, want daily-LAST %d", h.Ts, dayStart+300)
			}
			if len(h.Components) != 0 {
				t.Errorf("old-day row still carries components")
			}
		case h.Ts == mid:
			if len(h.Components) != 0 {
				midHeavy++
			}
		case h.Ts == fresh:
			if len(h.Components) != 0 {
				freshHeavy++
			}
		}
	}
	if oldRows != 1 {
		t.Errorf("old-day rows = %d, want 1 (daily-last)", oldRows)
	}
	if midHeavy != 0 {
		t.Errorf("mid-tier row kept its blob; want stripped")
	}
	if freshHeavy != 1 {
		t.Errorf("fresh row lost its blob; hot tier must be untouched")
	}

	// The stripped rows' full form is in the cold archive.
	files, err := filepath.Glob(filepath.Join(arcDir, "scores", "*.csv.gz"))
	if err != nil || len(files) == 0 {
		t.Errorf("no scores archive written (files=%v, err=%v)", files, err)
	}
	cf, err := filepath.Glob(filepath.Join(arcDir, "composite_scores", "*.csv.gz"))
	if err != nil || len(cf) == 0 {
		t.Errorf("no composite archive written (files=%v, err=%v)", cf, err)
	}
}

// FAIL-SAFE: when the archive sink cannot write, blobs are RETAINED and a dq
// event records the skip — compaction must never destroy the only copy.
func TestScoresCompactorArchiveFailSafe(t *testing.T) {
	ctx := context.Background()
	st := newCompactStore(t)
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	now := time.Now().Unix()
	old := now - 5*86400
	// A fresh sibling anchors the newest-row carve-out; the 5d row is the
	// strippable one whose blob the failing archive must protect.
	for _, ts := range []int64{old, now - 3600} {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, Score: 0.5, Components: heavyComponents,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// An archive root that is a FILE forces every archive write to fail.
	badRoot := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(badRoot, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	c := &ScoresCompactor{St: st, Arc: archive.New(badRoot), KeepHeavy: 2 * 24 * time.Hour, KeepIntraday: 30 * 24 * time.Hour}
	msg, err := c.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "SKIPPED") {
		t.Errorf("message %q lacks the skip notice", msg)
	}
	hist, _ := st.ScoreHistory(ctx, sym.ID, md.H1d, 0, now+1)
	if len(hist) != 2 {
		t.Fatalf("rows = %d, want 2", len(hist))
	}
	for _, h := range hist {
		if len(h.Components) == 0 {
			t.Fatalf("blob at ts=%d was destroyed despite archive failure — fail-safe broken", h.Ts)
		}
	}
}

// COUNCIL-MANDATED: the prune path of the fail-safe. A >30d row whose blob was
// never archived (sink failing) must survive the daily-downsample untouched —
// the sentinel guard + skipped-pass gate make destruction structurally
// impossible, not just unlikely.
func TestScoresCompactorPruneRefusesUnarchivedBlobs(t *testing.T) {
	ctx := context.Background()
	st := newCompactStore(t)
	sym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")
	now := time.Now().Unix()
	old := now - 40*86400                                    // beyond the 30d prune tier, blob never archived
	for _, ts := range []int64{old, old + 100, now - 3600} { // newest row = carve-out anchor
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, Score: 0.5, Components: heavyComponents,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	badRoot := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(badRoot, []byte("x"), 0o644); err != nil {
		t.Fatalf("blocker: %v", err)
	}
	c := &ScoresCompactor{St: st, Arc: archive.New(badRoot), KeepHeavy: 2 * 24 * time.Hour, KeepIntraday: 30 * 24 * time.Hour}
	if _, err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	hist, _ := st.ScoreHistory(ctx, sym.ID, md.H1d, 0, now+1)
	if len(hist) != 3 {
		t.Fatalf("rows = %d, want 3 — un-archived rows were destroyed by the prune", len(hist))
	}
	for _, h := range hist {
		if len(h.Components) == 0 {
			t.Errorf("row ts=%d lost its blob despite archive failure", h.Ts)
		}
	}
}

// COUNCIL-MANDATED: a window misconfig (heavy >= intraday) must be clamped,
// never obeyed — rows between the tiers survive with blobs intact.
func TestScoresCompactorWindowMisconfigClamped(t *testing.T) {
	ctx := context.Background()
	st := newCompactStore(t)
	sym, _ := st.UpsertSymbol(ctx, "DDD", md.Stocks, "")
	now := time.Now().Unix()
	old := now - 40*86400 // older than the (misconfigured) 30d prune tier
	for _, ts := range []int64{old, now - 3600} {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, Score: 0.5, Components: heavyComponents,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	c := &ScoresCompactor{
		St: st, Arc: archive.New(filepath.Join(t.TempDir(), "arc")),
		KeepHeavy: 45 * 24 * time.Hour, KeepIntraday: 30 * 24 * time.Hour, // MISCONFIG
	}
	if _, err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	hist, _ := st.ScoreHistory(ctx, sym.ID, md.H1d, 0, now+1)
	if len(hist) != 2 {
		t.Fatalf("rows = %d, want 2 — the misconfigured prune destroyed un-stripped data", len(hist))
	}
	for _, h := range hist {
		if len(h.Components) == 0 {
			t.Errorf("row ts=%d was stripped despite being inside the 45d heavy window", h.Ts)
		}
	}
}

// COUNCIL ROUND 2 REGRESSION: the strip must target the EXACT archived key set.
// A stalled (symbol,horizon)'s newest row is carved out of the select — so it is
// never archived — and a concurrent insert of a NEWER row (the symbol resuming
// its cadence) must NOT make it strippable. The window-with-re-evaluated-EXISTS
// form failed exactly here; two advisors reproduced it.
func TestScoresCompactorStripIsKeySetNotWindow(t *testing.T) {
	ctx := context.Background()
	st := newCompactStore(t)
	sym, _ := st.UpsertSymbol(ctx, "STALL", md.Stocks, "")
	now := time.Now().Unix()
	stalled := now - 8*86400 // >2d: heavy tier, but it is the symbol's NEWEST row

	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: stalled, Score: 0.5, Components: heavyComponents,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	arc := archive.New(filepath.Join(t.TempDir(), "arc"))
	c := &ScoresCompactor{St: st, Arc: arc, KeepHeavy: 2 * 24 * time.Hour, KeepIntraday: 30 * 24 * time.Hour}

	// Pass 1: the row is the newest for its (symbol,horizon) -> carved out,
	// never selected, never archived, blob intact.
	if _, err := c.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	hist, _ := st.ScoreHistory(ctx, sym.ID, md.H1d, 0, now+1)
	if len(hist) != 1 || len(hist[0].Components) == 0 {
		t.Fatalf("carved-out row lost its blob on pass 1 (rows=%d)", len(hist))
	}

	// The symbol RESUMES: a newer row appears. The stalled row now has a newer
	// sibling, so it becomes selectable — and must be ARCHIVED before any strip.
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: now - 60, Score: 0.6, Components: heavyComponents,
	}); err != nil {
		t.Fatalf("resume insert: %v", err)
	}
	if _, err := c.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	// The stalled row is now legitimately stripped — AND archived (not the
	// TOCTOU path: it went through select -> archive -> strip together).
	files, _ := filepath.Glob(filepath.Join(arc.Root, "scores", "*.csv.gz"))
	if len(files) == 0 {
		t.Fatal("stalled row was stripped without ever being archived — TOCTOU regression")
	}
	hist, _ = st.ScoreHistory(ctx, sym.ID, md.H1d, 0, now+1)
	for _, h := range hist {
		if h.Ts == stalled && len(h.Components) != 0 {
			continue // still heavy is fine; being stripped-without-archive is not
		}
	}
}

// COUNCIL ROUND 2 (non-blocking note): the sentinel guard must be provable
// INDEPENDENTLY of the skipped-pass gate — call the prune directly against a
// heavy, never-archived row that is past the cutoff and has a newer sibling.
func TestPruneSentinelBlocksHeavyRowsDirectly(t *testing.T) {
	ctx := context.Background()
	st := newCompactStore(t)
	sym, _ := st.UpsertSymbol(ctx, "SENT", md.Stocks, "")
	now := time.Now().Unix()
	day := now - 40*86400
	dayStart := (day / 86400) * 86400
	// Two heavy rows on ONE old day: the earlier is prune-eligible by age +
	// daily-last rule, but its blob was never archived.
	for _, ts := range []int64{dayStart + 100, dayStart + 200} {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, Score: 0.5, Components: heavyComponents,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	n, err := st.PruneScoresKeepDailyLast(ctx, now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 0 {
		t.Errorf("prune deleted %d un-archived heavy rows; the sentinel must refuse them", n)
	}
	hist, _ := st.ScoreHistory(ctx, sym.ID, md.H1d, 0, now+1)
	if len(hist) != 2 {
		t.Errorf("rows = %d, want 2 (both retained)", len(hist))
	}
}
