package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A daemon told to show IEX data publicly must refuse any database that was not
// built from IEX alone, and the default daemon must refuse an IEX database.
func TestCheckPriceSource(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck

	// Unmarked (the production shape).
	if err := st.CheckPriceSource(ctx, ""); err != nil {
		t.Errorf("default daemon on an unmarked database: %v", err)
	}
	if err := st.CheckPriceSource(ctx, "alpaca"); err != nil {
		t.Errorf("alpaca-public daemon on an unmarked database: %v", err)
	}
	if err := st.CheckPriceSource(ctx, "iexhist"); err == nil {
		t.Error("an IEX-public daemon accepted an unmarked database: it would publish whatever is in it")
	}

	// Marked IEX.
	if err := st.SetMeta(ctx, PriceSourceMetaKey, "iexhist"); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckPriceSource(ctx, "iexhist"); err != nil {
		t.Errorf("IEX daemon on an IEX database: %v", err)
	}
	if err := st.CheckPriceSource(ctx, ""); err == nil {
		t.Error("the default (Alpaca) daemon accepted an IEX-marked database")
	}

	// Marked IEX but holding an intraday bar: not single-source.
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1m, Ts: 60, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckPriceSource(ctx, "iexhist"); err == nil {
		t.Error("an IEX-marked database holding intraday bars was accepted")
	}
}
