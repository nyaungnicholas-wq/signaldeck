package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// registerStream wires the live push channel. It is a plain GET so the browser
// EventSource can open it (cookies ride along same-origin; no custom header is
// required on GET), and it is gated by requiresAuth like every other read.
func (d Deps) registerStream(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/stream/snaps", d.streamSnaps)
}

// maxStreamSubscribers caps concurrent SSE streams daemon-wide.
//
// A subscriber spends ONE rate-limit token and then runs for as long as it
// likes, polling the store on every tick — the 2026-07-26 review took daemon
// CPU from 26.9% to 55.2% with ten anonymous streams. The limiter cannot see
// this because it prices requests, not residency, so residency needs its own
// bound. 32 is far above any real UI need (a browser tab opens one per visible
// symbol) and far below the point where the polling load matters.
//
// Beyond the cap the daemon refuses the NEW subscriber. Admitting it would
// degrade every stream already running, which trades a clear error for a
// diffuse one.
const maxStreamSubscribers = 32

var streamSubscribers atomic.Int64

// streamSnaps pushes the newest 1-second microstructure snapshot for one symbol
// as Server-Sent Events. This is how the UI "keeps up" with the live feed: the
// server holds the connection open and emits a frame the instant a new snap
// lands, instead of the client re-polling REST on a timer.
//
// The server ticks on an internal clock (?ms=, default 1000, clamped to
// [250, 10000]) and only writes a frame when the snapshot's timestamp actually
// advances — so a 250ms tick against a 1 Hz source costs at most one frame per
// second on the wire, not four. Faster-than-1s ticks let sub-second sources
// (should the ingest cadence rise) surface immediately without a client change.
func (d Deps) streamSnaps(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}

	if n := streamSubscribers.Add(1); n > maxStreamSubscribers {
		streamSubscribers.Add(-1)
		w.Header().Set("Retry-After", "5")
		httpErr(w, http.StatusServiceUnavailable, fmt.Sprintf(
			"stream capacity reached (%d concurrent subscribers) — retry shortly, "+
				"or poll GET /api/snaps instead", maxStreamSubscribers))
		return
	}
	defer streamSubscribers.Add(-1)

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}

	ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
	switch {
	case ms <= 0:
		ms = 1000
	case ms < 250:
		ms = 250
	case ms > 10000:
		ms = 10000
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Defeat proxy buffering (nginx) so frames are not held back.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Tell the browser how long to wait before auto-reconnecting after a drop.
	fmt.Fprintf(w, "retry: %d\n\n", ms)
	flusher.Flush()

	ctx := r.Context()
	ticker := time.NewTicker(time.Duration(ms) * time.Millisecond)
	defer ticker.Stop()
	// A comment line every ~20s keeps intermediaries from reaping an idle
	// connection during a quiet market (nothing new to push).
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	var lastTs int64
	// Emit the current snapshot immediately so a fresh subscriber is not blank
	// until the first tick.
	lastTs = d.pushLatestSnap(ctx, w, flusher, s.ID, lastTs)

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			next := d.pushLatestSnap(ctx, w, flusher, s.ID, lastTs)
			if next < 0 {
				return // write failed — client gone
			}
			lastTs = next
		}
	}
}

// pushLatestSnap fetches the newest snapshot and, if its timestamp is newer than
// lastTs, writes one SSE frame. It returns the timestamp to remember next
// (unchanged when there is nothing new), or -1 when the write failed.
func (d Deps) pushLatestSnap(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	symbolID, lastTs int64,
) int64 {
	now := time.Now().Unix()
	// A short lookback window is plenty to catch the newest 1 Hz snap while
	// staying a cheap indexed range scan. Snaps() orders ascending, so the
	// last row is the newest; no LIMIT (0) keeps that guarantee within the
	// tiny window.
	snaps, err := d.St.Snaps(ctx, symbolID, now-6, now+1, 0)
	if err != nil || len(snaps) == 0 {
		return lastTs
	}
	latest := snaps[len(snaps)-1]
	if latest.Ts <= lastTs {
		return lastTs
	}
	payload, err := json.Marshal(latest)
	if err != nil {
		return lastTs
	}
	if _, err := fmt.Fprintf(w, "event: snap\ndata: %s\n\n", payload); err != nil {
		return -1
	}
	flusher.Flush()
	return latest.Ts
}
