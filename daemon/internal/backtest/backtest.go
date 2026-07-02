// Package backtest is SignalDeck's bias-free, long/flat strategy backtester and
// a deterministic plain-English strategy parser (the CopilotQuant capability).
//
// Honesty is the brand. Two design rules make every result defensible:
//
//   - NO LOOKAHEAD. A signal for bar i is computed only from bars[0..i]
//     (indices <= i). The resulting order fills at the NEXT bar's open
//     (bar i+1). A strategy can therefore never trade on the very bar whose
//     data produced the signal, which is the single most common way naive
//     backtests inflate returns.
//
//   - COSTS ARE EXPLICIT. Strategy.CostBps is charged as a fraction of equity
//     on every entry AND every exit (a round trip pays it twice). Results also
//     carry the trade count so a low-N, cost-sensitive result reads as
//     low-confidence rather than as a discovery.
//
// The backtester is pure: bars in (ascending by Ts), a Result out. No I/O, no
// persistence, no clock, no randomness. It imports only the stdlib and the
// marketdata contract, so it is safe to run anywhere the bars are available.
//
// Assumptions and limitations (read before trusting a number):
//
//   - Long/flat only. There is no shorting and no leverage; "flat" earns 0%.
//   - Fills are at the next bar's OPEN with no slippage model beyond CostBps.
//     Real fills can be worse, especially in illiquid names or gaps.
//   - CostBps is a single round-trip-style proxy for commission+spread+slippage.
//     It is charged per side; it does not model market impact or partial fills.
//   - Sharpe uses rf=0 and annualizes bar-to-bar returns by sqrt(252). That
//     constant assumes ~252 daily bars/year; on 1h or 1m bars it is wrong and
//     the Sharpe should be read as relative, not absolute.
//   - No survivorship or corporate-action handling — that is the caller's job
//     when assembling the bar series.
//   - This is IN-SAMPLE by construction: Backtest fits nothing, but a strategy
//     chosen because it looked good on these very bars is still overfit. Treat
//     a single backtest as a hypothesis, not evidence.
package backtest

import (
	"fmt"
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Rule is one enter- or exit-condition. Indicator selects the family and the
// other fields parameterize it; unused fields for a given Indicator are
// ignored. Supported Indicator values:
//
//   - "sma_cross":   Fast/Slow SMA crossover. Long signal when the Fast SMA is
//     at or above the Slow SMA, flat otherwise. Op is ignored (the direction
//     is implied by entry vs exit; see Strategy).
//   - "price_vs_sma": close vs its own Period SMA. Op ">" means "price above
//     SMA", "<" means "price below SMA".
//   - "rsi":         Wilder RSI over Period. Op ("<" / ">" / "<=" / ">=")
//     compares RSI to Threshold.
//   - "roc":         rate of change over Period bars. Op compares ROC (as a
//     fraction, e.g. 0.05 for +5%) to Threshold.
type Rule struct {
	Indicator string  // "sma_cross" | "rsi" | "price_vs_sma" | "roc"
	Fast      int     // sma_cross: fast window
	Slow      int     // sma_cross: slow window
	Period    int     // rsi / price_vs_sma / roc: lookback
	Threshold float64 // rsi / roc: comparison value (roc as a fraction)
	Op        string  // "<" | ">" | "<=" | ">=" (rsi/roc/price_vs_sma)
}

// Strategy is a named long/flat rule pair with an explicit trading cost.
// Entry is evaluated while flat (true => go long next bar); Exit is evaluated
// while long (true => go flat next bar). CostBps is charged per side in basis
// points of equity (1 bp = 0.01%).
type Strategy struct {
	Name    string
	Entry   Rule
	Exit    Rule
	CostBps float64
}

// Result is the outcome of a Backtest. Returns are expressed as fractions
// (0.10 == +10%). Equity starts at 1.0.
type Result struct {
	TotalReturn float64   // final equity / 1.0 - 1
	CAGR        float64   // compound annual growth rate (see assumptions on bar spacing)
	MaxDrawdown float64   // worst peak-to-trough decline of the equity curve, as a positive fraction
	Sharpe      float64   // mean/stdev of per-bar returns * sqrt(252); rf=0
	NumTrades   int       // completed + open entries (each entry counts once)
	WinRate     float64   // fraction of CLOSED trades with net-positive return
	ExposurePct float64   // fraction of bars spent long, in [0,1]
	Equity      []float64 // equity curve aligned to bars; len == len(bars)
	VsBuyHold   float64   // TotalReturn minus buy-and-hold total return
}

// Backtest runs Strategy s over bars (ascending by Ts) with strict next-bar
// execution and per-side costs, and returns performance measures.
//
// Mechanics, precisely:
//
//   - For each bar i, the position held DURING bar i was decided using data up
//     to bar i-1 only (the signal on bar i-1 fills at bar i's open). Equity
//     compounds by the in-position bar return open->? — we use close-to-close
//     returns while long, which is the standard bar-return convention and does
//     not peek: the return earned on bar i is close[i]/close[i-1]-1 and the
//     decision to be long for bar i was already locked in at bar i-1.
//   - The signal computed on the LAST bar can only fill after the data ends, so
//     it never affects equity; it is reflected in NumTrades only if it opens a
//     position that is then force-closed at the final bar for accounting.
//
// It returns an error if fewer than 2 bars are supplied or the strategy rules
// are unusable for the given data.
func Backtest(bars []marketdata.Bar, s Strategy) (Result, error) {
	if len(bars) < 2 {
		return Result{}, fmt.Errorf("backtest: need at least 2 bars, got %d", len(bars))
	}
	if err := validateRule(s.Entry, "entry"); err != nil {
		return Result{}, err
	}
	if err := validateRule(s.Exit, "exit"); err != nil {
		return Result{}, err
	}

	cost := s.CostBps / 10000.0 // bps -> fraction

	// The no-lookahead guarantee lives in the loop below: the position held
	// during bar i is decided from bars[:i] (data through bar i-1 only), so the
	// signal computed at index i-1 becomes a fill at bar i — never a same-bar
	// fill on the signal bar itself.
	n := len(bars)
	equity := make([]float64, n)
	equity[0] = 1.0

	long := false           // position held DURING the current bar
	var entryEquity float64 // equity at the bar we went long (for per-trade P&L)
	longBars := 0
	numTrades := 0
	wins := 0
	closedTrades := 0
	perBarRet := make([]float64, 0, n-1)

	for i := 1; i < n; i++ {
		// Position for bar i was decided at bar i-1 (fills at bar i's open).
		wantLong := decide(bars[:i], s, long /* prior state at i-1 */)

		// Apply the transition at the OPEN of bar i.
		eq := equity[i-1]
		if wantLong && !long {
			// Enter: pay cost.
			eq *= (1 - cost)
			long = true
			numTrades++
			entryEquity = eq
		} else if !wantLong && long {
			// Exit: pay cost, then tally the trade.
			eq *= (1 - cost)
			long = false
			closedTrades++
			if eq > entryEquity {
				wins++
			}
		}

		// Earn the bar return if long during bar i (close-to-close, no peek).
		ret := 0.0
		if long {
			prev := bars[i-1].Close
			if prev != 0 {
				ret = bars[i].Close/prev - 1
			}
			eq *= (1 + ret)
			longBars++
		}
		perBarRet = append(perBarRet, ret)
		equity[i] = eq
	}

	// Force-close an open position at the final bar so its P&L is counted.
	if long {
		final := equity[n-1] * (1 - cost)
		closedTrades++
		if final > entryEquity {
			wins++
		}
		equity[n-1] = final
	}

	res := Result{Equity: equity}
	res.TotalReturn = equity[n-1] - 1
	res.NumTrades = numTrades
	if closedTrades > 0 {
		res.WinRate = float64(wins) / float64(closedTrades)
	}
	res.ExposurePct = float64(longBars) / float64(n-1)
	res.MaxDrawdown = maxDrawdown(equity)
	res.Sharpe = sharpe(perBarRet)
	res.CAGR = cagr(equity[n-1], n-1)

	bh := buyHoldReturn(bars)
	res.VsBuyHold = res.TotalReturn - bh
	return res, nil
}

// decide returns whether we want to be long for the NEXT bar, given history
// bars[0..i] (i == len(hist)-1) and whether we are currently long. While flat
// the Entry rule is consulted: if its condition fires we want long. While long
// the Exit rule is consulted: if its condition fires we want flat. If the
// relevant rule cannot be evaluated yet (not enough bars) the position is left
// unchanged. Every rule's eval returns "the rule's ACTION should fire", so
// there is no negation here — the entry/exit asymmetry lives inside eval (see
// the `entry` flag), which keeps the sma_cross golden/death direction correct.
func decide(hist []marketdata.Bar, s Strategy, currentlyLong bool) bool {
	if currentlyLong {
		exitFires, ok := eval(hist, s.Exit, false /* exit role */)
		if !ok {
			return true // can't evaluate exit yet -> stay long
		}
		return !exitFires // exit fires => want flat
	}
	entryFires, ok := eval(hist, s.Entry, true /* entry role */)
	if !ok {
		return false // can't evaluate entry yet -> stay flat
	}
	return entryFires
}

// eval reports whether a Rule's ACTION should fire given history (last element
// is "now"). `entry` says whether this is the entry (true) or exit (false)
// rule; it only matters for sma_cross, whose entry action is the golden cross
// (fast>=slow) and whose exit action is the death cross (fast<slow). For the
// comparison indicators the Op already encodes the direction, so `entry` is
// unused. ok=false means the indicator had insufficient data at this point in
// time.
func eval(hist []marketdata.Bar, r Rule, entry bool) (bool, bool) {
	switch r.Indicator {
	case "sma_cross":
		fast, okf := sma(hist, r.Fast)
		slow, oks := sma(hist, r.Slow)
		if !okf || !oks {
			return false, false
		}
		// Entry action fires on the golden cross (fast>=slow); exit action
		// fires on the death cross (fast<slow). This makes the two rules of a
		// crossover strategy symmetric and lookahead-free.
		if entry {
			return fast >= slow, true
		}
		return fast < slow, true
	case "price_vs_sma":
		avg, ok := sma(hist, r.Period)
		if !ok {
			return false, false
		}
		price := hist[len(hist)-1].Close
		return compare(price, r.Op, avg), true
	case "rsi":
		v, ok := rsi(hist, r.Period)
		if !ok {
			return false, false
		}
		return compare(v, r.Op, r.Threshold), true
	case "roc":
		v, ok := roc(hist, r.Period)
		if !ok {
			return false, false
		}
		return compare(v, r.Op, r.Threshold), true
	default:
		return false, false
	}
}

// compare applies a comparison operator. An unknown op yields false.
func compare(x float64, op string, y float64) bool {
	switch op {
	case "<":
		return x < y
	case "<=":
		return x <= y
	case ">":
		return x > y
	case ">=":
		return x >= y
	default:
		return false
	}
}

// validateRule rejects rules whose parameters can never produce a signal.
func validateRule(r Rule, which string) error {
	switch r.Indicator {
	case "sma_cross":
		if r.Fast <= 0 || r.Slow <= 0 {
			return fmt.Errorf("backtest: %s sma_cross needs Fast>0 and Slow>0", which)
		}
		if r.Fast >= r.Slow {
			return fmt.Errorf("backtest: %s sma_cross needs Fast<Slow, got %d/%d", which, r.Fast, r.Slow)
		}
	case "price_vs_sma":
		if r.Period <= 0 {
			return fmt.Errorf("backtest: %s price_vs_sma needs Period>0", which)
		}
		if r.Op != "<" && r.Op != ">" && r.Op != "<=" && r.Op != ">=" {
			return fmt.Errorf("backtest: %s price_vs_sma needs Op in <,>,<=,>=", which)
		}
	case "rsi":
		if r.Period <= 0 {
			return fmt.Errorf("backtest: %s rsi needs Period>0", which)
		}
		if r.Op != "<" && r.Op != ">" && r.Op != "<=" && r.Op != ">=" {
			return fmt.Errorf("backtest: %s rsi needs Op in <,>,<=,>=", which)
		}
	case "roc":
		if r.Period <= 0 {
			return fmt.Errorf("backtest: %s roc needs Period>0", which)
		}
		if r.Op != "<" && r.Op != ">" && r.Op != "<=" && r.Op != ">=" {
			return fmt.Errorf("backtest: %s roc needs Op in <,>,<=,>=", which)
		}
	default:
		return fmt.Errorf("backtest: %s has unknown Indicator %q", which, r.Indicator)
	}
	return nil
}

// buyHoldReturn is the passive close-to-close return over the full window.
func buyHoldReturn(bars []marketdata.Bar) float64 {
	first := bars[0].Close
	last := bars[len(bars)-1].Close
	if first == 0 {
		return 0
	}
	return last/first - 1
}

// maxDrawdown is the worst peak-to-trough decline of an equity curve as a
// positive fraction (0.2 == a 20% drawdown). A monotonically rising curve
// returns 0.
func maxDrawdown(eq []float64) float64 {
	peak := math.Inf(-1)
	worst := 0.0
	for _, v := range eq {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			dd := 1 - v/peak
			if dd > worst {
				worst = dd
			}
		}
	}
	return worst
}

// sharpe is mean(rets)/stdev(rets) annualized by sqrt(252). rf=0. A
// constant-return or empty series returns 0 (undefined risk-adjusted return).
// The sample stdev uses an n-1 denominator.
func sharpe(rets []float64) float64 {
	if len(rets) < 2 {
		return 0
	}
	var sum float64
	for _, r := range rets {
		sum += r
	}
	mean := sum / float64(len(rets))
	var ss float64
	for _, r := range rets {
		d := r - mean
		ss += d * d
	}
	variance := ss / float64(len(rets)-1)
	if variance <= 0 {
		return 0
	}
	return mean / math.Sqrt(variance) * math.Sqrt(252)
}

// cagr is the compound annual growth rate implied by finalEquity over
// `periods` bars, assuming 252 bars per year (see package assumptions). With
// non-positive equity or zero periods it returns 0.
func cagr(finalEquity float64, periods int) float64 {
	if periods <= 0 || finalEquity <= 0 {
		return 0
	}
	years := float64(periods) / 252.0
	if years <= 0 {
		return 0
	}
	return math.Pow(finalEquity, 1/years) - 1
}
