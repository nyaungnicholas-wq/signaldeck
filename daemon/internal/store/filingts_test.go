package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestCorrectFilingTs(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "Zed")
	if err != nil {
		t.Fatal(err)
	}

	const base = int64(1790000000)
	const T = base + 37
	if T%86400 == 0 {
		t.Fatalf("T=%d is a multiple of 86400", T)
	}

	// accession "edt": filings AND insider_trades, stored filed_ts = T - 14400 (feed will say T) -> must be corrected in BOTH tables
	if _, err := st.InsertFiling(ctx, FilingRow{ID: "edt", SymbolID: sym.ID, Form: "4", FiledTs: T - 14400}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{Accession: "edt", SymbolID: sym.ID, Insider: "DOE JOHN", Code: "P", Shares: 1, Price: 1, Value: 1, TxTs: T, FiledTs: T - 14400}); err != nil {
		t.Fatal(err)
	}

	// accession "est": filings only, stored filed_ts = T - 18000 (feed will say T) -> corrected
	if _, err := st.InsertFiling(ctx, FilingRow{ID: "est", SymbolID: sym.ID, Form: "4", FiledTs: T - 18000}); err != nil {
		t.Fatal(err)
	}

	// accession "ok": filings AND insider_trades, stored filed_ts = T (feed says T) -> untouched
	if _, err := st.InsertFiling(ctx, FilingRow{ID: "ok", SymbolID: sym.ID, Form: "4", FiledTs: T}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{Accession: "ok", SymbolID: sym.ID, Insider: "DOE JOHN", Code: "P", Shares: 1, Price: 1, Value: 1, TxTs: T, FiledTs: T}); err != nil {
		t.Fatal(err)
	}

	// accession "odd": filings only, stored filed_ts = T - 3600 (feed says T) -> untouched (not an exact 4h/5h difference)
	if _, err := st.InsertFiling(ctx, FilingRow{ID: "odd", SymbolID: sym.ID, Form: "4", FiledTs: T - 3600}); err != nil {
		t.Fatal(err)
	}

	// accession "back": filings only, stored filed_ts = T (feed says T - 14400) -> untouched (feed is EARLIER)
	if _, err := st.InsertFiling(ctx, FilingRow{ID: "back", SymbolID: sym.ID, Form: "4", FiledTs: T}); err != nil {
		t.Fatal(err)
	}

	// accession "day": filings only, stored filed_ts = 86400*20000 - 14400 (feed says 86400*20000) -> untouched (feed value is a midnight, date-only fallback)
	dayFeed := int64(86400 * 20000)
	if _, err := st.InsertFiling(ctx, FilingRow{ID: "day", SymbolID: sym.ID, Form: "4", FiledTs: dayFeed - 14400}); err != nil {
		t.Fatal(err)
	}

	// accession "nofeed": filings only, stored filed_ts = T - 14400 (absent from feed) -> untouched
	if _, err := st.InsertFiling(ctx, FilingRow{ID: "nofeed", SymbolID: sym.ID, Form: "4", FiledTs: T - 14400}); err != nil {
		t.Fatal(err)
	}

	feed := map[string]int64{
		"edt":  T,
		"est":  T,
		"ok":   T,
		"odd":  T,
		"back": T - 14400,
		"day":  dayFeed,
	}

	getFiledTs := func(table, key string) int64 {
		var ts int64
		col := "id"
		if table == "insider_trades" {
			col = "accession"
		}
		if err := st.db.QueryRowContext(ctx, "SELECT filed_ts FROM "+table+" WHERE "+col+"=?", key).Scan(&ts); err != nil {
			t.Fatalf("select %s.%s: %v", table, key, err)
		}
		return ts
	}

	// 1. First call
	count, err := st.CorrectFilingTs(ctx, sym.ID, feed, 123456)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if count != 2 {
		t.Fatalf("first call count=%d want 2", count)
	}

	// 2. Read back filed_ts
	if got := getFiledTs("filings", "edt"); got != T {
		t.Fatalf("filings.edt got %d want %d", got, T)
	}
	if got := getFiledTs("insider_trades", "edt"); got != T {
		t.Fatalf("insider_trades.edt got %d want %d", got, T)
	}
	if got := getFiledTs("filings", "est"); got != T {
		t.Fatalf("filings.est got %d want %d", got, T)
	}
	if got := getFiledTs("filings", "ok"); got != T {
		t.Fatalf("filings.ok got %d want %d", got, T)
	}
	if got := getFiledTs("insider_trades", "ok"); got != T {
		t.Fatalf("insider_trades.ok got %d want %d", got, T)
	}
	if got := getFiledTs("filings", "odd"); got != T-3600 {
		t.Fatalf("filings.odd got %d want %d", got, T-3600)
	}
	if got := getFiledTs("filings", "back"); got != T {
		t.Fatalf("filings.back got %d want %d", got, T)
	}
	if got := getFiledTs("filings", "day"); got != dayFeed-14400 {
		t.Fatalf("filings.day got %d want %d", got, dayFeed-14400)
	}
	if got := getFiledTs("filings", "nofeed"); got != T-14400 {
		t.Fatalf("filings.nofeed got %d want %d", got, T-14400)
	}

	// 3. Assert filing_ts_corrections has exactly 3 rows
	var corrCount int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM filing_ts_corrections").Scan(&corrCount); err != nil {
		t.Fatalf("count corrections: %v", err)
	}
	if corrCount != 3 {
		t.Fatalf("filing_ts_corrections count=%d want 3", corrCount)
	}

	var oldTs, newTs, corrTs int64
	if err := st.db.QueryRowContext(ctx, `SELECT old_filed_ts, new_filed_ts, corrected_ts FROM filing_ts_corrections WHERE accession="edt" AND tbl="insider_trades"`).Scan(&oldTs, &newTs, &corrTs); err != nil {
		t.Fatalf("select edt/insider_trades correction: %v", err)
	}
	if oldTs != T-14400 {
		t.Fatalf("edt/insider_trades old_filed_ts=%d want %d", oldTs, T-14400)
	}
	if newTs != T {
		t.Fatalf("edt/insider_trades new_filed_ts=%d want %d", newTs, T)
	}
	if corrTs != 123456 {
		t.Fatalf("edt/insider_trades corrected_ts=%d want 123456", corrTs)
	}

	// 4. Second call with same feed
	count, err = st.CorrectFilingTs(ctx, sym.ID, feed, 123456)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if count != 0 {
		t.Fatalf("second call count=%d want 0", count)
	}
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM filing_ts_corrections").Scan(&corrCount); err != nil {
		t.Fatalf("count corrections after second: %v", err)
	}
	if corrCount != 3 {
		t.Fatalf("filing_ts_corrections count after second=%d want 3", corrCount)
	}

	// 5. Call with empty map
	count, err = st.CorrectFilingTs(ctx, sym.ID, map[string]int64{}, 123456)
	if err != nil {
		t.Fatalf("empty feed error: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty feed count=%d want 0", count)
	}
}
