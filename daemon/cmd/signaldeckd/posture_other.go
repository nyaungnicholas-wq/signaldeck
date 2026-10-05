//go:build !windows

package main

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// cloudflaredRunning reports whether a cloudflared process runs on this host;
// known is false when pgrep is missing, fails or takes over 10 s.
func cloudflaredRunning() (running, known bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "pgrep", "-x", "cloudflared").Run()
	if err == nil {
		return true, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 && ctx.Err() == nil { // pgrep: no match
		return false, true
	}
	return false, false
}
