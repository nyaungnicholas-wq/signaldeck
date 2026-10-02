package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// H-10 (2026-10-02): /api/vol-forecast/latest decided its verdict gate only
// when the cached body was built, so a warming record cached a 200
// {"available":false} for ten minutes and a verdict that left the pass kept
// serving cached forecasts. The gate is now read on every request.

// resetVolRecordCache stands in for the record cache refreshing, so the next
// read sees the record's new verdict: a fresh cache, and for an existing
// server its persisted last good body removed (a new cache would reload it).
func resetVolRecordCache(t *testing.T, servers ...Deps) {
	t.Helper()
	old := sharedVolRecordSWR
	sharedVolRecordSWR = newSWRBodyCache(10 * time.Minute)
	t.Cleanup(func() { sharedVolRecordSWR = old })
	for _, d := range servers {
		if f := d.cacheFile(volRecordCacheName, volRecordPersistFormat); f != "" {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
}

func seedLatestForecast(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRVForecast(ctx, store.RVForecast{SymbolID: sym.ID, Ts: 1_790_086_400, Horizon: 1, RVHat: 0.0004,
		NullRW: 0.1, NullEWMA: 0.1, NTrain: 600, Revision: "t"}, time.Unix(1_790_086_400, 0)); err != nil {
		t.Fatal(err)
	}
}

func readLatest(d Deps) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	d.serveVolLatest(rec, httptest.NewRequest("GET", "/api/vol-forecast/latest", nil))
	return rec
}

func TestVolForecastLatestGateIsPerRequest(t *testing.T) {
	show := func(rec *httptest.ResponseRecorder) string {
		return rec.Result().Status + " X-Cache=" + rec.Header().Get("X-Cache") + " " + rec.Body.String()
	}

	// 1. A pass serves the forecasts, and the forecast body is what is cached.
	recordVerdict(t, volPassVerdict)
	resetVolRecordCache(t)
	_, st, d := newTestServer(t, nil)
	seedLatestForecast(t, st)
	if rec := readLatest(d); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"forecasts":`) ||
		!strings.Contains(rec.Body.String(), "AAA") {
		t.Fatalf("pass: %s", show(rec))
	}
	if rec := readLatest(d); rec.Header().Get("X-Cache") != "hit" {
		t.Fatalf("pass, second read: want a cache hit: %s", show(rec))
	}

	// 2. The verdict leaves the pass: refused on the next read, although the
	// cache still holds the forecasts.
	recordVerdict(t, "NO SKILL DEMONSTRATED")
	resetVolRecordCache(t, d)
	rec := readLatest(d)
	if b := rec.Body.String(); rec.Code != 200 || !strings.Contains(b, `"available":false`) ||
		!strings.Contains(b, `"verdict":"NO SKILL DEMONSTRATED"`) || strings.Contains(b, "AAA") ||
		strings.Contains(b, `"forecasts":`) || rec.Header().Get("X-Cache") != "" {
		t.Fatalf("verdict left the pass: %s", show(rec))
	}

	// 3. A warming record answers warming, uncached: no refusal outlives it.
	prev := volRecordSource
	volRecordSource = func(Deps) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { writeWarming(w) }
	}
	t.Cleanup(func() { volRecordSource = prev })
	resetVolRecordCache(t)
	_, st2, d2 := newTestServer(t, nil)
	seedLatestForecast(t, st2)
	if rec := readLatest(d2); rec.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rec.Body.String(), "warming") || strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("warming record: %s", show(rec))
	}
	recordVerdict(t, volPassVerdict)
	resetVolRecordCache(t, d2)
	if rec := readLatest(d2); rec.Code != 200 || !strings.Contains(rec.Body.String(), "AAA") {
		t.Fatalf("pass after warming: a refusal was cached: %s", show(rec))
	}
}
