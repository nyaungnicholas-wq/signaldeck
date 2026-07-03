// Package sectors classifies tracked symbols into sectors/themes and aggregates
// per-symbol relative-strength inputs up to the sector level, so the app can
// answer "which sector is money rotating INTO right now, and which is it
// rotating OUT OF".
//
// Every function is pure: symbols and scores in, groupings and deltas out. No
// I/O, no persistence, no clock, no network, no marketdata dependency — it
// operates only on the small value types defined here plus the stdlib.
//
// HONESTY NOTES:
//
//   - The classification map is a hand-maintained STATIC lookup, not a live
//     data feed. An unknown symbol falls back to "Other" rather than being
//     silently forced into a bucket, so a sector aggregate never claims members
//     it cannot justify.
//
//   - Aggregation is a plain arithmetic mean of the caller-supplied scores; it
//     invents no data. Rotation is a same-name difference of two aggregate
//     snapshots — a description of how the cross-section moved between two
//     points in time, never a forecast of where it moves next.
package sectors

import "sort"

// OtherSector is the fallback sector name for any symbol not present in the
// static classification map.
const OtherSector = "Other"

// sectorMap is the static, hand-maintained symbol -> sector/theme lookup.
// Keys are canonical uppercased symbols (matching marketdata.Symbol.Symbol,
// e.g. "BTC/USD", "AAPL"). SectorOf normalizes case before lookup.
var sectorMap = map[string]string{
	// Technology single names.
	"AAPL":  "Technology",
	"NVDA":  "Technology",
	"AMD":   "Technology",
	"MSFT":  "Technology",
	"GOOGL": "Technology",
	"META":  "Technology",

	// Index / broad-market ETFs.
	"QQQ": "Tech/Index",
	"SPY": "Broad Index",

	// Consumer / autos.
	"TSLA": "Consumer/Auto",

	// Crypto & crypto-adjacent fintech.
	"COIN":    "Crypto/Fintech",
	"BTC/USD": "Crypto",
	"ETH/USD": "Crypto",

	// Financials.
	"JPM": "Financials",
	"BAC": "Financials",
	"GS":  "Financials",

	// Energy.
	"XOM": "Energy",
	"CVX": "Energy",
}

// SectorOf returns the sector/theme for symbol, matching the static map
// case-insensitively. Symbols not present in the map return OtherSector.
func SectorOf(symbol string) string {
	if sec, ok := sectorMap[toUpperASCII(symbol)]; ok {
		return sec
	}
	return OtherSector
}

// Input is one symbol's relative-strength reading fed into sector aggregation.
type Input struct {
	// Symbol is the canonical instrument symbol (e.g. "AAPL", "BTC/USD").
	Symbol string
	// Score is a 0..100 relative-strength rank score (higher = stronger).
	Score float64
	// Ret1M is the symbol's ~1-month total return, as a fraction (0.05 = +5%).
	Ret1M float64
}

// SectorAgg is the aggregated relative-strength reading for one sector.
type SectorAgg struct {
	// Sector is the sector/theme name (see the static map, or OtherSector).
	Sector string
	// MeanScore is the arithmetic mean of member Scores.
	MeanScore float64
	// MeanRet1M is the arithmetic mean of member Ret1M values.
	MeanRet1M float64
	// N is the number of member symbols.
	N int
	// Symbols lists the member symbols, in the order they first appeared in the
	// aggregation input.
	Symbols []string
}

// Aggregate groups inputs by SectorOf(Symbol), averaging Score and Ret1M within
// each sector and collecting member symbols. The result is sorted by MeanScore
// descending, so the strongest sector (what is rotating IN) is first. A nil or
// empty input yields an empty, non-nil slice.
func Aggregate(inputs []Input) []SectorAgg {
	// Accumulate per sector while preserving first-seen order for stable output.
	type acc struct {
		sumScore float64
		sumRet1M float64
		n        int
		symbols  []string
	}
	bySector := make(map[string]*acc)
	order := make([]string, 0)

	for _, in := range inputs {
		sec := SectorOf(in.Symbol)
		a, ok := bySector[sec]
		if !ok {
			a = &acc{}
			bySector[sec] = a
			order = append(order, sec)
		}
		a.sumScore += in.Score
		a.sumRet1M += in.Ret1M
		a.n++
		a.symbols = append(a.symbols, in.Symbol)
	}

	out := make([]SectorAgg, 0, len(order))
	for _, sec := range order {
		a := bySector[sec]
		agg := SectorAgg{
			Sector:  sec,
			N:       a.n,
			Symbols: a.symbols,
		}
		if a.n > 0 {
			agg.MeanScore = a.sumScore / float64(a.n)
			agg.MeanRet1M = a.sumRet1M / float64(a.n)
		}
		out = append(out, agg)
	}

	// Strongest sector first. Ties break by sector name for deterministic order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MeanScore != out[j].MeanScore {
			return out[i].MeanScore > out[j].MeanScore
		}
		return out[i].Sector < out[j].Sector
	})
	return out
}

// Move is a single sector's change in mean relative-strength score between two
// aggregate snapshots.
type Move struct {
	// Sector is the sector/theme name.
	Sector string
	// ScoreDelta is curr.MeanScore - prev.MeanScore. A missing side is treated
	// as a MeanScore of 0.
	ScoreDelta float64
}

// Rotation matches sectors by name across two aggregate snapshots and reports
// each sector's ScoreDelta = curr.MeanScore - prev.MeanScore. A sector present
// on only one side is treated as MeanScore 0 on the missing side. Results are
// sorted by ScoreDelta descending, so sectors money is rotating INTO are at the
// top and sectors it is rotating OUT OF are at the bottom. The returned slice
// is always non-nil.
func Rotation(prev, curr []SectorAgg) []Move {
	prevScore := make(map[string]float64, len(prev))
	for _, s := range prev {
		prevScore[s.Sector] = s.MeanScore
	}
	currScore := make(map[string]float64, len(curr))
	for _, s := range curr {
		currScore[s.Sector] = s.MeanScore
	}

	// Union of sector names, first-seen order (prev then curr) for stable ties.
	order := make([]string, 0, len(prev)+len(curr))
	seen := make(map[string]bool, len(prev)+len(curr))
	appendSector := func(name string) {
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}
	for _, s := range prev {
		appendSector(s.Sector)
	}
	for _, s := range curr {
		appendSector(s.Sector)
	}

	out := make([]Move, 0, len(order))
	for _, sec := range order {
		out = append(out, Move{
			Sector:     sec,
			ScoreDelta: currScore[sec] - prevScore[sec],
		})
	}

	// Money rotating in (largest positive delta) at the top.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ScoreDelta != out[j].ScoreDelta {
			return out[i].ScoreDelta > out[j].ScoreDelta
		}
		return out[i].Sector < out[j].Sector
	})
	return out
}

// toUpperASCII uppercases ASCII letters in s without allocating for the common
// already-uppercase case beyond the returned string. Non-ASCII bytes pass
// through unchanged (symbols are ASCII by contract).
func toUpperASCII(s string) string {
	b := []byte(s)
	changed := false
	for i := 0; i < len(b); i++ {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}
