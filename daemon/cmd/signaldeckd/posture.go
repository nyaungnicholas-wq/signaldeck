package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"
)

// publicWebDaemonPort is the port the web tier proxies every visitor to
// (web/src/app/api/[...path]/route.ts, SIGNALDECK_DAEMON default :8322).
const publicWebDaemonPort = 8322

var errTunnelBehindUnpublished = errors.New("a cloudflared tunnel is running on this host but this daemon " +
	"reads as unpublished, so every tunnel visitor would be local: set SIGNALDECK_PUBLIC_URL, " +
	"SIGNALDECK_TUNNEL_LOG or SIGNALDECK_PUBLIC_SURFACE=1 in daemon/.env (ops/CLOUDFLARE_TUNNEL.md section 3)")

// startupPosture refuses to serve when the daemon on the public web tier's
// port reads as unpublished while a cloudflared tunnel runs on the host
// (2026-10-05 audit, AUD-09). The web tier forwards every visitor with the
// local-proxy key, so published() is all that keeps them from operator
// authority and raw data. ctl and the guard ask earlier; this runs on every
// start (the Daemon task, its restart-on-failure, market-close, refresh,
// collect) and watchPosture repeats it while the daemon runs. A copy on any
// other port (tests, the restore drill, an audit) is out of the tunnel's
// reach. unknown means the process list could not be read.
func startupPosture(published bool, httpAddr string, tunnel func() (running, known bool)) (unknown bool, err error) {
	if published {
		return false, nil
	}
	_, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		return false, nil
	}
	if p, err := net.LookupPort("tcp", port); err != nil || p != publicWebDaemonPort {
		return false, nil
	}
	running, known := tunnel()
	if running {
		return false, errTunnelBehindUnpublished
	}
	return !known, nil
}

// startPostureWatch derives the daemon's context: for an unpublished daemon
// it runs watchPosture, which cancels the context with the refusal. A
// published daemon gets a plain cancellable context and is never scanned.
func startPostureWatch(ctx context.Context, published bool, httpAddr string, every time.Duration,
	tunnel func() (bool, bool)) (context.Context, context.CancelCauseFunc) {
	ctx, stop := context.WithCancelCause(ctx)
	if !published {
		go watchPosture(ctx, false, httpAddr, every, tunnel, stop)
	}
	return ctx, stop
}

// postureStopped is true when the watcher, not an operator, stopped the daemon.
func postureStopped(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errTunnelBehindUnpublished)
}

// watchPosture repeats the check every interval: a tunnel that came up after
// the daemon (logon order, a tunnel restart, the first go-live) was never
// caught (2026-10-05 review of round 6). It cancels ctx with the refusal, so
// the daemon stops gracefully and exits 3.
func watchPosture(ctx context.Context, published bool, httpAddr string, every time.Duration,
	tunnel func() (bool, bool), stop context.CancelCauseFunc) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := startupPosture(published, httpAddr, tunnel); err != nil {
				slog.Error("stopping: a tunnel came up in front of an unpublished daemon", "why", err)
				stop(err)
				return
			}
		}
	}
}
