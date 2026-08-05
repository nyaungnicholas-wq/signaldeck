package structregime

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// probeConvictions sits one probe comfortably inside each served tier, so these
// tests read the tier a claim names rather than a boundary.
var probeConvictions = []float64{0.25, 0.65, 0.85, 0.95}

// Every crypto accuracy this package serves must disclose the sample it was
// measured on. The crypto tables are 98.5% on 68 rows across 4 quarter clusters
// and 96.4% on 28 rows across 6, against ~900 stocks and 7.5 years for the
// equity tables. A reader given the accuracy and not the n cannot tell those
// apart, which is the whole reason EvidenceRows exists. If a tier is ever added
// to cryptoAccuracyFor without a matching entry in EvidenceSizeFor, this fails.
func TestEveryServedCryptoTierDisclosesItsSampleSize(t *testing.T) {
	for _, k := range []Kind{KindTrendCrypto21, KindLiquidityCrypto21} {
		for _, conv := range probeConvictions {
			acc := cryptoAccuracyFor(k, conv)
			if acc == 0.5 {
				t.Fatalf("%s conv=%.2f: no accuracy served, fixture is wrong", k, conv)
			}
			rows, clusters := EvidenceSizeFor(k, conv)
			if rows <= 0 || clusters <= 0 {
				t.Errorf("%s conv=%.2f advertises %.3f accuracy on rows=%d clusters=%d.\n"+
					"A served accuracy with no disclosed sample size is the exact gap this "+
					"field was added to close — add the measured n to EvidenceSizeFor.",
					k, conv, acc, rows, clusters)
			}
		}
	}
}

// Higher conviction means a narrower slice of an already small sample, so the
// disclosed row count must never RISE with conviction. A rising count would mean
// a tier is quoting a larger sample than the one it was actually measured on.
func TestEvidenceRowsNeverGrowWithConviction(t *testing.T) {
	for _, k := range []Kind{KindTrendCrypto21, KindLiquidityCrypto21} {
		prev := math.MaxInt32
		for _, conv := range probeConvictions {
			rows, _ := EvidenceSizeFor(k, conv)
			if rows > prev {
				t.Errorf("%s conv=%.2f discloses %d rows, more than the %d of the tier below it",
					k, conv, rows, prev)
			}
			prev = rows
		}
	}
}

// The 2026-07-17 equity loop did not record per-tier sample sizes, and trend63
// recorded no band shares at all. Those kinds must report NOTHING rather than a
// plausible-looking number: an invented n is worse than an absent one, because a
// reader cannot tell it was invented.
func TestEquityKindsDiscloseNothingRatherThanGuess(t *testing.T) {
	for _, k := range []Kind{KindTrend21, KindLiquidity21, KindVol21, KindTrend63} {
		for _, conv := range probeConvictions {
			if rows, clusters := EvidenceSizeFor(k, conv); rows != 0 || clusters != 0 {
				t.Errorf("%s conv=%.2f invented rows=%d clusters=%d; the equity loop "+
					"never recorded them", k, conv, rows, clusters)
			}
		}
	}
}

// The measured values, pinned. These are transcribed from the 2026-07-18 crypto
// loop recorded in crypto.go's package doc; if that doc and this table drift, one
// of them is lying to a reader.
func TestDisclosedSizesMatchTheMeasuredLoop(t *testing.T) {
	cases := []struct {
		kind           Kind
		conv           float64
		rows, clusters int
	}{
		{KindTrendCrypto21, 0.25, 106, 4},     // all-decisions 93.4%
		{KindTrendCrypto21, 0.65, 68, 4},      // >0.5 cumulative 98.5%
		{KindTrendCrypto21, 0.95, 68, 4},      // 100% bands are never quoted; serves >0.5
		{KindLiquidityCrypto21, 0.25, 161, 6}, // all-decisions 79.5%
		{KindLiquidityCrypto21, 0.65, 91, 6},  // >0.5 91.2%
		{KindLiquidityCrypto21, 0.85, 46, 6},  // >0.8 93.5%
		{KindLiquidityCrypto21, 0.95, 28, 6},  // >0.9 96.4%
	}
	for _, c := range cases {
		rows, clusters := EvidenceSizeFor(c.kind, c.conv)
		if rows != c.rows || clusters != c.clusters {
			t.Errorf("%s conv=%.2f: got rows=%d clusters=%d, measured loop says %d/%d",
				c.kind, c.conv, rows, clusters, c.rows, c.clusters)
		}
	}
}

// End to end: a real crypto Forecast carries the disclosure, and a real equity
// Forecast carries none.
func TestForecastsCarryTheDisclosure(t *testing.T) {
	closes := make([]float64, 300)
	vols := make([]float64, 300)
	for i := range closes {
		closes[i] = 100 + float64(i)*0.5 + 3*math.Sin(float64(i)/7)
		vols[i] = 1e6 + 1e4*float64(i%50)
	}

	cf, ok := PredictTrendCrypto(closes)
	if !ok {
		t.Fatal("crypto trend predictor refused the fixture")
	}
	wantRows, wantClusters := EvidenceSizeFor(KindTrendCrypto21, cf.Conviction)
	if cf.EvidenceRows != wantRows || cf.EvidenceClusters != wantClusters {
		t.Errorf("crypto trend forecast: rows=%d clusters=%d, want %d/%d",
			cf.EvidenceRows, cf.EvidenceClusters, wantRows, wantClusters)
	}
	if cf.EvidenceRows == 0 {
		t.Error("crypto trend forecast shipped an accuracy with no sample size")
	}

	lc, ok := PredictLiquidityCrypto(closes, vols)
	if !ok {
		t.Fatal("crypto liquidity predictor refused the fixture")
	}
	if lc.EvidenceRows == 0 || lc.EvidenceClusters == 0 {
		t.Error("crypto liquidity forecast shipped an accuracy with no sample size")
	}

	sf, ok := PredictTrend(closes)
	if !ok {
		t.Fatal("stock trend predictor refused the fixture")
	}
	if sf.EvidenceRows != 0 || sf.EvidenceClusters != 0 {
		t.Errorf("equity forecast disclosed rows=%d clusters=%d; the loop recorded neither",
			sf.EvidenceRows, sf.EvidenceClusters)
	}
}

// The disclosure is only worth anything if it survives serialisation: the web
// client reads these off the JSON, and an omitempty on a field that is silently
// never set looks identical to a field that was never added. Crypto rows must
// carry it; equity rows must omit it entirely rather than emit a zero, because
// "evidenceRows": 0 would render as a claim of no evidence rather than as an
// unrecorded measurement.
func TestDisclosureSurvivesJSON(t *testing.T) {
	closes := make([]float64, 300)
	for i := range closes {
		closes[i] = 100 + float64(i)*0.5 + 3*math.Sin(float64(i)/7)
	}

	cf, ok := PredictTrendCrypto(closes)
	if !ok {
		t.Fatal("crypto trend predictor refused the fixture")
	}
	b, err := json.Marshal(cf)
	if err != nil {
		t.Fatalf("marshal crypto forecast: %v", err)
	}
	for _, key := range []string{`"evidenceRows"`, `"evidenceClusters"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("crypto forecast JSON is missing %s; the client cannot show a "+
				"sample size it never receives.\n%s", key, b)
		}
	}

	sf, ok := PredictTrend(closes)
	if !ok {
		t.Fatal("stock trend predictor refused the fixture")
	}
	sb, err := json.Marshal(sf)
	if err != nil {
		t.Fatalf("marshal equity forecast: %v", err)
	}
	if strings.Contains(string(sb), `"evidenceRows"`) {
		t.Errorf("equity forecast JSON emitted evidenceRows; an unrecorded sample must be "+
			"ABSENT, not zero, or the UI renders it as a claim.\n%s", sb)
	}
}
