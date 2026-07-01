// Package insights is SignalDeck's readable layer: a deterministic,
// rule-based composer that turns already-computed scores and expectancy rows
// into plain-English insights. It is pure — no store access, no I/O, no LLM.
// Every number that appears in the prose is copied from the inputs; nothing
// is ever invented, and every symbol read carries a mandatory honesty line.
package insights

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// SymbolContext is everything ComposeSymbol is allowed to quote about one
// symbol. Maps may be nil or partial — missing pieces degrade to honest
// fallback sentences rather than invented numbers.
type SymbolContext struct {
	// Sym identifies the instrument being read.
	Sym md.Symbol
	// LastClose is the most recent daily close price.
	LastClose float64
	// DayChangePct is the day change already in percent units (-1.34 = -1.34%).
	DayChangePct float64
	// Scores holds the latest Pressure Score per horizon; may be partial.
	Scores map[md.Horizon]md.Score
	// Expect holds the expectancy row matching the CURRENT state per horizon;
	// nil entries (or a nil map) mean no comparable history.
	Expect map[md.Horizon]*md.Expectancy
	// StateKeys holds the current machine state key per horizon (fallback for
	// humanizing when the expectancy row lacks one).
	StateKeys map[md.Horizon]string
	// Stale flags a data-quality caveat; StaleFor says how far behind we are.
	Stale    bool
	StaleFor time.Duration
}

// MarketBrief is one symbol's contribution to the market-wide read.
type MarketBrief struct {
	Symbol       string
	Market       md.Market
	Score1d      float64
	DayChangePct float64
}

// noDriver is the headline fallback when the 1d score has no components.
const noDriver = "no dominant driver"

// symbolEvidence is the raw-numbers blob stored in Insight.Data so the UI can
// show exactly what the prose was built from.
type symbolEvidence struct {
	Symbol       string             `json:"symbol"`
	LastClose    float64            `json:"lastClose"`
	DayChangePct float64            `json:"dayChangePct"`
	Scores       map[string]float64 `json:"scores,omitempty"`
	Driver1d     string             `json:"driver1d,omitempty"`
	StateKey     string             `json:"stateKey,omitempty"`
	N            int                `json:"n,omitempty"`
	HitRate      float64            `json:"hitRate,omitempty"`
	MedianFwd    float64            `json:"medianFwd,omitempty"`
	Stale        bool               `json:"stale,omitempty"`
	StaleForSec  int64              `json:"staleForSec,omitempty"`
}

// marketEvidence is the raw-numbers blob behind a market-scope insight.
type marketEvidence struct {
	Total       int     `json:"total"`
	Positive    int     `json:"positive"`
	Negative    int     `json:"negative"`
	Regime      string  `json:"regime"`
	Best        string  `json:"best,omitempty"`
	BestScore   float64 `json:"bestScore,omitempty"`
	BestDayPct  float64 `json:"bestDayPct,omitempty"`
	Worst       string  `json:"worst,omitempty"`
	WorstScore  float64 `json:"worstScore,omitempty"`
	WorstDayPct float64 `json:"worstDayPct,omitempty"`
}

// ComposeSymbol renders one symbol's plain-English read from precomputed
// inputs. The body is 3–6 complete sentences and ALWAYS ends the evidence
// portion with an honesty line (measured tendency or explicit "not enough
// history"), plus a staleness caveat when the data is behind.
func ComposeSymbol(c SymbolContext, now time.Time) md.Insight {
	sc1d, has1d := c.Scores[md.H1d]
	driver := ""
	if has1d {
		driver = driver1d(sc1d)
	}

	var headline string
	switch {
	case has1d && driver != "":
		headline = fmt.Sprintf("%s %s: %s", c.Sym.Symbol, verdict(sc1d.Score), driver)
	case has1d:
		headline = fmt.Sprintf("%s %s: %s", c.Sym.Symbol, verdict(sc1d.Score), noDriver)
	default:
		// No 1d score to render a verdict from — say so instead of guessing.
		headline = fmt.Sprintf("%s: no 1-day read yet", c.Sym.Symbol)
	}

	var b []string

	// 1) Price context.
	b = append(b, fmt.Sprintf("%s last closed at %s (%s on the day).",
		c.Sym.Symbol, fmtPrice(c.LastClose), Pct(c.DayChangePct)))

	// 2) Per-horizon read, in display order, only for horizons we have.
	var reads []string
	for _, h := range md.Horizons {
		if s, ok := c.Scores[h]; ok {
			reads = append(reads, fmt.Sprintf("%s over the next %s (%+.2f)",
				verdict(s.Score), horizonNoun(h), s.Score))
		}
	}
	switch {
	case len(reads) == 0:
		b = append(b, "No horizon scores are available yet.")
	case driver != "":
		b = append(b, fmt.Sprintf("Pressure reads %s; the biggest 1-day driver: %s.",
			joinAnd(reads), driver))
	default:
		b = append(b, fmt.Sprintf("Pressure reads %s.", joinAnd(reads)))
	}

	// 3) + 4) Expectancy sentence, then the MANDATORY honesty line. A nil row
	// or n=0 means we have nothing comparable to quote — say so explicitly.
	e := c.Expect[md.H1d]
	if e != nil && e.N > 0 {
		state := e.StateKey
		if state == "" {
			state = c.StateKeys[md.H1d]
		}
		b = append(b, fmt.Sprintf(
			"Over the stored history, setups like this one (%s) resolved higher %.1f%% of the time over the next day (n=%d, median %s).",
			humanizeState(state), e.HitRate*100, e.N, Pct(e.MedianFwd*100)))
		b = append(b, fmt.Sprintf(
			"This is a measured historical tendency from n=%d samples, not a forecast.", e.N))
	} else {
		b = append(b, "Not enough comparable history to quote a tendency.")
	}

	// 5) Data-quality caveat.
	if c.Stale {
		b = append(b, fmt.Sprintf("Caveat: data is stale (%s) — treat this read as outdated.",
			fmtDur(c.StaleFor)))
	}

	ev := symbolEvidence{
		Symbol:       c.Sym.Symbol,
		LastClose:    c.LastClose,
		DayChangePct: c.DayChangePct,
		Driver1d:     driver,
		Stale:        c.Stale,
		StaleForSec:  int64(c.StaleFor / time.Second),
	}
	if len(c.Scores) > 0 {
		ev.Scores = make(map[string]float64, len(c.Scores))
		for h, s := range c.Scores {
			ev.Scores[string(h)] = s.Score
		}
	}
	if e != nil && e.N > 0 {
		ev.StateKey = e.StateKey
		if ev.StateKey == "" {
			ev.StateKey = c.StateKeys[md.H1d]
		}
		ev.N, ev.HitRate, ev.MedianFwd = e.N, e.HitRate, e.MedianFwd
	}

	id := c.Sym.ID
	return md.Insight{
		Scope:    "symbol",
		SymbolID: &id,
		Symbol:   c.Sym.Symbol,
		Ts:       now.Unix(),
		Headline: headline,
		Body:     strings.Join(b, " "),
		Data:     marshalEvidence(ev),
	}
}

// ComposeMarket renders the market-wide read: breadth, the best/worst symbol
// by 1-day score, and a one-line regime call. An empty slice degrades to an
// honest "nothing tracked" insight.
func ComposeMarket(briefs []MarketBrief, now time.Time) md.Insight {
	n := len(briefs)
	if n == 0 {
		return md.Insight{
			Scope:    "market",
			Ts:       now.Unix(),
			Headline: "Market: no tracked symbols",
			Body:     "No symbols are being tracked yet, so there is no market read. Add symbols to start measuring pressure. Not enough comparable history to quote a tendency.",
			Data:     marshalEvidence(marketEvidence{Regime: "none"}),
		}
	}

	pos, neg := 0, 0
	best, worst := 0, 0
	for i, br := range briefs {
		if br.Score1d > 0 {
			pos++
		} else if br.Score1d < 0 {
			neg++
		}
		if br.Score1d > briefs[best].Score1d {
			best = i
		}
		if br.Score1d < briefs[worst].Score1d {
			worst = i
		}
	}

	// Strict majority on either side; everything else is a split tape.
	regime := "split"
	regimeLine := "Regime read: split — buyers and sellers are roughly balanced across the tracked set."
	switch {
	case pos*2 > n:
		regime = "majority positive"
		regimeLine = "Regime read: majority positive — buy pressure dominates the tracked set."
	case neg*2 > n:
		regime = "majority negative"
		regimeLine = "Regime read: majority negative — sell pressure dominates the tracked set."
	}

	var b []string
	b = append(b, fmt.Sprintf("%d of %d tracked symbols show positive 1-day pressure.", pos, n))
	if n == 1 {
		br := briefs[0]
		b = append(b, fmt.Sprintf("The only tracked symbol is %s with a 1-day score of %+.2f (%s on the day).",
			br.Symbol, br.Score1d, Pct(br.DayChangePct)))
	} else {
		bb, wb := briefs[best], briefs[worst]
		b = append(b, fmt.Sprintf("Strongest is %s with a 1-day score of %+.2f (%s on the day); weakest is %s at %+.2f (%s on the day).",
			bb.Symbol, bb.Score1d, Pct(bb.DayChangePct),
			wb.Symbol, wb.Score1d, Pct(wb.DayChangePct)))
	}
	b = append(b, regimeLine)

	ev := marketEvidence{
		Total: n, Positive: pos, Negative: neg, Regime: regime,
		Best: briefs[best].Symbol, BestScore: briefs[best].Score1d, BestDayPct: briefs[best].DayChangePct,
		Worst: briefs[worst].Symbol, WorstScore: briefs[worst].Score1d, WorstDayPct: briefs[worst].DayChangePct,
	}

	return md.Insight{
		Scope:    "market",
		Ts:       now.Unix(),
		Headline: fmt.Sprintf("Market %s: %d of %d symbols positive over the next day", regime, pos, n),
		Body:     strings.Join(b, " "),
		Data:     marshalEvidence(ev),
	}
}

// Pct formats a value already in percent units with one decimal and an
// explicit sign (+1.2%, -0.8%) — change numbers must always carry direction.
func Pct(v float64) string { return fmt.Sprintf("%+.1f%%", v) }

// verdict maps a Pressure Score in [-1,+1] to the fixed verdict vocabulary.
// Boundaries follow the contract: >=0.5, >=0.15, >-0.15, >-0.5, else.
func verdict(s float64) string {
	switch {
	case s >= 0.5:
		return "strong buy pressure"
	case s >= 0.15:
		return "mild buy pressure"
	case s > -0.15:
		return "balanced"
	case s > -0.5:
		return "mild sell pressure"
	default:
		return "strong sell pressure"
	}
}

// driver1d picks the strongest driver phrase for the 1d score: the Note of
// the component with the largest |Contrib|. Returns "" when there is nothing
// usable, so callers can fall back rather than invent a driver.
func driver1d(sc md.Score) string {
	best := -1
	bestAbs := 0.0
	for i, comp := range sc.Components {
		if a := math.Abs(comp.Contrib); best == -1 || a > bestAbs {
			best, bestAbs = i, a
		}
	}
	if best == -1 {
		return ""
	}
	c := sc.Components[best]
	// Notes are one-liners that may already end with a period; trim it so the
	// phrase composes cleanly into headline and body.
	if note := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(c.Note), ".")); note != "" {
		return note
	}
	if c.Name != "" {
		return c.Name + " leads the composite"
	}
	return ""
}

// stateWords maps machine state tokens to reader-facing phrases. Unknown
// tokens fall back to the raw token with separators spaced out so no state
// information is silently dropped.
var stateWords = map[string]string{
	"rsi:low":     "oversold RSI",
	"rsi:high":    "overbought RSI",
	"rsi:mid":     "neutral RSI",
	"trend:above": "above its long-term trend",
	"trend:below": "below its long-term trend",
	"mom:up":      "positive momentum",
	"mom:down":    "negative momentum",
	"mom:flat":    "flat momentum",
	"rvol:high":   "elevated volume",
	"rvol:low":    "quiet volume",
	"rvol:normal": "normal volume",
}

// humanizeState turns a "|"-joined state key ("rsi:low|trend:above") into
// prose ("oversold RSI, above its long-term trend").
func humanizeState(key string) string {
	if key == "" {
		return "current conditions"
	}
	toks := strings.Split(key, "|")
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if w, ok := stateWords[t]; ok {
			out = append(out, w)
			continue
		}
		out = append(out, strings.ReplaceAll(strings.ReplaceAll(t, ":", " "), "_", " "))
	}
	if len(out) == 0 {
		return "current conditions"
	}
	return strings.Join(out, ", ")
}

// horizonNoun renders a horizon as the noun used in "over the next <noun>".
func horizonNoun(h md.Horizon) string {
	switch h {
	case md.H1h:
		return "hour"
	case md.H1d:
		return "day"
	case md.H1w:
		return "week"
	default:
		return string(h)
	}
}

// joinAnd joins clauses as natural English: "a", "a and b", "a, b, and c".
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

// fmtPrice keeps prices readable across asset classes: two decimals for
// instruments at $1+, trimmed higher precision for sub-dollar crypto.
func fmtPrice(v float64) string {
	if math.Abs(v) >= 1 {
		return fmt.Sprintf("%.2f", v)
	}
	s := fmt.Sprintf("%.6f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// fmtDur renders a staleness duration compactly ("45s", "12m", "2h30m",
// "3d4h") — time.Duration.String() is too noisy for prose.
func fmtDur(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute)/time.Minute))
	case d < 24*time.Hour:
		h := d / time.Hour
		m := (d % time.Hour) / time.Minute
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		days := d / (24 * time.Hour)
		h := (d % (24 * time.Hour)) / time.Hour
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, h)
	}
}

// marshalEvidence marshals the evidence blob; non-finite floats (the only way
// Marshal can fail here) degrade to an empty object rather than a panic.
func marshalEvidence(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
