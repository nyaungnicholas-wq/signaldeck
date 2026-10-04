package pipeline

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// SD-30 label window (PREREGISTRATION.md §15): from labelBaseSinceTs the base
// is the issue day's own daily bar, so the label never starts before issue for
// an in-session stock row or any crypto row, and an after-close stock row is
// graded from its own session's close, not the session before. US daily bars
// are stamped at ET midnight (04:00Z in October); crypto bars at 00:00Z.
func TestSettledBaseUsesTheIssueDaysOwnBar(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	utc := func(y int, m time.Month, d, h, min int) int64 {
		return time.Date(y, m, d, h, min, 0, 0, time.UTC).Unix()
	}
	stockBar := func(id int64, y int, m time.Month, d int) md.Bar {
		return md.Bar{SymbolID: id, TF: md.TF1d, Ts: utc(y, m, d, 4, 0), Open: 100, High: 100, Low: 100, Close: 100}
	}
	cryptoBar := func(id int64, y int, m time.Month, d int) md.Bar {
		return md.Bar{SymbolID: id, TF: md.TF1d, Ts: utc(y, m, d, 0, 0), Open: 100, High: 100, Low: 100, Close: 100}
	}
	mustSym := func(name string, market md.Market) int64 {
		s, err := st.UpsertSymbol(ctx, name, market, "")
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	stock := mustSym("STK", md.Stocks)       // Fri 10-02, Mon 10-05, Fri 10-09 bars
	gap := mustSym("GAP", md.Stocks)         // Fri 10-02 only: Mon 10-05 missing
	old := mustSym("OLD", md.Stocks)         // Fri 09-25, Mon 09-28: before the cutoff
	coin := mustSym("COIN", md.Crypto)       // 10-04, 10-05
	coinGap := mustSym("COINGAP", md.Crypto) // 10-04 only
	if err := st.UpsertBars(ctx, []md.Bar{
		stockBar(stock, 2026, 10, 2), stockBar(stock, 2026, 10, 5), stockBar(stock, 2026, 10, 9),
		stockBar(gap, 2026, 10, 2),
		stockBar(old, 2026, 9, 25), stockBar(old, 2026, 9, 28),
		cryptoBar(coin, 2026, 10, 4), cryptoBar(coin, 2026, 10, 5),
		cryptoBar(coinGap, 2026, 10, 4),
	}); err != nil {
		t.Fatal(err)
	}
	if utc(2026, 10, 4, 0, 0) != labelBaseSinceTs {
		t.Fatalf("labelBaseSinceTs = %d, want 2026-10-04T00:00:00Z", labelBaseSinceTs)
	}

	cases := []struct {
		name   string
		market md.Market
		sym    int64
		issue  int64
		wantOK bool
		want   int64 // base bar ts when wantOK
	}{
		// Mon 10-05 20:30 ET: after the close, before 22:00 ET settlement. The
		// old rule stepped back to Friday and graded Monday's visible move.
		{"stock after close", md.Stocks, stock, utc(2026, 10, 6, 0, 30), true, utc(2026, 10, 5, 4, 0)},
		{"stock in session", md.Stocks, stock, utc(2026, 10, 5, 15, 0), true, utc(2026, 10, 5, 4, 0)},
		// Monday 08:00 ET with no Monday bar yet: Friday, no session closed since.
		{"stock pre-market", md.Stocks, gap, utc(2026, 10, 5, 12, 0), true, utc(2026, 10, 2, 4, 0)},
		{"stock weekend", md.Stocks, stock, utc(2026, 10, 10, 15, 0), true, utc(2026, 10, 9, 4, 0)},
		// Monday's bar missing at Monday 20:30 ET: Friday would grade across
		// Monday's visible session, so the row waits.
		{"stock missing issue-day bar", md.Stocks, gap, utc(2026, 10, 6, 0, 30), false, 0},
		// 10-05 23:50Z: the old rule's base was 10-04 (10-05 still forming),
		// a window 99% elapsed at issue.
		{"crypto late in its day", md.Crypto, coin, utc(2026, 10, 5, 23, 50), true, utc(2026, 10, 5, 0, 0)},
		{"crypto missing issue-day bar", md.Crypto, coinGap, utc(2026, 10, 5, 23, 50), false, 0},
		// Before the cutoff the frozen rule stands: Mon 09-28 20:30 ET steps
		// back to the last SETTLED bar, Friday 09-25.
		{"before cutoff keeps the old rule", md.Stocks, old, utc(2026, 9, 29, 0, 30), true, utc(2026, 9, 25, 4, 0)},
	}
	for _, c := range cases {
		base, ok, err := settledBase(ctx, st, c.market, c.sym, c.issue)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if ok != c.wantOK {
			t.Errorf("%s: ok = %v, want %v (base %s)", c.name, ok, c.wantOK, time.Unix(base.Ts, 0).UTC())
			continue
		}
		if ok && base.Ts != c.want {
			t.Errorf("%s: base %s, want %s", c.name, time.Unix(base.Ts, 0).UTC(), time.Unix(c.want, 0).UTC())
		}
	}
}
