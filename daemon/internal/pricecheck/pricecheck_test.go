package pricecheck

import (
	"math"
	"testing"
)

const day = int64(86400)

func series(n int, base, step float64) []Point {
	out := make([]Point, n)
	for i := 0; i < n; i++ {
		out[i] = Point{Ts: int64(i) * day, Close: base + float64(i)*step}
	}
	return out
}

func TestIdenticalSeriesAgree(t *testing.T) {
	s := series(60, 100, 0.5)
	r := Compare(s, s, DefaultToleranceBps)
	if !r.Agree || r.DisagreeCount != 0 {
		t.Fatalf("identical series disagreed: %+v", r)
	}
	if r.Compared != 60 || !r.Confident {
		t.Fatalf("compared=%d confident=%v, want 60/true", r.Compared, r.Confident)
	}
	if r.MaxDevBps != 0 || r.MeanSignedBps != 0 {
		t.Fatalf("nonzero deviation on identical series: %+v", r)
	}
}

// A single wrong close — the exact failure no internal consistency test can see
// — must be located precisely.
func TestSingleWrongCloseIsCaught(t *testing.T) {
	ours := series(60, 100, 0.5)
	theirs := series(60, 100, 0.5)
	ours[30].Close *= 1.05 // 5% too high on one day
	r := Compare(ours, theirs, DefaultToleranceBps)
	if r.Agree {
		t.Fatal("a 5% single-day error was reported as agreement")
	}
	if r.DisagreeCount != 1 {
		t.Fatalf("DisagreeCount = %d, want 1", r.DisagreeCount)
	}
	if got := r.Disagreements[0].Day; got != 30*day {
		t.Fatalf("flagged day %d, want %d", got, 30*day)
	}
	if math.Abs(r.Disagreements[0].DevBps-500) > 1 {
		t.Fatalf("DevBps = %v, want ~500", r.Disagreements[0].DevBps)
	}
	if r.Systematic {
		t.Fatal("one bad day must not be called systematic")
	}
}

// A tolerance-sized difference must NOT fire. A check that cries wolf on vendor
// rounding gets ignored, which is worse than no check.
func TestWithinToleranceDoesNotFire(t *testing.T) {
	ours := series(60, 100, 0)
	theirs := series(60, 100, 0)
	for i := range ours {
		ours[i].Close = 100.1 // 10bps — inside the 25bps default
	}
	r := Compare(ours, theirs, DefaultToleranceBps)
	if !r.Agree {
		t.Fatalf("10bps difference fired at a 25bps tolerance: %+v", r)
	}
	if math.Abs(r.MedianDevBps-10) > 0.5 {
		t.Fatalf("MedianDevBps = %v, want ~10", r.MedianDevBps)
	}
}

// A consistent one-sided offset means the providers adjust differently. That is
// a completely different finding from sporadic corruption and must be labeled as
// such.
func TestSystematicOffsetIsLabeled(t *testing.T) {
	ours := series(60, 100, 0)
	theirs := series(60, 100, 0)
	for i := range ours {
		ours[i].Close = 100 * 1.01 // every day 100bps high — dividend adjustment
	}
	r := Compare(ours, theirs, DefaultToleranceBps)
	if r.Agree {
		t.Fatal("a 100bps systematic offset should exceed tolerance")
	}
	if !r.Systematic {
		t.Fatalf("one-sided offset not labeled systematic: meanSigned=%v", r.MeanSignedBps)
	}
	if r.MeanSignedBps <= 0 {
		t.Fatalf("MeanSignedBps = %v, want positive (ours above theirs)", r.MeanSignedBps)
	}
}

// Coverage differences are not disagreements.
func TestNonOverlappingDaysAreCoverageNotDisagreement(t *testing.T) {
	ours := series(60, 100, 0.5)
	theirs := series(60, 100, 0.5)[10:50] // provider covers a shorter span
	r := Compare(ours, theirs, DefaultToleranceBps)
	if !r.Agree {
		t.Fatalf("missing days reported as disagreement: %+v", r)
	}
	if r.Compared != 40 {
		t.Fatalf("Compared = %d, want 40 (intersection only)", r.Compared)
	}
	if r.OnlyOurs != 20 || r.OnlyTheirs != 0 {
		t.Fatalf("coverage counts = %d/%d, want 20/0", r.OnlyOurs, r.OnlyTheirs)
	}
}

// Intraday timestamps must collapse to one close per UTC day (latest wins), or
// the comparison would depend on how many rows each provider happens to return.
func TestAlignsOnUTCDayKeepingLatest(t *testing.T) {
	ours := []Point{
		{Ts: 0, Close: 99}, {Ts: 3600, Close: 100}, // same day; 100 is the close
		{Ts: day, Close: 101},
	}
	theirs := []Point{
		{Ts: 7200, Close: 100}, {Ts: day + 60, Close: 101},
	}
	r := Compare(ours, theirs, DefaultToleranceBps)
	if r.Compared != 2 || !r.Agree {
		t.Fatalf("day alignment failed: %+v", r)
	}
}

// Too little overlap must be honestly unconfident — never silent agreement.
func TestThinOverlapIsNotConfident(t *testing.T) {
	s := series(5, 100, 1)
	r := Compare(s, s, DefaultToleranceBps)
	if r.Confident {
		t.Fatalf("5 days reported as confident: %+v", r)
	}
	if !r.Agree {
		t.Fatal("identical thin series should still report Agree, with Confident=false")
	}
}

func TestDegenerateInputs(t *testing.T) {
	if r := Compare(nil, nil, 0); r.Confident || r.Compared != 0 {
		t.Fatalf("empty inputs = %+v", r)
	}
	if r := Compare(nil, nil, 0); r.ToleranceBps != DefaultToleranceBps {
		t.Fatalf("zero tolerance not defaulted: %v", r.ToleranceBps)
	}
	// A non-positive close is a different defect and must be skipped, not turned
	// into an infinite deviation.
	ours := []Point{{Ts: 0, Close: 0}, {Ts: day, Close: 100}}
	theirs := []Point{{Ts: 0, Close: 100}, {Ts: day, Close: 100}}
	r := Compare(ours, theirs, DefaultToleranceBps)
	if r.Compared != 1 {
		t.Fatalf("Compared = %d, want 1 (zero close skipped)", r.Compared)
	}
	if math.IsInf(r.MaxDevBps, 0) || math.IsNaN(r.MaxDevBps) {
		t.Fatalf("MaxDevBps = %v", r.MaxDevBps)
	}
}

// The report must stay bounded when a symbol is comprehensively broken.
func TestDisagreementListIsCapped(t *testing.T) {
	ours := series(200, 100, 0)
	theirs := series(200, 100, 0)
	for i := range ours {
		ours[i].Close = 100 * (1 + 0.01*float64(i%7+1))
	}
	r := Compare(ours, theirs, DefaultToleranceBps)
	if len(r.Disagreements) > MaxReported {
		t.Fatalf("reported %d disagreements, cap is %d", len(r.Disagreements), MaxReported)
	}
	if r.DisagreeCount <= MaxReported {
		t.Fatalf("fixture should produce more than %d disagreements, got %d", MaxReported, r.DisagreeCount)
	}
	// Worst first.
	for i := 1; i < len(r.Disagreements); i++ {
		if r.Disagreements[i-1].DevBps < r.Disagreements[i].DevBps {
			t.Fatal("disagreements not sorted worst-first")
		}
	}
}
