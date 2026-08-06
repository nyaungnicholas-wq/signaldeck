package pipeline

import "testing"

// cellKey decides which labeller names the adaptive weight cell, so getting it
// wrong silently repoints every learned weight. The default must stay the
// rule-based label: the HMM is a measured-better VOLATILITY labeller, which is
// not evidence that keying DIRECTIONAL weights by it helps.
func TestCellKeyDefaultsToRuleLabel(t *testing.T) {
	t.Setenv("SIGNALDECK_HMM_CELLS", "")
	if got := cellKey("squeeze", "turbulent"); got != "squeeze" {
		t.Errorf("cellKey with switch off = %q, want %q", got, "squeeze")
	}
	if got := cellKey("", "turbulent"); got != "" {
		t.Errorf("cellKey with switch off and no rule label = %q, want empty", got)
	}
}

func TestCellKeyUsesHMMWhenEnabled(t *testing.T) {
	t.Setenv("SIGNALDECK_HMM_CELLS", "1")
	got := cellKey("squeeze", "turbulent")
	if want := HMMCellPrefix + "turbulent"; got != want {
		t.Errorf("cellKey with switch on = %q, want %q", got, want)
	}
	// The prefix is what keeps cells learned under the two labellers from
	// pooling into one name when the switch is flipped.
	if got == "turbulent" {
		t.Error("HMM cell key is unprefixed; it would collide with rule-label cells")
	}
}

// Enabling the switch must never blank out a cell key: a symbol the HMM runner
// has not reached yet still has to land in its rule-based cell rather than in
// the unnamed one.
func TestCellKeyFallsBackWhenHMMMissing(t *testing.T) {
	t.Setenv("SIGNALDECK_HMM_CELLS", "1")
	if got := cellKey("squeeze", ""); got != "squeeze" {
		t.Errorf("cellKey with no HMM label = %q, want %q", got, "squeeze")
	}
}

func TestHMMCellsEnabledAcceptsOnlyExplicitOptIn(t *testing.T) {
	for _, v := range []string{"", "0", "false", "no", "off", "maybe"} {
		t.Setenv("SIGNALDECK_HMM_CELLS", v)
		if hmmCellsEnabled() {
			t.Errorf("SIGNALDECK_HMM_CELLS=%q enabled the switch, want off", v)
		}
	}
	for _, v := range []string{"1", "true", "TRUE", "yes"} {
		t.Setenv("SIGNALDECK_HMM_CELLS", v)
		if !hmmCellsEnabled() {
			t.Errorf("SIGNALDECK_HMM_CELLS=%q left the switch off, want on", v)
		}
	}
}
