package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newSourceHealthServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/source-health", d.sourceHealth)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func TestSourceHealthShape(t *testing.T) {
	srv, st := newSourceHealthServer(t, nil)
	ctx := context.Background()
	// A 3h-old crypto perp snapshot (24/7 source, budget 2h) ⇒ stale regardless
	// of the live clock, so the shape test always sees a nonzero staleCount.
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err := st.InsertCryptoPerp(ctx, store.CryptoPerpRow{SymbolID: btc.ID, Ts: time.Now().Unix() - 3*3600, MarkPx: 1}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/source-health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (public read default)", res.StatusCode)
	}
	var body struct {
		Sources []struct {
			Source          string `json:"source"`
			LastTs          int64  `json:"lastTs"`
			AgeSecs         int64  `json:"ageSecs"`
			StaleBudgetSecs int64  `json:"staleBudgetSecs"`
			Stale           bool   `json:"stale"`
			MarketGated     bool   `json:"marketGated"`
			Note            string `json:"note"`
		} `json:"sources"`
		Overall struct {
			StaleCount int `json:"staleCount"`
		} `json:"overall"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sources) == 0 {
		t.Fatal("no sources in payload")
	}
	var perp *int
	for i, s := range body.Sources {
		if s.Source == "crypto_perp" {
			perp = &i
		}
	}
	if perp == nil {
		t.Fatal("crypto_perp not reported")
	}
	if p := body.Sources[*perp]; !p.Stale || p.StaleBudgetSecs != int64((2*time.Hour)/time.Second) {
		t.Errorf("crypto_perp report wrong: %+v", p)
	}
	if body.Overall.StaleCount < 1 {
		t.Errorf("staleCount = %d, want >=1", body.Overall.StaleCount)
	}
}

func TestSourceHealthGatedWhenPublicReadsOff(t *testing.T) {
	srv, _ := newSourceHealthServer(t, func(c *config.Config) { c.PublicReads = false })
	res, err := newClient(t).Get(srv.URL + "/api/source-health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when public reads are off", res.StatusCode)
	}
}
