package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestVolForecastLatestServesOnlyTodaysDerivedForecasts: the member route
// returns the newest call bar per horizon only, stocks only, and exactly the
// derived fields (no null, coefficient or outcome).
func TestVolForecastLatestServesOnlyTodaysDerivedForecasts(t *testing.T) {
	ctx := context.Background()
	_, st, d := newTestServer(t, nil)
	sym := func(s string, m md.Market) int64 {
		v, err := st.UpsertSymbol(ctx, s, m, "")
		if err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	aaa, bbb, ccc := sym("AAA", md.Stocks), sym("BBB", md.Stocks), sym("CCC/USD", md.Crypto)
	old, cur := int64(1_790_000_000), int64(1_790_086_400)
	put := func(id, ts int64, h int, rv float64) {
		t.Helper()
		if err := st.UpsertRVForecast(ctx, store.RVForecast{SymbolID: id, Ts: ts, Horizon: h, RVHat: rv,
			NullRW: 0.1, NullEWMA: 0.1, NTrain: 600, Revision: "t"}, time.Unix(ts, 0)); err != nil {
			t.Fatal(err)
		}
	}
	put(aaa, old, 1, 0.0009)
	put(aaa, cur, 1, 0.0004) // sqrt(252*0.0004) = 0.31749 -> 31.7
	put(aaa, cur, 5, 0.0001) // sqrt(252*0.0001) = 0.15875 -> 15.9
	put(bbb, old, 1, 0.0009) // skipped on the current bar: no current forecast
	put(ccc, cur, 1, 0.0004) // crypto is not covered for members

	rec := httptest.NewRecorder()
	d.volForecastLatest(rec, httptest.NewRequest("GET", "/api/vol-forecast/latest", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Forecasts []map[string]any `json:"forecasts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	type row struct {
		sym  string
		h    float64
		asOf float64
		vol  float64
	}
	var got []row
	for _, f := range body.Forecasts {
		keys := make([]string, 0, len(f))
		for k := range f {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, []string{"asOf", "horizon", "symbol", "volPct"}) {
			t.Fatalf("fields %v: a member row carries only symbol, horizon, asOf, volPct", keys)
		}
		got = append(got, row{f["symbol"].(string), f["horizon"].(float64), f["asOf"].(float64), f["volPct"].(float64)})
	}
	want := []row{{"AAA", 1, float64(cur), 31.7}, {"AAA", 5, float64(cur), 15.9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
