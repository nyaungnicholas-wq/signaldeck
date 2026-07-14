package maintain

import (
	"context"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// count runs a scalar COUNT(*) against the store's read DB.
func count(t *testing.T, st *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// seedScore writes a score (which also seeds an unresolved score_outcomes row).
func seedScore(t *testing.T, st *store.Store, sym int64, ts int64) {
	t.Helper()
	if err := st.InsertScore(context.Background(), md.Score{
		SymbolID: sym, Horizon: md.H1d, Ts: ts, Score: 0.3,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestDerivedRetentionArchivesThenPrunes: scores + score_outcomes past the
// window are archived to cold storage AND pruned; recent rows stay; predictions
// + prediction_outcomes (the track record) are NEVER touched.
func TestDerivedRetentionArchivesThenPrunes(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-200 * 24 * time.Hour).Unix() // well past 90d
	recent := now.Add(-3 * 24 * time.Hour).Unix()

	seedScore(t, st, sym.ID, old)
	seedScore(t, st, sym.ID, recent)
	// A prediction + resolved outcome that must SURVIVE regardless of age.
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: old, RawProb: 0.6, CalProb: 0.58, NUsed: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, old, 0.02); err != nil {
		t.Fatal(err)
	}

	arcDir := t.TempDir()
	w := &DerivedRetention{St: st, Arc: archive.New(arcDir)}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v (%s)", err, msg)
	}

	if got := count(t, st, `SELECT COUNT(*) FROM scores`); got != 1 {
		t.Errorf("scores remaining = %d, want 1 (only the recent one)", got)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM score_outcomes`); got != 1 {
		t.Errorf("score_outcomes remaining = %d, want 1", got)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM scores WHERE ts=?`, recent); got != 1 {
		t.Errorf("recent score must survive, got %d", got)
	}
	// The permanence guarantee: the old prediction + its outcome are untouched.
	if got := count(t, st, `SELECT COUNT(*) FROM predictions`); got != 1 {
		t.Errorf("predictions must never be pruned, got %d", got)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM prediction_outcomes WHERE resolved_at IS NOT NULL`); got != 1 {
		t.Errorf("prediction_outcomes must never be pruned, got %d", got)
	}
	// Cold archive received the pruned rows (a scores + a score_outcomes file).
	if n, err := archive.DirSize(arcDir); err != nil || n == 0 {
		t.Errorf("archive dir size = %d err=%v, want durable archive before prune", n, err)
	}
}

// TestDerivedRetentionFeaturesOnlyResolved: features past the window are pruned
// ONLY when their prediction has resolved; an unlabeled feature row survives.
func TestDerivedRetentionFeaturesOnlyResolved(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldResolved := now.Add(-400 * 24 * time.Hour).Unix()  // past 180d, resolved
	oldUnlabeled := now.Add(-300 * 24 * time.Hour).Unix() // past 180d, NOT resolved
	recent := now.Add(-5 * 24 * time.Hour).Unix()         // inside window

	vec := map[string]float64{"pressure_score": 0.4}
	for _, ts := range []int64{oldResolved, oldUnlabeled, recent} {
		if err := st.InsertFeatures(ctx, sym.ID, md.H1d, ts, 3, vec); err != nil {
			t.Fatal(err)
		}
	}
	// Only oldResolved gets a resolved prediction.
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: oldResolved, RawProb: 0.6, CalProb: 0.55, NUsed: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, oldResolved, 0.01); err != nil {
		t.Fatal(err)
	}

	w := &DerivedRetention{St: st, Arc: archive.New(t.TempDir())}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if got := count(t, st, `SELECT COUNT(*) FROM features WHERE ts=?`, oldResolved); got != 0 {
		t.Errorf("resolved old feature must be pruned, got %d", got)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM features WHERE ts=?`, oldUnlabeled); got != 1 {
		t.Errorf("UNLABELED old feature must survive (never delete a training row), got %d", got)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM features WHERE ts=?`, recent); got != 1 {
		t.Errorf("recent feature must survive, got %d", got)
	}
}

// TestDerivedRetentionFailSafe: with no archive sink NOTHING is pruned and a
// dq_events(archive_skip) records the fail-safe — data is never lost silently.
func TestDerivedRetentionFailSafe(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "Tesla")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-200 * 24 * time.Hour).Unix()
	seedScore(t, st, sym.ID, old)

	w := &DerivedRetention{St: st, Arc: nil} // no sink
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM scores`); got != 1 {
		t.Errorf("no archive sink must SKIP the prune, scores=%d want 1", got)
	}
	events, err := st.RecentDQ(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var skips int
	for _, e := range events {
		if e.Kind == "archive_skip" {
			skips++
		}
	}
	if skips == 0 {
		t.Errorf("expected archive_skip dq events for the skipped prunes, got none")
	}
}

// TestDerivedRetentionExplicitWindows: the explicit Keep* fields override the
// env defaults so tests drive exact cutoffs (mirrors the Downsampler).
func TestDerivedRetentionExplicitWindows(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "Nvidia")
	if err != nil {
		t.Fatal(err)
	}
	// 10 days old: inside the 90d default, but OUTSIDE a 7d explicit window.
	ts := time.Now().Add(-10 * 24 * time.Hour).Unix()
	seedScore(t, st, sym.ID, ts)

	w := &DerivedRetention{St: st, Arc: archive.New(t.TempDir()), KeepScores: 7 * 24 * time.Hour}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM scores`); got != 0 {
		t.Errorf("explicit 7d window must prune the 10d-old score, got %d", got)
	}
}

// TestDerivedRetentionMultiBatch drives the trailing-max-ts batch boundary by
// shrinking archiveBatch below the row count.
func TestDerivedRetentionMultiBatch(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AMD", md.Stocks, "AMD")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-200 * 24 * time.Hour).Unix()
	for i := 0; i < 25; i++ {
		seedScore(t, st, sym.ID, base+int64(i)*3600) // distinct ts so trim advances
	}
	old := archiveBatch
	archiveBatch = 10
	defer func() { archiveBatch = old }()

	w := &DerivedRetention{St: st, Arc: archive.New(t.TempDir())}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM scores`); got != 0 {
		t.Errorf("multi-batch prune left %d scores, want 0", got)
	}
}
