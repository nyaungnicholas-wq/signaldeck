// Package signalbt is the end-to-end OUT-OF-SAMPLE backtester of SignalDeck's
// OWN flagship signal — the calibrated pressure-score/ensemble blend the
// PredictionRunner emits — as opposed to internal/backtest, which grades
// user-typed SMA/RSI rules.
//
// # What it grades and why it is out of sample
//
// The input is the FEATURE STORE replayed: every row is the exact calibrated
// signal a prediction actually emitted at time t, joined to the LATER realized
// forward return. Because the store's LabeledFeatures* joins require the outcome
// to be resolved (resolved_at NOT NULL, up/fwd_return NOT NULL), a row exists
// only AFTER its own forward window closed — the signal was computed from
// bars[..t] and graded on bars strictly after t. There is no lookahead: this
// package never recomputes a signal, it replays the one that was committed.
//
// # The honesty gates (this project's identity)
//
//   - INDEPENDENT-N. The minute-cadence pipeline writes many observations per
//     (symbol, day) that all resolve against the SAME daily forward move.
//     Pooling them pseudo-replicates the sample. Every statistic here is computed
//     over ONE observation per (symbol, UTC-day) — the LATEST signal that day —
//     exactly the Stage-1 dedup. Below MinIndependentN independent obs, the
//     headline skill numbers (IC, quintile spread, hit-rate) are WITHHELD.
//   - NET OF COST. The PnL / equity curve charges a per-side cost (bps of
//     equity) on every position change, the same convention as internal/backtest
//     and internal/papertrade. A gross edge that costs eat is not an edge.
//   - LABELED. Result.Live is always false and TrackLabel says "backtested" —
//     the flagship has ~0 resolved LIVE outcomes today, so the honest headline is
//     "insufficient data", which is the truth this package must be able to say.
//
// The engine is PURE: Observations in, a Result out. No I/O, no clock, no store.
// The API handler assembles Observations from the feature store; the tests feed
// synthetic ones. This keeps the math independently verifiable.
package signalbt

import "sort"

// MinIndependentN is the floor of distinct (symbol, UTC-day) observations below
// which the headline skill statistics (IC, quintile spread, hit-rate) are
// WITHHELD as "insufficient independent resolutions". Matches the Stage-1
// honesty gate on the /honesty page (api.minIndependentN). A correlation off a
// handful of independent symbol-days is noise, not evidence.
const MinIndependentN = 30

const secondsPerDay = 86400

// Observation is one graded signal: the calibrated up-probability the platform
// emitted at Ts for Symbol, joined to the realized forward returns at one or
// more lags. FwdByLag maps a forward horizon in DAYS to the realized simple
// return over that window (close_{t+lag}/close_t - 1). The primary grading lag
// (the signal's own horizon) is PrimaryLag; extra lags drive IC-decay.
//
// Signal is on a [0,1] up-probability scale (the ensemble's calibrated prob).
// It is centered to [-1,1] via 2*Signal-1 only where a signed lean is needed
// (position sign in the PnL leg); IC/quintiles use the raw [0,1] signal since
// rank/threshold statistics are invariant to that affine shift.
type Observation struct {
	SymbolID int64
	Ts       int64              // prediction bar ts (unix seconds, UTC)
	Signal   float64            // calibrated P(up) in [0,1] emitted at Ts
	FwdByLag map[int]float64    // lag(days) -> realized forward return
}

// Params configure the backtest. Zero values are filled with honest defaults by
// (Params).withDefaults so a caller can pass Params{} and get a sane run.
type Params struct {
	// PrimaryLag is the forward window (in days) the headline IC / quintile /
	// hit-rate / PnL are graded on — normally the signal's own horizon (1 for
	// 1d, 5 for 1w). Must be present in every Observation's FwdByLag.
	PrimaryLag int
	// DecayLags are the additional forward windows (days) for the IC-decay
	// curve. The engine reports IC at each lag that is present in the data.
	DecayLags []int
	// CostBps is the per-SIDE trading cost in basis points charged whenever the
	// signed target position changes (a round trip pays it twice), consistent
	// with internal/backtest and internal/papertrade.
	CostBps float64
	// LongThreshold / FlatThreshold define the signal->position deadband on the
	// [0,1] calibrated probability, mirroring the paper-trading book: at/above
	// LongThreshold target long, at/below FlatThreshold target flat, in between
	// hold. Keeping a deadband stops the equity curve churning cost on noise
	// around 0.5.
	LongThreshold float64
	FlatThreshold float64
}

// Default gates + cost, mirroring internal/papertrade so the two honest
// track records use the same assumptions.
const (
	defaultCostBps       = 7.5 // per side, stock-like (papertrade stock default)
	defaultLongThreshold = 0.60
	defaultFlatThreshold = 0.40
)

func (p Params) withDefaults() Params {
	if p.PrimaryLag <= 0 {
		p.PrimaryLag = 1
	}
	if p.CostBps < 0 {
		p.CostBps = 0
	}
	if p.CostBps == 0 {
		p.CostBps = defaultCostBps
	}
	if p.LongThreshold <= 0 || p.LongThreshold > 1 {
		p.LongThreshold = defaultLongThreshold
	}
	if p.FlatThreshold < 0 || p.FlatThreshold >= p.LongThreshold {
		p.FlatThreshold = defaultFlatThreshold
	}
	return p
}

// QuintileBucket is one signal-quintile's realized forward-return profile over
// the independent set. Buckets are cut on the SIGNAL value (equal-count
// quintiles), so Q5-Q1 mean-forward spread is the classic monotonicity check:
// a signal with edge shows rising MeanFwd from Q1 to Q5.
type QuintileBucket struct {
	Quintile int     `json:"quintile"` // 1 (lowest signal) .. 5 (highest)
	N        int     `json:"n"`
	MeanSig  float64 `json:"meanSignal"`
	MeanFwd  float64 `json:"meanFwd"`
	HitRate  float64 `json:"hitRate"` // fraction with fwd>0
}

// ICPoint is the information coefficient (rank correlation of signal vs forward
// return) at one forward lag — the IC-decay curve is a slice of these.
type ICPoint struct {
	LagDays int     `json:"lagDays"`
	IC      float64 `json:"ic"`
	N       int     `json:"n"` // independent obs with a forward return at this lag
}

// EquityPoint is one mark of the costed equity curve of the signal-driven
// long/flat strategy vs the SPY buy-and-hold benchmark. Both start at 1.0.
type EquityPoint struct {
	Ts        int64   `json:"ts"`
	Strategy  float64 `json:"strategy"`  // signal-driven equity (net of cost)
	Benchmark float64 `json:"benchmark"` // SPY buy-and-hold equity
}

// Result is the full OOS grade of the platform's own signal. Every headline
// skill number is GATED behind Gated: below MinIndependentN independent
// observations the fields are still populated for completeness but Gated is
// true and the UI must show "insufficient data".
type Result struct {
	Horizon string `json:"horizon"`

	// Sample accounting — raw rows vs the independent (symbol,day) set.
	RawN            int  `json:"rawN"`
	IndependentN    int  `json:"independentN"`
	MinIndependentN int  `json:"minIndependentN"`
	Gated           bool `json:"gated"`

	// Headline skill (over the independent set, at PrimaryLag).
	IC            float64          `json:"ic"`             // primary-lag information coefficient (Spearman)
	ICDecay       []ICPoint        `json:"icDecay"`        // IC by forward lag
	Quintiles     []QuintileBucket `json:"quintiles"`      // signal-quintile forward profile
	QuintileSpread float64         `json:"quintileSpread"` // Q5.MeanFwd - Q1.MeanFwd
	HitRate       float64          `json:"hitRate"`        // signal>0.5 predicts fwd>0, over independent set
	MeanFwd       float64          `json:"meanFwd"`        // mean primary-lag forward return over independent set

	// Costed strategy vs SPY buy-and-hold.
	Turnover        float64       `json:"turnover"`        // mean |position change| per observation (round-trip churn proxy)
	CostBps         float64       `json:"costBps"`         // per-side cost applied
	Equity          []EquityPoint `json:"equity"`          // costed equity curve + benchmark
	StrategyReturn  float64       `json:"strategyReturn"`  // net-of-cost total return of the signal strategy
	BenchmarkReturn float64       `json:"benchmarkReturn"` // SPY buy-and-hold total return
	ExcessReturn    float64       `json:"excessReturn"`    // strategyReturn - benchmarkReturn

	// Honesty labeling — the flagship has ~0 LIVE resolved outcomes today.
	Live       bool   `json:"live"`
	TrackLabel string `json:"trackLabel"`
	Note       string `json:"note"`
}

// Backtest replays the labeled observations of the platform's own signal and
// grades it out of sample. obs need NOT be pre-deduped or pre-sorted: Backtest
// collapses to one observation per (symbol, UTC-day) (keeping the LATEST signal
// that day) before computing anything, and sorts by ts for the equity curve.
//
// benchmark is the SPY buy-and-hold equity path aligned to the DISTINCT trading
// days present in the independent set (see BenchmarkCurve): benchmark[i] is the
// SPY buy-and-hold equity (start 1.0) as of the i-th distinct day. It may be nil
// (no SPY bars available) — the strategy equity is still returned; the benchmark
// series is simply empty and BenchmarkReturn is 0.
func Backtest(obs []Observation, benchmark []EquityPoint, horizon string, p Params) Result {
	p = p.withDefaults()
	res := Result{
		Horizon:         horizon,
		RawN:            len(obs),
		MinIndependentN: MinIndependentN,
		CostBps:         p.CostBps,
		Live:            false,
		TrackLabel:      "backtested — not live (own-signal replay of the feature store, net of cost)",
	}

	indep := dedupeIndependent(obs)
	res.IndependentN = len(indep)
	res.Gated = res.IndependentN < MinIndependentN
	if res.Gated {
		res.Note = "insufficient independent resolutions — headline skill numbers withheld until the signal has a real out-of-sample track record"
	}

	// Sort the independent set by ts (dedupeIndependent does not order).
	sort.SliceStable(indep, func(i, j int) bool { return indep[i].Ts < indep[j].Ts })

	// --- headline skill at the primary lag (over the independent set) ---
	sigs := make([]float64, 0, len(indep))
	fwds := make([]float64, 0, len(indep))
	for _, o := range indep {
		if f, ok := o.FwdByLag[p.PrimaryLag]; ok {
			sigs = append(sigs, o.Signal)
			fwds = append(fwds, f)
		}
	}
	res.IC = spearman(sigs, fwds)
	res.HitRate = hitRate(sigs, fwds)
	res.MeanFwd = mean(fwds)
	res.Quintiles = quintiles(sigs, fwds)
	if len(res.Quintiles) == 5 {
		res.QuintileSpread = res.Quintiles[4].MeanFwd - res.Quintiles[0].MeanFwd
	}

	// --- IC decay by lag ---
	lags := append([]int{p.PrimaryLag}, p.DecayLags...)
	res.ICDecay = icDecay(indep, lags)

	// --- costed equity curve vs SPY buy-and-hold ---
	res.Equity, res.Turnover = equityCurve(indep, benchmark, p)
	if n := len(res.Equity); n > 0 {
		res.StrategyReturn = res.Equity[n-1].Strategy - 1
		res.BenchmarkReturn = res.Equity[n-1].Benchmark - 1
		res.ExcessReturn = res.StrategyReturn - res.BenchmarkReturn
	}
	return res
}

// dedupeIndependent collapses observations to ONE per (symbol, UTC-day): the
// LATEST signal for that symbol on that day. This is the Stage-1 independent-N
// rule — computing skill stats on the raw minute rows would pseudo-replicate the
// same daily forward move. Order of the input does not matter (we keep the
// max-ts row per key), which is why Backtest sorts afterward.
func dedupeIndependent(obs []Observation) []Observation {
	type key struct {
		sym int64
		day int64
	}
	best := make(map[key]Observation, len(obs))
	for _, o := range obs {
		k := key{sym: o.SymbolID, day: o.Ts / secondsPerDay}
		if cur, ok := best[k]; !ok || o.Ts > cur.Ts {
			best[k] = o
		}
	}
	out := make([]Observation, 0, len(best))
	for _, o := range best {
		out = append(out, o)
	}
	return out
}
