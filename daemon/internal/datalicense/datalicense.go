// Package datalicense classifies each data source by what may legally be done
// with it, and is the single place the API asks before serving raw data.
//
// THE PROBLEM THIS SOLVES
// -----------------------
// Consuming licensed market data privately is fine. Serving it to someone else
// is redistribution, and several of this platform's sources prohibit that
// outright. The distinction is invisible in code — `GET /api/bars` looks
// identical whether the caller is the author on localhost or a customer on the
// internet — so it needs to be made explicit rather than remembered.
//
// Classification lives HERE, in code, next to the guard that enforces it,
// because a policy that lives only in a document drifts away from the software
// that is supposed to follow it.
package datalicense

import "strings"

// Class is what may be done with a source's data.
type Class string

const (
	// Public — government or public-domain. Free to use, store, and
	// redistribute. No agreement required.
	Public Class = "public"

	// Licensed — supplied under an agreement that permits USE but prohibits
	// REDISTRIBUTION. Derived analytics are generally fine; raw or
	// substantially-raw records are not.
	Licensed Class = "licensed"

	// Restricted — accessed through an undocumented or unauthenticated public
	// endpoint. Tolerated for personal use; not a defensible commercial supply
	// chain, and automated access may itself breach the operator's terms.
	Restricted Class = "restricted"
)

// Source describes one ingest path.
type Source struct {
	Name        string
	Class       Class
	Provider    string
	Note        string
	Redistrib   bool // may raw records be served onward?
}

// Sources is the authoritative classification. Adding an ingest package without
// adding it here is the mistake this table exists to make visible.
var Sources = map[string]Source{
	"alpaca": {
		Name: "alpaca", Class: Licensed, Provider: "Alpaca Markets",
		Note:      "market data under the Alpaca agreement; redistribution prohibited",
		Redistrib: false,
	},
	"cryptohist": {
		Name: "cryptohist", Class: Licensed, Provider: "Kraken",
		Note:      "exchange OHLC; terms restrict redistribution, derived works included",
		Redistrib: false,
	},
	"cryptolive": {
		Name: "cryptolive", Class: Licensed, Provider: "Coinbase/Kraken",
		Note:      "live book/trades; redistribution prohibited",
		Redistrib: false,
	},
	"hyperliquid": {
		Name: "hyperliquid", Class: Licensed, Provider: "Hyperliquid",
		Note: "public API, but commercial redistribution unestablished", Redistrib: false,
	},
	"tvscanner": {
		Name: "tvscanner", Class: Restricted, Provider: "TradingView",
		Note: "public scanner endpoint accessed without an account; automated access is " +
			"against TradingView's terms and the ratings are their IP — personal use only",
		Redistrib: false,
	},
	"stocktwits": {
		Name: "stocktwits", Class: Restricted, Provider: "StockTwits",
		Note: "undocumented public stream, no key; tolerated for personal use, not a " +
			"commercial supply chain",
		Redistrib: false,
	},
	"news": {
		Name: "news", Class: Licensed, Provider: "Alpaca news",
		Note: "headlines are publisher IP; store and display, never republish", Redistrib: false,
	},
	// ── genuinely free ────────────────────────────────────────────────────
	"edgar": {Name: "edgar", Class: Public, Provider: "SEC EDGAR",
		Note: "US government, public domain", Redistrib: true},
	"fred": {Name: "fred", Class: Public, Provider: "St. Louis Fed",
		Note: "public domain; attribution requested", Redistrib: true},
	"finra": {Name: "finra", Class: Public, Provider: "FINRA",
		Note: "public regulatory disclosure", Redistrib: true},
	"cftc": {Name: "cftc", Class: Public, Provider: "CFTC",
		Note: "public regulatory disclosure", Redistrib: true},
	"cboe": {Name: "cboe", Class: Public, Provider: "Cboe",
		Note: "free public statistics", Redistrib: true},
	"congress": {Name: "congress", Class: Public, Provider: "STOCK Act disclosures",
		Note: "public filings", Redistrib: true},
	"wikimedia": {Name: "wikimedia", Class: Public, Provider: "Wikimedia",
		Note: "CC-licensed pageview counts", Redistrib: true},
}

// PriceBarSources are the sources that produce the stored `bars` table. Any
// endpoint serving raw bars is serving THESE, so the guard consults them.
var PriceBarSources = []string{"alpaca", "cryptohist", "cryptolive"}

// BarsRedistributable reports whether raw stored bars may be served onward.
// It is false whenever ANY contributing source forbids it — which today is
// always, because every price feed in use is licensed. The function exists
// rather than a constant so adding a public-domain price source later changes
// behaviour by changing the table, not by editing the guard.
func BarsRedistributable() bool {
	for _, s := range PriceBarSources {
		if src, ok := Sources[s]; !ok || !src.Redistrib {
			return false
		}
	}
	return true
}

// RawDataNotice explains a refusal in terms an operator can act on.
func RawDataNotice() string {
	var restricted []string
	for _, s := range PriceBarSources {
		if src, ok := Sources[s]; ok && !src.Redistrib {
			restricted = append(restricted, src.Provider)
		}
	}
	return "raw bar export is disabled because the stored price data is licensed by " +
		strings.Join(restricted, ", ") +
		" and redistribution is prohibited by those agreements. Derived analytics " +
		"(forecasts, regimes, risk metrics) are unaffected and remain available. " +
		"Set SIGNALDECK_ALLOW_RAW_EXPORT=true ONLY if you hold your own agreements " +
		"permitting it — the flag records your assertion, it does not grant the right."
}
