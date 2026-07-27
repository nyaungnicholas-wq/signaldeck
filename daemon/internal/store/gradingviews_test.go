// Tests for the grading/aggregation reads that decide what the platform says
// about itself: the published-vs-raw resolved pair views (the C3 coordinate
// split), the prequential-majority benchmark, the directional record and its
// dashboard aggregates, regime/ranking lookups, feature drift windows, the
// model-evolution series, and the retention prune paths.
package store

import (
	"context"
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// seedResolvedPred writes one prediction (raw != cal on purpose) and resolves it.
func seedResolvedPred(t *testing.T, st *Store, symID int64, h md.Horizon, ts int64, raw, cal, fwd float64) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: symID, Horizon: h, Ts: ts, RawProb: raw, CalProb: cal,
		NUsed: 10, Components: "{}"}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}
	if err := st.ResolvePrediction(ctx, symID, h, ts, fwd); err != nil {
		t.Fatalf("resolve prediction: %v", err)
	}
}

func TestResolvedPairs_PublishedVsRawViews(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	seedResolvedPred(t, st, sym.ID, md.H1d, 1000, 0.55, 0.62, 0.01)
	seedResolvedPred(t, st, sym.ID, md.H1d, 2000, 0.48, 0.51, -0.02)
	// Unresolved row: excluded from both views.
	if err := st.UpsertPrediction(ctx, Prediction{SymbolID: sym.ID, Horizon: md.H1d,
		Ts: 3000, RawProb: 0.7, CalProb: 0.75, NUsed: 10, Components: "{}"}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}

	probs, ups, err := st.ResolvedPredictionPairs(ctx, md.H1d, 10)
	if err != nil || len(probs) != 2 || len(ups) != 2 {
		t.Fatalf("published pairs = %d/%d, %v; want 2/2", len(probs), len(ups), err)
	}
	// Newest first, and the PUBLISHED (calibrated) probability.
	if probs[0] != 0.51 || ups[0] != 0 || probs[1] != 0.62 || ups[1] != 1 {
		t.Fatalf("published view wrong: probs=%v ups=%v", probs, ups)
	}

	raws, ups2, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 10)
	if err != nil || len(raws) != 2 {
		t.Fatalf("raw pairs = %d, %v; want 2", len(raws), err)
	}
	// Same outcomes, but the RAW blend probability — never the map's output.
	if raws[0] != 0.48 || ups2[0] != 0 || raws[1] != 0.55 || ups2[1] != 1 {
		t.Fatalf("raw view wrong: raws=%v ups=%v", raws, ups2)
	}
}

func TestBenchmarkSeedAndPrequentialMajority(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")

	// Empty record: honest coin flip.
	if p, err := st.PrequentialMajorityProb(ctx, md.H1d, 1000, 0); err != nil || p != 0.5 {
		t.Fatalf("empty majority = %v, %v; want 0.5", p, err)
	}

	day := int64(86400)
	// Day 100: A up. Day 101: A up, B down.
	seedResolvedPred(t, st, a.ID, md.H1d, 100*day+60, 0.6, 0.6, 0.01)
	seedResolvedPred(t, st, a.ID, md.H1d, 101*day+60, 0.6, 0.6, 0.02)
	seedResolvedPred(t, st, b.ID, md.H1d, 101*day+60, 0.6, 0.6, -0.02)

	// Majority-up record → committed constant 1.
	if p, err := st.PrequentialMajorityProb(ctx, md.H1d, 102, 0); err != nil || p != 1 {
		t.Fatalf("majority-up = %v, %v; want 1", p, err)
	}
	// beforeDay is exclusive: only day 100 (1 up, 0 down) → still 1; and the
	// evidence window floor can exclude everything → 0.5 again.
	if p, _ := st.PrequentialMajorityProb(ctx, md.H1d, 101, 0); p != 1 {
		t.Fatalf("beforeDay slice = %v; want 1", p)
	}
	if p, _ := st.PrequentialMajorityProb(ctx, md.H1d, 102, 200*day); p != 0.5 {
		t.Fatalf("sinceTs floor = %v; want 0.5", p)
	}

	// A namespaced benchmark horizon stays out of the real horizon's record.
	bh := md.Horizon("1d#pm")
	if err := st.SeedBenchmarkOutcome(ctx, a.ID, bh, 100*day+60, 0.5); err != nil {
		t.Fatalf("seed benchmark: %v", err)
	}
	// Idempotent re-seed.
	if err := st.SeedBenchmarkOutcome(ctx, a.ID, bh, 100*day+60, 0.9); err != nil {
		t.Fatalf("re-seed benchmark: %v", err)
	}
	if err := st.ResolvePrediction(ctx, a.ID, bh, 100*day+60, -0.01); err != nil {
		t.Fatalf("resolve benchmark: %v", err)
	}
	// The benchmark row grades under its own horizon (1 down → 0)…
	if p, _ := st.PrequentialMajorityProb(ctx, bh, 102, 0); p != 0 {
		t.Fatalf("benchmark horizon majority = %v; want 0", p)
	}
	// …and the real horizon's majority is untouched by it.
	if p, _ := st.PrequentialMajorityProb(ctx, md.H1d, 102, 0); p != 1 {
		t.Fatalf("benchmark leaked into real horizon: %v", p)
	}
}

func TestDirectionalRecord_IndependentDaysAndBaseline(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Before anything resolves: honest absence.
	if _, ok, err := st.FirstResolutionAt(ctx, md.H1d); err != nil || ok {
		t.Fatalf("FirstResolutionAt empty = ok=%v, %v; want false", ok, err)
	}
	if r, err := st.DirectionalRecord(ctx, md.H1d, 0); err != nil || r.N != 0 {
		t.Fatalf("empty record = %+v, %v; want N=0", r, err)
	}

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	day := int64(86400)
	// A day100 correct-up; A day101 has TWO rows resolving the same move —
	// only the latest (correct) may count; B day101 wrong-up.
	seedResolvedPred(t, st, a.ID, md.H1d, 100*day+60, 0.6, 0.6, 0.01)
	seedResolvedPred(t, st, a.ID, md.H1d, 101*day+60, 0.2, 0.2, 0.02)  // early, wrong
	seedResolvedPred(t, st, a.ID, md.H1d, 101*day+120, 0.7, 0.7, 0.02) // latest, right
	seedResolvedPred(t, st, b.ID, md.H1d, 101*day+60, 0.6, 0.6, -0.02) // wrong

	ts, ok, err := st.FirstResolutionAt(ctx, md.H1d)
	if err != nil || !ok || ts == 0 {
		t.Fatalf("FirstResolutionAt = %d, %v, %v; want ok", ts, ok, err)
	}

	r, err := st.DirectionalRecord(ctx, md.H1d, 0)
	if err != nil {
		t.Fatalf("DirectionalRecord: %v", err)
	}
	if r.N != 3 {
		t.Fatalf("pseudo-replicated N: got %d, want 3 independent symbol-days", r.N)
	}
	if math.Abs(r.Accuracy-2.0/3.0) > 1e-9 || math.Abs(r.UpRate-2.0/3.0) > 1e-9 {
		t.Fatalf("accuracy/upRate = %v/%v; want 2/3", r.Accuracy, r.UpRate)
	}
	// Baseline is the best CONSTANT guess (majority class), never 50%.
	if math.Abs(r.BaselineAcc-2.0/3.0) > 1e-9 {
		t.Fatalf("baseline = %v; want 2/3", r.BaselineAcc)
	}
	if len(r.Days) != 2 {
		t.Fatalf("day clusters = %d; want 2", len(r.Days))
	}

	// The dashboard aggregate applies the same dedup discipline.
	n, win, err := st.LiveDirectionalRecord(ctx, md.H1d)
	if err != nil || n != 3 || math.Abs(win-2.0/3.0) > 1e-9 {
		t.Fatalf("LiveDirectionalRecord = %d, %v, %v; want 3, 2/3", n, win, err)
	}
}

func TestRegimeLookupsAndRankingPercentiles(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")

	if err := st.UpsertRegime(ctx, a.ID, 1000, "uptrend", 0.8, ""); err != nil {
		t.Fatalf("upsert regime: %v", err)
	}
	if err := st.UpsertRegime(ctx, b.ID, 1000, "range", 0.3, ""); err != nil {
		t.Fatalf("upsert regime: %v", err)
	}
	labels, err := st.RegimeLabels(ctx)
	if err != nil || len(labels) != 2 || labels[a.ID] != "uptrend" || labels[b.ID] != "range" {
		t.Fatalf("RegimeLabels = %v, %v", labels, err)
	}

	type rrow = struct {
		SymbolID     int64
		Score        float64
		Rank         int
		Ret1M, Ret3M float64
	}
	// Older snapshot, then the latest — percentiles must read ONLY the newest.
	if err := st.ReplaceRanking(ctx, 1000, []rrow{
		{SymbolID: a.ID, Score: 10, Rank: 1, Ret1M: 0.1, Ret3M: 0.2},
	}); err != nil {
		t.Fatalf("replace ranking: %v", err)
	}
	if err := st.ReplaceRanking(ctx, 2000, []rrow{
		{SymbolID: a.ID, Score: 88, Rank: 1, Ret1M: 0.1, Ret3M: 0.2},
		{SymbolID: b.ID, Score: 42, Rank: 2, Ret1M: 0.0, Ret3M: 0.1},
	}); err != nil {
		t.Fatalf("replace ranking 2: %v", err)
	}
	pct, err := st.RankingPercentiles(ctx)
	if err != nil || len(pct) != 2 || pct[a.ID] != 88 || pct[b.ID] != 42 {
		t.Fatalf("RankingPercentiles = %v, %v", pct, err)
	}
}

func TestFeatureWindowsAndLatestVersion(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if v, err := st.LatestFeatureVersion(ctx); err != nil || v != 0 {
		t.Fatalf("LatestFeatureVersion empty = %d, %v; want 0", v, err)
	}

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("insert features: %v", err)
		}
	}
	// Reference window [100,150), live window [200,250) — version 4 only.
	must(st.InsertFeatures(ctx, sym.ID, md.H1d, 110, 4, map[string]float64{"momo": 1, "vol": 5}))
	must(st.InsertFeatures(ctx, sym.ID, md.H1d, 120, 4, map[string]float64{"momo": 2, "vol": 6}))
	must(st.InsertFeatures(ctx, sym.ID, md.H1d, 210, 4, map[string]float64{"momo": 3}))
	// A different schema version inside the window must NOT be read as drift.
	must(st.InsertFeatures(ctx, sym.ID, md.H1d, 115, 5, map[string]float64{"momo": 99}))

	ref, live, err := st.FeatureWindows(ctx, 4, 100, 150, 200, 250, 0)
	if err != nil {
		t.Fatalf("FeatureWindows: %v", err)
	}
	if len(ref["momo"]) != 2 || len(ref["vol"]) != 2 || len(live["momo"]) != 1 {
		t.Fatalf("window shapes wrong: ref=%v live=%v", ref, live)
	}
	for _, v := range ref["momo"] {
		if v == 99 {
			t.Fatal("cross-version row leaked into the reference window")
		}
	}
	if v, err := st.LatestFeatureVersion(ctx); err != nil || v != 5 {
		t.Fatalf("LatestFeatureVersion = %d, %v; want 5", v, err)
	}
}

func TestRegimeBreadthAggregatesForecasts(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Empty table: all zeros, no error.
	up, tn, el, vn, err := st.RegimeBreadth(ctx)
	if err != nil || up != 0 || tn != 0 || el != 0 || vn != 0 {
		t.Fatalf("empty breadth = %d/%d/%d/%d, %v", up, tn, el, vn, err)
	}

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	put := func(symID int64, kind, regime string) {
		t.Helper()
		if err := st.UpsertRegimeForecast(ctx, symID, 1000, structregime.Forecast{
			Kind: structregime.Kind(kind), HorizonDays: 21, Regime: regime,
			Conviction: 0.7, HistoricalAccuracy: 0.6, Tier: "high", Rank: 0.9, N: 50,
		}); err != nil {
			t.Fatalf("upsert regime forecast: %v", err)
		}
	}
	put(a.ID, "trend21", "uptrend")
	put(b.ID, "trend21", "downtrend")
	put(a.ID, "vol21", "elevated")

	up, tn, el, vn, err = st.RegimeBreadth(ctx)
	if err != nil || up != 1 || tn != 2 || el != 1 || vn != 1 {
		t.Fatalf("breadth = %d/%d/%d/%d, %v; want 1/2/1/1", up, tn, el, vn, err)
	}
}

func TestModelEvolution_SeriesAreGroupedAndHonest(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Empty batch is a no-op; empty store returns empty (never nil) slices.
	if err := st.InsertWeightHistory(ctx, nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	data, err := st.ModelEvolution(ctx, 0)
	if err != nil || data.Weights == nil || data.FactorSkill == nil {
		t.Fatalf("empty evolution = %+v, %v; want empty non-nil", data, err)
	}

	if err := st.InsertWeightHistory(ctx, []WeightHistoryRow{
		{Ts: 100, Regime: "uptrend", Leg: "gbm", Weight: 0.4},
		{Ts: 200, Regime: "uptrend", Leg: "gbm", Weight: 0.5},
		{Ts: 100, Regime: "uptrend", Leg: "logit", Weight: 0.6},
	}); err != nil {
		t.Fatalf("insert weight history: %v", err)
	}
	audit := func(ts int64, metric, status string, v float64) {
		t.Helper()
		if err := st.InsertSelfAudit(ctx, SelfAuditRow{Ts: ts, Metric: metric,
			Value: v, Status: status, Detail: ""}); err != nil {
			t.Fatalf("insert self audit: %v", err)
		}
	}
	audit(100, "factor_ic:gbm", "ok", 0.05)
	audit(200, "factor_ic:gbm", "sign_flip", -0.02)
	audit(300, "factor_ic:gbm", "insufficient", 0) // honest gap — never plotted
	audit(100, "calibration", "ok", 0.9)           // not a factor-IC metric

	data, err = st.ModelEvolution(ctx, 0)
	if err != nil {
		t.Fatalf("ModelEvolution: %v", err)
	}
	if len(data.Weights) != 2 {
		t.Fatalf("weight series = %d; want 2 (gbm, logit)", len(data.Weights))
	}
	// Deterministic (regime, leg) order: gbm before logit within uptrend.
	if data.Weights[0].Leg != "gbm" || len(data.Weights[0].Points) != 2 ||
		data.Weights[0].Points[1].Weight != 0.5 {
		t.Fatalf("gbm series wrong: %+v", data.Weights[0])
	}
	if len(data.FactorSkill) != 1 || data.FactorSkill[0].Leg != "gbm" {
		t.Fatalf("factor skill legs = %+v", data.FactorSkill)
	}
	pts := data.FactorSkill[0].Points
	if len(pts) != 2 || pts[0].Status != "ok" || pts[1].Status != "sign_flip" {
		t.Fatalf("insufficient audit leaked into the IC series: %+v", pts)
	}

	// The window floor drops the older half of every series.
	data, _ = st.ModelEvolution(ctx, 150)
	if len(data.Weights) != 1 || len(data.Weights[0].Points) != 1 {
		t.Fatalf("sinceTs floor not applied to weights: %+v", data.Weights)
	}
}

func TestRecentNewsJoinsSymbols(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err := st.InsertNews(ctx, NewsItem{ID: "n1", SymbolID: sym.ID, Ts: 100,
		Headline: "old story", URL: "u1", Source: "wire"}); err != nil {
		t.Fatalf("insert news: %v", err)
	}
	if err := st.InsertNews(ctx, NewsItem{ID: "n2", SymbolID: sym.ID, Ts: 200,
		Headline: "new story", URL: "u2", Source: "wire"}); err != nil {
		t.Fatalf("insert news: %v", err)
	}
	if err := st.RateNews(ctx, "n2", "bullish", 0.8, "strong guide"); err != nil {
		t.Fatalf("rate news: %v", err)
	}

	items, err := st.RecentNews(ctx, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("RecentNews = %d rows, %v; want 2", len(items), err)
	}
	if items[0].ID != "n2" || items[0].Symbol != "AAPL" ||
		items[0].Sentiment != "bullish" || items[0].Score != 0.8 {
		t.Fatalf("newest row mangled: %+v", items[0])
	}
	if items, _ := st.RecentNews(ctx, 1); len(items) != 1 {
		t.Fatal("limit not applied")
	}
}

func TestAnomalies_ArchiveBeforePruneContract(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	ins := func(ts int64, kind string) {
		t.Helper()
		if fresh, err := st.InsertAnomaly(ctx, AnomalyRow{SymbolID: sym.ID, Ts: ts,
			Kind: kind, Z: 3.0, Detail: "d"}); err != nil || !fresh {
			t.Fatalf("insert anomaly(%d) = %v, %v", ts, fresh, err)
		}
	}
	ins(1000, "anomaly_vol")
	ins(5000, "anomaly_vol")
	ins(9000, "anomaly_volume")

	// The archive read and the prune must share the same [<cutoff) predicate.
	old, err := st.AnomaliesBelow(ctx, 6000, 0)
	if err != nil || len(old) != 2 || old[0].Ts != 1000 || old[1].Ts != 5000 {
		t.Fatalf("AnomaliesBelow = %+v, %v; want ts 1000,5000", old, err)
	}
	if capped, _ := st.AnomaliesBelow(ctx, 6000, 1); len(capped) != 1 {
		t.Fatal("limit not applied to archive read")
	}
	n, err := st.PruneAnomalies(ctx, 6000)
	if err != nil || n != 2 {
		t.Fatalf("PruneAnomalies = %d, %v; want 2", n, err)
	}
	if left, _ := st.AnomaliesBelow(ctx, 1<<40, 0); len(left) != 1 || left[0].Ts != 9000 {
		t.Fatalf("prune removed the wrong rows: %+v", left)
	}
}

func TestTVQuoteTapeAndLatestReads(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")

	if _, ok, err := st.LatestTVQuote(ctx, sym.ID); err != nil || ok {
		t.Fatalf("absent quote = ok=%v, %v; want false", ok, err)
	}
	if err := st.InsertTVQuote(ctx, TVQuoteRow{SymbolID: sym.ID, Ts: 100,
		Price: 210, DelayedClose: 209, ChangePct: 0.5, DayVolume: 1e6, Realtime: false}); err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	if err := st.InsertTVQuote(ctx, TVQuoteRow{SymbolID: sym.ID, Ts: 200,
		Price: 212, DelayedClose: 210, ChangePct: 1.1, DayVolume: 2e6, Realtime: true}); err != nil {
		t.Fatalf("insert quote 2: %v", err)
	}
	q, ok, err := st.LatestTVQuote(ctx, sym.ID)
	if err != nil || !ok || q.Ts != 200 || !q.Realtime || q.Price != 212 {
		t.Fatalf("LatestTVQuote = %+v, %v, %v", q, ok, err)
	}
	// Trailing-window retention: only the old row goes.
	if n, err := st.PruneTVQuotes(ctx, 150); err != nil || n != 1 {
		t.Fatalf("PruneTVQuotes = %d, %v; want 1", n, err)
	}
	if q, ok, _ := st.LatestTVQuote(ctx, sym.ID); !ok || q.Ts != 200 {
		t.Fatalf("prune removed the live quote: %+v ok=%v", q, ok)
	}
}

func TestLatestPerpStocktwitsAndCOTReads(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")

	if _, ok, err := st.LatestCryptoPerp(ctx, btc.ID); err != nil || ok {
		t.Fatalf("absent perp = ok=%v, %v; want false", ok, err)
	}
	for _, r := range []CryptoPerpRow{
		{SymbolID: btc.ID, Ts: 100, Funding: 0.0001, OpenInterest: 1e8, MarkPx: 60000},
		{SymbolID: btc.ID, Ts: 200, Funding: 0.0002, OpenInterest: 2e8, MarkPx: 61000},
	} {
		if err := st.InsertCryptoPerp(ctx, r); err != nil {
			t.Fatalf("insert perp: %v", err)
		}
	}
	if p, ok, err := st.LatestCryptoPerp(ctx, btc.ID); err != nil || !ok || p.Ts != 200 || p.MarkPx != 61000 {
		t.Fatalf("LatestCryptoPerp = %+v, %v, %v", p, ok, err)
	}

	if _, ok, err := st.LatestStocktwits(ctx, btc.ID); err != nil || ok {
		t.Fatalf("absent stocktwits = ok=%v, %v; want false", ok, err)
	}
	if err := st.InsertStocktwits(ctx, StocktwitsRow{SymbolID: btc.ID, Ts: 300,
		Bullish: 12, Bearish: 4, Untagged: 9, Total: 25}); err != nil {
		t.Fatalf("insert stocktwits: %v", err)
	}
	if r, ok, err := st.LatestStocktwits(ctx, btc.ID); err != nil || !ok || r.Bullish != 12 || r.Total != 25 {
		t.Fatalf("LatestStocktwits = %+v, %v, %v", r, ok, err)
	}

	if _, ok, err := st.LatestCOTByContract(ctx, "%E-MINI%"); err != nil || ok {
		t.Fatalf("absent COT = ok=%v, %v; want false", ok, err)
	}
	if err := st.UpsertCOT(ctx, []COTRow{
		{Contract: "E-MINI S&P 500", ReportDate: "2026-07-14", NoncommLong: 100,
			NoncommShort: 80, CommLong: 200, CommShort: 210, OpenInterest: 500},
		{Contract: "E-MINI S&P 500", ReportDate: "2026-07-21", NoncommLong: 110,
			NoncommShort: 70, CommLong: 190, CommShort: 220, OpenInterest: 520},
	}); err != nil {
		t.Fatalf("upsert COT: %v", err)
	}
	// Case-insensitive LIKE, newest report wins.
	r, ok, err := st.LatestCOTByContract(ctx, "%e-mini s&p%")
	if err != nil || !ok || r.ReportDate != "2026-07-21" || r.NoncommLong != 110 {
		t.Fatalf("LatestCOTByContract = %+v, %v, %v", r, ok, err)
	}
}
