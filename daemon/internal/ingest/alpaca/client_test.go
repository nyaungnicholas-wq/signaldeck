package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() }) //nolint:errcheck
	return st
}

// fastPages shrinks the inter-page pause for tests.
func fastPages(t *testing.T) {
	t.Helper()
	old := pagePause
	pagePause = time.Millisecond
	t.Cleanup(func() { pagePause = old })
}

func TestBackfillPaging(t *testing.T) {
	fastPages(t)
	tests := []struct {
		name          string
		call          func(c *Client, st *store.Store, id int64) (int, error)
		wantTimeframe string
		wantTF        md.Timeframe
		// start must be roughly this far in the past (sanity, not exact)
		minAge, maxAge time.Duration
	}{
		{
			name: "daily two pages",
			call: func(c *Client, st *store.Store, id int64) (int, error) {
				return c.BackfillDaily(context.Background(), st, id, "AAPL")
			},
			wantTimeframe: "1Day",
			wantTF:        md.TF1d,
			minAge:        729 * 24 * time.Hour,
			maxAge:        732 * 24 * time.Hour,
		},
		{
			name: "minute two pages",
			call: func(c *Client, st *store.Store, id int64) (int, error) {
				return c.BackfillMinute(context.Background(), st, id, "AAPL")
			},
			wantTimeframe: "1Min",
			wantTF:        md.TF1m,
			minAge:        59 * 24 * time.Hour,
			maxAge:        61 * 24 * time.Hour,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotTokens []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/stocks/AAPL/bars" {
					t.Errorf("path = %q, want /stocks/AAPL/bars", r.URL.Path)
				}
				if got := r.Header.Get("APCA-API-KEY-ID"); got != "k" {
					t.Errorf("key header = %q, want k", got)
				}
				if got := r.Header.Get("APCA-API-SECRET-KEY"); got != "s" {
					t.Errorf("secret header = %q, want s", got)
				}
				q := r.URL.Query()
				for k, want := range map[string]string{
					"timeframe": tt.wantTimeframe, "limit": "10000",
					"adjustment": "split", "feed": "sip",
				} {
					if got := q.Get(k); got != want {
						t.Errorf("query %s = %q, want %q", k, got, want)
					}
				}
				start, err := time.Parse(time.RFC3339, q.Get("start"))
				if err != nil {
					t.Errorf("start %q not RFC3339: %v", q.Get("start"), err)
				} else if age := time.Since(start); age < tt.minAge || age > tt.maxAge {
					t.Errorf("start age = %v, want in [%v, %v]", age, tt.minAge, tt.maxAge)
				}
				gotTokens = append(gotTokens, q.Get("page_token"))
				w.Header().Set("Content-Type", "application/json")
				if q.Get("page_token") == "" {
					fmt.Fprint(w, `{"bars":[
						{"t":"2026-06-01T04:00:00Z","o":1,"h":2,"l":0.5,"c":1.5,"v":100},
						{"t":"2026-06-02T04:00:00Z","o":1.5,"h":3,"l":1,"c":2.5,"v":200}
					],"next_page_token":"tok2"}`)
					return
				}
				fmt.Fprint(w, `{"bars":[
					{"t":"2026-06-03T04:00:00Z","o":2.5,"h":4,"l":2,"c":3.5,"v":300}
				],"next_page_token":null}`)
			}))
			defer srv.Close()

			st := openTestStore(t)
			c := New("k", "s")
			c.BaseData = srv.URL

			n, err := tt.call(c, st, 7)
			if err != nil {
				t.Fatalf("backfill: %v", err)
			}
			if n != 3 {
				t.Errorf("bar count = %d, want 3", n)
			}
			wantTokens := []string{"", "tok2"}
			if len(gotTokens) != len(wantTokens) {
				t.Fatalf("requests = %d (%v), want %d", len(gotTokens), gotTokens, len(wantTokens))
			}
			for i := range wantTokens {
				if gotTokens[i] != wantTokens[i] {
					t.Errorf("request %d page_token = %q, want %q", i, gotTokens[i], wantTokens[i])
				}
			}

			bars, err := st.Bars(context.Background(), 7, tt.wantTF, 0, 1<<62, 0)
			if err != nil {
				t.Fatalf("read back bars: %v", err)
			}
			if len(bars) != 3 {
				t.Fatalf("stored bars = %d, want 3", len(bars))
			}
			first := bars[0]
			wantTs := time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC).Unix()
			if first.Ts != wantTs || first.Open != 1 || first.High != 2 ||
				first.Low != 0.5 || first.Close != 1.5 || first.Volume != 100 {
				t.Errorf("first bar = %+v, want ts=%d o=1 h=2 l=0.5 c=1.5 v=100", first, wantTs)
			}
		})
	}
}

func TestBackfillHTTPError(t *testing.T) {
	fastPages(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	st := openTestStore(t)
	c := New("k", "s")
	c.BaseData = srv.URL
	if _, err := c.BackfillDaily(context.Background(), st, 1, "AAPL"); err == nil {
		t.Fatal("want error on 429, got nil")
	}
}

func TestValidateSymbol(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     any
		wantName string
		wantOK   bool
		wantErr  bool
	}{
		{
			name:   "active asset",
			status: http.StatusOK,
			body: map[string]any{
				"name": "Apple Inc. Common Stock", "tradable": true, "status": "active",
			},
			wantName: "Apple Inc. Common Stock",
			wantOK:   true,
		},
		{
			name:   "inactive asset",
			status: http.StatusOK,
			body: map[string]any{
				"name": "Dead Co", "tradable": false, "status": "inactive",
			},
			wantName: "Dead Co",
			wantOK:   false,
		},
		{
			name:   "unknown symbol 404",
			status: http.StatusNotFound,
			body:   map[string]any{"message": "asset not found"},
			wantOK: false,
		},
		{
			name:    "server error",
			status:  http.StatusInternalServerError,
			body:    map[string]any{"message": "boom"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/assets/AAPL" {
					t.Errorf("path = %q, want /v2/assets/AAPL", r.URL.Path)
				}
				if got := r.Header.Get("APCA-API-KEY-ID"); got != "k" {
					t.Errorf("key header = %q, want k", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(tt.body) //nolint:errcheck
			}))
			defer srv.Close()

			c := New("k", "s")
			c.BasePaper = srv.URL
			name, ok, err := c.ValidateSymbol(context.Background(), "AAPL")
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateSymbol: %v", err)
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}
