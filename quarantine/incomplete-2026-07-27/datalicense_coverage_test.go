package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// THE COVERAGE TABLE.
//
// Every route below serves stored records that originate from a source whose
// datalicense classification says Redistrib=false. The redistribution guard is
// a legal control, and a legal control that is present on some doors and absent
// from others is a documented policy rather than a mechanism — which is exactly
// what the 2026-07-27 review found: /api/bars and the three CSV exports were
// guarded while /api/tv-rating, /api/tv-quote and /api/stocktwits handed the
// same class of records to any non-loopback caller.
//
// This table is the artifact a reviewer reads. Adding a route that serves
// stored rows of a restricted source without adding a line here is the omission
// the file exists to make visible; the assertions below then make endpoint
// coverage a tested property instead of an assumed one.
var restrictedRoutes = []struct {
	route   string // for the failure message
	query   string
	sources []string // whose records the route hands over
	handler func(Deps) http.HandlerFunc
}{
	{"GET /api/bars", "?symbol=NVDA&market=stocks", datalicense.PriceBarSources,
		func(d Deps) http.HandlerFunc { return d.bars }},
	{"GET /api/export/bars.csv", "?symbol=NVDA&market=stocks", datalicense.PriceBarSources,
		func(d Deps) http.HandlerFunc { return d.exportBars }},
	{"GET /api/export/scores.csv", "?symbol=NVDA&market=stocks", datalicense.PriceBarSources,
		func(d Deps) http.HandlerFunc { return d.exportScores }},
	{"GET /api/export/outcomes.csv", "?symbol=NVDA&market=stocks", datalicense.PriceBarSources,
		func(d Deps) http.HandlerFunc { return d.exportOutcomes }},
	{"GET /api/tv-rating", "?symbol=NVDA&market=stocks", []string{"tvscanner"},
		func(d Deps) http.HandlerFunc { return d.tvRating }},
	{"GET /api/tv-quote", "?symbol=NVDA&market=stocks", []string{"tvscanner"},
		func(d Deps) http.HandlerFunc { return d.tvQuote }},
	{"GET /api/stocktwits", "?symbol=NVDA&market=stocks", []string{"stocktwits"},
		func(d Deps) http.HandlerFunc { return d.stocktwitsSentiment }},
	{"GET /api/news", "?symbol=NVDA&market=stocks", []string{"news"},
		func(d Deps) http.HandlerFunc { return d.news }},
}

// licenseCoverageDeps gives the handlers a real store with one tracked symbol,
// so a route that fails to refuse fails on the guard rather than on lookup.
func licenseCoverageDeps(t *testing.T) Deps {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "license_cov.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.UpsertSymbol(context.Background(), "NVDA", md.Stocks, "NVIDIA"); err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	return Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
}

func TestEveryRestrictedRouteRefusesNonLoopbackCallers(t *testing.T) {
	d := licenseCoverageDeps(t)
	for _, tc := range restrictedRoutes {
		if datalicense.Redistributable(tc.sources...) {
			t.Fatalf("%s: table says %v are redistributable; if a source's terms really "+
				"changed, change the classification deliberately — do not drop the route",
				tc.route, tc.sources)
		}
		w := httptest.NewRecorder()
		req := remoteReq("/x" + tc.query)
		req.Header.Set("X-Forwarded-For", "203.0.113.9") // a tunnel presents both
		tc.handler(d)(w, req)
		if w.Code != http.StatusUnavailableForLegalReasons {
			t.Errorf("%s served %d to a non-loopback caller; it hands over records from %v, "+
				"which may not be redistributed — it must refuse with 451",
				tc.route, w.Code, tc.sources)
			continue
		}
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: refusal body not JSON: %v", tc.route, err)
			continue
		}
		want := datalicense.RawDataNotice(tc.sources...)
		if body["error"] != want {
			t.Errorf("%s: refusal must carry the actionable notice naming the real sources.\n got: %s\nwant: %s",
				tc.route, body["error"], want)
		}
		// The notice must name the actual providers refused, not the price
		// feeds by default — a TradingView refusal citing Alpaca is a lie the
		// operator cannot act on.
		for _, s := range tc.sources {
			if p := datalicense.Sources[s].Provider; p != "" && !strings.Contains(body["error"], p) {
				t.Errorf("%s: refusal does not name %s", tc.route, p)
			}
		}
	}
}

func TestRestrictedRoutesStillServeLoopback(t *testing.T) {
	// The flip side: the guard is about redistribution, not about disabling the
	// author's own machine. A guard that refuses everyone would pass the test
	// above while breaking the product.
	d := licenseCoverageDeps(t)
	for _, tc := range restrictedRoutes {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x"+tc.query, nil)
		r.RemoteAddr = "127.0.0.1:41234"
		tc.handler(d)(w, r)
		if w.Code == http.StatusUnavailableForLegalReasons {
			t.Errorf("%s refused a loopback caller; local use is not redistribution", tc.route)
		}
	}
}
