// Signal8 wave — Stage 4: tape-seeder tests. Uses the same t.TempDir store +
// httptest multi-bars mock as the poller tests; never the live DB or Alpaca.
package universe

import (
	"context"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestTapeSeederNoClient: with no Alpaca keys the seeder still REGISTERS every
// tape ETF (daily-only) and reports honestly that it can't backfill.
func TestTapeSeederNoClient(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	w := &TapeSeeder{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "no Alpaca keys") {
		t.Fatalf("detail = %q, want honest no-keys note", detail)
	}
	daily := false
	syms, err := st.ActiveStockSymbols(ctx, &daily)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]bool{}
	for _, s := range syms {
		got[s.Symbol] = true
	}
	for _, e := range TapeETFs {
		if !got[e.Symbol] {
			t.Errorf("tape ETF %s not registered as daily-only", e.Symbol)
		}
	}
}

// TestTapeSeederBackfillsMissingOnly: only ETFs with ZERO daily bars are
// backfilled; a streamed hot-set member (SPY) keeps its stream flag; the
// second run is a no-op sweep.
func TestTapeSeederBackfillsMissingOnly(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	// SPY is already streamed (first-boot hot set) AND already has a daily bar.
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("seed SPY: %v", err)
	}
	if err := st.SetSymbolStream(ctx, spy.ID, true); err != nil {
		t.Fatalf("stream SPY: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: spy.ID, TF: md.TF1d, Ts: 1_700_000_000, Open: 1, High: 1, Low: 1, Close: 1},
	}); err != nil {
		t.Fatalf("seed SPY bar: %v", err)
	}

	var seen []string
	mock := multiBarsMock(t, &seen)
	w := &TapeSeeder{St: st, Alpaca: mockClient(mock.URL)}

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "backfilled") {
		t.Fatalf("detail = %q, want backfill summary", detail)
	}
	// SPY had bars → must NOT be re-requested; the other 7 must be.
	reqd := map[string]bool{}
	for _, s := range seen {
		reqd[s] = true
	}
	if reqd["SPY"] {
		t.Errorf("SPY re-backfilled despite existing daily bars")
	}
	for _, e := range TapeETFs {
		if e.Symbol == "SPY" {
			continue
		}
		if !reqd[e.Symbol] {
			t.Errorf("missing tape ETF %s not backfilled", e.Symbol)
		}
	}

	// SPY keeps its stream flag (registration never demotes the hot set).
	got, err := st.GetSymbol(ctx, "SPY", md.Stocks)
	if err != nil {
		t.Fatalf("get SPY: %v", err)
	}
	if !got.Stream {
		t.Errorf("SPY demoted out of the streamed hot set by tape seeding")
	}

	// Second run: everything now has bars → cheap no-op sweep, no new requests.
	seen = seen[:0]
	detail, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(detail, "all") || len(seen) != 0 {
		t.Fatalf("second run detail=%q requests=%v, want no-op", detail, seen)
	}
}
