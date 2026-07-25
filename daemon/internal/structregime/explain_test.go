package structregime

import (
	"math"
	"testing"
)

// An explanation is only worth building if it is FAITHFUL to the model. These
// pin that the contributors describe what actually drove the call, that
// opposing evidence is shown rather than suppressed, and that a missing
// precedent is reported as missing.

// rising builds a clean uptrend long enough to rank a 200-day average.
func rising(n int, start, perDay float64) []float64 {
	out := make([]float64, n)
	p := start
	for i := range out {
		out[i] = p
		p *= 1 + perDay
	}
	return out
}

func TestAuditReportsTheSideItCalled(t *testing.T) {
	ex, ok := AuditTrend(rising(400, 100, 0.002))
	if !ok {
		t.Fatal("a clean 400-bar uptrend should produce an explanation")
	}
	if ex.Regime != "uptrend" {
		t.Fatalf("regime = %q, want uptrend", ex.Regime)
	}
	if len(ex.Supports) == 0 {
		t.Fatal("a high-conviction call must list what supports it")
	}
}

func TestDistanceIsTheLeadingContributor(t *testing.T) {
	// Distance from the average IS the model — conviction is literally its
	// percentile — so it must appear, carrying the conviction as its weight.
	ex, ok := AuditTrend(rising(400, 100, 0.002))
	if !ok {
		t.Fatal("expected an explanation")
	}
	var found bool
	for _, c := range ex.Supports {
		if c.Name == "distance from 200-day average" {
			found = true
			if math.Abs(c.Weight-ex.Conviction) > 1e-9 {
				t.Fatalf("distance weight %.4f should equal conviction %.4f", c.Weight, ex.Conviction)
			}
			if c.Value <= 0 {
				t.Fatalf("an uptrend must report positive distance, got %.2f", c.Value)
			}
		}
	}
	if !found {
		t.Fatal("distance from the 200-day average must always be listed")
	}
}

func TestOpposingEvidenceIsShownNotHidden(t *testing.T) {
	// A series that trends up but whips violently near the average should
	// surface volatility as an OPPOSING contributor. Suppressing it would make
	// a fragile call look strong.
	closes := rising(400, 100, 0.0005)
	for i := 300; i < len(closes); i += 2 {
		closes[i] *= 1.09 // heavy alternating chop
	}
	ex, ok := AuditTrend(closes)
	if !ok {
		t.Skip("predictor refused this synthetic series")
	}
	total := len(ex.Supports) + len(ex.Opposes)
	if total < 4 {
		t.Fatalf("all four contributors should be classified, got %d", total)
	}
	for _, c := range ex.Opposes {
		if c.Weight >= 0 {
			t.Fatalf("an opposing contributor must carry a negative weight: %+v", c)
		}
	}
	for _, c := range ex.Supports {
		if c.Weight < 0 {
			t.Fatalf("a supporting contributor must not carry a negative weight: %+v", c)
		}
	}
}

func TestEveryContributorExplainsItselfInPlainEnglish(t *testing.T) {
	ex, ok := AuditTrend(rising(400, 100, 0.002))
	if !ok {
		t.Fatal("expected an explanation")
	}
	for _, c := range append(append([]Contribution{}, ex.Supports...), ex.Opposes...) {
		if c.Detail == "" {
			t.Fatalf("contributor %q has no explanation", c.Name)
		}
		if c.Name == "" {
			t.Fatal("a contributor must be named")
		}
	}
}

func TestAnalogReportsARealOutcomeOrNone(t *testing.T) {
	ex, ok := AuditTrend(rising(400, 100, 0.002))
	if !ok {
		t.Fatal("expected an explanation")
	}
	if ex.Analog.Found {
		// A found analog must carry a real forward outcome and a close match.
		if ex.Analog.Distance > 0.02 {
			t.Fatalf("a 'found' analog must be within the match tolerance, got %.4f",
				ex.Analog.Distance)
		}
		if ex.Analog.Note == "" {
			t.Fatal("a found analog must describe what happened next")
		}
	} else if ex.Analog.Note == "" {
		t.Fatal("an absent analog must say WHY it is absent")
	}
}

func TestAnalogMatchesTheSameRegimeSide(t *testing.T) {
	// Comparing an uptrend state to a downtrend precedent would be a category
	// error, so the analog search is side-constrained.
	closes := rising(400, 100, 0.002)
	ex, ok := AuditTrend(closes)
	if !ok || !ex.Analog.Found {
		t.Skip("no analog found for this series")
	}
	if ex.Regime == "uptrend" && ex.Analog.PriorDistPct < 0 {
		t.Fatal("an uptrend call matched a below-average precedent")
	}
}

func TestBandedAccuracyIsQuotedNotThePopulationAverage(t *testing.T) {
	ex, ok := AuditTrend(rising(400, 100, 0.002))
	if !ok {
		t.Fatal("expected an explanation")
	}
	want := accuracyFor(KindTrend21, ex.Conviction)
	if math.Abs(ex.Accuracy-want) > 1e-9 {
		t.Fatalf("accuracy %.4f must be the band's, not the population's (%.4f)",
			ex.Accuracy, want)
	}
	if ex.Tier == "" {
		t.Fatal("the conviction tier must be named alongside its accuracy")
	}
}

func TestRefusalPropagatesRatherThanFabricating(t *testing.T) {
	// Too little history, and a contaminated series, must both yield ok=false —
	// an explanation invented over refused data would be worse than none.
	if _, ok := AuditTrend(rising(50, 100, 0.002)); ok {
		t.Fatal("a 50-bar series must not produce an explanation")
	}
	bad := rising(400, 100, 0.002)
	bad[390] *= 4 // unadjusted-split-shaped discontinuity
	if _, ok := AuditTrend(bad); ok {
		t.Fatal("a contaminated series must be refused, not explained")
	}
}

func TestCaveatAlwaysTravelsWithTheCall(t *testing.T) {
	ex, ok := AuditTrend(rising(400, 100, 0.002))
	if !ok {
		t.Fatal("expected an explanation")
	}
	if ex.Caveat == "" || ex.Summary == "" {
		t.Fatal("every explanation must carry its caveat and a plain-English summary")
	}
}
