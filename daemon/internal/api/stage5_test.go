// Stage 5 (visual hubs) — tests for GET /api/predictions/latest. Contracts:
// ONE row per symbol (the newest prediction, not every historical row),
// inactive symbols excluded, strongest conviction first, and the honesty
// gate: with zero resolved outcomes the payload says gated=true and the
// caption carries the n=X/30 gate text — never a silent verdict.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newStage5Server(t *testing.T) (string, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) {})
	mux := http.NewServeMux()
	d.registerStage5(mux)
	srv.Config.Handler = d.secure(mux)
	return srv.URL, st
}

type stage5Resp struct {
	Horizon      string                 `json:"horizon"`
	Rows         []store.LatestPredRow  `json:"rows"`
	N            int                    `json:"n"`
	ResolvedN    int                    `json:"resolvedN"`
	MinResolvedN int                    `json:"minResolvedN"`
	Gated        bool                   `json:"gated"`
	Caption      string                 `json:"caption"`
	TrackLabel   string                 `json:"trackLabel"`
}

func TestPredictionsLatestEndpoint(t *testing.T) {
	ctx := context.Background()
	url, st := newStage5Server(t)

	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	btc, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatal(err)
	}
	// An unsubscribed (inactive) symbol with a prediction must be excluded.
	dead, err := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "Delisted Corp")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolActive(ctx, dead.ID, false); err != nil {
		t.Fatal(err)
	}

	// Two predictions for AAPL — only the NEWEST (ts=200) may appear.
	for _, p := range []store.Prediction{
		{SymbolID: aapl.ID, Horizon: md.H1d, Ts: 100, RawProb: 0.52, CalProb: 0.55, NUsed: 3},
		{SymbolID: aapl.ID, Horizon: md.H1d, Ts: 200, RawProb: 0.60, CalProb: 0.58, NUsed: 4},
		{SymbolID: btc.ID, Horizon: md.H1d, Ts: 150, RawProb: 0.80, CalProb: 0.71, NUsed: 4},
		{SymbolID: dead.ID, Horizon: md.H1d, Ts: 150, RawProb: 0.90, CalProb: 0.90, NUsed: 4},
		// A 1w row must NOT leak into the 1d payload.
		{SymbolID: aapl.ID, Horizon: md.H1w, Ts: 150, RawProb: 0.50, CalProb: 0.50, NUsed: 2},
	} {
		if err := st.UpsertPrediction(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	res, err := newClient(t).Get(url + "/api/predictions/latest?horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var got stage5Resp
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Horizon != "1d" || got.N != 2 || len(got.Rows) != 2 {
		t.Fatalf("want 2 1d rows, got %+v", got)
	}
	// Strongest conviction first: BTC |0.71−0.5|=0.21 > AAPL |0.58−0.5|=0.08.
	if got.Rows[0].Symbol != "BTC/USD" || got.Rows[1].Symbol != "AAPL" {
		t.Fatalf("conviction order wrong: %+v", got.Rows)
	}
	// Only the newest AAPL prediction survives the window.
	if got.Rows[1].Ts != 200 || got.Rows[1].CalProb != 0.58 || got.Rows[1].NUsed != 4 {
		t.Fatalf("stale AAPL row leaked: %+v", got.Rows[1])
	}
	// Honesty gate: nothing resolved yet → gated, caption carries n=0/min.
	if !got.Gated || got.ResolvedN != 0 || got.MinResolvedN <= 0 {
		t.Fatalf("gate not honest: %+v", got)
	}
	if got.Caption == "" || got.TrackLabel == "" {
		t.Fatalf("gate caption / track label missing: %+v", got)
	}
}

// An unknown horizon quietly falls back to 1d (same behavior as
// /api/calibration) instead of erroring the whole hub page.
func TestPredictionsLatestHorizonFallback(t *testing.T) {
	url, _ := newStage5Server(t)
	res, err := newClient(t).Get(url + "/api/predictions/latest?horizon=bogus")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var got stage5Resp
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Horizon != "1d" {
		t.Fatalf("horizon fallback wrong: %q", got.Horizon)
	}
	if got.Rows == nil || len(got.Rows) != 0 {
		t.Fatalf("empty store must yield empty (non-nil) rows, got %+v", got.Rows)
	}
}
