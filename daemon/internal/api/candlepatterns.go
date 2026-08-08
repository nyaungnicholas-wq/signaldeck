// CANDLESTICK-PATTERNS wave — API (read-only, public read like the other
// descriptive context endpoints):
//
//	GET /api/candle-patterns?symbol=&market=&tf=1d — the candlestick patterns
//	firing over the most recent ~200 daily bars (bars with NO pattern omitted),
//	each annotated with its measured edge on THIS symbol's own history where the
//	pattern-stats worker had a large enough sample (n>=15), else null.
//
// HONESTY (shipped verbatim in every payload): candlestick patterns are WEAK,
// context-only signals; the measured hit-rate is descriptive, not advice.
package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/candles"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// candlePatternsNote ships verbatim with every /api/candle-patterns payload.
const candlePatternsNote = "candlestick patterns are WEAK, context-only signals; measured hit-rate on this symbol's own history is shown where n>=15 — descriptive, not advice"

// candlePatternBars is how many of the most recent daily bars are scanned for
// firing patterns.
const candlePatternBars = 200

// measuredEdge is the per-pattern measured-outcome block (null when the
// pattern-stats worker had no gated sample for this symbol+pattern).
type measuredEdge struct {
	HitRate float64 `json:"hitRate"`
	MeanFwd float64 `json:"meanFwd"`
	N       int     `json:"n"`
	Horizon int     `json:"horizon"`
}

// patternOut is one firing pattern with its (optional) measured edge.
type patternOut struct {
	Name     string        `json:"name"`
	Bias     int           `json:"bias"`
	Desc     string        `json:"desc"`
	Measured *measuredEdge `json:"measured"`
}

// barPatterns is one bar's firing patterns.
type barPatterns struct {
	Ts       int64        `json:"ts"`
	Patterns []patternOut `json:"patterns"`
}

// candlePatterns serves the recent firing candlestick patterns for a symbol.
// GET /api/candle-patterns?symbol=&market=&tf=1d
func (d Deps) candlePatterns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	tf := r.URL.Query().Get("tf")
	if tf == "" {
		tf = "1d"
	}
	if tf != "1d" {
		httpErr(w, 400, "only tf=1d is supported")
		return
	}

	// Load the scan window plus a little context so the earliest scanned bar
	// still has prior bars for pattern trend-context.
	bars, err := d.St.LastBars(ctx, s.ID, md.TF1d, candlePatternBars+60)
	if err != nil {
		httpInternal(w, err)
		return
	}

	// Measured edge per pattern, preferring the default horizon when several
	// were stored (currently only one horizon is written per pattern).
	byPattern := map[string]measuredEdge{}
	if stats, err := d.St.PatternStatsForSymbol(ctx, s.ID); err == nil {
		for _, p := range stats {
			if _, seen := byPattern[p.Pattern]; !seen || p.Horizon == candles.DefaultHorizon {
				byPattern[p.Pattern] = measuredEdge{HitRate: p.HitRate, MeanFwd: p.MeanFwd, N: p.N, Horizon: p.Horizon}
			}
		}
	}

	start := len(bars) - candlePatternBars
	if start < 0 {
		start = 0
	}
	outBars := make([]barPatterns, 0)
	for i := start; i < len(bars); i++ {
		pats := candles.Detect(bars[:i+1])
		if len(pats) == 0 {
			continue // omit no-pattern bars
		}
		pp := make([]patternOut, 0, len(pats))
		for _, p := range pats {
			po := patternOut{Name: p.Name, Bias: p.Bias, Desc: p.Desc}
			if m, ok := byPattern[p.Name]; ok {
				me := m
				po.Measured = &me
			}
			pp = append(pp, po)
		}
		outBars = append(outBars, barPatterns{Ts: bars[i].Ts, Patterns: pp})
	}

	writeJSON(w, map[string]any{
		"symbol": s.Symbol,
		"market": s.Market,
		"tf":     tf,
		"bars":   outBars,
		"note":   candlePatternsNote,
	})
}

func (d Deps) registerCandlePatterns(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/candle-patterns", d.candlePatterns)
}
