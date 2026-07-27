package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newPredAttrServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "predattr.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := Deps{
		St:      st,
		Cfg:     config.Config{WebOrigins: []string{"http://app.example"}, PublicReads: true},
		Version: "test",
		Started: time.Now(),
	}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerPredAttribution(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func TestPredAttribution_LatestAndBySeq(t *testing.T) {
	srv, st := newPredAttrServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	rows := []store.PredictionAttribution{
		{LedgerSeq: 5, Rank: 0, SymbolID: sym.ID, Horizon: md.H1d, Ts: 1234,
			Name: "comp_trend", Kind: "component", Contribution: 0.12, Method: store.AttributionMethodSaabas},
		{LedgerSeq: 5, Rank: 1, SymbolID: sym.ID, Horizon: md.H1d, Ts: 1234,
			Name: "sentiment", Kind: "leg", Contribution: -0.06, Method: store.AttributionMethodSaabas},
	}
	if err := st.InsertPredictionAttributions(ctx, rows); err != nil {
		t.Fatal(err)
	}

	var body struct {
		Available bool   `json:"available"`
		Seq       int64  `json:"seq"`
		Method    string `json:"method"`
		Note      string `json:"note"`
		Parts     []struct {
			Name         string  `json:"name"`
			Kind         string  `json:"kind"`
			Contribution float64 `json:"contribution"`
			Display      string  `json:"display"`
		} `json:"parts"`
	}
	get := func(url string, wantCode int) {
		t.Helper()
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != wantCode {
			t.Fatalf("GET %s = %d, want %d", url, resp.StatusCode, wantCode)
		}
		if wantCode == 200 {
			body = struct {
				Available bool   `json:"available"`
				Seq       int64  `json:"seq"`
				Method    string `json:"method"`
				Note      string `json:"note"`
				Parts     []struct {
					Name         string  `json:"name"`
					Kind         string  `json:"kind"`
					Contribution float64 `json:"contribution"`
					Display      string  `json:"display"`
				} `json:"parts"`
			}{}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Latest (no seq).
	get(srv.URL+"/api/attribution/prediction?symbol=AAA&market=stocks&horizon=1d", 200)
	if !body.Available || body.Seq != 5 || len(body.Parts) != 2 {
		t.Fatalf("latest payload wrong: %+v", body)
	}
	if body.Parts[0].Name != "comp_trend" || math.Abs(body.Parts[0].Contribution-0.12) > 1e-12 {
		t.Errorf("part 0 = %+v", body.Parts[0])
	}
	if body.Parts[0].Display != "+12.0% comp_trend" || body.Parts[1].Display != "-6.0% sentiment" {
		t.Errorf("displays = %q, %q", body.Parts[0].Display, body.Parts[1].Display)
	}
	if body.Method != store.AttributionMethodSaabas || body.Note == "" {
		t.Error("method/caveat note missing from payload")
	}

	// Explicit seq.
	get(srv.URL+"/api/attribution/prediction?symbol=AAA&market=stocks&horizon=1d&seq=5", 200)
	if body.Seq != 5 {
		t.Errorf("by-seq returned seq %d", body.Seq)
	}
	// Unknown seq -> 404; bad seq -> 400; unknown symbol -> 404.
	get(srv.URL+"/api/attribution/prediction?symbol=AAA&market=stocks&horizon=1d&seq=999", 404)
	get(srv.URL+"/api/attribution/prediction?symbol=AAA&market=stocks&seq=-1", 400)
	get(srv.URL+"/api/attribution/prediction?symbol=ZZZ&market=stocks", 404)

	// No attributions for a horizon -> available:false, 200.
	get(srv.URL+"/api/attribution/prediction?symbol=AAA&market=stocks&horizon=1w", 200)
	if body.Available {
		t.Error("1w should report available:false")
	}
}
