package store

// Equivalence tests for the two member per-symbol lookups rewritten in step 4
// (2026-10-01): each new query must return exactly the rows the old one did.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// oldInstBySymbolSQL is InstHoldingsBySymbol as it was: a GROUP BY cik join
// over every manager's history, run on each request.
const oldInstBySymbolSQL = `
	SELECT h.cik, h.manager, h.period, h.symbol_id, h.cusip, h.name, h.value, h.shares
	FROM inst_holdings h
	JOIN (SELECT cik, MAX(period) AS mx FROM inst_holdings GROUP BY cik) t
	  ON t.cik = h.cik AND t.mx = h.period
	WHERE h.symbol_id = ?
	ORDER BY h.value DESC LIMIT ?`

func TestInstHoldingsBySymbolMatchesGroupByJoin(t *testing.T) {
	st := openSignal8Store(t)
	ctx := context.Background()
	sym := func(s string) int64 {
		v, err := st.UpsertSymbol(ctx, s, md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	aapl, nvda, msft := sym("AAPL"), sym("NVDA"), sym("MSFT")
	p := func(id int64) *int64 { return &id }
	for i, h := range []InstHoldingRow{
		{CIK: "0001", Period: "2026-06-30", SymbolID: p(aapl), Value: 500},
		{CIK: "0001", Period: "2026-06-30", SymbolID: p(nvda), Value: 450},
		{CIK: "0001", Period: "2026-03-31", SymbolID: p(aapl), Value: 900},
		{CIK: "0002", Period: "2026-03-31", SymbolID: p(aapl), Value: 300},
		{CIK: "0002", Period: "2026-03-31", SymbolID: p(msft), Value: 350},
		{CIK: "0002", Period: "2025-12-31", SymbolID: p(aapl), Value: 700},
		{CIK: "0003", Period: "2026-06-30", SymbolID: p(nvda), Value: 410},
		{CIK: "0003", Period: "2026-03-31", SymbolID: p(aapl), Value: 800}, // an older period: never current
		{CIK: "0004", Period: "2026-06-30", SymbolID: p(aapl), Value: 100},
		{CIK: "0004", Period: "2026-06-30", SymbolID: nil, Value: 120}, // unmatched
	} {
		h.Manager, h.Name, h.Shares = "M"+h.CIK, "N", 1
		h.CUSIP = "C" + string(rune('A'+i))
		if err := st.UpsertInstHolding(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	old := func(id int64, limit int) []InstHoldingRow {
		rows, err := st.db.QueryContext(ctx, oldInstBySymbolSQL, id, limit)
		if err != nil {
			t.Fatal(err)
		}
		out, err := scanInstRows(rows)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// The fixture must exercise the latest-period rule, or equality proves nothing.
	want := old(aapl, 50)
	var ciks []string
	for _, r := range want {
		ciks = append(ciks, r.CIK)
	}
	if strings.Join(ciks, ",") != "0001,0002,0004" {
		t.Fatalf("old query on the fixture returned ciks %v, want 0001,0002,0004", ciks)
	}

	for _, id := range []int64{aapl, nvda, msft, 999999} {
		for _, limit := range []int{50, 2, 1} {
			got, err := st.InstHoldingsBySymbol(ctx, id, limit)
			if err != nil {
				t.Fatal(err)
			}
			if want := old(id, limit); !reflect.DeepEqual(got, want) {
				t.Fatalf("symbol %d limit %d:\n new %+v\n old %+v", id, limit, got, want)
			}
		}
	}
}

// oldResolve is companyProfile's loop over ListSymbols(ctx, true), as it was.
func oldResolve(syms []md.Symbol, ticker string) (md.Symbol, bool) {
	for _, s := range syms {
		base := s.Symbol
		if i := strings.IndexByte(base, '/'); i >= 0 {
			base = base[:i]
		}
		if eqASCIIFold(s.Symbol, ticker) || eqASCIIFold(base, ticker) {
			return s, true
		}
	}
	return md.Symbol{}, false
}

func eqASCIIFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	lower := func(c byte) byte {
		if 'A' <= c && c <= 'Z' {
			return c + 'a' - 'A'
		}
		return c
	}
	for i := 0; i < len(a); i++ {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func TestActiveSymbolByTickerMatchesListSymbolsLoop(t *testing.T) {
	st := openSignal8Store(t)
	ctx := context.Background()
	for _, s := range []struct {
		sym string
		m   md.Market
	}{
		{"AAPL", md.Stocks}, {"BTC/USD", md.Crypto}, {"BTC", md.Stocks},
		{"ETH/USD", md.Crypto}, {"eth", md.Stocks}, {"A_B", md.Stocks},
		{"AXB/USD", md.Crypto}, {"BRK.B", md.Stocks}, {"X/Y/Z", md.Crypto},
		{"INACT", md.Stocks},
	} {
		v, err := st.UpsertSymbol(ctx, s.sym, s.m, "")
		if err != nil {
			t.Fatal(err)
		}
		if s.sym == "INACT" {
			if err := st.SetSymbolActive(ctx, v.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	syms, err := st.ListSymbols(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	// The fixture must reach the cases that distinguish a correct lookup.
	for ticker, want := range map[string]string{
		"BTC":   "BTC/USD", // crypto sorts before stocks: the loop's first match wins
		"A_B":   "A_B",     // an unescaped LIKE would have matched AXB/USD first
		"eth":   "ETH/USD",
		"X/Y":   "", // a ticker holding '/' never matches a base
		"INACT": "", // inactive
	} {
		got, ok := oldResolve(syms, ticker)
		if (want == "") == ok || (ok && got.Symbol != want) {
			t.Fatalf("fixture: old resolve(%q) = %q/%v, want %q", ticker, got.Symbol, ok, want)
		}
	}

	for _, ticker := range []string{
		"AAPL", "aapl", "BTC", "btc", "ETH", "eth", "BTC/USD", "btc/usd", "A_B", "A%", "A_",
		"AXB", "X", "X/Y", "X/Y/Z", "INACT", "NOPE", "", "BRK.B", "brk.b",
	} {
		want, wantOK := oldResolve(syms, ticker)
		got, ok, err := st.ActiveSymbolByTicker(ctx, ticker)
		if err != nil {
			t.Fatalf("%q: %v", ticker, err)
		}
		if ok != wantOK || (ok && (got.ID != want.ID || got.Symbol != want.Symbol || got.Market != want.Market)) {
			t.Errorf("%q: new %+v/%v, old %+v/%v", ticker, got, ok, want, wantOK)
		}
	}
}
