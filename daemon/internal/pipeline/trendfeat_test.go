package pipeline

import (
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/trend"
)

// A clean ascending series must yield trend_class=+1 (uptrend), a positive
// slope, and a support-distance feature; too-short history yields nothing.
func TestTrendFeatures(t *testing.T) {
	// Thin history → no analysis, empty map (absence, not fabricated zeros).
	if got := trendFeatures([]md.Bar{{Close: 10}}); len(got) != 0 {
		t.Fatalf("thin history should yield no trend features, got %v", got)
	}

	// 80 ascending daily bars with a steady uptrend + pullbacks (so swing
	// pivots exist for a support line).
	bars := make([]md.Bar, 0, 80)
	base := 100.0
	for i := 0; i < 80; i++ {
		c := base + float64(i) // steady climb
		if i%5 == 0 {          // periodic dip → swing lows
			c -= 3
		}
		h, l := c+1, c-1
		bars = append(bars, md.Bar{Ts: int64(i * 86400), Open: c, High: h, Low: l, Close: c, Volume: 1000})
	}
	f := trendFeatures(bars)
	if f["trend_class"] != 1 {
		t.Errorf("clean uptrend should classify +1, got %v (present=%v)", f["trend_class"], f)
	}
	if f["trend_slope"] <= 0 {
		t.Errorf("uptrend slope should be positive, got %v", f["trend_slope"])
	}
	// Distance-to-line features must be present EXACTLY when trend.Analyze
	// fitted that kind of line — the feature builder never invents a distance.
	res, _ := trend.Analyze(bars)
	var hasSup, hasRes bool
	for _, l := range res.Trendlines {
		hasSup = hasSup || l.Kind == "support"
		hasRes = hasRes || l.Kind == "resistance"
	}
	if _, ok := f["trend_dist_support"]; ok != hasSup {
		t.Errorf("trend_dist_support present=%v but a support line fitted=%v", ok, hasSup)
	}
	if _, ok := f["trend_dist_resistance"]; ok != hasRes {
		t.Errorf("trend_dist_resistance present=%v but a resistance line fitted=%v", ok, hasRes)
	}

	// The keys must NOT be swept up by the model self-reference exclusions
	// (they are features, not model outputs).
	for k := range f {
		if excludedGBMKey(k) {
			t.Errorf("trend feature %q must not be on the GBM exclusion list", k)
		}
	}
}
