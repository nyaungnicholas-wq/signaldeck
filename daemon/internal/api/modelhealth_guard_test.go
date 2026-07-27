package api

import "testing"

// An inverted or relabeled variant of a retired model must never reach the
// wire with emitting=true unless the canary re-admission gate has explicitly
// readmitted it. Inversion is not a rescue: 48.1% flips to 51.9%, still below
// the 54.6% majority-class null, so a variant that skips the gate is the same
// dead model under a new name.
func TestDerivedVariantCannotEmitWithoutReadmission(t *testing.T) {
	cases := []string{
		"directional-ensemble-1d-inverted",
		"directional-ensemble-1w-relabeled",
		"structural-trend21-flipped",
	}
	for _, name := range cases {
		v := guardDerivedVariant(map[string]any{
			"model": name, "emitting": true, "verdict": "healthy",
		})
		if emitting, _ := v["emitting"].(bool); emitting {
			t.Errorf("%s: derived variant emitted without re-admission", name)
		}
		if v["verdict"] != "retired" {
			t.Errorf("%s: verdict = %v, want retired", name, v["verdict"])
		}
	}
}

func TestReadmittedVariantMayEmit(t *testing.T) {
	v := guardDerivedVariant(map[string]any{
		"model": "directional-ensemble-1d-inverted", "emitting": true, "readmitted": true,
	})
	if emitting, _ := v["emitting"].(bool); !emitting {
		t.Error("a variant that passed the re-admission gate must keep its graded emitting state")
	}
}

func TestOriginalModelPassesThroughUntouched(t *testing.T) {
	v := guardDerivedVariant(map[string]any{
		"model": "directional-ensemble-1d", "emitting": false, "verdict": "retired",
	})
	if v["verdict"] != "retired" || v["emitting"] != false {
		t.Error("guard must not rewrite non-variant models")
	}
	if _, hasNote := v["note"]; hasNote {
		t.Error("guard must not annotate non-variant models")
	}
}
