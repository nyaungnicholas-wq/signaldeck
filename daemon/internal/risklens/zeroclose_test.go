package risklens

import (
	"errors"
	"math"
	"testing"
)

func nan() float64      { return math.NaN() }
func inf(s int) float64 { return math.Inf(s) }

// The latent panic the C7 agent found: alignSeries validated series LENGTH but
// never their CONTENT. DailyReturns returns nil for a series containing a zero
// close (the division is undefined), so a book whose SECOND holding carries a
// zero close produced rets = [[59 returns], nil]; PortfolioReturns then sized
// its loop from rets[0] and indexed rets[1][t] — index out of range.
//
// It is unreachable from today's live path (the ingest never stores a zero
// close), which is exactly why it needs a test: nothing else would catch the
// day it becomes reachable. The fix is validation at the boundary — reject the
// holding with a named error — not a defensive length check downstream.

func TestPortfolioReturns_RejectsZeroClose(t *testing.T) {
	good := make([]float64, MinCloses)
	bad := make([]float64, MinCloses)
	for i := range good {
		good[i] = 100 + float64(i)
		bad[i] = 100 + float64(i)
	}
	bad[7] = 0 // the poison: one zero close mid-series

	holdings := []Holding{{Symbol: "AAA", Weight: 0.5}, {Symbol: "BBB", Weight: 0.5}}
	series := []Series{{Symbol: "AAA", Closes: good}, {Symbol: "BBB", Closes: bad}}

	// Must not panic, and must not silently return a portfolio built from one
	// leg: a portfolio return series that quietly drops a holding is worse than
	// an error, because it renders as a real number.
	got, err := PortfolioReturns(holdings, series)
	if !errors.Is(err, ErrNonPositiveClose) {
		t.Fatalf("PortfolioReturns err=%v (returns=%d), want ErrNonPositiveClose", err, len(got))
	}
}

func TestRiskContributions_RejectsZeroClose(t *testing.T) {
	good := make([]float64, MinCloses)
	bad := make([]float64, MinCloses)
	for i := range good {
		good[i] = 100 + float64(i)
		bad[i] = 100 + float64(i)
	}
	bad[MinCloses-2] = 0

	_, err := RiskContributions(
		[]Holding{{Symbol: "AAA", Weight: 0.5}, {Symbol: "BBB", Weight: 0.5}},
		[]Series{{Symbol: "AAA", Closes: good}, {Symbol: "BBB", Closes: bad}},
	)
	if !errors.Is(err, ErrNonPositiveClose) {
		t.Fatalf("RiskContributions err=%v, want ErrNonPositiveClose", err)
	}
}

// A negative close is not a division hazard — it produces a finite, entirely
// fictional return — so it slips past the nil check DailyReturns performs. It
// is still not a price, and a risk number computed from it is fiction.
func TestAlignSeries_RejectsNegativeAndNonFiniteCloses(t *testing.T) {
	base := make([]float64, MinCloses)
	for i := range base {
		base[i] = 100 + float64(i)
	}
	cases := map[string]float64{
		"negative": -5,
		"NaN":      nan(),
		"+Inf":     inf(1),
	}
	for name, poison := range cases {
		bad := append([]float64(nil), base...)
		bad[3] = poison
		_, err := PortfolioReturns(
			[]Holding{{Symbol: "AAA", Weight: 1}},
			[]Series{{Symbol: "AAA", Closes: bad}},
		)
		if !errors.Is(err, ErrNonPositiveClose) {
			t.Errorf("%s close: err=%v, want ErrNonPositiveClose", name, err)
		}
	}
}
