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

// TestShortVolumeBudgetBracketsRealCadence is the SD-H35 regression. FINRA's
// DAILY short-sale volume feed died on 2026-08-02 and /api/source-health went
// on reporting every source fresh — short_volume was not in the registry at
// all, so the auditor never asked. The bi-monthly short_interest source cannot
// stand in for it: its legitimate age reaches ~28d (settlement every ~15d plus
// FINRA's ~9-business-day publication lag), so no honest budget there flags a
// week-old outage in a feed that publishes every session.
//
// Both directions are asserted, because a staleness budget is only specified
// once something pins it from BELOW as well as above — a budget that catches
// every outage and also fires on every holiday weekend is not a working check.
func TestShortVolumeBudgetBracketsRealCadence(t *testing.T) {
	cases := []struct {
		name      string
		newestDay string
		now       time.Time
		wantStale bool
	}{{
		// Lower bound. Thu 2026-07-02 was the last session before the July 4
		// holiday (Fri 07-03 closed), trading resumed Mon 07-06. This exact
		// 4-calendar-day gap is present in the live table, so it is a real
		// cadence, not a hypothetical — flagging it would be a false alarm.
		name: "holiday weekend gap is not staleness", newestDay: "2026-07-02",
		now: time.Date(2026, 7, 6, 15, 0, 0, 0, marketcal.Loc()), wantStale: false,
	}, {
		// Upper bound: SD-H35 exactly as it happened. The finra-shorts worker
		// last ran 2026-08-02, leaving MAX(day)=2026-07-31 while four sessions
		// (Aug 3-6) came and went.
		name: "dead FINRA daily feed is caught", newestDay: "2026-07-31",
		now: time.Date(2026, 8, 6, 15, 0, 0, 0, marketcal.Loc()), wantStale: true,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// short_volume is market-gated, so a wrong assumption about the
			// calendar would suppress the check and let the "fresh" case pass
			// for entirely the wrong reason. Assert the window explicitly.
			if !marketcal.OpenForBars(tc.now) {
				t.Fatalf("fixture bug: %s is not an open session", tc.now)
			}
			st, err := store.Open(filepath.Join(t.TempDir(), "sv.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close() //nolint:errcheck
			ctx := context.Background()
			sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertShortVolume(ctx, []store.ShortVolumeRow{{
				SymbolID: sym.ID, Day: tc.newestDay, ShortVol: 1, TotalVol: 2, ShortPct: 0.5,
			}}); err != nil {
				t.Fatal(err)
			}

			reports, err := srchealth.Evaluate(ctx, st, tc.now, false)
			if err != nil {
				t.Fatal(err)
			}
			got := bySource(t, reports, "short_volume")
			if got.Stale != tc.wantStale {
				t.Errorf("newest day %s judged at %s: stale = %v, want %v\n  age %ds vs budget %ds — %s",
					tc.newestDay, tc.now.Format(time.RFC3339), got.Stale, tc.wantStale,
					got.AgeSecs, got.StaleBudgetSecs, got.Note)
			}
		})
	}
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
