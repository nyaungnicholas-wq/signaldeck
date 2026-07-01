package expectancy

import (
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ── synthetic series builders ───────────────────────────────────────────

// mkBars zips parallel close/volume series into bars (Ts spacing is
// irrelevant to the package logic; it never reads timestamps).
func mkBars(closes, vols []float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, len(closes))
	for i := range closes {
		bars[i] = marketdata.Bar{
			Ts:     int64(i) * 86400,
			Open:   closes[i],
			High:   closes[i],
			Low:    closes[i],
			Close:  closes[i],
			Volume: vols[i],
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
			got := Build(mkBars(tt.closes, tt.vols), nil)

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
	closes := geom(400, 100, 0.01)
	got := Build(nil, mkBars(closes, constVol(400, 5)))

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
	if want := 19; r.N != want { // i = 60,75,...,330 with i+60 < 400
		t.Errorf("N = %d, want %d", r.N, want)
	}
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
		{"daily one short of minimum", 79, 0, 0},
		{"minute one short of minimum", 0, 299, 0},
		{"daily at minimum", 80, 0, 2}, // 1d + 1w
		{"minute at minimum", 0, 300, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var daily, minute []marketdata.Bar
			if tt.daily > 0 {
				daily = mkBars(geom(tt.daily, 100, 0.01), constVol(tt.daily, 1))
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
}

// ── fallback rollup ─────────────────────────────────────────────────────

// Volume spikes split the walk into rvol:high and rvol:normal children that
// share every coarser prefix; the parents must count BOTH children's samples.
func TestBuildFallbackRollup(t *testing.T) {
	const n = 120
	closes := geom(n, 100, 0.01)
	vols := constVol(n, 1)
	for _, i := range []int{70, 80, 90, 100, 110} {
		vols[i] = 100 // >1.5x the SMA20 even with two spikes in window
	}
	got := Build(mkBars(closes, vols), nil)
	rows := got[marketdata.H1d]

	base := "rsi:high|trend:above|mom:up"
	high := findRow(t, rows, base+"|rvol:high")
	normal := findRow(t, rows, base+"|rvol:normal")
	if high.N != 5 {
		t.Errorf("rvol:high N = %d, want 5", high.N)
	}
	if normal.N != 54 {
		t.Errorf("rvol:normal N = %d, want 54", normal.N)
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
	got := Build(mkBars(closes, constVol(n, 7)), nil)
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
	upDaily := mkBars(geom(120, 100, 0.01), constVol(120, 1))
	upMinute := mkBars(geom(400, 100, 0.001), constVol(400, 1))

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
	regimeBars := mkBars(regime, constVol(250, 1))

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
			daily:     mkBars(geom(120, 100, 0.01), spikeVols),
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
			daily:  mkBars(geom(60, 100, 0.01), constVol(60, 1)),
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
	daily := mkBars(geom(120, 100, 0.01), constVol(120, 3))
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
