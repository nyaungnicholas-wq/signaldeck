package iexhist

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestBarTsIsNewYorkMidnight(t *testing.T) {
	for date, want := range map[string]string{
		"20261110": "2026-11-10T05:00:00Z", // winter: UTC-5, matches the stored Alpaca daily bars
		"20260710": "2026-07-10T04:00:00Z", // summer: UTC-4
	} {
		ts, err := BarTs(date)
		if err != nil || time.Unix(ts, 0).UTC().Format(time.RFC3339) != want {
			t.Errorf("BarTs(%s) = %s (%v), want %s", date, time.Unix(ts, 0).UTC().Format(time.RFC3339), err, want)
		}
	}
	if _, err := BarTs("2026-11-10"); err == nil {
		t.Error("a malformed date must be an error")
	}
}

// 40 days at $400 then a 4:1 split to $100 with four times the volume. After
// adjustment the series must be continuous and the newest bars untouched.
func TestAdjustSplitsRemovesAFourForOne(t *testing.T) {
	var days []Day
	d0 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 80; i++ {
		px, vol := 400.0+float64(i%3), int64(1000)
		if i >= 40 {
			px, vol = 100.0+float64(i%3)/4, 4000
		}
		days = append(days, Day{Date: d0.AddDate(0, 0, i).Format("20060102"),
			Bar: Bar{Open: px, High: px + 1, Low: px - 1, Close: px, Volume: vol}})
	}
	adj, applied := AdjustSplits(days)
	if len(applied) != 1 {
		t.Fatalf("applied = %v, want exactly one 4:1 split", applied)
	}
	for i := 1; i < len(adj); i++ {
		if r := math.Abs(adj[i].Close/adj[i-1].Close - 1); r > 0.05 {
			t.Fatalf("adjusted series still jumps %.0f%% at %s", r*100, adj[i].Date)
		}
	}
	if adj[0].Close != 100 || adj[0].Volume != 4000 {
		t.Errorf("oldest bar = %+v, want close 100 and volume 4000 on the post-split basis", adj[0])
	}
	if adj[79] != days[79] {
		t.Errorf("newest bar changed: %+v", adj[79])
	}
	// A real crash on no clean ratio is left alone.
	crash := append([]Day(nil), days[:40]...)
	crash = append(crash, Day{Date: "20260301", Bar: Bar{Open: 230, High: 231, Low: 229, Close: 230, Volume: 1000}})
	if _, applied := AdjustSplits(crash); len(applied) != 0 {
		t.Errorf("a -42%% move on no split ratio was adjusted: %v", applied)
	}
}

func TestPickUniverseRanksByDollarVolumeAndDropsTestSymbols(t *testing.T) {
	var dates []string
	for i := 1; i <= 20; i++ {
		dates = append(dates, fmt.Sprintf("202601%02d", i))
	}
	mk := func(n int, px float64, vol int64) []Day {
		var ds []Day
		for _, d := range dates[len(dates)-n:] {
			ds = append(ds, Day{Date: d, Bar: Bar{Close: px, Volume: vol}})
		}
		return ds
	}
	series := map[string][]Day{
		"BIG":   mk(20, 100, 1000), // $100k a day
		"MID":   mk(20, 10, 5000),  // $50k
		"SMALL": mk(20, 1, 100),    // $100
		"NEW":   mk(5, 500, 9000),  // huge, but only a quarter of the history
		"ZVZZT": mk(20, 10, 1e6),   // IEX test symbol
	}
	got := PickUniverse(series, dates, 10, 2, 0.9)
	if len(got) != 2 || got[0] != "BIG" || got[1] != "MID" {
		t.Fatalf("universe = %v, want [BIG MID]", got)
	}
}
