package pipeline

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
)

// A30: a ROW cap feeding a DAY gate. adaptiveMaxRows is per horizon and Run
// pools two, so the learner sees ~40,000 rows spanning 8 days while
// prediction_outcomes holds 642,431 rows across 41 distinct days — more than
// twice the 20-day floor. Every cell comes back gated, adaptive.Pick returns
// nil, and the whole fleet blends on the static equal prior.
//
// What made that invisible is that the learner reported something TRUE — "only
// 8 distinct trading day(s), below the 20-day floor" — which reads as "not
// enough history yet" and sends an operator away to wait. These cases pin the
// clause that distinguishes starved from young. The weights are untouched:
// this is a reporting change only.

func TestCapBindingNote_FiresWhenTruncatedAndFullyGated(t *testing.T) {
	note := capBindingNote(true, 0, 8)
	if note == "" {
		t.Fatal("a truncated read with zero learned cells below the day floor must say the cap is binding")
	}
	for _, want := range []string{"ROW CAP BINDING", "ALLOWED", "8 day"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note must contain %q, got %q", want, note)
		}
	}
}

// The whole point is to contradict the "wait for more data" reading, so the
// note has to name the remedy rather than merely restate the day count.
func TestCapBindingNote_NamesTheRemedy(t *testing.T) {
	note := capBindingNote(true, 0, 8)
	if !strings.Contains(note, "day window") {
		t.Fatalf("note must point at widening the read to a day window, got %q", note)
	}
}

// A short read means the day count is the real one — the floor message is then
// honest and must not be second-guessed.
func TestCapBindingNote_SilentWhenReadWasNotTruncated(t *testing.T) {
	if n := capBindingNote(false, 0, 8); n != "" {
		t.Fatalf("an untruncated read has an honest day count; got %q", n)
	}
}

// If any cell cleared the floor the mechanism is working, cap or not.
func TestCapBindingNote_SilentWhenSomethingLearned(t *testing.T) {
	if n := capBindingNote(true, 1, 8); n != "" {
		t.Fatalf("a cell yielded weights, so the gate is not blocking; got %q", n)
	}
}

// At or above the floor a zero-learned result is some other gate (samples,
// panel test), not day starvation — claiming the cap would misdiagnose it.
func TestCapBindingNote_SilentAtOrAboveTheDayFloor(t *testing.T) {
	if n := capBindingNote(true, 0, adaptive.MinCellDays); n != "" {
		t.Fatalf("at the day floor the block is not day starvation; got %q", n)
	}
}
