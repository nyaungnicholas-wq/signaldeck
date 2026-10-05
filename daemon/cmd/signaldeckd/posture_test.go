package main

import "testing"

// TestUnpublishedDaemonRefusesBehindATunnel pins AUD-09's last line of
// defence (2026-10-05 review of round 5): scheduled starts bypassed the ctl
// and guard checks, so the daemon itself refuses.
func TestUnpublishedDaemonRefusesBehindATunnel(t *testing.T) {
	up := func() (bool, bool) { return true, true }
	down := func() (bool, bool) { return false, true }
	unknown := func() (bool, bool) { return false, false }
	const live = "127.0.0.1:8322"
	cases := []struct {
		name      string
		published bool
		addr      string
		tunnel    func() (bool, bool)
		refuse    bool
	}{
		{"unpublished behind a tunnel", false, live, up, true},
		{"unpublished behind a tunnel, any interface", false, "0.0.0.0:8322", up, true},
		{"published behind a tunnel", true, live, up, false},
		{"unpublished, no tunnel", false, live, down, false},
		{"unpublished, cannot tell", false, live, unknown, false},
		{"isolated copy on another port (tests, drill, audit)", false, "127.0.0.1:18322", up, false},
	}
	for _, c := range cases {
		if err := startupPosture(c.published, c.addr, c.tunnel); (err != nil) != c.refuse {
			t.Errorf("%s: err=%v, want refuse=%v", c.name, err, c.refuse)
		}
	}
	if running, known := cloudflaredRunning(); !known {
		t.Logf("cloudflaredRunning cannot tell on this host (running=%v)", running)
	}
}
