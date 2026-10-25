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
	// REDISTRIBUTION. Raw or substantially-raw records may never be served
	// onward. Derived analytics are NOT automatically fine: Alpaca's terms bar
	// showing derivations to third parties too, so since 23 Oct 2026 they reach
	// only the operator on a published deployment (NonOperatorRoutes, below).
	Licensed Class = "licensed"

	// Restricted — accessed through an undocumented or unauthenticated public
	// endpoint. Tolerated for personal use; not a defensible commercial supply
	// chain, and automated access may itself breach the operator's terms.
	Restricted Class = "restricted"
)

// Source describes one ingest path.
type Source struct {
	Name      string
	Class     Class
	Provider  string
	Note      string
	Redistrib bool // may raw records be served onward?
}

// Sources is the authoritative classification. Adding an ingest package without
// adding it here is the mistake this table exists to make visible.
var Sources = map[string]Source{
	"alpaca": {
		Name: "alpaca", Class: Licensed, Provider: "Alpaca Markets",
		Note:      "market data under the Alpaca agreement; redistribution prohibited",
		Redistrib: false,
	},
	// Crypto paused 22 Oct 2026. Since 23 Oct 2026 the stored crypto history and
	// the forecasts built from it reach only the operator on a published
	// deployment: no crypto route is on NonOperatorRoutes, so NonOperatorSource
	// withholds it. Kraken's "derived works included" question is therefore
	// moot for anything served today.
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
	// The Note's "store and display" is stale: since 23 Oct 2026 headlines are
	// displayed only to the operator on a published deployment (Alpaca's
	// personal-use terms cover its news too). The string is left as it is
	// during the launch freeze; correct it in the next daemon release.
	"news": {
		Name: "news", Class: Licensed, Provider: "Alpaca news",
		Note: "headlines are publisher IP; store and display, never republish", Redistrib: false,
	},
	// ── retired 22 Oct 2026: their terms do not permit SignalDeck's use ─────
	// Pollers unwired, feature keys retired (gbm.RetiredSourceKey); stored rows
	// are kept and their routes are governed below.
	"fred": {Name: "fred", Class: Restricted, Provider: "St. Louis Fed (FRED)",
		Note: "RETIRED 22 Oct 2026: personal, non-commercial licence only; storing FRED " +
			"content in a database needs written consent, and some series are third-party " +
			"copyrighted (VIXCLS by Cboe, BAMLH0A0HYM2 by ICE)",
		Redistrib: false},
	"finra": {Name: "finra", Class: Restricted, Provider: "FINRA",
		Note: "RETIRED 22 Oct 2026: finra.org's Terms of Use bar building a database, " +
			"derivative works, automated extraction and use beyond one's own non-commercial use",
		Redistrib: false},
	"cboe": {Name: "cboe", Class: Restricted, Provider: "Cboe",
		Note: "RETIRED 22 Oct 2026: Cboe's website terms bar storing its market statistics " +
			"in a retrieval system, displaying them and deriving from them",
		Redistrib: false},
	// Not a data feed but a processor: the sentiment tagger and the AI layer sent
	// Alpaca/Benzinga headlines and SignalDeck data to it. llm.ProviderRetired
	// refuses its hosts; sentiment_score/sentiment_n are retired model inputs.
	"nvidia": {Name: "nvidia", Class: Restricted, Provider: "NVIDIA",
		Note: "RETIRED 2026-10-23: the hosted API (integrate.api.nvidia.com) was used under " +
			"NVIDIA's trial terms, which do not permit production use; nothing is sent to it " +
			"any more, and its stored sentiment ratings are not a model input",
		Redistrib: false},
	// Every stored congress_trades row is treated as Kadoa's: no row records its
	// source, Kadoa was the fallback from 7 Sep 2026 and the Stock Watcher
	// mirrors answer 403. Its terms (s.4, s.8) bar automated access,
	// reproduction and making the data available to third parties without written
	// permission, so the poller is unwired and the stored rows are operator-only.
	"kadoa": {Name: "kadoa", Class: Restricted, Provider: "Kadoa (congress trades dataset)",
		Note: "RETIRED 23 Oct 2026: Kadoa's terms (s.4, s.8) bar automated access, reproduction " +
			"and sharing its compiled rows without written permission; stored rows are kept " +
			"and shown only to the operator",
		Redistrib: false},
	// ── genuinely free ────────────────────────────────────────────────────
	"edgar": {Name: "edgar", Class: Public, Provider: "SEC EDGAR",
		Note: "free to access and reuse (SEC); fair-access rate limits apply", Redistrib: true},
	"cftc": {Name: "cftc", Class: Public, Provider: "CFTC",
		Note: "public regulatory disclosure", Redistrib: true},
	// KNOWN CONTRADICTION, latent: this row says Public/Redistrib, but the 23 Oct
	// 2026 rights review rated the official House/Senate route UNCLEAR (inventory
	// SD23-054/055). Nothing consults this key today: no RestrictedRoutes entry
	// names it, and /api/congress is withheld under "kadoa" (withheldBy). Left
	// unchanged during the launch freeze; reclassify it in the next daemon
	// release, before any route or poller uses it.
	"congress": {Name: "congress", Class: Public, Provider: "STOCK Act disclosures",
		Note: "public filings", Redistrib: true},
	"wikimedia": {Name: "wikimedia", Class: Public, Provider: "Wikimedia",
		Note: "pageview counts released under CC0", Redistrib: true},
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
//
// Its "derived analytics ... remain available" sentence is true only for the
// operator. On a published deployment every other caller is stopped earlier by
// the operator-only check (api security step 6c, NonOperatorSource) and gets
// WithheldNotice instead, so only the operator ever reads this text there.
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

// RestrictedRoutes maps an API path to the source whose licence governs what it
// serves.
//
// ONE list, beside the classification table, rather than a guard call copied
// into each handler. The 2026-07-26 review found exactly that copy MISSING from
// the three CSV exports: the same licensed rows walked out through a second
// door with no policy on it. Two copies of a legal rule drift; one cannot.
//
// This is defence in depth and NOT the primary control. On a published
// deployment api.publicRoutes already refuses every path below to an anonymous
// caller. What this adds is the case the allowlist cannot cover: a route added
// next month that serves vendor rows and that nobody remembers to think about.
//
// DELIBERATELY ABSENT: /api/chart-overlays. It looks like a bar endpoint and
// its own header says "derived from bars", but it emits only annotations -- a
// timestamp, a marker type, a label, this platform's own score and a direction
// flag. No OHLC, no volume, no vendor price. That is exactly the "derived
// analytics" the refusal notice says are unaffected, so governing it here
// would refuse a legitimate surface with a 451 whose stated reason is false.
// An over-broad licence guard is not a safe default: it publishes an untrue
// claim about what the data is.
//
// tvscanner and stocktwits are class Restricted -- automated access is against
// those providers' terms and the ratings are their IP -- so they are governed
// here even though they are not price bars and BarsRedistributable does not
// speak to them. FINRA, Cboe and FRED joined them on 22 Oct 2026, when their
// terms were found not to permit storing, deriving from or displaying their data.
//
// THE LICENCE LINE, for every route a member or an anonymous caller reaches:
//   - RAW is a vendor price, OHLC, volume, quote, rating, headline or message
//     text, OR a per-row value keyed to a symbol and a time from which a vendor
//     price or return series can be recovered by arithmetic alone (e.g. a
//     realized forward return between two vendor closes).
//   - DERIVED is forecasts, scores, probabilities, regime labels, indicator
//     values such as close-vs-own-200d-mean, volatility forecasts, and
//     aggregates over many rows. An aggregate of realized returns KEYED to a
//     symbol, or to a group a caller can narrow (a market, a postmortem
//     cluster, a symbol), counts as derived only over >= 10 rows. The score-
//     bucket means on /api/honesty (buckets[].meanFwd) are exempt for the
//     reason its points[] are: neither carries a symbol, and a point's ts is
//     the scoring pass's minute, which every symbol scored in that pass
//     shares, so nothing joins back to one symbol's closes (see
//     stripHonestyPoints in the api tests). Differencing successive snapshots
//     is a known limit of publishing any running aggregate.
//
// Members and anonymous callers get DERIVED and public-domain data only.
// api.TestMemberResponsesCarryNoVendorSentinels holds every member-reachable
// route to this line with seeded sentinel values. Since 23 Oct 2026 a
// published deployment narrows that further: a non-operator reaches only
// NonOperatorRoutes, so Alpaca-derived values reach the operator alone.
var RestrictedRoutes = map[string]string{
	"/api/bars":       "alpaca",
	"/api/snaps":      "cryptolive",
	"/api/news":       "news",
	"/api/stocktwits": "stocktwits",
	// /api/crypto-perp emits funding, openInterest and MARK PX -- a vendor
	// price. By the discriminator this file already uses two paragraphs above
	// ("No OHLC, no volume, no vendor price" is what makes a surface derived
	// analytics), that is vendor data, not an annotation, so it belongs here.
	//
	// It was the only Licensed source with a serving route and no entry.
	// stocktwits, added to the same dataexpansion.go package, was governed; this
	// one was missed. The asymmetry that hid it: an unknown source KEY fails
	// closed (TestUnknownSourceFailsClosed), but an ungoverned ROUTE fails OPEN
	// -- RouteRedistributable returns redistributable=true for any path absent
	// from this map. So a missing row is silent in the safe-looking direction.
	//
	// hyperliquid's own note records its commercial terms as UNESTABLISHED,
	// which is the case the release instruction says to withhold on rather than
	// guess. Loopback operators are unaffected: the guard also requires the
	// daemon to be reachable beyond localhost before it refuses.
	"/api/crypto-perp": "hyperliquid",
	// /api/stream/snaps is /api/snaps over SSE: the same cryptolive rows on a
	// push socket instead of a poll. Found 2026-09-20 by the route scan
	// (TestEveryRegisteredVendorRouteIsGoverned); a second door for the same
	// vendor rows with no policy on it, exactly the shape the CSV exports had.
	"/api/stream/snaps": "cryptolive",
	// Retired 22 Oct 2026 (licence): these serve only rows stored before then.
	"/api/shorts":         "finra",
	"/api/short-interest": "finra",
	"/api/cboe-pc":        "cboe",
	"/api/macro-series":   "fred",
	// /api/scenario fits a symbol's beta to a stored FRED series (VIXCLS by
	// default); it has no non-FRED factor to fall back to. /api/calendar,
	// /api/world-model, /api/recommendation and /api/options/vol-edge mixed
	// FRED into other data, so their FRED parts were removed instead.
	"/api/scenario":            "fred",
	"/api/tv-quote":            "tvscanner",
	"/api/tv-rating":           "tvscanner",
	"/api/tv-signals":          "tvscanner",
	"/api/export/bars.csv":     "alpaca",
	"/api/export/scores.csv":   "alpaca",
	"/api/export/outcomes.csv": "alpaca",
}

// RouteRedistributable reports whether the path may be served, and the source
// governing it. ok=true for any path this table does not govern. That fails
// OPEN, which is acceptable only because it now answers for the OPERATOR alone:
// every other caller on a published deployment goes through NonOperatorSource,
// which fails closed.
func RouteRedistributable(path string) (source string, redistributable bool, governed bool) {
	key, ok := RestrictedRoutes[path]
	if !ok {
		return "", true, false
	}
	s, known := Sources[key]
	if !known {
		// An unknown key is a drift bug, and the safe reading of "we do not
		// know what licence governs this" is that it may not be redistributed.
		return key, false, true
	}
	return key, s.Redistrib, true
}

// ── Operator-only content (23 Oct 2026) ──────────────────────────────────
//
// Alpaca licenses its market data AND news for personal, non-commercial use and
// forbids displaying, republishing or transmitting it to anyone else without
// written consent; its Developer Relations team wrote on 2026-09-03 that
// DERIVATIONS may not be provided to third parties either, and the NYSE
// agreement counts information derived from market data as market data.
// Nearly every number SignalDeck shows (prices, forecasts, grades, accuracy
// statistics) is computed from Alpaca bars. Kadoa's terms (s.4, s.8) bar
// sharing its compiled congress rows. So on a published deployment only the
// operator receives that content. A member account is not the operator, and a
// login is not a licence.
//
// NonOperatorRoutes is the WHOLE surface a non-operator may reach there, each
// entry with the reason its payload carries nothing derived from a licensed
// source. It is an allowlist for the reason api.publicRoutes is one: a route
// added next month is withheld until somebody checks its payload, instead of
// being served because nobody remembered it. RestrictedRoutes cannot do this
// job: it names refused routes, so an unlisted route passes it. The auth tiers
// still apply on top; this list never opens a route to a caller who could not
// reach it before.
var NonOperatorRoutes = map[string]string{
	"/api/health":  "probe; a non-operator gets the summary only (degraded flag, sign-up switches)",
	"/api/ready":   "probe; a non-operator gets ready true/false only",
	"/api/version": "build identity (version, revision, row stamp); no data",

	"/api/auth/register": "account flow", "/api/auth/login": "account flow",
	"/api/auth/logout": "account flow", "/api/auth/me": "the session's own identity",
	"/api/auth/verify": "account flow", "/api/auth/resend": "account flow",
	"/api/auth/forgot": "account flow", "/api/auth/reset": "account flow",
	"/api/auth/google":        "account flow",
	"/api/alerts/unsubscribe": "tokened off-switch for the daily email; a fixed sentence",
	"/api/waitlist":           "takes an email and answers ok; serves nothing back",
	"/api/tv-webhook":         "the operator's own inbound TradingView alerts; serves nothing back",

	"/api/prereg": "frozen claim wording, resolution rules and chain hashes; the measured " +
		"numbers are stripped for a non-operator (api/prereg.go)",
	"/api/ledger/verify":  "chain integrity: count, head hash, anchor and tamper status; no forecast",
	"/api/ledger/anchors": "signed chain heads: hashes, counts, signatures, keys; no forecast",

	"/api/account/export":              "the caller's own account rows; journal grades stripped (api/accountdata.go)",
	"/api/account/delete":              "erases the caller's own account; status only",
	"/api/alert-prefs":                 "the caller's own email and Telegram switches",
	"/api/alert-prefs/telegram-link":   "a one-time link code for the caller's own chat",
	"/api/alert-prefs/telegram-unlink": "the caller's own switches",

	"/api/watchlist": "the member's own symbols, identity only: no close, spark, score or bar time",
	"/api/watch":     "adds to the caller's own list; answers the symbol's identity",
	"/api/unwatch":   "removes from the caller's own list; answers the symbol's identity",
	"/api/subscribe": "answers the symbol's identity; the member tier refuses it (starts ingestion)",
	"/api/unsubscribe": "answers the symbol's identity; the member tier refuses it " +
		"(can stop a feed)",

	"/api/companies": "SEC company map, EDGAR share count and float; price, volume, day " +
		"change and market cap stripped for a non-operator (api/companies.go)",
	"/api/filings":      "SEC EDGAR filing index rows",
	"/api/insiders":     "SEC Form 4 rows as filed: shares, price and value come from the filing",
	"/api/institutions": "SEC 13F holdings as reported",
	"/api/dilution":     "flag built from SEC filings and XBRL share counts only",
	"/api/fundamentals": "SEC EDGAR XBRL company facts",
	"/api/company/profile": "EDGAR identity, peers, Form 4, 13F, filings and XBRL facts; the " +
		"LLM summary never runs for a non-operator",
}

// withheldBy names the governing source of a withheld route that is neither
// in RestrictedRoutes nor built on Alpaca data (the default).
var withheldBy = map[string]string{"/api/congress": "kadoa"}

// NonOperatorSource reports whether a non-operator may be served path on a
// published deployment and, when not, the source whose terms withhold it. A
// path on no list is withheld under Alpaca's terms: this fails CLOSED.
func NonOperatorSource(path string) (source string, cleared bool) {
	if _, ok := NonOperatorRoutes[path]; ok {
		return "", true
	}
	if s, ok := withheldBy[path]; ok {
		return s, false
	}
	if s, ok := RestrictedRoutes[path]; ok {
		return s, false
	}
	return "alpaca", false
}

// WithheldNotice is the plain sentence a withheld caller is shown for source.
func WithheldNotice(source string) string {
	switch source {
	case "alpaca":
		return "Withheld: SignalDeck's prices, forecasts and statistics are derived from Alpaca " +
			"market data, which is licensed for personal use only. They are shown only to the " +
			"operator until Alpaca gives written permission."
	case "kadoa":
		return "Withheld: the congressional trading rows were compiled by Kadoa, whose terms do " +
			"not permit sharing them without written permission. Collection has stopped and the " +
			"stored rows are shown only to the operator."
	}
	if s, ok := Sources[source]; ok {
		return "Withheld: this data comes from " + s.Provider + ", whose terms do not permit " +
			"SignalDeck to share it. It is shown only to the operator."
	}
	return "Withheld: the licence for this data does not permit sharing it. It is shown only " +
		"to the operator."
}
