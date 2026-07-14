package alpaca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// DefaultStreamURL is the production IEX minute-bar websocket endpoint.
const DefaultStreamURL = "wss://stream.data.alpaca.markets/v2/iex"

// Streamer maintains one websocket session to Alpaca's IEX stream and writes
// every received minute bar into the store. It is designed to be run under a
// supervising worker: Run returns on the first error and the supervisor
// redials after a cooldown.
type Streamer struct {
	client  *Client
	st      *store.Store
	resolve func(symbol string) (int64, bool) // symbol → symbolID (false = untracked)
	wsURL   string

	mu      sync.Mutex
	symbols []string // desired subscription set

	// nudge is buffered(1): SetSymbols never blocks, and coalescing repeated
	// nudges is fine because resync always diffs against the full desired set.
	nudge chan struct{}
}

// NewStreamer builds a Streamer. wsURL "" means the production endpoint;
// tests pass an httptest-backed ws:// URL. (Named NewStreamer rather than New
// because the package's New is taken by the REST client.)
func NewStreamer(client *Client, st *store.Store, resolve func(symbol string) (int64, bool), wsURL string) *Streamer {
	if wsURL == "" {
		wsURL = DefaultStreamURL
	}
	return &Streamer{
		client:  client,
		st:      st,
		resolve: resolve,
		wsURL:   wsURL,
		nudge:   make(chan struct{}, 1),
	}
}

// SetSymbols replaces the desired subscription set. Safe to call from any
// goroutine at any time; a live session applies the diff on its next loop
// iteration, and a future session subscribes to the new set on connect.
func (s *Streamer) SetSymbols(symbols []string) {
	s.mu.Lock()
	s.symbols = append([]string(nil), symbols...)
	s.mu.Unlock()
	select {
	case s.nudge <- struct{}{}:
	default: // a nudge is already pending; resync reads the latest set anyway
	}
}

func (s *Streamer) snapshotSymbols() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.symbols...)
}

// wsEnvelope is the union wire shape of every stream element we care about.
// Alpaca frames are JSON arrays of these; "T" discriminates.
type wsEnvelope struct {
	T    string  `json:"T"`
	Msg  string  `json:"msg"`
	Code int     `json:"code"`
	S    string  `json:"S"` // bar symbol
	O    float64 `json:"o"`
	H    float64 `json:"h"`
	L    float64 `json:"l"`
	C    float64 `json:"c"`
	V    float64 `json:"v"`
	Time string  `json:"t"` // RFC3339 bar open time
}

// subFrame is the subscribe/unsubscribe control message.
type subFrame struct {
	Action string   `json:"action"`
	Bars   []string `json:"bars"`
}

// authFrame is the credential handshake message.
type authFrame struct {
	Action string `json:"action"`
	Key    string `json:"key"`
	Secret string `json:"secret"`
}

// Run dials the stream, authenticates, subscribes to the current symbol set,
// and then consumes bar messages until ctx is cancelled or any error occurs —
// errors are returned to the caller (the worker framework restarts us).
func (s *Streamer) Run(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, s.wsURL, nil)
	if err != nil {
		return fmt.Errorf("alpaca stream: dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
	// A burst at minute rollover can carry every subscribed symbol in one
	// frame; the default 32KiB limit is too tight for large watchlists.
	conn.SetReadLimit(1 << 20)

	// Server greets with [{"T":"success","msg":"connected"}] before it will
	// accept auth; drain it (an error element here is fatal anyway).
	if _, err := readFrame(ctx, conn); err != nil {
		return fmt.Errorf("alpaca stream: greeting: %w", err)
	}
	if err := wsjson.Write(ctx, conn, authFrame{Action: "auth", Key: s.client.Key, Secret: s.client.Secret}); err != nil {
		return fmt.Errorf("alpaca stream: send auth: %w", err)
	}
	ack, err := readFrame(ctx, conn)
	if err != nil {
		return fmt.Errorf("alpaca stream: auth ack: %w", err)
	}
	if !authenticated(ack) {
		return fmt.Errorf("alpaca stream: auth rejected: %s", describe(ack))
	}

	// Track what the server believes we're subscribed to so SetSymbols can be
	// applied as a minimal unsubscribe/subscribe diff.
	subscribed := map[string]bool{}
	if err := s.resync(ctx, conn, subscribed); err != nil {
		return err
	}

	// One reader goroutine feeds the select loop so nudges can interleave
	// with blocking reads. coder/websocket allows one concurrent reader and
	// one concurrent writer, and only this loop writes after the handshake.
	type readResult struct {
		data []byte
		err  error
	}
	reads := make(chan readResult)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			select {
			case reads <- readResult{data: data, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.nudge:
			if err := s.resync(ctx, conn, subscribed); err != nil {
				return err
			}
		case r := <-reads:
			if r.err != nil {
				if isExpectedDisconnect(r.err) {
					// Routine server-side teardown (idle timeout, deploy,
					// network blip). The supervisor redials after its cooldown;
					// a clean return keeps these out of the failure log so real
					// faults stay visible.
					return nil
				}
				return fmt.Errorf("alpaca stream: read: %w", r.err)
			}
			if err := s.handleFrame(ctx, r.data); err != nil {
				return err
			}
		}
	}
}

// isExpectedDisconnect reports whether a read error is a routine websocket
// teardown rather than a real fault: a normal/going-away/abnormal close frame,
// a plain EOF, a closed socket, connection reset, or broken pipe. These recur
// continuously on any long-lived feed and the supervisor already redials, so
// they must not be logged as worker failures (they were ~90% of daemon.err.log).
// Genuine faults — decode errors, DB upserts, bad bar data — still propagate.
func isExpectedDisconnect(err error) bool {
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway,
		websocket.StatusAbnormalClosure, websocket.StatusNoStatusRcvd:
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	return false
}

// handleFrame processes one raw websocket frame (a JSON array of envelopes):
// bar elements become 1m upserts, success/subscription acks are ignored, and
// stream-level error elements are logged (the server usually follows them by
// closing, which surfaces as a read error).
func (s *Streamer) handleFrame(ctx context.Context, data []byte) error {
	var msgs []wsEnvelope
	if err := json.Unmarshal(data, &msgs); err != nil {
		return fmt.Errorf("alpaca stream: decode frame: %w", err)
	}
	for _, m := range msgs {
		switch m.T {
		case "b":
			id, ok := s.resolve(m.S)
			if !ok {
				continue // bar for a symbol we no longer track
			}
			ts, err := time.Parse(time.RFC3339, m.Time)
			if err != nil {
				return fmt.Errorf("alpaca stream: bad bar time %q for %s: %w", m.Time, m.S, err)
			}
			bar := md.Bar{
				SymbolID: id, TF: md.TF1m, Ts: ts.Unix(),
				Open: m.O, High: m.H, Low: m.L, Close: m.C, Volume: m.V,
			}
			if err := s.st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
				return fmt.Errorf("alpaca stream: upsert %s bar: %w", m.S, err)
			}
		case "error":
			log.Printf("alpaca stream: server error code=%d msg=%q", m.Code, m.Msg)
		default:
			// "success", "subscription", and anything future — not bar data.
		}
	}
	return nil
}

// resync brings the server-side subscription in line with the desired set.
// subscribed is mutated to reflect what was actually acknowledged-by-send.
func (s *Streamer) resync(ctx context.Context, conn *websocket.Conn, subscribed map[string]bool) error {
	want := map[string]bool{}
	for _, sym := range s.snapshotSymbols() {
		want[sym] = true
	}
	add, del := diffSymbols(want, subscribed)
	if len(del) > 0 {
		if err := wsjson.Write(ctx, conn, subFrame{Action: "unsubscribe", Bars: del}); err != nil {
			return fmt.Errorf("alpaca stream: unsubscribe: %w", err)
		}
		for _, sym := range del {
			delete(subscribed, sym)
		}
	}
	if len(add) > 0 {
		if err := wsjson.Write(ctx, conn, subFrame{Action: "subscribe", Bars: add}); err != nil {
			return fmt.Errorf("alpaca stream: subscribe: %w", err)
		}
		for _, sym := range add {
			subscribed[sym] = true
		}
	}
	return nil
}

// diffSymbols returns sorted to-subscribe / to-unsubscribe lists; sorted so
// the control frames are deterministic (stable logs and testable output).
func diffSymbols(want, have map[string]bool) (add, del []string) {
	for sym := range want {
		if !have[sym] {
			add = append(add, sym)
		}
	}
	for sym := range have {
		if !want[sym] {
			del = append(del, sym)
		}
	}
	sort.Strings(add)
	sort.Strings(del)
	return add, del
}

// readFrame reads one frame and decodes the envelope array.
func readFrame(ctx context.Context, conn *websocket.Conn) ([]wsEnvelope, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msgs []wsEnvelope
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return msgs, nil
}

// authenticated reports whether a frame contains the auth success ack.
func authenticated(msgs []wsEnvelope) bool {
	for _, m := range msgs {
		if m.T == "success" && m.Msg == "authenticated" {
			return true
		}
	}
	return false
}

// describe summarizes a frame for error messages.
func describe(msgs []wsEnvelope) string {
	if len(msgs) == 0 {
		return "empty frame"
	}
	return fmt.Sprintf("T=%q code=%d msg=%q", msgs[0].T, msgs[0].Code, msgs[0].Msg)
}
