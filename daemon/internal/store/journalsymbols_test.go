package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestJournalSymbolsOffersExactlyWhatTheJournalAccepts: the member journal's
// picker (JournalSymbols) and the POST /api/journal validation
// (JournalSymbolByTicker) share one predicate, so the server accepts exactly
// what the picker offers: tracked US stocks AND ETFs with daily bars, which
// the SEC directory (companies) does not list.
func TestJournalSymbolsOffersExactlyWhatTheJournalAccepts(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "journal.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Insert symbols
	aaplSym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "AAPL")
	if err != nil {
		t.Fatalf("UpsertSymbol AAPL: %v", err)
	}
	spySym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "SPDR S&P 500 ETF Trust")
	if err != nil {
		t.Fatalf("UpsertSymbol SPY: %v", err)
	}
	spygsym, err := st.UpsertSymbol(ctx, "SPYG", md.Stocks, "SPDR Portfolio S&P 500 Growth ETF")
	if err != nil {
		t.Fatalf("UpsertSymbol SPYG: %v", err)
	}
	_, err = st.UpsertSymbol(ctx, "NOBAR", md.Stocks, "No Bars Corp")
	if err != nil {
		t.Fatalf("UpsertSymbol NOBAR: %v", err)
	}
	hourlySym, err := st.UpsertSymbol(ctx, "HOURLY", md.Stocks, "Hourly Only Inc")
	if err != nil {
		t.Fatalf("UpsertSymbol HOURLY: %v", err)
	}
	goneSym, err := st.UpsertSymbol(ctx, "GONE", md.Stocks, "Gone Inc")
	if err != nil {
		t.Fatalf("UpsertSymbol GONE: %v", err)
	}
	if err := st.SetSymbolActive(ctx, goneSym.ID, false); err != nil {
		t.Fatalf("SetSymbolActive GONE false: %v", err)
	}
	btcSym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("UpsertSymbol BTC/USD: %v", err)
	}

	// Insert bars
	bar1d := func(id int64) md.Bar {
		return md.Bar{SymbolID: id, TF: md.TF1d, Ts: 1_790_000_000, Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 1000}
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar1d(aaplSym.ID)}); err != nil {
		t.Fatalf("UpsertBars AAPL: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar1d(spySym.ID)}); err != nil {
		t.Fatalf("UpsertBars SPY: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar1d(spygsym.ID)}); err != nil {
		t.Fatalf("UpsertBars SPYG: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: hourlySym.ID, TF: md.TF1h, Ts: 1_790_000_000, Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 1000}}); err != nil {
		t.Fatalf("UpsertBars HOURLY: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar1d(goneSym.ID)}); err != nil {
		t.Fatalf("UpsertBars GONE: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar1d(btcSym.ID)}); err != nil {
		t.Fatalf("UpsertBars BTC/USD: %v", err)
	}

	// Insert companies for AAPL
	if err := st.UpsertCompanies(ctx, []CompanyRow{
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc.", Exchange: "", SIC: "", SICDesc: "", UpdatedTs: 0},
	}); err != nil {
		t.Fatalf("UpsertCompanies AAPL: %v", err)
	}

	// 1. JournalSymbols(ctx, "spy", 8) -> [SPY, SPYG]
	got, err := st.JournalSymbols(ctx, "spy", 8)
	if err != nil {
		t.Fatalf("JournalSymbols(\"spy\", 8): %v", err)
	}
	want := []JournalSymbol{
		{ID: spySym.ID, Symbol: "SPY", Name: "SPDR S&P 500 ETF Trust"},
		{ID: spygsym.ID, Symbol: "SPYG", Name: "SPDR Portfolio S&P 500 Growth ETF"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("JournalSymbols(\"spy\", 8) = %v, want %v", got, want)
	}

	// 2. JournalSymbols(ctx, "apple", 8) -> [AAPL] with Name from companies
	got, err = st.JournalSymbols(ctx, "apple", 8)
	if err != nil {
		t.Fatalf("JournalSymbols(\"apple\", 8): %v", err)
	}
	want = []JournalSymbol{
		{ID: aaplSym.ID, Symbol: "AAPL", Name: "Apple Inc."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("JournalSymbols(\"apple\", 8) = %v, want %v", got, want)
	}

	// 3. Negative cases: nobar, hourly, gone, bitcoin, btc -> no rows
	for _, tc := range []struct {
		query string
		desc  string
	}{
		{"nobar", "NOBAR"},
		{"hourly", "HOURLY"},
		{"gone", "GONE"},
		{"bitcoin", "BTC/USD"},
		{"btc", "BTC/USD"},
	} {
		got, err := st.JournalSymbols(ctx, tc.query, 8)
		if err != nil {
			t.Fatalf("JournalSymbols(%q, 8) for %s: %v", tc.query, tc.desc, err)
		}
		if len(got) != 0 {
			t.Errorf("JournalSymbols(%q, 8) for %s: got %v, want empty", tc.query, tc.desc, got)
		}
	}

	// 4. JournalSymbols(ctx, "  ", 8) -> zero rows
	got, err = st.JournalSymbols(ctx, "  ", 8)
	if err != nil {
		t.Fatalf("JournalSymbols(\"  \", 8): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("JournalSymbols(\"  \", 8) = %v, want empty", got)
	}

	// 5. JournalSymbolByTicker tests
	testCases := []struct {
		ticker   string
		wantOK   bool
		wantID   int64
		wantSym  string
		wantName string
	}{
		{"spy", true, spySym.ID, "SPY", "SPDR S&P 500 ETF Trust"},
		{"AAPL", true, aaplSym.ID, "AAPL", "Apple Inc."},
		{"NOBAR", false, 0, "", ""},
		{"HOURLY", false, 0, "", ""},
		{"GONE", false, 0, "", ""},
		{"BTC/USD", false, 0, "", ""},
		{"ZZZZ", false, 0, "", ""},
	}
	for _, tc := range testCases {
		gotJS, ok, err := st.JournalSymbolByTicker(ctx, tc.ticker)
		if err != nil {
			t.Fatalf("JournalSymbolByTicker(%q): %v", tc.ticker, err)
		}
		if ok != tc.wantOK {
			t.Errorf("JournalSymbolByTicker(%q).ok = %v, want %v", tc.ticker, ok, tc.wantOK)
			continue
		}
		if !tc.wantOK {
			continue
		}
		if gotJS.ID != tc.wantID {
			t.Errorf("JournalSymbolByTicker(%q).ID = %d, want %d", tc.ticker, gotJS.ID, tc.wantID)
		}
		if gotJS.Symbol != tc.wantSym {
			t.Errorf("JournalSymbolByTicker(%q).Symbol = %q, want %q", tc.ticker, gotJS.Symbol, tc.wantSym)
		}
		if gotJS.Name != tc.wantName {
			t.Errorf("JournalSymbolByTicker(%q).Name = %q, want %q", tc.ticker, gotJS.Name, tc.wantName)
		}
	}

	// 6. Agreement: for queries in []string{"s", "a", "n", "h", "g", "b"} and every row from JournalSymbols,
	//    JournalSymbolByTicker(row.Symbol) must be ok with same ID.
	queries := []string{"s", "a", "n", "h", "g", "b"}
	for _, q := range queries {
		rows, err := st.JournalSymbols(ctx, q, 8)
		if err != nil {
			t.Fatalf("JournalSymbols(%q, 8): %v", q, err)
		}
		for _, row := range rows {
			gotJS, ok, err := st.JournalSymbolByTicker(ctx, row.Symbol)
			if err != nil {
				t.Fatalf("JournalSymbolByTicker(%q): %v", row.Symbol, err)
			}
			if !ok {
				t.Errorf("JournalSymbolByTicker(%q) from JournalSymbols(%q) returned ok=false", row.Symbol, q)
				continue
			}
			if gotJS.ID != row.ID {
				t.Errorf("JournalSymbolByTicker(%q) from JournalSymbols(%q): got ID %d, want %d", row.Symbol, q, gotJS.ID, row.ID)
			}
		}
	}
}
