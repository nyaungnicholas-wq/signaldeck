package main

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// stopOnFile handles stopping a Windows daemon gracefully when Task Scheduler's schtasks /End is used.
// schtasks /End is TerminateProcess on Windows and never reaches the signal handler,
// so ops/lib-portable.sh sd_svc_stop writes this file and waits for a graceful drain before falling back to /End.
//
// Only a file OLDER than `since` (process start) is removed as a leftover. A
// stop written while this process was still opening its 6GB store is meant
// for it; deleting that one would leave /End to orphan whatever starts next.
func stopOnFile(parent context.Context, path string, since time.Time, every time.Duration) context.Context {
	if fi, err := os.Stat(path); err == nil && fi.ModTime().Before(since) {
		_ = os.Remove(path)
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := os.Stat(path); err == nil {
					slog.Info("stop requested via stop file", "path", path)
					_ = os.Remove(path)
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return ctx
}
