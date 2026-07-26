package researchledger

import (
	"encoding/json"
	"math"
	"testing"
)

// The failure these tests exist to prevent, measured on the live DB
// (2026-07-25, data/signaldeck.db, mode=ro, 86 evidence rows):
//
//	H006 "The pressure-inverse edge grows with horizon" published posterior
//	0.75 / status tentative. Its ENTIRE evidence chain is one `manual` row with
//	k=0, n=0 and a typed BF of 3.0. Recomputed over its data-backed rows the
//	posterior is 0.50 — its own prior. A hand-declared 3 was the whole belief.
//
//	H003 "One component (RSI) causes most of Pressure's damage" published
//	posterior 0.0476 / status REJECTED, from one n=0 row at exactly MinBF —
//	the floor of the honesty clamp, i.e. the most evidential weight the model
//	permits in the against direction, asserted on zero trials.
//
// The asymmetry encoded below is the fix: an assertion may WITHHOLD belief
// (H003 stands) but may never MANUFACTURE it (H006 falls back to its prior).
// A declared penalty states a known weakness and moves the posterior the
// conservative way; a declared BF>1 claims positive evidence for which no n
// exists.

func TestAssertedEvidenceCannotPromote(t *testing.T) {
	// The live H006 chain, verbatim.
	h006 := []Evidence{{
		Kind: KindManual, K: 0, N: 0, P0: 0, BF: 3,
		Note: "STATED JUDGMENT (BF=3 is a declared weak-moderate weight, not a computed statistic)",
	}}
	if got := Posterior(0.50, h006); math.Abs(got-0.50) > 1e-9 {
		t.Errorf("posterior over one asserted BF=3 = %.4f, want the 0.50 prior — "+
			"an assertion with n=0 must not raise a belief", got)
	}
}

func TestAssertedEvidenceStillPenalizes(t *testing.T) {
	// The mirror, and the reason the cap is one-sided: every attack penalty in
	// the battery is an n=0 row, and clamping those toward 1 would LOOSEN the
	// ledger — the opposite of the defect being fixed.
	pen := []Evidence{
		{Kind: KindAttack, BF: PenaltySingleRegime, Note: "single-regime: calm only"},
		{Kind: KindAttack, BF: PenaltyNoIncrementalValue, Note: "ablation: no incremental value"},
	}
	want := oddsToProb(1 * PenaltySingleRegime * PenaltyNoIncrementalValue)
	if got := Posterior(0.5, pen); math.Abs(got-want) > 1e-9 {
		t.Errorf("posterior over asserted penalties = %.6f, want %.6f (full force retained)", got, want)
	}

	// The live H003 chain: one asserted row at the floor keeps the hypothesis
	// rejected.
	h003 := []Evidence{{Kind: KindManual, N: 0, BF: MinBF, Note: "STATED JUDGMENT from the 2026-07-15 decomposition"}}
	if got := Posterior(0.50, h003); got >= 0.10 {
		t.Errorf("posterior over one asserted floor BF = %.4f, want < 0.10 (still rejected)", got)
	}
}

// A grade row (experiment/replication/backtest) that carries no observation is
// an assertion wearing a grade's kind — the shape a hand-typed number takes
// when it is filed as a measurement. The kind must not buy it any weight.
func TestGradeKindWithoutObservationIsAsserted(t *testing.T) {
	for _, kind := range []string{KindExperiment, KindReplication, KindBacktest, KindEconomic} {
		ev := []Evidence{{Kind: kind, N: 0, BF: MaxBF}}
		if got := Posterior(0.5, ev); math.Abs(got-0.5) > 1e-9 {
			t.Errorf("%s row with n=0 and BF=%.0f moved the posterior to %.4f, want 0.5", kind, MaxBF, got)
		}
	}
}

// The same BF filed WITH its observation is evidence and does move the belief —
// otherwise the fix would have gutted the ledger rather than disciplined it.
func TestDataBackedEvidenceUnaffected(t *testing.T) {
	ev := []Evidence{{Kind: KindReplication, K: 546, N: 1006, P0: 0.507, BF: 5.2}}
	want := oddsToProb(1 * 5.2)
	if got := Posterior(0.5, ev); math.Abs(got-want) > 1e-9 {
		t.Errorf("posterior over a data-backed BF=5.2 = %.6f, want %.6f", got, want)
	}
}

// Refusing the cap for asserted rows is not enough on its own: the number that
// WAS refused has to be reportable, or the correction is itself an undisclosed
// adjustment — the failure mode this repo's review named "disclosure as a
// substitute for correction", run in reverse.
func TestBreakdownReportsWhatWasRefused(t *testing.T) {
	chain := []Evidence{
		{Kind: KindReplication, K: 60, N: 100, P0: 0.5, BF: 4},
		{Kind: KindManual, N: 0, BF: 3, Note: "STATED JUDGMENT"},
		{Kind: KindAttack, N: 0, BF: PenaltySingleRegime, Note: "single-regime: calm only"},
	}
	b := Breakdown(0.5, chain)
	if b.DataRows != 1 || b.AssertedRows != 2 {
		t.Errorf("row split = %d data / %d asserted, want 1 / 2", b.DataRows, b.AssertedRows)
	}
	if b.RefusedRows != 1 {
		t.Errorf("refused rows = %d, want 1 (only the BF>1 assertion)", b.RefusedRows)
	}
	if want := oddsToProb(4 * PenaltySingleRegime); math.Abs(b.Posterior-want) > 1e-9 {
		t.Errorf("headline posterior = %.6f, want %.6f", b.Posterior, want)
	}
	if want := oddsToProb(4); math.Abs(b.DataOnly-want) > 1e-9 {
		t.Errorf("data-only posterior = %.6f, want %.6f", b.DataOnly, want)
	}
	if want := oddsToProb(4 * 3 * PenaltySingleRegime); math.Abs(b.Declared-want) > 1e-9 {
		t.Errorf("as-declared posterior = %.6f, want %.6f", b.Declared, want)
	}
	if b.Declared <= b.Posterior {
		t.Error("as-declared posterior must exceed the headline when a promotion was refused")
	}
	if b.Note == "" {
		t.Error("a refused assertion must carry a stated reason, not a silent adjustment")
	}
}

// Breakdown must stay silent when there is nothing to disclose — a permanent
// caveat on every hypothesis trains readers to ignore it.
func TestBreakdownSilentWhenNothingRefused(t *testing.T) {
	b := Breakdown(0.5, []Evidence{{Kind: KindReplication, K: 60, N: 100, P0: 0.5, BF: 4}})
	if b.RefusedRows != 0 || b.Note != "" {
		t.Errorf("clean chain produced refusal note %q (%d rows)", b.Note, b.RefusedRows)
	}
}

// The marker has to reach the payload. /api/research-ledger marshals
// []rl.Evidence directly, so a reader can only tell an assertion from a
// measurement if the row itself says so.
func TestEvidenceJSONMarksAssertedRows(t *testing.T) {
	b, err := json.Marshal(Evidence{HypID: "H006", Kind: KindManual, N: 0, BF: 3})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["asserted"] != true {
		t.Errorf("n=0 row marshaled asserted=%v, want true — payload: %s", got["asserted"], b)
	}
	if got["appliedBf"] != 1.0 {
		t.Errorf("n=0 row marshaled appliedBf=%v, want 1 (the refused weight is visible)", got["appliedBf"])
	}
	if got["bf"] != 3.0 {
		t.Errorf("stored bf changed to %v — the declared number must survive verbatim", got["bf"])
	}

	b2, _ := json.Marshal(Evidence{HypID: "H002", Kind: KindReplication, K: 60, N: 100, P0: 0.5, BF: 4})
	var got2 map[string]any
	_ = json.Unmarshal(b2, &got2)
	if got2["asserted"] != false || got2["appliedBf"] != 4.0 {
		t.Errorf("data-backed row marshaled asserted=%v appliedBf=%v, want false / 4", got2["asserted"], got2["appliedBf"])
	}
}

// Round-tripping must not lose or mutate a row: the ledger's audit claim is
// that the chain IS the belief.
func TestEvidenceJSONRoundTrip(t *testing.T) {
	in := Evidence{HypID: "H002", Ts: 42, Kind: KindBacktest, K: 12, N: 20, P0: 0.5,
		BF: 1.5, Note: "era=bear_2022", WindowFrom: 5, WindowTo: 6}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Evidence
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("round trip changed the row:\n got %+v\nwant %+v", out, in)
	}
}

// ReproducesBF is the audit the ledger did not have: a row carrying k/n/p0 must
// be re-derivable from the exact integral. Run over the live DB on 2026-07-25
// all 28 data-backed rows reproduced — but nothing in the code checked, which
// is precisely why a typed number could not be distinguished from a computed
// one.
func TestReproducesBFAcceptsComputedRows(t *testing.T) {
	// A live backtest row: 1/19 winning weeks vs 0.5 on the week-trial band.
	e := Evidence{Kind: KindBacktest, K: 1, N: 19, P0: 0.5,
		BF: BayesFactorAbove(1, 19, 0.5, WeekTrialMaxEdge)}
	side, ok := ReproducesBF(e, WeekTrialMaxEdge)
	if !ok || side != SideAbove {
		t.Errorf("computed above-band row: side=%q ok=%v, want above/true", side, ok)
	}

	// A live below-band row: the H005 contamination grade.
	e2 := Evidence{Kind: KindManual, K: 1330, N: 2984, P0: 0.5563,
		BF: BayesFactorBelow(1330, 2984, 0.5563, 0.15)}
	if side, ok := ReproducesBF(e2, 0.15); !ok || side != SideBelow {
		t.Errorf("computed below-band row: side=%q ok=%v, want below/true", side, ok)
	}
}

func TestReproducesBFRejectsHandEnteredNumbers(t *testing.T) {
	// Same observation, a BF someone typed instead of computing.
	e := Evidence{Kind: KindBacktest, K: 1, N: 19, P0: 0.5, BF: 7.5}
	if side, ok := ReproducesBF(e, WeekTrialMaxEdge); ok {
		t.Errorf("hand-entered BF on a data-backed row reproduced as %q — the audit is blind", side)
	}
	// A row with no observation cannot be reproduced at all, in either
	// direction; that is the definition of asserted, not a verification pass.
	if _, ok := ReproducesBF(Evidence{Kind: KindManual, N: 0, BF: 3}, DefaultMaxEdge); ok {
		t.Error("an n=0 row reported as reproducible")
	}
}

func oddsToProb(o float64) float64 { return o / (1 + o) }

// A malformed row must report the weight the posterior gives it — 1, i.e. no
// effect — rather than the floor it would clamp to. The payload describes the
// chain; a row shown at 0.05 that actually contributes nothing does not.
func TestMalformedBFAppliesAsNeutral(t *testing.T) {
	for _, bf := range []float64{0, -1, math.NaN()} {
		e := Evidence{Kind: KindExperiment, K: 60, N: 100, P0: 0.5, BF: bf}
		if got := e.AppliedBF(); got != 1 {
			t.Errorf("AppliedBF for stored BF %v = %v, want 1", bf, got)
		}
		if got := Posterior(0.5, []Evidence{e}); math.Abs(got-0.5) > 1e-12 {
			t.Errorf("posterior over a malformed BF %v = %.6f, want 0.5", bf, got)
		}
	}
}
