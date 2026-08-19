package store

import (
	"context"
	"fmt"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Case A -- the live 2026-08-15 defect. The calibrator squashed every symbol
// into a band BELOW 0.5 (1w 2026-08-03: the whole cross-section in
// [0.104, 0.381]), so `prob >= 0.5` called DOWN on the entire book while the
// market rose. Accuracy then equals 1-UpRate by ARITHMETIC, not anti-skill.
//
// Re-thresholding does NOT rescue this case: a band below 0.5 is also below
// the base rate, so every call stays DOWN. The only honest response is to
// REFUSE it -- which is what OneSided exists to say.
func TestDirectionalRecord_OneSidedBookIsFlagged(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	const days, perDay = 12, 40
	for d := 0; d < days; d++ {
		rising := d%3 != 0 // 8 of 12 days rise -> a real up-drift
		for i := 0; i < perDay; i++ {
			sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("S%03d", i), md.Stocks, "x")
			if err != nil {
				t.Fatalf("symbol: %v", err)
			}
			p := 0.42 + 0.07*float64(i)/float64(perDay) // [0.42, 0.49]
			fwd := -0.01
			if rising {
				fwd = 0.01
			}
			seedResolvedPred(t, st, sym.ID, md.H1d, int64(d+1)*86400+int64(i), p, p, fwd)
		}
	}
	r, err := st.DirectionalRecord(ctx, md.H1d, 0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if r.N == 0 {
		t.Fatal("no graded rows")
	}
	if diff := r.Accuracy - (1 - r.UpRate); diff > 0.02 || diff < -0.02 {
		t.Fatalf("accuracy %.4f must equal 1-upRate %.4f by arithmetic", r.Accuracy, 1-r.UpRate)
	}
	if !r.OneSided {
		t.Fatalf("a unanimous book must be flagged OneSided (agreement %.3f)", r.Agreement)
	}
	if r.Agreement < 0.99 {
		t.Fatalf("every call points the same way; want agreement ~1.0, got %.3f", r.Agreement)
	}
	// Documents the LIMIT of the threshold fix: a band below 0.5 is below the
	// base rate too, so re-grading cannot save it. Refusal is the fix here.
	if r.AccuracyAtBase != r.Accuracy {
		t.Fatalf("a wholly-below band cannot be rescued by re-thresholding: %.4f vs %.4f",
			r.AccuracyAtBase, r.Accuracy)
	}
}

// Case B -- where the base-rate threshold DOES earn its keep. The band sits
// entirely ABOVE 0.5 but straddles the 0.575 base rate. At 0.5 the book reads
// unanimously UP and scores exactly the up-rate, which looks unremarkable and
// hides real cross-sectional discrimination. At the base rate it splits.
func TestDirectionalRecord_BaseRateThresholdRecoversDiscrimination(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	const days, perDay = 12, 40
	for d := 0; d < days; d++ {
		for i := 0; i < perDay; i++ {
			sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("D%03d", i), md.Stocks, "x")
			if err != nil {
				t.Fatalf("symbol: %v", err)
			}
			p := 0.52 + 0.10*float64(i)/float64(perDay) // [0.52, 0.62], all > 0.5
			fwd := -0.01
			if i >= 17 { // 23/40 = 0.575 up-rate, and the model ranks it
				fwd = 0.01
			}
			seedResolvedPred(t, st, sym.ID, md.H1d, int64(d+1)*86400+int64(i), p, p, fwd)
		}
	}
	r, err := st.DirectionalRecord(ctx, md.H1d, 0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	// At 0.5 every name is an UP call, so accuracy collapses onto the up-rate.
	if diff := r.Accuracy - r.UpRate; diff > 0.02 || diff < -0.02 {
		t.Fatalf("unanimous-UP book should score the up-rate: acc %.4f vs upRate %.4f",
			r.Accuracy, r.UpRate)
	}
	// At the base rate the same rows discriminate.
	if r.AccuracyAtBase <= r.Accuracy+0.10 {
		t.Fatalf("base-rate threshold must recover discrimination: %.4f vs %.4f",
			r.AccuracyAtBase, r.Accuracy)
	}
	if r.OneSided {
		t.Fatalf("book splits at the base rate; must not be flagged (agreement %.3f)", r.Agreement)
	}
}

// A genuinely balanced book must NOT trip the flag. A guard that is
// red-by-construction gets ignored, which is how the last one died.
func TestDirectionalRecord_BalancedBookIsNotFlagged(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	for d := 0; d < 12; d++ {
		for i := 0; i < 40; i++ {
			sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("B%03d", i), md.Stocks, "x")
			if err != nil {
				t.Fatalf("symbol: %v", err)
			}
			p := 0.30 + 0.40*float64(i)/float64(40)
			fwd := -0.01
			if i%2 == 0 {
				fwd = 0.01
			}
			seedResolvedPred(t, st, sym.ID, md.H1d, int64(d+1)*86400+int64(i), p, p, fwd)
		}
	}
	r, err := st.DirectionalRecord(ctx, md.H1d, 0)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if r.OneSided {
		t.Fatalf("a 0.30-0.70 book is a real cross-section (agreement %.3f)", r.Agreement)
	}
}
