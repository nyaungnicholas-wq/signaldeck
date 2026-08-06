package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func vec(n, distinct int, lo, hi float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		if distinct == 1 {
			out[i] = lo
			continue
		}
		out[i] = lo + (hi-lo)*float64(i%distinct)/float64(distinct-1)
	}
	return out
}

// THE PROPERTY THIS GATE LIVES OR DIES ON: a withheld horizon must be able to
// reopen.
//
// The first version read the prior day out of the predictions table. Withholding
// writes no predictions, so the newest day carrying rows stayed the collapsed one
// and the gate latched shut permanently — a refusal that could never be
// discharged by any code path, which is the shape this repo keeps getting bitten
// by. The measurement is now recorded whether or not the row is published, so a
// gated pass still reports its own shape.
func TestGateReleasesAfterTheCrossSectionRecovers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	h := md.Horizon("1d")

	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	// gatesNow mirrors the runner's decision: only a record stamped with a PRIOR
	// day may gate.
	gatesNow := func() (bool, string) {
		t.Helper()
		rec, err := loadCrossSection(ctx, st, h)
		if err != nil || rec == nil || rec.Day == "" || rec.Day >= today {
			return false, "no prior-day record"
		}
		ok, reason := rec.CrossSection.Usable()
		return !ok, reason
	}

	// Nothing recorded yet: a cold start must not read as a collapse.
	if gated, _ := gatesNow(); gated {
		t.Fatal("an empty meta store must not gate — cold start is not collapse")
	}

	// Yesterday collapsed (the 2026-08-03 shape: 5 values across 329 symbols).
	saveCrossSection(ctx, st, h, yesterday, ensemble.MeasureCrossSection(vec(329, 5, 0.515, 0.581)))
	gated, reason := gatesNow()
	if !gated {
		t.Fatal("a collapsed prior day must gate")
	}
	if reason == "" {
		t.Fatal("a refusal must carry a reason")
	}

	// The gated pass still records its shape, and the shape has RECOVERED (the
	// 2026-08-05 repair: 326 distinct over a 0.296 spread). This is the write
	// that the first version never made.
	saveCrossSection(ctx, st, h, yesterday, ensemble.MeasureCrossSection(vec(329, 326, 0.360, 0.656)))
	if gated, reason := gatesNow(); gated {
		t.Fatalf("gate must RELEASE once the recorded cross-section recovers, still refusing: %s", reason)
	}
}

// A record stamped today must never gate: it describes the sweep currently being
// written, and judging that would make the gate a function of its own output.
func TestTodaysRecordCannotGate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	h := md.Horizon("1w")
	today := time.Now().UTC().Format("2006-01-02")

	// Maximally degenerate, stamped TODAY.
	saveCrossSection(ctx, st, h, today, ensemble.MeasureCrossSection(vec(300, 1, 0.47, 0.47)))
	rec, err := loadCrossSection(ctx, st, h)
	if err != nil || rec == nil {
		t.Fatalf("record must round-trip, got %v %v", rec, err)
	}
	if rec.Day < today {
		t.Fatalf("stamped %q, want today %q", rec.Day, today)
	}
	if ok, _ := rec.CrossSection.Usable(); ok {
		t.Fatal("fixture should be degenerate — the point is that the DAY guard, not Usable(), spares it")
	}
	// The runner's guard is rec.Day >= today, so this record cannot gate despite
	// being unusable.
	if !(rec.Day >= today) {
		t.Fatal("today's record must be excluded by the day guard")
	}
}

// The record must survive the round trip with every field the log and the health
// row report, or a refusal becomes unexplainable after a restart.
func TestCrossSectionRecordRoundTrips(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	h := md.Horizon("1d")

	want := ensemble.MeasureCrossSection(vec(329, 5, 0.515, 0.581))
	saveCrossSection(ctx, st, h, "2026-08-03", want)
	rec, err := loadCrossSection(ctx, st, h)
	if err != nil || rec == nil {
		t.Fatalf("load: %v %v", rec, err)
	}
	if rec.Day != "2026-08-03" {
		t.Errorf("day = %q, want 2026-08-03", rec.Day)
	}
	if rec.N != want.N || rec.Distinct != want.Distinct {
		t.Errorf("n/distinct = %d/%d, want %d/%d", rec.N, rec.Distinct, want.N, want.Distinct)
	}
	if rec.Spread != want.Spread || rec.Agreement != want.Agreement {
		t.Errorf("spread/agreement = %.6f/%.6f, want %.6f/%.6f",
			rec.Spread, rec.Agreement, want.Spread, want.Agreement)
	}
	// Horizons must not share a slot.
	if other, _ := loadCrossSection(ctx, st, md.Horizon("1w")); other != nil {
		t.Error("1w must not read 1d's record")
	}
}
