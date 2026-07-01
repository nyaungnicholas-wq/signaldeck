package cryptohist

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func TestPairFor(t *testing.T) {
	tests := []struct {
		symbol string
		want   string
	}{
		{"BTC/USD", "XBTUSD"},
		{"ETH/USD", "ETHUSD"},
		{"SOL/USD", "SOLUSD"},
		{"ETH/BTC", "ETHXBT"}, // BTC renamed on the quote side too
		{"DOGE/EUR", "DOGEEUR"},
		{"XBTUSD", "XBTUSD"}, // already Kraken-shaped: pass through
	}
	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			if got := PairFor(tt.symbol); got != tt.want {
				t.Errorf("PairFor(%q) = %q, want %q", tt.symbol, got, tt.want)
			}
		})
	}
}

// fixtureRow is one expected bar per timeframe fixture.
type fixtureRow struct {
	ts                 int64
	o, h, l, c, v      float64
	os, hs, ls, cs, vs string // string forms as Kraken serves them
}

// fixtures returns per-interval rows with distinct timestamps so the test can
// prove interval=1440 landed in tf 1d, 60 in 1h and 1 in 1m — not merely that
// bars exist somewhere.
func fixtures() map[int][]fixtureRow {
	mk := func(ts int64, o, h, l, c, v string) fixtureRow {
		pf := func(s string) float64 {
			var f float64
			fmt.Sscanf(s, "%g", &f) //nolint:errcheck
			return f
		}
		return fixtureRow{ts, pf(o), pf(h), pf(l), pf(c), pf(v), o, h, l, c, v}
	}
	return map[int][]fixtureRow{
		1440: {
			mk(1688601600, "30306.1", "30450.0", "30100.5", "30305.7", "3.39243896"),
			mk(1688688000, "30305.7", "30500.2", "30200.1", "30410.8", "1.13931201"),
			mk(1688774400, "30410.8", "30600.0", "30350.6", "30599.9", "0.51234567"),
		},
		60: {
			mk(1688671200, "30306.1", "30310.2", "30300.7", "30305.7", "0.25000000"),
			mk(1688674800, "30305.7", "30306.1", "30295.5", "30298.8", "0.10500000"),
			mk(1688678400, "30298.8", "30301.0", "30290.6", "30300.9", "0.08123456"),
		},
		1: {
			mk(1688678400, "30300.9", "30301.5", "30300.1", "30301.2", "0.01000000"),
			mk(1688678460, "30301.2", "30302.0", "30301.0", "30301.8", "0.00250000"),
			mk(1688678520, "30301.8", "30301.9", "30300.5", "30300.7", "0.00500000"),
		},
	}
}

// ohlcBody renders a realistic Kraken response: the result keyed under the
// classified pair name XXBTZUSD (differing from the requested XBTUSD) plus
// the "last" pagination cursor.
func ohlcBody(rows []fixtureRow) string {
	var b strings.Builder
	b.WriteString(`{"error":[],"result":{"XXBTZUSD":[`)
	for i, r := range rows {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `[%d,%q,%q,%q,%q,"30305.9",%q,23]`, r.ts, r.os, r.hs, r.ls, r.cs, r.vs)
	}
	fmt.Fprintf(&b, `],"last":%d}}`, rows[len(rows)-1].ts)
	return b.String()
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() }) //nolint:errcheck
	return st
}

func TestBackfill(t *testing.T) {
	fx := fixtures()
	var mu sync.Mutex
	var gotIntervals []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/0/public/OHLC" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if pair := r.URL.Query().Get("pair"); pair != "XBTUSD" {
			t.Errorf("pair = %q, want XBTUSD", pair)
		}
		iv := r.URL.Query().Get("interval")
		mu.Lock()
		gotIntervals = append(gotIntervals, iv)
		mu.Unlock()
		var n int
		fmt.Sscanf(iv, "%d", &n) //nolint:errcheck
		rows, ok := fx[n]
		if !ok {
			t.Errorf("unexpected interval %q", iv)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, ohlcBody(rows)) //nolint:errcheck
	}))
	defer srv.Close()

	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	c := &Client{Base: srv.URL, HTTP: srv.Client()}
	counts, err := c.Backfill(ctx, st, sym.ID, "BTC/USD")
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	wantIntervals := []string{"1440", "60", "1"}
	mu.Lock()
	if len(gotIntervals) != len(wantIntervals) {
		t.Fatalf("requested intervals %v, want %v", gotIntervals, wantIntervals)
	}
	for i, w := range wantIntervals {
		if gotIntervals[i] != w {
			t.Errorf("request %d interval = %q, want %q", i, gotIntervals[i], w)
		}
	}
	mu.Unlock()

	tfFor := map[int]md.Timeframe{1440: md.TF1d, 60: md.TF1h, 1: md.TF1m}
	for interval, tf := range tfFor {
		rows := fx[interval]
		if counts[tf] != len(rows) {
			t.Errorf("counts[%s] = %d, want %d", tf, counts[tf], len(rows))
		}
		bars, err := st.LastBars(ctx, sym.ID, tf, 10)
		if err != nil {
			t.Fatalf("read %s bars: %v", tf, err)
		}
		if len(bars) != len(rows) {
			t.Fatalf("stored %d %s bars, want %d", len(bars), tf, len(rows))
		}
		for i, want := range rows {
			got := bars[i]
			if got.Ts != want.ts {
				t.Errorf("%s bar %d ts = %d, want %d", tf, i, got.Ts, want.ts)
			}
			for _, f := range []struct {
				name      string
				got, want float64
			}{
				{"open", got.Open, want.o}, {"high", got.High, want.h},
				{"low", got.Low, want.l}, {"close", got.Close, want.c},
				{"volume", got.Volume, want.v},
			} {
				if math.Abs(f.got-f.want) > 1e-9 {
					t.Errorf("%s bar %d %s = %v, want %v", tf, i, f.name, f.got, f.want)
				}
			}
		}
	}
}

func TestBackfillErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantSub string
	}{
		{
			name: "api error array surfaces",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"error":["EQuery:Unknown asset pair"],"result":{}}`) //nolint:errcheck
			},
			wantSub: "EQuery:Unknown asset pair",
		},
		{
			name: "http error status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusBadGateway)
			},
			wantSub: "502",
		},
		{
			name: "missing pair key",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"error":[],"result":{"last":1}}`) //nolint:errcheck
			},
			wantSub: "no pair key",
		},
		{
			name: "malformed price string",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"error":[],"result":{"XXBTZUSD":[[1688671200,"oops","1","1","1","1","1",1]],"last":1}}`) //nolint:errcheck
			},
			wantSub: "row 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			st := openTestStore(t)
			c := &Client{Base: srv.URL, HTTP: srv.Client()}
			counts, err := c.Backfill(context.Background(), st, 1, "BTC/USD")
			if err == nil {
				t.Fatal("Backfill error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err, tt.wantSub)
			}
			// First call fails, so nothing should have been counted.
			if len(counts) != 0 {
				t.Errorf("counts = %v, want empty", counts)
			}
		})
	}
}

func TestBackfillContextCancel(t *testing.T) {
	// The server answers the first call fine; cancellation must interrupt the
	// 500ms inter-call sleep instead of blocking through it.
	fx := fixtures()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, ohlcBody(fx[1440])) //nolint:errcheck
	}))
	defer srv.Close()
	st := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	go cancel()
	c := &Client{Base: srv.URL, HTTP: srv.Client()}
	_, err := c.Backfill(ctx, st, 1, "BTC/USD")
	if err == nil {
		t.Fatal("Backfill error = nil, want context error")
	}
}
