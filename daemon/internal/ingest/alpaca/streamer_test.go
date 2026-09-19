package alpaca

import (
	"context"
	"reflect"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// testResolver maps a fixed symbol table; anything else is untracked.
func testResolver(m map[string]int64) func(string) (int64, bool) {
	return func(sym string) (int64, bool) {
		id, ok := m[sym]
		return id, ok
	}
}

func TestHandleFrame(t *testing.T) {
	ts1 := time.Date(2026, 6, 30, 13, 30, 0, 0, time.UTC)
	ts2 := time.Date(2026, 6, 30, 13, 31, 0, 0, time.UTC)
	tests := []struct {
		name     string
		frame    string
		wantErr  bool
		wantBars map[int64][]md.Bar // symbolID → expected stored 1m bars (ascending)
	}{
		{
			name: "single bar",
			frame: `[{"T":"b","S":"AAPL","o":190.1,"h":190.9,"l":189.8,"c":190.5,"v":12345,` +
				`"t":"2026-06-30T13:30:00Z"}]`,
			wantBars: map[int64][]md.Bar{
				1: {{SymbolID: 1, TF: md.TF1m, Ts: ts1.Unix(), Open: 190.1, High: 190.9, Low: 189.8, Close: 190.5, Volume: 12345}},
			},
		},
		{
			name: "mixed frame with acks and two bars",
			frame: `[{"T":"success","msg":"connected"},` +
				`{"T":"subscription","bars":["AAPL","MSFT"]},` +
				`{"T":"b","S":"AAPL","o":1,"h":2,"l":0.5,"c":1.5,"v":10,"t":"2026-06-30T13:30:00Z"},` +
				`{"T":"b","S":"MSFT","o":400,"h":401,"l":399,"c":400.5,"v":20,"t":"2026-06-30T13:31:00Z"}]`,
			wantBars: map[int64][]md.Bar{
				1: {{SymbolID: 1, TF: md.TF1m, Ts: ts1.Unix(), Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 10}},
				2: {{SymbolID: 2, TF: md.TF1m, Ts: ts2.Unix(), Open: 400, High: 401, Low: 399, Close: 400.5, Volume: 20}},
			},
		},
		{
			name:     "unresolved symbol skipped",
			frame:    `[{"T":"b","S":"TSLA","o":1,"h":2,"l":0.5,"c":1.5,"v":10,"t":"2026-06-30T13:30:00Z"}]`,
			wantBars: map[int64][]md.Bar{},
		},
		{
			name:     "error element is logged not fatal",
			frame:    `[{"T":"error","code":406,"msg":"connection limit exceeded"}]`,
			wantBars: map[int64][]md.Bar{},
		},
		{
			name:     "acks only",
			frame:    `[{"T":"success","msg":"authenticated"}]`,
			wantBars: map[int64][]md.Bar{},
		},
		{
			name:    "malformed json",
			frame:   `{"T":"b"`,
			wantErr: true,
		},
		{
			name:    "bad bar timestamp",
			frame:   `[{"T":"b","S":"AAPL","o":1,"h":2,"l":0.5,"c":1.5,"v":10,"t":"not-a-time"}]`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openTestStore(t)
			s := NewStreamer(New("k", "s"), st, testResolver(map[string]int64{"AAPL": 1, "MSFT": 2}), "")

			err := s.handleFrame(context.Background(), []byte(tt.frame), map[string]bool{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("handleFrame: %v", err)
			}
			for _, id := range []int64{1, 2} {
				got, err := st.Bars(context.Background(), id, md.TF1m, 0, 1<<62, 0)
				if err != nil {
					t.Fatalf("read bars for %d: %v", id, err)
				}
				want := tt.wantBars[id]
				if len(got) == 0 && len(want) == 0 {
					continue
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("bars for symbol %d = %+v, want %+v", id, got, want)
				}
			}
		})
	}
}

func TestDiffSymbols(t *testing.T) {
	tests := []struct {
		name    string
		want    map[string]bool
		have    map[string]bool
		wantAdd []string
		wantDel []string
	}{
		{
			name:    "cold start subscribes everything",
			want:    map[string]bool{"AAPL": true, "MSFT": true},
			have:    map[string]bool{},
			wantAdd: []string{"AAPL", "MSFT"},
		},
		{
			name:    "swap one symbol",
			want:    map[string]bool{"AAPL": true, "NVDA": true},
			have:    map[string]bool{"AAPL": true, "MSFT": true},
			wantAdd: []string{"NVDA"},
			wantDel: []string{"MSFT"},
		},
		{
			name: "no change",
			want: map[string]bool{"AAPL": true},
			have: map[string]bool{"AAPL": true},
		},
		{
			name:    "clear all",
			want:    map[string]bool{},
			have:    map[string]bool{"AAPL": true},
			wantDel: []string{"AAPL"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			add, del := diffSymbols(tt.want, tt.have)
			if !reflect.DeepEqual(add, tt.wantAdd) {
				t.Errorf("add = %v, want %v", add, tt.wantAdd)
			}
			if !reflect.DeepEqual(del, tt.wantDel) {
				t.Errorf("del = %v, want %v", del, tt.wantDel)
			}
		})
	}
}

func TestSetSymbolsNudgeAndSnapshot(t *testing.T) {
	s := NewStreamer(New("k", "s"), nil, testResolver(nil), "")

	s.SetSymbols([]string{"AAPL", "MSFT"})
	select {
	case <-s.nudge:
	default:
		t.Fatal("SetSymbols did not queue a nudge")
	}
	if got := s.snapshotSymbols(); !reflect.DeepEqual(got, []string{"AAPL", "MSFT"}) {
		t.Errorf("snapshot = %v, want [AAPL MSFT]", got)
	}

	// A second call while a nudge is pending must not block, and the snapshot
	// must reflect the latest set (the whole point of full-set resync).
	s.SetSymbols([]string{"NVDA"})
	s.SetSymbols([]string{"NVDA", "AMD"})
	if got := s.snapshotSymbols(); !reflect.DeepEqual(got, []string{"NVDA", "AMD"}) {
		t.Errorf("snapshot = %v, want [NVDA AMD]", got)
	}

	// Caller mutating its slice after the call must not leak into the streamer.
	in := []string{"SPY"}
	s.SetSymbols(in)
	in[0] = "MUTATED"
	if got := s.snapshotSymbols(); !reflect.DeepEqual(got, []string{"SPY"}) {
		t.Errorf("snapshot after caller mutation = %v, want [SPY]", got)
	}
}

func TestAuthenticated(t *testing.T) {
	tests := []struct {
		name string
		msgs []wsEnvelope
		want bool
	}{
		{"auth ack", []wsEnvelope{{T: "success", Msg: "authenticated"}}, true},
		{"connected only", []wsEnvelope{{T: "success", Msg: "connected"}}, false},
		{"error", []wsEnvelope{{T: "error", Code: 402, Msg: "auth failed"}}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := authenticated(tt.msgs); got != tt.want {
				t.Errorf("authenticated = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSubscriptionAckIsAuthoritative pins the defect that made a refused
// subscription invisible: resync marked a symbol live as soon as the subscribe
// frame was WRITTEN, and the server's reply -- the only message that knows what
// was actually granted -- had no field on wsEnvelope, so it fell through to the
// default branch and was discarded. A symbol Alpaca refused stayed "subscribed"
// for the life of the connection, produced no bars, and was never retried.
func TestSubscriptionAckIsAuthoritative(t *testing.T) {
	st := openTestStore(t)
	s := NewStreamer(New("k", "s"), st, testResolver(map[string]int64{"AAPL": 1, "MSFT": 2}), "")

	// What resync believed after its writes succeeded. ZZZZ is the refused one.
	subscribed := map[string]bool{"AAPL": true, "MSFT": true, "ZZZZ": true}

	// What the server says is actually live. Note it also adds a symbol we did
	// not have locally, so the ack is treated as the whole truth and not as a
	// delete-only filter.
	frame := []byte(`[{"T":"subscription","trades":[],"quotes":[],"bars":["AAPL","MSFT","NVDA"]}]`)
	if err := s.handleFrame(context.Background(), frame, subscribed); err != nil {
		t.Fatalf("handleFrame on a subscription ack: %v", err)
	}

	want := map[string]bool{"AAPL": true, "MSFT": true, "NVDA": true}
	if len(subscribed) != len(want) {
		t.Fatalf("subscribed = %v; want exactly %v", subscribed, want)
	}
	for sym := range want {
		if !subscribed[sym] {
			t.Errorf("server granted %s but it is not marked subscribed", sym)
		}
	}
	if subscribed["ZZZZ"] {
		t.Error("ZZZZ was refused by the server but is still marked subscribed -- " +
			"this is the bug: a refused symbol believed live forever, never retried")
	}
}
