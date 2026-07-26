package histfeat

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/macrofeat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signals"
)

const day = 86400

// t0 is week-aligned (t0 % WeekSecs == 0) so bar k of a contiguous daily
// series falls in week bucket k/7 relative to t0, and the last bar of each
// week is k = 7w+6. 2810*WeekSecs = 2023-11-09T00:00:00Z (ai_rally era).
const t0 int64 = 2810 * WeekSecs

func mkBars(start int64, closes []float64) []md.Bar {
	bars := make([]md.Bar, len(closes))
	for k, c := range closes {
		bars[k] = md.Bar{
			Ts:     start + int64(k)*day,
			Open:   c,
			High:   c + 1,
			Low:    c - 1,
			Close:  c,
			Volume: 1000 + float64(k%17)*25,
		}
	}
	return bars
}

// waveCloses is a deterministic non-trivial price path (two sines + drift)
// so every indicator has real variation without any RNG.
func waveCloses(n int) []float64 {
	out := make([]float64, n)
	for k := range out {
		out[k] = 100 + 10*math.Sin(float64(k)/9) + 0.5*math.Sin(1.7*float64(k)) + 0.03*float64(k)
	}
	return out
}

func flatCloses(n int, v float64) []float64 {
	out := make([]float64, n)
	for k := range out {
		out[k] = v
	}
	return out
}

func rowAt(t *testing.T, rows []WeekRow, ts int64) WeekRow {
	t.Helper()
	for _, r := range rows {
		if r.Ts == ts {
			return r
		}
	}
	t.Fatalf("no row at ts %d", ts)
	return WeekRow{}
}

func hasRow(rows []WeekRow, ts int64) bool {
	for _, r := range rows {
		if r.Ts == ts {
			return true
		}
	}
	return false
}

// TestTruncationInvariance is the leakage sentinel's structural guarantee:
// rows computed on the full series equal rows computed on a truncated
// prefix, everywhere the forward window does not cross the cut.
func TestTruncationInvariance(t *testing.T) {
	bars := mkBars(t0, waveCloses(400))
	vix := make([]Point, 400)
	for k := range vix {
		vix[k] = Point{Ts: t0 + int64(k)*day, Value: 18 + 12*math.Sin(float64(k)/10)}
	}
	mkt := BuildMarketCtx(bars, vix)

	full := WeeklyRows(bars, mkt, 0)
	cut := WeeklyRows(bars[:301], mkt, 0)

	limit := bars[300].Ts - 2*WeekSecs
	trim := func(rows []WeekRow) []WeekRow {
		out := []WeekRow{}
		for _, r := range rows {
			if r.Ts <= limit {
				out = append(out, r)
			}
		}
		return out
	}
	a, b := trim(full), trim(cut)
	if len(a) < 20 {
		t.Fatalf("too few comparable rows: %d", len(a))
	}
	if len(a) != len(b) {
		t.Fatalf("row count differs: full %d vs truncated %d", len(a), len(b))
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Fatalf("row %d differs:\nfull      %+v\ntruncated %+v", i, a[i], b[i])
		}
	}
}

func TestLabelGeometry(t *testing.T) {
	// Bars 0..76 contiguous: anchor 69 (last bar of its week) resolves
	// against bar 76 sitting exactly at target = anchor + 7d.
	closes := flatCloses(77, 100)
	closes[76] = 105
	rows := WeeklyRows(mkBars(t0, closes), MarketCtx{}, 0)
	r := rowAt(t, rows, t0+69*day)
	if math.Abs(r.FwdReturn-0.05) > 1e-12 || !r.Up {
		t.Fatalf("exact-target: FwdReturn=%v Up=%v, want 0.05/true", r.FwdReturn, r.Up)
	}
	if r.Week != (t0+69*day)/WeekSecs {
		t.Fatalf("week bucket = %d", r.Week)
	}
	if r.Era != EraAIRally {
		t.Fatalf("era = %q, want %q", r.Era, EraAIRally)
	}

	// Zero forward return is NOT up (strictly > 0).
	rows = WeeklyRows(mkBars(t0, flatCloses(77, 100)), MarketCtx{}, 0)
	r = rowAt(t, rows, t0+69*day)
	if r.FwdReturn != 0 || r.Up {
		t.Fatalf("flat forward: FwdReturn=%v Up=%v, want 0/false", r.FwdReturn, r.Up)
	}

	// First bar AT OR AFTER target: series stops at bar 69, resumes 2 days
	// past the target.
	bars := mkBars(t0, flatCloses(70, 100))
	bars = append(bars, md.Bar{Ts: t0 + 78*day, Open: 90, High: 91, Low: 89, Close: 90, Volume: 1000})
	rows = WeeklyRows(bars, MarketCtx{}, 0)
	r = rowAt(t, rows, t0+69*day)
	if math.Abs(r.FwdReturn-(-0.1)) > 1e-12 || r.Up {
		t.Fatalf("gap forward: FwdReturn=%v Up=%v, want -0.1/false", r.FwdReturn, r.Up)
	}

	// Forward bar exactly 21d past target still labels; one more day voids.
	edge := mkBars(t0, flatCloses(70, 100))
	edge = append(edge, md.Bar{Ts: t0 + 97*day, Open: 110, High: 111, Low: 109, Close: 110, Volume: 1000})
	rows = WeeklyRows(edge, MarketCtx{}, 0)
	r = rowAt(t, rows, t0+69*day)
	if math.Abs(r.FwdReturn-0.1) > 1e-12 || !r.Up {
		t.Fatalf("21d edge: FwdReturn=%v Up=%v, want 0.1/true", r.FwdReturn, r.Up)
	}
	void := mkBars(t0, flatCloses(70, 100))
	void = append(void, md.Bar{Ts: t0 + 98*day, Open: 110, High: 111, Low: 109, Close: 110, Volume: 1000})
	if hasRow(WeeklyRows(void, MarketCtx{}, 0), t0+69*day) {
		t.Fatal("forward bar 22d past target must void the anchor")
	}

	// Non-positive anchor close voids (resolver parity).
	zc := flatCloses(77, 100)
	zc[69] = 0
	if hasRow(WeeklyRows(mkBars(t0, zc), MarketCtx{}, 0), t0+69*day) {
		t.Fatal("zero anchor close must void")
	}

	// The first anchor needs >= 60 trailing bars; rowsFrom skips earlier
	// anchors.
	rows = WeeklyRows(mkBars(t0, flatCloses(77, 100)), MarketCtx{}, 0)
	if len(rows) != 2 || rows[0].Ts != t0+62*day {
		t.Fatalf("anchors = %d rows, first %d; want 2 rows starting at bar 62", len(rows), rows[0].Ts)
	}
	rows = WeeklyRows(mkBars(t0, flatCloses(77, 100)), MarketCtx{}, t0+69*day)
	if len(rows) != 1 || rows[0].Ts != t0+69*day {
		t.Fatalf("rowsFrom: got %d rows", len(rows))
	}
}

// TestPressureParity: the row's pressure block equals signals.ComputeScores
// called directly on the same trailing 500-bar slice.
func TestPressureParity(t *testing.T) {
	bars := mkBars(t0, waveCloses(620))
	rows := WeeklyRows(bars, MarketCtx{}, 0)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	last := rows[len(rows)-1]
	idx := int((last.Ts - t0) / day)
	if bars[idx].Ts != last.Ts {
		t.Fatalf("anchor index mix-up: bar ts %d vs row ts %d", bars[idx].Ts, last.Ts)
	}
	if idx < 500 {
		t.Fatalf("last anchor %d does not exercise the 500-bar cap", idx)
	}
	win := bars[idx+1-500 : idx+1]
	want, ok := signals.ComputeScores(win, nil, signals.MicroInputs{})[md.H1w]
	if !ok {
		t.Fatal("reference 1w score missing")
	}
	if got := last.Vec["pressure_score"]; got != want.Score {
		t.Fatalf("pressure_score %v != %v", got, want.Score)
	}
	if got := last.Vec["pressure_abs"]; got != math.Abs(want.Score) {
		t.Fatalf("pressure_abs %v != %v", got, math.Abs(want.Score))
	}
	nComp := 0
	for k := range last.Vec {
		if strings.HasPrefix(k, "comp_") {
			nComp++
		}
	}
	if nComp != len(want.Components) {
		t.Fatalf("comp_ key count %d != %d components", nComp, len(want.Components))
	}
	// A zero-WEIGHT component is stored as its VALUE under a distinct key, not
	// as a contribution that is algebraically zero forever (A8, 2026-07-26
	// re-audit: measured min = max = 0 over 248,390 live rows, which left the
	// derived __has bit — "enough history exists" — as the column's only
	// varying signal). This assertion is STRONGER than the one it replaces: it
	// used to accept the constant, and now it requires the informative reading
	// to be there under its own name.
	for _, c := range want.Components {
		if c.Weight == 0 {
			got, ok := last.Vec["comp_"+c.Name+"_value"]
			if !ok || got != c.Value {
				t.Fatalf("comp_%s_value = %v (present %v), want the component's Value %v",
					c.Name, got, ok, c.Value)
			}
			if _, ok := last.Vec["comp_"+c.Name]; ok {
				t.Fatalf("comp_%s is still stored; a zero-weight component's contribution is a constant", c.Name)
			}
			continue
		}
		got, ok := last.Vec["comp_"+c.Name]
		if !ok || got != c.Contrib {
			t.Fatalf("comp_%s = %v (present %v), want %v", c.Name, got, ok, c.Contrib)
		}
	}
}

func TestEraBoundaries(t *testing.T) {
	cases := []struct {
		ts   int64
		want string
	}{
		{0, EraPreCovid},
		{1581724799, EraPreCovid},   // 2020-02-14T23:59:59Z
		{1581724800, EraCovidCrash}, // 2020-02-15T00:00:00Z
		{1593561599, EraCovidCrash}, // 2020-06-30T23:59:59Z
		{1593561600, EraBull2021},   // 2020-07-01T00:00:00Z
		{1640995199, EraBull2021},   // 2021-12-31T23:59:59Z
		{1640995200, EraBear2022},   // 2022-01-01T00:00:00Z
		{1672531199, EraBear2022},   // 2022-12-31T23:59:59Z
		{1672531200, EraAIRally},    // 2023-01-01T00:00:00Z
		{1767225599, EraAIRally},    // 2025-12-31T23:59:59Z
		{1767225600, EraY2026},      // 2026-01-01T00:00:00Z
	}
	for _, c := range cases {
		if got := EraOf(c.ts); got != c.want {
			t.Errorf("EraOf(%d) = %q, want %q", c.ts, got, c.want)
		}
	}
	want := []string{EraCovidCrash, EraBull2021, EraBear2022, EraAIRally, EraY2026}
	if got := EraOrder(); !reflect.DeepEqual(got, want) {
		t.Fatalf("EraOrder() = %v, want %v", got, want)
	}
}

func TestConsecDirAndExtScore(t *testing.T) {
	// 5 strictly-up closes into the anchor after a flat change -> +0.5.
	closes := flatCloses(77, 100)
	for k := 65; k <= 69; k++ {
		closes[k] = 100 + float64(k-64)
	}
	for k := 70; k <= 76; k++ {
		closes[k] = 105
	}
	rows := WeeklyRows(mkBars(t0, closes), MarketCtx{}, 0)
	if got := rowAt(t, rows, t0+69*day).Vec["consec_dir"]; got != 0.5 {
		t.Fatalf("consec_dir = %v, want 0.5", got)
	}

	// 15 straight down closes cap at -10 -> -1.
	closes = flatCloses(77, 300)
	for k := 55; k <= 76; k++ {
		closes[k] = 300 - float64(k-54)
	}
	rows = WeeklyRows(mkBars(t0, closes), MarketCtx{}, 0)
	if got := rowAt(t, rows, t0+69*day).Vec["consec_dir"]; got != -1 {
		t.Fatalf("consec_dir = %v, want -1 (capped)", got)
	}

	// A flat last change zeroes the streak (key present, honest 0).
	rows = WeeklyRows(mkBars(t0, flatCloses(77, 100)), MarketCtx{}, 0)
	if v, ok := rowAt(t, rows, t0+69*day).Vec["consec_dir"]; !ok || v != 0 {
		t.Fatalf("consec_dir = %v (present %v), want 0", v, ok)
	}

	// ext_score is the mean of exactly the AVAILABLE members: the earliest
	// anchor predates the 60-value percentile histories (3 members), deep
	// history has vol_pct too (4 members).
	rows = WeeklyRows(mkBars(t0, waveCloses(400)), MarketCtx{}, 0)
	early := rows[0]
	if _, ok := early.Vec["rsi_pct"]; ok {
		t.Fatal("rsi_pct must be absent before 60 RSI values exist")
	}
	if _, ok := early.Vec["vol_pct"]; ok {
		t.Fatal("vol_pct must be absent before 60 realized-vol values exist")
	}
	late := rows[len(rows)-1]
	if _, ok := late.Vec["vol_pct"]; !ok {
		t.Fatal("vol_pct missing on deep history")
	}
	for _, r := range []WeekRow{early, late} {
		sum, n := 0.0, 0
		if v, ok := r.Vec["rsi14"]; ok {
			sum += math.Abs(v-50) / 50
			n++
		}
		if v, ok := r.Vec["vwap_dist_atr"]; ok {
			sum += clamp01(math.Abs(v) / 3)
			n++
		}
		if v, ok := r.Vec["atr_ext_20"]; ok {
			sum += clamp01(math.Abs(v) / 3)
			n++
		}
		if v, ok := r.Vec["vol_pct"]; ok {
			if v < 0 || v > 1 {
				t.Fatalf("vol_pct %v out of [0,1]", v)
			}
			sum += v
			n++
		}
		got, ok := r.Vec["ext_score"]
		if !ok || n == 0 || math.Abs(got-sum/float64(n)) > 1e-12 {
			t.Fatalf("ext_score = %v (present %v), want %v over %d members", got, ok, sum/float64(n), n)
		}
	}
	if v := late.Vec["rsi_pct"]; v < 0 || v > 1 {
		t.Fatalf("rsi_pct %v out of [0,1]", v)
	}
}

func TestMarketCtxJoin(t *testing.T) {
	spy := mkBars(t0, waveCloses(400))
	vix := make([]Point, 400)
	for k := range vix {
		vix[k] = Point{Ts: t0 + int64(k)*day, Value: 30} // elevated regime
	}
	mkt := BuildMarketCtx(spy, vix)
	rows := WeeklyRows(mkBars(t0, waveCloses(400)), mkt, 0)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	wantVIX := macrofeat.FromVIX(30)
	for _, r := range rows {
		if !r.HighVol {
			t.Fatalf("row %d: VIX 30 must flag HighVol", r.Ts)
		}
		if r.Vec["vix_high_vol"] != 1 || r.Vec["vix_level"] != wantVIX.Level || r.Vec["vix_regime"] != wantVIX.Regime {
			t.Fatalf("vix keys wrong at %d", r.Ts)
		}
	}
	// Market keys stay honest during SPY's own warmup.
	if _, ok := rows[0].Vec["mkt_trend"]; ok {
		t.Fatal("mkt_trend requires 200 trailing SPY bars")
	}
	if _, ok := rows[0].Vec["mkt_ret_13w"]; ok {
		t.Fatal("mkt_ret_13w requires a 13-week-old SPY anchor")
	}
	last := rows[len(rows)-1]
	if v, ok := last.Vec["mkt_trend"]; !ok || (v != 1 && v != -1 && v != 0) {
		t.Fatalf("mkt_trend = %v (present %v)", v, ok)
	}
	if _, ok := last.Vec["mkt_ret_13w"]; !ok {
		t.Fatal("mkt_ret_13w missing on deep history")
	}

	// No VIX series at all: vix keys omitted, HighVol false — never a fake 0.
	dry := BuildMarketCtx(spy, nil)
	for _, r := range WeeklyRows(mkBars(t0, waveCloses(400)), dry, 0) {
		if r.HighVol {
			t.Fatal("HighVol without VIX data")
		}
		if _, ok := r.Vec["vix_level"]; ok {
			t.Fatal("vix keys must be omitted without VIX data")
		}
	}
}
