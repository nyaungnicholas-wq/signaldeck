package insights

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

var testNow = time.Unix(1_750_000_000, 0).UTC()

// honestyTendency / honestyFallback are the two allowed honesty lines — one
// of them must appear in EVERY symbol body.
const (
	honestyTendencyFrag = "measured historical tendency from n="
	honestyFallback     = "Not enough comparable history to quote a tendency."
)

func mkScore(h md.Horizon, s float64, comps ...md.ScoreComponent) md.Score {
	return md.Score{Horizon: h, Score: s, Components: comps}
}

// fullContext is a fixture with every field populated.
func fullContext() SymbolContext {
	return SymbolContext{
		Sym:          md.Symbol{ID: 7, Symbol: "AAPL", Market: md.Stocks},
		LastClose:    189.23,
		DayChangePct: -1.34,
		Scores: map[md.Horizon]md.Score{
			md.H1h: mkScore(md.H1h, 0.22),
			md.H1d: mkScore(md.H1d, 0.30,
				md.ScoreComponent{Name: "trend_sma", Contrib: 0.10, Note: "price holding above the 200-day average"},
				md.ScoreComponent{Name: "rsi", Contrib: -0.18, Note: "RSI recovering from oversold"},
			),
			md.H1w: mkScore(md.H1w, -0.05),
		},
		Expect: map[md.Horizon]*md.Expectancy{
			md.H1d: {
				Horizon: md.H1d, StateKey: "rsi:low|trend:above",
				N: 42, HitRate: 0.62, MedianFwd: 0.004,
			},
		},
		StateKeys: map[md.Horizon]string{md.H1d: "rsi:low|trend:above"},
	}
}

func hasBody(t *testing.T, got md.Insight, frags ...string) {
	t.Helper()
	for _, f := range frags {
		if !strings.Contains(got.Body, f) {
			t.Errorf("body missing %q\nbody: %s", f, got.Body)
		}
	}
}

// sentenceCount approximates the number of sentences; every clause the
// composer emits ends with ". " or a final ".".
func sentenceCount(body string) int {
	body = strings.TrimSpace(body)
	if body == "" {
		return 0
	}
	n := strings.Count(body, ". ")
	if strings.HasSuffix(body, ".") {
		n++
	}
	return n
}

func TestVerdictThresholds(t *testing.T) {
	cases := []struct {
		name  string
		score float64
		want  string
	}{
		{"deep_positive", 0.75, "strong buy pressure"},
		{"boundary_strong_buy", 0.5, "strong buy pressure"},
		{"just_below_strong", 0.49, "mild buy pressure"},
		{"boundary_mild_buy", 0.15, "mild buy pressure"},
		{"just_below_mild", 0.14, "balanced"},
		{"zero", 0, "balanced"},
		{"just_above_neg_band", -0.14, "balanced"},
		{"boundary_mild_sell", -0.15, "mild sell pressure"},
		{"just_above_strong_sell", -0.49, "mild sell pressure"},
		{"boundary_strong_sell", -0.5, "strong sell pressure"},
		{"deep_negative", -0.9, "strong sell pressure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdict(tc.score); got != tc.want {
				t.Fatalf("verdict(%v) = %q, want %q", tc.score, got, tc.want)
			}
			c := SymbolContext{
				Sym:    md.Symbol{ID: 1, Symbol: "TST"},
				Scores: map[md.Horizon]md.Score{md.H1d: mkScore(md.H1d, tc.score)},
			}
			in := ComposeSymbol(c, testNow)
			if !strings.Contains(in.Headline, tc.want) {
				t.Errorf("headline %q missing verdict %q", in.Headline, tc.want)
			}
		})
	}
}

func TestComposeSymbolFull(t *testing.T) {
	in := ComposeSymbol(fullContext(), testNow)

	if in.Scope != "symbol" {
		t.Errorf("scope = %q, want symbol", in.Scope)
	}
	if in.Symbol != "AAPL" {
		t.Errorf("symbol = %q", in.Symbol)
	}
	if in.SymbolID == nil || *in.SymbolID != 7 {
		t.Errorf("symbolID = %v, want 7", in.SymbolID)
	}
	if in.Ts != testNow.Unix() {
		t.Errorf("ts = %d, want %d", in.Ts, testNow.Unix())
	}
	// Headline: "<SYM> <verdict>: <driver note>" — driver is the top-|Contrib|
	// component (rsi at -0.18, not trend_sma at 0.10).
	if want := "AAPL mild buy pressure: RSI recovering from oversold"; in.Headline != want {
		t.Errorf("headline = %q, want %q", in.Headline, want)
	}

	hasBody(t, in,
		"AAPL last closed at 189.23 (-1.3% on the day).",
		"over the next hour (+0.22)",
		"over the next day (+0.30)",
		"over the next week (-0.05)",
		"the biggest 1-day driver: RSI recovering from oversold.",
		"setups like this one (oversold RSI, above its long-term trend)",
		"resolved higher 62.0% of the time over the next day (n=42, median +0.4%).",
		"This is a measured historical tendency from n=42 samples, not a forecast.",
	)
	if strings.Contains(in.Body, "Caveat") {
		t.Errorf("unexpected stale caveat in body: %s", in.Body)
	}
	if n := sentenceCount(in.Body); n < 3 || n > 6 {
		t.Errorf("sentence count = %d, want 3..6\nbody: %s", n, in.Body)
	}

	var ev struct {
		Symbol    string             `json:"symbol"`
		LastClose float64            `json:"lastClose"`
		Scores    map[string]float64 `json:"scores"`
		N         int                `json:"n"`
		HitRate   float64            `json:"hitRate"`
		StateKey  string             `json:"stateKey"`
	}
	if err := json.Unmarshal([]byte(in.Data), &ev); err != nil {
		t.Fatalf("data not valid JSON: %v\ndata: %s", err, in.Data)
	}
	if ev.Symbol != "AAPL" || ev.LastClose != 189.23 || ev.N != 42 || ev.HitRate != 0.62 {
		t.Errorf("evidence numbers wrong: %+v", ev)
	}
	if ev.Scores["1d"] != 0.30 {
		t.Errorf("evidence 1d score = %v, want 0.30", ev.Scores["1d"])
	}
	if ev.StateKey != "rsi:low|trend:above" {
		t.Errorf("evidence stateKey = %q", ev.StateKey)
	}
}

func TestComposeSymbolDegraded(t *testing.T) {
	cases := []struct {
		name        string
		ctx         SymbolContext
		wantBody    []string
		notInBody   []string
		headlineHas string
	}{
		{
			name:        "everything_missing",
			ctx:         SymbolContext{Sym: md.Symbol{ID: 2, Symbol: "BTC/USD"}, LastClose: 65000, DayChangePct: 2.1},
			wantBody:    []string{"No horizon scores are available yet.", honestyFallback, "+2.1% on the day"},
			headlineHas: "no 1-day read",
		},
		{
			name: "only_1h_score",
			ctx: SymbolContext{
				Sym:    md.Symbol{ID: 3, Symbol: "ETH/USD"},
				Scores: map[md.Horizon]md.Score{md.H1h: mkScore(md.H1h, 0.6)},
			},
			wantBody:    []string{"strong buy pressure over the next hour (+0.60)", honestyFallback},
			notInBody:   []string{"over the next day ("},
			headlineHas: "no 1-day read",
		},
		{
			name: "nil_expectancy_pointer",
			ctx: SymbolContext{
				Sym:    md.Symbol{ID: 4, Symbol: "AAPL"},
				Scores: map[md.Horizon]md.Score{md.H1d: mkScore(md.H1d, -0.2)},
				Expect: map[md.Horizon]*md.Expectancy{md.H1d: nil},
			},
			wantBody:    []string{honestyFallback},
			headlineHas: "mild sell pressure",
		},
		{
			name: "zero_sample_expectancy",
			ctx: SymbolContext{
				Sym:    md.Symbol{ID: 5, Symbol: "AAPL"},
				Scores: map[md.Horizon]md.Score{md.H1d: mkScore(md.H1d, 0)},
				Expect: map[md.Horizon]*md.Expectancy{md.H1d: {N: 0, HitRate: 0.9}},
			},
			wantBody:    []string{honestyFallback},
			notInBody:   []string{"resolved higher"},
			headlineHas: "balanced",
		},
		{
			name: "score_without_components",
			ctx: SymbolContext{
				Sym:    md.Symbol{ID: 6, Symbol: "SPY"},
				Scores: map[md.Horizon]md.Score{md.H1d: mkScore(md.H1d, 0.55)},
			},
			wantBody:    []string{honestyFallback},
			headlineHas: "strong buy pressure: no dominant driver",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := ComposeSymbol(tc.ctx, testNow) // must not panic
			hasBody(t, in, tc.wantBody...)
			for _, f := range tc.notInBody {
				if strings.Contains(in.Body, f) {
					t.Errorf("body unexpectedly contains %q\nbody: %s", f, in.Body)
				}
			}
			if !strings.Contains(in.Headline, tc.headlineHas) {
				t.Errorf("headline %q missing %q", in.Headline, tc.headlineHas)
			}
			if n := sentenceCount(in.Body); n < 3 || n > 6 {
				t.Errorf("sentence count = %d, want 3..6\nbody: %s", n, in.Body)
			}
			if !json.Valid([]byte(in.Data)) {
				t.Errorf("data not valid JSON: %s", in.Data)
			}
		})
	}
}

// TestHonestyLineAlwaysPresent sweeps contexts and requires one of the two
// honesty lines in every body — the non-negotiable of the readable layer.
func TestHonestyLineAlwaysPresent(t *testing.T) {
	ctxs := map[string]SymbolContext{
		"empty":       {},
		"full":        fullContext(),
		"stale_only":  {Sym: md.Symbol{Symbol: "X"}, Stale: true, StaleFor: time.Hour},
		"scores_only": {Sym: md.Symbol{Symbol: "Y"}, Scores: map[md.Horizon]md.Score{md.H1w: mkScore(md.H1w, -0.7)}},
	}
	for name, c := range ctxs {
		t.Run(name, func(t *testing.T) {
			in := ComposeSymbol(c, testNow)
			if !strings.Contains(in.Body, honestyTendencyFrag) && !strings.Contains(in.Body, honestyFallback) {
				t.Errorf("no honesty line in body: %s", in.Body)
			}
		})
	}
}

func TestStaleCaveat(t *testing.T) {
	cases := []struct {
		name     string
		stale    bool
		staleFor time.Duration
		want     string
	}{
		{"stale_hours_minutes", true, 2*time.Hour + 30*time.Minute, "Caveat: data is stale (2h30m) — treat this read as outdated."},
		{"stale_minutes", true, 12 * time.Minute, "Caveat: data is stale (12m) — treat this read as outdated."},
		{"stale_seconds", true, 45 * time.Second, "Caveat: data is stale (45s) — treat this read as outdated."},
		{"stale_days", true, 76 * time.Hour, "Caveat: data is stale (3d4h) — treat this read as outdated."},
		{"not_stale", false, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fullContext()
			c.Stale, c.StaleFor = tc.stale, tc.staleFor
			in := ComposeSymbol(c, testNow)
			if tc.want == "" {
				if strings.Contains(in.Body, "Caveat") {
					t.Errorf("unexpected caveat: %s", in.Body)
				}
				return
			}
			hasBody(t, in, tc.want)
		})
	}
}

func TestHumanizeState(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"rsi:low", "oversold RSI"},
		{"trend:above", "above its long-term trend"},
		{"mom:up", "positive momentum"},
		{"rvol:high", "elevated volume"},
		{"rsi:low|trend:above", "oversold RSI, above its long-term trend"},
		{"rsi:low|trend:above|mom:up|rvol:high", "oversold RSI, above its long-term trend, positive momentum, elevated volume"},
		{"foo:bar", "foo bar"},             // unknown token: keep it visible
		{"macd_cross:up", "macd cross up"}, // underscores spaced too
		{"", "current conditions"},
		{"|", "current conditions"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := humanizeState(tc.key); got != tc.want {
				t.Fatalf("humanizeState(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestPct(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{1.23, "+1.2%"},
		{-0.8, "-0.8%"},
		{0, "+0.0%"},
		{12.34, "+12.3%"},
		{-15.06, "-15.1%"},
	}
	for _, tc := range cases {
		if got := Pct(tc.v); got != tc.want {
			t.Errorf("Pct(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestComposeMarket(t *testing.T) {
	four := []MarketBrief{
		{Symbol: "BTC/USD", Market: md.Crypto, Score1d: 0.60, DayChangePct: 3.12},
		{Symbol: "ETH/USD", Market: md.Crypto, Score1d: 0.20, DayChangePct: 1.05},
		{Symbol: "AAPL", Market: md.Stocks, Score1d: -0.30, DayChangePct: -1.24},
		{Symbol: "MSFT", Market: md.Stocks, Score1d: -0.05, DayChangePct: -0.20},
	}
	cases := []struct {
		name       string
		briefs     []MarketBrief
		wantBody   []string
		wantHeadln []string
	}{
		{
			name:   "split_four",
			briefs: four,
			wantBody: []string{
				"2 of 4 tracked symbols show positive 1-day pressure.",
				"Strongest is BTC/USD with a 1-day score of +0.60 (+3.1% on the day)",
				"weakest is AAPL at -0.30 (-1.2% on the day)",
				"Regime read: split",
			},
			wantHeadln: []string{"split", "2 of 4"},
		},
		{
			name: "majority_positive",
			briefs: []MarketBrief{
				{Symbol: "A", Score1d: 0.4, DayChangePct: 1},
				{Symbol: "B", Score1d: 0.3, DayChangePct: 0.5},
				{Symbol: "C", Score1d: 0.1, DayChangePct: 0.2},
				{Symbol: "D", Score1d: -0.2, DayChangePct: -0.4},
			},
			wantBody:   []string{"3 of 4 tracked symbols show positive 1-day pressure.", "Regime read: majority positive"},
			wantHeadln: []string{"majority positive", "3 of 4"},
		},
		{
			name: "majority_negative",
			briefs: []MarketBrief{
				{Symbol: "A", Score1d: -0.4, DayChangePct: -1},
				{Symbol: "B", Score1d: -0.3, DayChangePct: -0.5},
				{Symbol: "C", Score1d: 0.1, DayChangePct: 0.2},
			},
			wantBody:   []string{"1 of 3 tracked symbols show positive 1-day pressure.", "Regime read: majority negative"},
			wantHeadln: []string{"majority negative", "1 of 3"},
		},
		{
			// A zero score is neither positive nor negative: 1 pos of 2 is a
			// split, not a majority.
			name: "zero_score_is_neutral",
			briefs: []MarketBrief{
				{Symbol: "A", Score1d: 0, DayChangePct: 0},
				{Symbol: "B", Score1d: 0.1, DayChangePct: 0.3},
			},
			wantBody:   []string{"1 of 2 tracked symbols show positive 1-day pressure.", "Regime read: split"},
			wantHeadln: []string{"split", "1 of 2"},
		},
		{
			name:   "single_symbol",
			briefs: []MarketBrief{{Symbol: "BTC/USD", Score1d: 0.7, DayChangePct: 4.2}},
			wantBody: []string{
				"1 of 1 tracked symbols show positive 1-day pressure.",
				"The only tracked symbol is BTC/USD with a 1-day score of +0.70 (+4.2% on the day).",
				"Regime read: majority positive",
			},
			wantHeadln: []string{"1 of 1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := ComposeMarket(tc.briefs, testNow)
			if in.Scope != "market" {
				t.Errorf("scope = %q, want market", in.Scope)
			}
			if in.SymbolID != nil {
				t.Errorf("market insight should have nil SymbolID, got %v", *in.SymbolID)
			}
			if in.Ts != testNow.Unix() {
				t.Errorf("ts = %d, want %d", in.Ts, testNow.Unix())
			}
			hasBody(t, in, tc.wantBody...)
			for _, f := range tc.wantHeadln {
				if !strings.Contains(in.Headline, f) {
					t.Errorf("headline %q missing %q", in.Headline, f)
				}
			}
			if !json.Valid([]byte(in.Data)) {
				t.Errorf("data not valid JSON: %s", in.Data)
			}
		})
	}
}

func TestComposeMarketEmpty(t *testing.T) {
	in := ComposeMarket(nil, testNow) // must not panic
	if in.Scope != "market" {
		t.Errorf("scope = %q", in.Scope)
	}
	if !strings.Contains(in.Headline, "no tracked symbols") {
		t.Errorf("headline = %q", in.Headline)
	}
	hasBody(t, in, "No symbols are being tracked yet")
	if !json.Valid([]byte(in.Data)) {
		t.Errorf("data not valid JSON: %s", in.Data)
	}
}

func TestMarketEvidenceNumbers(t *testing.T) {
	in := ComposeMarket([]MarketBrief{
		{Symbol: "A", Score1d: 0.5, DayChangePct: 2},
		{Symbol: "B", Score1d: -0.6, DayChangePct: -3},
	}, testNow)
	var ev struct {
		Total    int     `json:"total"`
		Positive int     `json:"positive"`
		Negative int     `json:"negative"`
		Best     string  `json:"best"`
		Worst    string  `json:"worst"`
		BestScr  float64 `json:"bestScore"`
	}
	if err := json.Unmarshal([]byte(in.Data), &ev); err != nil {
		t.Fatalf("data not valid JSON: %v", err)
	}
	if ev.Total != 2 || ev.Positive != 1 || ev.Negative != 1 || ev.Best != "A" || ev.Worst != "B" || ev.BestScr != 0.5 {
		t.Errorf("evidence wrong: %+v", ev)
	}
}

func TestFmtPrice(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{189.23, "189.23"},
		{65000, "65000.00"},
		{0.000123, "0.000123"},
		{0.5, "0.5"},
		{0, "0"},
	}
	for _, tc := range cases {
		if got := fmtPrice(tc.v); got != tc.want {
			t.Errorf("fmtPrice(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}
