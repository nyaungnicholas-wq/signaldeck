package stresslab

import (
	"math"
	"math/rand"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// trendBars builds n bars with per-bar return drift so blocks are
// distinguishable.
func trendBars(n int) []md.Bar {
	bars := make([]md.Bar, n)
	px := 100.0
	for i := range bars {
		r := 0.001 * float64(i%7) // repeating return pattern
		px *= 1 + r
		bars[i] = md.Bar{
			Ts:   int64(1_700_000_000 + i*86400),
			Open: px * 0.999, High: px * 1.005, Low: px * 0.995, Close: px,
			Volume: float64(1000 + i),
		}
	}
	return bars
}

func TestBootstrap_DeterministicGivenSeed(t *testing.T) {
	bars := trendBars(60)
	a := BlockBootstrap(bars, nil, 10, rand.New(rand.NewSource(42)))
	b := BlockBootstrap(bars, nil, 10, rand.New(rand.NewSource(42)))
	c := BlockBootstrap(bars, nil, 10, rand.New(rand.NewSource(43)))
	if len(a) != len(bars) || len(b) != len(bars) {
		t.Fatalf("length changed: %d/%d want %d", len(a), len(b), len(bars))
	}
	var differsFromC bool
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed diverged at bar %d", i)
		}
		if a[i] != c[i] {
			differsFromC = true
		}
	}
	if !differsFromC {
		t.Fatal("different seeds produced an identical path (suspicious)")
	}
}

func TestBootstrap_PreservesBlockStructureAndCalendar(t *testing.T) {
	bars := trendBars(60)
	out := BlockBootstrap(bars, nil, 10, rand.New(rand.NewSource(7)))

	// Calendar preserved.
	for i := range out {
		if out[i].Ts != bars[i].Ts {
			t.Fatalf("timestamp %d changed", i)
		}
	}
	// Anchor preserved and path continuous/positive.
	if out[0] != bars[0] {
		t.Fatal("first bar must be the original anchor")
	}
	for i, b := range out {
		if b.Close <= 0 || b.Low <= 0 {
			t.Fatalf("non-positive price at %d", i)
		}
	}

	// Block structure: within a block, consecutive close-to-close returns
	// must be returns that exist consecutively in the source. Check the first
	// resampled block (bars 1..10): its return sequence must appear as a
	// contiguous run in the source return series.
	ret := func(bs []md.Bar, i int) float64 { return bs[i].Close/bs[i-1].Close - 1 }
	first := make([]float64, 0, 9)
	for i := 2; i <= 10; i++ {
		first = append(first, ret(out, i))
	}
	found := false
	for s := 2; s+len(first) <= len(bars); s++ {
		match := true
		for j := range first {
			if math.Abs(ret(bars, s+j)-first[j]) > 1e-12 {
				match = false
				break
			}
		}
		if match {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("resampled block's return sequence does not appear contiguously in the source — block structure broken")
	}
}

func TestBootstrap_RegimeConditioning(t *testing.T) {
	bars := trendBars(60)
	regimes := make([]string, len(bars))
	for i := range regimes {
		if i < 30 {
			regimes[i] = "calm"
		} else {
			regimes[i] = "stressed"
		}
	}
	// With blockLen 5 every valid start in [1,25] is calm and [30,55] stressed;
	// conditioned draws for a calm position must come from calm starts. Verify
	// via volumes, which uniquely identify source bars.
	out := BlockBootstrap(bars, regimes, 5, rand.New(rand.NewSource(3)))
	for i := 1; i <= 25; i += 5 { // block starts within the calm prefix
		v := out[i].Volume
		src := int(v) - 1000
		if src >= 30 {
			t.Fatalf("calm position %d drew from stressed source bar %d", i, src)
		}
	}
}
