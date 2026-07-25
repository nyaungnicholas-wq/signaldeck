package structregime

import "testing"

func TestGapFillBucketsAndRefusals(t *testing.T) {
	// 1.5% gap up, not yet filled (low stayed above prior close) — reports
	// the SURVIVOR-CONDITIONAL rate, which sits below the 70% product bar
	f, ok := PredictGapFill(100, 101.5, 102, 100.8)
	if !ok || f.Kind != KindGapFill5 || f.Regime != "gap-up fill expected" {
		t.Fatalf("ok=%v f=%+v", ok, f)
	}
	if f.HistoricalAccuracy != 0.528 {
		t.Fatalf("acc=%v", f.HistoricalAccuracy)
	}
	// same gap already filled intraday -> honest absence
	if _, ok := PredictGapFill(100, 101.5, 102, 99.9); ok {
		t.Fatal("filled gap must not forecast")
	}
	// gap down bucket + conditional accuracy
	f, ok = PredictGapFill(100, 97.5, 98.2, 97.0)
	if !ok || f.Regime != "gap-down fill expected" || f.HistoricalAccuracy != 0.510 {
		t.Fatalf("ok=%v f=%+v", ok, f)
	}
	// no bucket may claim the 70% bar — the reason the worker never emits this
	for _, b := range gapBuckets {
		if b.accUp >= 0.70 || b.accDown >= 0.70 {
			t.Fatalf("bucket %s claims >=0.70 — gapfill must stay unshipped", b.label)
		}
	}
	// noise gap (<0.5%) and news gap (>=10%) refuse
	if _, ok := PredictGapFill(100, 100.3, 101, 100.1); ok {
		t.Fatal("noise gap must refuse")
	}
	if _, ok := PredictGapFill(100, 112, 113, 111); ok {
		t.Fatal("news-sized gap must refuse")
	}
	// degenerate inputs refuse
	if _, ok := PredictGapFill(0, 100, 101, 99); ok {
		t.Fatal("zero prev close must refuse")
	}
}
