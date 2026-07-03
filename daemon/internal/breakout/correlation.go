package breakout

import (
	"math"
	"sort"
)

// CorrBreakThreshold is the default |Delta| above which a pair's correlation is
// considered to have BROKEN (decoupled, or newly coupled). Delta is
// recentR - baseR, both Pearson correlations of daily returns.
const CorrBreakThreshold = 0.5

// Series is one symbol's close-price history, ASCENDING by time and aligned in
// index with every other Series passed alongside it (index i is the same bar
// for all symbols). Only Closes are needed; returns are derived internally.
type Series struct {
	Symbol string
	Closes []float64
}

// Break is a pair of symbols whose return correlation changed materially
// between a long BASE window and a short RECENT window.
//
// RecentR and BaseR are the Pearson correlations of the two symbols' daily
// (bar-over-bar) log-free simple returns over the recent and base windows
// respectively. Delta = RecentR - BaseR. A large negative Delta means a pair
// that used to move together has decoupled; a large positive Delta means a pair
// that was independent is now moving together.
type Break struct {
	A       string
	B       string
	RecentR float64
	BaseR   float64
	Delta   float64
}

// CorrelationBreaks compares, for every unordered pair of the given Series, the
// Pearson correlation of their daily returns over the last `recent` returns
// against the last `base` returns, and flags pairs whose |Delta| exceeds
// CorrBreakThreshold. Results are sorted by |Delta| descending (ties broken by
// A then B for determinism).
//
// Honesty / no-lookahead: correlations are computed only from the provided
// history; `recent` is a strict subset of `base` (recent <= base), so a break
// is a real change between a long-run baseline and its own tail — never a peek
// forward. Grading whether a flagged decoupling persists is the caller's job;
// this function only measures the change that has already happened.
//
// Alignment: all series are truncated to the shortest length before returns are
// taken, so mismatched histories cannot silently misalign. A pair is skipped
// (no Break emitted, not an error) when there are too few aligned returns for
// either window, or when a window has zero variance in either series (Pearson
// undefined).
func CorrelationBreaks(series []Series, recent, base int) []Break {
	out := []Break{}
	if recent <= 1 || base <= 1 || recent > base {
		return out
	}
	if len(series) < 2 {
		return out
	}

	// Align to the shortest close history.
	minLen := len(series[0].Closes)
	for _, s := range series {
		if len(s.Closes) < minLen {
			minLen = len(s.Closes)
		}
	}
	// returns has length minLen-1; need at least `base` of them.
	if minLen-1 < base {
		return out
	}

	// Precompute each series' aligned return vector once.
	rets := make([][]float64, len(series))
	for i, s := range series {
		rets[i] = simpleReturns(s.Closes[:minLen])
	}

	for i := 0; i < len(series); i++ {
		for j := i + 1; j < len(series); j++ {
			ri := rets[i]
			rj := rets[j]

			recentR, okR := pearsonTail(ri, rj, recent)
			baseR, okB := pearsonTail(ri, rj, base)
			if !okR || !okB {
				continue
			}
			delta := recentR - baseR
			if math.Abs(delta) < CorrBreakThreshold {
				continue
			}
			out = append(out, Break{
				A:       series[i].Symbol,
				B:       series[j].Symbol,
				RecentR: recentR,
				BaseR:   baseR,
				Delta:   delta,
			})
		}
	}

	sort.SliceStable(out, func(a, b int) bool {
		da, db := math.Abs(out[a].Delta), math.Abs(out[b].Delta)
		if da != db {
			return da > db
		}
		if out[a].A != out[b].A {
			return out[a].A < out[b].A
		}
		return out[a].B < out[b].B
	})
	return out
}

// simpleReturns turns a close series into bar-over-bar simple returns
// (c[i]-c[i-1])/c[i-1]. A zero or missing prior close yields a 0 return for
// that step (the pair test tolerates it via the variance guard). Output length
// is len(closes)-1, or nil if fewer than two closes.
func simpleReturns(closes []float64) []float64 {
	if len(closes) < 2 {
		return nil
	}
	out := make([]float64, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		prev := closes[i-1]
		if prev == 0 {
			out[i-1] = 0
			continue
		}
		out[i-1] = (closes[i] - prev) / prev
	}
	return out
}

// pearsonTail computes the Pearson correlation of the last n aligned elements of
// x and y. ok=false when n<2, either slice is shorter than n, or either window
// has zero variance (correlation undefined).
func pearsonTail(x, y []float64, n int) (float64, bool) {
	if n < 2 || len(x) < n || len(y) < n {
		return 0, false
	}
	xs := x[len(x)-n:]
	ys := y[len(y)-n:]

	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += xs[i]
		sy += ys[i]
	}
	mx := sx / float64(n)
	my := sy / float64(n)

	var cov, vx, vy float64
	for i := 0; i < n; i++ {
		dx := xs[i] - mx
		dy := ys[i] - my
		cov += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx <= 0 || vy <= 0 {
		return 0, false
	}
	r := cov / math.Sqrt(vx*vy)
	// Guard against tiny floating drift outside [-1,1].
	if r > 1 {
		r = 1
	} else if r < -1 {
		r = -1
	}
	return r, true
}
