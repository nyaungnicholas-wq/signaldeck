package marketmem

import (
	"math"
	"testing"
)

// The feature-nearest snapshot must rank first with a small distance even when
// several far-away snapshots widen the per-dimension scale.
func TestFind_NearestRanksFirst(t *testing.T) {
	current := []float64{10, 10}
	history := []Snapshot{
		{Ts: 1000, Features: []float64{10.02, 9.98}, FwdReturn: 1}, // ~ current
		{Ts: 2000, Features: []float64{0, 0}, FwdReturn: 2},
		{Ts: 3000, Features: []float64{20, 20}, FwdReturn: -3},
		{Ts: 4000, Features: []float64{-8, 12}, FwdReturn: 4},
		{Ts: 5000, Features: []float64{12, -8}, FwdReturn: -5},
		{Ts: 6000, Features: []float64{18, 2}, FwdReturn: 6},
	}
	res := Find(current, history, 3, 3, 0, 0)
	if res.Gated {
		t.Fatalf("unexpected gate: %s", res.Note)
	}
	if len(res.Analogs) != 3 {
		t.Fatalf("want 3 analogs, got %d", len(res.Analogs))
	}
	if res.Analogs[0].Ts != 1000 {
		t.Fatalf("nearest should be Ts=1000, got Ts=%d", res.Analogs[0].Ts)
	}
	if !(res.Analogs[0].Distance < res.Analogs[1].Distance) {
		t.Fatalf("nearest distance %.6f should be < runner-up %.6f", res.Analogs[0].Distance, res.Analogs[1].Distance)
	}
	if res.Analogs[0].Distance > 0.5 {
		t.Fatalf("near-identical snapshot should have a small distance, got %.6f", res.Analogs[0].Distance)
	}
}

// MeanFwd / MedianFwd / HitRate on a hand-checked selection. The three
// near-origin snapshots (Ts 1,2,3) are the nearest to current, so the selected
// forward returns are {2, -1, 4}: mean = 5/3, median = 2, hit rate = 2/3.
func TestFind_SummaryStats(t *testing.T) {
	current := []float64{0, 0, 0}
	history := []Snapshot{
		{Ts: 1, Features: []float64{0.1, 0, 0}, FwdReturn: 2.0},
		{Ts: 2, Features: []float64{0, 0.1, 0}, FwdReturn: -1.0},
		{Ts: 3, Features: []float64{0, 0, 0.1}, FwdReturn: 4.0},
		{Ts: 4, Features: []float64{10, 0, 0}, FwdReturn: 100},
		{Ts: 5, Features: []float64{0, 10, 0}, FwdReturn: -100},
		{Ts: 6, Features: []float64{0, 0, 10}, FwdReturn: 50},
		{Ts: 7, Features: []float64{10, 10, 10}, FwdReturn: 99},
	}
	res := Find(current, history, 3, 3, 0, 0)
	if res.Gated {
		t.Fatalf("unexpected gate: %s", res.Note)
	}
	if res.N != 7 {
		t.Fatalf("N should be 7 usable snapshots, got %d", res.N)
	}
	got := map[int64]bool{}
	for _, a := range res.Analogs {
		got[a.Ts] = true
	}
	for _, ts := range []int64{1, 2, 3} {
		if !got[ts] {
			t.Fatalf("expected Ts=%d among the analogs, got %+v", ts, res.Analogs)
		}
	}
	if math.Abs(res.MeanFwd-5.0/3.0) > 1e-9 {
		t.Fatalf("MeanFwd = %.9f, want %.9f", res.MeanFwd, 5.0/3.0)
	}
	if res.MedianFwd != 2.0 {
		t.Fatalf("MedianFwd = %.9f, want 2.0", res.MedianFwd)
	}
	if math.Abs(res.HitRate-2.0/3.0) > 1e-9 {
		t.Fatalf("HitRate = %.9f, want %.9f", res.HitRate, 2.0/3.0)
	}
}

// History below minHistory gates with an explanatory Note and no analogs.
func TestFind_GatesThinHistory(t *testing.T) {
	current := []float64{1, 2}
	history := []Snapshot{
		{Ts: 1, Features: []float64{1, 2}, FwdReturn: 1},
		{Ts: 2, Features: []float64{3, 4}, FwdReturn: -1},
	}
	res := Find(current, history, 3, 5, 0, 0)
	if !res.Gated {
		t.Fatal("history below minHistory should gate")
	}
	if res.Note == "" {
		t.Fatal("a gated result must carry an explanatory Note")
	}
	if len(res.Analogs) != 0 {
		t.Fatalf("gated result should have no analogs, got %d", len(res.Analogs))
	}
	if res.N != 2 {
		t.Fatalf("N should report the 2 usable snapshots, got %d", res.N)
	}
}

// The exclude window removes near-in-time matches (both the exact Ts and any
// neighbour inside the window), promoting the next feature-nearest day.
func TestFind_ExcludeWindowDropsNearInTime(t *testing.T) {
	current := []float64{10, 10}
	history := []Snapshot{
		{Ts: 1000, Features: []float64{10.01, 9.99}, FwdReturn: 1},   // closest by feature
		{Ts: 1050, Features: []float64{10.02, 9.98}, FwdReturn: 1.5}, // also close, within 100s of 1000
		{Ts: 5000, Features: []float64{11, 11}, FwdReturn: 2},        // next best, far in time
		{Ts: 6000, Features: []float64{0, 0}, FwdReturn: 3},
		{Ts: 7000, Features: []float64{20, 20}, FwdReturn: -4},
		{Ts: 8000, Features: []float64{-5, 15}, FwdReturn: 5},
	}
	// No exclusion: the feature-nearest day (Ts=1000) leads.
	base := Find(current, history, 3, 3, 0, 0)
	if base.Gated || base.Analogs[0].Ts != 1000 {
		t.Fatalf("without exclusion the nearest should be Ts=1000, got gated=%v %+v", base.Gated, base.Analogs)
	}
	// Exclude everything within 100s of Ts=1000 => drops both 1000 and 1050.
	res := Find(current, history, 3, 3, 1000, 100)
	if res.Gated {
		t.Fatalf("unexpected gate: %s", res.Note)
	}
	for _, a := range res.Analogs {
		if a.Ts == 1000 || a.Ts == 1050 {
			t.Fatalf("Ts=%d is inside the exclude window and must be dropped", a.Ts)
		}
	}
	if res.Analogs[0].Ts != 5000 {
		t.Fatalf("after exclusion the nearest should be Ts=5000, got Ts=%d", res.Analogs[0].Ts)
	}
}

// A zero-variance feature dimension is skipped, so it produces no NaNs; the one
// live dimension still ranks the analogs (exact match => distance 0).
func TestFind_ZeroVarianceDimNoNaN(t *testing.T) {
	current := []float64{5, 100} // second dim is constant everywhere
	history := []Snapshot{
		{Ts: 1, Features: []float64{1, 100}, FwdReturn: 1},
		{Ts: 2, Features: []float64{2, 100}, FwdReturn: -1},
		{Ts: 3, Features: []float64{5, 100}, FwdReturn: 3}, // dim0 == current
		{Ts: 4, Features: []float64{9, 100}, FwdReturn: 2},
	}
	res := Find(current, history, 3, 3, 0, 0)
	if res.Gated {
		t.Fatalf("one live dimension is enough; unexpected gate: %s", res.Note)
	}
	if len(res.Analogs) == 0 {
		t.Fatal("expected analogs")
	}
	for _, a := range res.Analogs {
		if math.IsNaN(a.Distance) || math.IsInf(a.Distance, 0) {
			t.Fatalf("distance for Ts=%d is not finite: %v", a.Ts, a.Distance)
		}
	}
	if math.IsNaN(res.MeanFwd) || math.IsNaN(res.MedianFwd) || math.IsNaN(res.HitRate) {
		t.Fatalf("summary stats must be finite: mean=%v median=%v hit=%v", res.MeanFwd, res.MedianFwd, res.HitRate)
	}
	if res.Analogs[0].Ts != 3 || res.Analogs[0].Distance != 0 {
		t.Fatalf("exact match on the live dim should be distance 0 at Ts=3, got Ts=%d dist=%v", res.Analogs[0].Ts, res.Analogs[0].Distance)
	}
}

// When every dimension is constant there is no similarity signal at all, so the
// lookup gates rather than returning distance-0 ties for everyone.
func TestFind_AllConstantDimensionsGated(t *testing.T) {
	current := []float64{5, 5}
	history := []Snapshot{
		{Ts: 1, Features: []float64{7, 7}, FwdReturn: 1},
		{Ts: 2, Features: []float64{7, 7}, FwdReturn: 2},
		{Ts: 3, Features: []float64{7, 7}, FwdReturn: 3},
	}
	res := Find(current, history, 2, 3, 0, 0)
	if !res.Gated {
		t.Fatal("all-constant features carry no similarity signal and must gate")
	}
	if len(res.Analogs) != 0 {
		t.Fatalf("gated result should have no analogs, got %d", len(res.Analogs))
	}
}

// Snapshots whose feature length differs from the query are skipped, not fatal:
// the matching ones still drive the lookup and N counts only them.
func TestFind_PartialMismatchUsesMatching(t *testing.T) {
	current := []float64{0, 0}
	history := []Snapshot{
		{Ts: 1, Features: []float64{0.1, 0}, FwdReturn: 1},
		{Ts: 2, Features: []float64{9, 9, 9}, FwdReturn: 99}, // wrong length -> skipped
		{Ts: 3, Features: []float64{5, 0}, FwdReturn: 2},
		{Ts: 4, Features: []float64{0, 5}, FwdReturn: 3},
	}
	res := Find(current, history, 2, 3, 0, 0)
	if res.Gated {
		t.Fatalf("3 usable snapshots >= minHistory 3 should not gate: %s", res.Note)
	}
	if res.N != 3 {
		t.Fatalf("N should be 3 (the length-matched snapshots), got %d", res.N)
	}
	for _, a := range res.Analogs {
		if a.Ts == 2 {
			t.Fatal("the wrong-length snapshot (Ts=2) must never appear as an analog")
		}
	}
}

// All snapshots mismatch the query length => nothing usable => gated with N=0.
func TestFind_AllMismatchGated(t *testing.T) {
	current := []float64{1, 2}
	history := []Snapshot{
		{Ts: 1, Features: []float64{1, 2, 3}, FwdReturn: 1},
		{Ts: 2, Features: []float64{5}, FwdReturn: 2},
	}
	res := Find(current, history, 2, 1, 0, 0)
	if !res.Gated {
		t.Fatal("all snapshots mismatch the query length => must gate")
	}
	if res.N != 0 {
		t.Fatalf("no usable snapshots => N=0, got %d", res.N)
	}
}

// Bad query vectors and non-positive k gate up front.
func TestFind_BadInputsGate(t *testing.T) {
	history := []Snapshot{
		{Ts: 1, Features: []float64{1, 2}, FwdReturn: 1},
		{Ts: 2, Features: []float64{3, 4}, FwdReturn: 2},
		{Ts: 3, Features: []float64{5, 6}, FwdReturn: 3},
	}
	if res := Find(nil, history, 2, 1, 0, 0); !res.Gated {
		t.Fatal("empty query vector should gate")
	}
	if res := Find([]float64{1, math.NaN()}, history, 2, 1, 0, 0); !res.Gated {
		t.Fatal("non-finite query vector should gate")
	}
	if res := Find([]float64{1, 2}, history, 0, 1, 0, 0); !res.Gated {
		t.Fatal("k=0 should gate")
	}
}

// k larger than the candidate pool is clamped, not an error.
func TestFind_ClampsK(t *testing.T) {
	current := []float64{0, 0}
	history := []Snapshot{
		{Ts: 1, Features: []float64{1, 0}, FwdReturn: 1},
		{Ts: 2, Features: []float64{0, 1}, FwdReturn: 2},
		{Ts: 3, Features: []float64{2, 2}, FwdReturn: 3},
	}
	res := Find(current, history, 99, 3, 0, 0)
	if res.Gated {
		t.Fatalf("unexpected gate: %s", res.Note)
	}
	if len(res.Analogs) != 3 {
		t.Fatalf("k should clamp to the 3 available candidates, got %d", len(res.Analogs))
	}
}

// aggregate: even-count median averages the two middles, odd-count takes the
// middle, and a zero forward return is NOT counted as a hit (strictly > 0).
func TestAggregate_MedianAndHitRate(t *testing.T) {
	mean, median, hit := aggregate([]Analog{{FwdReturn: 1}, {FwdReturn: 2}, {FwdReturn: 3}, {FwdReturn: 10}})
	if mean != 4.0 {
		t.Fatalf("mean = %v, want 4.0", mean)
	}
	if median != 2.5 {
		t.Fatalf("even-count median = %v, want 2.5", median)
	}
	if hit != 1.0 {
		t.Fatalf("hit rate = %v, want 1.0", hit)
	}
	_, median2, hit2 := aggregate([]Analog{{FwdReturn: 0}, {FwdReturn: -1}, {FwdReturn: 3}})
	if median2 != 0 {
		t.Fatalf("odd-count median = %v, want 0", median2)
	}
	if math.Abs(hit2-1.0/3.0) > 1e-9 {
		t.Fatalf("hit rate = %v, want 1/3 (zero is not a positive return)", hit2)
	}
}
