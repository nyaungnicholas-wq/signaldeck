package micro

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func snap(ts int64, bid, ask, mid, wmid, imb, spread float64) marketdata.Snap1s {
	return marketdata.Snap1s{Ts: ts, Bid: bid, Ask: ask, Mid: mid, WMid: wmid, ImbSigned: imb, Spread: spread}
}

func TestExtract_TooFewSnapsIsAbsent(t *testing.T) {
	snaps := []marketdata.Snap1s{
		snap(1, 99, 101, 100, 100, 0.1, 2),
		snap(2, 99, 101, 100, 100, 0.1, 2),
	}
	if _, ok := Extract(snaps); ok {
		t.Fatal("fewer than MinSnaps should be absent (ok=false)")
	}
}

func TestExtract_ImbalanceMeanAndLast(t *testing.T) {
	// Steady bid-heavy book, then a final strongly ask-heavy snapshot.
	snaps := []marketdata.Snap1s{
		snap(1, 99, 101, 100, 100.05, 0.2, 2),
		snap(2, 99, 101, 100, 100.05, 0.2, 2),
		snap(3, 99, 101, 100, 100.05, 0.2, 2),
		snap(4, 99, 101, 100, 100.05, 0.2, 2),
		snap(5, 99, 101, 100, 99.95, -0.6, 2),
	}
	f, ok := Extract(snaps)
	if !ok {
		t.Fatal("expected ok")
	}
	wantMean := (0.2*4 - 0.6) / 5.0
	if math.Abs(f.Imbalance-wantMean) > 1e-9 {
		t.Fatalf("mean imbalance = %.4f, want %.4f", f.Imbalance, wantMean)
	}
	if math.Abs(f.ImbalanceLast-(-0.6)) > 1e-9 {
		t.Fatalf("last imbalance = %.4f, want -0.6", f.ImbalanceLast)
	}
}

func TestExtract_SpreadAndWmidBps(t *testing.T) {
	// mid=100, spread=2 => 200 bps; wmid-mid = +0.05 => +5 bps.
	snaps := make([]marketdata.Snap1s, 6)
	for i := range snaps {
		snaps[i] = snap(int64(i+1), 99, 101, 100, 100.05, 0.1, 2)
	}
	f, ok := Extract(snaps)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(f.SpreadBps-200) > 1e-6 {
		t.Fatalf("spread bps = %.4f, want 200", f.SpreadBps)
	}
	if math.Abs(f.WmidMidBps-5) > 1e-6 {
		t.Fatalf("wmid-mid bps = %.4f, want 5", f.WmidMidBps)
	}
}

func TestExtract_SpreadFallsBackToAskBid(t *testing.T) {
	// spread field zero but ask/bid present => derive from ask-bid.
	snaps := make([]marketdata.Snap1s, 6)
	for i := range snaps {
		snaps[i] = snap(int64(i+1), 99, 101, 100, 100, 0.0, 0)
	}
	f, ok := Extract(snaps)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(f.SpreadBps-200) > 1e-6 {
		t.Fatalf("fallback spread bps = %.4f, want 200 (ask-bid=2 over mid=100)", f.SpreadBps)
	}
}

func TestExtract_SignedVolDirectional(t *testing.T) {
	// Monotonically rising mid => signed flow should be strongly positive (+1),
	// because every mid move is up.
	snaps := make([]marketdata.Snap1s, 8)
	mid := 100.0
	for i := range snaps {
		snaps[i] = snap(int64(i+1), mid-1, mid+1, mid, mid, 0.0, 2)
		mid += 0.1
	}
	f, ok := Extract(snaps)
	if !ok {
		t.Fatal("expected ok")
	}
	if f.SignedVol < 0.99 {
		t.Fatalf("monotonic up mid should give signed_vol≈+1, got %.4f", f.SignedVol)
	}

	// Monotonically falling mid => strongly negative.
	mid = 100.0
	for i := range snaps {
		snaps[i] = snap(int64(i+1), mid-1, mid+1, mid, mid, 0.0, 2)
		mid -= 0.1
	}
	f, _ = Extract(snaps)
	if f.SignedVol > -0.99 {
		t.Fatalf("monotonic down mid should give signed_vol≈-1, got %.4f", f.SignedVol)
	}
}

func TestExtract_NoMidMovementZeroFlow(t *testing.T) {
	snaps := make([]marketdata.Snap1s, 6)
	for i := range snaps {
		snaps[i] = snap(int64(i+1), 99, 101, 100, 100, 0.1, 2)
	}
	f, ok := Extract(snaps)
	if !ok {
		t.Fatal("expected ok")
	}
	if f.SignedVol != 0 {
		t.Fatalf("flat mid should give zero signed flow, got %.4f", f.SignedVol)
	}
}

func TestExtract_ImbalanceClampedToUnit(t *testing.T) {
	// Corrupt out-of-range imbalance must be clamped, not propagated.
	snaps := make([]marketdata.Snap1s, 6)
	for i := range snaps {
		snaps[i] = snap(int64(i+1), 99, 101, 100, 100, 5.0, 2) // absurd imbalance
	}
	f, ok := Extract(snaps)
	if !ok {
		t.Fatal("expected ok")
	}
	if f.Imbalance != 1 || f.ImbalanceLast != 1 {
		t.Fatalf("imbalance should clamp to +1, got mean=%.4f last=%.4f", f.Imbalance, f.ImbalanceLast)
	}
}

func TestMap_HasAllKeys(t *testing.T) {
	f := Features{Imbalance: 0.1, ImbalanceLast: -0.2, SignedVol: 0.3, SpreadBps: 12, WmidMidBps: -1.5}
	m := f.Map()
	for _, k := range []string{"micro_imbalance", "micro_imbalance_last", "micro_signed_vol", "micro_spread_bps", "micro_wmid_mid_bps"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("Map missing key %q", k)
		}
	}
	if m["micro_imbalance"] != 0.1 || m["micro_spread_bps"] != 12 {
		t.Fatalf("Map values wrong: %+v", m)
	}
}
