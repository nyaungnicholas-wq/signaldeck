// The pre-registration is only worth something if two things hold: the frozen
// claims match what the predictors actually advertise TODAY (otherwise the
// record documents a fiction), and the chain detects tampering (otherwise it is
// a timestamp with extra steps). Both are asserted here.
package prereg

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// The frozen band claims must equal what structregime actually attaches to a
// forecast at that conviction. If someone edits an accuracy table without
// amending the pre-registration, this fails — which is the entire mechanism.
func TestFrozenClaimsMatchLivePredictors(t *testing.T) {
	// A conviction comfortably inside each band, so the test reads the band the
	// claim names rather than a boundary.
	probe := map[float64]float64{0.0: 0.25, 0.5: 0.65, 0.8: 0.85, 0.9: 0.95}

	for _, spec := range Specs() {
		kind := structregime.Kind(spec.Kind)
		for _, band := range spec.Bands {
			conv, ok := probe[band.MinConviction]
			if !ok {
				t.Fatalf("%s: band floor %v has no probe conviction", spec.Kind, band.MinConviction)
			}
			got := structregime.AccuracyForTest(kind, conv)
			if got == 0 {
				continue // kind not served by the shared table (crypto tables live elsewhere)
			}
			if got != band.Claimed {
				t.Errorf("%s band >=%v: pre-registered %.3f but the predictor now advertises %.3f.\n"+
					"The frozen record is the commitment. If the change is deliberate, APPEND an "+
					"amendment record — do not edit the claim to match the code, which is the exact "+
					"move pre-registration exists to prevent.",
					spec.Kind, band.MinConviction, band.Claimed, got)
			}
		}
	}
}

// Bands must be ascending and monotone: a higher-conviction call may not claim
// a lower accuracy, or the conviction score means nothing.
func TestBandsAreMonotone(t *testing.T) {
	for _, spec := range Specs() {
		for i := 1; i < len(spec.Bands); i++ {
			if spec.Bands[i].MinConviction <= spec.Bands[i-1].MinConviction {
				t.Errorf("%s: band floors not ascending at %d", spec.Kind, i)
			}
			if spec.Bands[i].Claimed < spec.Bands[i-1].Claimed {
				t.Errorf("%s: claimed accuracy DROPS from %.3f to %.3f as conviction rises",
					spec.Kind, spec.Bands[i-1].Claimed, spec.Bands[i].Claimed)
			}
		}
	}
}

// Every spec must carry a baseline and a known weakness. A claim with no stated
// null is unfalsifiable in practice: several of these predictors score high
// precisely because the underlying state is sticky, and without the persistence
// baseline recorded alongside, a 0.876 reads as skill when it is not.
func TestEverySpecStatesItsNullAndItsWeakness(t *testing.T) {
	for _, spec := range Specs() {
		if spec.Baseline == "" {
			t.Errorf("%s: no baseline stated — the accuracy has nothing to be measured against", spec.Kind)
		}
		if spec.KnownWeakness == "" {
			t.Errorf("%s: no known weakness recorded — it would be free to disappear later", spec.Kind)
		}
		if spec.Resolution == "" || spec.Question == "" {
			t.Errorf("%s: question or resolution rule missing — the claim is not checkable", spec.Kind)
		}
		if spec.HorizonDays <= 0 {
			t.Errorf("%s: horizon %d", spec.Kind, spec.HorizonDays)
		}
	}
}

// The hash must cover the CONTENT. Changing a claimed number must change the
// hash; re-stating the identical spec must not.
func TestSpecHashCoversContent(t *testing.T) {
	a, _ := SpecFor("trend21")
	b := a
	if a.Hash() != b.Hash() {
		t.Fatal("identical specs hashed differently")
	}
	b.Bands = append([]Band(nil), a.Bands...)
	b.Bands[0].Claimed += 0.001
	if a.Hash() == b.Hash() {
		t.Error("a changed accuracy claim did not change the spec hash")
	}
	c := a
	c.KnownWeakness = "removed"
	if a.Hash() == c.Hash() {
		t.Error("deleting the known weakness did not change the spec hash")
	}
}

// Chain links must depend on the previous hash, or a record could be lifted out
// of one position and dropped into another undetected.
func TestChainLinksDependOnHistory(t *testing.T) {
	r := Record{Ts: 1, Kind: "trend21", SpecHash: "abc", Note: "initial"}
	h1 := HashEntry("", r)
	h2 := HashEntry("something-else", r)
	if h1 == h2 {
		t.Error("entry hash ignores prev_hash — the chain does not actually chain")
	}
	tampered := r
	tampered.SpecHash = "def"
	if HashEntry("", tampered) == h1 {
		t.Error("entry hash ignores the spec hash — a swapped claim would verify")
	}
}
