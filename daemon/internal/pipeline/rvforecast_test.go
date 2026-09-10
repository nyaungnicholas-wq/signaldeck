package pipeline

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/harrv"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// rvTestBars builds a long, well-behaved daily series: a deterministic walk
// with a real intraday range on every session, so the estimator always has
// something to read and the fit has enough history to converge.
func rvTestBars(symbolID int64, n int, start time.Time) []md.Bar {
	bars := make([]md.Bar, n)
	px := 100.0
	d := start // advanced to the next NYSE trading day per bar; stamped at NY midnight like production
	for i := 0; i < n; i++ {
		for !marketcal.IsTradingDay(d) {
			d = d.AddDate(0, 0, 1)
		}
		px *= 1 + 0.004*math.Sin(float64(i)/11)
		rng := px * (0.006 + 0.004*math.Abs(math.Cos(float64(i)/7)))
		bars[i] = md.Bar{
			SymbolID: symbolID, TF: md.TF1d,
			Ts:     d.Unix(),
			Open:   px,
			High:   px + rng,
			Low:    px - rng,
			Close:  px + 0.3*rng*math.Sin(float64(i)/3),
			Volume: 1_000_000,
		}
		d = d.AddDate(0, 0, 1)
	}
	return bars
}

func newRVPipelineStore(t *testing.T, market md.Market, nBars int) (*store.Store, md.Symbol, time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "rvpipe.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "TESTCO", market, "")
	if err != nil {
		t.Fatalf("symbol: %v", err)
	}
	start := time.Date(2020, 1, 2, 0, 0, 0, 0, marketcal.Loc())
	bars := rvTestBars(sym.ID, nBars, start)
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("bars: %v", err)
	}
	// now = 22h after the last stamp: that session has settled and no later one has closed.
	return st, sym, time.Unix(bars[len(bars)-1].Ts, 0).Add(22 * time.Hour)
}

// The whole loop: freeze with both nulls, refuse to resolve before the window
// closes, then resolve. This is the integration the unit tests cannot reach --
// the model, the store invariants and the two workers all have to agree on
// what a forecast IS.
func TestRVForecastAndResolveRoundTrip(t *testing.T) {
	const nBars = 900
	st, sym, now := newRVPipelineStore(t, md.Stocks, nBars)
	ctx := context.Background()

	// Rev is injected because lineage.RevisionStamp() is "" under `go test`
	// (no ldflags on a test binary) and the store refuses a row it cannot
	// attribute to a build. That refusal is correct and is what caught this.
	run := &RVForecastRunner{
		St:  st,
		Now: func() time.Time { return now },
		Rev: func() string { return "testrev0000000000000000000000000000000000" },
	}
	detail, err := run.Run(ctx)
	if err != nil {
		t.Fatalf("forecast run: %v (%s)", err, detail)
	}
	t.Logf("forecast: %s", detail)

	for _, h := range RVHorizons {
		open, err := st.OpenRVForecasts(ctx, int(h), 100)
		if err != nil {
			t.Fatalf("open h=%d: %v", h, err)
		}
		if len(open) != 1 {
			t.Fatalf("h=%d: %d open forecasts, want exactly 1", h, len(open))
		}
		f := open[0]
		if f.NullRW <= 0 || f.NullEWMA <= 0 {
			t.Errorf("h=%d: a null was not frozen (rw=%v ewma=%v)", h, f.NullRW, f.NullEWMA)
		}
		if f.RVHat <= 0 {
			t.Errorf("h=%d: non-positive forecast %v", h, f.RVHat)
		}
		if f.NTrain < harrv.MinTrain {
			t.Errorf("h=%d: fit used %d rows, below MinTrain %d", h, f.NTrain, harrv.MinTrain)
		}
		if f.Revision == "" {
			t.Errorf("h=%d: no revision stamp, so the row cannot be tied to a build", h)
		}
	}

	// Nothing may resolve while the outcome window is still in the future.
	res := &RVOutcomeWorker{St: st, Now: func() time.Time { return now }}
	if _, err := res.Run(ctx); err != nil && !errors.Is(err, workers.ErrDegraded) {
		t.Fatalf("early resolver: %v", err)
	}
	for _, h := range RVHorizons {
		rec, err := st.RVLiveRecord(ctx, int(h))
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if rec.N != 0 {
			t.Errorf("h=%d resolved %d forecasts before their window closed", h, rec.N)
		}
	}

	// Extend past the longest horizon, then resolve for real.
	start := time.Date(2020, 1, 2, 0, 0, 0, 0, marketcal.Loc())
	if err := st.UpsertBars(ctx, rvTestBars(sym.ID, nBars+30, start)); err != nil {
		t.Fatalf("extend bars: %v", err)
	}
	later := now.AddDate(0, 0, 40)
	res = &RVOutcomeWorker{St: st, Now: func() time.Time { return later }}
	if detail, err = res.Run(ctx); err != nil {
		t.Fatalf("resolve run: %v (%s)", err, detail)
	}
	t.Logf("resolve: %s", detail)

	for _, h := range RVHorizons {
		rec, err := st.RVLiveRecord(ctx, int(h))
		if err != nil {
			t.Fatalf("record h=%d: %v", h, err)
		}
		if rec.N != 1 {
			t.Errorf("h=%d: resolved %d, want 1", h, rec.N)
		}
		// A NaN or Inf here would mean a null had been stored as zero and
		// QLIKE divided by it -- which the store is supposed to make impossible.
		for name, v := range map[string]float64{
			"har": rec.MeanQLIKEHAR, "rw": rec.MeanQLIKERW, "ewma": rec.MeanQLIKEEW,
		} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				t.Errorf("h=%d: %s QLIKE = %v", h, name, v)
			}
		}
	}
}

// Too little history means NO forecast, and the run must report itself
// DEGRADED rather than ok. A worker returning "ok" having written nothing is
// how congress-poller sat green over an empty table for weeks.
func TestRVForecastRunnerDegradesOnThinHistory(t *testing.T) {
	st, _, now := newRVPipelineStore(t, md.Stocks, 100) // far below MinHistory
	ctx := context.Background()

	detail, err := (&RVForecastRunner{St: st, Now: func() time.Time { return now },
		Rev: func() string { return "testrev" }}).Run(ctx)
	if !errors.Is(err, workers.ErrDegraded) {
		t.Errorf("thin history returned %v, want ErrDegraded", err)
	}
	if detail == "" {
		t.Error("a degraded run must still say what it saw")
	}
	t.Logf("detail: %s", detail)
	for _, h := range RVHorizons {
		if open, _ := st.OpenRVForecasts(ctx, int(h), 10); len(open) != 0 {
			t.Errorf("h=%d: wrote %d forecasts from 100 bars", h, len(open))
		}
	}
}

// Crypto is skipped on purpose: the estimator is a daily RANGE estimator built
// on equity sessions, and a 24/7 series has no overnight gap in the same
// sense. Forecasting it there would be a different estimand wearing the same
// name -- and it would look like coverage rather than like a mistake.
func TestRVForecastRunnerSkipsCrypto(t *testing.T) {
	st, _, now := newRVPipelineStore(t, md.Crypto, 900)
	ctx := context.Background()

	if _, err := (&RVForecastRunner{St: st, Now: func() time.Time { return now },
		Rev: func() string { return "testrev" }}).Run(ctx); !errors.Is(err, workers.ErrDegraded) {
		t.Errorf("a crypto-only universe returned %v, want ErrDegraded", err)
	}
	for _, h := range RVHorizons {
		if open, _ := st.OpenRVForecasts(ctx, int(h), 10); len(open) != 0 {
			t.Errorf("h=%d: forecast a crypto symbol", h)
		}
	}
}
