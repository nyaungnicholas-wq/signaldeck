package api

// TestMemberResponsesCarryNoVendorSentinels holds every route a member can
// reach to the licence line written beside datalicense.RestrictedRoutes:
// members get DERIVED and public-domain data only. It seeds a sentinel value
// into every vendor table a handler could read, drives each member-reachable
// route through the PRODUCTION mux (d.routes) as a signed-in member, and scans
// each body for those values in every spelling a handler could print them in.
//
// A scanner that finds nothing and a scanner that never looked look the same,
// so three things stop this one passing hollow:
//   - the probe list is driven by the route set: a route memberAllowed admits
//     later (a new memberRoutes or publicRoutes entry, or anything under
//     /api/auth/ or /api/evidence/) fails here until it has a probe;
//   - every probe must answer 2xx (a 403 or 404 body holds no sentinel by
//     construction) and carry a marker proving its body came from the seeded
//     store;
//   - the same scanner, pointed at the OPERATOR's views of the same store,
//     must FIND the sentinels, or the seed is not reaching the handlers.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/backup"
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/evidence"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/postmortem"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

// newProductionServer serves the production route set behind the production
// middleware chain (the handler Serve builds) on a published fixture store,
// with an admin "owner" and the mail seams of startPublished. registry is the
// accuracy registry the accuracy route reads.
func newProductionServer(t *testing.T, mutate func(*config.Config), registry string) (*httptest.Server, *store.Store, *mailbox) {
	t.Helper()
	// Handlers that resolve paths from the environment must never reach the
	// live tree: the archive (see newTestServer) and the anchor signing key.
	t.Setenv("SIGNALDECK_ARCHIVE_DIR", filepath.Join(t.TempDir(), "archive"))
	// /api/ledger/verify signs anchors with the key at this path, and its
	// default is the operator's own key under the home directory.
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	return startPublished(t, mutate, true, func(d Deps) http.Handler {
		d.Cfg.DBPath = d.St.Path()
		d.RegistryPath = registry
		lim := newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst)
		return d.httpServerWith(d.routes(lim), lim).Handler
	})
}

// Sentinel vendor values: numbers no handler produces by accident.
const (
	sntO1, sntH1, sntL1, sntC1 = 7769.13731, 7779.13731, 7759.13731, 7771.13731
	sntO2, sntH2, sntL2, sntC2 = 7781.13731, 7799.13731, 7777.13731, 7793.37731
	sntV1, sntV2               = 9137731.0, 9237731.0
	sntFwd1, sntFwd2           = 0.0137731, 0.0291357 // realized forward returns between vendor closes
	sntShares                  = 1e6                  // EDGAR share count (public), for the mcap leak
	sntTVClose, sntTVRSI       = 6661.17731, 61.17731
	sntTVPrice, sntTVDelayed   = 6662.27731, 6663.37731
	sntTVChg, sntTVDayVol      = 1.717731, 8137731.0
	sntTwBull, sntTwBear       = 71713.0, 31371.0
	sntBid, sntAsk             = 5551.19731, 5552.29731
	sntMid, sntWMid            = 5551.74731, 5551.84731
	sntMark, sntOI             = 4441.17731, 4417731.0
)

type sentinel struct {
	what   string
	tokens []string // any one found in a body is a leak
}

func vendorSentinels() []sentinel {
	num := func(what string, vs ...float64) sentinel {
		s := sentinel{what: what}
		for _, v := range vs {
			s.tokens = append(s.tokens, numberSpellings(v)...)
		}
		return s
	}
	return []sentinel{
		num("vendor bar OHLC", sntO1, sntH1, sntL1, sntC1, sntO2, sntH2, sntL2, sntC2),
		num("vendor bar volume", sntV1, sntV2),
		num("day change % of two vendor closes", (sntC2/sntC1-1)*100),
		num("return between two vendor closes", sntC2/sntC1-1),
		num("market cap = shares x vendor close", sntShares*sntC2),
		num("realized forward return", sntFwd1, sntFwd2),
		num("TradingView rating", sntTVClose, sntTVRSI),
		num("TradingView quote", sntTVPrice, sntTVDelayed, sntTVChg, sntTVDayVol),
		num("StockTwits counts", sntTwBull, sntTwBear),
		num("crypto book", sntBid, sntAsk, sntMid, sntWMid),
		num("Hyperliquid perp", sntMark, sntOI),
		{"news headline text", []string{"SNTL_HEADLINE_Q9", "SNTL_PUBLISHER_Q9", "SNTL_NEWS_URL"}},
		{"TradingView label", []string{"SNTL_TVRATING"}},
		{"provider error text", []string{"SNTL_DQ_SECRET", "127.0.0.1:8321"}},
		{"backup path or URL", []string{"SNTL_BACKUP_PATH", "SNTL_OFFSITE"}},
	}
}

// numberSpellings is every way a handler could print v: exact (what
// encoding/json writes), rounded to 2, 4 and 6 places, and the same four for
// v*100 (a percent). A spelling with fewer than three significant digits is
// dropped: "0.01" would match by accident.
func numberSpellings(v float64) []string {
	var out []string
	for _, x := range []float64{v, v * 100} {
		for _, prec := range []int{-1, 2, 4, 6} {
			s := strconv.FormatFloat(x, 'f', prec, 64)
			if len(strings.TrimLeft(strings.ReplaceAll(s, ".", ""), "0")) >= 3 {
				out = append(out, s)
			}
		}
	}
	return out
}

// leaks lists every sentinel found in body. A number counts only standing on
// its own, never as digits inside a longer number (a timestamp, say).
func leaks(body string, sents []sentinel) []string {
	var found []string
	for _, s := range sents {
		for _, tok := range s.tokens {
			if containsToken(body, tok) {
				found = append(found, s.what+" "+tok)
			}
		}
	}
	return found
}

func containsToken(body, tok string) bool {
	if _, err := strconv.ParseFloat(tok, 64); err != nil {
		return strings.Contains(body, tok)
	}
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	for i := 0; ; {
		j := strings.Index(body[i:], tok)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(tok)
		if (j == 0 || !(digit(body[j-1]) || body[j-1] == '.')) && (end == len(body) || !digit(body[end])) {
			return true
		}
		i = j + 1
	}
}

type sentinelFixture struct {
	sntl, sntc, sntw md.Symbol
	evidenceID       string
	variant          int
}

// seedSentinels writes the sentinels into every vendor table, plus the
// derived and public-domain rows the member routes serve, so those routes
// answer with real bodies rather than empty ones. variant (0 or 1) changes
// the derived rows, so two stores seeded with different variants answer the
// cached routes differently and a body cached from the other store fails its
// marker.
func seedSentinels(t *testing.T, st *store.Store, variant int) sentinelFixture {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	sym := func(s string, m md.Market, name string) md.Symbol {
		t.Helper()
		v, err := st.UpsertSymbol(ctx, s, m, name)
		must(err)
		return v
	}
	fx := sentinelFixture{
		sntl:    sym("SNTL", md.Stocks, "Sentinel Corp"),
		sntc:    sym("SNTC/USD", md.Crypto, "Sentinel Coin"),
		sntw:    sym("SNTW", md.Stocks, "Sentinel Watch Inc"),
		variant: variant,
	}
	now := time.Now().Unix()
	day := now / 86400 * 86400
	ids := []int64{fx.sntl.ID, fx.sntc.ID}

	// Vendor rows. Alpaca/Kraken bars for both markets.
	for _, id := range ids {
		must(st.UpsertBars(ctx, []md.Bar{
			{SymbolID: id, TF: md.TF1d, Ts: day - 86400, Open: sntO1, High: sntH1, Low: sntL1, Close: sntC1, Volume: sntV1},
			{SymbolID: id, TF: md.TF1d, Ts: day, Open: sntO2, High: sntH2, Low: sntL2, Close: sntC2, Volume: sntV2},
		}))
	}
	must(st.UpsertTVRating(ctx, store.TVRatingRow{SymbolID: fx.sntl.ID, Ts: day, RecoAll: 0.5, RecoMA: 0.4,
		RecoOther: 0.1, RSI: sntTVRSI, Close: sntTVClose, Label: "SNTL_TVRATING"}))
	must(st.InsertTVQuote(ctx, store.TVQuoteRow{SymbolID: fx.sntl.ID, Ts: day, Price: sntTVPrice,
		DelayedClose: sntTVDelayed, ChangePct: sntTVChg, DayVolume: sntTVDayVol}))
	must(st.InsertStocktwits(ctx, store.StocktwitsRow{SymbolID: fx.sntl.ID, Ts: day,
		Bullish: int(sntTwBull), Bearish: int(sntTwBear), Total: int(sntTwBull + sntTwBear)}))
	must(st.InsertNews(ctx, store.NewsItem{ID: "sntl-news-1", SymbolID: fx.sntl.ID, Ts: day,
		Headline: "SNTL_HEADLINE_Q9", URL: "https://news.example/SNTL_NEWS_URL", Source: "SNTL_PUBLISHER_Q9",
		Sentiment: "positive", Score: 0.5}))
	must(st.InsertNewsSymbols(ctx, "sntl-news-1", []int64{fx.sntl.ID}))
	must(st.InsertSnap1s(ctx, md.Snap1s{SymbolID: fx.sntc.ID, Ts: now, Bid: sntBid, Ask: sntAsk,
		Mid: sntMid, WMid: sntWMid, Spread: sntAsk - sntBid}))
	must(st.InsertCryptoPerp(ctx, store.CryptoPerpRow{SymbolID: fx.sntc.ID, Ts: now, Funding: 0.0001,
		OpenInterest: sntOI, MarkPx: sntMark}))
	must(st.InsertDQ(ctx, md.DQEvent{SymbolID: &fx.sntl.ID, Ts: now, Kind: "stale",
		Detail: `Get "http://127.0.0.1:8321/api/snapshot?key=SNTL_DQ_SECRET": context deadline exceeded`}))
	must(st.SetMeta(ctx, backup.MetaLastBackupFile, `C:\backups\SNTL_BACKUP_PATH.db`))
	must(st.SetMeta(ctx, backup.MetaOffsiteDir, "s3://sntl-bucket/SNTL_OFFSITE"))

	// Realized returns, the realistic way: a calibrated call that missed,
	// resolved against the closes, then attributed. Two misses per market, so
	// no per-market aggregate below is a single row's value.
	miss := func(id, ts int64, fwd float64) {
		t.Helper()
		must(st.UpsertPrediction(ctx, store.Prediction{SymbolID: id, Horizon: md.H1d, Ts: ts,
			RawProb: 0.3, CalProb: 0.3, NUsed: 40, Components: "{}"}))
		must(st.ResolvePrediction(ctx, id, md.H1d, ts, fwd))
		ms, err := st.UnPostmortemedMisses(ctx, md.H1d, 10)
		must(err)
		for _, m := range ms {
			if m.SymbolID == id && m.Ts == ts {
				rep := postmortem.Classify(postmortem.Case{Prob: m.Prob, Up: m.Up, FwdReturn: m.FwdReturn,
					Disagreement: 0.5}, postmortem.DefaultThresholds())
				must(st.InsertPostmortem(ctx, m, rep, now))
			}
		}
		// The minute-score record the honesty scatter is built from.
		must(st.InsertScore(ctx, md.Score{SymbolID: id, Ts: ts, Horizon: md.H1d, Score: 0.7}))
		must(st.ResolveOutcome(ctx, id, md.H1d, ts, fwd))
	}
	miss(fx.sntl.ID, day-3*86400, sntFwd1)
	miss(fx.sntl.ID, day-2*86400, sntFwd2)
	miss(fx.sntc.ID, day-3*86400, sntFwd2)
	miss(fx.sntc.ID, day-2*86400, sntFwd1)
	for i := 0; i < variant; i++ { // not a sentinel: SNTW has no vendor bars
		miss(fx.sntw.ID, day-int64(4+i)*86400, 0.0123457)
	}
	_, err := st.AppendLedger(ctx, store.LedgerEntry{PredictedAt: day - 2*86400, SymbolID: fx.sntl.ID,
		Horizon: md.H1d, BarTs: day - 3*86400, RawProb: 0.31, CalProb: 0.3, FeatureHash: "sntl", ModelVersion: 1})
	must(err)

	// Derived rows: forecasts, a symbol model, regime-call postmortems.
	for _, c := range []struct {
		id   int64
		kind structregime.Kind
	}{{fx.sntl.ID, structregime.KindTrend21}, {fx.sntc.ID, structregime.KindTrendCrypto21}} {
		must(st.UpsertRegimeForecast(ctx, c.id, day, structregime.Forecast{Kind: c.kind, HorizonDays: 21,
			Regime: "uptrend", Conviction: regimeConviction[variant], HistoricalAccuracy: 0.972, Tier: "very-high",
			Rank: 0.97, N: 900}))
		must(st.UpsertVolForecast(ctx, c.id, day, volregime.Forecast{Regime: "elevated", Conviction: 0.86,
			HistoricalAccuracy: 0.743, Tier: "high", Rank: 0.93, N: 500}))
		_, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{SymbolID: c.id, Kind: c.kind, Ts: day - 40*86400,
			HorizonDays: 21, Regime: "uptrend", Conviction: 0.91, HistoricalAccuracy: 0.972, Rank: 0.95,
			NaiveLabel: "uptrend"})
		must(err)
	}
	due, err := st.DueRegimeOutcomes(ctx, now, 10)
	must(err)
	for _, o := range due {
		o.Actual = "downtrend"
		must(st.InsertRegimePostmortem(ctx, o.ID, o.SymbolID, o, "closeVsSma200Pct", 3.217,
			"called uptrend at high conviction and it broke", now))
	}
	// One index basket with a call, for /api/market-regimes.
	spy := sym("SPY", md.Stocks, "SPDR S&P 500")
	must(st.UpsertRegimeForecast(ctx, spy.ID, day, structregime.Forecast{Kind: structregime.KindTrend21,
		HorizonDays: 21, Regime: "uptrend", Conviction: 0.88, HistoricalAccuracy: 0.946, Tier: "high", Rank: 0.94, N: 900}))
	must(st.UpsertSymbolModel(ctx, store.SymbolModelRow{SymbolID: fx.sntl.ID, Horizon: "1d",
		Weights:     `{"pressure":0.4,"expectancy":0.3,"forecast":0.2,"sentiment":0.1}`,
		Skill:       `{"pressure":{"hitRate":0.55,"ic":0.03,"n":60,"hasHR":true,"hasIC":true}}`,
		Personality: "SNTL_PERSONALITY: a steady trend follower", NSamples: 60, Tier: "personal", UpdatedTs: day}))
	_, err = evidence.EnsureSeeds(ctx, st)
	must(err)
	fx.evidenceID = evidence.SeedClaims()[0].ID

	// Public-domain rows (EDGAR, FINRA, STOCK Act). The Form 4 price is a
	// filed public value and is deliberately NOT a sentinel: a member may see it.
	must(st.UpsertCompanies(ctx, []store.CompanyRow{{CIK: 990001, Ticker: "SNTL", Name: "Sentinel Corp",
		Exchange: "NYSE", SIC: "3571", SICDesc: "Electronic Computers"}}))
	must(st.UpsertFundamental(ctx, store.FundamentalRow{SymbolID: fx.sntl.ID, Metric: "SharesOutstanding",
		Value: sntShares, AsOf: day, FetchedAt: day}))
	_, err = st.InsertFiling(ctx, store.FilingRow{ID: "0000990001-26-000001", SymbolID: fx.sntl.ID, Form: "10-Q",
		FiledTs: day - 5*86400, Title: "SNTL quarterly report", URL: "https://www.sec.gov/sntl", Label: "quarterly report"})
	must(err)
	must(st.InsertInsiderTrade(ctx, store.InsiderTradeRow{Accession: "0000990001-26-000002", SymbolID: fx.sntl.ID,
		Insider: "Jane Sentinel", Title: "CEO", Code: "P", Shares: 1000, Price: 3331.13, Value: 3331130,
		TxTs: day - 6*86400, FiledTs: day - 5*86400}))
	must(st.UpsertInstHolding(ctx, store.InstHoldingRow{CIK: "990002", Manager: "Sentinel Capital",
		Period: "2026-06-30", SymbolID: &fx.sntl.ID, CUSIP: "SNTL00000", Name: "SENTINEL CORP", Value: 1.5e6, Shares: 2000}))
	must(st.UpsertShortVolume(ctx, []store.ShortVolumeRow{{SymbolID: fx.sntl.ID, Day: time.Unix(day, 0).UTC().Format("2006-01-02"),
		ShortVol: 2.5e6, ShortExempt: 1000, TotalVol: 6e6, ShortPct: 2.5e6 / 6e6}}))
	must(st.UpsertShortInterest(ctx, []store.ShortInterestRow{{SymbolID: fx.sntl.ID, Settlement: "2026-09-15",
		ShortQty: 120000, PrevQty: 100000, ADV: 50000, DaysToCover: 2.4, ChangePct: 20}}))
	_, err = st.InsertCongressTrade(ctx, store.CongressTradeRow{ID: "sntl-ct-1", Chamber: "house",
		Member: "Rep. Sentinel", Symbol: "SNTL", SymbolID: &fx.sntl.ID, TxType: "purchase",
		AmountRange: "$1,001 - $15,000", TxTs: day - 40*86400, DisclosedTs: day - 5*86400})
	must(err)
	must(st.UpsertDilutionFlag(ctx, store.DilutionFlagRow{SymbolID: fx.sntl.ID, Level: "elevated",
		Reasons: `["SNTL share count up 12% in a year"]`, UpdatedTs: day}))
	return fx
}

// regimeConviction is the seeded SNTL/SNTC regime conviction per variant.
var regimeConviction = []float64{0.93, 0.94}

// memberProbe is one request a member makes to one member-reachable route.
type memberProbe struct {
	method, url string // url is path plus query, any {id} filled in
	body        any    // POST body
	marker      string // must appear: proves the body came from the seeded store
	status      int    // non-zero: the expected non-2xx status, with why
	why         string
	// strip, when set, removes a part of the body the scan must not judge,
	// with the reason at the probe.
	strip func(t *testing.T, body string) string
}

// memberProbeExempt are member-reachable routes the scan does not probe, each
// with the reason. /mcp is not listed because the route scan does not see it
// (it is not under /api/ and is mounted only with SIGNALDECK_MCP_ENABLED); it
// ignores session cookies and needs an operator-issued key, so a member
// session cannot reach it.
var memberProbeExempt = map[string]string{
	"/api/auth/register": "account flow: answers with account state, never market data (accounts_test.go)",
	"/api/auth/login":    "account flow: answers with the session's identity (accounts_test.go)",
	"/api/auth/logout":   "account flow: ends the session; would sign the probing member out",
	"/api/auth/verify":   "account flow: redeems an emailed token (accounts_test.go)",
	"/api/auth/resend":   "account flow: answers the same fixed text whatever the address",
	"/api/auth/forgot":   "account flow: answers the same fixed text whatever the address",
	"/api/auth/reset":    "account flow: redeems an emailed token (accounts_test.go)",
	"/api/auth/google":   "account flow: verifies a Google ID token; off without a client ID",
	"/api/tv-webhook":    "inbound TradingView POST authenticated by a shared secret; serves nothing back",
	"/api/waitlist":      "takes an email and answers ok either way; serves nothing back",
}

// memberProbeFloor is the measured size of the probed member surface. It may
// only go up: a drop means routes left the scan.
const memberProbeFloor = 43

func memberProbes(fx sentinelFixture) map[string]memberProbe {
	get := func(url, marker string) memberProbe { return memberProbe{method: "GET", url: url, marker: marker} }
	watchBody := func(s string) map[string]string { return map[string]string{"symbol": s, "market": "stocks"} }
	n := 4 + fx.variant // resolved predictions and scores in the store
	return map[string]memberProbe{
		// RETIRED only because EnsureSeeds put a refuted claim in this store;
		// the registry alone reads INSUFFICIENT (accuracy_test.go).
		"/api/accuracy":    get("/api/accuracy", `"publication_status":"RETIRED"`),
		"/api/auth/me":     get("/api/auth/me", `"username":"mira"`),
		"/api/calibration": get("/api/calibration", fmt.Sprintf(`"N":%d`, n)),
		// No seedable row: built per request from d.St and uncached, so the
		// marker only has to prove the handler answered. Same below where noted.
		"/api/canary":           get("/api/canary", `"gates":{`),
		"/api/companies":        get("/api/companies?q=SNTL", "Sentinel Corp"),
		"/api/company/profile":  get("/api/company/profile?symbol=SNTL", "Sentinel Capital"),
		"/api/congress":         get("/api/congress", "Rep. Sentinel"),
		"/api/dataset-versions": get("/api/dataset-versions", `"revisedSymbols":0`), // per request, uncached
		"/api/dilution":         get("/api/dilution", "SNTL share count"),
		"/api/evidence":         get("/api/evidence", fx.evidenceID),
		"/api/evidence/{id}":    get("/api/evidence/"+fx.evidenceID, fx.evidenceID),
		"/api/filings":          get("/api/filings", "0000990001-26-000001"),
		"/api/fundamentals":     get("/api/fundamentals?symbol=SNTL", "SharesOutstanding"),
		"/api/health":           get("/api/health", `"openSignup":true`), // per request, uncached
		"/api/honesty":          {method: "GET", url: "/api/honesty", marker: fmt.Sprintf(`"independentN":%d`, n), strip: stripHonestyPoints},
		"/api/insiders":         get("/api/insiders", "Jane Sentinel"),
		"/api/institutions":     get("/api/institutions?symbol=SNTL", "Sentinel Capital"),
		"/api/ledger":           get("/api/ledger?symbol=SNTL&market=stocks", `"count":1`),
		"/api/ledger/anchors":   get("/api/ledger/anchors", `"mode":"stored"`), // per request, uncached
		"/api/ledger/range":     get("/api/ledger/range", `"featureHash":"sntl"`),
		"/api/ledger/verify":    get("/api/ledger/verify", `"count":1,"head"`),
		"/api/lineage":          get("/api/lineage?kind=claim&id="+fx.evidenceID, `"id":"`+fx.evidenceID+`"`),
		"/api/market-regimes":   get("/api/market-regimes", `"symbol":"SPY"`),            // the seeded basket call
		"/api/model-health":     get("/api/model-health", "1 forecasts are outstanding"), // the seeded regime call
		"/api/postmortems":      get("/api/postmortems", "SNTL"),
		"/api/prereg":           get("/api/prereg", `"chainVerified":true`), // per request, uncached
		"/api/quality":          get("/api/quality", "SNTL"),
		"/api/ready": {method: "GET", url: "/api/ready", marker: `"ready":false`, status: http.StatusServiceUnavailable,
			why: "a fixture daemon runs no workers, so it is not ready; the body is the anonymous summary"},
		"/api/regime-postmortems":  get("/api/regime-postmortems", "SNTL"),
		"/api/regimes":             get("/api/regimes", fmt.Sprintf(`"regime":"uptrend","conviction":%v`, regimeConviction[fx.variant])),
		"/api/research-ledger":     get("/api/research-ledger", `"state":"live"`), // keyed by d.St.CacheKey()
		"/api/research-loop":       get("/api/research-loop", `"corpusRows":0`),   // per request, uncached
		"/api/self-audit":          get("/api/self-audit", `"empty":true`),        // per request, uncached
		"/api/short-interest":      get("/api/short-interest?symbol=SNTL&market=stocks", "2026-09-15"),
		"/api/shorts":              get("/api/shorts", "SNTL"),
		"/api/symbol-agent":        get("/api/symbol-agent?symbol=SNTL&market=stocks", "SNTL_PERSONALITY"),
		"/api/track-record":        get("/api/track-record", fmt.Sprintf(`"cluster":{"n":%d`, n)),
		"/api/unwatch":             {method: "POST", url: "/api/unwatch", body: watchBody("SNTW"), marker: "SNTW"},
		"/api/version":             get("/api/version", `"version":"test"`),
		"/api/vol-forecast/record": get("/api/vol-forecast/record", `"evidence":"LIVE"`), // keyed by d.St.CacheKey()
		"/api/vol-regime":          get("/api/vol-regime", "SNTL"),
		"/api/watch":               {method: "POST", url: "/api/watch", body: watchBody("SNTL"), marker: "SNTL"},
		"/api/watchlist":           get("/api/watchlist", "SNTL"),
	}
}

// stripHonestyPoints removes /api/honesty's points[] before the scan. Each
// point is {score, fwd, ts}: a realized forward return between two vendor
// closes, but keyed to NO symbol. honestyPt serializes neither SymbolID nor
// SettleTs (json:"-"); ts is the scoring pass's minute, which every symbol
// scored in that pass shares (pipeline.go stamps one ts per pass); and the
// score is published per symbol on no member-reachable route (watchlist
// strips scores for members, the ledger carries calibrated probabilities, not
// scores). So nothing in a point joins back to a symbol, and by the licence
// line a value not keyed to a symbol is not RAW. The rest of the body (bucket
// means and counts) is still scanned.
func stripHonestyPoints(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `"points":[`)
	if i < 0 {
		t.Fatalf("/api/honesty carries no points[]; the exemption is stale: %.300s", body)
	}
	j := strings.Index(body[i:], "]")
	if j < 0 {
		t.Fatalf("/api/honesty points[] is not closed: %.300s", body)
	}
	return body[:i] + body[i+j+1:]
}

// memberAdmittedRoutes is every registered /api route the member gate lets a
// member session through, sorted.
func memberAdmittedRoutes(t *testing.T) []string {
	t.Helper()
	var out []string
	for p := range registeredAPIRoutes(t) {
		if memberAllowed(p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func TestMemberResponsesCarryNoVendorSentinels(t *testing.T) {
	// Members reach the same union under both published postures; the gate
	// differs (allowlist vs PublicReads) and both must hold.
	//
	// Each posture seeds its own store with a different variant, so a body the
	// package-level response caches kept from the first store fails a marker
	// in the second: the d.St.CacheKey() prefix on those caches is what keeps
	// this test from passing on another store's answer.
	for i, posture := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"public-surface", nil},
		{"tunnel", func(c *config.Config) { c.PublicSurface, c.PublicReads = false, false }},
	} {
		t.Run(posture.name, func(t *testing.T) { scanMemberSurface(t, posture.mutate, i) })
	}
}

func scanMemberSurface(t *testing.T, mutate func(*config.Config), variant int) {
	ctx := context.Background()
	srv, st, mb := newProductionServer(t, mutate, writeRegistry(t, thinWindowRegistry))
	freshHeartbeat(t, st)
	fx := seedSentinels(t, st, variant)
	sents := vendorSentinels()

	member := signupVerified(t, srv, mb, "mira", "mira@gmail.com")
	owner := newClient(t)
	if code, body := acctPost(t, owner, srv.URL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("owner login: %d %s", code, body)
	}
	uid := func(name string) int64 {
		u, _, err := st.GetUserByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	// The member watches SNTL, and holds an SNTC/USD row from before crypto
	// left the member product; the operator watches SNTL.
	for _, id := range []int64{fx.sntl.ID, fx.sntc.ID} {
		if err := st.AddMemberSymbol(ctx, uid("mira"), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddUserSymbol(ctx, uid("owner"), fx.sntl.ID); err != nil {
		t.Fatal(err)
	}

	// POSITIVE CONTROL. The same scanner over the operator's view of the same
	// store must find the sentinels; otherwise the seed is not reaching the
	// handlers (or the scanner is blind) and a clean member scan means nothing.
	for _, pc := range []struct{ url, want string }{
		{"/api/postmortems", "realized forward return"},
		{"/api/watchlist", "vendor bar OHLC"},
		{"/api/companies?q=SNTL", "vendor bar OHLC"},
		{"/api/quality", "provider error text"},
	} {
		code, body := getAs(t, owner, srv.URL+pc.url)
		found := false
		for _, l := range leaks(body, sents) {
			found = found || strings.HasPrefix(l, pc.want)
		}
		if code != 200 || !found {
			t.Fatalf("positive control: the operator's %s (%d) carries no %q sentinel, so the seed does not "+
				"reach the handlers or the scanner is blind: %.400s", pc.url, code, pc.want, body)
		}
	}

	probes := memberProbes(fx)
	admitted := memberAdmittedRoutes(t)
	exercised, exempt := 0, 0
	for _, path := range admitted {
		if _, ok := memberProbeExempt[path]; ok {
			exempt++
			continue
		}
		pr, ok := probes[path]
		if !ok {
			t.Errorf("%s is reachable by a member and has no probe: add one to memberProbes "+
				"(or an entry with its reason to memberProbeExempt)", path)
			continue
		}
		var code int
		var body string
		if pr.method == "POST" {
			resp := postJSON(t, member, srv.URL+pr.url, pr.body)
			code, body = resp.StatusCode, drain(t, resp)
		} else {
			code, body = getAs(t, member, srv.URL+pr.url)
		}
		exercised++
		if pr.status != 0 {
			if code != pr.status {
				t.Errorf("member %s %s: %d, want %d (%s): %.300s", pr.method, pr.url, code, pr.status, pr.why, body)
				continue
			}
		} else if code < 200 || code > 299 {
			t.Errorf("member %s %s: %d, want 2xx (a refusal body holds no sentinel, so it proves nothing): %.300s",
				pr.method, pr.url, code, body)
			continue
		}
		if !strings.Contains(body, pr.marker) {
			t.Errorf("member %s: marker %q missing, so the body may not come from the seeded store: %.600s",
				pr.url, pr.marker, body)
		}
		scanned := body
		if pr.strip != nil {
			scanned = pr.strip(t, body)
		}
		for _, l := range leaks(scanned, sents) {
			t.Errorf("member %s leaks a vendor value (%s); see the licence line in datalicense.go", pr.url, l)
		}
		// Crypto is not part of the member product (refuseMemberCrypto). The
		// public proof record (publicRoutes) is exempt and stays complete.
		if memberRoutes[path] && strings.Contains(body, "SNTC") {
			t.Errorf("member %s serves a crypto row (SNTC): %.400s", pr.url, body)
		}
	}
	reachable := map[string]bool{}
	for _, p := range admitted {
		reachable[p] = true
	}
	for path := range probes {
		if !reachable[path] {
			t.Errorf("memberProbes names %s, which a member cannot reach: stale probe", path)
		}
	}
	for path := range memberProbeExempt {
		if !reachable[path] {
			t.Errorf("memberProbeExempt names %s, which a member cannot reach: stale exemption", path)
		}
	}
	if exercised != len(admitted)-exempt || exercised < memberProbeFloor {
		t.Errorf("probed %d of %d member-reachable routes (%d exempt, floor %d)",
			exercised, len(admitted), exempt, memberProbeFloor)
	}

	// Single-symbol crypto lookups on member routes: refused for a member,
	// unchanged for the operator.
	for _, c := range []struct {
		url  string
		body any
		want int
	}{
		{"/api/watch", map[string]string{"symbol": "SNTC/USD", "market": "crypto"}, 400},
		{"/api/symbol-agent?symbol=SNTC/USD&market=crypto", nil, 404},
		{"/api/short-interest?symbol=SNTC/USD&market=crypto", nil, 404},
		{"/api/company/profile?symbol=SNTC", nil, 404},
	} {
		var code int
		var body string
		if c.body != nil {
			resp := postJSON(t, member, srv.URL+c.url, c.body)
			code, body = resp.StatusCode, drain(t, resp)
		} else {
			code, body = getAs(t, member, srv.URL+c.url)
		}
		if code != c.want || !strings.Contains(body, cryptoNotForMembers) {
			t.Errorf("member crypto %s: %d %s, want %d %q", c.url, code, body, c.want, cryptoNotForMembers)
		}
	}
	for _, u := range []string{"/api/symbol-agent?symbol=SNTC/USD&market=crypto", "/api/regimes", "/api/vol-regime"} {
		if code, body := getAs(t, owner, srv.URL+u); code != 200 || !strings.Contains(body, "SNTC") {
			t.Errorf("operator %s lost its crypto rows: %d %.300s", u, code, body)
		}
	}
}
