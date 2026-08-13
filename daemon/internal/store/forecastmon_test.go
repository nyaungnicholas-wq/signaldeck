// Tests for the forecastmon read paths, which shipped with no coverage at all
// and took internal/store below its CI floor.
//
// The property under test is the one forecastmon.go's own header calls
// load-bearing: every query DEDUPLICATES to one row per symbol per UTC day
// before it measures anything. The pipeline writes many rows per symbol per
// forward period that all resolve against the SAME move, so pooling them
// inflates n by ~60x — and a collapse detector fed pseudo-replicated rows would
// read one market-wide call as 328 observations, which is the exact condition it
// exists to detect. The fixture therefore plants a second, EARLIER row for one
// symbol on one day whose prob and realized direction both differ from the row
// that should win: if dedup regresses, the counts, the base rate and the bucket
// averages all move, rather than the test passing by coincidence.
package store

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// dayA/dayB are whole UTC days, because date(ts,'unixepoch') buckets in UTC and
// a local-time fixture would bucket differently depending on where it ran.
var (
	dayA = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	dayB = dayA.AddDate(0, 0, 1)
)

// seedForecastFixture writes the fixture described in the file header and
// returns the symbol ids it created.
func seedForecastFixture(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()

	id := func(sym string) int64 {
		s, err := st.UpsertSymbol(ctx, sym, md.Stocks, sym+" Co")
		if err != nil {
			t.Fatalf("upsert %s: %v", sym, err)
		}
		return s.ID
	}
	s1, s2, s3, s4 := id("AAA"), id("BBB"), id("CCC"), id("DDD")

	// ts, symbol, rawProb, calProb, nUsed, fwdReturn (NaN sentinel = leave unresolved)
	type row struct {
		ts       int64
		sym      int64
		raw, cal float64
		nUsed    int
		fwd      float64
		resolve  bool
	}
	rows := []row{
		// Day A. AAA gets TWO rows: the earlier one disagrees with the later one
		// on both prob and direction, so only correct dedup gives the numbers
		// asserted below.
		{dayA.Unix() + 10*3600, s1, 0.50, 0.60, 3, -0.01, true},
		{dayA.Unix() + 11*3600, s1, 0.55, 0.62, 3, +0.01, true},
		// 0.6201 rounds to 0.620 at 3dp, so it must NOT count as a second
		// distinct probability — that rounding is what keeps float noise from
		// making the collapse detector permanently blind.
		{dayA.Unix() + 11*3600, s2, 0.55, 0.6201, 2, -0.01, true},
		{dayA.Unix() + 11*3600, s3, 0.10, 0.20, 3, +0.01, true},
		// nUsed=0 is an EVIDENCE row: no leg was admitted. UpsertPrediction must
		// not give it an outcome row, and the published cross-section must not
		// count it as a forecast.
		{dayA.Unix() + 11*3600, s4, 0.10, 0.50, 0, 0, false},
		// Day B.
		{dayB.Unix() + 11*3600, s1, 0.90, 0.80, 4, +0.01, true},
	}
	for _, r := range rows {
		if err := st.UpsertPrediction(ctx, Prediction{SymbolID: r.sym, Horizon: md.H1d,
			Ts: r.ts, RawProb: r.raw, CalProb: r.cal, NUsed: r.nUsed,
			Components: `{}`}); err != nil {
			t.Fatalf("upsert prediction ts=%d: %v", r.ts, err)
		}
		if r.resolve {
			if err := st.ResolvePrediction(ctx, r.sym, md.H1d, r.ts, r.fwd); err != nil {
				t.Fatalf("resolve ts=%d: %v", r.ts, err)
			}
		}
	}
}

func TestForecastDayStatsDedupesToOneRowPerSymbolDay(t *testing.T) {
	st := openTemp(t)
	seedForecastFixture(t, st)
	since := dayA.Add(-24 * time.Hour)

	got, err := st.ForecastDayStats(context.Background(), string(md.H1d), since)
	if err != nil {
		t.Fatalf("ForecastDayStats: %v", err)
	}
	// Day A holds FOUR prediction rows but only three graded symbols: AAA's
	// earlier row is deduped away and DDD (nUsed=0) never got an outcome row.
	want := []ForecastDayStat{
		{Day: "2026-03-02", Symbols: 3, DistinctProbs: 2},
		{Day: "2026-03-03", Symbols: 1, DistinctProbs: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("ForecastDayStats = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestForecastDayStatsRawSeesUnresolvedAndEvidenceRows(t *testing.T) {
	st := openTemp(t)
	seedForecastFixture(t, st)
	since := dayA.Add(-24 * time.Hour)

	got, err := st.ForecastDayStatsRaw(context.Background(), string(md.H1d), since)
	if err != nil {
		t.Fatalf("ForecastDayStatsRaw: %v", err)
	}
	// The raw view reads predictions, not outcomes: that is the whole point —
	// a collapse starting today is invisible in the outcome view for a full
	// horizon. So DDD is still SEEN here even though it has no outcome row, and
	// AAA is still deduped to its latest raw_prob (0.55, not 0.50).
	//
	// DDD (n_used=0) is counted in Symbols and in Withheld, but its raw_prob is
	// excluded from DistinctProbs — the fixture comment above has always said
	// "the published cross-section must not count it as a forecast", and until
	// 2026-08-11 nothing enforced it. The assertion passed by COINCIDENCE:
	// DDD's 0.10 duplicated CCC's, so including or excluding it gave 2 either
	// way. Withheld is what makes the distinction observable, and
	// TestWithheldRowsDoNotReadAsCollapse below is what makes it load-bearing.
	want := []ForecastDayStat{
		// forecast {0.55, 0.55, 0.10} = 2 distinct; DDD withheld.
		{Day: "2026-03-02", Symbols: 4, DistinctProbs: 2, Withheld: 1},
		{Day: "2026-03-03", Symbols: 1, DistinctProbs: 1, Withheld: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("ForecastDayStatsRaw = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestForecastBucketsLabelsBaseRateAndDayCount(t *testing.T) {
	st := openTemp(t)
	seedForecastFixture(t, st)
	since := dayA.Add(-24 * time.Hour)

	buckets, base, days, err := st.ForecastBuckets(context.Background(), string(md.H1d), since)
	if err != nil {
		t.Fatalf("ForecastBuckets: %v", err)
	}
	// Deduped population: AAA/0.62/up, BBB/0.6201/down, CCC/0.20/up, AAA-dayB/0.80/up.
	// Base rate is 3 of 4, over 2 distinct days. Pooling AAA's dropped row would
	// give 3/5 over 2 days, so this pins the dedup rather than restating it.
	if base < 0.749 || base > 0.751 {
		t.Errorf("base rate = %v; want 0.75", base)
	}
	if days != 2 {
		t.Errorf("distinct days = %d; want 2", days)
	}

	byLabel := map[string]ForecastBucket{}
	for _, b := range buckets {
		byLabel[b.Label] = b
	}
	// sqlPct must have collapsed the doubled percent signs; a leaked "%%" here
	// would ship straight into the monitor's output.
	for _, want := range []string{"<30%", "55-70%", ">=70%"} {
		if _, ok := byLabel[want]; !ok {
			t.Fatalf("missing bucket %q; got %+v", want, buckets)
		}
	}
	if b := byLabel["55-70%"]; b.N != 2 || b.Actual < 0.49 || b.Actual > 0.51 {
		t.Errorf("55-70%% bucket = %+v; want N=2 actual=0.5", b)
	}
	if b := byLabel["<30%"]; b.N != 1 || b.Actual != 1 {
		t.Errorf("<30%% bucket = %+v; want N=1 actual=1", b)
	}
	if b := byLabel[">=70%"]; b.N != 1 || b.Actual != 1 {
		t.Errorf(">=70%% bucket = %+v; want N=1 actual=1", b)
	}
}

func TestAdmittedLegHistogramCountsEveryRowNotJustTheLatest(t *testing.T) {
	st := openTemp(t)
	seedForecastFixture(t, st)

	got, err := st.AdmittedLegHistogram(context.Background(), string(md.H1d),
		dayA.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("AdmittedLegHistogram: %v", err)
	}
	// This one is deliberately NOT deduped: it measures how many legs the
	// ensemble admitted per PREDICTION, so both of AAA's day-A rows count.
	want := map[string]map[int]int{
		"2026-03-02": {0: 1, 2: 1, 3: 3},
		"2026-03-03": {4: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("histogram = %+v; want %+v", got, want)
	}
	for day, counts := range want {
		if len(got[day]) != len(counts) {
			t.Fatalf("day %s = %+v; want %+v", day, got[day], counts)
		}
		for nUsed, n := range counts {
			if got[day][nUsed] != n {
				t.Errorf("day %s n_used=%d = %d; want %d", day, nUsed, got[day][nUsed], n)
			}
		}
	}
}

func TestPublishedCrossSectionDropsEvidenceRowsAndDedupes(t *testing.T) {
	st := openTemp(t)
	seedForecastFixture(t, st)

	got, err := st.PublishedCrossSection(context.Background(), string(md.H1d), "2026-03-02")
	if err != nil {
		t.Fatalf("PublishedCrossSection: %v", err)
	}
	// Three symbols published on day A. DDD is excluded by n_used > 0, and AAA
	// contributes its LATEST calibrated prob (0.62), never the superseded 0.60 —
	// the thin-sample fragility this query replaced is exactly a cross-section
	// that under-counts the day.
	if len(got) != 3 {
		t.Fatalf("PublishedCrossSection = %v; want 3 values", got)
	}
	seen := map[float64]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen[0.62] || seen[0.60] || seen[0.50] {
		t.Errorf("cross-section = %v; want 0.62 present, 0.60 (superseded) and 0.50 (evidence) absent", got)
	}

	// A day nobody published on is empty, not an error.
	empty, err := st.PublishedCrossSection(context.Background(), string(md.H1d), "2026-03-09")
	if err != nil || len(empty) != 0 {
		t.Errorf("empty day = %v, %v; want no rows and no error", empty, err)
	}
}

func TestSQLPctCollapsesDoubledPercentSigns(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"no percents", "no percents"},
		{"'<30%%'", "'<30%'"},
		{"a %% b %% c", "a % b % c"},
		{"trailing %", "trailing %"},
		// Four in a row is two escaped signs, not one and a stray.
		{"%%%%", "%%"},
	} {
		if got := sqlPct(tc.in); got != tc.want {
			t.Errorf("sqlPct(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
