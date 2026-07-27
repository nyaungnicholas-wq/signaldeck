// Package stresslab is the Layer-4 stress-replay simulator, scoped honestly
// (ARCHITECTURE_EV.md "Layer 4 — simulator, scoped honestly").
//
// internal/scenario is a single-factor linear beta×shock estimate; a full
// agent-based market simulator would be a fiction this codebase cannot back.
// The honest middle path implemented here:
//
//   - SCENARIOS AS DATA. A named scenario is a composable vector of effects
//     (price shock, spread multiple, ADV multiple, range multiple, feed delay,
//     dropped bars, outage) applied as a pure transform over a bar/signal
//     window. Composition is arithmetic on the effect vector, not code.
//   - REGIME-CONDITIONAL BLOCK BOOTSTRAP (bootstrap.go). Counterfactual paths
//     are resampled from STORED bars in contiguous blocks so autocorrelation
//     survives, conditioned on a regime label where one is available.
//     Deterministic given the caller's seed.
//   - REPLAY THROUGH THE REAL DECISION PATH (replay.go). The window's signals
//     are the COMMITTED calibrated probabilities (signalbt's discipline:
//     never recompute a signal, replay the one that was committed), pushed
//     through the real threshold decision, the real riskgate envelope and the
//     real papertrade execution-cost model. The output is how the SYSTEM
//     behaved — trades taken/refused, breaker trips, drawdown path, fill
//     degradation — not a fabricated PnL claim.
//
// Everything here is pure functions over inputs: no I/O, no clock, no store,
// no global RNG. The API handler assembles windows from the store; the tests
// feed synthetic ones.
package stresslab

import (
	"fmt"
	"math/rand"
	"sort"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Signal is one committed calibrated P(up) the pipeline actually emitted at
// Ts. It is replayed, never recomputed.
type Signal struct {
	Ts int64   `json:"ts"`
	P  float64 `json:"p"`
}

// Window is one symbol's replay input: stored bars (ascending), the committed
// signal series over the same span, and the liquidity/spread context the
// execution model prices against. Regimes, when present, is parallel to Bars
// (label per bar) and conditions the block bootstrap.
type Window struct {
	Market  md.Market
	Bars    []md.Bar
	Signals []Signal
	Regimes []string // optional, len==len(Bars) when set

	// ADVUSD is the average daily dollar volume the execution model divides
	// impact and capacity by. Scenario ADV multipliers scale it.
	ADVUSD float64

	// SpreadMult scales the market's per-side half-spread cost during replay
	// (1 = the normal papertrade.CostBpsFor spread). The papertrade model's
	// spread is a per-market constant, so widening is charged by the harness
	// on top and reported in the fill degradation numbers.
	SpreadMult float64

	// DelayBars shifts every signal's EFFECT forward this many bars — the
	// decision fires late, on stale information, exactly as a delayed feed
	// would make it.
	DelayBars int
}

// Effects is the composable shock vector. The identity element (no scenario)
// is Identity(); Combine folds several scenarios into one Effects.
type Effects struct {
	// PriceShock moves the shock bar's close by this fraction (e.g. -0.08)
	// and level-shifts every later bar with it — the market repriced and
	// stayed there for the rest of the window.
	PriceShock float64 `json:"priceShock"`
	// GapOpen moves the shock bar's OPEN by this fraction relative to the
	// prior close (and level-shifts the rest of the bar and every later bar):
	// the move happened where no order could work, between sessions.
	GapOpen float64 `json:"gapOpen"`
	// SpreadMult multiplies the per-side half-spread charged on every fill.
	SpreadMult float64 `json:"spreadMult"`
	// ADVMult multiplies the average daily dollar volume — impact rises and
	// fill capacity falls as it shrinks.
	ADVMult float64 `json:"advMult"`
	// RangeMult widens each bar's high/low range around its close, which
	// raises the Parkinson sigma the impact model prices off.
	RangeMult float64 `json:"rangeMult"`
	// DelayBars delays every signal's effect by this many bars.
	DelayBars int `json:"delayBars"`
	// DropFrac independently drops this fraction of bars (never the first),
	// using the caller's seeded RNG.
	DropFrac float64 `json:"dropFrac"`
	// OutageBars removes this many CONTIGUOUS bars starting at the shock bar
	// — an exchange outage, not scattered gaps.
	OutageBars int `json:"outageBars"`
}

// Identity is the no-op effect vector: multipliers 1, everything else 0.
func Identity() Effects {
	return Effects{SpreadMult: 1, ADVMult: 1, RangeMult: 1}
}

// Scenario is a named, documented effect vector. Definitions are data; the
// one Apply function interprets them.
type Scenario struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Effects     Effects `json:"effects"`
}

// Catalog is the built-in scenario library. Order is stable for the API.
func Catalog() []Scenario {
	return []Scenario{
		{
			Name:        "flash_crash",
			Description: "-8% in one bar, spreads 3x, liquidity down to 0.3x ADV — the market falls and the exit costs more than the model normally assumes",
			Effects:     Effects{PriceShock: -0.08, SpreadMult: 3, ADVMult: 0.3, RangeMult: 1},
		},
		{
			Name:        "liquidity_drought",
			Description: "ADV down to 0.1x and spreads 2x with prices unchanged — the same decisions, priced against a market that cannot absorb them",
			Effects:     Effects{SpreadMult: 2, ADVMult: 0.1, RangeMult: 1},
		},
		{
			Name:        "vol_spike",
			Description: "bar high/low ranges 3x around unchanged closes — Parkinson sigma and therefore modelled impact rise on every fill",
			Effects:     Effects{SpreadMult: 1, ADVMult: 1, RangeMult: 3},
		},
		{
			Name:        "spread_widening",
			Description: "per-side half-spread 4x — every entry and exit pays four times the normal crossing cost",
			Effects:     Effects{SpreadMult: 4, ADVMult: 1, RangeMult: 1},
		},
		{
			Name:        "gap_open",
			Description: "-5% overnight gap at the shock bar's open — the move happens where no intrabar order could have worked",
			Effects:     Effects{GapOpen: -0.05, SpreadMult: 1, ADVMult: 1, RangeMult: 1},
		},
		{
			Name:        "delayed_feed",
			Description: "every signal acts 3 bars late — decisions fire on stale information",
			Effects:     Effects{DelayBars: 3, SpreadMult: 1, ADVMult: 1, RangeMult: 1},
		},
		{
			Name:        "missing_candles",
			Description: "20% of bars dropped at random (seeded) — marks, decisions and fills all skip the holes",
			Effects:     Effects{DropFrac: 0.20, SpreadMult: 1, ADVMult: 1, RangeMult: 1},
		},
		{
			Name:        "exchange_outage",
			Description: "5 contiguous bars removed at the shock point — no marks, no decisions, no exits while the venue is dark",
			Effects:     Effects{OutageBars: 5, SpreadMult: 1, ADVMult: 1, RangeMult: 1},
		},
	}
}

// Lookup resolves scenario names against the catalog.
func Lookup(names []string) ([]Scenario, error) {
	byName := map[string]Scenario{}
	for _, s := range Catalog() {
		byName[s.Name] = s
	}
	out := make([]Scenario, 0, len(names))
	for _, n := range names {
		s, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown scenario %q", n)
		}
		out = append(out, s)
	}
	return out, nil
}

// Combine folds several scenarios into one joint effect vector: shocks and
// delays accumulate, multipliers multiply, drop probabilities compose as
// independent events, outages take the longest. This is what makes
// "vol_spike + spread_widening" a first-class joint scenario rather than two
// sequential runs.
func Combine(scenarios ...Scenario) Effects {
	e := Identity()
	for _, s := range scenarios {
		f := s.Effects
		e.PriceShock += f.PriceShock
		e.GapOpen += f.GapOpen
		if f.SpreadMult > 0 {
			e.SpreadMult *= f.SpreadMult
		}
		if f.ADVMult > 0 {
			e.ADVMult *= f.ADVMult
		}
		if f.RangeMult > 0 {
			e.RangeMult *= f.RangeMult
		}
		e.DelayBars += f.DelayBars
		e.DropFrac = 1 - (1-e.DropFrac)*(1-clamp01(f.DropFrac))
		if f.OutageBars > e.OutageBars {
			e.OutageBars = f.OutageBars
		}
	}
	return e
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ShockIndex is where the point shocks (price shock, gap, outage) land: the
// middle bar, so there is history to be positioned in before it and a path to
// suffer through after it.
func ShockIndex(n int) int { return n / 2 }

// Apply interprets an effect vector over a window and returns a NEW window;
// the input is never mutated. rng drives only the stochastic effect
// (DropFrac) and must be seeded by the caller — given the same seed the
// output is identical.
func Apply(w Window, e Effects, rng *rand.Rand) Window {
	out := w
	out.Bars = make([]md.Bar, len(w.Bars))
	copy(out.Bars, w.Bars)
	out.Signals = append([]Signal(nil), w.Signals...)
	if len(w.Regimes) == len(w.Bars) {
		out.Regimes = append([]string(nil), w.Regimes...)
	}
	if out.SpreadMult <= 0 {
		out.SpreadMult = 1
	}

	shockIdx := ShockIndex(len(out.Bars))

	// Price shock: the shock bar's close (and low) takes the hit; every later
	// bar is level-shifted with it.
	if e.PriceShock != 0 && shockIdx < len(out.Bars) {
		k := 1 + e.PriceShock
		b := out.Bars[shockIdx]
		b.Close *= k
		if b.Close < b.Low {
			b.Low = b.Close
		}
		if b.Close > b.High {
			b.High = b.Close
		}
		out.Bars[shockIdx] = b
		for i := shockIdx + 1; i < len(out.Bars); i++ {
			out.Bars[i] = scaleBar(out.Bars[i], k)
		}
	}

	// Gap open: the whole shock bar (open included) and everything after it
	// is level-shifted — the reprice happened between bars.
	if e.GapOpen != 0 && shockIdx < len(out.Bars) {
		k := 1 + e.GapOpen
		for i := shockIdx; i < len(out.Bars); i++ {
			out.Bars[i] = scaleBar(out.Bars[i], k)
		}
	}

	// Range widening around the close (vol spike). The close is the pivot so
	// the close-to-close path — and with it the drawdown story — is unchanged;
	// only the range the impact model reads gets wider.
	if e.RangeMult > 0 && e.RangeMult != 1 {
		for i, b := range out.Bars {
			b.High = b.Close + (b.High-b.Close)*e.RangeMult
			lo := b.Close - (b.Close-b.Low)*e.RangeMult
			if lo <= 0 {
				lo = b.Close * 0.01
			}
			b.Low = lo
			if b.Open > b.High {
				b.High = b.Open
			}
			if b.Open < b.Low {
				b.Low = b.Open
			}
			out.Bars[i] = b
		}
	}

	// Exchange outage: a contiguous run of bars vanishes at the shock point.
	if e.OutageBars > 0 && shockIdx < len(out.Bars) {
		end := shockIdx + e.OutageBars
		if end > len(out.Bars) {
			end = len(out.Bars)
		}
		out.Bars = append(out.Bars[:shockIdx:shockIdx], out.Bars[end:]...)
		out.Regimes = dropRange(out.Regimes, shockIdx, end)
	}

	// Missing candles: independent seeded drops, never the first bar (the
	// window needs an anchor to be a window at all).
	if e.DropFrac > 0 && len(out.Bars) > 1 {
		kept := out.Bars[:1]
		keptR := []string(nil)
		if out.Regimes != nil {
			keptR = out.Regimes[:1:1]
		}
		for i := 1; i < len(out.Bars); i++ {
			if rng.Float64() < e.DropFrac {
				continue
			}
			kept = append(kept, out.Bars[i])
			if out.Regimes != nil {
				keptR = append(keptR, out.Regimes[i])
			}
		}
		out.Bars = kept
		out.Regimes = keptR
	}

	out.SpreadMult *= e.SpreadMult
	if out.ADVUSD > 0 {
		out.ADVUSD *= e.ADVMult
	}
	out.DelayBars += e.DelayBars

	sort.Slice(out.Signals, func(i, j int) bool { return out.Signals[i].Ts < out.Signals[j].Ts })
	return out
}

func scaleBar(b md.Bar, k float64) md.Bar {
	b.Open *= k
	b.High *= k
	b.Low *= k
	b.Close *= k
	return b
}

func dropRange(s []string, from, to int) []string {
	if s == nil || from >= len(s) {
		return s
	}
	if to > len(s) {
		to = len(s)
	}
	return append(s[:from:from], s[to:]...)
}
