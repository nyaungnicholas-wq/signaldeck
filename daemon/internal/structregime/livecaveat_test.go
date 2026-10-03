package structregime

import (
	"strings"
	"testing"
)

// E-CAVEAT-DATE (2026-10-02): the caveat used to end "First gradable
// 2026-08-07 — before that date this number has zero live resolutions behind
// it", which read as the current state long after live grading began. It now
// states the kind's live status: zero, or how many calls resolved, never how
// they scored.
func TestEvidenceCaveatStatesTheLiveStatus(t *testing.T) {
	zero := EvidenceCaveatFor(0)
	one := EvidenceCaveatFor(1)
	many := EvidenceCaveatFor(13880)
	neutral := EvidenceCaveatText()

	cases := map[string]string{
		"zero":    zero,
		"one":     one,
		"many":    many,
		"neutral": neutral,
	}
	for name, txt := range cases {
		if !strings.HasPrefix(txt, evidenceCaveatBase) {
			t.Errorf("%s: does not start with evidenceCaveatBase: %q", name, txt)
		}
		if !strings.Contains(txt, "BACKTEST CLAIM") {
			t.Errorf("%s: missing BACKTEST CLAIM: %q", name, txt)
		}
		if !strings.Contains(txt, "not a live measurement") {
			t.Errorf("%s: missing 'not a live measurement': %q", name, txt)
		}
		if !strings.Contains(txt, "/api/prereg") {
			t.Errorf("%s: missing /api/prereg: %q", name, txt)
		}
		if !strings.Contains(txt, firstGradableOn) {
			t.Errorf("%s: missing firstGradableOn (%s): %q", name, firstGradableOn, txt)
		}
		if !strings.Contains(txt, "/api/track-record") {
			t.Errorf("%s: missing /api/track-record: %q", name, txt)
		}
		if strings.Contains(txt, "before that date") {
			t.Errorf("%s: must not contain 'before that date': %q", name, txt)
		}
		for _, bad := range []string{"%", "accurate", "live accuracy", "beats", "skill"} {
			if strings.Contains(txt, bad) {
				t.Errorf("%s: must not contain %q: %q", name, bad, txt)
			}
		}
	}
	if !strings.Contains(zero, "No call of this kind has resolved live yet") {
		t.Errorf("zero: missing 'No call of this kind has resolved live yet': %q", zero)
	}
	if !strings.Contains(zero, "zero live resolutions") {
		t.Errorf("zero: missing 'zero live resolutions': %q", zero)
	}
	if !strings.Contains(one, "1 call of this kind has resolved live") {
		t.Errorf("one: missing '1 call of this kind has resolved live': %q", one)
	}
	if strings.Contains(one, "zero live resolutions") {
		t.Errorf("one: must not contain 'zero live resolutions': %q", one)
	}
	if !strings.Contains(many, "13880 calls of this kind have resolved live") {
		t.Errorf("many: missing '13880 calls of this kind have resolved live': %q", many)
	}
	if strings.Contains(many, "zero live resolutions") {
		t.Errorf("many: must not contain 'zero live resolutions': %q", many)
	}
	if EvidenceCaveatFor(-3) != zero {
		t.Errorf("EvidenceCaveatFor(-3) = %q, want zero %q", EvidenceCaveatFor(-3), zero)
	}
	if strings.Contains(neutral, "zero live resolutions") {
		t.Errorf("neutral: must not contain 'zero live resolutions': %q", neutral)
	}
	if strings.Contains(neutral, "No call of this kind") {
		t.Errorf("neutral: must not contain 'No call of this kind': %q", neutral)
	}
}
