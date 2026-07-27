package ev

import "testing"

// goodInputs is a candidate that should BUY under default thresholds: strong
// probability, positive cost-adjusted EV, thin no-trade zone, modest tail,
// execution cost inside the band.
func goodInputs() Inputs {
	return Inputs{
		Symbol: "AAA", Horizon: "1d",
		CalProb: 0.80, HasProb: true,
		DistMean: 0.012, Tau: 0.002, Sigma: 0.02, PInside: 0.30,
		DistEV: 0.010, DistN: 200, HasDist: true,
		Regime: "calm", HasRegime: true,
		TailP90: 0.03, TailN: 100, HasTail: true,
		ADVUSD: 5e7, CapacityUSD: 2.5e6, HasLiquidity: true,
		RoundTripCostFrac: 0.0015, HasCost: true,
	}
}

func decideRanked(in Inputs, th Thresholds) Decision {
	ranked := RankByNetEV([]Assessment{Assess(in)})
	return Decide(ranked[0], EnterLong, th)
}

func TestDecide_BuyWhenNetEVPositive(t *testing.T) {
	d := decideRanked(goodInputs(), DefaultThresholds())
	if d.Action != BUY || d.Reason != ReasonPositiveNetEV {
		t.Fatalf("want BUY/positive-net-ev, got %s/%s", d.Action, d.Reason)
	}
}

// The whole point of the engine: a HIGH probability whose economics are
// negative (cost exceeds edge) must refuse where the bare threshold traded.
func TestDecide_RefusesHighProbNegativeNetEV(t *testing.T) {
	in := goodInputs()
	in.CalProb = 0.95                          // the old gate's dream candidate
	in.DistEV = 0.0004                         // barely positive after tau...
	in.RoundTripCostFrac = in.Tau + 0.0010     // ...but this fill costs 10bps beyond the band
	d := decideRanked(in, DefaultThresholds()) // NetEV = 0.0004 − 0.0010 < 0
	if d.Action != DO_NOTHING || d.Reason != ReasonNetEVBelowFloor {
		t.Fatalf("want DO_NOTHING/net-ev-below-floor, got %s/%s", d.Action, d.Reason)
	}
}

func TestDecide_RefusesInsideNoTradeZone(t *testing.T) {
	in := goodInputs()
	in.PInside = 0.80 // distribution says the move overwhelmingly stays inside the cost band
	d := decideRanked(in, DefaultThresholds())
	if d.Action != DO_NOTHING || d.Reason != ReasonNoTradeZone {
		t.Fatalf("want DO_NOTHING/inside-no-trade-zone, got %s/%s", d.Action, d.Reason)
	}
}

func TestDecide_RefusesFatTail(t *testing.T) {
	in := goodInputs()
	in.TailP90 = 0.15
	d := decideRanked(in, DefaultThresholds())
	if d.Action != DO_NOTHING || d.Reason != ReasonTailTooFat {
		t.Fatalf("want DO_NOTHING/tail-too-fat, got %s/%s", d.Action, d.Reason)
	}
}

// A tail that was WITHHELD (thin episode sample) is not a fat tail: the
// candidate must not be refused on an advisory input that was not measured.
func TestDecide_WithheldTailDoesNotRefuse(t *testing.T) {
	in := goodInputs()
	in.HasTail, in.TailP90 = false, 0
	d := decideRanked(in, DefaultThresholds())
	if d.Action != BUY {
		t.Fatalf("withheld tail must not refuse: got %s/%s", d.Action, d.Reason)
	}
}

// Missing REQUIRED inputs refuse, never default: each of probability,
// distribution, and execution cost absent must produce the missing-input
// refusal even when every present number looks excellent.
func TestDecide_MissingRequiredInputRefuses(t *testing.T) {
	cases := map[string]func(*Inputs){
		"probability":  func(in *Inputs) { in.HasProb = false },
		"distribution": func(in *Inputs) { in.HasDist = false },
		"cost":         func(in *Inputs) { in.HasCost = false },
	}
	for name, strip := range cases {
		in := goodInputs()
		strip(&in)
		d := decideRanked(in, DefaultThresholds())
		if d.Action != DO_NOTHING || d.Reason != ReasonMissingInput {
			t.Errorf("%s missing: want DO_NOTHING/missing-required-input, got %s/%s", name, d.Action, d.Reason)
		}
	}
}

func TestDecide_ExitNeverBlocked(t *testing.T) {
	// The worst assessable candidate: everything missing, everything ugly.
	d := Decide(Assess(Inputs{}), ExitLong, DefaultThresholds())
	if d.Action != SELL || d.Reason != ReasonExitNeverBlocked {
		t.Fatalf("exit must be unconditional: got %s/%s", d.Action, d.Reason)
	}
}

func TestRankByNetEV_OrdersAndStampsOpportunityCost(t *testing.T) {
	lo, mid, hi := goodInputs(), goodInputs(), goodInputs()
	lo.Symbol, lo.DistEV = "LO", 0.002
	mid.Symbol, mid.DistEV = "MID", 0.005
	hi.Symbol, hi.DistEV = "HI", 0.020
	noEV := goodInputs()
	noEV.Symbol, noEV.HasDist = "NOEV", false // unmeasurable — must sort last

	ranked := RankByNetEV([]Assessment{Assess(lo), Assess(mid), Assess(noEV), Assess(hi)})
	want := []string{"HI", "MID", "LO", "NOEV"}
	for i, s := range want {
		if ranked[i].Symbol != s {
			t.Fatalf("rank %d = %s, want %s (full: %+v)", i+1, ranked[i].Symbol, s, ranked)
		}
		if ranked[i].Rank != i+1 || ranked[i].RankOf != 4 {
			t.Fatalf("%s stamped rank %d/%d, want %d/4", s, ranked[i].Rank, ranked[i].RankOf, i+1)
		}
	}
}

// The opportunity-cost refusal: an otherwise-tradeable candidate ranked past
// MaxRank is refused as outranked — capital belongs to better EV first.
func TestDecide_OutrankedRefusal(t *testing.T) {
	th := DefaultThresholds()
	th.MaxRank = 1
	a, b := goodInputs(), goodInputs()
	a.Symbol, a.DistEV = "BEST", 0.020
	b.Symbol, b.DistEV = "SECOND", 0.010
	ranked := RankByNetEV([]Assessment{Assess(a), Assess(b)})
	if d := Decide(ranked[0], EnterLong, th); d.Action != BUY {
		t.Fatalf("rank 1 should BUY, got %s/%s", d.Action, d.Reason)
	}
	if d := Decide(ranked[1], EnterLong, th); d.Action != DO_NOTHING || d.Reason != ReasonOutranked {
		t.Fatalf("rank 2 should be outranked, got %s/%s", d.Action, d.Reason)
	}
}

// Assess must not double-charge the cost band: execution cost inside tau adds
// nothing, cost beyond tau subtracts only the excess.
func TestAssess_NetEVChargesOnlyCostBeyondTau(t *testing.T) {
	in := goodInputs() // cost 0.0015 < tau 0.002
	a := Assess(in)
	if !a.HasNetEV || a.NetEV != in.DistEV {
		t.Fatalf("cost inside tau: NetEV=%v want %v", a.NetEV, in.DistEV)
	}
	in.RoundTripCostFrac = 0.0030 // 10bps beyond the band
	a = Assess(in)
	want := in.DistEV - 0.0010
	if diff := a.NetEV - want; diff > 1e-12 || diff < -1e-12 {
		t.Fatalf("cost beyond tau: NetEV=%v want %v", a.NetEV, want)
	}
}
