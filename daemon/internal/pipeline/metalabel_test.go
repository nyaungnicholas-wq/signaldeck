package pipeline

import (
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The meta-model must never see the primary's own output. If the call itself
// leaked into the context, a filter could score well by simply re-reading it,
// which would look like skill and be a tautology.
func TestMetaLabelContextExcludesTheCall(t *testing.T) {
	rows := []store.LabeledFeature{{
		SymbolID: 1, Horizon: md.H1d, Ts: 86400, Version: 8,
		Vec: map[string]float64{
			"pred_cal": 0.62, "pred_raw": 0.60, "gbm_prob": 0.55,
			"meanrev_prob": 0.48, "alphax_prob": 0.51,
			"vix_level": 0.18, "adx14": 22, "rank_pct": 0.7,
		},
		FwdReturn: 0.01,
	}}
	_, ctxKeys := metaLabelSamples(rows, horizonSecs(md.H1d))
	for _, banned := range []string{"pred_cal", "pred_raw", "gbm_prob", "meanrev_prob", "alphax_prob"} {
		for _, k := range ctxKeys {
			if k == banned {
				t.Fatalf("context must not contain the model's own output %q", banned)
			}
		}
	}
	if len(ctxKeys) == 0 {
		t.Fatal("context should still carry the circumstance features")
	}
}

// One observation per symbol per UTC day. Pooling intraday rows inflates n
// roughly 60x on this platform and has manufactured false significance twice;
// the worker must collapse them before anything is graded.
func TestMetaLabelDedupesToSymbolDay(t *testing.T) {
	day := int64(86400)
	rows := []store.LabeledFeature{
		// Three intraday rows for symbol 1 on the same UTC day.
		{SymbolID: 1, Ts: day + 100, Vec: map[string]float64{"pred_cal": 0.60, "vix_level": 0.1}, FwdReturn: 0.01},
		{SymbolID: 1, Ts: day + 200, Vec: map[string]float64{"pred_cal": 0.61, "vix_level": 0.1}, FwdReturn: 0.01},
		{SymbolID: 1, Ts: day + 300, Vec: map[string]float64{"pred_cal": 0.62, "vix_level": 0.1}, FwdReturn: 0.01},
		// One row for symbol 2 the same day, and symbol 1 again the next day.
		{SymbolID: 2, Ts: day + 150, Vec: map[string]float64{"pred_cal": 0.40, "vix_level": 0.1}, FwdReturn: -0.01},
		{SymbolID: 1, Ts: 2*day + 100, Vec: map[string]float64{"pred_cal": 0.58, "vix_level": 0.1}, FwdReturn: 0.02},
	}
	samples, _ := metaLabelSamples(rows, horizonSecs(md.H1d))
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want 3 (symbol-day pairs), intraday rows were not collapsed", len(samples))
	}
	// The LATEST row of a symbol-day is the one kept.
	var found bool
	for _, s := range samples {
		if s.Ts == day+300 && s.PrimaryProb == 0.62 {
			found = true
		}
		if s.Ts == day+100 || s.Ts == day+200 {
			t.Fatalf("kept a superseded intraday row at ts=%d", s.Ts)
		}
	}
	if !found {
		t.Fatal("the latest row of the symbol-day should be retained")
	}
}

// Samples must reach the grader in ascending time order — the walk-forward
// no-lookahead guarantee is defined entirely by that ordering, and the engine
// refuses unsorted input rather than silently grading it.
func TestMetaLabelSamplesAreSorted(t *testing.T) {
	rows := []store.LabeledFeature{
		{SymbolID: 1, Ts: 5 * 86400, Vec: map[string]float64{"pred_cal": 0.6, "vix_level": 0.1}, FwdReturn: 0.01},
		{SymbolID: 2, Ts: 1 * 86400, Vec: map[string]float64{"pred_cal": 0.6, "vix_level": 0.1}, FwdReturn: 0.01},
		{SymbolID: 3, Ts: 3 * 86400, Vec: map[string]float64{"pred_cal": 0.6, "vix_level": 0.1}, FwdReturn: 0.01},
	}
	samples, _ := metaLabelSamples(rows, horizonSecs(md.H1d))
	for i := 1; i < len(samples); i++ {
		if samples[i].Ts < samples[i-1].Ts {
			t.Fatalf("samples not ascending at %d: %d < %d", i, samples[i].Ts, samples[i-1].Ts)
		}
	}
}

// Every candidate must declare when its outcome resolved. Without it
// metalabel.Evaluate cannot purge and refuses to grade at all, so a regression
// that dropped the declaration would not produce a wrong number — it would turn
// the whole surface off, and the reason would be one indirection away from the
// worker that stopped publishing.
func TestMetaLabelDeclaresTheLabelHorizon(t *testing.T) {
	for _, h := range metaLabelHorizons {
		rows := []store.LabeledFeature{
			{SymbolID: 1, Horizon: h, Ts: 86400, Vec: map[string]float64{"pred_cal": 0.62, "vix_level": 0.1}, FwdReturn: 0.01},
			{SymbolID: 2, Horizon: h, Ts: 2 * 86400, Vec: map[string]float64{"pred_cal": 0.38, "vix_level": 0.2}, FwdReturn: -0.01},
		}
		samples, _ := metaLabelSamples(rows, horizonSecs(h))
		if len(samples) != 2 {
			t.Fatalf("%s: got %d samples, want 2", h, len(samples))
		}
		for _, s := range samples {
			// The horizon must match the GBM leg's declaration exactly: the same
			// rows graded through the same learner with two different purge widths
			// would make the two verdicts incomparable.
			if want := s.Ts + horizonSecs(h); s.LabelEnd != want {
				t.Fatalf("%s: LabelEnd = %d, want %d (ts + horizon)", h, s.LabelEnd, want)
			}
		}
	}
}

// A row with no primary call is not a candidate: there is no decision to accept
// or reject, and inventing one would put a phantom trade into the grade.
func TestMetaLabelSkipsRowsWithoutAPrimaryCall(t *testing.T) {
	rows := []store.LabeledFeature{
		{SymbolID: 1, Ts: 86400, Vec: map[string]float64{"vix_level": 0.1, "adx14": 20}, FwdReturn: 0.01},
		{SymbolID: 2, Ts: 2 * 86400, Vec: map[string]float64{"pred_raw": 0.7, "vix_level": 0.1}, FwdReturn: 0.01},
	}
	samples, _ := metaLabelSamples(rows, horizonSecs(md.H1d))
	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1 — a row with no pred_cal/pred_raw is not a candidate", len(samples))
	}
	if samples[0].PrimaryProb != 0.7 {
		t.Fatalf("PrimaryProb = %v, want 0.7 (pred_raw fallback)", samples[0].PrimaryProb)
	}
}
