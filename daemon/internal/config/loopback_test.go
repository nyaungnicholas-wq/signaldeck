// The PublicReads default is derived from the bind address, so this is a
// security boundary: getting it wrong hands out unauthenticated reads on a
// network-reachable daemon.
package config

import "testing"

func TestLoopbackDefault(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8322", true},
		{"localhost:8322", true},
		{"[::1]:8322", true},
		{"127.0.0.53:8322", true},
		// Everything below is reachable by someone else, so PublicReads must
		// default CLOSED. ":8322" is the dangerous one — it binds every
		// interface while looking local.
		{":8322", false},
		{"0.0.0.0:8322", false},
		{"192.168.1.5:8322", false},
		{"10.0.0.7:8322", false},
	}
	for _, c := range cases {
		if got := loopbackOnly(c.addr); got != c.want {
			t.Fatalf("loopbackOnly(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}
