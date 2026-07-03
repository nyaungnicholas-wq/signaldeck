package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newDatastatsServer wires only the datastats route behind the real
// middleware (kept separate from newTestServer so parallel edits to the
// shared test harness never collide with this wave).
func newDatastatsServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	_ = srv // route table below replaces the handler wholesale
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/datastats", d.datastats)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func TestDatastatsEndpoint(t *testing.T) {
	srv, st := newDatastatsServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1d, Ts: 1000, Close: 1}}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/datastats")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (public read default)", res.StatusCode)
	}
	var body store.DataStatsResult
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tables) == 0 || body.DBBytes <= 0 {
		t.Fatalf("thin datastats payload: %+v", body)
	}
	found := false
	for _, ts := range body.Tables {
		if ts.Table == "bars" && ts.Rows == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("bars row count not reported: %+v", body.Tables)
	}
}

// datastats is a read endpoint and must be gated exactly like other reads:
// anonymous access is rejected when PublicReads is off.
func TestDatastatsGatedWhenPublicReadsOff(t *testing.T) {
	srv, _ := newDatastatsServer(t, func(c *config.Config) { c.PublicReads = false })
	res, err := newClient(t).Get(srv.URL + "/api/datastats")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when public reads are off", res.StatusCode)
	}
}
