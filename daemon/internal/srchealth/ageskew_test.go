package srchealth

import (
	"strings"
	"testing"
)

// TestAgeStringRendersSubMinuteMagnitudes pins the fix for the bug where the
// sub-minute case did not exist: anything under 30 seconds was rounded to the
// zero duration, stringified as "0s", and then had "0s" trimmed off — leaving
// the empty string. On the live feed this silently deleted the clock-skew
// magnitude (almost always a few seconds) for crypto_perp, snapshots_1s and
// bars_1m_hot, producing detail lines like "ahead of our clock by  old".
func TestAgeStringRendersSubMinuteMagnitudes(t *testing.T) {
	cases := []struct {
		secs int64
		want string
	}{
		{0, "0s"},
		{1, "1s"},
		{3, "3s"},
		{29, "29s"},
		{45, "45s"},
		{59, "59s"},
		{60, "1m"},
		{90, "2m"},
		{3600, "1h"},
		{7200, "2h"},
		{86400, "1d"},
		{5 * 86400, "5d"},
	}
	for _, c := range cases {
		got := ageString(c.secs)
		if got == "" {
			t.Errorf("ageString(%d) returned empty string — the exact bug; want %q", c.secs, c.want)
		}
		if got != c.want {
			t.Errorf("ageString(%d) = %q, want %q", c.secs, got, c.want)
		}
	}
}

// TestDetailLineCarriesTheSkewMagnitude guards the composed detail line against
// the regression where a sub-minute "ahead of our clock" magnitude collapsed to
// the empty string, yielding "ahead of our clock by  old" (double space) in the
// dq_events line. The sources snapshots_1s, crypto_perp and bars_1m_hot hit this
// daily because clock skew is almost always a few seconds.
func TestDetailLineCarriesTheSkewMagnitude(t *testing.T) {
	r := Report{
		Source:          "snapshots_1s",
		AgeSecs:         -3,
		StaleBudgetSecs: 600,
		Note:            "1s microstructure snapshots (crypto 24/7)",
	}
	line := DetailLine(r)

	if !strings.Contains(line, "ahead of our clock by 3s") {
		t.Errorf("DetailLine: line = %q, want it to contain %q", line, "ahead of our clock by 3s")
	}
	if strings.Contains(line, "by  old") {
		t.Errorf("DetailLine: line = %q, must NOT contain %q (the double-space regression)", line, "by  old")
	}
	if strings.Contains(line, "no rows") {
		t.Errorf("DetailLine: line = %q, must NOT contain %q for a real-ahead source", line, "no rows")
	}

	emptyR := Report{
		Source:          "snapshots_1s",
		AgeSecs:         noRowsAge,
		StaleBudgetSecs: 600,
		Note:            "1s microstructure snapshots (crypto 24/7)",
	}
	emptyLine := DetailLine(emptyR)
	if !strings.Contains(emptyLine, "no rows") {
		t.Errorf("DetailLine: line = %q, want it to contain %q", emptyLine, "no rows")
	}
}