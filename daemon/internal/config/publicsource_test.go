package config

import "testing"

// An instance that shows its data publicly must not be able to ingest Alpaca
// data: naming any public price source other than alpaca drops the keys,
// including the ones Load would otherwise borrow from stock-trader/.env.
func TestPublicPriceSourceDropsAlpacaKeys(t *testing.T) {
	t.Setenv("ALPACA_KEY", "k")
	t.Setenv("ALPACA_SECRET", "s")
	t.Setenv("SIGNALDECK_PUBLIC_PRICE_SOURCE", " IEXHist ")
	c := Load()
	if c.PublicPriceSource != "iexhist" {
		t.Fatalf("PublicPriceSource = %q, want iexhist", c.PublicPriceSource)
	}
	if c.HasAlpaca() {
		t.Fatal("an IEX-fed public instance still holds Alpaca keys")
	}
	t.Setenv("SIGNALDECK_PUBLIC_PRICE_SOURCE", "")
	if !Load().HasAlpaca() {
		t.Fatal("the default instance lost its Alpaca keys")
	}
	t.Setenv("SIGNALDECK_PUBLIC_PRICE_SOURCE", "alpaca")
	if !Load().HasAlpaca() {
		t.Fatal("alpaca as the public source must keep its keys")
	}
}
