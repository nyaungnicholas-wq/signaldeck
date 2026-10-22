package srchealth_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/srchealth"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// openNow is a normal NYSE trading session (Mon 2026-07-13, 15:00 ET);
// closedNow is a weekend (Sat 2026-07-18, 12:00 ET) — both well after openNow so
// rows inserted relative to openNow are strictly older at closedNow too.
var (
	openNow   = time.Date(2026, 7, 13, 15, 0, 0, 0, marketcal.Loc())
	closedNow = time.Date(2026, 7, 18, 12, 0, 0, 0, marketcal.Loc())
)

func fixtureStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "srch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	// Crypto sources are PAUSED (2026-10-21): a 3h-old perp snapshot (budget 2h)
	// would be stale if judged, so it proves the pause suppresses the check.
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err := st.InsertCryptoPerp(ctx, store.CryptoPerpRow{SymbolID: btc.ID, Ts: openNow.Unix() - 3*3600, MarkPx: 1}); err != nil {
		t.Fatal(err)
	}
	// An ungated, always-checked source: Wikipedia views 5 days old (budget 3d) ⇒
	// stale whether or not the stock market is open.
	aaplWiki, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err := st.UpsertWikiViews(ctx, []store.WikiViewRow{{
		SymbolID: aaplWiki.ID, Day: openNow.UTC().AddDate(0, 0, -5).Format("2006-01-02"), Views: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	// A streamed stock with a 2h-old 1m bar: budget 30m, gated ⇒ stale only when
	// the market is open.
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err := st.SetSymbolStream(ctx, aapl.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: aapl.ID, TF: md.TF1m, Ts: openNow.Unix() - 2*3600, Close: 1}}); err != nil {
		t.Fatal(err)
	}
	// A webhook signal 8h old: budget 6h ⇒ stale only when open AND a secret is set.
	if _, err := st.InsertTVSignal(ctx, store.TVSignal{Ticker: "NASDAQ:AAPL", Action: "buy", Ts: openNow.Unix() - 8*3600}); err != nil {
		t.Fatal(err)
	}
	return st
}

func bySource(t *testing.T, reports []srchealth.Report, src string) srchealth.Report {
	t.Helper()
	for _, r := range reports {
		if r.Source == src {
			return r
		}
	}
	t.Fatalf("source %q not in report", src)
	return srchealth.Report{}
}

// TestLicenceRetiredSourcesArePaused: FINRA (Reg SHO volume, short interest),
// Cboe put/call and FRED were retired on 22 Oct 2026 because their terms do not
// permit this use. Their tables stop growing by design, so each must be reported
// as paused and never judged stale, however old its newest row. This replaced
// the SD-H35 short_volume budget regression: the budget is kept in the registry
// for a resumption, but nothing judges it while the source is retired.
func TestLicenceRetiredSourcesArePaused(t *testing.T) {
	st := fixtureStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	old := openNow.UTC().AddDate(0, -3, 0) // far past every budget
	day := old.Format("2006-01-02")
	if err := st.UpsertShortVolume(ctx, []store.ShortVolumeRow{{SymbolID: sym.ID, Day: day, ShortVol: 1, TotalVol: 2, ShortPct: 0.5}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertShortInterest(ctx, []store.ShortInterestRow{{SymbolID: sym.ID, Settlement: day, ShortQty: 1, DaysToCover: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCboePC(ctx, store.CboePCRow{Day: day, TotalPC: 0.9}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMacro(ctx, "VIXCLS", old.Unix(), 20); err != nil {
		t.Fatal(err)
	}
	reports, err := srchealth.Evaluate(ctx, st, openNow, false)
	if err != nil {
		t.Fatal(err)
	}
	const want = "paused: retired 22 Oct 2026: licence terms do not permit this use — not checked"
	for _, src := range []string{"short_volume", "short_interest", "cboe_pc", "macro_series"} {
		if got := bySource(t, reports, src); got.Stale || got.Note != want || got.LastTs == 0 {
			t.Errorf("%s must be reported (with its last row) as paused, never stale: %+v", src, got)
		}
	}
}

func TestEvaluateMarketOpenClassifies(t *testing.T) {
	st := fixtureStore(t)
	reports, err := srchealth.Evaluate(context.Background(), st, openNow, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(t, reports, "wiki_views"); !got.Stale {
		t.Errorf("wiki_views 5d old (budget 3d) should be stale: %+v", got)
	}
	// Paused sources are reported but never judged stale, even when they would be.
	for _, src := range []string{"crypto_perp", "snapshots_1s", "tv_quotes", "tv_ratings", "stocktwits_sentiment"} {
		if got := bySource(t, reports, src); got.Stale || !strings.Contains(got.Note, "paused:") {
			t.Errorf("%s is paused and must not be judged: %+v", src, got)
		}
	}
	if got := bySource(t, reports, "bars_1m_hot"); !got.Stale {
		t.Errorf("bars_1m_hot 2h old (budget 30m, market open) should be stale: %+v", got)
	}
	// No rows at all + gated source + market open ⇒ stale, honestly labeled.
	if got := bySource(t, reports, "news"); !got.Stale || !strings.Contains(got.Note, "no rows yet") {
		t.Errorf("news (no rows, market open) should be stale w/ note: %+v", got)
	}
	// Webhook with no secret configured is not checked.
	if got := bySource(t, reports, "tv_signals"); got.Stale || !strings.Contains(got.Note, "webhook disabled") {
		t.Errorf("tv_signals w/o secret must not be checked: %+v", got)
	}
}

func TestEvaluateMarketClosedSuppressesStockStaleness(t *testing.T) {
	st := fixtureStore(t)
	reports, err := srchealth.Evaluate(context.Background(), st, closedNow, true)
	if err != nil {
		t.Fatal(err)
	}
	// Gated stock sources are "market closed", never stale, over the weekend.
	if got := bySource(t, reports, "bars_1m_hot"); got.Stale || !strings.Contains(got.Note, "market closed") {
		t.Errorf("bars_1m_hot should be suppressed when market closed: %+v", got)
	}
	if got := bySource(t, reports, "tv_signals"); got.Stale || !strings.Contains(got.Note, "market closed") {
		t.Errorf("tv_signals should be suppressed when market closed even w/ secret: %+v", got)
	}
	// Ungated sources are still checked when the stock market is closed.
	if got := bySource(t, reports, "wiki_views"); !got.Stale {
		t.Errorf("wiki_views must still be checked when market closed: %+v", got)
	}
}

// TestWebhookSilentFailure: secret configured + market open + no recent signal ⇒
// the tv_signals source is flagged, catching a silently-dead tunnel/webhook.
func TestWebhookSilentFailure(t *testing.T) {
	st := fixtureStore(t)
	reports, err := srchealth.Evaluate(context.Background(), st, openNow, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(t, reports, "tv_signals"); !got.Stale {
		t.Errorf("tv_signals 8h stale w/ secret + market open should flag: %+v", got)
	}
	if srchealth.StaleCount(reports) == 0 {
		t.Error("expected a nonzero stale count")
	}
}
