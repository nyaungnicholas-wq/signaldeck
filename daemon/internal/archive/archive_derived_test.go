// Round-trip tests for the DERIVED-table archivers (scores, score_outcomes,
// features, anomalies, filings, insights, postmortems, research_weeks,
// composite_scores) — the cold files a retention prune trusts. Each must write
// one file per symbol group, name it by the actual ts span, and carry every
// column faithfully (including nullable ones).
package archive

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func TestArchiveScoresAndOutcomes(t *testing.T) {
	root := t.TempDir()
	a := New(root)
	names := map[int64]string{7: "AAPL"}

	n, err := a.ArchiveScores(context.Background(), []store.ScoreRow{
		{SymbolID: 7, Horizon: "1d", Ts: 2000, Score: 0.4, Components: `[{"name":"rsi"}]`},
		{SymbolID: 7, Horizon: "1d", Ts: 1000, Score: 0.2, Components: `[]`},
	}, names)
	if err != nil || n != 1 {
		t.Fatalf("ArchiveScores = %d, %v; want 1 file", n, err)
	}
	path := findOne(t, filepath.Join(root, "scores"))
	if filepath.Base(path) != "AAPL_1000-2000.csv.gz" {
		t.Fatalf("scores filename = %q", filepath.Base(path))
	}
	header, recs := readGzCSV(t, path)
	if len(header) != 6 || header[5] != "components" || len(recs) != 2 {
		t.Fatalf("scores archive shape wrong: %v / %d rows", header, len(recs))
	}
	// ts-ascending, components verbatim.
	if recs[0][3] != "1000" || recs[1][5] != `[{"name":"rsi"}]` {
		t.Fatalf("scores rows wrong: %v", recs)
	}

	// Outcomes: nullable fwd_return/resolved_at must archive both ways.
	n, err = a.ArchiveScoreOutcomes(context.Background(), []store.ScoreOutcomeRow{
		{SymbolID: 7, Horizon: "1d", Ts: 1000, Score: 0.2,
			FwdReturn:  sql.NullFloat64{Float64: 0.03, Valid: true},
			ResolvedAt: sql.NullInt64{Int64: 1500, Valid: true}},
		{SymbolID: 7, Horizon: "1d", Ts: 2000, Score: 0.4},
	}, names)
	if err != nil || n != 1 {
		t.Fatalf("ArchiveScoreOutcomes = %d, %v; want 1 file", n, err)
	}
	_, orecs := readGzCSV(t, findOne(t, filepath.Join(root, "score_outcomes")))
	if len(orecs) != 2 {
		t.Fatalf("outcome rows = %d; want 2", len(orecs))
	}
	if orecs[0][5] == "" || orecs[1][5] != "" {
		t.Fatalf("nullable fwd_return not carried faithfully: %v", orecs)
	}
}

func TestArchiveFeaturesAndAnomalies(t *testing.T) {
	root := t.TempDir()
	a := New(root)
	names := map[int64]string{7: "AAPL", 9: "MSFT"}

	// Two symbols → two files; the vec JSON rides verbatim.
	n, err := a.ArchiveFeatures(context.Background(), []store.FeatureArchiveRow{
		{ID: 1, SymbolID: 7, Horizon: "1d", Ts: 1000, Version: 4, Vec: `{"momo":1.5}`},
		{ID: 2, SymbolID: 9, Horizon: "1d", Ts: 1100, Version: 4, Vec: `{"momo":2.5}`},
	}, names)
	if err != nil || n != 2 {
		t.Fatalf("ArchiveFeatures = %d, %v; want 2 files", n, err)
	}
	var found int
	for _, sym := range []string{"AAPL", "MSFT"} {
		matches, _ := filepath.Glob(filepath.Join(root, "features", sym+"_*.csv.gz"))
		found += len(matches)
	}
	if found != 2 {
		t.Fatalf("per-symbol feature files = %d; want 2", found)
	}
	_, frecs := readGzCSV(t, filepath.Join(root, "features", "AAPL_1000-1000.csv.gz"))
	if len(frecs) != 1 || frecs[0][6] != `{"momo":1.5}` {
		t.Fatalf("feature vec did not round-trip: %v", frecs)
	}

	n, err = a.ArchiveAnomalies(context.Background(), []store.AnomalyRow{
		{ID: 5, SymbolID: 7, Symbol: "AAPL", Ts: 3000, Kind: "anomaly_vol", Z: 3.2, Detail: "d"},
	}, names)
	if err != nil || n != 1 {
		t.Fatalf("ArchiveAnomalies = %d, %v; want 1 file", n, err)
	}
	_, arecs := readGzCSV(t, findOne(t, filepath.Join(root, "anomalies")))
	if len(arecs) != 1 || arecs[0][4] != "anomaly_vol" || arecs[0][2] != "AAPL" {
		t.Fatalf("anomaly row wrong: %v", arecs)
	}
}

func TestArchiveUnmanagedSweepTables(t *testing.T) {
	root := t.TempDir()
	a := New(root)
	names := map[int64]string{7: "AAPL"}
	ctx := context.Background()

	// Filings: the EDGAR pointer must survive into cold storage.
	n, err := a.ArchiveFilings(ctx, []store.FilingArchiveRow{
		{ID: "acc-1", SymbolID: 7, Form: "10-K", FiledTs: 1000, Title: "fy",
			URL: "https://sec.gov/1", Label: "periodic"},
	}, names)
	if err != nil || n != 1 {
		t.Fatalf("ArchiveFilings = %d, %v; want 1 file", n, err)
	}
	_, frecs := readGzCSV(t, findOne(t, filepath.Join(root, "filings")))
	found := false
	for _, cell := range frecs[0] {
		if cell == "https://sec.gov/1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("EDGAR url lost in archive: %v", frecs[0])
	}

	// Insights: market-scope rows (NULL symbol) file under "market".
	n, err = a.ArchiveInsights(ctx, []store.InsightArchiveRow{
		{ID: 1, Scope: "symbol", SymbolID: sql.NullInt64{Int64: 7, Valid: true},
			Ts: 1000, Headline: "h1", Body: "b", Data: "{}"},
		{ID: 2, Scope: "market", Ts: 2000, Headline: "h2", Body: "b", Data: "{}"},
	}, names)
	if err != nil || n != 2 {
		t.Fatalf("ArchiveInsights = %d, %v; want 2 files", n, err)
	}
	if m, _ := filepath.Glob(filepath.Join(root, "insights", "market_*.csv.gz")); len(m) != 1 {
		t.Fatal("market-scope insight did not file under market_*")
	}

	// Postmortems: ranked reasons JSON verbatim.
	n, err = a.ArchivePostmortems(ctx, []store.PostmortemArchiveRow{
		{SymbolID: 7, Horizon: "1d", Ts: 1000, Prob: 0.7, Up: 0, FwdReturn: -0.02,
			Conviction: 0.2, Magnitude: 0.02, PrimaryReason: "regime_flip",
			Reasons: `[{"reason":"regime_flip"}]`, CreatedAt: 1100},
	}, names)
	if err != nil || n != 1 {
		t.Fatalf("ArchivePostmortems = %d, %v; want 1 file", n, err)
	}
	_, precs := readGzCSV(t, findOne(t, filepath.Join(root, "prediction_postmortems")))
	found = false
	for _, cell := range precs[0] {
		if cell == `[{"reason":"regime_flip"}]` {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons JSON lost: %v", precs[0])
	}

	// Research weeks: vec + labels verbatim.
	n, err = a.ArchiveResearchWeeks(ctx, []store.ResearchWeekArchiveRow{
		{SymbolID: 7, Week: 2, Ts: 1300000, Vec: `{"m":1}`, FwdReturn: 0.01,
			Up: 1, Era: "qe", HighVol: 0, CreatedAt: 99},
	}, names)
	if err != nil || n != 1 {
		t.Fatalf("ArchiveResearchWeeks = %d, %v; want 1 file", n, err)
	}
	_, wrecs := readGzCSV(t, findOne(t, filepath.Join(root, "research_weeks")))
	if wrecs[0][4] != `{"m":1}` {
		t.Fatalf("research week vec lost: %v", wrecs[0])
	}

	// Composite: payload JSON verbatim before the compactor strips it.
	n, err = a.ArchiveComposite(ctx, []store.CompositeArchiveRow{
		{SymbolID: 7, Ts: 1000, Horizon: "1d", Score: 72, CurvePct: 0.8,
			Edge: 0.01, Payload: `{"parts":[1]}`},
	}, names)
	if err != nil || n != 1 {
		t.Fatalf("ArchiveComposite = %d, %v; want 1 file", n, err)
	}
	_, crecs := readGzCSV(t, findOne(t, filepath.Join(root, "composite_scores")))
	if crecs[0][7] != `{"parts":[1]}` {
		t.Fatalf("composite payload lost: %v", crecs[0])
	}

	// Empty inputs are no-ops for every archiver.
	if n, err := a.ArchiveFilings(ctx, nil, names); err != nil || n != 0 {
		t.Fatalf("empty filings = %d, %v", n, err)
	}
	if n, err := a.ArchiveComposite(ctx, nil, names); err != nil || n != 0 {
		t.Fatalf("empty composite = %d, %v", n, err)
	}
}
