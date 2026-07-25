package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newAttributionServer stands up a temp store behind the real secure()
// middleware with only the attribution route wired.
func newAttributionServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "attr.db"))
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
	d.registerAttribution(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

// TestAttributionLive_DedupesPerDayAndScoresDirection: the fleet-wide
// aggregation must reproduce, for every symbol at once, exactly what the
// per-symbol loop did — at most ONE observation per (symbol, UTC day), and a
// DIRECTIONAL score (predUp == actualUp), not an up-rate. A symbol predicted 20
// times in one session must not thereby claim 20 independent observations.
func TestAttributionLive_DedupesPerDayAndScoresDirection(t *testing.T) {
	_, st := newAttributionServer(t)
	ctx := context.Background()
	d := Deps{St: st}

	aaa, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert AAA: %v", err)
	}
	bbb, err := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert BBB: %v", err)
	}

	const day = int64(86400)
	base := int64(1_700_000_000)
	base -= base % day

	// AAA: four days, two right and two wrong — and both directions scored, so
	// an up-rate implementation (3/4 up) could not pass this.
	//   d0 predUp   / up   → correct
	//   d1 predUp   / down → wrong
	//   d2 predDown / down → correct
	//   d3 predDown / up   → wrong
	seedResolvedPrediction(t, st, aaa.ID, md.H1d, base+0*day, 0.60, +0.01)
	seedResolvedPrediction(t, st, aaa.ID, md.H1d, base+1*day, 0.60, -0.01)
	seedResolvedPrediction(t, st, aaa.ID, md.H1d, base+2*day, 0.30, -0.01)
	seedResolvedPrediction(t, st, aaa.ID, md.H1d, base+3*day, 0.30, +0.01)
	// Same-day extras on d0: deduped away, so N must stay 4.
	for ri := 1; ri <= 10; ri++ {
		seedResolvedPrediction(t, st, aaa.ID, md.H1d, base+int64(ri)*600, 0.60, +0.01)
	}

	// BBB: two days, both correct — proves symbols are aggregated separately
	// rather than pooled.
	seedResolvedPrediction(t, st, bbb.ID, md.H1d, base+0*day, 0.70, +0.02)
	seedResolvedPrediction(t, st, bbb.ID, md.H1d, base+1*day, 0.70, +0.02)

	// A different horizon must not leak into the 1d aggregation.
	seedResolvedPrediction(t, st, aaa.ID, md.H1w, base+0*day, 0.90, +0.05)

	payload, err := d.buildAttributionLive(ctx, md.H1d)
	if err != nil {
		t.Fatalf("buildAttributionLive: %v", err)
	}
	byID, ok := payload[attributionLiveField].(attributionLive)
	if !ok {
		t.Fatalf("payload %q is not an attributionLive", attributionLiveField)
	}

	if got := byID[aaa.ID]; got.N != 4 || math.Abs(got.HitRate-0.5) > 1e-9 {
		t.Fatalf("AAA: got N=%d hitRate=%v, want N=4 hitRate=0.5", got.N, got.HitRate)
	}
	if got := byID[bbb.ID]; got.N != 2 || math.Abs(got.HitRate-1.0) > 1e-9 {
		t.Fatalf("BBB: got N=%d hitRate=%v, want N=2 hitRate=1", got.N, got.HitRate)
	}
}

// TestCurrentStateFor_CachesAndTolerates: currentStateFor exists purely to stop
// the handler from reloading 25k minute bars per request, so the thing worth
// asserting is that a second call does NOT reach the builder — and that a Deps
// without a CurrentState hook (the zero value several tests construct) returns
// empty rather than panicking.
func TestCurrentStateFor_CachesAndTolerates(t *testing.T) {
	_, st := newAttributionServer(t)
	ctx := context.Background()

	var calls int32
	d := Deps{St: st, CurrentState: func(context.Context, int64) (map[md.Horizon]string, error) {
		atomic.AddInt32(&calls, 1)
		return map[md.Horizon]string{md.H1d: "trend|hi"}, nil
	}}

	for i := 0; i < 3; i++ {
		if got := d.currentStateFor(ctx, 1)[md.H1d]; got != "trend|hi" {
			t.Fatalf("call %d: got state %q, want %q", i, got, "trend|hi")
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("CurrentState was called %d times, want 1 (the cache did not hold)", n)
	}
	// A different symbol is a different entry, so it must build again.
	if got := d.currentStateFor(ctx, 2)[md.H1d]; got != "trend|hi" {
		t.Fatalf("symbol 2: got state %q", got)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("CurrentState called %d times after a second symbol, want 2", n)
	}

	if got := (Deps{St: st}).currentStateFor(ctx, 1); len(got) != 0 {
		t.Fatalf("nil CurrentState hook: got %v, want empty", got)
	}
}

// TestAttribution_CachedPerStore: the live cache is package-level, so two
// stores must never see each other's ledger. Without store identity in the key
// the second server would be served the first's aggregation — the exact leak
// that has bitten the predictions and xs-factor caches.
func TestAttribution_CachedPerStore(t *testing.T) {
	ctx := context.Background()
	const day = int64(86400)
	base := int64(1_700_000_000)
	base -= base % day

	// Store A: symbol AAA with 3 resolved days, all correct.
	_, stA := newAttributionServer(t)
	aaa, err := stA.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert AAA: %v", err)
	}
	for di := 0; di < 3; di++ {
		seedResolvedPrediction(t, stA, aaa.ID, md.H1d, base+int64(di)*day, 0.60, +0.01)
	}
	// Store B: an EMPTY ledger with a symbol at the same row id.
	srvB, stB := newAttributionServer(t)
	if _, err := stB.UpsertSymbol(ctx, "AAA", md.Stocks, ""); err != nil {
		t.Fatalf("upsert AAA (B): %v", err)
	}

	dA := Deps{St: stA}
	if got := dA.attributionLiveFor(ctx, md.H1d, aaa.ID); got.N != 3 {
		t.Fatalf("store A: got live N=%d, want 3", got.N)
	}

	// B's answer must be its own (empty), not A's cached three observations —
	// and it must come back over HTTP, through the same shared cache.
	resp, err := http.Get(srvB.URL + "/api/attribution?symbol=AAA&market=stocks")
	if err != nil {
		t.Fatalf("GET attribution: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET attribution: status %d", resp.StatusCode)
	}
	var body struct {
		Report struct {
			LiveN int `json:"liveResolvedN"`
		} `json:"report"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Report.LiveN != 0 {
		t.Fatalf("store B leaked store A's ledger: liveResolvedN=%d, want 0", body.Report.LiveN)
	}
}
