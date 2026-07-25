package newssent

import (
	"math"
	"testing"
)

func TestNoPolarityTermsIsAbsentNotNeutral(t *testing.T) {
	// The single most important behaviour: a factual headline has NO sentiment.
	// If these came back Polar with value 0 they would be averaged into the
	// feature as real observations, diluting genuine opinions and inflating the
	// sample size — which is how a study reports a confident nothing.
	for _, h := range []string{
		"Apple to report fiscal Q3 results on Thursday",
		"Nvidia CEO to speak at conference",
		"Tesla files 8-K",
		"",
	} {
		got := Rate(h)
		if got.Polar {
			t.Errorf("Rate(%q).Polar = true, want false (no polarity words)", h)
		}
		if got.Matched != 0 {
			t.Errorf("Rate(%q).Matched = %d, want 0", h, got.Matched)
		}
	}
}

func TestDirectionalPolarity(t *testing.T) {
	cases := []struct {
		headline string
		wantSign int // +1 bullish, -1 bearish
	}{
		{"Acme beats earnings estimates, raises guidance", +1},
		{"Acme stock surges to record high on strong demand", +1},
		{"FDA approves Acme's new drug", +1},
		{"Acme announces $2B buyback", +1},
		{"Acme misses estimates and cuts full-year guidance", -1},
		{"Acme shares plunge after profit warning", -1},
		{"Acme files for Chapter 11 bankruptcy", -1},
		{"SEC opens investigation into Acme accounting", -1},
		{"Acme recalls 2 million units", -1},
		{"Analysts downgrade Acme on weak demand", -1},
	}
	for _, c := range cases {
		got := Rate(c.headline)
		if !got.Polar {
			t.Fatalf("Rate(%q) not polar; expected a reading", c.headline)
		}
		if sign(got.Value) != c.wantSign {
			t.Errorf("Rate(%q) = %+.3f (sign %d), want sign %d",
				c.headline, got.Value, sign(got.Value), c.wantSign)
		}
	}
}

func TestNegationFlipsPolarity(t *testing.T) {
	// A bag-of-words scorer gets these exactly backwards, and they are common
	// phrasings in earnings coverage.
	cases := []struct{ headline string }{
		{"Acme fails to beat estimates"},
		{"Acme not profitable despite record revenue"},
		{"Acme unable to secure approval"},
	}
	for _, c := range cases {
		got := Rate(c.headline)
		if !got.Polar {
			t.Fatalf("Rate(%q) not polar", c.headline)
		}
		if got.Value >= 0 {
			t.Errorf("Rate(%q) = %+.3f, want negative (negated positive)", c.headline, got.Value)
		}
	}
}

func TestHedgingShrinksMagnitude(t *testing.T) {
	firm := Rate("Acme cuts guidance")
	hedged := Rate("Acme may cut guidance, sources say")
	if !firm.Polar || !hedged.Polar {
		t.Fatal("both should be polar")
	}
	if !hedged.Hedged {
		t.Error("hedged headline should set Hedged")
	}
	if math.Abs(hedged.Value) >= math.Abs(firm.Value) {
		t.Errorf("hedged |%.3f| should be weaker than firm |%.3f| — a rumour is weaker evidence",
			hedged.Value, firm.Value)
	}
}

func TestPhraseNotDoubleCountedAsWords(t *testing.T) {
	// "guidance cut" is a phrase AND contains the negative word "cut". Scoring
	// both would double-count the same piece of information.
	got := Rate("Acme issues guidance cut")
	if got.Matched != 1 {
		t.Errorf("Matched = %d, want 1 (phrase blanks its own span)", got.Matched)
	}
}

func TestDeterministic(t *testing.T) {
	// Reproducibility is the reason this exists instead of the LLM tagger:
	// stored scores must never change without a Version bump.
	h := "Acme beats estimates but warns on margins amid a possible probe"
	first := Rate(h)
	for i := 0; i < 50; i++ {
		if got := Rate(h); got != first {
			t.Fatalf("iteration %d differs: %+v vs %+v", i, got, first)
		}
	}
}

func TestValueStaysInRange(t *testing.T) {
	// A pile-on headline must not escape [-1,+1] and break downstream math.
	h := "record surge beats tops soars rallies jumps profit growth wins approval breakthrough"
	got := Rate(h)
	if got.Value < -1 || got.Value > 1 {
		t.Errorf("Value = %f, out of [-1,+1]", got.Value)
	}
	neg := Rate("plunge bankruptcy fraud lawsuit recall halt default delisting layoffs writedown")
	if neg.Value < -1 || neg.Value > 1 {
		t.Errorf("Value = %f, out of [-1,+1]", neg.Value)
	}
}

func TestLabelMatchesValue(t *testing.T) {
	if l := Rate("Acme to hold annual meeting").Label(); l != "" {
		t.Errorf("non-polar Label = %q, want empty", l)
	}
	if l := Rate("Acme beats estimates and raises guidance").Label(); l != "bullish" {
		t.Errorf("Label = %q, want bullish", l)
	}
	if l := Rate("Acme files for bankruptcy").Label(); l != "bearish" {
		t.Errorf("Label = %q, want bearish", l)
	}
}

func sign(f float64) int {
	if f > 0 {
		return 1
	}
	if f < 0 {
		return -1
	}
	return 0
}
