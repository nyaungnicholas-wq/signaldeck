// TREND-STRUCTURE wave — featureVersion 8 features.
//
// This file turns the geometric trend read (internal/trend — the same analysis
// the chart draws) into PREDICTION FEATURES so the per-symbol GBM and the
// pooled alphax leg can LEARN whether trend structure carries edge. The
// OOS-lift gate is the referee: these join the vector as ordinary learnable
// features and only ever influence a live prediction if they measurably help.
// None is a model output, so none belongs on the self-reference exclusion lists
// (excludedGBMKey / alphax excludedKey) — those match by base name and match
// none of these keys.
//
// Same honesty contract as every other feature: each field is ABSENT when it
// cannot be computed (too little history, or no fitted line of that kind).
// Absence is information, not a fabricated zero — e.g. trend_channel is present
// (=1) ONLY inside a channel; its absence means "not a channel", never "0".
package pipeline

import (
	"github.com/nyaungnicholas-wq/signaldeck/internal/trend"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// trendFeatures returns the featureVersion-8 trend-structure features from a
// symbol's daily bars. The map is never nil; a field is present only when
// computable.
func trendFeatures(daily []md.Bar) map[string]float64 {
	out := map[string]float64{}
	res, ok := trend.Analyze(daily)
	if !ok || len(daily) == 0 {
		return out
	}

	// trend_class: the geometric classification as a directional signal
	// (uptrend +1 / range 0 / downtrend -1). Always present once Analyze runs.
	switch res.Class {
	case trend.Uptrend:
		out["trend_class"] = 1
	case trend.Downtrend:
		out["trend_class"] = -1
	case trend.Range:
		out["trend_class"] = 0
	}

	// trend_slope: least-squares slope of closes as a fraction of mean price
	// per bar — the magnitude/direction of the drift the classification labels.
	out["trend_slope"] = res.SlopePctPerBar

	// Distance from the latest close to each fitted line, projected to the
	// latest bar's timestamp, as a fraction of price. Positive headroom below
	// resistance / cushion above support; proximity to a line is where price
	// tends to react. Present only when that line was fitted.
	last := daily[len(daily)-1]
	close := last.Close
	if close > 0 {
		for _, l := range res.Trendlines {
			at, ok := lineValueAt(l, last.Ts)
			if !ok {
				continue
			}
			switch l.Kind {
			case "resistance":
				out["trend_dist_resistance"] = (at - close) / close
			case "support":
				out["trend_dist_support"] = (close - at) / close
			}
		}
	}

	// trend_channel: present (=1) only inside a channel — presence carries the
	// signal; absence means "not a channel".
	if res.Channel {
		out["trend_channel"] = 1
	}
	return out
}

// lineValueAt linearly projects a fitted trendline to timestamp ts. ok=false
// for a degenerate (zero-span) line.
func lineValueAt(l trend.Line, ts int64) (float64, bool) {
	span := l.ToTs - l.FromTs
	if span == 0 {
		return 0, false
	}
	frac := float64(ts-l.FromTs) / float64(span)
	return l.FromPrice + (l.ToPrice-l.FromPrice)*frac, true
}
