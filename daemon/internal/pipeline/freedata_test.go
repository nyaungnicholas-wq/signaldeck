package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/fred"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

func TestFredPoller_NoClientNoOp(t *testing.T) {
	st := openStore(t)
	w := &FredPoller{St: st, Client: nil}
	msg, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "skipped") {
		t.Fatalf("want skipped, got %q", msg)
	}
}

func TestFredPoller_IngestsAndReportsVIX(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("observation_date,VIXCLS\n2026-07-01,12.88\n2026-07-02,14.10\n"))
	}))
	defer srv.Close()

	st := openStore(t)
	c := fred.New("")
	c.CSVBase = srv.URL
	w := &FredPoller{St: st, Client: c, Series: []string{"VIXCLS"}}
	msg, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "vix 14.10") {
		t.Fatalf("run message missing latest vix: %q", msg)
	}
	if v, ok, _ := st.LatestVIX(context.Background()); !ok || v != 14.10 {
		t.Fatalf("VIX not persisted: %v ok=%v", v, ok)
	}
}

func TestEdgarFetcher_NoClientNoOp(t *testing.T) {
	st := openStore(t)
	w := &EdgarFetcher{St: st, Client: nil}
	msg, err := w.Run(context.Background())
	// A fetcher with no client has never fetched a filing and never will. It
	// must not crash the fleet, and it must not read as success either -- this
	// asserted err == nil, so it filed status=ok on every run forever.
	if !errors.Is(err, workers.ErrDegraded) {
		t.Fatalf("no client must report DEGRADED, not %v", err)
	}
	if !strings.Contains(msg, "skipped") {
		t.Fatalf("want skipped, got %q", msg)
	}
}

// TestEdgarSelectWindow_RotatesCursor proves the alphabetical sweep cursor
// advances across runs and wraps around, so a universe larger than one batch
// is covered over successive daily runs.
func TestEdgarSelectWindow_RotatesCursor(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	// Six stocks, batch of 2 ⇒ three runs cover A..F then wrap.
	stocks := []md.Symbol{
		{Symbol: "AAA"}, {Symbol: "BBB"}, {Symbol: "CCC"},
		{Symbol: "DDD"}, {Symbol: "EEE"}, {Symbol: "FFF"},
	}

	w1 := selectWindow(ctx, st, cloneSyms(stocks), 2)
	if got := syms(w1); got != "AAA,BBB" {
		t.Fatalf("run1 = %s, want AAA,BBB", got)
	}
	w2 := selectWindow(ctx, st, cloneSyms(stocks), 2)
	if got := syms(w2); got != "CCC,DDD" {
		t.Fatalf("run2 = %s, want CCC,DDD", got)
	}
	w3 := selectWindow(ctx, st, cloneSyms(stocks), 2)
	if got := syms(w3); got != "EEE,FFF" {
		t.Fatalf("run3 = %s, want EEE,FFF", got)
	}
	// cursor now at FFF (the last symbol) ⇒ next run wraps to the beginning.
	w4 := selectWindow(ctx, st, cloneSyms(stocks), 2)
	if got := syms(w4); got != "AAA,BBB" {
		t.Fatalf("run4 (wrap) = %s, want AAA,BBB", got)
	}
}

func TestEdgarSelectWindow_SmallUniverseReturnsAll(t *testing.T) {
	st := openStore(t)
	stocks := []md.Symbol{{Symbol: "AAA"}, {Symbol: "BBB"}}
	w := selectWindow(context.Background(), st, cloneSyms(stocks), 50)
	if syms(w) != "AAA,BBB" {
		t.Fatalf("want all symbols, got %s", syms(w))
	}
}

func cloneSyms(s []md.Symbol) []md.Symbol {
	out := make([]md.Symbol, len(s))
	copy(out, s)
	return out
}

func syms(s []md.Symbol) string {
	var b []string
	for _, x := range s {
		b = append(b, x.Symbol)
	}
	return strings.Join(b, ",")
}
