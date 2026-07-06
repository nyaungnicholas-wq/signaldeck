package signalbt

// Pinned is the WEEKLY SNAPSHOT of the own-signal backtest: the exact Result
// per horizon computed by the signalbt-weekly worker on Sunday evening (NY),
// persisted as JSON in the meta table under MetaKeyLatest (always the newest)
// and MetaKeyWeekly(dayKey) (the immutable per-week record). The API's
// ?pinned=1 branch serves this stored result so the /lab/signal-backtest page
// can show "as of Sunday" without recomputing — and so the weekly grade the
// insight described is EXACTLY the grade the page shows, not a fresher one.
//
// This file stays pure like the rest of the package: types + math only, no
// I/O. The worker (internal/briefing) writes; the API handler reads.

import "math"

// DefaultDecayLags are the extra forward lags (trading-day bars) graded for
// the IC-decay curve on top of the horizon's own primary lag. Shared by the
// live /api/signal-backtest handler AND the weekly pin worker so the pinned
// result and a live recompute grade the same thing (no parameter drift).
var DefaultDecayLags = []int{2, 3, 5, 10}

// DefaultMaxObs caps how many resolved feature-store rows one evaluation
// replays (newest-first). Shared by the live handler and the weekly worker.
const DefaultMaxObs = 20000

// MaxStoredEquityPoints bounds the equity curve persisted inside a Pinned
// snapshot so the meta blob stays small as the record grows. When the curve is
// decimated the snapshot says so (EquityDownsampled) — the summary statistics
// are always computed on the FULL curve before downsampling.
const MaxStoredEquityPoints = 1000

// MetaKeyLatest is the meta key holding the newest weekly Pinned snapshot.
const MetaKeyLatest = "signalbt_latest"

// MetaKeyWeekly returns the immutable per-week meta key for a snapshot
// (dayKey = the NY Sunday the evaluation ran for, YYYY-MM-DD).
func MetaKeyWeekly(dayKey string) string { return "signalbt_weekly:" + dayKey }

// PrimaryLagFor maps a horizon label to its primary grading lag in trading-day
// bars (1w = 5 bars, everything else 1) — the same rule the live handler uses.
func PrimaryLagFor(horizon string) int {
	if horizon == "1w" {
		return 5
	}
	return 1
}

// Pinned is one stored weekly evaluation across horizons.
type Pinned struct {
	DayKey            string            `json:"dayKey"`     // NY Sunday, YYYY-MM-DD
	ComputedTs        int64             `json:"computedTs"` // unix seconds
	BenchmarkSymbol   string            `json:"benchmarkSymbol"`
	HasBenchmark      bool              `json:"hasBenchmark"`
	EquityDownsampled bool              `json:"equityDownsampled"` // true when any stored curve was decimated
	Results           map[string]Result `json:"results"`           // horizon -> full graded Result
}

// DownsampleEquity decimates an equity curve to at most max points, always
// keeping the first and last marks (so total returns are preserved exactly).
// Returns the (possibly original) slice and whether decimation happened.
func DownsampleEquity(pts []EquityPoint, max int) ([]EquityPoint, bool) {
	if max <= 1 || len(pts) <= max {
		return pts, false
	}
	out := make([]EquityPoint, 0, max)
	step := float64(len(pts)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx > len(pts)-1 {
			idx = len(pts) - 1
		}
		out = append(out, pts[idx])
	}
	return out, true
}
