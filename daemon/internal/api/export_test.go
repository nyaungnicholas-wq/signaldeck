package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newExportDeps builds Deps over a temp store holding one symbol with bars, so
// a refusal can be told apart from an empty result.
func newExportDeps(t *testing.T) (Deps, md.Symbol) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "export.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	s, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	bars := make([]md.Bar, 0, 10)
	for i := range 10 {
		ts := int64(1700000000 + i*86400)
		bars = append(bars, md.Bar{SymbolID: s.ID, TF: md.TF1d, Ts: ts,
			Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 100})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("insert bars: %v", err)
	}
	return Deps{St: st, Cfg: config.Config{}}, s
}

// remoteReq is a request from somewhere that is NOT this machine — the only
// case in which serving licensed rows is redistribution rather than personal
// use.
func remoteReq(target string) *http.Request {
	r := httptest.NewRequest("GET", target, nil)
	r.RemoteAddr = "203.0.113.9:51234"
	return r
}

// A CSV of the same licensed rows is the same redistribution as /api/bars, so
// every export must refuse with the SAME status and the SAME actionable notice.
// Before the fix the export handlers carried no guard at all and streamed the
// rows to any remote caller.
func TestExportsRefuseRedistributionLikeBars(t *testing.T) {
	d, _ := newExportDeps(t)

	barsRec := httptest.NewRecorder()
	d.bars(barsRec, remoteReq("/api/bars?symbol=AAPL&market=stocks"))
	if barsRec.Code != 451 {
		t.Fatalf("premise broken: /api/bars from a remote caller = %d, want 451", barsRec.Code)
	}
	notice := barsRec.Body.String()

	for _, tc := range []struct {
		name   string
		target string
		h      func(http.ResponseWriter, *http.Request)
	}{
		{"bars.csv", "/api/export/bars.csv?symbol=AAPL&market=stocks", d.exportBars},
		{"scores.csv", "/api/export/scores.csv?symbol=AAPL&market=stocks", d.exportScores},
		{"outcomes.csv", "/api/export/outcomes.csv?symbol=AAPL&market=stocks", d.exportOutcomes},
	} {
		rec := httptest.NewRecorder()
		tc.h(rec, remoteReq(tc.target))
		if rec.Code != 451 {
			t.Errorf("%s: status %d, want 451 (%d bytes served)", tc.name, rec.Code, rec.Body.Len())
		}
		if rec.Body.String() != notice {
			t.Errorf("%s: notice differs from /api/bars — the two policies have drifted\n got %q\nwant %q",
				tc.name, rec.Body.String(), notice)
		}
	}
}

// The operator's own machine may read its own data; the assertion flag has the
// same effect it has on /api/bars. Both paths must stay open or the guard is
// just an outage.
func TestExportsServeLoopbackAndAssertedOperator(t *testing.T) {
	d, _ := newExportDeps(t)

	rec := httptest.NewRecorder()
	local := httptest.NewRequest("GET", "/api/export/bars.csv?symbol=AAPL&market=stocks", nil)
	local.RemoteAddr = "127.0.0.1:51234" // httptest's default RemoteAddr is not loopback
	d.exportBars(rec, local)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ts,open") {
		t.Fatalf("loopback export: %d %q", rec.Code, rec.Body.String())
	}

	d.Cfg.AllowRawExport = true
	rec = httptest.NewRecorder()
	d.exportBars(rec, remoteReq("/api/export/bars.csv?symbol=AAPL&market=stocks"))
	if rec.Code != 200 {
		t.Fatalf("asserted remote export: %d, want 200", rec.Code)
	}
}

// exportOutcomes had no symbol scoping and asked the store for 100,000 rows —
// a whole-database dump behind a URL with no parameters. It must name a symbol
// like every other export, and its row request must be bounded.
func TestExportOutcomesRequiresSymbolAndIsBounded(t *testing.T) {
	d, _ := newExportDeps(t)

	rec := httptest.NewRecorder()
	d.exportOutcomes(rec, httptest.NewRequest("GET", "/api/export/outcomes.csv?market=stocks", nil))
	if rec.Code != 404 {
		t.Errorf("unscoped outcomes export: %d, want 404 (symbol required)", rec.Code)
	}

	if maxExportRows > 20000 {
		t.Errorf("maxExportRows = %d: an export bound that large is not a bound", maxExportRows)
	}
	for _, q := range []string{"&limit=999999", "&limit=-1", ""} {
		if got := exportLimit(remoteReq("/x?symbol=AAPL&market=stocks" + q)); got <= 0 || got > maxExportRows {
			t.Errorf("exportLimit(%q) = %d, want within (0, %d]", q, got, maxExportRows)
		}
	}
}

// A slow-body client held a connection open for 60s having sent 15 bytes. The
// server must carry deadlines, and the streaming route must be exempt from the
// write deadline or every legitimate SSE subscriber is killed on a timer.
func TestServerCarriesTimeouts(t *testing.T) {
	d, _ := newExportDeps(t)
	srv := d.httpServer(http.NewServeMux())

	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout unset")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout unset — keep-alive connections are never reaped")
	}
	// A blanket WriteTimeout would kill long-lived SSE streams, so the write
	// deadline is applied per-route instead (see withDeadlines).
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v: a server-wide write deadline kills SSE", srv.WriteTimeout)
	}
	if !streamPath("/api/stream/snaps") || streamPath("/api/bars") {
		t.Error("streamPath does not identify the streaming route")
	}
}

// SSE subscribers each cost ONE rate-limit token and then run unbounded: ten
// anonymous streams doubled daemon CPU. Beyond the cap the daemon must refuse
// the new subscriber rather than degrade every existing one.
func TestStreamSubscriberCapRejectsBeyondLimit(t *testing.T) {
	d, s := newExportDeps(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for range maxStreamSubscribers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/api/stream/snaps?symbol="+s.Symbol+"&market=stocks", nil).WithContext(ctx)
			d.streamSnaps(httptest.NewRecorder(), r)
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	for streamSubscribers.Load() < int64(maxStreamSubscribers) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	rec := httptest.NewRecorder()
	d.streamSnaps(rec, httptest.NewRequest("GET", "/api/stream/snaps?symbol="+s.Symbol+"&market=stocks", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("subscriber %d: status %d, want 503", maxStreamSubscribers+1, rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("refusal carries no Retry-After")
	}

	cancel()
	wg.Wait()
	if n := streamSubscribers.Load(); n != 0 {
		t.Fatalf("subscriber count leaked: %d still held after every stream ended", n)
	}
}
