package expectancy

import (
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ── synthetic series builders ───────────────────────────────────────────

// mkBars zips parallel close/volume series into bars, spaced 60s apart. The
// daily walk ignores timestamps, but the 1h walk now skips 60-bar-ahead
// samples that span a session/data gap (>3h), so minute fixtures must carry
// realistic contiguous 1-minute spacing.
func mkBars(closes, vols []float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, len(closes))
	for i := range closes {
		bars[i] = marketdata.Bar{
			Ts:     int64(i) * 60,
			Open:   closes[i],
			High:   closes[i],
			Low:    closes[i],
			Close:  closes[i],
			Volume: vols[i],
		}
	}
	return bars
}

// mkDailyBars is mkBars for DAILY fixtures: one bar per UTC day. mkBars spaces
// every bar 60s apart, which is right for minute fixtures and wrong for daily
// ones — 1440 "daily" bars would share a single UTC day and be refused by the
// minDistinctDays floor. Real daily bars are one per day, so these are too.
func mkDailyBars(closes, vols []float64) []marketdata.Bar {
	bars := mkBars(closes, vols)
	for i := range bars {
		bars[i].Ts = int64(i) * 86400
	}
	return bars
}

// mkMinuteDays lays minute bars out as `days` contiguous sessions of `perDay`
// bars, 60s apart within a session and starting on successive UTC days. This is
// what real minute data looks like, and it is the only way a minute fixture can
// clear minDistinctDays. Build's own >3h gap guard drops the samples whose
// 60-bar forward window would straddle a session boundary, exactly as it does
// in production.
func mkMinuteDays(days, perDay int, start, rate, vol float64) []marketdata.Bar {
	n := days * perDay
	bars := mkBars(geom(n, start, rate), constVol(n, vol))
	for d := 0; d < days; d++ {
		dayStart := int64(d) * 86400
		for k := 0; k < perDay; k++ {
			bars[d*perDay+k].Ts = dayStart + int64(k)*60
		}
	}
	return bars
}

// geom returns n closes growing (or shrinking) by rate each bar.
func geom(n int, start, rate float64) []float64 {
	out := make([]float64, n)
	c := start
	for i := range out {
		out[i] = c
		c *= 1 + rate
	}
	return out
}

func constVol(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func findRow(t *testing.T, rows []marketdata.Expectancy, key string) marketdata.Expectancy {
	t.Helper()
	for _, r := range rows {
		if r.StateKey == key {
			return r
		}
	}
	t.Fatalf("no row with key %q; have %v", key, keysOf(rows))
	return marketdata.Expectancy{}
}

func keysOf(rows []marketdata.Expectancy) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.StateKey
	}
	return out
}

// ── bucketing on the Build walk ─────────────────────────────────────────

func TestBuildDailyBucketing(t *testing.T) {
	tests := []struct {
		name    string
		closes  []float64
		vols    []float64
		wantKey string
		wantN1d int
		wantN1w int
	}{
		{
			// Strictly rising: Wilder RSI pegs at 100 (high), close above
			// every SMA, ROC20 positive, flat volume.
			name:    "monotonic up",
			closes:  geom(120, 100, 0.01),
			vols:    constVol(120, 100),
			wantKey: "rsi:high|trend:above|mom:up|rvol:normal",
			wantN1d: 59, // i = 60..118 (i+1 must exist)
			wantN1w: 55, // i = 60..114 (i+5 must exist)
		},
		{
			name:    "monotonic down",
			closes:  geom(120, 100, -0.01),
			vols:    constVol(120, 100),
			wantKey: "rsi:low|trend:below|mom:down|rvol:normal",
			wantN1d: 59,
			wantN1w: 55,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(mkDailyBars(tt.closes, tt.vols), nil)

			r1d := findRow(t, got[marketdata.H1d], tt.wantKey)
			if r1d.N != tt.wantN1d {
				t.Errorf("1d N = %d, want %d", r1d.N, tt.wantN1d)
			}
			if r1d.Horizon != marketdata.H1d {
				t.Errorf("1d Horizon = %q", r1d.Horizon)
			}
			if r1d.SymbolID != 0 || r1d.UpdatedAt != 0 {
				t.Errorf("SymbolID/UpdatedAt must stay zero, got %d/%d", r1d.SymbolID, r1d.UpdatedAt)
			}
			r1w := findRow(t, got[marketdata.H1w], tt.wantKey)
			if r1w.N != tt.wantN1w {
				t.Errorf("1w N = %d, want %d", r1w.N, tt.wantN1w)
			}
			// A single regime must produce exactly one full (4-dim) key.
			for _, r := range got[marketdata.H1d] {
				if strings.Count(r.StateKey, "|") == 3 && r.StateKey != tt.wantKey {
					t.Errorf("unexpected extra full key %q", r.StateKey)
				}
			}
			if _, ok := got[marketdata.H1h]; ok {
				t.Errorf("no minute bars given but H1h present")
			}
		})
	}
}

func TestBuildMinuteHorizon(t *testing.T) {
	// Six sessions of 200 bars. A single 400-bar run would be ONE UTC day and
	// is now refused by minDistinctDays — which is the whole point of that floor.
	minute := mkMinuteDays(6, 200, 100, 0.01, 5)
	got := Build(nil, minute)

	rows, ok := got[marketdata.H1h]
	if !ok {
		t.Fatal("expected H1h rows")
	}
	// Minute states have no trend dimension.
	for _, r := range rows {
		if strings.Contains(r.StateKey, "trend:") {
			t.Errorf("minute key %q contains trend dimension", r.StateKey)
		}
	}
	r := findRow(t, rows, "rsi:high|mom:up|rvol:normal")
	// Recompute the walk's sample count by Build's own rules rather than
	// hardcoding it: step minuteStep from walkStart, require the 60-bar forward
	// window to exist and not straddle a session gap.
	wantN := 0
	for i := walkStart; i+60 < len(minute); i += minuteStep {
		if minute[i+60].Ts-minute[i].Ts > 3*3600 {
			continue
		}
		wantN++
	}
	if wantN < minSamples {
		t.Fatalf("fixture produces only %d samples, below the %d floor — fix the fixture, not the floor", wantN, minSamples)
	}
	if r.N != wantN {
		t.Errorf("N = %d, want %d", r.N, wantN)
	}
	// Every forward return is positive, so the horizon's pooled base rate is
	// 1.0 and shrinking toward it is the identity: HitRate stays exactly 1.
	if r.HitRate != 1 {
		t.Errorf("HitRate = %v, want 1", r.HitRate)
	}
	wantFwd := math.Pow(1.01, 60) - 1
	if math.Abs(r.MeanFwd-wantFwd) > 1e-9 {
		t.Errorf("MeanFwd = %v, want %v", r.MeanFwd, wantFwd)
	}
	if r.Stdev > 1e-9 {
		t.Errorf("Stdev = %v, want ~0 (constant-ratio series)", r.Stdev)
	}
	if len(got[marketdata.H1d]) != 0 || len(got[marketdata.H1w]) != 0 {
		t.Error("no daily bars given but daily horizons present")
	}
}

func TestBuildInsufficientData(t *testing.T) {
	tests := []struct {
		name         string
		daily, min   int
		wantHorizons int
	}{
		{"empty", 0, 0, 0},
		{"daily one short of minimum", minDailyBars - 1, 0, 0},
		{"minute one short of minimum", 0, minMinuteBars - 1, 0},
		{"daily at minimum", minDailyBars, 0, 2}, // 1d + 1w
		// A minute run AT the bar minimum but confined to one UTC day emits
		// nothing: the bar floor and the distinct-day floor are independent
		// gates, and rows from a single session are one observation.
		{"minute at bar minimum but single day", 0, minMinuteBars, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var daily, minute []marketdata.Bar
			if tt.daily > 0 {
				daily = mkDailyBars(geom(tt.daily, 100, 0.01), constVol(tt.daily, 1))
			}
			if tt.min > 0 {
				minute = mkBars(geom(tt.min, 100, 0.001), constVol(tt.min, 1))
			}
			got := Build(daily, minute)
			if got == nil {
				t.Fatal("Build returned nil map")
			}
			if len(got) != tt.wantHorizons {
				t.Errorf("got %d horizons (%v), want %d", len(got), got, tt.wantHorizons)
			}
		})
	}

	// The same bar count spread across enough sessions DOES emit — proving the
	// refusal above is the day floor, not the bar floor.
	t.Run("minute across enough days", func(t *testing.T) {
		got := Build(nil, mkMinuteDays(6, 200, 100, 0.001, 1))
		if len(got[marketdata.H1h]) == 0 {
			t.Error("6 sessions of 200 bars emitted no 1h rows")
		}
	})
}

// ── fallback rollup ─────────────────────────────────────────────────────

// Volume spikes split the walk into rvol:high and rvol:normal children that
// share every coarser prefix; the parents must count BOTH children's samples.
func TestBuildFallbackRollup(t *testing.T) {
	// n raised from 120 and the spikes made periodic so BOTH children clear
	// minSamples. The old fixture gave rvol:high only 5 samples, which the
	// evidence floor now (correctly) refuses to emit at all — a five-sample
	// cell was exactly the thing that floor exists to stop.
	const n = 400
	closes := geom(n, 100, 0.01)
	vols := constVol(n, 1)
	for i := 70; i < n; i += 4 {
		vols[i] = 100 // SMA20 holds ~5 spikes -> mean ~25.75, so 100 is ~3.9x
	}
	got := Build(mkDailyBars(closes, vols), nil)
	rows := got[marketdata.H1d]

	base := "rsi:high|trend:above|mom:up"
	high := findRow(t, rows, base+"|rvol:high")
	normal := findRow(t, rows, base+"|rvol:normal")
	if high.N < minSamples {
		t.Errorf("rvol:high N = %d, below the %d floor", high.N, minSamples)
	}
	if normal.N < minSamples {
		t.Errorf("rvol:normal N = %d, below the %d floor", normal.N, minSamples)
	}
	for _, parent := range []string{base, "rsi:high|trend:above", "rsi:high"} {
		p := findRow(t, rows, parent)
		if p.N != high.N+normal.N {
			t.Errorf("parent %q N = %d, want %d (sum of children)", parent, p.N, high.N+normal.N)
		}
	}
	// The parent's mean must be the sample-weighted mean of its children —
	// proof the identical samples flowed into both levels.
	p := findRow(t, rows, base)
	wantMean := (high.MeanFwd*float64(high.N) + normal.MeanFwd*float64(normal.N)) / float64(high.N+normal.N)
	if math.Abs(p.MeanFwd-wantMean) > 1e-12 {
		t.Errorf("parent MeanFwd = %v, want %v", p.MeanFwd, wantMean)
	}
	// Nothing thinner than minSamples may be emitted, on any horizon.
	for h, hr := range got {
		for _, r := range hr {
			if r.N < minSamples {
				t.Errorf("%s row %q emitted with N=%d < %d", h, r.StateKey, r.N, minSamples)
			}
		}
	}
}

// ── stats math on a hand-checkable walk ─────────────────────────────────

// Zigzag +3%/-1% keeps a single state (RSI ~75, rising trend) while forward
// returns alternate sign. The test recomputes every walk sample by hand and
// compares against the single emitted full-key row.
func TestBuildStatsMatchHandComputedWalk(t *testing.T) {
	const n = 120
	closes := make([]float64, n)
	closes[0] = 100
	for i := 1; i < n; i++ {
		if i%2 == 1 {
			closes[i] = closes[i-1] * 1.03
		} else {
			closes[i] = closes[i-1] * 0.99
		}
	}
	got := Build(mkDailyBars(closes, constVol(n, 7)), nil)
	rows := got[marketdata.H1d]

	var full []marketdata.Expectancy
	for _, r := range rows {
		if strings.Count(r.StateKey, "|") == 3 {
			full = append(full, r)
		}
	}
	if len(full) != 1 {
		t.Fatalf("want exactly one full-key row, got %v", keysOf(full))
	}
	r := full[0]

	// Independent recomputation of the walk's forward returns.
	var samples []float64
	for i := walkStart; i+1 < n; i++ {
		samples = append(samples, closes[i+1]/closes[i]-1)
	}
	if r.N != len(samples) {
		t.Fatalf("N = %d, want %d", r.N, len(samples))
	}
	var sum float64
	hits := 0
	for _, v := range samples {
		sum += v
		if v > 0 {
			hits++
		}
	}
	mean := sum / float64(len(samples))
	var sq float64
	for _, v := range samples {
		sq += (v - mean) * (v - mean)
	}
	stdev := math.Sqrt(sq / float64(len(samples)))
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2] // 59 samples → odd

	if math.Abs(r.MeanFwd-mean) > 1e-12 {
		t.Errorf("MeanFwd = %v, want %v", r.MeanFwd, mean)
	}
	if math.Abs(r.MedianFwd-median) > 1e-12 {
		t.Errorf("MedianFwd = %v, want %v", r.MedianFwd, median)
	}
	if want := float64(hits) / float64(len(samples)); math.Abs(r.HitRate-want) > 1e-12 {
		t.Errorf("HitRate = %v, want %v", r.HitRate, want)
	}
	if math.Abs(r.Stdev-stdev) > 1e-12 {
		t.Errorf("Stdev = %v, want %v", r.Stdev, stdev)
	}
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name                         string
		samples                      []float64
		mean, median, hitRate, stdev float64
	}{
		{
			name:    "odd count hand computed",
			samples: []float64{0.01, -0.02, 0.03, 0.04, -0.02},
			mean:    0.008,
			median:  0.01,
			hitRate: 0.6,
			stdev:   math.Sqrt(0.000616), // population variance
		},
		{
			name:    "even count median averages middle pair",
			samples: []float64{4, 1, 3, 2},
			mean:    2.5,
			median:  2.5,
			hitRate: 1,
			stdev:   math.Sqrt(1.25),
		},
		{
			name:    "zero is not a hit",
			samples: []float64{0, 0, 0, 0, 1},
			mean:    0.2,
			median:  0,
			hitRate: 0.2,
			stdev:   0.4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mean, median, hit, stdev := summarize(tt.samples)
			if math.Abs(mean-tt.mean) > 1e-12 {
				t.Errorf("mean = %v, want %v", mean, tt.mean)
			}
			if math.Abs(median-tt.median) > 1e-12 {
				t.Errorf("median = %v, want %v", median, tt.median)
			}
			if math.Abs(hit-tt.hitRate) > 1e-12 {
				t.Errorf("hitRate = %v, want %v", hit, tt.hitRate)
			}
			if math.Abs(stdev-tt.stdev) > 1e-12 {
				t.Errorf("stdev = %v, want %v", stdev, tt.stdev)
			}
		})
	}
}

// ── current state keys ──────────────────────────────────────────────────

func TestCurrentStateKeys(t *testing.T) {
	upDaily := mkDailyBars(geom(120, 100, 0.01), constVol(120, 1))
	upMinute := mkMinuteDays(6, 200, 100, 0.001, 1)

	spikeVols := constVol(120, 1)
	spikeVols[119] = 100 // RVOL = 100/5.95 ≈ 16.8 → high

	// 250 bars: flat at 200, then a crash to ~100 with a modest recovery.
	// The full series has >=200 bars → close sits far BELOW the SMA200;
	// the last-100 slice has <200 bars → SMA50 governs and the same close
	// is ABOVE it. Same data, honest regime-dependent trend read.
	regime := make([]float64, 250)
	for i := range regime {
		if i < 200 {
			regime[i] = 200
		} else {
			regime[i] = 100 + 0.2*float64(i-200)
		}
	}
	regimeBars := mkDailyBars(regime, constVol(250, 1))

	tests := []struct {
		name      string
		daily     []marketdata.Bar
		minute    []marketdata.Bar
		wantDaily string // exact key, or "contains:" prefix for substring
		wantH1h   string
	}{
		{
			name:      "up regimes on both timeframes",
			daily:     upDaily,
			minute:    upMinute,
			wantDaily: "rsi:high|trend:above|mom:up|rvol:normal",
			wantH1h:   "rsi:high|mom:up|rvol:normal",
		},
		{
			name:      "volume spike on the last bar reads rvol high",
			daily:     mkDailyBars(geom(120, 100, 0.01), spikeVols),
			wantDaily: "rsi:high|trend:above|mom:up|rvol:high",
		},
		{
			name:      "200 plus bars use SMA200 for trend",
			daily:     regimeBars,
			wantDaily: "contains:trend:below",
		},
		{
			name:      "under 200 bars fall back to SMA50",
			daily:     regimeBars[len(regimeBars)-100:],
			wantDaily: "contains:trend:above",
		},
		{
			name:   "insufficient bars yield empty keys",
			daily:  mkDailyBars(geom(60, 100, 0.01), constVol(60, 1)),
			minute: mkBars(geom(60, 100, 0.01), constVol(60, 1)),
		},
		{name: "nil inputs yield empty keys"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CurrentStateKeys(tt.daily, tt.minute)
			for _, h := range []marketdata.Horizon{marketdata.H1h, marketdata.H1d, marketdata.H1w} {
				if _, ok := got[h]; !ok {
					t.Errorf("horizon %s missing from map", h)
				}
			}
			check := func(h marketdata.Horizon, want string) {
				g := got[h]
				if sub, isSub := strings.CutPrefix(want, "contains:"); isSub {
					if !strings.Contains(g, sub) {
						t.Errorf("%s key = %q, want it to contain %q", h, g, sub)
					}
					return
				}
				if g != want {
					t.Errorf("%s key = %q, want %q", h, g, want)
				}
			}
			check(marketdata.H1d, tt.wantDaily)
			check(marketdata.H1w, tt.wantDaily) // daily key serves 1d and 1w
			check(marketdata.H1h, tt.wantH1h)
		})
	}
}

// The current key must resolve against rows Build produced from the same
// history — the round trip the daemon actually performs.
func TestCurrentKeyResolvesAgainstBuild(t *testing.T) {
	daily := mkDailyBars(geom(120, 100, 0.01), constVol(120, 3))
	rows := Build(daily, nil)
	key := CurrentStateKeys(daily, nil)[marketdata.H1d]
	if key == "" {
		t.Fatal("expected a current 1d key")
	}
	got, ok := Lookup(rows[marketdata.H1d], key)
	if !ok {
		t.Fatalf("Lookup(%q) found nothing", key)
	}
	if got.StateKey != key {
		t.Errorf("resolved %q, want exact match %q", got.StateKey, key)
	}
}

// ── lookup fallback policy ──────────────────────────────────────────────

func TestLookup(t *testing.T) {
	mk := func(key string, n int) marketdata.Expectancy {
		return marketdata.Expectancy{Horizon: marketdata.H1d, StateKey: key, N: n, MeanFwd: float64(n)}
	}
	full := "rsi:low|trend:above|mom:up|rvol:high"

	tests := []struct {
		name    string
		rows    []marketdata.Expectancy
		key     string
		wantKey string
		wantOK  bool
	}{
		{
			name:    "exact match with enough samples wins",
			rows:    []marketdata.Expectancy{mk(full, 8), mk("rsi:low", 100)},
			key:     full,
			wantKey: full,
			wantOK:  true,
		},
		{
			name: "thin exact match loses to first well-sampled parent",
			rows: []marketdata.Expectancy{
				mk(full, 6),
				mk("rsi:low|trend:above|mom:up", 7),
				mk("rsi:low|trend:above", 20),
				mk("rsi:low", 100),
			},
			key:     full,
			wantKey: "rsi:low|trend:above",
			wantOK:  true,
		},
		{
			name:    "missing exact key falls through to parent",
			rows:    []marketdata.Expectancy{mk("rsi:low|trend:above", 9)},
			key:     full,
			wantKey: "rsi:low|trend:above",
			wantOK:  true,
		},
		{
			name:    "all candidates thin returns most specific anyway",
			rows:    []marketdata.Expectancy{mk(full, 5), mk("rsi:low", 6)},
			key:     full,
			wantKey: full,
			wantOK:  true,
		},
		{
			name:   "no candidate matches",
			rows:   []marketdata.Expectancy{mk("rsi:high", 50)},
			key:    full,
			wantOK: false,
		},
		{
			name:   "empty key finds nothing",
			rows:   []marketdata.Expectancy{mk("rsi:low", 50)},
			key:    "",
			wantOK: false,
		},
		{
			name:   "empty rows find nothing",
			key:    full,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(tt.rows, tt.key)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tt.wantOK, got)
			}
			if ok && got.StateKey != tt.wantKey {
				t.Errorf("resolved %q, want %q", got.StateKey, tt.wantKey)
			}
		})
	}
}

func TestPrefixChain(t *testing.T) {
	tests := []struct {
		key  string
		want []string
	}{
		{
			key:  "rsi:low|trend:above|mom:up|rvol:high",
			want: []string{"rsi:low|trend:above|mom:up|rvol:high", "rsi:low|trend:above|mom:up", "rsi:low|trend:above", "rsi:low"},
		},
		{
			key:  "rsi:mid|mom:down|rvol:normal",
			want: []string{"rsi:mid|mom:down|rvol:normal", "rsi:mid|mom:down", "rsi:mid"},
		},
		{key: "rsi:high", want: []string{"rsi:high"}},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := prefixChain(tt.key)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("chain[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
