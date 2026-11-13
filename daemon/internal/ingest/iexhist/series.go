package iexhist

import (
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/splitfix"
)

// Day is one symbol's bar on one trading date ("20261110").
type Day struct {
	Date string
	Bar
}

var nyc = mustNYC()

func mustNYC() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic("iexhist: no America/New_York zone data: " + err.Error())
	}
	return loc
}

// BarTs is the timestamp the rest of SignalDeck expects on a daily bar:
// midnight New York time of the trading date (05:00Z in winter, 04:00Z in
// summer), the same stamp the existing daily bars carry.
func BarTs(date string) (int64, error) {
	t, err := time.ParseInLocation("20060102", date, nyc)
	if err != nil {
		return 0, fmt.Errorf("iexhist: bad trading date %q: %w", date, err)
	}
	return t.Unix(), nil
}

// AdjustSplits back-adjusts a raw series for splits. IEX trade prints are
// unadjusted, so a 4:1 split is a -75% one-day "move" that every predictor
// would read as real. Detection is splitfix's ratio test (a near-exact simple
// ratio, corroborated by volume); every bar BEFORE a detected split has its
// prices divided by the factor and its volume multiplied by it. Returns the
// adjusted series, oldest first, and the factors applied.
//
// ponytail: dividends are not adjusted (price return, not total return), and a
// split on a ratio outside splitfix's table is left in and will be refused by
// the predictors' own wild-move guard. A corporate-actions source would fix
// both.
func AdjustSplits(days []Day) ([]Day, []string) {
	out := append([]Day(nil), days...)
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	ts := make(map[int64]int, len(out))
	in := make([]splitfix.Bar, 0, len(out))
	for i, d := range out {
		t, err := BarTs(d.Date)
		if err != nil {
			continue
		}
		ts[t] = i
		in = append(in, splitfix.Bar{Ts: t, Close: d.Close, Volume: float64(d.Volume)})
	}
	var applied []string
	for _, s := range splitfix.Detect(in).Suspects {
		var a, b float64
		if n, _ := fmt.Sscanf(s.NearestName, "%f:%f", &a, &b); n != 2 || a <= 0 || b <= 0 {
			continue
		}
		f := a / b
		at, ok := ts[s.Ts]
		if !ok {
			continue
		}
		for i := 0; i < at; i++ {
			out[i].Open /= f
			out[i].High /= f
			out[i].Low /= f
			out[i].Close /= f
			out[i].Volume = int64(float64(out[i].Volume)*f + 0.5)
		}
		applied = append(applied, out[at].Date+" "+s.NearestName)
	}
	return out, applied
}

// IEX test symbols (ZVZZT, ZWZZT, ZXIET, ZTEST, ...) trade on the feed and are
// not securities.
var testSymbol = regexp.MustCompile(`^Z[A-Z]ZZT$|^ZEXIT$|^ZIEXT$|^ZXIET$|^ZTEST$|^ZVV$`)

// PickUniverse ranks symbols by median IEX dollar volume over the newest
// `recent` trading dates and returns the top n that traded on at least
// minCoverage of ALL the dates given. dates must be sorted ascending.
//
// ponytail: funds and leveraged products are not excluded (IEX's feed does not
// label them). The public record must say so; a fund list would fix it.
func PickUniverse(series map[string][]Day, dates []string, recent, n int, minCoverage float64) []string {
	if len(dates) == 0 || recent < 1 || n < 1 {
		return nil
	}
	if recent > len(dates) {
		recent = len(dates)
	}
	cut := dates[len(dates)-recent]
	type ranked struct {
		sym string
		dv  float64
	}
	var rs []ranked
	for sym, days := range series {
		if testSymbol.MatchString(sym) || float64(len(days)) < minCoverage*float64(len(dates)) {
			continue
		}
		var dv []float64
		for _, d := range days {
			if d.Date >= cut && d.Close > 0 {
				dv = append(dv, d.Close*float64(d.Volume))
			}
		}
		if len(dv) == 0 || len(dv) < recent*9/10 {
			continue
		}
		sort.Float64s(dv)
		rs = append(rs, ranked{sym, dv[len(dv)/2]})
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].dv != rs[j].dv {
			return rs[i].dv > rs[j].dv
		}
		return rs[i].sym < rs[j].sym
	})
	if n > len(rs) {
		n = len(rs)
	}
	out := make([]string, n)
	for i := range out {
		out[i] = rs[i].sym
	}
	return out
}
