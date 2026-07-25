package pipeline

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedPredictable gives one symbol the minimum state (daily bars + a score
// per horizon) for the PredictionRunner to emit a prediction + feature vector
// — the same scaffold newstrends_test uses.
func seedPredictable(t *testing.T, st *store.Store, symbol string, market md.Market) md.Symbol {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, symbol, market, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	var bars []md.Bar
	for i := int64(0); i < 30; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
			Open: 100, High: 101, Low: 99, Close: 100 + float64(i), Volume: 1,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for _, h := range predHorizons {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: h, Ts: now - 60, Score: 0.3,
			Components: []md.ScoreComponent{{Name: "rsi", Contrib: 0.3}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return sym
}

// FEATURE-VECTOR integration (featureVersion 5): fresh, gate-clearing
// new-source data must ride the persisted vector as short_int_dtc,
// stocktwits_bull_ratio, wiki_z, tv_reco, pc_total and cot_spx_net.
func TestAlphaFeaturesJoinFeatureVector(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym := seedPredictable(t, st, "AAPL", md.Stocks)
	now := time.Now()
	today := now.UTC().Format("2006-01-02")

	// FINRA short interest, settlement 5 days old (inside the 20d bound).
	if err := st.UpsertShortInterest(ctx, []store.ShortInterestRow{{
		SymbolID: sym.ID, Settlement: now.UTC().AddDate(0, 0, -5).Format("2006-01-02"),
		ShortQty: 1e6, PrevQty: 9e5, ADV: 3e5, DaysToCover: 3.5, ChangePct: 11,
	}}); err != nil {
		t.Fatal(err)
	}
	// StockTwits snapshot: fresh, total 20 >= 10 → ratio 12/(12+4) = 0.75.
	if err := st.InsertStocktwits(ctx, store.StocktwitsRow{
		SymbolID: sym.ID, Ts: now.Unix() - 600, Bullish: 12, Bearish: 4, Untagged: 4, Total: 20,
	}); err != nil {
		t.Fatal(err)
	}
	// Wikipedia views: 11 varying prior days + a spiking latest day (today).
	var wv []store.WikiViewRow
	for i := 11; i >= 1; i-- {
		wv = append(wv, store.WikiViewRow{
			SymbolID: sym.ID, Day: now.UTC().AddDate(0, 0, -i).Format("2006-01-02"),
			Views: int64(100 + i),
		})
	}
	wv = append(wv, store.WikiViewRow{SymbolID: sym.ID, Day: today, Views: 500})
	if err := st.UpsertWikiViews(ctx, wv); err != nil {
		t.Fatal(err)
	}
	// TradingView rating: fresh.
	if err := st.UpsertTVRating(ctx, store.TVRatingRow{
		SymbolID: sym.ID, Ts: now.Unix() - 600, RecoAll: 0.4, Label: "buy",
	}); err != nil {
		t.Fatal(err)
	}
	// Market-wide: CBOE put/call (today) + COT E-mini S&P 500 (3 days old).
	if err := st.UpsertCboePC(ctx, store.CboePCRow{Day: today, TotalPC: 0.91}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCOT(ctx, []store.COTRow{{
		Contract:    "E-MINI S&P 500 - CHICAGO MERCANTILE EXCHANGE",
		ReportDate:  now.UTC().AddDate(0, 0, -3).Format("2006-01-02"),
		NoncommLong: 100, NoncommShort: 40, OpenInterest: 200,
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	feats, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil || len(feats) != 1 {
		t.Fatalf("features: %v %v", feats, err)
	}
	// (v5 introduced these fields; the alphax-leg wave bumped the vector to
	// v6 — the new-source fields keep riding the CURRENT version.)
	if feats[0].Version != featureVersion {
		t.Fatalf("new-source rows must be stamped v%d, got v%d", featureVersion, feats[0].Version)
	}
	vec := feats[0].Vec
	for k, want := range map[string]float64{
		"short_int_dtc":         3.5,
		"stocktwits_bull_ratio": 0.75,
		"tv_reco":               0.4,
		"pc_total":              0.91,
		"cot_spx_net":           (100.0 - 40.0) / 200.0,
	} {
		if got := vec[k]; got != want {
			t.Fatalf("vec[%q] = %v, want %v (vec %+v)", k, got, want, vec)
		}
	}
	if z, ok := vec["wiki_z"]; !ok || z <= 0 {
		t.Fatalf("wiki_z should be present and positive for a view spike, got %v (present=%v)", z, ok)
	}
}

// GATES: stale or thin sources must leave every field ABSENT — absence is
// information, not zero.
func TestAlphaFeaturesGatesLeaveFieldsAbsent(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym := seedPredictable(t, st, "MSFT", md.Stocks)
	now := time.Now()

	// Everything present but STALE or THIN:
	if err := st.UpsertShortInterest(ctx, []store.ShortInterestRow{{
		SymbolID: sym.ID, Settlement: now.UTC().AddDate(0, 0, -30).Format("2006-01-02"),
		DaysToCover: 3.5,
	}}); err != nil { // 30d > 20d bound
		t.Fatal(err)
	}
	if err := st.InsertStocktwits(ctx, store.StocktwitsRow{
		SymbolID: sym.ID, Ts: now.Unix() - 600, Bullish: 3, Bearish: 1, Total: 5,
	}); err != nil { // total 5 < 10
		t.Fatal(err)
	}
	var wv []store.WikiViewRow
	for i := 4; i >= 0; i-- { // only 4 prior days < the 10-prior gate
		wv = append(wv, store.WikiViewRow{
			SymbolID: sym.ID, Day: now.UTC().AddDate(0, 0, -i).Format("2006-01-02"),
			Views: int64(100 + i*3),
		})
	}
	if err := st.UpsertWikiViews(ctx, wv); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertTVRating(ctx, store.TVRatingRow{
		SymbolID: sym.ID, Ts: now.Unix() - 3*86400, RecoAll: 0.4,
	}); err != nil { // 3d > 1d bound
		t.Fatal(err)
	}
	if err := st.UpsertCboePC(ctx, store.CboePCRow{
		Day: now.UTC().AddDate(0, 0, -10).Format("2006-01-02"), TotalPC: 0.91,
	}); err != nil { // 10d > 3d bound
		t.Fatal(err)
	}
	if err := st.UpsertCOT(ctx, []store.COTRow{{
		Contract:    "E-MINI S&P 500 - CHICAGO MERCANTILE EXCHANGE",
		ReportDate:  now.UTC().AddDate(0, 0, -60).Format("2006-01-02"),
		NoncommLong: 100, NoncommShort: 40, OpenInterest: 200,
	}}); err != nil { // 60d > 21d bound
		t.Fatal(err)
	}

	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	feats, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil || len(feats) != 1 {
		t.Fatalf("features: %v %v", feats, err)
	}
	for _, k := range []string{
		"short_int_dtc", "stocktwits_bull_ratio", "wiki_z", "tv_reco",
		"pc_total", "cot_spx_net", "funding_rate",
	} {
		if v, ok := feats[0].Vec[k]; ok {
			t.Fatalf("gated/stale source must be ABSENT, but vec[%q]=%v", k, v)
		}
	}
}

// Crypto: a fresh perp snapshot joins as funding_rate (crypto symbols only);
// stock-only fields stay absent.
func TestAlphaFeaturesCryptoFunding(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym := seedPredictable(t, st, "BTC/USD", md.Crypto)
	now := time.Now()

	if err := st.InsertCryptoPerp(ctx, store.CryptoPerpRow{
		SymbolID: sym.ID, Ts: now.Unix() - 900, Funding: 0.0001, OpenInterest: 5e8, MarkPx: 60000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	feats, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil || len(feats) != 1 {
		t.Fatalf("features: %v %v", feats, err)
	}
	if got := feats[0].Vec["funding_rate"]; got != 0.0001 {
		t.Fatalf("funding_rate = %v, want 0.0001 (vec %+v)", got, feats[0].Vec)
	}
	if _, ok := feats[0].Vec["short_int_dtc"]; ok {
		t.Fatal("short_int_dtc must never appear on a crypto symbol")
	}
}
