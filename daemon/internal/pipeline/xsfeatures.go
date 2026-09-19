package pipeline

import (
	"context"
	"sort"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/xsfactor"
)

// CROSS-SECTIONAL FEATURES FOR THE ALPHAX LEG
//
// WHY THIS EXISTS
// The cross-sectional alpha leg is fully wired — pipeline reads it, the
// ensemble blends it — and it has been dark because the model behind it grades
// oos_lift = -0.0072 with oos_auc = 0.5011. A coin flip, correctly gated off.
//
// Its 62 features are all computed from ONE symbol's trailing window: technical
// indicators, macro series, sentiment. Measured over 1,883 days of point-in-time
// universe (tools/alpha/xsection.py), the factors that actually rank the
// cross-section were absent from that list entirely:
//
//	dollar_vol_21   IC +0.02497  t +11.48
//	vol_21          IC -0.02917  t  -6.09
//	mom_252_21      IC +0.02788  t  +5.65
//	rev_1           IC +0.02116  t  +5.55
//
// all surviving Bonferroni over 20 tests, and combining them with weights and
// signs fitted on train alone ranks out-of-sample at IC +0.0497, t +8.71 over
// 561 held-out days (tools/alpha/xscore.py).
//
// A per-symbol window CANNOT produce these: a percentile is a statement about a
// symbol's position among its peers on that date, so it needs the whole
// cross-section at once. That is why they were missing, and why adding them is
// a pass-level computation rather than another entry in the per-symbol builder.
//
// WHAT THIS DOES NOT DO
// It does not open the gate. The alphax trainer still has to grade a model
// built on these features and measure oos_lift > 0 before a single forecast row
// is written. Supplying a feature that ranks is not the same as demonstrating
// that a model using it earns its place, and the second claim is the trainer's
// to make.

// xsFeatures is one symbol's cross-sectional percentiles for the pass.
type xsFeatures struct {
	liquidityPct float64
	lowVolPct    float64
	mom121Pct    float64
	rev1Pct      float64
}

// vec writes the features under stable keys. Absent legs are simply not
// written: a missing percentile must read as missing, never as 0.5, which
// would place a symbol at the median of a cross-section it was not in.
func (f xsFeatures) vec() map[string]float64 {
	return map[string]float64{
		"xs_liquidity_pct": f.liquidityPct,
		"xs_lowvol_pct":    f.lowVolPct,
		"xs_mom121_pct":    f.mom121Pct,
		"xs_rev1_pct":      f.rev1Pct,
	}
}

// crossSectionalFeatures computes the percentiles for every symbol in one pass.
//
// ONE batched read for the whole universe, like the screener: a per-symbol
// query here would be ~1,300 round-trips per pass.
//
// Returns an empty map rather than an error when the universe is too thin to
// rank — a percentile over five names is not a cross-section, and a feature
// that is silently meaningless is worse than one that is absent.
func crossSectionalFeatures(ctx context.Context, st *store.Store, syms []md.Symbol) map[int64]xsFeatures {
	const minUniverse = 20

	stocks := make([]md.Symbol, 0, len(syms))
	for _, s := range syms {
		if s.Market == md.Stocks {
			stocks = append(stocks, s)
		}
	}
	if len(stocks) < minUniverse {
		return map[int64]xsFeatures{}
	}

	ids := make([]int64, len(stocks))
	byID := make(map[int64]md.Symbol, len(stocks))
	for i, s := range stocks {
		ids[i] = s.ID
		byID[s.ID] = s
	}
	bars, err := st.LastBarsBatch(ctx, ids, md.TF1d, xsfactor.TrailingBars)
	if err != nil {
		return map[int64]xsFeatures{}
	}
	for id, b := range bars { // settled bars only: rev1 used the forming close as "last" (2026-09-07)
		bars[id], _ = trimFormingDaily(byID[id].Market, b, time.Now().Unix())
	}

	inputs := make([]xsfactor.Input, 0, len(ids))
	idBySymbol := make(map[string]int64, len(ids))
	// rev1 is not one of xsfactor's legs, so it is ranked here. It is the
	// NEGATED last daily return: yesterday's losers lead, which is the sign the
	// measurement found (+0.0212, t +5.55).
	rev1 := make(map[int64]float64, len(ids))
	for _, id := range ids {
		s := byID[id]
		bs := bars[id]
		if len(bs) < 2 {
			continue
		}
		in := xsfactor.Input{
			Symbol:     s.Symbol,
			Market:     string(s.Market),
			Closes:     make([]float64, len(bs)),
			DollarVols: make([]float64, len(bs)),
		}
		for i, b := range bs { // ascending ts
			in.Closes[i] = b.Close
			in.DollarVols[i] = b.Close * b.Volume
		}
		prev, last := bs[len(bs)-2].Close, bs[len(bs)-1].Close
		if prev > 0 {
			rev1[id] = -(last/prev - 1)
		}
		inputs = append(inputs, in)
		idBySymbol[s.Symbol] = id
	}
	if len(inputs) < minUniverse {
		return map[int64]xsFeatures{}
	}

	// H21d matches the horizon whose legs xsfactor publishes an edge for; the
	// percentiles themselves are horizon-independent point-in-time ranks.
	res, err := xsfactor.Rank(xsfactor.H21d, inputs)
	if err != nil {
		return map[int64]xsFeatures{}
	}

	rev1Ranks := percentileRank(rev1)
	out := make(map[int64]xsFeatures, len(res.Rows))
	for _, row := range res.Rows {
		id, ok := idBySymbol[row.Symbol]
		if !ok {
			continue
		}
		var f xsFeatures
		// A nil percentile means the leg was uncomputable for this symbol. Skip
		// the symbol rather than substitute a value it did not earn.
		if row.LiquidityPct == nil || row.LowVolPct == nil || row.Mom121Pct == nil {
			continue
		}
		f.liquidityPct = *row.LiquidityPct
		f.lowVolPct = *row.LowVolPct
		f.mom121Pct = *row.Mom121Pct
		r, ok := rev1Ranks[id]
		if !ok {
			continue
		}
		f.rev1Pct = r
		out[id] = f
	}
	return out
}

// percentileRank maps values to [0,1] by rank. Ties share the average rank so
// the transform is order-preserving and deterministic.
func percentileRank(vals map[int64]float64) map[int64]float64 {
	n := len(vals)
	if n < 2 {
		return map[int64]float64{}
	}
	type kv struct {
		id int64
		v  float64
	}
	xs := make([]kv, 0, n)
	for id, v := range vals {
		xs = append(xs, kv{id, v})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].v != xs[j].v {
			return xs[i].v < xs[j].v
		}
		return xs[i].id < xs[j].id // deterministic on ties
	})
	out := make(map[int64]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && xs[j+1].v == xs[i].v {
			j++
		}
		avg := (float64(i) + float64(j)) / 2 / float64(n-1)
		for k := i; k <= j; k++ {
			out[xs[k].id] = avg
		}
		i = j + 1
	}
	return out
}
