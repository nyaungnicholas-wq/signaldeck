package maintain

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// The regression this locks: US daily bars open at ~05:00 UTC (midnight ET),
// NOT 00:00 UTC. A score stamped between 00:00 UTC and the bar's timestamp
// must still resolve to a ONE-trading-day return. The earlier arithmetic
// target (p.Ts/86400)*86400 + 86400 picked the bar two days out and recorded
// a ~2-day return as a 1d outcome, corrupting the Honesty page.
func TestOutcomeResolverBaseAlignedNotUTCMidnight(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Three consecutive daily bars, each opening at 05:00 UTC, 5+ days ago so
	// the 1d window is fully mature relative to time.Now().
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(-6 * 24 * time.Hour).Add(5 * time.Hour).Unix()
	bars := []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: base, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: base + 86400, Open: 110, High: 110, Low: 110, Close: 110},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: base + 2*86400, Open: 121, High: 121, Low: 121, Close: 121},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	// Score stamped 3h BEFORE the middle bar's timestamp — i.e. in the
	// [00:00, 05:00) UTC window on that calendar day. Its base bar is the
	// FIRST bar (100); one trading day forward is the middle bar (110).
	scoreTs := base + 86400 - 3*3600
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: scoreTs, Score: 0.5,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	outcomes, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].FwdReturn == nil {
		t.Fatalf("want 1 resolved outcome with a return, got %+v", outcomes)
	}
	// Correct 1-day return is 110/100-1 = +0.10. The old 2-day bug produced
	// 121/100-1 = +0.21.
	if got := *outcomes[0].FwdReturn; got < 0.099 || got > 0.101 {
		t.Fatalf("fwd return = %.4f, want ~0.10 (1 trading day); ~0.21 would be the 2-day regression", got)
	}
}

// Per-horizon resolution must not let the large immature 1w backlog starve a
// freshly-mature 1d outcome (they no longer share one ts-ordered queue).
func TestOutcomeResolverPerHorizonNoStarve(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	d0 := now.Add(-4 * 24 * time.Hour).Unix()
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: d0, Close: 100},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: d0 + 86400, Close: 105},
		// The SETTLING bar. The resolver refuses to grade against a forward bar
		// that could still be forming, and its test for that is "a later bar
		// exists". Without this the forward bar never settles and the fixture
		// resolves nothing — failing this test for a reason unrelated to the
		// per-horizon starvation it exists to check.
		{SymbolID: sym.ID, TF: md.TF1d, Ts: d0 + 2*86400, Close: 106},
	}); err != nil {
		t.Fatal(err)
	}

	// One mature 1d score (3 days old) plus many young 1w scores whose ts are
	// OLDER (they would sit ahead in a single ts-ordered queue).
	if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1d, Ts: d0 + 3600, Score: 0.2}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		ts := now.Add(-time.Duration(i) * time.Minute).Unix() // recent → immature for 1w
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1w, Ts: ts, Score: 0.1}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("mature 1d outcome should resolve despite the 1w backlog; got %d", len(got))
	}
}

// ── storage-permanence wave: compaction, not deletion ───────────────────

// Minute bars past retention must be rolled into hourly bars BEFORE they are
// pruned — pruning without a surviving rollup would be data loss.
func TestDownsamplerCompactsMinutesBeforePruning(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Two full hours of 1m bars ~100 days old (well past the 90d default),
	// aligned to hour boundaries so expectations are exact.
	old := ((time.Now().Add(-100 * 24 * time.Hour).Unix()) / 3600) * 3600
	var bars []md.Bar
	for m := int64(0); m < 120; m++ {
		ts := old + m*60
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1m, Ts: ts,
			Open: float64(100 + m), High: float64(105 + m), Low: float64(95 + m),
			Close: float64(101 + m), Volume: 2,
		})
	}
	// A daily bar in the same ancient range: must survive no matter what.
	bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: old, Open: 1, High: 1, Low: 1, Close: 1})
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	if _, err := (&Downsampler{St: st, Arc: archive.New(t.TempDir())}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	// 1m bars past retention are gone…
	mins, err := st.Bars(ctx, sym.ID, md.TF1m, 0, old+7200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mins) != 0 {
		t.Fatalf("old 1m bars must be pruned, %d remain", len(mins))
	}
	// …but ONLY because their hourly rollup now exists (compaction).
	hours, err := st.Bars(ctx, sym.ID, md.TF1h, old, old+7200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 2 {
		t.Fatalf("want 2 hourly rollup bars covering the pruned minutes, got %d", len(hours))
	}
	// OHLCV of hour 0: first open, max high, min low, last close, sum volume.
	h0 := hours[0]
	if h0.Open != 100 || h0.High != 105+59 || h0.Low != 95 || h0.Close != 101+59 || h0.Volume != 120 {
		t.Fatalf("hour-0 rollup wrong: %+v", h0)
	}
	// Daily bars are NEVER pruned.
	daily, err := st.Bars(ctx, sym.ID, md.TF1d, 0, old+86400, 0)
	if err != nil || len(daily) != 1 {
		t.Fatalf("daily bar must survive retention: err=%v n=%d", err, len(daily))
	}
}

// A pre-existing (source-backfilled) hourly bar in the compacted range is
// authoritative: compaction must fill only the MISSING hours, not overwrite.
func TestDownsamplerCompactionKeepsAuthoritativeHourlyBars(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	old := ((time.Now().Add(-100 * 24 * time.Hour).Unix()) / 3600) * 3600
	if err := st.UpsertBars(ctx, []md.Bar{
		// Source 1h bar for the hour…
		{SymbolID: sym.ID, TF: md.TF1h, Ts: old, Open: 500, High: 500, Low: 500, Close: 500, Volume: 999},
		// …plus a lone 1m bar inside it (a partial-coverage trap: aggregating
		// it would produce a WRONG hourly bar).
		{SymbolID: sym.ID, TF: md.TF1m, Ts: old + 60, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&Downsampler{St: st, Arc: archive.New(t.TempDir())}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	hours, err := st.Bars(ctx, sym.ID, md.TF1h, old, old+3600, 0)
	if err != nil || len(hours) != 1 {
		t.Fatalf("want the 1 hourly bar: err=%v n=%d", err, len(hours))
	}
	if hours[0].Close != 500 || hours[0].Volume != 999 {
		t.Fatalf("compaction overwrote an authoritative source 1h bar: %+v", hours[0])
	}
	mins, err := st.Bars(ctx, sym.ID, md.TF1m, 0, old+3600, 0)
	if err != nil || len(mins) != 0 {
		t.Fatalf("old 1m bar should still be pruned: err=%v n=%d", err, len(mins))
	}
}

// ── tiered-storage wave: archive-before-prune + fail-safe + daily-forever ──

// countArchiveFiles counts *.csv.gz files under an archive root's subdir.
func countArchiveFiles(t *testing.T, root, sub string) int {
	t.Helper()
	dir := filepath.Join(root, sub)
	n := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(p) == ".gz" {
			n++
		}
		return nil
	})
	return n
}

// Snapshots pass a SHORT hot window (6h default), so old ones must be archived
// to gzip-CSV and then pruned; recent ones stay. The pruned rows must be
// readable back from the archive (round-trip through the retention path).
func TestDownsamplerTieredSnapshotArchiveThenPrune(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	root := t.TempDir()
	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldTs := now.Add(-10 * time.Hour).Unix() // past the 6h default → archive+prune
	newTs := now.Add(-1 * time.Hour).Unix()  // inside window → keep
	if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: sym.ID, Ts: oldTs, Bid: 1, Ask: 2, Mid: 1.5}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: sym.ID, Ts: newTs, Bid: 3, Ask: 4, Mid: 3.5}); err != nil {
		t.Fatal(err)
	}

	d := &Downsampler{St: st, Arc: archive.New(root)}
	if _, err := d.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Old snap pruned, new snap retained.
	kept, err := st.Snaps(ctx, sym.ID, 0, now.Unix()+1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Ts != newTs {
		t.Fatalf("want only the in-window snap kept, got %+v", kept)
	}
	// The old snap survives in cold storage.
	if got := countArchiveFiles(t, root, "snapshots_1s"); got != 1 {
		t.Fatalf("want 1 snapshot archive file, got %d", got)
	}
	// Verify the archived row's value round-trips.
	var found bool
	_ = filepath.Walk(filepath.Join(root, "snapshots_1s"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(p) != ".gz" {
			return nil
		}
		fp, _ := os.Open(p)
		defer func() { _ = fp.Close() }()
		gz, _ := gzip.NewReader(fp)
		defer func() { _ = gz.Close() }()
		recs, _ := csv.NewReader(gz).ReadAll()
		for _, r := range recs[1:] { // skip header
			if r[2] == itoa(oldTs) {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Fatalf("archived old snap ts=%d not found in cold storage", oldTs)
	}
}

// Explicit cutoff selection: with an explicit KeepSnaps window, a snap exactly
// inside it stays and one just outside is archived+pruned — the cutoff is
// picked correctly.
func TestDownsamplerRetentionPicksRightCutoff(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	root := t.TempDir()
	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	inside := now.Add(-30 * time.Minute).Unix()
	outside := now.Add(-90 * time.Minute).Unix()
	for _, ts := range []int64{inside, outside} {
		if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: sym.ID, Ts: ts, Mid: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// Keep only the last hour.
	d := &Downsampler{St: st, Arc: archive.New(root), KeepSnaps: time.Hour,
		Keep1m: 60 * 24 * time.Hour, Keep1h: 3 * 365 * 24 * time.Hour}
	if _, err := d.Run(ctx); err != nil {
		t.Fatal(err)
	}
	kept, err := st.Snaps(ctx, sym.ID, 0, now.Unix()+1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Ts != inside {
		t.Fatalf("cutoff wrong: want only ts=%d kept, got %+v", inside, kept)
	}
}

// Daily bars are NEVER archived and NEVER pruned, no matter how old.
func TestDownsamplerDailyNeverArchivedOrPruned(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	root := t.TempDir()
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	// A 10-year-old daily bar — well past any tier window.
	ancient := time.Now().Add(-10 * 365 * 24 * time.Hour).Unix()
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: ancient, Open: 1, High: 1, Low: 1, Close: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Downsampler{St: st, Arc: archive.New(root)}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	daily, err := st.Bars(ctx, sym.ID, md.TF1d, 0, time.Now().Unix(), 0)
	if err != nil || len(daily) != 1 {
		t.Fatalf("daily bar must survive forever: err=%v n=%d", err, len(daily))
	}
	// No bars_1d archive directory should ever be created.
	if _, err := os.Stat(filepath.Join(root, "bars_1d")); !os.IsNotExist(err) {
		t.Fatalf("daily bars must never be archived (no bars_1d dir), stat err=%v", err)
	}
}

// FAIL-SAFE: when the archive write fails, the matching prune is SKIPPED — the
// rows stay in the hot store (never lost silently) and a dq event records it.
func TestDownsamplerFailSafeSkipsPruneOnArchiveError(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Hour).Unix()
	if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: sym.ID, Ts: old, Mid: 1}); err != nil {
		t.Fatal(err)
	}
	// An anomaly past its retention window too — the anomalies tier must obey
	// the SAME fail-safe (no durable archive ⇒ no prune).
	oldAnomTs := time.Now().Add(-100 * 24 * time.Hour).Unix()
	if _, err := st.InsertAnomaly(ctx, store.AnomalyRow{
		SymbolID: sym.ID, Ts: oldAnomTs, Kind: "anomaly_vol", Z: 3.0, Detail: "old",
	}); err != nil {
		t.Fatal(err)
	}

	// Make the archive root un-creatable: a regular FILE where the root dir
	// (and its subdirs) would need to be — MkdirAll then fails, so ArchiveX
	// errors and the prune must be skipped.
	badRootParent := filepath.Join(t.TempDir(), "block")
	if err := os.WriteFile(badRootParent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	badRoot := filepath.Join(badRootParent, "archive") // parent is a file

	d := &Downsampler{St: st, Arc: archive.New(badRoot)}
	msg, err := d.Run(ctx)
	if err != nil {
		t.Fatalf("Run must not hard-error on archive failure (fail-safe): %v", err)
	}

	// Data must still be present (NOT pruned).
	kept, err := st.Snaps(ctx, sym.ID, 0, time.Now().Unix()+1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatalf("fail-safe violated: old snap was pruned despite archive failure (kept=%d)", len(kept))
	}
	keptAnoms, err := st.Anomalies(ctx, sym.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(keptAnoms) != 1 {
		t.Fatalf("fail-safe violated: old anomaly was pruned despite archive failure (kept=%d)", len(keptAnoms))
	}
	// A dq event must record the skip.
	dq, err := st.RecentDQ(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sawSkip bool
	for _, e := range dq {
		if e.Kind == "archive_skip" {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatalf("expected an archive_skip dq event; run msg=%q dq=%+v", msg, dq)
	}
}

// Multi-batch boundary: with a tiny batch size and rows spanning MANY
// timestamps, every pruned minute must appear in the cold archive — no row is
// deleted before it is archived at a batch edge. The archived-row count must
// equal the pruned-row count exactly (conservation of data).
func TestDownsamplerArchiveConservesRowsAcrossBatches(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	root := t.TempDir()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	// 250 one-minute bars, all ~100 days old (past the 60d default), each a
	// distinct ts. With batch=7 this forces ~36 batches with real edges.
	old := ((time.Now().Add(-100 * 24 * time.Hour).Unix()) / 3600) * 3600
	var bars []md.Bar
	for m := int64(0); m < 250; m++ {
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1m, Ts: old + m*60, Open: 1, High: 1, Low: 1, Close: float64(m), Volume: 1})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	orig := archiveBatch
	archiveBatch = 7
	defer func() { archiveBatch = orig }()

	if _, err := (&Downsampler{St: st, Arc: archive.New(root)}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	// All 250 1m bars pruned from the hot store.
	remaining, err := st.Bars(ctx, sym.ID, md.TF1m, 0, old+250*60, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("all old 1m bars should be pruned, %d remain", len(remaining))
	}

	// Count archived rows across ALL bars_1m files; must equal 250 (no loss,
	// no duplication at batch edges).
	total := 0
	seen := map[string]bool{}
	_ = filepath.Walk(filepath.Join(root, "bars_1m"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(p) != ".gz" {
			return nil
		}
		fp, _ := os.Open(p)
		defer func() { _ = fp.Close() }()
		gz, _ := gzip.NewReader(fp)
		defer func() { _ = gz.Close() }()
		recs, _ := csv.NewReader(gz).ReadAll()
		for _, r := range recs[1:] { // skip header
			total++
			seen[r[3]] = true // ts column
		}
		return nil
	})
	if total != 250 {
		t.Fatalf("archived row count = %d, want 250 (conservation across batches)", total)
	}
	if len(seen) != 250 {
		t.Fatalf("distinct archived timestamps = %d, want 250 (no dup/loss at edges)", len(seen))
	}
}

// A nil archive sink must also fail safe: prune nothing, flag a dq event.
func TestDownsamplerNilArchiveFailsSafe(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Hour).Unix()
	if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: sym.ID, Ts: old, Mid: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Downsampler{St: st /* Arc nil */}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	kept, _ := st.Snaps(ctx, sym.ID, 0, time.Now().Unix()+1, 0)
	if len(kept) != 1 {
		t.Fatalf("nil archive must not prune (kept=%d)", len(kept))
	}
}

// StorageGovernor checkpoints the WAL without error on a live temp DB and
// reports sizes; VACUUM stays gated off under the (huge) default threshold.
func TestStorageGovernorCheckpoints(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	// Write something so there's a WAL to checkpoint.
	sym, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	_ = st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1m, Ts: 60, Close: 1}})

	g := &StorageGovernor{St: st}
	msg, err := g.Run(ctx)
	if err != nil {
		t.Fatalf("governor run: %v", err)
	}
	if msg == "" {
		t.Fatalf("governor should report status")
	}
}

// makeFreePages leaves a large freelist behind: insert ballast, drop it, then
// checkpoint so the freed pages land in the main file.
//
// A VACUUM can only return FREE pages, so a test that wants the governor to
// actually vacuum has to create some. A freshly opened store has almost no
// freelist and is now correctly SKIPPED — see
// TestStorageGovernorSkipsVacuumWhenNothingToReclaim.
func makeFreePages(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	db := st.DB()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS vacuum_ballast (id INTEGER PRIMARY KEY, blob BLOB)`); err != nil {
		t.Fatalf("create vacuum_ballast table: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO vacuum_ballast (blob) VALUES (?)`)
	if err != nil {
		t.Fatalf("prepare insert: %v", err)
	}
	blob := []byte(strings.Repeat("x", 1024))
	for i := 0; i < 4000; i++ {
		if _, err := stmt.ExecContext(ctx, blob); err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("close stmt: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE vacuum_ballast`); err != nil {
		t.Fatalf("drop vacuum_ballast table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("wal checkpoint: %v", err)
	}
}

// The governor rewrites a file that has almost nothing to reclaim: 5.1 GB held
// for 32 minutes on 2026-09-10 to return 6.8 MB, because the off-hours bypass
// was keyed on FILE SIZE. Reclaimable space is the only number that authorises
// the stall, so a fresh store must be skipped and must say why.
func TestStorageGovernorSkipsVacuumWhenNothingToReclaim(t *testing.T) {
	ctx := context.Background()
	st := openStore(t) // fresh store: essentially no freelist
	g := &StorageGovernor{St: st, VacuumThreshold: 1, MinVacuumInterval: time.Hour}
	msg, err := g.Run(ctx)
	if err != nil {
		t.Fatalf("governor run: %v", err)
	}
	if !contains(msg, "vacuumed=false") {
		t.Fatalf("a rewrite that would reclaim almost nothing must be skipped, msg=%q", msg)
	}
	var count int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dq_events WHERE kind='vacuum_skip'`).Scan(&count); err != nil {
		t.Fatalf("query dq_events: %v", err)
	}
	if count == 0 {
		t.Errorf("expected a vacuum_skip dq event explaining the refusal")
	}
}

// StorageGovernor VACUUMs when the DB exceeds the threshold, then records the
// meta cursor so it won't re-vacuum within MinVacuumInterval.
//
// The fixture has to create real free pages now: reclaimable space, not file
// size, is what authorises the rewrite.
func TestStorageGovernorVacuumsAboveThreshold(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	makeFreePages(t, st)
	g := &StorageGovernor{St: st, VacuumThreshold: 1, MinVacuumInterval: time.Hour} // 1 byte → always over
	msg, err := g.Run(ctx)
	if err != nil {
		t.Fatalf("governor run: %v", err)
	}
	if !contains(msg, "vacuumed=true") {
		t.Fatalf("expected a vacuum above threshold, msg=%q", msg)
	}
	if v, _ := st.GetMeta(ctx, "storage_last_vacuum"); v == "" {
		t.Fatalf("vacuum cursor should be recorded")
	}
	// Second run within the interval must NOT vacuum again.
	msg2, err := g.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg2, "vacuumed=false") {
		t.Fatalf("second run within interval should skip vacuum, msg=%q", msg2)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// The anomalies tier: detections older than the hot window (default ~90d) are
// archived to cold gzip-CSV and pruned; in-window detections stay; the
// archived row round-trips. Same archive-before-prune fail-safe as bars/snaps
// (exercised for anomalies in TestDownsamplerFailSafeSkipsPruneOnArchiveError).
func TestDownsamplerAnomaliesTierArchiveThenPrune(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	root := t.TempDir()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldTs := now.Add(-100 * 24 * time.Hour).Unix() // past the 90d default → archive+prune
	newTs := now.Add(-1 * 24 * time.Hour).Unix()   // inside window → keep
	if _, err := st.InsertAnomaly(ctx, store.AnomalyRow{
		SymbolID: sym.ID, Ts: oldTs, Kind: "anomaly_vol", Z: 3.2, Detail: "old detection",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertAnomaly(ctx, store.AnomalyRow{
		SymbolID: sym.ID, Ts: newTs, Kind: "anomaly_volume", Z: 2.8, Detail: "fresh detection",
	}); err != nil {
		t.Fatal(err)
	}

	d := &Downsampler{St: st, Arc: archive.New(root)}
	if _, err := d.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Old anomaly pruned, fresh one retained.
	kept, err := st.Anomalies(ctx, sym.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Ts != newTs {
		t.Fatalf("want only the in-window anomaly kept, got %+v", kept)
	}
	// The old anomaly survives in cold storage, values intact.
	if got := countArchiveFiles(t, root, "anomalies"); got != 1 {
		t.Fatalf("want 1 anomalies archive file, got %d", got)
	}
	var found bool
	_ = filepath.Walk(filepath.Join(root, "anomalies"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(p) != ".gz" {
			return nil
		}
		fp, _ := os.Open(p)
		defer func() { _ = fp.Close() }()
		gz, _ := gzip.NewReader(fp)
		defer func() { _ = gz.Close() }()
		recs, _ := csv.NewReader(gz).ReadAll()
		for _, r := range recs[1:] { // id,symbol_id,symbol,ts,kind,z,detail
			if r[3] == itoa(oldTs) && r[4] == "anomaly_vol" && r[2] == "AAPL" && r[6] == "old detection" {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Fatalf("archived old anomaly ts=%d not found in cold storage", oldTs)
	}

	// Explicit window override: a shorter KeepAnoms prunes the fresh one too.
	if _, err := (&Downsampler{St: st, Arc: archive.New(root), KeepAnoms: time.Hour}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	kept2, _ := st.Anomalies(ctx, sym.ID, "", 10)
	if len(kept2) != 0 {
		t.Fatalf("KeepAnoms=1h must prune the 1d-old anomaly, kept %+v", kept2)
	}
	if got := countArchiveFiles(t, root, "anomalies"); got != 2 {
		t.Fatalf("second prune must add a second archive file, got %d", got)
	}
}

// fakeQuiescer stands in for the worker Runner: it records that the window was
// requested and runs the work inside it.
type fakeQuiescer struct {
	calls  int
	except []string
}

func (q *fakeQuiescer) QuiesceDo(ctx context.Context, d time.Duration, fn func(context.Context), except ...string) error {
	q.calls++
	q.except = except
	if fn != nil {
		fn(ctx)
	}
	return nil
}

// The checkpoint ladder must walk PASSIVE → RESTART → TRUNCATE and REPORT which
// rungs ran with the frames each moved. The old single-rung design attempted
// only TRUNCATE, which requires a reader-free instant the fleet never yields:
// measured 0 successes in 22 passes while the WAL reached 5,396MB.
func TestStorageGovernorCheckpointLadder(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	for i := 0; i < 50; i++ {
		_ = st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1m, Ts: int64(60 * i), Close: float64(i)}})
	}

	// CLOSED market, pinned. The TRUNCATE rung is gated on marketcal.OpenForBars,
	// so with the real clock this test reached the rung it exists to cover only
	// outside trading hours and asserted nothing during them. A Saturday is the
	// simplest instant the calendar calls closed for every venue.
	closed := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC) // Saturday
	q := &fakeQuiescer{}
	g := &StorageGovernor{St: st, Quiescer: q, Now: func() time.Time { return closed }}
	msg, err := g.Run(ctx)
	if err != nil {
		t.Fatalf("governor run: %v", err)
	}
	for _, want := range []string{"PASSIVE", "frames"} {
		if !contains(msg, want) {
			t.Fatalf("ladder report %q missing %q", msg, want)
		}
	}
	// The report names BOTH TRUNCATE outcomes with the same word — "TRUNCATE
	// n/m frames" when it ran, "TRUNCATE deferred (market hours)" when the gate
	// held it back. Testing for the bare substring conflated them, so during
	// market hours this read a deferral as a run and demanded a quiesce window
	// that had correctly never opened. Discriminate, do not soften.
	deferred := contains(msg, "TRUNCATE deferred")
	ran := contains(msg, "TRUNCATE") && !deferred
	if deferred {
		t.Fatalf("market pinned CLOSED yet TRUNCATE was deferred: %q", msg)
	}
	// PASSIVE may empty the WAL outright on a quiet temp DB, in which case the
	// upper rungs are correctly skipped; otherwise TRUNCATE must have run inside
	// the quiesce window.
	if ran && q.calls != 1 {
		t.Errorf("TRUNCATE ran with %d quiesce windows, want exactly 1", q.calls)
	}
	if q.calls > 0 && (len(q.except) != 1 || q.except[0] != g.Name()) {
		t.Errorf("quiesce except=%v, want the governor itself (else it waits on its own run)", q.except)
	}
}

// The other half of the gate, which nothing covered: during market hours with a
// WAL under the alert threshold, TRUNCATE must be DEFERRED and the fleet must
// NOT be held still. Holding a 3s quiesce window for a rung that then does not
// run is a pure availability cost, and it is the failure the bare-substring
// assertion would have hidden in either direction.
func TestStorageGovernorDefersTruncateDuringMarketHours(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	for i := 0; i < 50; i++ {
		_ = st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1m, Ts: int64(60 * i), Close: float64(i)}})
	}

	// Wednesday 15:00 UTC = 11:00 ET, unambiguously inside the session.
	open := time.Date(2026, 8, 5, 15, 0, 0, 0, time.UTC)
	q := &fakeQuiescer{}
	g := &StorageGovernor{St: st, Quiescer: q, Now: func() time.Time { return open }}
	msg, err := g.Run(ctx)
	if err != nil {
		t.Fatalf("governor run: %v", err)
	}
	// A WAL that PASSIVE empties outright returns before the gate is consulted;
	// that is a legitimate early exit and not what this test is about.
	if contains(msg, "wal empty") {
		t.Skip("PASSIVE emptied the WAL before the market-hours gate was reached")
	}
	if !contains(msg, "TRUNCATE deferred") {
		t.Errorf("market pinned OPEN with a small WAL, want TRUNCATE deferred, got %q", msg)
	}
	if q.calls != 0 {
		t.Errorf("deferred TRUNCATE opened %d quiesce window(s) — the fleet must not be "+
			"held still for a rung that does not run", q.calls)
	}
	// The lower rungs are the entire point of the ladder: they run regardless.
	if !contains(msg, "PASSIVE") {
		t.Errorf("PASSIVE must run even when TRUNCATE is deferred, got %q", msg)
	}
}

// After walIneffectiveRuns consecutive passes that reclaim ZERO frames while the
// WAL GROWS, the governor must raise its own dq event — the "the mechanism does
// nothing" state that went unobserved for 22 straight passes, distinct from the
// existing size-threshold wal_checkpoint_busy alert.
func TestStorageGovernorFlagsIneffectiveCheckpoints(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	g := &StorageGovernor{St: st}

	var wal int64
	for i := 0; i < walIneffectiveRuns; i++ {
		wal += 1 << 20 // WAL grew, nothing reclaimed
		g.trackEffectiveness(ctx, 0, wal)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM dq_events WHERE kind='wal_checkpoint_ineffective'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("wal_checkpoint_ineffective events = %d, want 1 after %d dead passes", n, walIneffectiveRuns)
	}
	// A pass that actually reclaims frames resets the streak.
	g.trackEffectiveness(ctx, 5, wal+1<<20)
	if v, _ := st.GetMeta(ctx, "storage_wal_ineffective_runs"); v != "0" {
		t.Errorf("streak = %q after a productive pass, want reset to 0", v)
	}
}

// A held read snapshot is what makes TRUNCATE return Busy, so this test
// manufactures one and releases it mid-run to prove the retry works. The
// measured justification is that against the live fleet 25 single attempts one
// second apart won exactly once, so a single-shot pass loses ~96 percent of the
// time.
func TestStorageGovernorRetriesBlockedTruncate(t *testing.T) {
	// SLOW BY NECESSITY, not by sloppiness. The store opens its connections with
	// busy_timeout(15000), so a blocked checkpoint does not return Busy for a
	// full 15 seconds - it sits in SQLite's busy handler instead. The pin must
	// therefore outlast that timeout, or the very first attempt simply waits the
	// reader out and succeeds, which is precisely what the first two versions of
	// this test measured: RESTART 840/840, no BUSY, one attempt, nothing proven.
	if testing.Short() {
		t.Skip("holds a read snapshot past the 15s busy_timeout")
	}
	ctx := context.Background()
	st := openStore(t)
	sym, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	for i := 0; i < 25; i++ {
		_ = st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1m, Ts: int64(60 * i), Close: float64(i)}})
	}

	// PIN A SNAPSHOT, THEN WRITE PAST IT. Order is the whole mechanism. A reader
	// that opens AFTER the last write holds the newest snapshot and blocks
	// nothing - measured: the first version of this test did exactly that and
	// RESTART sailed through 840/840 frames. The checkpoint can only be denied
	// its reset by a reader pinned to an OLDER frame, so the transaction takes
	// its snapshot here (a constant `SELECT 1` would not even acquire the read
	// lock - it must touch real pages) and the next 25 bars land beyond it.
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM bars`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	for i := 25; i < 50; i++ {
		_ = st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1m, Ts: int64(60 * i), Close: float64(i)}})
	}

	// Release the pin mid-run so a LATER attempt is the one that wins. Against
	// the live fleet 25 single attempts one second apart won exactly once, so a
	// single-shot pass loses ~96% of the time; this is that rare instant,
	// manufactured on purpose.
	t.Setenv("SIGNALDECK_WAL_TRUNCATE_RETRY_SEC", "45")
	go func() {
		time.Sleep(18 * time.Second)
		_ = tx.Rollback()
	}()

	// Saturday: the market-hours gate must not defer the rung under test.
	closed := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	g := &StorageGovernor{St: st, Quiescer: &fakeQuiescer{}, Now: func() time.Time { return closed }}
	msg, err := g.Run(ctx)
	if err != nil {
		t.Fatalf("governor run: %v", err)
	}
	if !contains(msg, "attempts") {
		t.Fatalf("TRUNCATE was pinned BUSY yet only one attempt ran - the retry did not happen: %q", msg)
	}
	// Assert on TRUNCATE's OWN verdict, not the bare word BUSY: the RESTART rung
	// legitimately reports BUSY here (it is blocked by the same pin), and an
	// assertion that cannot tell the two rungs apart fails on a pass that did
	// exactly what it should. "WAL NOT truncated" is only ever written by the
	// rung under test.
	if contains(msg, "WAL NOT truncated") {
		t.Fatalf("the pin was released mid-run, so a later TRUNCATE should have won: %q", msg)
	}
	if !contains(msg, "TRUNCATE") || contains(msg, "TRUNCATE deferred") {
		t.Fatalf("expected TRUNCATE to run, got %q", msg)
	}
}

// The pressure branch needs a 128 MB WAL, which is too expensive for a unit
// test, so we cover the cheaply reachable branches instead: the default base
// interval, the nil-store guard, the base override, the guard that a longer
// pressure interval can never win, and the pressure-disabled case.
func TestStorageGovernorIntervalRespondsToPressure(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		g := &StorageGovernor{St: openStore(t)}
		got := g.Interval()
		if got != 60*time.Minute {
			t.Fatalf("Interval() = %v, want 60m", got)
		}
	})
	t.Run("nil store", func(t *testing.T) {
		g := &StorageGovernor{}
		got := g.Interval()
		if got != 60*time.Minute {
			t.Fatalf("Interval() = %v, want 60m", got)
		}
	})
	t.Run("base override", func(t *testing.T) {
		t.Setenv("SIGNALDECK_WAL_CHECKPOINT_MIN", "5")
		g := &StorageGovernor{St: openStore(t)}
		got := g.Interval()
		if got != 5*time.Minute {
			t.Fatalf("Interval() = %v, want 5m", got)
		}
	})
	t.Run("pressure never exceeds base", func(t *testing.T) {
		t.Setenv("SIGNALDECK_WAL_CHECKPOINT_MIN", "5")
		t.Setenv("SIGNALDECK_WAL_PRESSURE_MIN", "30")
		g := &StorageGovernor{St: openStore(t)}
		got := g.Interval()
		if got != 5*time.Minute {
			t.Fatalf("Interval() = %v, want 5m", got)
		}
	})
	t.Run("pressure disabled", func(t *testing.T) {
		t.Setenv("SIGNALDECK_WAL_PRESSURE_MIN", "0")
		g := &StorageGovernor{St: openStore(t)}
		got := g.Interval()
		if got != 60*time.Minute {
			t.Fatalf("Interval() = %v, want 60m", got)
		}
	})
}
