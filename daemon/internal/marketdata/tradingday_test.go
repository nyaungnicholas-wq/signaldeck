package marketdata

import "testing"

// A UTC midnight to anchor synthetic sessions on.
func utcMidnight() int64 {
	const t = int64(1_700_000_000)
	return t - t%SecondsPerDay
}

// TestTradingDay_SessionNeverStraddles is the whole point of the fold: a US
// trading day must land in ONE bucket. The US extended session closes at 20:00
// ET — 00:00Z under EDT, 01:00Z under EST — so a UTC-midnight boundary splits
// every session's tail into a phantom second observation.
func TestTradingDay_SessionNeverStraddles(t *testing.T) {
	base0 := utcMidnight()
	regimes := []struct {
		name              string
		preOpen, close    int64 // seconds past UTC midnight
		rthOpen, rthClose int64
	}{
		// 04:00 ET pre-market → 20:00 ET close; RTH 09:30–16:00 ET.
		{"EDT", 8 * 3600, 24 * 3600, 13*3600 + 1800, 20 * 3600},
		{"EST", 9 * 3600, 25 * 3600, 14*3600 + 1800, 21 * 3600},
	}
	for _, r := range regimes {
		for day := int64(0); day < 365; day++ {
			base := base0 + day*SecondsPerDay
			want := TradingDay(base + r.preOpen)
			for _, tc := range []struct {
				label string
				ts    int64
			}{
				{"rth open", base + r.rthOpen},
				{"rth close", base + r.rthClose},
				{"extended close", base + r.close},
			} {
				if got := TradingDay(tc.ts); got != want {
					t.Fatalf("%s day %d: %s (ts=%d) folded to day %d but the session's "+
						"pre-market open (ts=%d) folded to %d — one session must not split",
						r.name, day, tc.label, tc.ts, got, base+r.preOpen, want)
				}
			}
		}
	}
}

// TestTradingDay_RegularHoursGroupingUnchanged proves the shift is a pure
// relabelling for regular-hours data: rows that already shared a UTC day still
// share a trading day, and rows that didn't still don't. This is what makes the
// change safe to adopt — no already-published regular-hours statistic moves,
// only the after-close tail is merged back onto its session.
func TestTradingDay_RegularHoursGroupingUnchanged(t *testing.T) {
	base0 := utcMidnight()
	rth := []int64{13*3600 + 1800, 15 * 3600, 17 * 3600, 19 * 3600, 21 * 3600}
	var delta int64
	first := true
	for day := int64(0); day < 365; day++ {
		for _, off := range rth {
			ts := base0 + day*SecondsPerDay + off
			d := TradingDay(ts) - ts/SecondsPerDay
			if first {
				delta, first = d, false
				continue
			}
			if d != delta {
				t.Fatalf("ts=%d: TradingDay-UTCday offset is %d but every other regular-hours "+
					"sample gave %d — the fold is not a constant relabelling within RTH, so "+
					"existing grouping WOULD move", ts, d, delta)
			}
		}
	}
}

// TestTradingDay_FloorDivisionAcrossEpoch: Go's / truncates toward zero, so a
// naive (ts-offset)/86400 folds days -1 and 0 onto the same index — two
// observations silently merged into one. Synthetic fixtures start at ts=0
// routinely, so this is reachable in tests even though live data is not.
func TestTradingDay_FloorDivisionAcrossEpoch(t *testing.T) {
	for ts := int64(-3 * SecondsPerDay); ts <= 3*SecondsPerDay; ts += 3600 {
		if got, next := TradingDay(ts), TradingDay(ts+SecondsPerDay); next != got+1 {
			t.Fatalf("ts=%d folds to %d but ts+1day folds to %d (want %d) — the fold is not "+
				"monotone across the epoch, so distinct days collapse", ts, got, next, got+1)
		}
		if got, prev := TradingDay(ts), TradingDay(ts-3600); got < prev {
			t.Fatalf("ts=%d folds to %d which is BEFORE ts-1h at %d — fold must be non-decreasing",
				ts, got, prev)
		}
	}
}

// TestTradingDay_BoundaryIsInTheDarkWindow pins WHERE the boundary sits. The
// safe band is [01:00Z, 08:00Z]: EST sessions run until 01:00Z and EDT
// pre-market opens at 08:00Z, so only inside that band does the cut split no
// session in either DST regime.
func TestTradingDay_BoundaryIsInTheDarkWindow(t *testing.T) {
	if TradingDayOffsetSecs < 1*3600 || TradingDayOffsetSecs > 8*3600 {
		t.Fatalf("fold boundary is %02d:00Z, outside the dark band [01:00Z, 08:00Z]; EST "+
			"sessions run to 01:00Z and EDT pre-market opens 08:00Z, so a boundary outside "+
			"that band splits a live session", TradingDayOffsetSecs/3600)
	}
	base := utcMidnight()
	at := base + TradingDayOffsetSecs
	if got, before := TradingDay(at), TradingDay(at-1); got != before+1 {
		t.Fatalf("the boundary instant ts=%d folds to %d and one second earlier folds to %d — "+
			"the boundary must start a new day", at, got, before)
	}
}
