package maintain

import (
	"context"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestOutcomeResolverVoidsDelistedImmediately(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	t0 := dayStart - 5*86400 + 5*3600

	deadSym, err := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	liveSym, err := st.UpsertSymbol(ctx, "LIVE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	bars := []md.Bar{
		{SymbolID: deadSym.ID, TF: md.TF1d, Ts: t0, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: deadSym.ID, TF: md.TF1d, Ts: t0 - 86400, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: liveSym.ID, TF: md.TF1d, Ts: t0, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: liveSym.ID, TF: md.TF1d, Ts: t0 - 86400, Open: 100, High: 100, Low: 100, Close: 100},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	scoreTS := t0 + 3600
	if err := st.InsertScore(ctx, md.Score{SymbolID: deadSym.ID, Horizon: md.H1d, Ts: scoreTS, Score: 0.5}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertScore(ctx, md.Score{SymbolID: liveSym.ID, Horizon: md.H1d, Ts: scoreTS, Score: 0.5}); err != nil {
		t.Fatal(err)
	}

	delistTS := now.Unix() - 86400
	if err := st.MarkDelisted(ctx, deadSym.ID, delistTS); err != nil {
		t.Fatal(err)
	}

	detail, err := (&OutcomeResolver{St: st}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}

	deadRows, err := st.ResolvedOutcomes(ctx, deadSym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadRows) != 1 {
		t.Fatalf("DEAD: expected 1 resolved row, got %d", len(deadRows))
	}
	if deadRows[0].ResolvedAt == nil {
		t.Fatalf("DEAD: expected ResolvedAt set, got nil")
	}
	if deadRows[0].FwdReturn != nil {
		t.Fatalf("DEAD: expected FwdReturn nil, got %v", *deadRows[0].FwdReturn)
	}

	liveRows, err := st.ResolvedOutcomes(ctx, liveSym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(liveRows) != 0 {
		t.Fatalf("LIVE: expected 0 resolved rows, got %d", len(liveRows))
	}

	if !strings.Contains(detail, "voided 1") {
		t.Errorf("detail missing 'voided 1': %s", detail)
	}
}

func TestOutcomeResolverVoidsDeadPredictions(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	t0 := dayStart - 40*86400 + 5*3600

	goneSym, err := st.UpsertSymbol(ctx, "GONE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	aliveSym, err := st.UpsertSymbol(ctx, "ALIVE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	bars := []md.Bar{
		{SymbolID: goneSym.ID, TF: md.TF1d, Ts: t0, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: aliveSym.ID, TF: md.TF1d, Ts: t0, Open: 100, High: 100, Low: 100, Close: 100},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	seedTS := t0 + 3600
	if err := st.SeedBenchmarkOutcome(ctx, goneSym.ID, md.H1d, seedTS, 0.6); err != nil {
		t.Fatal(err)
	}
	if err := st.SeedBenchmarkOutcome(ctx, aliveSym.ID, md.H1d, seedTS, 0.6); err != nil {
		t.Fatal(err)
	}

	delistTS := now.Unix() - 30*86400
	if err := st.MarkDelisted(ctx, goneSym.ID, delistTS); err != nil {
		t.Fatal(err)
	}

	detail, err := (&OutcomeResolver{St: st}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var goneUnresolved, goneVoided int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=? AND resolved_at IS NULL`, goneSym.ID).Scan(&goneUnresolved); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=? AND resolved_at IS NOT NULL AND up IS NULL`, goneSym.ID).Scan(&goneVoided); err != nil {
		t.Fatal(err)
	}
	if goneUnresolved != 0 {
		t.Errorf("GONE: expected 0 unresolved, got %d", goneUnresolved)
	}
	if goneVoided != 1 {
		t.Errorf("GONE: expected 1 voided, got %d", goneVoided)
	}

	var aliveUnresolved, aliveVoided int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=? AND resolved_at IS NULL`, aliveSym.ID).Scan(&aliveUnresolved); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=? AND resolved_at IS NOT NULL AND up IS NULL`, aliveSym.ID).Scan(&aliveVoided); err != nil {
		t.Fatal(err)
	}
	if aliveUnresolved != 1 {
		t.Errorf("ALIVE: expected 1 unresolved, got %d", aliveUnresolved)
	}
	if aliveVoided != 0 {
		t.Errorf("ALIVE: expected 0 voided, got %d", aliveVoided)
	}

	if !strings.Contains(detail, "dead predictions voided 1") {
		t.Errorf("detail missing 'dead predictions voided 1': %s", detail)
	}

	events, err := st.RecentDQ(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "dead_predictions_voided" && strings.Contains(e.Detail, "1 forecast outcome") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no dead_predictions_voided DQ event with '1 forecast outcome' in %+v", events)
	}
}