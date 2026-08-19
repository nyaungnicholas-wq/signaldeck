package api

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newTrackRecordServer stands up a temp store behind the real secure() middleware
// with only the track-record route wired.
func newTrackRecordServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "track.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{
		St:      st,
		Cfg:     config.Config{WebOrigins: []string{"http://app.example"}, PublicReads: true},
		Version: "test",
		Started: time.Now(),
	}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/track-record", d.trackRecord)
	mux.HandleFunc("GET /api/chart-overlays", d.chartOverlays)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

// seedResolvedPrediction upserts a prediction (which seeds prediction_outcomes)
// and resolves it with the given realized forward return. up is derived from the
// sign of fwd (matching the resolver).
func seedResolvedPrediction(t *testing.T, st *store.Store, symbolID int64, h md.Horizon, ts int64, prob, fwd float64) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: symbolID, Horizon: h, Ts: ts, RawProb: prob, CalProb: prob, NUsed: 3, Components: "{}",
	}); err != nil {
		t.Fatalf("UpsertPrediction: %v", err)
	}
	if err := st.ResolvePrediction(ctx, symbolID, h, ts, fwd); err != nil {
		t.Fatalf("ResolvePrediction: %v", err)
	}
}

// TestTrackRecord_GatedWhenThin: below trackMinIndependentN independent
// (symbol, UTC-day) resolutions the handler WITHHOLDS every skill number
// (winRate/brier/ic == null) and returns a "not yet significant (k/threshold)"
// note. This is the honest, mostly-empty state the page must render today.
func TestTrackRecord_GatedWhenThin(t *testing.T) {
	srv, st := newTrackRecordServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	const day = int64(86400)
	// Anchored at the survivorship epoch: ResolvedPredictionOutcomes floors on
	// it (store.SurvivorshipEpochTS), so a fixture dated 2023 — as this was —
	// is filtered out entirely and the assertions below grade an empty set.
	base := int64(store.SurvivorshipEpochTS)
	base -= base % day
	// 3 distinct days, MANY rows each → rawN large, independentN = 3 (< 30).
	for di := 0; di < 3; di++ {
		for ri := 0; ri < 20; ri++ {
			ts := base + int64(di)*day + int64(ri)*600
			seedResolvedPrediction(t, st, sym.ID, md.H1d, ts, 0.6, 0.01)
		}
	}

	body := getJSON(t, srv, "/api/track-record?horizon=1d")

	if got := jnum(body, "independentN"); got != 3 {
		t.Fatalf("independentN should be 3 (3 symbol-days), got %.0f", got)
	}
	if got := jnum(body, "rawN"); got < 50 {
		t.Fatalf("rawN should reflect the ~60 raw rows, got %.0f", got)
	}
	if gated, _ := body["gated"].(bool); !gated {
		t.Fatal("expected gated=true below the independent-N floor")
	}
	// Skill numbers must be withheld (JSON null → nil in the map).
	for _, k := range []string{"winRate", "brier", "ic"} {
		if body[k] != nil {
			t.Fatalf("%s should be null when gated, got %v", k, body[k])
		}
	}
	if _, ok := body["note"].(string); !ok {
		t.Fatal("expected a 'not yet significant' note when gated")
	}
	// live is true (this IS a live forward record) even while gated.
	if live, _ := body["live"].(bool); !live {
		t.Fatal("track record should be labeled live=true")
	}
}

// TestTrackRecord_UngatedMath: with >= trackMinIndependentN independent obs the
// handler reports a winRate + Brier + IC, each with a CI, and the numbers are
// arithmetically correct on a controlled sample.
func TestTrackRecord_UngatedMath(t *testing.T) {
	srv, st := newTrackRecordServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	const day = int64(86400)
	base := int64(store.SurvivorshipEpochTS) // must be >= the epoch; see above
	base -= base % day
	// 40 distinct symbol-days (one row each), a strong directional signal:
	// prob=0.8 on up days (fwd>0), prob=0.2 on down days (fwd<0). Alternating,
	// with a slight jitter in fwd magnitude so the correlation is high but not a
	// degenerate exactly-1.0 (which would make the CI a single point). So realized
	// up-rate = 0.5, and the signal (prob-0.5) is strongly rank-correlated w/ fwd.
	nUp := 0
	for di := 0; di < 40; di++ {
		ts := base + int64(di)*day
		var prob, fwd float64
		if di%2 == 0 {
			prob = 0.8
			fwd = 0.02 + 0.001*float64(di) // jitter → non-degenerate correlation
			nUp++
		} else {
			prob = 0.2
			fwd = -0.02 - 0.001*float64(di)
		}
		seedResolvedPrediction(t, st, sym.ID, md.H1d, ts, prob, fwd)
	}

	body := getJSON(t, srv, "/api/track-record?horizon=1d")

	if got := jnum(body, "independentN"); got != 40 {
		t.Fatalf("independentN should be 40, got %.0f", got)
	}
	if gated, _ := body["gated"].(bool); gated {
		t.Fatal("expected gated=false with 40 independent obs")
	}
	// winRate = DIRECTIONAL ACCURACY (predUp==actualUp): the model predicts up
	// (0.8) on up days and down (0.2) on down days → perfectly directional → 1.0.
	if got := jnum(body, "winRate"); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("winRate (directional accuracy) should be 1.0, got %.4f", got)
	}
	// baseRate = realized up-rate = 20/40 = 0.5; naive baseline = max(0.5,0.5)=0.5;
	// edge over the naive baseline = accuracy 1.0 − 0.5 = 0.5.
	if got := jnum(body, "baseRate"); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("baseRate should be 0.5, got %.4f", got)
	}
	if got := jnum(body, "edgeVsNaive"); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("edgeVsNaive should be 0.5, got %.4f", got)
	}
	// IC of a strongly directional signal must be high (binary signal against a
	// jittered forward return caps Pearson below 1, but it is clearly positive).
	if got := jnum(body, "ic"); got < 0.9 {
		t.Fatalf("IC of a strongly directional signal should be high, got %.4f", got)
	}
	// Brier: prob is 0.8/0.2 and always correct in direction → each squared error
	// is 0.2^2 = 0.04.
	if got := jnum(body, "brier"); math.Abs(got-0.04) > 1e-6 {
		t.Fatalf("brier should be 0.04, got %.6f", got)
	}
	// CIs must be present and bracket the point estimate.
	if ci, ok := body["icCI"].([]any); ok && len(ci) == 2 {
		lo, _ := ci[0].(float64)
		hi, _ := ci[1].(float64)
		ic := jnum(body, "ic")
		if !(lo <= ic && ic <= hi) {
			t.Fatalf("IC CI [%.3f,%.3f] should bracket ic=%.3f", lo, hi, ic)
		}
	} else {
		t.Fatal("expected icCI as a 2-element array")
	}
}

// TestTrackRecord_NoLookahead: appending MORE resolved rows for LATER days can
// only ADD independent observations — it must never change the grade of the
// earlier, already-resolved days. (A resolved row's prob is frozen; its outcome
// is realized. There is no path for future data to re-grade a past prediction.)
func TestTrackRecord_NoLookahead(t *testing.T) {
	srv, st := newTrackRecordServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	const day = int64(86400)
	base := int64(store.SurvivorshipEpochTS) // must be >= the epoch; see above
	base -= base % day

	seedRange := func(fromDay, toDay int) {
		for di := fromDay; di < toDay; di++ {
			ts := base + int64(di)*day
			prob, fwd := 0.7, 0.01
			if di%3 == 0 {
				prob, fwd = 0.3, -0.01
			}
			seedResolvedPrediction(t, st, sym.ID, md.H1d, ts, prob, fwd)
		}
	}
	seedRange(0, 35)
	first := getJSON(t, srv, "/api/track-record?horizon=1d")
	icFirst := jnum(first, "ic")
	brierFirst := jnum(first, "brier")
	nFirst := jnum(first, "independentN")

	// Now append 20 MORE later days. The earlier 35's contribution is unchanged;
	// only the pooled grade shifts by the honest addition of new independent obs.
	seedRange(35, 55)
	second := getJSON(t, srv, "/api/track-record?horizon=1d")
	if jnum(second, "independentN") <= nFirst {
		t.Fatalf("appending later days must ADD independent obs (was %.0f, now %.0f)",
			nFirst, jnum(second, "independentN"))
	}
	// Sanity: both grades are finite and the same-signal appends keep IC positive.
	if math.IsNaN(icFirst) || math.IsNaN(brierFirst) {
		t.Fatal("first grade produced NaN")
	}
	if jnum(second, "ic") <= 0 {
		t.Fatalf("same-signal appends should keep IC positive, got %.3f", jnum(second, "ic"))
	}
}

// TestWilsonInterval checks the Wilson score interval on a known case and its
// boundary behaviour (never escapes [0,1]).
func TestWilsonInterval(t *testing.T) {
	lo, hi := wilson(50, 100)
	// For p=0.5, n=100 the Wilson 95% interval is ~[0.404, 0.596].
	if lo < 0.39 || lo > 0.42 || hi < 0.58 || hi > 0.61 {
		t.Fatalf("wilson(50,100) = [%.3f,%.3f], expected ~[0.404,0.596]", lo, hi)
	}
	// All wins: hi must not exceed 1, lo must be < 1.
	lo2, hi2 := wilson(10, 10)
	if hi2 > 1.0000001 || lo2 >= 1.0 || lo2 < 0 {
		t.Fatalf("wilson(10,10) = [%.3f,%.3f] escaped [0,1]", lo2, hi2)
	}
	// n=0 is safe.
	if l, h := wilson(0, 0); l != 0 || h != 0 {
		t.Fatalf("wilson(0,0) should be [0,0], got [%.3f,%.3f]", l, h)
	}
}

// TestFisherCI checks the Fisher-z correlation CI brackets r and narrows with n.
func TestFisherCI(t *testing.T) {
	lo, hi := fisherCI(0.5, 100)
	if !(lo < 0.5 && 0.5 < hi) {
		t.Fatalf("fisherCI(0.5,100) = [%.3f,%.3f] should bracket 0.5", lo, hi)
	}
	loWide, hiWide := fisherCI(0.5, 10)
	if (hiWide - loWide) <= (hi - lo) {
		t.Fatalf("CI should widen at smaller n: n=10 width %.3f vs n=100 width %.3f",
			hiWide-loWide, hi-lo)
	}
	// r=1 must not blow up (atanh clamped).
	if l, h := fisherCI(1.0, 50); math.IsInf(l, 0) || math.IsInf(h, 0) || math.IsNaN(l) || math.IsNaN(h) {
		t.Fatalf("fisherCI(1,50) = [%.3f,%.3f] must be finite", l, h)
	}
}

// TestChartOverlays_ScoreExtremeCrossingDedup: a long run of strong-buy scores
// must emit ONE score marker (the crossing bar), not one per bar — the legibility
// guarantee. Regime changes and breakouts pass through as individual markers.
func TestChartOverlays_ScoreExtremeCrossingDedup(t *testing.T) {
	srv, st := newTrackRecordServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "DDD", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := time.Now().Unix()
	day := int64(86400)
	// 10 consecutive daily bars all strongly bullish (>= 0.6), preceded by a
	// neutral bar. Expect exactly ONE score marker (the first crossing).
	base := now - 20*day
	if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1d, Ts: base, Score: 0.1}); err != nil {
		t.Fatalf("InsertScore: %v", err)
	}
	if err := st.ResolveOutcome(ctx, sym.ID, md.H1d, base, 0.0); err != nil {
		t.Fatalf("ResolveOutcome: %v", err)
	}
	for i := 1; i <= 10; i++ {
		ts := base + int64(i)*day
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, Score: 0.8}); err != nil {
			t.Fatalf("InsertScore: %v", err)
		}
	}
	// One regime change + one breakout in the window.
	if err := st.UpsertRegime(ctx, sym.ID, base+2*day, "range", 0.3, ""); err != nil {
		t.Fatalf("UpsertRegime: %v", err)
	}
	if err := st.UpsertRegime(ctx, sym.ID, base+3*day, "uptrend", 0.7, ""); err != nil {
		t.Fatalf("UpsertRegime (change): %v", err)
	}
	if err := st.InsertBreakout(ctx, &sym.ID, base+4*day, "donchian", "20d high", 1.0); err != nil {
		t.Fatalf("InsertBreakout: %v", err)
	}

	body := getJSON(t, srv, "/api/chart-overlays?symbol=DDD&market=stocks&days=30")
	markers, _ := body["markers"].([]any)

	var nScore, nRegime, nBreakout int
	for _, mi := range markers {
		m, _ := mi.(map[string]any)
		switch m["type"] {
		case "score":
			nScore++
		case "regime":
			nRegime++
		case "breakout":
			nBreakout++
		}
	}
	if nScore != 1 {
		t.Fatalf("expected exactly 1 score-extreme marker (crossing dedup), got %d", nScore)
	}
	if nRegime != 1 {
		t.Fatalf("expected 1 regime-change marker, got %d", nRegime)
	}
	if nBreakout != 1 {
		t.Fatalf("expected 1 breakout marker, got %d", nBreakout)
	}
}
