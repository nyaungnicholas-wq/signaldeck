package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedClusteredRecord writes a PERFECTLY day-clustered live record: symsPerDay
// symbols resolving on each of days distinct UTC days, where every symbol on a
// given day shares that day's outcome. That is what the real universe looks like
// — ~1,000 names sharing one market move — with the clustering turned up to its
// limit so the arithmetic is unambiguous.
func seedClusteredRecord(t *testing.T, st *store.Store, days, symsPerDay int) {
	t.Helper()
	ctx := context.Background()
	// AFTER the 2026-07-24 survivorship epoch — ResolvedPredictionOutcomes
	// floors on it, so the old 2026-05-04 anchor graded to an empty set.
	base := time.Date(2026, 8, 8, 14, 0, 0, 0, time.UTC).Unix() // after store.GradingEpoch (2026-08-07)
	for s := 0; s < symsPerDay; s++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("CL%03d", s), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		for d := 0; d < days; d++ {
			ts := base + int64(d)*86400
			// One market move per day, shared by the whole cross-section. The
			// day alone decides whether the day's calls were right, so 40
			// symbols contain one bet's worth of information.
			fwd := 0.01 * (1 + 0.1*float64(d))
			if d%2 == 1 {
				fwd = -fwd
			}
			// Conviction varies across the cross-section so the IC is defined,
			// and leans higher on days that went up so it is non-zero. Every
			// symbol still calls UP, so the DIRECTIONAL call is unanimous —
			// which is what the live record looks like: 98.6% of the universe
			// on one side. All the covariance lives at the DAY level, which is
			// exactly why a row-level IC interval overstates its evidence.
			prob := 0.52 + 0.001*float64(s)
			if fwd > 0 {
				prob += 0.03
			}
			if err := st.UpsertPrediction(ctx, store.Prediction{
				SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
				RawProb: prob, CalProb: prob, NUsed: 2, Components: "{}",
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, fwd); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestTrackRecord_ClusterCorrectedInterval is the regression for C4: the
// endpoint published a Wilson interval computed at the RAW symbol-day count, as
// if 40 symbols sharing one market move were 40 independent tests. On the live
// record that interval was 3.8x too narrow.
//
// The fixture below is the same defect with the clustering maximal: 15 days x 40
// symbols = 600 observations that between them contain 15 bets. The endpoint
// must publish the interval for 15 bets, report the MEASURED design effect and
// the effective N beside the raw one, and never let the raw count set the width.
func TestTrackRecord_ClusterCorrectedInterval(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cluster.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	const days, symsPerDay = 15, 40
	seedClusteredRecord(t, st, days, symsPerDay)

	d := Deps{St: st}
	rr := httptest.NewRecorder()
	d.trackRecord(rr, httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}

	if g, _ := resp["gated"].(bool); g {
		t.Fatalf("600 obs over 15 days must clear both existing gates, got gated (note=%v)", resp["note"])
	}
	if got := jnum(resp, "independentN"); got != days*symsPerDay {
		t.Fatalf("independentN = %.0f, want %d", got, days*symsPerDay)
	}
	if got := jnum(resp, "distinctDays"); got != days {
		t.Fatalf("distinctDays = %.0f, want %d", got, days)
	}

	cl, ok := resp["cluster"].(map[string]any)
	if !ok {
		t.Fatal("payload must carry a cluster-robust block: every published interval " +
			"has to state the design effect it was corrected by")
	}
	// A perfectly clustered day of m symbols carries one observation's worth of
	// information, so the measured design effect must be near m (not 1).
	if deff := jnum(cl, "designEffect"); deff < 0.5*symsPerDay {
		t.Fatalf("designEffect = %.2f, want ~%d — the cross-section on one day is ONE "+
			"market move, and an estimator that misses that publishes the same too-narrow "+
			"interval as before", deff, symsPerDay)
	}
	// Effective N must collapse to roughly the day count.
	effN := jnum(cl, "effectiveN")
	if effN > 2*days {
		t.Fatalf("effectiveN = %.1f, want ~%d — the sample holds %d bets, not %d rows",
			effN, days, days, days*symsPerDay)
	}
	if raw := jnum(cl, "n"); raw != days*symsPerDay {
		t.Fatalf("the cluster block must report raw N (%0.f) beside effective N", raw)
	}
	if dd := jnum(cl, "distinctDays"); dd != days {
		t.Fatalf("the cluster block must report distinct days, got %v", cl["distinctDays"])
	}

	// The HEADLINE winRateCI must be the corrected one. Compare against the
	// Wilson interval the old code published at the raw count.
	ci, okCI := ciPair(resp["winRateCI"])
	if !okCI {
		t.Fatal("winRateCI must be present and a 2-element interval")
	}
	winRate := jnum(resp, "winRate")
	naiveLo, naiveHi := wilson(int(math.Round(winRate*float64(days*symsPerDay))), days*symsPerDay)
	naiveWidth := naiveHi - naiveLo
	width := ci[1] - ci[0]
	if width < 3*naiveWidth {
		t.Fatalf("published winRateCI width %.4f is not meaningfully wider than the "+
			"discredited naive %.4f — the raw row count is still setting the width",
			width, naiveWidth)
	}
	if ci[0] > winRate || ci[1] < winRate {
		t.Fatalf("winRateCI [%.4f,%.4f] must bracket winRate %.4f", ci[0], ci[1], winRate)
	}
	// The naive interval may be shown for contrast, but only under a name that
	// makes reuse a deliberate act. Disclosure must not substitute for correction.
	if _, present := cl["naiveCIDiscredited"]; !present {
		t.Fatal("the cluster block should carry the discredited naive interval for contrast")
	}
	// A day-clustered bootstrap is the independent check on the analytic width.
	if _, present := cl["bootstrapCI"]; !present {
		t.Fatal("the cluster block must carry a day-resampled bootstrap interval")
	}
	// The IC interval is the same defect on a different statistic and must also
	// be day-clustered, not Fisher-z at the raw count.
	if icCI, okIC := ciPair(resp["icCI"]); okIC {
		fLo, fHi := fisherCI(jnum(resp, "ic"), days*symsPerDay)
		if (icCI[1] - icCI[0]) <= (fHi - fLo) {
			t.Fatalf("icCI width %.4f must exceed the raw-N Fisher width %.4f",
				icCI[1]-icCI[0], fHi-fLo)
		}
	}
}

// TestTrackRecord_ClusterRefusesRatherThanNarrows: when the record clears the
// existing count gates but cannot support a cluster-robust interval, the
// endpoint must publish NO interval rather than the narrow one. A withheld
// number beats a wrong number, and the wrong number here is the one that made
// "no demonstrated edge" look ten times better established than it is.
func TestTrackRecord_ClusterRefusesRatherThanNarrows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "refuse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	// Exactly at the existing distinct-day gate but with a deliberately tiny
	// day count relative to what an interval needs: the cluster module's own
	// floor is what decides, and its refusal must reach the payload.
	seedClusteredRecord(t, st, trackMinDistinctDays, 40)

	d := Deps{St: st}
	rr := httptest.NewRecorder()
	d.trackRecord(rr, httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	cl, ok := resp["cluster"].(map[string]any)
	if !ok {
		t.Fatal("cluster block must be present whenever the record is ungated")
	}
	// At exactly the floor the module grades; the point of this test is that
	// whenever it REFUSES, the endpoint publishes nil rather than falling back.
	if ref, _ := cl["refused"].(bool); ref {
		if resp["winRateCI"] != nil {
			t.Fatalf("a cluster-stat refusal must withhold winRateCI, got %v", resp["winRateCI"])
		}
		if r, _ := cl["reason"].(string); r == "" {
			t.Fatal("a refusal must state its reason")
		}
	}
	// Either way the raw count must never be the published sample size alone.
	if _, present := cl["effectiveN"]; !present {
		t.Fatal("effective N must always accompany raw N")
	}
}

// ciPair pulls a 2-element interval out of a decoded JSON payload value.
func ciPair(v any) ([2]float64, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		return [2]float64{}, false
	}
	lo, ok1 := arr[0].(float64)
	hi, ok2 := arr[1].(float64)
	if !ok1 || !ok2 {
		return [2]float64{}, false
	}
	return [2]float64{lo, hi}, true
}
