package api

import (
	"net/http"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
)

// SIGNALDECK_PUBLIC_PRICE_SOURCE names the one price source an instance is fed
// by. A displayable source (IEX's historical feed) opens the price-derived
// routes to non-operators on THAT instance; Kadoa rows stay withheld on it, an
// unknown source clears nothing, and the default instance is unchanged.
func TestPublicPriceSourceOpensOnlyPriceDerivedRoutes(t *testing.T) {
	for _, tc := range []struct {
		source string
		open   bool
	}{{"", false}, {"iexhist", true}, {"alpaca", true}, {"stooq", false}, {"cryptohist", false}} {
		if got := datalicense.PublicPriceCleared(tc.source); got != tc.open {
			t.Errorf("PublicPriceCleared(%q) = %v, want %v", tc.source, got, tc.open)
		}
	}
	base := func(c *config.Config) { c.PublicSurface, c.PublicReads = false, true }

	srv, _, member, _ := startGuardOnly(t, func(c *config.Config) { base(c); c.PublicPriceSource = "iexhist" })
	// /api/accuracy is on both the anonymous and the member tier, and is a 451
	// for both on the default instance (it is built from price bars).
	for who, cl := range map[string]*http.Client{"anonymous": newClient(t), "member": member} {
		if code, _, body := call(t, cl, srv, http.MethodGet, "/api/accuracy"); code != 200 || body != "reached" {
			t.Errorf("%s on an IEX-fed instance: price-derived route answered %d %.80s, want it reached", who, code, body)
		}
		code, ctype, body := call(t, cl, srv, http.MethodGet, "/api/congress")
		checkWithheld(t, who, "/api/congress on an IEX-fed instance", code, ctype, body, "kadoa")
	}

	for _, src := range []string{"", "stooq"} {
		srv2, _, _, _ := startGuardOnly(t, func(c *config.Config) { base(c); c.PublicPriceSource = src })
		code, ctype, body := call(t, newClient(t), srv2, http.MethodGet, "/api/accuracy")
		checkWithheld(t, "anonymous", "price-derived route with source "+src, code, ctype, body, "alpaca")
	}
}
