package marketdata

import "testing"

func TestDailyBarSettledStocks(t *testing.T) {
	// 2026-09-04 04:00Z (EDT stamp)
	ts := int64(1788494400)
	// session still open (20h after stamp)
	if DailyBarSettled(Stocks, ts, ts+20*3600) {
		t.Fatalf("stocks: expected false at +20h, got true")
	}
	// settled after 22h
	if !DailyBarSettled(Stocks, ts, ts+22*3600) {
		t.Fatalf("stocks: expected true at +22h, got false")
	}
	// 21h + 3599s = 21:59:59, still not settled
	if DailyBarSettled(Stocks, ts, ts+21*3600+3599) {
		t.Fatalf("stocks: expected false at +21h3599s, got true")
	}
}

func TestDailyBarSettledCrypto(t *testing.T) {
	// 2026-09-07 00:00Z
	ts := int64(1788739200)
	if DailyBarSettled(Crypto, ts, ts+23*3600) {
		t.Fatalf("crypto: expected false at +23h, got true")
	}
	if !DailyBarSettled(Crypto, ts, ts+24*3600) {
		t.Fatalf("crypto: expected true at +24h, got false")
	}
}

func TestDailyBarSettledUnknownMarketIsConservative(t *testing.T) {
	ts := int64(1788494400) // any stamp
	if DailyBarSettled(Market("other"), ts, ts+23*3600) {
		t.Fatalf("unknown market: expected false at +23h, got true")
	}
	if !DailyBarSettled(Market("other"), ts, ts+24*3600) {
		t.Fatalf("unknown market: expected true at +24h, got false")
	}
}

func TestLatestSettledDaily(t *testing.T) {
	// three consecutive EDT stock stamps: 2026-09-02 04:00Z, 2026-09-03 04:00Z, 2026-09-04 04:00Z
	bars := []Bar{
		{Ts: 1788321600},
		{Ts: 1788408000},
		{Ts: 1788494400},
	}
	now := int64(1788494400 + 3600) // +1h after last stamp
	bar, ok := LatestSettledDaily(Stocks, bars, now)
	if !ok {
		t.Fatalf("expected ok true, got false")
	}
	if bar.Ts != 1788408000 {
		t.Fatalf("expected Ts 1788408000, got %d", bar.Ts)
	}
	// now after settle of last bar
	now = 1788494400 + 22*3600 // exactly settle time
	bar, ok = LatestSettledDaily(Stocks, bars, now)
	if !ok {
		t.Fatalf("expected ok true after settle, got false")
	}
	if bar.Ts != 1788494400 {
		t.Fatalf("expected Ts 1788494400, got %d", bar.Ts)
	}
	// empty slice
	bar, ok = LatestSettledDaily(Stocks, []Bar{}, now)
	if ok {
		t.Fatalf("expected ok false for empty slice, got true")
	}
	// now earlier than every bar's settle time
	now = 1788321600 - 1 // before first bar timestamp
	bar, ok = LatestSettledDaily(Stocks, bars, now)
	if ok {
		t.Fatalf("expected ok false when now earlier than all settle times, got true")
	}
}
