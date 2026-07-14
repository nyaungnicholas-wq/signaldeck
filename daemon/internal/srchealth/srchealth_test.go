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

	// A crypto pair (24/7 source) with a 3h-old perp snapshot: budget 2h ⇒ stale.
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err := st.InsertCryptoPerp(ctx, store.CryptoPerpRow{SymbolID: btc.ID, Ts: openNow.Unix() - 3*3600, MarkPx: 1}); err != nil {
		t.Fatal(err)
	}
	// A 1s snapshot 1min old: budget 10m ⇒ fresh (ungated, always checked).
	if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: btc.ID, Ts: openNow.Unix() - 60, Mid: 1}); err != nil {
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

func TestEvaluateMarketOpenClassifies(t *testing.T) {
	st := fixtureStore(t)
	reports, err := srchealth.Evaluate(context.Background(), st, openNow, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := bySource(t, reports, "crypto_perp"); !got.Stale {
		t.Errorf("crypto_perp 3h old (budget 2h) should be stale: %+v", got)
	}
	if got := bySource(t, reports, "snapshots_1s"); got.Stale {
		t.Errorf("snapshots_1s 1min old should be fresh: %+v", got)
	}
	if got := bySource(t, reports, "bars_1m_hot"); !got.Stale {
		t.Errorf("bars_1m_hot 2h old (budget 30m, market open) should be stale: %+v", got)
	}
	// No rows at all + gated source + market open ⇒ stale, honestly labeled.
	if got := bySource(t, reports, "tv_quotes"); !got.Stale || !strings.Contains(got.Note, "no rows yet") {
		t.Errorf("tv_quotes (no rows, market open) should be stale w/ note: %+v", got)
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
	// Crypto / 24-7 sources are still checked when the stock market is closed.
	if got := bySource(t, reports, "crypto_perp"); !got.Stale {
		t.Errorf("crypto_perp must still be checked when market closed: %+v", got)
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
