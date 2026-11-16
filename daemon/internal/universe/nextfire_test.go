package universe

import (
	"testing"
	"time"
)

// The poll after a session close is never later than 20 minutes past it, so
// the day's bar is refetched final before the settled rule's 22:00 ET.
func TestPollerNextFireRefreshesAfterClose(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	at := func(d, hm string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", d+" "+hm, ny)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	p := &Poller{}
	cases := []struct{ last, want time.Time }{
		{at("2026-11-12", "09:00"), at("2026-11-12", "15:00")}, // interval first
		{at("2026-11-12", "13:00"), at("2026-11-12", "16:20")}, // the interval says 19:00; the post-close refresh comes first
		{at("2026-11-12", "16:30"), at("2026-11-12", "22:30")}, // already refreshed after today's close
		{at("2026-11-13", "17:00"), at("2026-11-13", "23:00")}, // Friday: next close is Monday
	}
	for _, c := range cases {
		if got := p.NextFire(c.last, c.last); !got.Equal(c.want) {
			t.Errorf("NextFire(%s) = %s, want %s", c.last, got.In(ny), c.want)
		}
	}
	if now := time.Now(); !p.NextFire(time.Time{}, now).Equal(now) {
		t.Error("with no run history the poller must run now, not wait an interval")
	}
}
