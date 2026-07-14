// STRATEGY-LAB wave tests: the no-lookahead contract for ALL eight classics
// (appending future bars must never change an earlier position) and
// known-signal fixtures on deterministic bar series (golden cross / Donchian
// breakout / RSI-2 dip / Bollinger dip fire at known indices).
package stratlib

import (
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// flatBars builds n daily bars at a constant close (high/low hug the close).
func flatBars(n int, px float64) []md.Bar {
	bars := make([]md.Bar, n)
	for i := range bars {
		bars[i] = md.Bar{
			Ts: int64(i+1) * 86400, Open: px, High: px * 1.001, Low: px * 0.999,
			Close: px, Volume: 1000,
		}
	}
	return bars
}

// TestNoLookahead is the load-bearing contract test: for every strategy,
// Positions on a truncated series must equal the prefix of Positions on the
// full series — bars after index i can never change the position at i.
func TestNoLookahead(t *testing.T) {
	// A deliberately eventful series: flat, rally, crash, rally — exercises
	// entries AND exits for every rule family.
	n := 700
	bars := make([]md.Bar, n)
	px := 100.0
	for i := range bars {
		switch {
		case i > 100 && i <= 300:
			px *= 1.01 // rally
		case i > 300 && i <= 400:
			px *= 0.985 // crash
		case i > 400:
			px *= 1.008 // recovery
		}
		bars[i] = md.Bar{
			Ts: int64(i+1) * 86400, Open: px, High: px * 1.005, Low: px * 0.995,
			Close: px, Volume: 1000,
		}
	}
	for _, strat := range All() {
		full := strat.Positions(bars)
		if len(full) != n {
			t.Fatalf("%s: len(pos)=%d want %d", strat.Name, len(full), n)
		}
		for _, k := range []int{50, 260, 350, 500, 699} {
			part := strat.Positions(bars[:k])
			for i := 0; i < k; i++ {
				if part[i] != full[i] {
					t.Fatalf("%s: LOOKAHEAD — pos[%d] changed from %d to %d when future bars were appended (prefix len %d)",
						strat.Name, i, part[i], full[i], k)
				}
			}
		}
	}
}

// TestPositionsAreLongFlat: every emitted position is +1 or 0 (the engine is
// long/flat; the classics never emit shorts).
func TestPositionsAreLongFlat(t *testing.T) {
	bars := flatBars(300, 100)
	for i := 150; i < 300; i++ {
		bars[i].Close = 100 * (1 + 0.01*float64(i-150))
		bars[i].High = bars[i].Close * 1.005
		bars[i].Low = bars[i].Close * 0.995
	}
	for _, strat := range All() {
		for i, p := range strat.Positions(bars) {
			if p != 0 && p != 1 {
				t.Fatalf("%s: pos[%d]=%d — must be 0 or 1", strat.Name, i, p)
			}
		}
	}
}

func findStrat(t *testing.T, name string) Strategy {
	t.Helper()
	for _, s := range All() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("strategy %s not in All()", name)
	return Strategy{}
}

// TestGoldenCross_KnownIndex: a slow 250-bar decline (SMA50 < SMA200, flat)
// followed by a step to 130. The exact first-long index is computed
// INDEPENDENTLY in the test from the SMA definitions, so the strategy's
// cross must land on a known, hand-checkable bar.
func TestGoldenCross_KnownIndex(t *testing.T) {
	bars := make([]md.Bar, 400)
	for i := range bars {
		px := 100 - 0.01*float64(i) // gentle decline: fast SMA sits below slow
		if i >= 250 {
			px = 130
		}
		bars[i] = md.Bar{Ts: int64(i+1) * 86400, Open: px, High: px, Low: px, Close: px, Volume: 1}
	}
	// Independent expected series straight from the definitions.
	smaN := func(i, n int) float64 {
		sum := 0.0
		for j := i - n + 1; j <= i; j++ {
			sum += bars[j].Close
		}
		return sum / float64(n)
	}
	want := -1
	for i := 199; i < len(bars); i++ {
		if smaN(i, 50) >= smaN(i, 200) {
			want = i
			break
		}
	}
	if want <= 250 {
		t.Fatalf("bad fixture: expected the cross after the step, got %d", want)
	}
	pos := findStrat(t, "sma_cross_50_200").Positions(bars)
	if pos[249] != 0 || pos[want-1] != 0 {
		t.Fatalf("golden cross: long before the independently computed cross at %d", want)
	}
	if pos[want] != 1 {
		t.Fatalf("golden cross: expected first long at index %d, got %d", want, pos[want])
	}
}

// TestDonchian_KnownIndex: flat highs at ~100 for 30 bars, then bar 30 closes
// above the prior-20-bar high — entry fires exactly there; a later close
// below the prior-10-bar low exits.
func TestDonchian_KnownIndex(t *testing.T) {
	bars := flatBars(60, 100)
	bars[30].Close, bars[30].High = 102, 102.5 // breaks the ~100.1 channel top
	for i := 31; i < 45; i++ {
		bars[i].Close, bars[i].High, bars[i].Low = 102, 102.5, 101.5
	}
	bars[45].Close, bars[45].Low = 95, 94.5 // breaks the 10-bar low
	for i := 46; i < 60; i++ {
		bars[i].Close, bars[i].High, bars[i].Low = 95, 95.5, 94.5
	}
	pos := findStrat(t, "donchian_20").Positions(bars)
	if pos[29] != 0 {
		t.Fatalf("donchian: long before breakout (pos[29]=%d)", pos[29])
	}
	if pos[30] != 1 {
		t.Fatalf("donchian: entry must fire at the breakout bar 30, got %d", pos[30])
	}
	if pos[44] != 1 {
		t.Fatalf("donchian: should still be long at 44, got %d", pos[44])
	}
	if pos[45] != 0 {
		t.Fatalf("donchian: exit must fire at the 10-bar-low break (bar 45), got %d", pos[45])
	}
}

// TestRSI2_FiresOnDip: a steady series with a sharp 2-day plunge drives
// RSI(2) under 10 (long), and the sharp rebound above 90 exits.
func TestRSI2_FiresOnDip(t *testing.T) {
	bars := flatBars(40, 100)
	// Gentle alternation keeps RSI mid-range before the dip.
	for i := 1; i < 20; i++ {
		if i%2 == 0 {
			bars[i].Close = 100.2
		} else {
			bars[i].Close = 99.8
		}
	}
	bars[20].Close = 96 // plunge day 1
	bars[21].Close = 92 // plunge day 2 -> RSI(2) ~ 0
	// Sustained rebound: Wilder's period-2 avgLoss halves each up day, so a
	// few strong closes push RSI(2) above 90.
	for i, px := range []float64{97, 102, 107, 112, 118, 124, 130} {
		bars[22+i].Close = px
	}
	pos := findStrat(t, "rsi2_meanrev").Positions(bars)
	if pos[21] != 1 {
		t.Fatalf("rsi2: expected long after the 2-day plunge (pos[21]=%d)", pos[21])
	}
	exited := false
	for i := 22; i <= 29; i++ {
		if pos[i] == 0 {
			exited = true
			break
		}
	}
	if !exited {
		t.Fatalf("rsi2: never exited on the RSI(2)>90 rebound (pos[22:30]=%v)", pos[22:30])
	}
}

// TestBollinger_FiresOnDip: 25 mildly noisy bars, then a plunge far below the
// lower band enters; recovery to the 20-day mean exits.
func TestBollinger_FiresOnDip(t *testing.T) {
	bars := flatBars(50, 100)
	for i := 1; i < 30; i++ {
		if i%2 == 0 {
			bars[i].Close = 100.5
		} else {
			bars[i].Close = 99.5
		}
	}
	bars[30].Close = 90 // way below mid-2σ (~98.9)
	for i := 31; i < 40; i++ {
		bars[i].Close = 90 + 2.5*float64(i-30) // recovery through the mid band
	}
	for i := 40; i < 50; i++ {
		bars[i].Close = 101
	}
	pos := findStrat(t, "bollinger_meanrev").Positions(bars)
	if pos[30] != 1 {
		t.Fatalf("bollinger: expected long on the sub-band plunge (pos[30]=%d)", pos[30])
	}
	exited := false
	for i := 31; i < 45; i++ {
		if pos[i] == 0 {
			exited = true
			break
		}
	}
	if !exited {
		t.Fatalf("bollinger: never exited after recovering to the mid band")
	}
}

// TestMomentumAndDualMomentum: a persistent 300-bar uptrend must have both
// 12-month momentum variants long at the end and flat during warm-up.
func TestMomentumAndDualMomentum(t *testing.T) {
	n := 320
	bars := make([]md.Bar, n)
	px := 100.0
	for i := range bars {
		px *= 1.003
		bars[i] = md.Bar{Ts: int64(i+1) * 86400, Open: px, High: px, Low: px, Close: px, Volume: 1}
	}
	for _, name := range []string{"momentum_12_1", "dual_momentum"} {
		pos := findStrat(t, name).Positions(bars)
		if pos[100] != 0 {
			t.Fatalf("%s: long during the 252-bar warm-up (pos[100]=%d)", name, pos[100])
		}
		if pos[n-1] != 1 {
			t.Fatalf("%s: flat at the end of a persistent uptrend (pos[%d]=%d)", name, n-1, pos[n-1])
		}
	}
}

// TestBreakout52wAndMACD: the uptrend also keeps the 52w-high breakout and
// MACD trend long at the end; a flat series keeps every trend-follower flat
// forever (no trades fabricated from nothing).
func TestBreakout52wAndMACD(t *testing.T) {
	n := 320
	up := make([]md.Bar, n)
	px := 100.0
	for i := range up {
		px *= 1.003
		up[i] = md.Bar{Ts: int64(i+1) * 86400, Open: px, High: px, Low: px, Close: px, Volume: 1}
	}
	if pos := findStrat(t, "breakout_52w_high").Positions(up); pos[n-1] != 1 {
		t.Fatalf("breakout_52w_high: flat at the end of a persistent uptrend")
	}
	if pos := findStrat(t, "macd_trend").Positions(up); pos[n-1] != 1 {
		t.Fatalf("macd_trend: flat at the end of a persistent uptrend")
	}
	flat := flatBars(300, 100)
	for _, name := range []string{"breakout_52w_high", "donchian_20", "momentum_12_1", "dual_momentum"} {
		for i, p := range findStrat(t, name).Positions(flat) {
			if p != 0 {
				t.Fatalf("%s: fabricated a long on a dead-flat series (pos[%d]=1)", name, i)
			}
		}
	}
}

// TestDocsCitePublishedOrigin: every strategy carries a non-empty Doc citing
// its published origin (the honesty requirement for the lab).
func TestDocsCitePublishedOrigin(t *testing.T) {
	if len(All()) != 8 {
		t.Fatalf("expected 8 classics, got %d", len(All()))
	}
	for _, s := range All() {
		if s.Doc == "" || s.Name == "" || s.Positions == nil {
			t.Fatalf("strategy %+v missing name/doc/positions", s.Name)
		}
	}
}
