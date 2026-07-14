package rebalance

import (
	"math"
	"testing"
)

const (
	tol      = 1e-9
	yearSecs = int64(365 * 24 * 3600) // holding-period boundary used throughout
	nowTs    = int64(2_000_000_000)   // fixed "now" so tests are deterministic
)

func approx(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// daysAgo returns the unix timestamp d days before nowTs.
func daysAgo(d int64) int64 { return nowTs - d*24*3600 }

// findTrade returns the first trade for sym, or a zero Trade with found=false.
func findTrade(trades []Trade, sym string) (Trade, bool) {
	for _, tr := range trades {
		if tr.Symbol == sym {
			return tr, true
		}
	}
	return Trade{}, false
}

// planHasNaN reports whether any numeric field of the plan or its trades is
// NaN or Inf.
func planHasNaN(p Plan) bool {
	vals := []float64{p.ShortTermGain, p.LongTermGain, p.TotalRealizedGain, p.EstTax, p.TotalCost, p.TurnoverPct}
	for _, tr := range p.Trades {
		vals = append(vals, tr.Shares, tr.Notional, tr.Cost, tr.RealizedGain)
	}
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return true
		}
	}
	return false
}

// --- (1) over target -> sell, under target -> buy ---------------------------

func TestBuildPlanBuyAndSell(t *testing.T) {
	// equity 10,000, weights already sum to 1 (no normalization).
	//   A: 60 sh @ mark 100 = 6,000, target 0.5 -> 5,000 -> sell 1,000/100 = 10 sh
	//   B: 20 sh @ mark 100 = 2,000, target 0.5 -> 5,000 -> buy  3,000/100 = 30 sh
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{{Shares: 60, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
		{Symbol: "B", Price: 100, Lots: []Lot{{Shares: 20, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.5}}

	p := BuildPlan(positions, targets, 10000, nowTs, yearSecs, 0.35, 0.15, 0)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	if len(p.Trades) != 2 {
		t.Fatalf("len(Trades) = %d, want 2 (%+v)", len(p.Trades), p.Trades)
	}

	a, ok := findTrade(p.Trades, "A")
	if !ok || a.Side != SideSell {
		t.Fatalf("A trade = %+v, want a sell", a)
	}
	if !approx(a.Shares, 10, tol) {
		t.Errorf("A sell shares = %v, want 10", a.Shares)
	}

	b, ok := findTrade(p.Trades, "B")
	if !ok || b.Side != SideBuy {
		t.Fatalf("B trade = %+v, want a buy", b)
	}
	if !approx(b.Shares, 30, tol) {
		t.Errorf("B buy shares = %v, want 30", b.Shares)
	}
	// Buys never realize a gain.
	if b.RealizedGain != 0 {
		t.Errorf("B buy RealizedGain = %v, want 0", b.RealizedGain)
	}
}

// --- (2) FIFO realized gain across two lots ---------------------------------

func TestBuildPlanFIFOGain(t *testing.T) {
	// A: two lots, 10 @ 40 then 10 @ 60 (oldest first), mark 100 -> value 2,000.
	// B: 10 @ 100, mark 100 -> value 1,000. equity 4,000 (weights sum to 1).
	//   A target 0.125 -> 500 -> sell 1,500/100 = 15 sh.
	//   FIFO: 10 from lot@40 -> (100-40)*10 = 600
	//          5 from lot@60 -> (100-60)*5  = 200  => realized 800.
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{
			{Shares: 10, CostBasis: 40, AcquiredTs: daysAgo(800)},
			{Shares: 10, CostBasis: 60, AcquiredTs: daysAgo(800)},
		}},
		{Symbol: "B", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 100, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 0.125}, {Symbol: "B", Weight: 0.875}}

	p := BuildPlan(positions, targets, 4000, nowTs, yearSecs, 0.35, 0.15, 0)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	a, ok := findTrade(p.Trades, "A")
	if !ok || a.Side != SideSell {
		t.Fatalf("A trade = %+v, want a sell", a)
	}
	if !approx(a.Shares, 15, tol) {
		t.Errorf("A sell shares = %v, want 15", a.Shares)
	}
	if !approx(a.RealizedGain, 800, tol) {
		t.Errorf("A RealizedGain = %v, want 800 (FIFO 600+200)", a.RealizedGain)
	}
	// Both lots are >1y old, so the whole gain is long-term.
	if !approx(p.LongTermGain, 800, tol) || !approx(p.ShortTermGain, 0, tol) {
		t.Errorf("gains short=%v long=%v, want short=0 long=800", p.ShortTermGain, p.LongTermGain)
	}
	if !approx(p.TotalRealizedGain, 800, tol) {
		t.Errorf("TotalRealizedGain = %v, want 800", p.TotalRealizedGain)
	}
}

// --- (3) short-term vs long-term classification -----------------------------

func TestBuildPlanShortVsLongTerm(t *testing.T) {
	// A: lot1 10 @ 50 acquired 400d ago (long), lot2 10 @ 50 acquired 100d ago
	// (short). mark 100 -> value 2,000. Sell 15 sh (A target 0.125 of 4,000=500).
	//   FIFO: 10 from long lot  -> (100-50)*10 = 500 long
	//          5 from short lot -> (100-50)*5  = 250 short  => any short = true.
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{
			{Shares: 10, CostBasis: 50, AcquiredTs: daysAgo(400)},
			{Shares: 10, CostBasis: 50, AcquiredTs: daysAgo(100)},
		}},
		{Symbol: "B", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 100, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 0.125}, {Symbol: "B", Weight: 0.875}}

	p := BuildPlan(positions, targets, 4000, nowTs, yearSecs, 0.35, 0.15, 0)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	a, _ := findTrade(p.Trades, "A")
	if !approx(a.Shares, 15, tol) {
		t.Fatalf("A sell shares = %v, want 15", a.Shares)
	}
	if !a.ShortTerm {
		t.Error("A.ShortTerm = false, want true (a short-term lot was matched)")
	}
	if !approx(p.ShortTermGain, 250, tol) {
		t.Errorf("ShortTermGain = %v, want 250", p.ShortTermGain)
	}
	if !approx(p.LongTermGain, 500, tol) {
		t.Errorf("LongTermGain = %v, want 500", p.LongTermGain)
	}

	// Boundary check: a lot exactly at the holding boundary is long-term
	// (classification is strict < holdingSecs for short-term).
	if nowTs-daysAgo(365) >= yearSecs {
		// 365 days == yearSecs exactly -> not < boundary -> long-term. Confirm
		// via a single-lot sell of exactly-boundary age.
		bp := BuildPlan(
			[]Position{
				{Symbol: "X", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 50, AcquiredTs: nowTs - yearSecs}}},
				{Symbol: "Y", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 100, AcquiredTs: daysAgo(800)}}},
			},
			[]Target{{Symbol: "X", Weight: 0}, {Symbol: "Y", Weight: 1}},
			2000, nowTs, yearSecs, 0.35, 0.15, 0,
		)
		x, _ := findTrade(bp.Trades, "X")
		if x.ShortTerm {
			t.Error("lot aged exactly holdingSecs classified short-term; want long-term")
		}
	}
}

// --- (4) EstTax, with a net loss in one class taxed at zero -----------------

func TestBuildPlanEstTaxWithClassLoss(t *testing.T) {
	// A: long lot 10 @ 130 (400d, a LOSS at mark 100) then short lot 10 @ 50
	// (100d, a gain). mark 100 -> value 2,000. Sell all (A target 0).
	//   FIFO: long 10 -> (100-130)*10 = -300 (long loss)
	//         short 10 -> (100-50)*10  = +500 (short gain)
	//   EstTax = max(0,500)*0.35 + max(0,-300)*0.15 = 175 + 0 = 175.
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{
			{Shares: 10, CostBasis: 130, AcquiredTs: daysAgo(400)},
			{Shares: 10, CostBasis: 50, AcquiredTs: daysAgo(100)},
		}},
		{Symbol: "B", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 100, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 0}, {Symbol: "B", Weight: 1}}

	p := BuildPlan(positions, targets, 3000, nowTs, yearSecs, 0.35, 0.15, 0)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	if !approx(p.ShortTermGain, 500, tol) {
		t.Errorf("ShortTermGain = %v, want 500", p.ShortTermGain)
	}
	if !approx(p.LongTermGain, -300, tol) {
		t.Errorf("LongTermGain = %v, want -300 (a reported loss)", p.LongTermGain)
	}
	if !approx(p.TotalRealizedGain, 200, tol) {
		t.Errorf("TotalRealizedGain = %v, want 200", p.TotalRealizedGain)
	}
	if !approx(p.EstTax, 175, tol) {
		t.Errorf("EstTax = %v, want 175 (500*0.35 + 0 on the long loss)", p.EstTax)
	}
}

// --- (5) turnover and transaction cost --------------------------------------

func TestBuildPlanTurnoverAndCost(t *testing.T) {
	// Same book as TestBuildPlanBuyAndSell but with a 10 bps cost.
	//   A sell notional 1,000, B buy notional 3,000 -> traded 4,000.
	//   turnover = 4,000/10,000 * 100 = 40%.
	//   cost = 4,000 * 10/10000 = 4.0 (A 1.0 + B 3.0).
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{{Shares: 60, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
		{Symbol: "B", Price: 100, Lots: []Lot{{Shares: 20, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 0.5}, {Symbol: "B", Weight: 0.5}}

	p := BuildPlan(positions, targets, 10000, nowTs, yearSecs, 0.35, 0.15, 10)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	if !approx(p.TurnoverPct, 40, tol) {
		t.Errorf("TurnoverPct = %v, want 40", p.TurnoverPct)
	}
	if !approx(p.TotalCost, 4, tol) {
		t.Errorf("TotalCost = %v, want 4.0", p.TotalCost)
	}
	a, _ := findTrade(p.Trades, "A")
	b, _ := findTrade(p.Trades, "B")
	if !approx(a.Cost, 1, tol) {
		t.Errorf("A.Cost = %v, want 1.0", a.Cost)
	}
	if !approx(b.Cost, 3, tol) {
		t.Errorf("B.Cost = %v, want 3.0", b.Cost)
	}
}

// --- (6) a symbol held but absent from targets is fully sold ----------------

func TestBuildPlanAbsentTargetFullySold(t *testing.T) {
	// A held & targeted, B held but NOT in targets -> B gets weight 0 -> sold out.
	//   equity 3,000; A target 1.0 -> 3,000, A value 2,000 -> buy 10 sh.
	//   B value 1,000, target 0 -> sell all 10 sh, gain (100-50)*10 = 500 long.
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{{Shares: 20, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
		{Symbol: "B", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 1.0}}

	p := BuildPlan(positions, targets, 3000, nowTs, yearSecs, 0.35, 0.15, 0)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	b, ok := findTrade(p.Trades, "B")
	if !ok || b.Side != SideSell {
		t.Fatalf("B trade = %+v, want a sell", b)
	}
	if !approx(b.Shares, 10, tol) {
		t.Errorf("B sold shares = %v, want 10 (fully liquidated)", b.Shares)
	}
	if !approx(b.RealizedGain, 500, tol) {
		t.Errorf("B RealizedGain = %v, want 500", b.RealizedGain)
	}
	a, ok := findTrade(p.Trades, "A")
	if !ok || a.Side != SideBuy || !approx(a.Shares, 10, tol) {
		t.Errorf("A trade = %+v, want buy 10", a)
	}
}

// --- (7) degenerate inputs -> empty plan + Note, no NaN ----------------------

func TestBuildPlanDegenerate(t *testing.T) {
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 1}}

	tests := []struct {
		name      string
		positions []Position
		targets   []Target
		equity    float64
	}{
		{"zero_equity", positions, targets, 0},
		{"negative_equity", positions, targets, -500},
		{"empty_everything", nil, nil, 10000},
		{"positions_but_no_targets", positions, nil, 10000},
		{"targets_sum_to_zero", positions, []Target{{Symbol: "A", Weight: 0}}, 10000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := BuildPlan(tc.positions, tc.targets, tc.equity, nowTs, yearSecs, 0.35, 0.15, 10)
			if len(p.Trades) != 0 {
				t.Errorf("Trades = %+v, want empty", p.Trades)
			}
			if p.Trades == nil {
				t.Error("Trades is nil, want an empty non-nil slice")
			}
			if p.Note == "" {
				t.Error("Note is empty, want a plain-English explanation")
			}
			if planHasNaN(p) {
				t.Errorf("degenerate plan contains NaN/Inf: %+v", p)
			}
			if p.EstTax != 0 || p.TurnoverPct != 0 || p.TotalRealizedGain != 0 {
				t.Errorf("degenerate plan not all-zero: %+v", p)
			}
		})
	}
}

// --- normalization: weights that do not sum to 1 are rescaled ----------------

func TestBuildPlanNormalizesWeights(t *testing.T) {
	// Weights 0.2/0.2 (sum 0.4) must normalize to 0.5/0.5, matching the sum-to-1
	// book in TestBuildPlanBuyAndSell (A sell 10, B buy 30).
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{{Shares: 60, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
		{Symbol: "B", Price: 100, Lots: []Lot{{Shares: 20, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 0.2}, {Symbol: "B", Weight: 0.2}}

	p := BuildPlan(positions, targets, 10000, nowTs, yearSecs, 0.35, 0.15, 0)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	a, _ := findTrade(p.Trades, "A")
	b, _ := findTrade(p.Trades, "B")
	if a.Side != SideSell || !approx(a.Shares, 10, tol) {
		t.Errorf("A trade = %+v, want sell 10 after normalization", a)
	}
	if b.Side != SideBuy || !approx(b.Shares, 30, tol) {
		t.Errorf("B trade = %+v, want buy 30 after normalization", b)
	}
	if p.Note == "" {
		t.Error("expected a Note recording that weights were normalized")
	}
}

// --- a target symbol with no priced position is skipped and noted ------------

func TestBuildPlanSkipsUnpricedTarget(t *testing.T) {
	// A is held & priced; GHOST is targeted but never priced. GHOST is skipped
	// (its 0.5 weight still dilutes the normalization) and A is planned.
	positions := []Position{
		{Symbol: "A", Price: 100, Lots: []Lot{{Shares: 10, CostBasis: 50, AcquiredTs: daysAgo(800)}}},
	}
	targets := []Target{{Symbol: "A", Weight: 0.5}, {Symbol: "GHOST", Weight: 0.5}}

	p := BuildPlan(positions, targets, 4000, nowTs, yearSecs, 0.35, 0.15, 0)
	if planHasNaN(p) {
		t.Fatalf("plan contains NaN/Inf: %+v", p)
	}
	if _, ok := findTrade(p.Trades, "GHOST"); ok {
		t.Error("GHOST produced a trade, want it skipped for lack of a price")
	}
	// A normalizes to 0.5 -> target 2,000; value 1,000 -> buy 1,000/100 = 10 sh.
	a, ok := findTrade(p.Trades, "A")
	if !ok || a.Side != SideBuy || !approx(a.Shares, 10, tol) {
		t.Errorf("A trade = %+v, want buy 10", a)
	}
	if p.Note == "" {
		t.Error("expected a Note recording the skipped symbol")
	}
}
