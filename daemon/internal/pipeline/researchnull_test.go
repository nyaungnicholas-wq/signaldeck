package pipeline

import (
	"context"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
)

// A hypothesis whose null arm cannot be measured writes NOTHING — no
// hypothesis row, no evidence row, no capped experiment. Before this, an
// unmeasurable null silently became the literal 0.5 and the grade was
// published against a rate the same repository elsewhere disproves.
func TestUnmeasurableNullProducesZeroEvidenceRows(t *testing.T) {
	st := adTestStore(t)
	ctx := context.Background()
	w := &ResearchEngineWorker{St: st}

	// CF.NullMatched graded no weeks ⇒ the no-skill rate for this window was
	// never measured.
	c := researchx.Candidate{
		ID:    "AD-nonull01",
		Rule:  researchx.Rule{Call: "long"},
		Desc:  "candidate whose null arm graded nothing",
		Grade: researchx.WeekGrade{WinWeeks: 9, Weeks: 10, TotalObs: 120},
	}
	if c.CF.NullMatched.Grade.Weeks != 0 {
		t.Fatalf("test setup: null arm must be unmeasured")
	}
	obs := adObs(histfeat.WeekSecs, 6, 12)
	if err := w.insertCandidate(ctx, c, obs, 1000, obs[0].Ts, obs[len(obs)-1].Ts, 3, 4); err != nil {
		t.Fatalf("insertCandidate: %v", err)
	}
	ev, err := st.LedgerEvidence(ctx, c.ID)
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if len(ev) != 0 {
		t.Fatalf("an unmeasured null produced %d evidence rows, want 0: %+v", len(ev), ev)
	}
	hyps, err := st.LedgerHypotheses(ctx)
	if err != nil {
		t.Fatalf("hypotheses: %v", err)
	}
	for _, h := range hyps {
		if h.ID == c.ID {
			t.Fatalf("an unmeasured null registered hypothesis %s", h.ID)
		}
	}
}

// Every newly written grade row records the null it was actually graded
// against: Evidence.P0 must equal the measured null-matched arm of that same
// window, never a literal.
func TestGradeRowsCarryTheMeasuredNull(t *testing.T) {
	st := adTestStore(t)
	ctx := context.Background()
	w := &ResearchEngineWorker{St: st}
	discEnd := int64(100) * histfeat.WeekSecs
	seedADHyp(t, st, "AD-null0001", rl.StatusTentative, histfeat.WeekSecs, discEnd)

	fresh := adObs(discEnd+2*histfeat.WeekSecs, 6, 12)
	if n := w.adReplicate(ctx, fresh, 1000, 3, 4); n != 1 {
		t.Fatalf("want 1 AD grade, got %d", n)
	}

	rule := researchx.Rule{Call: "long"}
	cf := researchx.Counterfactual(fresh, rule, 3, 4)
	want := rl.NullFromArm(cf.NullMatched.Grade.WinWeeks, cf.NullMatched.Grade.Weeks, "null-matched week")
	if !want.Measured() {
		t.Fatalf("test setup: expected a measurable null arm")
	}

	ev, err := st.LedgerEvidence(ctx, "AD-null0001")
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	seen := 0
	for _, e := range ev {
		if e.Ts != 1000 || !e.DataBacked() {
			continue // seeded row, or an attack row carrying no observation
		}
		seen++
		if e.P0 != want.P0() {
			t.Errorf("%s row P0 = %.6f, want the measured null %.6f", e.Kind, e.P0, want.P0())
		}
		if e.P0 < 0.5 {
			t.Errorf("%s row P0 = %.6f is below the 0.5 floor", e.Kind, e.P0)
		}
	}
	if seen == 0 {
		t.Fatal("no newly written data-backed row to check")
	}
}
