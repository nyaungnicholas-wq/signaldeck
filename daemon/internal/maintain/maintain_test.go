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
		defer fp.Close()
		gz, _ := gzip.NewReader(fp)
		defer gz.Close()
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
		defer fp.Close()
		gz, _ := gzip.NewReader(fp)
		defer gz.Close()
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

// StorageGovernor VACUUMs when the DB exceeds the threshold, then records the
// meta cursor so it won't re-vacuum within MinVacuumInterval.
func TestStorageGovernorVacuumsAboveThreshold(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
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
		defer fp.Close()
		gz, _ := gzip.NewReader(fp)
		defer gz.Close()
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
