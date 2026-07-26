package metalabel

import (
	"errors"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
)

// ── H1 ripple · purged/embargoed walk-forward in the META-model ──────────────
//
// walkForward split train/test with ZERO gap: fold k trained on cands[:trainEnd]
// and was graded on cands[trainEnd:testEnd] the instant after. Index order is not
// time order once labels overlap. The meta-label here is "did the primary's call
// clear cost over its forward horizon", so a row sitting BEFORE the boundary
// carries an answer decided AFTER it — and this platform's candidate rows are
// symbol-days: ~530 rows share a single UTC day, so a boundary cutting a day in
// half hands the tree that day's outcomes and the test block is graded on the
// same day's rows through a feature region they share.
//
// MEASURED on the live database (2026-07-25, read-only), replicating
// pipeline.metaLabelSamples exactly:
//
//	horizon 1d: ~5,870 candidates over 11 distinct days, folds at 60/120/240/
//	            480/960/1920/3840 — 4,402 of 7,620 training rows across folds
//	            (57.8%) carried labels realised inside the block they were about
//	            to be graded on. Folds 0-4 were 100% contaminated.
//	horizon 1w: ~5,610 candidates over 12 distinct days — 7,171 of 7,620 (94.1%),
//	            with folds 0-5 100% contaminated.
//
// The verdicts those grades produced were "rejected" only because the FIRST gate
// (primary has no cost-net edge) fired. The numbers behind the gate were already
// leak-fed: 1d reported filteredExpectancy +0.19% per decision against a primary
// at -0.22%, i.e. a filter that appears to turn a losing signal profitable. That
// is the number that ships the day the primary has any edge at all.
//
// AFTER the purge, on that same live data: 5,436 training rows removed at 1d
// leaving 2 of 7 boundaries trainable, 7,181 removed at 1w leaving 1 of 7 —
// against 4 retrains demanded. Both horizons therefore publish a REFUSAL now
// instead of a grade. That is the finding, not a regression: eleven days of
// history cannot support a purged walk-forward over a one-day label.

// dayClustered builds the live shape: `days` UTC days with `perDay` symbols each,
// one observation per symbol-day, labels resolving `span` seconds later.
//
// The label is a pure function of the DAY — every symbol shares that day's market
// move, which is the actual structure of this platform's data and the reason the
// day, not the row, is the unit of evidence. Context carries a persistent per-day
// marker plus noise, so a tree that has seen ANY row of a day can classify every
// other row of it. There is no cross-day structure to learn, so out-of-sample
// skill is zero by construction and anything a grade reports above the base rate
// came from rows it should not have trained on.
func dayClustered(days, perDay int, span int64) []Sample {
	step := int64(86400) / int64(perDay)
	out := make([]Sample, 0, days*perDay)
	for d := 0; d < days; d++ {
		// Deterministic, non-repeating alternation: no RNG (this repo forbids it
		// where a grade must reproduce), and no pattern a tree can extrapolate to
		// a day it has not seen.
		good := d%2 == 0
		fwd := -0.02
		if good {
			fwd = 0.03
		}
		for k := 0; k < perDay; k++ {
			ts := int64(d)*86400 + int64(k)*step
			out = append(out, Sample{
				Ts:          ts,
				LabelEnd:    ts + span,
				PrimaryProb: 0.62, // a directional long call
				Context:     []float64{float64(d), float64(k%7) / 7.0},
				FwdReturn:   fwd,
			})
		}
	}
	return out
}

// TestEvaluate_RefusesUndeclaredLabelHorizon is the withhold-don't-guess rule
// applied to the meta-model: the purge width is the label horizon, the horizon is
// only knowable from the caller, and a grade that cannot be purged cannot be
// certified free of the overlap leak. metalabel is a PUBLISHED verdict surface,
// so an uncertifiable verdict must be withheld with a reason rather than printed.
func TestEvaluate_RefusesUndeclaredLabelHorizon(t *testing.T) {
	samples := dayClustered(240, 4, 86400)
	for i := range samples {
		samples[i].LabelEnd = 0 // caller never declared its horizon
	}
	if _, err := Evaluate(samples, 4, 0.001, DefaultThreshold); !errors.Is(err, ErrNoLabelSpan) {
		t.Fatalf("Evaluate err = %v, want ErrNoLabelSpan", err)
	}
	if _, _, _, ok := Run(samples, samples[0].Context, 4, 0.001, DefaultThreshold); ok {
		t.Fatal("Run must not gate a decision on a verdict whose grade could not be purged")
	}
}

// TestEvaluate_RefusesWhenPurgeLeavesTooFewRetrains is the LIVE finding encoded.
//
// The live candidate set is ~5,800 symbol-days spanning ELEVEN days. A one-day
// label plus 530 rows per day means every training row within a day of the
// boundary straddles it, and the geometric fold boundaries (60, 120, 240, ...)
// all fall inside the first day or two of history. Purge them honestly and the
// early folds have no training set left at all.
//
// The honest output is then a REFUSAL naming the reason — not a grade computed on
// the one or two folds that survived, and emphatically not a looser gate. A
// filter graded on a single retrain is not walk-forward evidence, and this
// package exists to refuse exactly that kind of flattering arithmetic.
func TestEvaluate_RefusesWhenPurgeLeavesTooFewRetrains(t *testing.T) {
	// 11 days x 96 symbols: the live shape (11 distinct days, ~1,000 symbols),
	// scaled down to keep the boosted-tree fits cheap.
	samples := dayClustered(11, 96, 86400)

	_, err := Evaluate(samples, 4, 0.001, DefaultThreshold)
	if !errors.Is(err, ErrPurgedTooThin) {
		t.Fatalf("Evaluate err = %v, want ErrPurgedTooThin — a history shorter than "+
			"a few label spans cannot support four purged retrains", err)
	}
	if err.Error() == ErrPurgedTooThin.Error() {
		t.Fatal("the refusal must state the measured numbers, not just the sentinel")
	}

	// The same data with the SAME fold geometry grades fine when the rows are
	// spread over enough days for the purge to leave real training sets behind —
	// so the refusal above is about the leak, not about the sample being small.
	spread := dayClustered(11*96, 1, 86400)
	g, err := Evaluate(spread, 4, 0.001, DefaultThreshold)
	if err != nil {
		t.Fatalf("spread over %d days should still grade, got %v", 11*96, err)
	}
	// And when it does grade, the purge must be visible as a number. A published
	// grade reporting zero purged rows on overlapping labels would mean the
	// caller mis-declared its horizon and the certification is worthless.
	if g.PurgedTrainRows == 0 {
		t.Fatal("a graded result must report the training rows the purge removed")
	}
	if g.LabelSpan != 86400 || g.EmbargoSpan != 8640 {
		t.Fatalf("purge report = span %d / embargo %d, want 86400 / 8640", g.LabelSpan, g.EmbargoSpan)
	}
	if g.TrainedFolds < 4 {
		t.Fatalf("graded with %d retrains, below the 4 demanded", g.TrainedFolds)
	}
}

// TestWithLabelSpan_DeclaresHorizonWithoutMutating covers the one-line path a
// caller uses when it assembles Samples from a source that does not carry the
// horizon row by row. It must not touch the caller's slice — a helper that
// mutated its input would silently re-declare a set another grade already used.
func TestWithLabelSpan_DeclaresHorizonWithoutMutating(t *testing.T) {
	samples := dayClustered(80, 4, 86400)
	for i := range samples {
		samples[i].LabelEnd = 0
	}
	declared := WithLabelSpan(samples, 604800)
	for i, s := range declared {
		if s.LabelEnd != s.Ts+604800 {
			t.Fatalf("sample %d: LabelEnd = %d, want %d", i, s.LabelEnd, s.Ts+604800)
		}
	}
	if samples[0].LabelEnd != 0 {
		t.Fatal("WithLabelSpan must not mutate the caller's slice")
	}
	if span, ok := labelSpanOf(declared); !ok || span != 604800 {
		t.Fatalf("labelSpanOf(declared) = %d,%v want 604800,true", span, ok)
	}
	// A wider horizon already on a row must survive: the purge is sized by the
	// WIDEST label present, so a narrower re-declaration must not shrink it.
	mixed := append([]Sample{}, samples...)
	mixed[0].LabelEnd = mixed[0].Ts + 999999
	if got := WithLabelSpan(mixed, 604800)[0].LabelEnd; got != mixed[0].Ts+999999 {
		t.Fatalf("a wider declared horizon was narrowed to %d", got)
	}
}

// TestPurgedTrain_NoTrainingLabelReachesTestBlock is the structural guarantee,
// asserted directly rather than inferred from a statistic: on a set whose labels
// overlap the fold boundaries, the training rows walkForward actually fits on
// must contain none whose outcome resolves inside the test block, nor inside the
// embargo gap in front of it.
//
// The second half is what makes it a regression test rather than a tautology: the
// SAME boundaries on the SAME data, split the old zero-gap way, are shown to be
// contaminated. If a future change quietly stops purging, the first loop fails;
// if a future fixture stops exercising overlap, the second loop fails and the
// first stops meaning anything.
func TestPurgedTrain_NoTrainingLabelReachesTestBlock(t *testing.T) {
	const span = int64(86400)
	cands := directional(dayClustered(60, 24, span))
	ms := make([]gbm.Sample, len(cands))
	for i, c := range cands {
		ms[i] = gbm.Sample{Ts: c.Ts, LabelEnd: c.LabelEnd, Feat: c.Context, Y: metaLabel(c, 0.001)}
	}
	embargo := embargoFor(span)
	if embargo <= 0 {
		t.Fatalf("a %d-second label span must earn a positive embargo, got %d", span, embargo)
	}

	n := len(ms)
	contaminatedUnpurged := 0
	for _, trainEnd := range foldBoundaries(n) {
		testStart := ms[trainEnd].Ts
		for _, s := range purgedTrain(ms, trainEnd, testStart, span, embargo) {
			// One violation is one training row whose outcome was decided by the
			// very prices the test block is about to be graded on.
			if s.LabelEnd > testStart-embargo {
				t.Fatalf("boundary %d: retained training row ts=%d labelEnd=%d reaches test block at %d (embargo %d) — LEAKAGE",
					trainEnd, s.Ts, s.LabelEnd, testStart, embargo)
			}
		}
		for i := 0; i < trainEnd; i++ {
			if ms[i].LabelEnd > testStart {
				contaminatedUnpurged++
			}
		}
	}
	if contaminatedUnpurged == 0 {
		t.Fatal("fixture no longer overlaps its fold boundaries — it cannot demonstrate the defect it guards")
	}
	t.Logf("zero-gap split would have trained on %d rows whose labels resolve inside their own test block", contaminatedUnpurged)
}
