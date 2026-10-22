package main

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func TestPauseCryptoDeactivatesOnlyCrypto(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(filepath.Join(t.TempDir(), "pc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck

	if _, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSymbol(ctx, "ETH/USD", md.Crypto, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, ""); err != nil {
		t.Fatal(err)
	}

	pauseCrypto(ctx, st)

	all, err := st.ListSymbols(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.Market == md.Crypto && s.Active {
			t.Errorf("crypto symbol %s should be inactive", s.Symbol)
		}
		if s.Market == md.Stocks && s.Symbol == "AAPL" && !s.Active {
			t.Errorf("stock symbol %s should be active", s.Symbol)
		}
	}

	active, err := st.ListSymbols(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Symbol != "AAPL" {
		t.Fatalf("expected only AAPL active, got %v", active)
	}

	pauseCrypto(ctx, st)

	all2, err := st.ListSymbols(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all2) != 3 {
		t.Fatalf("expected 3 symbols, got %d", len(all2))
	}
	for _, s := range all2 {
		if s.Market == md.Crypto && s.Active {
			t.Errorf("crypto symbol %s should be inactive after second call", s.Symbol)
		}
		if s.Market == md.Stocks && s.Symbol == "AAPL" && !s.Active {
			t.Errorf("stock symbol %s should be active after second call", s.Symbol)
		}
	}
}