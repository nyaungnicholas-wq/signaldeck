package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestUnpublishedDaemonRefusesBehindATunnel pins AUD-09's last line of
// defence (2026-10-05 reviews of rounds 5 and 6): scheduled starts bypassed
// the ctl and guard checks, so the daemon on the web tier's port refuses.
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
		unknown   bool
	}{
		{"unpublished behind a tunnel", false, live, up, true, false},
		{"unpublished behind a tunnel, any interface", false, ":8322", up, true, false},
		{"leading-zero spelling of 8322", false, "127.0.0.1:08322", up, true, false},
		{"published behind a tunnel", true, live, up, false, false},
		{"unpublished, no tunnel", false, live, down, false, false},
		{"unpublished, cannot tell: said, not refused", false, live, unknown, false, true},
		{"isolated copy on another port (tests, drill, audit)", false, "127.0.0.1:18322", up, false, false},
	}
	for _, c := range cases {
		unk, err := startupPosture(c.published, c.addr, c.tunnel)
		if (err != nil) != c.refuse || unk != c.unknown {
			t.Errorf("%s: err=%v unknown=%v, want refuse=%v unknown=%v", c.name, err, unk, c.refuse, c.unknown)
		}
	}
	if running, known := cloudflaredRunning(); !known {
		t.Errorf("cloudflaredRunning cannot read this host's process list (running=%v)", running)
	}
}

// A tunnel that comes up after the daemon (logon order, a tunnel restart, the
// first go-live) stops it; before round 7 only the start was checked.
func TestWatchPostureStopsWhenATunnelAppears(t *testing.T) {
	var appeared atomic.Bool
	tunnel := func() (bool, bool) { return appeared.Load(), true }
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	go watchPosture(ctx, false, "127.0.0.1:8322", 5*time.Millisecond, tunnel, stop)
	time.Sleep(30 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("stopped with no tunnel")
	}
	appeared.Store(true)
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), errTunnelBehindUnpublished) {
			t.Fatalf("stopped for %v, want the posture refusal", context.Cause(ctx))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a tunnel came up and the unpublished daemon kept serving")
	}
}
