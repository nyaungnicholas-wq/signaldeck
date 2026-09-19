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
	defer st.Close() //nolint:errcheck
	ctx := context.Background()
	h := md.Horizon("1d")

	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	// gatesNow CALLS THE RUNNER'S OWN DECISION rather than mirroring it. It used
	// to mirror it, with a "rec.Day >= today" rule, and stayed green for weeks
	// after the runner had stopped using that rule and moved to reading the
	// predictions table -- these tests were describing a design nothing executed.
	// Sharing xsGateDecision is what makes that drift impossible.
	gatesNow := func() (bool, string) {
		t.Helper()
		rec, err := loadCrossSection(ctx, st, h)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return xsGateDecision(rec, today, yesterday)
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

// SUPERSEDED SPEC, kept deliberately as the explanation of why it changed.
//
// This used to be TestTodaysRecordCannotGate, asserting that a record stamped
// today may never gate, "because it describes the sweep currently being written
// and judging that would make the gate a function of its own output".
//
// The PROPERTY is right and is still enforced. The MECHANISM was wrong. The
// runner loads the record before the symbol loop and saves the new one after it,
// so a pass always reads the PREVIOUS pass and can never read its own --
// sequencing already guarantees it. Enforcing it with the date instead is what
// made the gate able to fire only ONCE PER UTC DAY: saveCrossSection stamps
// TODAY at the end of pass one, so every later pass that day short-circuited and
// roughly 137 passes per session published ungated. A six-day collapse withheld
// six passes out of about 830 while logging a warning that read like the gate
// working.
//
// So today's record MUST be able to gate, and this pins that.
func TestTodaysRecordCanGate(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	rec := &crossSectionRecord{Day: today, CrossSection: ensemble.MeasureCrossSection(vec(300, 1, 0.47, 0.47))}
	gate, reason := xsGateDecision(rec, today, yesterday)
	if !gate {
		t.Fatal("a degenerate record stamped TODAY must gate — the previous rule let it through, which is how the gate fired once per day")
	}
	if reason == "" {
		t.Fatal("a refusal must carry a reason")
	}
}

// A pass too thin to describe a cross-section is not evidence in EITHER
// direction. Measured 2026-08-08: a record of n=12 on a 329-symbol day, where
// the distinct rule then needed only 6, silently DISARMED the gate and the
// collapses of 08-06 and 08-07 published. Reading a different population was the
// wrong cure; refusing to treat a thin pass as evidence is the right one.
func TestThinRecordIsNotEvidence(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	// Maximally degenerate but far too thin to describe a cross-section.
	rec := &crossSectionRecord{Day: today, CrossSection: ensemble.MeasureCrossSection(vec(xsGateMinEvidenceN-1, 1, 0.47, 0.47))}
	if gate, _ := xsGateDecision(rec, today, yesterday); gate {
		t.Fatalf("a record of n=%d must not gate — too thin to be evidence", xsGateMinEvidenceN-1)
	}
	// One more symbol and the same shape IS evidence, so the floor is a floor and
	// not an accidental always-off.
	rec.CrossSection = ensemble.MeasureCrossSection(vec(xsGateMinEvidenceN, 1, 0.47, 0.47))
	if gate, _ := xsGateDecision(rec, today, yesterday); !gate {
		t.Fatalf("a record of n=%d must gate — otherwise the floor disables the gate entirely", xsGateMinEvidenceN)
	}
}

// A record older than yesterday means the runner was DOWN. It describes a market
// that is no longer the one being forecast, so it is not evidence -- the same
// answer as a cold start. Without this, a stale record gates a restarted fleet
// indefinitely, which is the latch this gate must never have.
func TestStaleRecordIsNotEvidence(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	old := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	rec := &crossSectionRecord{Day: old, CrossSection: ensemble.MeasureCrossSection(vec(329, 1, 0.47, 0.47))}
	if gate, _ := xsGateDecision(rec, today, yesterday); gate {
		t.Fatal("a record older than yesterday must not gate — the runner was down, that is not evidence")
	}
}

// Cold start must not read as a collapse.
func TestNilRecordIsColdStart(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	if gate, _ := xsGateDecision(nil, today, yesterday); gate {
		t.Fatal("no record at all must not gate — a cold start is not a collapse")
	}
}

// The record must survive the round trip with every field the log and the health
// row report, or a refusal becomes unexplainable after a restart.
func TestCrossSectionRecordRoundTrips(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
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
