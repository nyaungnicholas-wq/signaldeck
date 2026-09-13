package papertrade

import "testing"

// The deadband is the whole reason DecideTarget has three outcomes instead of
// two. LongThreshold and FlatThreshold were read independently, so an operator
// who swapped the two values passed every per-key check and silently lost it:
// nothing between the thresholds could return Hold, and the book re-decided on
// every signal. The invariant was asserted in a comment and nowhere else.
//
// The load-bearing assertion in each case below is DecideTarget(0.5) == Hold --
// that is the outcome the inversion destroys.
func TestThresholdPairCannotOverlap(t *testing.T) {
	cases := []struct {
		name     string
		flatEnv  string
		longEnv  string
		wantFlat float64
		wantLong float64
		why      string
	}{
		{
			name: "defaults", flatEnv: "", longEnv: "",
			wantFlat: defaultFlatThreshold, wantLong: defaultLongThreshold,
			why: "neither variable is set, so both defaults must stand",
		},
		{
			name: "inverted pair is refused whole", flatEnv: "0.7", longEnv: "0.6",
			wantFlat: defaultFlatThreshold, wantLong: defaultLongThreshold,
			why: "FLAT >= LONG deletes the deadband, so BOTH overrides must fall back",
		},
		{
			name: "equal pair is refused too", flatEnv: "0.5", longEnv: "0.5",
			wantFlat: defaultFlatThreshold, wantLong: defaultLongThreshold,
			why: "equal thresholds leave no probability that can Hold",
		},
		{
			name: "flat alone raised past the default long", flatEnv: "0.9", longEnv: "",
			wantFlat: defaultFlatThreshold, wantLong: defaultLongThreshold,
			why: "one bad key still overlaps the other's default and must be refused",
		},
		{
			name: "a valid wider pair is honoured", flatEnv: "0.3", longEnv: "0.8",
			wantFlat: 0.3, wantLong: 0.8,
			why: "a legitimate override must still take effect",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIGNALDECK_PAPER_FLAT", tc.flatEnv)
			t.Setenv("SIGNALDECK_PAPER_LONG", tc.longEnv)

			if got := FlatThreshold(); got != tc.wantFlat {
				t.Fatalf("FlatThreshold() = %v, want %v -- %s", got, tc.wantFlat, tc.why)
			}
			if got := LongThreshold(); got != tc.wantLong {
				t.Fatalf("LongThreshold() = %v, want %v -- %s", got, tc.wantLong, tc.why)
			}
			if tc.wantFlat >= tc.wantLong {
				t.Fatalf("the test's own expectation overlaps (%v >= %v): there would be no deadband to check",
					tc.wantFlat, tc.wantLong)
			}

			// Midway between the two surviving thresholds: this MUST Hold.
			mid := (tc.wantFlat + tc.wantLong) / 2
			if got := DecideTarget(mid); got != Hold {
				t.Fatalf("DecideTarget(%v) = %v, want Hold -- the deadband between %v and %v is gone, "+
					"so the book re-decides on every signal and bleeds out on execution cost",
					mid, got, tc.wantFlat, tc.wantLong)
			}
			// The two edges still resolve, and ties go the documented way.
			if got := DecideTarget(tc.wantLong); got != GoLong {
				t.Fatalf("DecideTarget(%v) = %v, want GoLong at exactly LongThreshold", tc.wantLong, got)
			}
			if got := DecideTarget(tc.wantFlat); got != GoFlat {
				t.Fatalf("DecideTarget(%v) = %v, want GoFlat at exactly FlatThreshold", tc.wantFlat, got)
			}
		})
	}
}
