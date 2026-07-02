package backtest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Parse turns a plain-English strategy description into a Strategy. It is a
// DETERMINISTIC, rule-based parser for a small set of common phrasings — an
// LLM parser will augment it later, but this layer must stay predictable and
// testable, so it recognizes patterns rather than "understanding" free text.
//
// Supported forms (case-insensitive; extra words are tolerated):
//
//   - Moving-average crossover:
//     "50/200 moving-average crossover", "golden cross 50 200",
//     "buy when the 50-day crosses above the 200-day"
//     => sma_cross, Entry fast/slow, Exit fast/slow (death cross), CostBps default.
//
//   - RSI mean reversion:
//     "buy when RSI below 30, sell above 70", "RSI < 25 buy, > 75 sell"
//     => rsi, Entry Op<Threshold(enter), Exit Op>Threshold(exit).
//
//   - Price vs a long moving average:
//     "buy above the 200-day, sell below", "buy when price is over the 100 day sma"
//     => price_vs_sma, Entry price > SMA(period), Exit price < SMA(period).
//
// A trailing "N bps cost" / "N bps" phrase overrides the default CostBps.
// Anything unrecognized returns an error that lists the supported forms.
func Parse(text string) (Strategy, error) {
	orig := strings.TrimSpace(text)
	if orig == "" {
		return Strategy{}, unsupported("")
	}
	lower := strings.ToLower(orig)

	cost := parseCostBps(lower)

	// Order matters: try the most specific / least ambiguous shapes first.
	if s, ok := parseSMACross(lower); ok {
		s.Name = orig
		s.CostBps = cost
		return s, nil
	}
	if s, ok := parseRSI(lower); ok {
		s.Name = orig
		s.CostBps = cost
		return s, nil
	}
	if s, ok := parsePriceVsSMA(lower); ok {
		s.Name = orig
		s.CostBps = cost
		return s, nil
	}
	return Strategy{}, unsupported(orig)
}

// twoNums pulls the first two integers out of s (in order).
var numRe = regexp.MustCompile(`\d+`)

func firstNInts(s string, n int) ([]int, bool) {
	found := numRe.FindAllString(s, -1)
	if len(found) < n {
		return nil, false
	}
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		v, err := strconv.Atoi(found[i])
		if err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// parseCostBps extracts an explicit "N bps" cost if present, else returns the
// default round-trip cost proxy of 10 bps per side. Documented default so a
// caller who says nothing about costs still pays a realistic, non-zero cost —
// a free backtest is a dishonest backtest.
func parseCostBps(lower string) float64 {
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*bps`)
	if m := re.FindStringSubmatch(lower); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	return 10.0
}

// parseSMACross recognizes moving-average crossover phrasings. It requires an
// explicit crossover cue plus two numbers, and orders them fast<slow.
func parseSMACross(lower string) (Strategy, bool) {
	crossCue := strings.Contains(lower, "cross") ||
		strings.Contains(lower, "golden") ||
		strings.Contains(lower, "death")
	maCue := strings.Contains(lower, "moving average") ||
		strings.Contains(lower, "moving-average") ||
		strings.Contains(lower, "ma ") ||
		strings.Contains(lower, "sma") ||
		strings.Contains(lower, "cross")
	if !crossCue || !maCue {
		return Strategy{}, false
	}
	nums, ok := firstNInts(lower, 2)
	if !ok {
		return Strategy{}, false
	}
	fast, slow := nums[0], nums[1]
	if fast > slow {
		fast, slow = slow, fast
	}
	if fast == slow || fast <= 0 {
		return Strategy{}, false
	}
	// Entry = golden cross (fast>=slow true), Exit = death cross (fast<slow).
	// eval("sma_cross") returns fast>=slow, so Entry true => long; the exit
	// rule's truth (fast>=slow) is negated inside decide() when we're long, so
	// we exit exactly when fast<slow. Op is unused by sma_cross.
	entry := Rule{Indicator: "sma_cross", Fast: fast, Slow: slow}
	exit := Rule{Indicator: "sma_cross", Fast: fast, Slow: slow}
	return Strategy{Entry: entry, Exit: exit}, true
}

// parseRSI recognizes "buy when RSI below X, sell above Y" family phrasings.
func parseRSI(lower string) (Strategy, bool) {
	if !strings.Contains(lower, "rsi") {
		return Strategy{}, false
	}
	// Pull the two thresholds. Prefer explicit below/above; fall back to the
	// two numbers in order (first=buy level, second=sell level).
	buyLvl, sellLvl, ok := rsiThresholds(lower)
	if !ok {
		return Strategy{}, false
	}
	entry := Rule{Indicator: "rsi", Period: 14, Op: "<", Threshold: buyLvl}
	exit := Rule{Indicator: "rsi", Period: 14, Op: ">", Threshold: sellLvl}
	return Strategy{Entry: entry, Exit: exit}, true
}

// rsiThresholds finds the buy (oversold) and sell (overbought) RSI levels.
func rsiThresholds(lower string) (buy, sell float64, ok bool) {
	// Look for "below/under/< N" (buy) and "above/over/> N" (sell) explicitly.
	belowRe := regexp.MustCompile(`(?:below|under|less than|<)\s*(\d+(?:\.\d+)?)`)
	aboveRe := regexp.MustCompile(`(?:above|over|greater than|>)\s*(\d+(?:\.\d+)?)`)
	var haveBuy, haveSell bool
	if m := belowRe.FindStringSubmatch(lower); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			buy, haveBuy = v, true
		}
	}
	if m := aboveRe.FindStringSubmatch(lower); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			sell, haveSell = v, true
		}
	}
	if haveBuy && haveSell {
		return buy, sell, true
	}
	// Fallback: two bare numbers, first is buy, second is sell.
	nums := numRe.FindAllString(lower, -1)
	if len(nums) >= 2 {
		b, e1 := strconv.ParseFloat(nums[0], 64)
		s, e2 := strconv.ParseFloat(nums[1], 64)
		if e1 == nil && e2 == nil {
			return b, s, true
		}
	}
	return 0, 0, false
}

// parsePriceVsSMA recognizes "buy above the N-day, sell below" phrasings.
func parsePriceVsSMA(lower string) (Strategy, bool) {
	// Needs a price-vs-average intent and a period.
	hasAbove := strings.Contains(lower, "above") || strings.Contains(lower, "over")
	hasDay := strings.Contains(lower, "day") || strings.Contains(lower, "sma") ||
		strings.Contains(lower, "moving average") || strings.Contains(lower, "moving-average")
	if !hasAbove || !hasDay {
		return Strategy{}, false
	}
	nums, ok := firstNInts(lower, 1)
	if !ok {
		return Strategy{}, false
	}
	period := nums[0]
	if period <= 0 {
		return Strategy{}, false
	}
	entry := Rule{Indicator: "price_vs_sma", Period: period, Op: ">"}
	exit := Rule{Indicator: "price_vs_sma", Period: period, Op: "<"}
	return Strategy{Entry: entry, Exit: exit}, true
}

// unsupported builds a helpful error naming the supported phrasings.
func unsupported(text string) error {
	return fmt.Errorf(`backtest: could not parse strategy %q. Supported forms:
  - moving-average crossover, e.g. "50/200 moving-average crossover"
  - RSI mean reversion, e.g. "buy when RSI below 30, sell above 70"
  - price vs a moving average, e.g. "buy above the 200-day, sell below"
Optionally append a cost, e.g. "... 5 bps cost" (default 10 bps/side)`, text)
}
