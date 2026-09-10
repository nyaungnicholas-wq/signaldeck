package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newRVStore(t *testing.T) (*Store, int64) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "rv.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sym, err := st.UpsertSymbol(context.Background(), "TEST", "stocks", "")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	return st, sym.ID
}

func sampleForecast(symbolID, ts int64) RVForecast {
	return RVForecast{
		SymbolID: symbolID, Ts: ts, Horizon: 1,
		RVHat: 4e-4, NullRW: 5e-4, NullEWMA: 45e-5,
		Beta0: -0.5, BetaD: 0.35, BetaW: 0.35, BetaM: 0.25,
		ResidVar: 0.25, NTrain: 900, Revision: "abc123",
	}
}

// Forecast writes must use the shared writer even while all reader slots are
// occupied. Otherwise they compete with ingestion for SQLite's write lock.
func TestRVWritesDoNotNeedReaderConnection(t *testing.T) {
	st, sid := newRVStore(t)
	st.db.SetMaxOpenConns(1)
	reader, err := st.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	now := time.Now()
	if err := st.UpsertRVForecast(ctx, sampleForecast(sid, 1000), now); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolveRVForecast(ctx, sid, 1000, 1, 0.001, now); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRVForecast(ctx, sampleForecast(sid, 2000), now); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRVUngradable(ctx, sid, 2000, 1, "missing bars", now); err != nil {
		t.Fatal(err)
	}
}

// A forecast without both nulls frozen at call time is not gradable, and the
// write path refuses it rather than trusting a reviewer to notice. A null
// reconstructed after the outcome is known is hindsight -- this repository
// already had to build a quarantine manifest once because that happened.
func TestRVForecastRefusesUnfrozenNulls(t *testing.T) {
	st, sid := newRVStore(t)
	ctx, now := context.Background(), time.Unix(1_700_000_000, 0)

	for _, tc := range []struct {
		name string
		mut  func(*RVForecast)
	}{
		{"no random-walk null", func(f *RVForecast) { f.NullRW = 0 }},
		{"no EWMA null", func(f *RVForecast) { f.NullEWMA = 0 }},
		{"negative null", func(f *RVForecast) { f.NullEWMA = -1 }},
	} {
		f := sampleForecast(sid, 1000)
		tc.mut(&f)
		if err := st.UpsertRVForecast(ctx, f, now); !errors.Is(err, ErrNullNotFrozen) {
			t.Errorf("%s: got %v, want ErrNullNotFrozen", tc.name, err)
		}
	}

	// and an otherwise-incomplete row is refused too
	for _, tc := range []struct {
		name string
		mut  func(*RVForecast)
	}{
		{"no forecast", func(f *RVForecast) { f.RVHat = 0 }},
		{"no horizon", func(f *RVForecast) { f.Horizon = 0 }},
		{"no revision", func(f *RVForecast) { f.Revision = "" }},
	} {
		f := sampleForecast(sid, 1000)
		tc.mut(&f)
		if err := st.UpsertRVForecast(ctx, f, now); err == nil {
			t.Errorf("%s: stored an incomplete row", tc.name)
		}
	}
}

// Re-running the worker must not create a second row, and must NEVER overwrite
// one that has already resolved. A resolved outcome is evidence; replacing it
// would be rewriting the record.
func TestRVForecastUpsertNeverRewritesEvidence(t *testing.T) {
	st, sid := newRVStore(t)
	ctx, now := context.Background(), time.Unix(1_700_000_000, 0)

	f := sampleForecast(sid, 1000)
	if err := st.UpsertRVForecast(ctx, f, now); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// A re-run before resolution must NOT refresh the row either: the forecast
	// is frozen at first write (2026-09-09). Before this, a pass on a forming
	// bar was silently replaced by the next pass, so "frozen" meant "the last
	// value written before resolution".
	f.RVHat = 9e-4
	if err := st.UpsertRVForecast(ctx, f, now); err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}
	open, err := st.OpenRVForecasts(ctx, 1, 10)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("got %d open rows, want exactly 1", len(open))
	}
	if open[0].RVHat != sampleForecast(sid, 1000).RVHat {
		t.Errorf("an unresolved row was rewritten after its freeze: %v", open[0].RVHat)
	}

	// resolve it, then try to overwrite
	if err := st.ResolveRVForecast(ctx, sid, 1000, 1, 5e-4, now); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	f.RVHat = 1e-9
	if err := st.UpsertRVForecast(ctx, f, now); err != nil {
		t.Fatalf("post-resolution upsert errored: %v", err)
	}
	rec, err := st.RVLiveRecord(ctx, 1)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if rec.N != 1 {
		t.Fatalf("resolved count = %d, want 1", rec.N)
	}
	// If the overwrite had landed, rv_hat would be 1e-9 and QLIKE astronomical.
	if rec.MeanQLIKEHAR > 100 {
		t.Errorf("a resolved row was overwritten after the fact (QLIKE %v)", rec.MeanQLIKEHAR)
	}
	if open, _ := st.OpenRVForecasts(ctx, 1, 10); len(open) != 0 {
		t.Errorf("a resolved row is still listed as open")
	}
}

// Abandoning a forecast requires saying why. A silent drop is how the
// unfavourable cases disappear: a forecast is most likely to be unresolvable
// exactly when its symbol's data went bad.
func TestRVUngradableRequiresAReason(t *testing.T) {
	st, sid := newRVStore(t)
	ctx, now := context.Background(), time.Unix(1_700_000_000, 0)
	if err := st.UpsertRVForecast(ctx, sampleForecast(sid, 1000), now); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.MarkRVUngradable(ctx, sid, 1000, 1, "", now); err == nil {
		t.Error("abandoned a forecast with no stated reason")
	}
	if err := st.MarkRVUngradable(ctx, sid, 1000, 1, "bars went stale", now); err != nil {
		t.Fatalf("mark: %v", err)
	}
	rec, _ := st.RVLiveRecord(ctx, 1)
	if rec.Ungradable != 1 {
		t.Errorf("ungradable count = %d, want 1", rec.Ungradable)
	}
	if rec.N != 0 {
		t.Errorf("an ungradable row was counted as graded evidence")
	}
}

// N and DistinctDays are different evidence and must be reported separately.
// Forecasts resolving on one day share a market shock, so the DAY count is the
// number of independent observations. Returning only N invites exactly the
// inflation this platform has retired predictors for.
func TestRVLiveRecordSeparatesRowsFromDays(t *testing.T) {
	st, _ := newRVStore(t)
	ctx, now := context.Background(), time.Unix(1_700_000_000, 0)

	// six forecasts across three symbols but only two calendar days
	day1 := int64(1_700_000_000)
	day2 := day1 + 86400
	for i, sym := range []string{"AAA", "BBB", "CCC"} {
		s, err := st.UpsertSymbol(ctx, sym, "stocks", "")
		if err != nil {
			t.Fatalf("symbol: %v", err)
		}
		for _, ts := range []int64{day1, day2} {
			f := sampleForecast(s.ID, ts)
			f.RVHat = 4e-4 + float64(i)*1e-5
			if err := st.UpsertRVForecast(ctx, f, now); err != nil {
				t.Fatalf("insert: %v", err)
			}
			// NOT 45e-5: that is exactly the EWMA null in the fixture, so its
			// QLIKE would be identically zero and the assertion below would
			// read as "the loss was never computed".
			if err := st.ResolveRVForecast(ctx, s.ID, ts, 1, 6e-4, now); err != nil {
				t.Fatalf("resolve: %v", err)
			}
		}
	}
	rec, err := st.RVLiveRecord(ctx, 1)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if rec.N != 6 {
		t.Errorf("N = %d, want 6 rows", rec.N)
	}
	if rec.DistinctDays != 2 {
		t.Errorf("DistinctDays = %d, want 2 -- six rows are not six observations", rec.DistinctDays)
	}
	if rec.MeanQLIKEHAR <= 0 || rec.MeanQLIKEEW <= 0 {
		t.Errorf("losses not computed: har=%v ewma=%v", rec.MeanQLIKEHAR, rec.MeanQLIKEEW)
	}
}

// The registrar's pre-flight: a forward test filed after its own results are
// readable is not a registration.
func TestCountResolvedRVGatesRegistration(t *testing.T) {
	st, sid := newRVStore(t)
	ctx, now := context.Background(), time.Unix(1_700_000_000, 0)
	if n, err := st.CountResolvedRV(ctx); err != nil || n != 0 {
		t.Fatalf("empty store: n=%d err=%v, want 0", n, err)
	}
	if err := st.UpsertRVForecast(ctx, sampleForecast(sid, 1000), now); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n, _ := st.CountResolvedRV(ctx); n != 0 {
		t.Errorf("an UNRESOLVED forecast counted as a readable result (n=%d)", n)
	}
	if err := st.ResolveRVForecast(ctx, sid, 1000, 1, 5e-4, now); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if n, _ := st.CountResolvedRV(ctx); n != 1 {
		t.Errorf("a resolved forecast was not visible to the registration gate (n=%d)", n)
	}
}

// FreezeRVForecast says whether it wrote: the first call for a (symbol, ts,
// horizon) inserts, every later one is a no-op, and the runner counts only the
// first as frozen.
func TestFreezeRVForecastReportsInsertion(t *testing.T) {
	st, sid := newRVStore(t)
	ctx, now := context.Background(), time.Unix(1_700_000_000, 0)
	ins, err := st.FreezeRVForecast(ctx, sampleForecast(sid, 1000), now)
	if err != nil || !ins {
		t.Fatalf("first freeze: inserted=%v err=%v", ins, err)
	}
	ins, err = st.FreezeRVForecast(ctx, sampleForecast(sid, 1000), now.Add(time.Hour))
	if err != nil || ins {
		t.Fatalf("second freeze: inserted=%v err=%v, want a no-op", ins, err)
	}
}
