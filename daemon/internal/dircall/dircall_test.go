package dircall

import "testing"

// The defect these tests exist for, measured on the live DB 2026-08-15:
// the calibrator squashed the whole cross-section into a narrow band BELOW
// 0.5 (e.g. 1w 2026-08-03: every symbol in [0.104, 0.381]), so a hard 0.5
// threshold read "below average" as SHORT EVERYTHING and published one market
// call as ~325 forecasts. Live accuracy then equals 1 - the up-rate by
// arithmetic: 43.3% against a 56.5% null.

func TestThresholdIsTheBaseRateNotAHalf(t *testing.T) {
	if got := Threshold(0.565); got != 0.565 {
		t.Fatalf("threshold must be the prevailing base rate, got %v", got)
	}
	// A 0.48 probability against a 56.5%% up-rate is BELOW AVERAGE, not short.
	if Call(0.48, Threshold(0.565)) {
		t.Fatal("0.48 vs a 0.565 base rate should not be an UP call")
	}
	if !Call(0.58, Threshold(0.565)) {
		t.Fatal("0.58 vs a 0.565 base rate should be an UP call")
	}
	// The same probability against a 0.40 base rate IS an up call. This is the
	// whole point: the call is relative, not absolute.
	if !Call(0.48, Threshold(0.40)) {
		t.Fatal("0.48 vs a 0.40 base rate should be an UP call")
	}
}

func TestThresholdClampedToASaneBand(t *testing.T) {
	// A degenerate window must not push the boundary to 0 or 1 and make every
	// call trivially one-way.
	if got := Threshold(0.0); got < 0.3 || got > 0.7 {
		t.Fatalf("threshold must clamp into [0.3,0.7], got %v", got)
	}
	if got := Threshold(1.0); got < 0.3 || got > 0.7 {
		t.Fatalf("threshold must clamp into [0.3,0.7], got %v", got)
	}
}

func TestAgreementAndStraddle(t *testing.T) {
	// The measured pathology: every probability below the threshold.
	oneSided := []float64{0.104, 0.20, 0.31, 0.381, 0.44}
	if Straddles(oneSided, 0.5) {
		t.Fatal("a cross-section entirely below the threshold does not straddle")
	}
	if a := Agreement(oneSided, 0.5); a != 1.0 {
		t.Fatalf("unanimous book must report agreement 1.0, got %v", a)
	}
	balanced := []float64{0.30, 0.45, 0.55, 0.70}
	if !Straddles(balanced, 0.5) {
		t.Fatal("a balanced cross-section straddles")
	}
	if a := Agreement(balanced, 0.5); a != 0.5 {
		t.Fatalf("2 up / 2 down must report agreement 0.5, got %v", a)
	}
}

func TestPublishableRefusesTheOneSidedBook(t *testing.T) {
	// 1w 2026-08-03 shape: 325 names, every one below 0.5.
	probs := make([]float64, 325)
	for i := range probs {
		probs[i] = 0.10 + 0.28*float64(i)/325.0 // spans [0.10, 0.38]
	}
	if ok, _ := Publishable(probs, 0.5, DefaultMaxAgreement); ok {
		t.Fatal("a unanimous 325-name book is ONE market call, not 325 forecasts")
	}
	// A healthy day (measured agreement ~0.75-0.86) must still publish.
	healthy := make([]float64, 300)
	for i := range healthy {
		if i < 220 {
			healthy[i] = 0.55
		} else {
			healthy[i] = 0.45
		}
	}
	if ok, why := Publishable(healthy, 0.5, DefaultMaxAgreement); !ok {
		t.Fatalf("a 73%%-agreement book is a real cross-section, must publish: %s", why)
	}
	if ok, _ := Publishable(nil, 0.5, DefaultMaxAgreement); ok {
		t.Fatal("an empty cross-section is not publishable")
	}
}

func TestRefusalCarriesAReason(t *testing.T) {
	probs := []float64{0.1, 0.2, 0.3}
	ok, why := Publishable(probs, 0.5, DefaultMaxAgreement)
	if ok || why == "" {
		t.Fatal("a refusal must explain itself; a silent refusal is how this defect survived")
	}
}

func TestDefaultBoundSitsBetweenMeasuredHealthyAndBroken(t *testing.T) {
	// Healthy days measured 0.75-0.86 agreement; broken days 0.95-1.00.
	if DefaultMaxAgreement <= 0.86 || DefaultMaxAgreement >= 0.95 {
		t.Fatalf("bound %v must separate measured healthy (<=0.86) from broken (>=0.95)",
			DefaultMaxAgreement)
	}
}
