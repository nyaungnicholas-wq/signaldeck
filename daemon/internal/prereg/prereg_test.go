// The pre-registration is only worth something if two things hold: the frozen
// claims match what the predictors actually advertise TODAY (otherwise the
// record documents a fiction), and the chain detects tampering (otherwise it is
// a timestamp with extra steps). Both are asserted here.
package prereg

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// The registered protocol must describe the grader that actually executes.
// The refusal thresholds are greped out of the grader source, so loosening
// MIN_DISTINCT_DAYS or MIN_INDEPENDENT_N in tools/accuracy_registry.py without
// amending the registered protocol fails here — the registered refusal rule
// and the executed one cannot diverge silently. (The file DIGEST is not
// pinned in code: the registrar measures it at registration time and a
// changed grader appends an AMENDMENT record automatically.)
func TestGradingProtocolMatchesTheActualGrader(t *testing.T) {
	p := GradingProtocol("", "")
	path := filepath.Join("..", "..", "..", p.Grader)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("registered grader %s not readable: %v", p.Grader, err)
	}
	src := string(raw)
	// Parsed, not merely substring-matched: the grader's value for each gate is
	// read back out of its source and compared, so a loosened constant fails
	// here naming which side moved.
	for _, c := range []struct {
		name string
		want int
	}{
		{"MIN_INDEPENDENT_N", p.MinIndependentN},
		{"MIN_DISTINCT_DAYS", p.MinDistinctDays},
		{"MIN_DISTINCT_BLOCKS", p.MinDistinctBlocks},
	} {
		got, ok := graderConst(src, c.name)
		if !ok {
			t.Errorf("grader source does not define %s at all — the registered protocol names a "+
				"gate the executed grader no longer has", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s: grader says %d, registered protocol says %d — the registered refusal rule "+
				"and the executed one have diverged. If the GRADER moved, update GradingProtocol() so "+
				"the registrar appends an AMENDMENT record; if the PROTOCOL moved, the grader must "+
				"follow. Do not regrade pre-registered claims under an unregistered protocol.",
				c.name, got, c.want)
		}
	}
	// The multiplicity price is part of the executed refusal rule now: a
	// registered maxAlpha the grader does not run means the published coverage
	// is not the registered one.
	if got, ok := graderFloatConst(src, "MAX_ALPHA"); !ok {
		t.Error("grader source does not define MAX_ALPHA — the registered protocol prices a " +
			"family-wise error budget the executed grader no longer has")
	} else if got != p.MaxAlpha {
		t.Errorf("MAX_ALPHA: grader says %v, registered protocol says %v — the published error "+
			"rate and the registered one have diverged", got, p.MaxAlpha)
	}
	if p.MultiplicityRule == "" {
		t.Error("the protocol registers no multiplicity rule, so the divisor pricing every " +
			"published interval is free to move after outcomes are visible")
	}
	if p.MinDistinctBlocks <= 0 || p.ClusterUnit == "" {
		t.Error("the protocol does not register the BLOCK gate that actually decides structural " +
			"verdicts — PREREGISTRATION.md makes the chain authoritative over the prose, so a chain " +
			"pinned to the superseded day gate contradicts the grader on every structural verdict")
	}
	if p.Refusal == "" || p.VerdictMap == "" || p.Independence == "" {
		t.Error("protocol is missing its refusal rule, verdict map, or independence definition — " +
			"an outcome with no pre-committed meaning can be renamed after the fact")
	}
}

// The protocol hash must cover its content, exactly like Spec hashes: changing
// the verdict map or a threshold must change the hash so the registrar appends
// an amendment rather than treating the change as a no-op.
func TestProtocolHashCoversContent(t *testing.T) {
	a := GradingProtocol("d0e1f2", "c0ffee")
	b := a
	if a.Hash() != b.Hash() {
		t.Fatal("identical protocols hashed differently")
	}
	b.MinDistinctDays = 3
	if a.Hash() == b.Hash() {
		t.Error("a loosened refusal threshold did not change the protocol hash")
	}
	c := a
	c.VerdictMap = "everything is HOLDING"
	if a.Hash() == c.Hash() {
		t.Error("a rewritten verdict map did not change the protocol hash")
	}
	e := a
	e.MinDistinctBlocks = 3
	if a.Hash() == e.Hash() {
		t.Error("a loosened block gate did not change the protocol hash")
	}
	d := GradingProtocol("a-different-grader-digest", "c0ffee")
	if a.Hash() == d.Hash() {
		t.Error("a changed grader digest did not change the protocol hash — a grader edit " +
			"would never trigger the automatic AMENDMENT")
	}
	f := GradingProtocol("d0e1f2", "deadbeef")
	if a.Hash() == f.Hash() {
		t.Error("a changed grader commit did not change the protocol hash")
	}
}

// graderFloatConst reads a float constant out of the grader source.
func graderFloatConst(src, name string) (float64, bool) {
	m := regexp.MustCompile(`(?m)^` + name + ` = ([0-9.]+)$`).FindStringSubmatch(src)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// A widened interval must change the protocol hash: otherwise the grader could
// run a different error budget than the one the chain froze without the
// registrar ever appending an amendment.
func TestProtocolHashCoversTheMultiplicityPrice(t *testing.T) {
	a := GradingProtocol("d0e1f2", "c0ffee")
	b := a
	b.MaxAlpha = 0.10
	if a.Hash() == b.Hash() {
		t.Error("a loosened family-wise error budget did not change the protocol hash")
	}
	c := a
	c.MultiplicityRule = "divisor is always 1"
	if a.Hash() == c.Hash() {
		t.Error("a rewritten multiplicity rule did not change the protocol hash")
	}
}

// A look record must hash its counter and its grade stamp, so two looks can
// never collapse into one on the chain.
func TestLookHashCoversContent(t *testing.T) {
	a := Look{Counter: 3, GradedAt: "2026-08-07T00:00:00", Registry: RegistryRel}
	b := a
	b.Counter = 4
	if a.Hash() == b.Hash() {
		t.Error("a different look counter hashed identically")
	}
	c := a
	c.GradedAt = "2026-08-08T00:00:00"
	if a.Hash() == c.Hash() {
		t.Error("a different grade stamp hashed identically")
	}
}

// graderConst reads an int constant out of the grader source.
func graderConst(src, name string) (int, bool) {
	m := regexp.MustCompile(`(?m)^` + name + ` = (\d+)$`).FindStringSubmatch(src)
	if m == nil {
		return 0, false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return v, true
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
