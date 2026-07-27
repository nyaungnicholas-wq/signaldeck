package stresslab

import (
	"math/rand"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// DefaultBlockLen is the block-bootstrap block length in bars. Two trading
// weeks preserves short-horizon autocorrelation (the property an i.i.d.
// bootstrap destroys and the reason this is a BLOCK bootstrap) while still
// giving a 250-bar window ~25 independent blocks to shuffle.
const DefaultBlockLen = 10

// BlockBootstrap resamples one counterfactual bar path from stored bars by
// drawing CONTIGUOUS blocks of blockLen bars, so within-block autocorrelation
// is preserved exactly and only the block ordering is counterfactual.
//
// Regime conditioning: when regimes is non-nil and parallel to bars, the block
// drawn for position i is picked among blocks whose STARTING bar carries the
// same regime label as the original block starting at i — a calm stretch is
// replaced by another calm stretch, a stressed one by another stressed one.
// When no other block shares the label (or regimes is nil) the draw falls
// back to all blocks, so a lone label never dead-ends the resample.
//
// The path is rebuilt as a RETURN chain anchored at the original first close:
// each drawn bar contributes its open/high/low/close as ratios to its own
// previous close, chained onto the running level. That keeps the path
// continuous (no teleporting levels at block seams) while the returns are the
// stored, real ones. Timestamps and volumes keep the ORIGINAL calendar so the
// resampled path joins the same signal timeline.
//
// Deterministic: all randomness comes from rng, which the caller seeds.
func BlockBootstrap(bars []md.Bar, regimes []string, blockLen int, rng *rand.Rand) []md.Bar {
	if len(bars) < 2 {
		return append([]md.Bar(nil), bars...)
	}
	if blockLen <= 0 {
		blockLen = DefaultBlockLen
	}
	if blockLen > len(bars)-1 {
		blockLen = len(bars) - 1
	}
	if len(regimes) != len(bars) {
		regimes = nil
	}

	// Valid block starts: bar i starts a block of returns computed against
	// bar i-1, so starts run over [1, len-blockLen].
	maxStart := len(bars) - blockLen
	starts := make([]int, 0, maxStart)
	for i := 1; i <= maxStart; i++ {
		starts = append(starts, i)
	}
	byRegime := map[string][]int{}
	if regimes != nil {
		for _, s := range starts {
			byRegime[regimes[s]] = append(byRegime[regimes[s]], s)
		}
	}

	out := make([]md.Bar, 0, len(bars))
	out = append(out, bars[0])
	level := bars[0].Close

	pos := 1
	for pos < len(bars) {
		want := starts
		if regimes != nil && pos <= maxStart {
			if same := byRegime[regimes[pos]]; len(same) > 0 {
				want = same
			}
		}
		start := want[rng.Intn(len(want))]

		n := blockLen
		if pos+n > len(bars) {
			n = len(bars) - pos
		}
		for j := 0; j < n && start+j < len(bars); j++ {
			src := bars[start+j]
			prev := bars[start+j-1].Close
			if prev <= 0 || level <= 0 {
				// Unpriceable link: carry the level flat rather than invent.
				prev, src = 1, md.Bar{Open: 1, High: 1, Low: 1, Close: 1, Volume: src.Volume}
			}
			b := md.Bar{
				SymbolID: bars[pos].SymbolID,
				TF:       bars[pos].TF,
				Ts:       bars[pos+j].Ts, // original calendar slot
				Open:     level * src.Open / prev,
				High:     level * src.High / prev,
				Low:      level * src.Low / prev,
				Close:    level * src.Close / prev,
				Volume:   src.Volume,
			}
			out = append(out, b)
			level = b.Close
			if pos+j+1 >= len(bars) {
				break
			}
		}
		pos += n
	}
	return out
}
