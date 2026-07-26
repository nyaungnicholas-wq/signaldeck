// C4 (residual) — /api/signal-backtest publishes the platform's own hit rate and
// information coefficient over the independent (symbol, UTC-day) set with NO
// interval at all, gated only on a raw count (signalbt.MinIndependentN = 30,
// with no distinct-day floor). Thirty symbol-days can be three market days.
//
// Measured on the live 1d feature store (2026-07-25, data/signaldeck.db) — the
// same rows this endpoint replays:
//
//	13,058 independent obs spanning 23 distinct days
//	design effect 14.7x -> effective N 887
//
// The engine is right to compute point estimates over the deduped set; what was
// missing is any statement of how many DAYS those 13,058 rows came from and how
// much of their apparent precision the day clustering removes. The engine stays
// pure; the interval is attached here, by the one module that owns intervals.
package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/signalbt"
)

// signalBTClusterBody decodes only the cluster-robust additions.
type signalBTClusterBody struct {
	Result struct {
		IndependentN int     `json:"independentN"`
		HitRate      float64 `json:"hitRate"`
		Gated        bool    `json:"gated"`
	} `json:"result"`
	Cluster *struct {
		N                int     `json:"n"`
		DistinctDays     int     `json:"distinctDays"`
		DesignEffect     float64 `json:"designEffect"`
		EffectiveN       float64 `json:"effectiveN"`
		P                float64 `json:"p"`
		Refused          bool    `json:"refused"`
		Reason           string  `json:"reason"`
		CI               *ciJSON `json:"ci"`
		NaiveDiscredited *ciJSON `json:"naiveCIDiscredited"`
	} `json:"cluster"`
	ClusterNote string `json:"clusterNote"`
	IC          *struct {
		Point   float64 `json:"point"`
		CI      *ciJSON `json:"ci"`
		Refused bool    `json:"refused"`
		Reason  string  `json:"reason"`
		Method  string  `json:"method"`
	} `json:"icInterval"`
}

func getSignalBTCluster(t *testing.T, url string) signalBTClusterBody {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status=%d want 200", res.StatusCode)
	}
	var body signalBTClusterBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestSignalBacktest_ClusterRobustHitRate is the failing test: the own-signal
// grade ships a hit rate with no interval and no day count. The day count is the
// only honest measure of how much independent evidence this record holds.
func TestSignalBacktest_ClusterRobustHitRate(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	seedCompositeClusteredRecord(t, st, 12, 100)

	body := getSignalBTCluster(t, srv.URL+"/api/signal-backtest?horizon=1d")
	if body.Cluster == nil {
		t.Fatal("no cluster block on the own-signal backtest")
	}
	cl := body.Cluster
	if cl.N != body.Result.IndependentN {
		t.Fatalf("cluster N %d != engine independentN %d — the interval and the point estimate must be over the SAME set",
			cl.N, body.Result.IndependentN)
	}
	if cl.DistinctDays != 12 {
		t.Fatalf("distinctDays = %d, want 12", cl.DistinctDays)
	}
	if cl.DesignEffect <= 1.5 {
		t.Fatalf("design effect = %.2f — the clustering in this fixture was not measured", cl.DesignEffect)
	}
	if cl.EffectiveN >= float64(cl.N) {
		t.Fatalf("effective N %.1f >= raw N %d", cl.EffectiveN, cl.N)
	}
	if cl.CI == nil || cl.NaiveDiscredited == nil {
		t.Fatal("at 12 distinct days both intervals must be present")
	}
	if cl.CI.width() <= cl.NaiveDiscredited.width() {
		t.Fatalf("corrected width %.4f not wider than naive %.4f", cl.CI.width(), cl.NaiveDiscredited.width())
	}
	// The cluster proportion must be the engine's own hit rate, not a second,
	// separately-derived number that could drift from what the page renders.
	if diff := cl.P - body.Result.HitRate; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cluster p %.6f != engine hitRate %.6f", cl.P, body.Result.HitRate)
	}
}

// TestSignalBacktest_ICIntervalResamplesDays: the IC is a correlation over the
// same clustered rows, so it needs the same treatment — a day-resampled interval,
// not a Fisher-z at the row count.
func TestSignalBacktest_ICIntervalResamplesDays(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	seedCompositeClusteredRecord(t, st, 12, 100)

	body := getSignalBTCluster(t, srv.URL+"/api/signal-backtest?horizon=1d")
	if body.IC == nil {
		t.Fatal("no icInterval block on the own-signal backtest")
	}
	if body.IC.CI == nil {
		t.Fatalf("IC interval withheld at 12 days: %s", body.IC.Reason)
	}
	if !(body.IC.CI.Lo <= body.IC.Point && body.IC.Point <= body.IC.CI.Hi) {
		t.Fatalf("IC %.4f outside its own interval [%.4f, %.4f]", body.IC.Point, body.IC.CI.Lo, body.IC.CI.Hi)
	}
	// This fixture's signal is right on half the days and wrong on the other
	// half. Resampling days must therefore straddle zero — there is no IC here.
	if !(body.IC.CI.Lo < 0 && body.IC.CI.Hi > 0) {
		t.Fatalf("day-resampled IC interval [%.4f, %.4f] excludes zero on a record with no day-level edge",
			body.IC.CI.Lo, body.IC.CI.Hi)
	}
}

// TestSignalBacktest_ThinRecordWithheld: below the day floor there is no honest
// interval, so the cluster block reports the sample-size facts and refuses, with
// the reason naming the shortfall. It must NOT publish a narrow band.
func TestSignalBacktest_ThinRecordWithheld(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	seedCompositeClusteredRecord(t, st, 4, 100) // 400 obs, 4 days

	body := getSignalBTCluster(t, srv.URL+"/api/signal-backtest?horizon=1d")
	if body.Cluster == nil {
		t.Fatal("no cluster block")
	}
	if body.Cluster.CI != nil {
		t.Fatalf("interval published off %d distinct days", body.Cluster.DistinctDays)
	}
	if !body.Cluster.Refused || body.Cluster.Reason == "" {
		t.Fatalf("refusal carries no reason: %+v", body.Cluster)
	}
	if body.IC == nil || body.IC.CI != nil {
		t.Fatalf("IC interval published off %d distinct days", body.Cluster.DistinctDays)
	}
}

// TestSignalBacktest_PinnedCarriesNoFabricatedCluster: the pinned weekly
// snapshot stores a Result, not the observations behind it, so no design effect
// can be measured from it. The endpoint must say that rather than attaching a
// cluster block computed from a different (live) read, which would put a Sunday
// point estimate beside a Wednesday interval.
func TestSignalBacktest_PinnedCarriesNoFabricatedCluster(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	ctx := context.Background()
	pin := signalbt.Pinned{
		DayKey: "2026-07-19", ComputedTs: 1752361200,
		BenchmarkSymbol: "SPY", HasBenchmark: true,
		Results: map[string]signalbt.Result{
			"1d": {Horizon: "1d", RawN: 5000, IndependentN: 900, MinIndependentN: 30, Gated: false, IC: 0.02, HitRate: 0.51},
		},
	}
	if err := st.SetJSON(ctx, signalbt.MetaKeyLatest, pin); err != nil {
		t.Fatal(err)
	}

	body := getSignalBTCluster(t, srv.URL+"/api/signal-backtest?horizon=1d&pinned=1")
	if body.Cluster != nil && body.Cluster.CI != nil {
		t.Fatal("pinned snapshot published an interval it has no observations to support")
	}
	if body.ClusterNote == "" {
		t.Fatal("pinned snapshot must state WHY no cluster grade accompanies it")
	}
}
