// Tests for the derived-table retention helpers: every *Before read must
// return exactly what the matching Delete*Before will remove (archive-before-
// prune fail-safe), and an unlabeled feature row must never be prunable.
package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestScoresAndOutcomesRetention(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	for _, ts := range []int64{1000, 2000, 9000} {
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1d,
			Ts: ts, Score: 0.1}); err != nil {
			t.Fatalf("insert score: %v", err)
		}
	}

	old, err := st.ScoresBefore(ctx, 5000, 0)
	if err != nil || len(old) != 2 || old[0].Ts != 1000 || old[1].Ts != 2000 {
		t.Fatalf("ScoresBefore = %+v, %v; want ts 1000,2000 asc", old, err)
	}
	if capped, _ := st.ScoresBefore(ctx, 5000, 1); len(capped) != 1 {
		t.Fatal("ScoresBefore limit not applied")
	}
	if n, err := st.DeleteScoresBefore(ctx, 5000); err != nil || n != 2 {
		t.Fatalf("DeleteScoresBefore = %d, %v; want 2", n, err)
	}
	if left, _ := st.ScoresBefore(ctx, 1<<40, 0); len(left) != 1 || left[0].Ts != 9000 {
		t.Fatalf("prune removed the wrong scores: %+v", left)
	}

	// InsertScore seeded a score_outcomes row per score; resolve one so the
	// nullable columns are exercised both ways.
	if err := st.ResolveOutcome(ctx, sym.ID, md.H1d, 1000, 0.02); err != nil {
		t.Fatalf("resolve outcome: %v", err)
	}
	oc, err := st.ScoreOutcomesBefore(ctx, 5000, 0)
	if err != nil || len(oc) != 2 {
		t.Fatalf("ScoreOutcomesBefore = %d rows, %v; want 2", len(oc), err)
	}
	if !oc[0].FwdReturn.Valid || oc[0].FwdReturn.Float64 != 0.02 || oc[1].FwdReturn.Valid {
		t.Fatalf("nullable outcome columns wrong: %+v", oc)
	}
	if n, err := st.DeleteScoreOutcomesBefore(ctx, 5000); err != nil || n != 2 {
		t.Fatalf("DeleteScoreOutcomesBefore = %d, %v; want 2", n, err)
	}
}

func TestResolvedFeaturesRetention_UnlabeledNeverPruned(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	// ts=1000: labeled (prediction resolved). ts=2000: still open.
	seedResolvedPred(t, st, sym.ID, md.H1d, 1000, 0.6, 0.6, 0.01)
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 1000, 4,
		map[string]float64{"x": 1}); err != nil {
		t.Fatalf("insert features: %v", err)
	}
	if err := st.UpsertPrediction(ctx, Prediction{SymbolID: sym.ID, Horizon: md.H1d,
		Ts: 2000, RawProb: 0.5, CalProb: 0.5, NUsed: 1, Components: "{}"}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 2000, 4,
		map[string]float64{"x": 2}); err != nil {
		t.Fatalf("insert features: %v", err)
	}

	rows, err := st.ResolvedFeaturesBefore(ctx, 5000, 0)
	if err != nil || len(rows) != 1 || rows[0].Ts != 1000 {
		t.Fatalf("ResolvedFeaturesBefore = %+v, %v; want only ts=1000", rows, err)
	}
	if rows[0].Vec == "" || rows[0].Horizon != "1d" {
		t.Fatalf("archive row incomplete: %+v", rows[0])
	}
	n, err := st.DeleteResolvedFeaturesBefore(ctx, 5000)
	if err != nil || n != 1 {
		t.Fatalf("DeleteResolvedFeaturesBefore = %d, %v; want 1", n, err)
	}
	// The unlabeled row survived — it may still become a training label.
	left, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil || len(left) != 1 || left[0].Ts != 2000 {
		t.Fatalf("unlabeled feature was pruned: %+v, %v", left, err)
	}
}

func TestFilingsInsightsPostmortemsRetention(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")

	// Filings age out by filed_ts; the EDGAR pointer must ride the archive row.
	for _, f := range []FilingRow{
		{ID: "acc-1", SymbolID: sym.ID, Form: "8-K", FiledTs: 1000, Title: "old",
			URL: "https://sec.gov/1", Label: "material"},
		{ID: "acc-2", SymbolID: sym.ID, Form: "10-Q", FiledTs: 9000, Title: "new",
			URL: "https://sec.gov/2", Label: ""},
	} {
		if fresh, err := st.InsertFiling(ctx, f); err != nil || !fresh {
			t.Fatalf("insert filing = %v, %v", fresh, err)
		}
	}
	fl, err := st.FilingsBefore(ctx, 5000, 0)
	if err != nil || len(fl) != 1 || fl[0].ID != "acc-1" || fl[0].URL != "https://sec.gov/1" {
		t.Fatalf("FilingsBefore = %+v, %v", fl, err)
	}
	if n, err := st.DeleteFilingsBefore(ctx, 5000); err != nil || n != 1 {
		t.Fatalf("DeleteFilingsBefore = %d, %v; want 1", n, err)
	}

	// Insights: symbol-scoped and market-scoped (NULL symbol) both archive.
	if err := st.InsertInsight(ctx, md.Insight{Scope: "symbol", SymbolID: &sym.ID,
		Ts: 1000, Headline: "h1", Body: "b1", Data: "{}"}); err != nil {
		t.Fatalf("insert insight: %v", err)
	}
	if err := st.InsertInsight(ctx, md.Insight{Scope: "market", Ts: 2000,
		Headline: "h2", Body: "b2", Data: "{}"}); err != nil {
		t.Fatalf("insert insight: %v", err)
	}
	ins, err := st.InsightsBefore(ctx, 5000, 0)
	if err != nil || len(ins) != 2 {
		t.Fatalf("InsightsBefore = %d rows, %v; want 2", len(ins), err)
	}
	if !ins[0].SymbolID.Valid || ins[1].SymbolID.Valid {
		t.Fatalf("nullable symbol scope wrong: %+v", ins)
	}
	if n, err := st.DeleteInsightsBefore(ctx, 1500); err != nil || n != 1 {
		t.Fatalf("DeleteInsightsBefore = %d, %v; want 1", n, err)
	}

	// Postmortems age out by the explained prediction's bar ts; the ranked
	// reasons JSON must round-trip verbatim into the archive row.
	insPM := func(ts int64, reason string) {
		t.Helper()
		if _, err := st.w.ExecContext(ctx, `
			INSERT INTO prediction_postmortems
			  (symbol_id, horizon, ts, prob, up, fwd_return, conviction, magnitude,
			   primary_reason, secondary_reason, reasons, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			sym.ID, "1d", ts, 0.7, 0, -0.03, 0.2, 0.03,
			reason, "", `[{"reason":"`+reason+`"}]`, ts+10); err != nil {
			t.Fatalf("seed postmortem: %v", err)
		}
	}
	insPM(1000, "regime_flip")
	insPM(9000, "news_shock")

	pm, err := st.PostmortemsBefore(ctx, 5000, 0)
	if err != nil || len(pm) != 1 || pm[0].PrimaryReason != "regime_flip" {
		t.Fatalf("PostmortemsBefore = %+v, %v", pm, err)
	}
	if pm[0].Reasons != `[{"reason":"regime_flip"}]` || pm[0].CreatedAt != 1010 {
		t.Fatalf("archive row lost fields: %+v", pm[0])
	}
	if n, err := st.DeletePostmortemsBefore(ctx, 5000); err != nil || n != 1 {
		t.Fatalf("DeletePostmortemsBefore = %d, %v; want 1", n, err)
	}
	if left, _ := st.PostmortemsBefore(ctx, 1<<40, 0); len(left) != 1 || left[0].Ts != 9000 {
		t.Fatalf("prune removed the wrong postmortem: %+v", left)
	}
}
