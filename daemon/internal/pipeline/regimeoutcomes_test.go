// Credibility-wave tests: outcome-snapshot idempotency, resolver correctness
// on constructed bar fixtures (grading only ever uses bars after the call up
// to the horizon), the not-enough-bars hold, and postmortems written only for
// high-conviction misses (idempotent per outcome).
package pipeline

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

func newRegimeOutcomeStore(t *testing.T, name string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// countOutcomes counts ALL outcome rows (resolved or not) via the due read with
// a far-future clock plus the resolved read.
func countOutcomes(t *testing.T, st *store.Store) int {
	t.Helper()
	ctx := context.Background()
	due, err := st.DueRegimeOutcomes(ctx, 1<<60, 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	res, err := st.ResolvedRegimeOutcomes(ctx, 0)
	if err != nil {
		t.Fatalf("resolved: %v", err)
	}
	return len(due) + len(res)
}

// Snapshot pass: one outcome row per (symbol, kind, UTC-day) no matter how many
// times the worker runs; a NEW day freezes a new row.
func TestRegimeOutcomeSnapshotIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "snap.db")
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}

	day0 := int64(1_700_000_000)
	f := structregime.Forecast{Kind: structregime.KindTrend21, HorizonDays: 21,
		Regime: "uptrend", Conviction: 0.91, HistoricalAccuracy: 0.972,
		Tier: "very-high conviction", Rank: 0.91, N: 500}
	if err := st.UpsertRegimeForecast(ctx, sym.ID, day0, f); err != nil {
		t.Fatalf("forecast: %v", err)
	}

	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(day0+100, 0) }}
	for i := 0; i < 3; i++ {
		if _, err := w.Run(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if n := countOutcomes(t, st); n != 1 {
		t.Fatalf("same UTC day must freeze exactly 1 outcome, got %d", n)
	}
	// forecast refreshed later the SAME day → still one row
	if err := st.UpsertRegimeForecast(ctx, sym.ID, day0+3600, f); err != nil {
		t.Fatalf("forecast2: %v", err)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run same-day refresh: %v", err)
	}
	if n := countOutcomes(t, st); n != 1 {
		t.Fatalf("same-day refresh must not duplicate, got %d", n)
	}
	// next UTC day → a second frozen call
	if err := st.UpsertRegimeForecast(ctx, sym.ID, day0+86400, f); err != nil {
		t.Fatalf("forecast3: %v", err)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run next day: %v", err)
	}
	if n := countOutcomes(t, st); n != 2 {
		t.Fatalf("new UTC day must freeze a 2nd outcome, got %d", n)
	}
}

// seedRegimeBars writes n daily bars, close/vol via fn(i), ts = (startDay+i)*86400.
func seedRegimeBars(t *testing.T, st *store.Store, symID int64, startDay, n int,
	fn func(i int) (close, vol float64),
) {
	t.Helper()
	bars := make([]md.Bar, 0, n)
	for i := 0; i < n; i++ {
		c, v := fn(i)
		bars = append(bars, md.Bar{SymbolID: symID, TF: md.TF1d,
			Ts: int64(startDay+i) * 86400, Open: c, High: c, Low: c, Close: c, Volume: v})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("bars: %v", err)
	}
}

// freezeCall inserts one frozen outcome row directly (the snapshot the worker
// would have taken at call time) and returns nothing — grading is the test.
func freezeCall(t *testing.T, st *store.Store, symID int64, kind structregime.Kind,
	ts int64, horizonDays int, regime string, conviction, claimed float64,
) {
	t.Helper()
	if _, err := st.InsertRegimeOutcome(context.Background(), store.RegimeCall{
		SymbolID: symID, Kind: kind, Ts: ts, HorizonDays: horizonDays,
		Regime: regime, Conviction: conviction, HistoricalAccuracy: claimed, Rank: conviction,
	}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
}

// Trend grading: a call that the tape then contradicts resolves wrong (and,
// being high-conviction, earns a postmortem); the same call on a tape that
// keeps trending resolves correct. NO LOOKAHEAD: grading happens only once the
// horizon bars exist — before that the row must stay unresolved even though the
// calendar due date has passed.
func TestRegimeOutcomeResolveTrend(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "trend.db")
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	// 280 bars of drift up, then a crash to half from bar 281 on. Call at bar
	// 280 (day 1280): "uptrend" — realized at bar 301 is far below SMA200.
	const start = 1000
	crashAt := 281
	price := func(i int) (float64, float64) {
		p := 100.0 * math.Pow(1.002, float64(i))
		if i >= crashAt {
			p = 100.0 * math.Pow(1.002, float64(crashAt)) * 0.5
		}
		return p, 1e6
	}
	callTs := int64(start+280) * 86400
	dueClock := callTs + int64(21*1.45*86400) + 10

	// Phase 1: bars only up to the call — due by calendar, but no forward bars.
	seedRegimeBars(t, st, sym.ID, start, 281, price)
	freezeCall(t, st, sym.ID, structregime.KindTrend21, callTs, 21, "uptrend", 0.91, 0.972)
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(dueClock, 0) }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if res, _ := st.ResolvedRegimeOutcomes(ctx, 0); len(res) != 0 {
		t.Fatalf("must NOT grade without the forward bars (lookahead-free), got %d resolved", len(res))
	}

	// Phase 2: forward bars exist → grades wrong → postmortem (conv 0.91).
	seedRegimeBars(t, st, sym.ID, start, 310, price)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run2: %v", err)
	}
	res, _ := st.ResolvedRegimeOutcomes(ctx, 0)
	if len(res) != 1 || res[0].Actual != "downtrend" || res[0].Correct != 0 {
		t.Fatalf("want wrong trend resolution, got %+v", res)
	}
	pms, err := st.RecentRegimePostmortems(ctx, 10)
	if err != nil || len(pms) != 1 {
		t.Fatalf("want 1 postmortem, got %d (err %v)", len(pms), err)
	}
	if pms[0].Kind != "trend21" || pms[0].Actual != "downtrend" || pms[0].Narrative == "" {
		t.Fatalf("malformed postmortem: %+v", pms[0])
	}
	// base-rate sentence computed from the claimed 97.2%: 1/(1-0.972) ≈ 36
	if want := "wrong ~1 in 36 times"; !strings.Contains(pms[0].Narrative, want) {
		t.Fatalf("narrative missing computed base rate %q: %s", want, pms[0].Narrative)
	}

	// Idempotent: a re-run neither re-grades nor duplicates the postmortem.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run3: %v", err)
	}
	if pms, _ = st.RecentRegimePostmortems(ctx, 10); len(pms) != 1 {
		t.Fatalf("postmortem must be idempotent per outcome, got %d", len(pms))
	}
}

// A correct high-conviction call and a wrong LOW-conviction call both resolve —
// and neither earns a postmortem.
func TestRegimePostmortemOnlyHighConvictionMisses(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "pmgate.db")
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")

	const start = 1000
	crashAt := 281
	price := func(i int) (float64, float64) {
		p := 100.0 * math.Pow(1.002, float64(i))
		if i >= crashAt {
			p = 100.0 * math.Pow(1.002, float64(crashAt)) * 0.5
		}
		return p, 1e6
	}
	seedRegimeBars(t, st, sym.ID, start, 310, price)
	callTs := int64(start+280) * 86400
	// correct high-conviction call (downtrend? no — tape stays up until 281,
	// crash makes downtrend the realized label, so "downtrend" is CORRECT here)
	freezeCall(t, st, sym.ID, structregime.KindTrend21, callTs, 21, "downtrend", 0.95, 0.972)
	// wrong LOW-conviction call on a second kind (same series: realized label
	// is downtrend, so "uptrend" at conviction 0.3 is a miss below the floor)
	freezeCall(t, st, sym.ID, structregime.KindTrend63, callTs, 21, "uptrend", 0.30, 0.700)

	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time {
		return time.Unix(callTs+int64(21*1.45*86400)+10, 0)
	}}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	res, _ := st.ResolvedRegimeOutcomes(ctx, 0)
	if len(res) != 2 {
		t.Fatalf("want both calls resolved, got %d", len(res))
	}
	if pms, _ := st.RecentRegimePostmortems(ctx, 10); len(pms) != 0 {
		t.Fatalf("no postmortem for a correct call or a low-conviction miss, got %d", len(pms))
	}
}

// Liquidity + vol21 grading on constructed fixtures: forward surge → active /
// elevated, graded against the CALL-TIME trailing median (causal).
func TestRegimeOutcomeResolveLiquidityAndVol(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "liqvol.db")
	sym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")

	const start = 1000
	callIdx := 280
	price := func(i int) (float64, float64) {
		// gentle alternating price so returns are nonzero; vol surges forward
		p := 100.0
		if i%2 == 0 {
			p = 100.4
		}
		if i > callIdx { // forward window: big swings + volume surge
			p = 100.0
			if i%2 == 0 {
				p = 110.0
			}
			return p, 8e6
		}
		return p, 1e6
	}
	seedRegimeBars(t, st, sym.ID, start, 310, price)
	callTs := int64(start+callIdx) * 86400
	freezeCall(t, st, sym.ID, structregime.KindLiquidity21, callTs, 21, "quiet", 0.85, 0.801)
	freezeCall(t, st, sym.ID, structregime.KindVol21, callTs, 21, "calm", 0.85, 0.668)

	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time {
		return time.Unix(callTs+int64(21*1.45*86400)+10, 0)
	}}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	res, _ := st.ResolvedRegimeOutcomes(ctx, 0)
	if len(res) != 2 {
		t.Fatalf("want 2 resolved, got %d", len(res))
	}
	byKind := map[structregime.Kind]store.RegimeOutcomeRow{}
	for _, r := range res {
		byKind[r.Kind] = r
	}
	if r := byKind[structregime.KindLiquidity21]; r.Actual != "active" || r.Correct != 0 {
		t.Fatalf("liquidity: want wrong 'active', got %+v", r)
	}
	if r := byKind[structregime.KindVol21]; r.Actual != "elevated" || r.Correct != 0 {
		t.Fatalf("vol21: want wrong 'elevated', got %+v", r)
	}
	// both were high-conviction misses → two postmortems, each with a key number
	pms, _ := st.RecentRegimePostmortems(ctx, 10)
	if len(pms) != 2 {
		t.Fatalf("want 2 postmortems, got %d", len(pms))
	}
	for _, p := range pms {
		if p.KeyName == "" || p.KeyValue <= 0 {
			t.Fatalf("postmortem missing its measured key number: %+v", p)
		}
	}
}
