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

// newUniverseServer wires only the /api/universe route behind the real
// middleware (separate from the shared harness so parallel edits never
// collide).
func newUniverseServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/universe", d.universe)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func TestUniverseEndpoint(t *testing.T) {
	t.Setenv("SIGNALDECK_STREAM_CAP", "25")
	t.Setenv("SIGNALDECK_UNIVERSE_CAP", "500")
	srv, st := newUniverseServer(t, nil)
	ctx := context.Background()

	// 1 streamed hot symbol + 2 daily-only universe symbols.
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolStream(ctx, spy.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertDailyUniverseSymbol(ctx, "MSFT", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertDailyUniverseSymbol(ctx, "GOOG", ""); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/universe")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (public read default)", res.StatusCode)
	}
	var body struct {
		Streamed     int `json:"streamed"`
		StreamCap    int `json:"streamCap"`
		DailyOnly    int `json:"dailyOnly"`
		UniverseCap  int `json:"universeCap"`
		UniverseSeed int `json:"universeSeed"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Streamed != 1 {
		t.Errorf("streamed = %d, want 1", body.Streamed)
	}
	if body.DailyOnly != 2 {
		t.Errorf("dailyOnly = %d, want 2", body.DailyOnly)
	}
	if body.StreamCap != 25 {
		t.Errorf("streamCap = %d, want 25", body.StreamCap)
	}
	if body.UniverseCap != 500 {
		t.Errorf("universeCap = %d, want 500", body.UniverseCap)
	}
	if body.UniverseSeed < 400 {
		t.Errorf("universeSeed = %d, want >= 400 (curated list)", body.UniverseSeed)
	}
}
