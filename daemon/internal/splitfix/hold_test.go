package splitfix

import "testing"

func TestHeldForReview(t *testing.T) {
	day := int64(86400)
	var bars []Bar
	for i := 0; i < 30; i++ {
		c := 100.0
		if i >= 20 {
			c = 50 // an unadjusted 2:1 split on bar 20
		}
		bars = append(bars, Bar{Ts: int64(i) * day, Close: c, Volume: 1e6})
	}
	split := bars[20].Ts
	if !HeldForReview(bars, 0, false) {
		t.Fatal("an unexamined split-shaped move must hold the window")
	}
	if !HeldForReview(bars, split-day, true) {
		t.Fatal("a review from before the move does not cover it")
	}
	if !HeldForReview(bars, split+day/2, true) {
		t.Fatal("a review before the suspect bar finished forming does not cover it")
	}
	if HeldForReview(bars, split+2*day, true) {
		t.Fatal("once the worker has examined the move the window is graded")
	}
	if HeldForReview(bars[:20], 0, false) {
		t.Fatal("a clean window is never held")
	}
}
