package alpaca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A vendor-rejected symbol must not destroy the batch around it.
//
// Alpaca 400s the WHOLE multi-symbol bars request when any single symbol is
// invalid. Measured live 2026-08-24..26: universe-poller failed every day on
// ATC.220816 (delisted 2022-08-16), taking up to MaxBatchSymbols symbols' bars
// down with it and erroring the worker run -- while universe/poller.go's own
// comment asserted such names were "simply absent from the backfill -- never
// fatal". They were fatal. These tests pin the repair.

func TestInvalidSymbolFromErr(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"live shape", errors.New(`alpaca: multi bars: status 400: {"message":"invalid symbol: ATC.220816"}`), "ATC.220816"},
		{"dotted class share", errors.New(`invalid symbol: BRK.B"}`), "BRK.B"},
		{"hyphen kept", errors.New(`invalid symbol: FOO-BAR"}`), "FOO-BAR"},
		{"lowercased input is normalised", errors.New(`invalid symbol: abc"}`), "ABC"},
		{"unrelated 400", errors.New(`alpaca: multi bars: status 400: {"message":"end is too late"}`), ""},
		{"429", errors.New("alpaca: rate limited"), ""},
		{"empty name", errors.New(`invalid symbol: "}`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := invalidSymbolFromErr(tc.err); got != tc.want {
				t.Errorf("invalidSymbolFromErr = %q, want %q", got, tc.want)
			}
		})
	}
}

// The batch drops exactly the named symbol and keeps every other symbol's bars.
// Without the repair this test fails with the 400 propagated out of
// BackfillDailyMulti and zero bars stored for AAA and CCC.
func TestBackfillDailyMultiDropsVendorRejectedSymbol(t *testing.T) {
	fastBackoff(t)
	var reqCount int32
	var sawBadInRequest int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		syms := strings.Split(r.URL.Query().Get("symbols"), ",")
		for _, s := range syms {
			if s == "ATC.220816" {
				atomic.AddInt32(&sawBadInRequest, 1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"invalid symbol: ATC.220816"}`))
				return
			}
		}
		bars := map[string]any{}
		for _, s := range syms {
			bars[s] = []map[string]any{
				{"t": "2026-06-01T04:00:00Z", "o": 1, "h": 2, "l": 0.5, "c": 1.5, "v": 100},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"bars": bars, "next_page_token": nil})
	}))
	defer srv.Close()

	st := openTestStore(t)
	resolve := registerStocks(t, st, "AAA", "CCC")
	c := New("k", "s")
	c.BaseData = srv.URL

	in := []string{"AAA", "ATC.220816", "CCC"}
	counts, err := c.BackfillDailyMulti(context.Background(), st,
		in, resolve, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("one bad symbol still failed the whole batch: %v", err)
	}
	if atomic.LoadInt32(&sawBadInRequest) == 0 {
		t.Fatal("test did not exercise the rejection path")
	}
	for _, s := range []string{"AAA", "CCC"} {
		if counts[s] != 1 {
			t.Errorf("counts[%s] = %d, want 1 - a good symbol lost its bars to the bad one", s, counts[s])
		}
	}
	if counts["ATC.220816"] != 0 {
		t.Errorf("the rejected symbol was counted: %v", counts)
	}
	// The caller's slice must not be mutated - callers reuse it for other timeframes.
	if len(in) != 3 || in[1] != "ATC.220816" {
		t.Errorf("caller slice was mutated: %v", in)
	}
}

// Every symbol rejected is not an error: there is simply nothing left to fetch.
func TestBackfillDailyMultiAllSymbolsRejected(t *testing.T) {
	fastBackoff(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first := strings.Split(r.URL.Query().Get("symbols"), ",")[0]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid symbol: ` + first + `"}`))
	}))
	defer srv.Close()

	st := openTestStore(t)
	resolve := registerStocks(t, st, "AAA", "BBB")
	c := New("k", "s")
	c.BaseData = srv.URL

	counts, err := c.BackfillDailyMulti(context.Background(), st,
		[]string{"AAA", "BBB"}, resolve, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("draining the batch should not be an error: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("counts = %v, want empty", counts)
	}
}

// A 400 that names a symbol NOT in the batch must propagate, or the loop would
// never terminate on a symbol it cannot remove.
func TestBackfillDailyMultiUnremovableSymbolPropagates(t *testing.T) {
	fastBackoff(t)
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid symbol: NOTINBATCH"}`))
	}))
	defer srv.Close()

	st := openTestStore(t)
	resolve := registerStocks(t, st, "AAA")
	c := New("k", "s")
	c.BaseData = srv.URL

	_, err := c.BackfillDailyMulti(context.Background(), st,
		[]string{"AAA"}, resolve, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("a 400 naming a symbol we cannot drop must propagate, not spin")
	}
	if n := atomic.LoadInt32(&reqCount); n > 3 {
		t.Errorf("made %d requests - it is retrying a symbol it cannot remove", n)
	}
}
