package options

import (
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

// minInstances is the fewest non-overlapping walk-forward windows this symbol
// must have before its conditional vol levels are reported. Below it the levels
// are noise and the honest output is no forecast at all — the same discipline
// the rest of the platform applies to thin history.
const minInstances = 8

// VolStats is what forward volatility ACTUALLY looked like on this symbol,
// measured over the same non-overlapping walk-forward windows that the
// vol-regime predictor was validated on (internal/volregime.History). Nothing
// here is assumed: every number is this symbol's own realized history.
type VolStats struct {
	Horizon int `json:"horizon"`
	// N is the number of non-overlapping windows behind these levels.
	N int `json:"n"`
	// Accuracy is the predictor's hit rate ON THIS SYMBOL's walk — the local
	// check against the fleet-wide measured tiers, not a replacement for them.
	Accuracy float64 `json:"accuracy"`
	// ElevatedVol / CalmVol are the MEAN annualized realized vol that followed
	// windows whose ACTUAL regime was elevated / calm. These two levels are the
	// two outcomes a live call is choosing between.
	ElevatedVol float64 `json:"elevatedVol"`
	CalmVol     float64 `json:"calmVol"`
	ElevatedN   int     `json:"elevatedN"`
	CalmN       int     `json:"calmN"`
	// Unconditional / Median are the same history ignoring the regime label —
	// the null a forecast has to beat to be worth anything.
	Unconditional float64 `json:"unconditional"`
	Median        float64 `json:"median"`
	// P10 / P90 bracket the realized range this symbol has actually produced.
	P10 float64 `json:"p10"`
	P90 float64 `json:"p90"`
	// Realized21 / Realized63 are the CURRENT trailing realized vols — where
	// the symbol is right now, before any forecast.
	Realized21 float64 `json:"realized21"`
	Realized63 float64 `json:"realized63"`
}

// Stats summarizes a symbol's walk-forward vol history. rets are the symbol's
// daily simple returns (oldest first), insts the replay from
// volregime.History over the same returns. ok=false when either regime group is
// empty or the walk is too short — an honest absence, never a one-sided level.
func Stats(insts []volregime.Instance, rets []float64, horizon int) (VolStats, bool) {
	st := VolStats{Horizon: horizon, N: len(insts)}
	if len(insts) < minInstances {
		return st, false
	}
	var elev, calm, all []float64
	correct := 0
	for _, in := range insts {
		if in.ForwardVol <= 0 {
			continue
		}
		all = append(all, in.ForwardVol)
		if in.Correct {
			correct++
		}
		if in.Actual == "elevated" {
			elev = append(elev, in.ForwardVol)
		} else {
			calm = append(calm, in.ForwardVol)
		}
	}
	if len(elev) == 0 || len(calm) == 0 || len(all) < minInstances {
		return st, false
	}
	st.N = len(all)
	st.Accuracy = float64(correct) / float64(len(insts))
	st.ElevatedVol, st.ElevatedN = mean(elev), len(elev)
	st.CalmVol, st.CalmN = mean(calm), len(calm)
	st.Unconditional = mean(all)
	st.Median = quantile(all, 0.5)
	st.P10, st.P90 = quantile(all, 0.1), quantile(all, 0.9)
	st.Realized21 = RealizedVol(rets, 21)
	st.Realized63 = RealizedVol(rets, 63)
	return st, true
}

// Expectation converts the binary regime call into the ONE number an options
// comparison needs: the expected annualized volatility over the horizon.
type Expectation struct {
	Regime     string  `json:"regime"`
	Conviction float64 `json:"conviction"`
	Tier       string  `json:"tier"`
	// Accuracy is the MEASURED fleet-wide walk-forward accuracy of this call's
	// conviction band (volregime.AccuracyForConviction) — never invented.
	Accuracy float64 `json:"accuracy"`
	// IfRight / IfWrong are this symbol's own conditional vol levels: what
	// realized vol has looked like when the call landed and when it did not.
	IfRight float64 `json:"ifRight"`
	IfWrong float64 `json:"ifWrong"`
	// Expected is the probability-weighted mix — Accuracy*IfRight +
	// (1-Accuracy)*IfWrong. It is the honest point forecast precisely BECAUSE
	// it prices in being wrong ~25-30% of the time.
	Expected float64 `json:"expected"`
	// Realized63 is where trailing vol sits now, for reference.
	Realized63 float64 `json:"realized63"`
	// LevelDrift is Expected / Realized63 — how far the historical conditional
	// level sits from where this symbol's volatility actually is today.
	LevelDrift float64 `json:"levelDrift"`
	// DriftWarning is set when that ratio is large enough that the LEVEL
	// extrapolation, not the validated regime call, is driving the forecast.
	DriftWarning string `json:"driftWarning,omitempty"`
	// Basis states, in one line, exactly what is measured and what is not.
	Basis string `json:"basis"`
}

// Expect mixes this symbol's two conditional outcomes by the measured accuracy
// of the live call's conviction band. ok=false when the call itself has no
// measurable edge (bottom band) — at a coin-flip conviction the mixture is just
// the unconditional mean dressed up as a forecast, and saying so is better.
func Expect(f volregime.Forecast, st VolStats) (Expectation, bool) {
	acc := volregime.AccuracyForConviction(f.Conviction)
	e := Expectation{
		Regime: f.Regime, Conviction: f.Conviction, Tier: f.Tier, Accuracy: acc,
		Realized63: st.Realized63,
		Basis: "Expected vol = measured band accuracy x this symbol's own mean realized vol after ELEVATED windows + (1-accuracy) x its mean after CALM windows. The ACCURACY is fleet-measured (~900 stocks, 7.5y, non-overlapping, quarter-clustered). The two VOL LEVELS are descriptive statistics of this one symbol's history over " +
			"a small number of independent windows — they were NOT part of the validated backtest, and the mapping from a binary regime label to a vol LEVEL is an extrapolation the 69.7-76.0% hit rate does not cover.",
	}
	if st.N < minInstances || st.ElevatedVol <= 0 || st.CalmVol <= 0 {
		return e, false
	}
	if f.Conviction < 0.25 {
		return e, false // "no measurable edge" band — refuse rather than dress up the null
	}
	if f.Regime == "elevated" {
		e.IfRight, e.IfWrong = st.ElevatedVol, st.CalmVol
	} else {
		e.IfRight, e.IfWrong = st.CalmVol, st.ElevatedVol
	}
	e.Expected = acc*e.IfRight + (1-acc)*e.IfWrong
	// The conditional levels are averages over this symbol's WHOLE history, so
	// they can sit far from today's vol — a 2020-heavy "elevated" average
	// applied to a currently quiet symbol is the level extrapolation talking,
	// not the validated regime call. Say so instead of letting the number pass.
	if st.Realized63 > 0 {
		e.LevelDrift = e.Expected / st.Realized63
		if e.LevelDrift > driftHigh || e.LevelDrift < driftLow {
			e.DriftWarning = "the expected level sits far from this symbol's CURRENT trailing volatility (ratio " +
				trimFloat(e.LevelDrift) + "x). The conditional levels are averages over the symbol's whole history, so the LEVEL extrapolation — the part that was never validated — is doing more work here than the regime call. Weigh the verdict accordingly."
		}
	}
	return e, true
}

// driftLow / driftHigh bound the ratio of expected vol to current trailing vol
// before the level extrapolation is flagged as the dominant assumption.
const (
	driftLow  = 0.67
	driftHigh = 1.5
)

// trimFloat renders a ratio to two decimals without pulling in fmt.
func trimFloat(v float64) string {
	n := int(math.Round(v * 100))
	return itoa(n/100) + "." + itoa2(n%100)
}

func itoa2(n int) string {
	if n < 0 {
		n = -n
	}
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// RealizedVol is the annualized realized volatility of the last n daily
// returns. Zero when there is not enough history — never a partial-window
// number passed off as an n-day vol.
func RealizedVol(rets []float64, n int) float64 {
	if len(rets) < n || n < 2 {
		return 0
	}
	w := rets[len(rets)-n:]
	return stdev(w) * math.Sqrt(volregime.TradingDaysPerYear)
}

// ── small stats helpers (dependency-free) ──

func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func stdev(x []float64) float64 {
	if len(x) < 2 {
		return 0
	}
	m := mean(x)
	var v float64
	for _, e := range x {
		v += (e - m) * (e - m)
	}
	return math.Sqrt(v / float64(len(x)-1))
}

func quantile(x []float64, q float64) float64 {
	if len(x) == 0 {
		return 0
	}
	a := append([]float64(nil), x...)
	sort.Float64s(a)
	i := int(q * float64(len(a)-1))
	if i < 0 {
		i = 0
	}
	if i >= len(a) {
		i = len(a) - 1
	}
	return a[i]
}
