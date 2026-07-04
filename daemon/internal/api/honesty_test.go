package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestDedupeIndependent_CollapsesToSymbolDay verifies the core independent-N
// math: many minute-cadence rows for one symbol on one UTC day collapse to a
// SINGLE observation (the newest that day), and distinct symbol-days survive.
func TestDedupeIndependent_CollapsesToSymbolDay(t *testing.T) {
	const day = int64(86400)
	base := int64(1_700_000_000)
	base -= base % day // align to a UTC-day boundary
	newest := base + 8*3600

	// Symbol 1: 5 rows on day D (varied ts within the day) + 2 rows on day D+1.
	// Symbol 2: 3 rows on day D. Newest-first input (ts DESC), as the store gives.
	in := []honestyPt{
		// symbol 1, day D+1 (newest overall)
		{Score: 0.9, Fwd: 0.02, Ts: base + day + 9*3600, SymbolID: 1},
		{Score: 0.8, Fwd: 0.02, Ts: base + day + 1*3600, SymbolID: 1},
		// symbol 1, day D
		{Score: 0.5, Fwd: 0.01, Ts: newest, SymbolID: 1}, // latest on day D → the keeper
		{Score: 0.4, Fwd: 0.01, Ts: base + 6*3600, SymbolID: 1},
		{Score: 0.3, Fwd: 0.01, Ts: base + 4*3600, SymbolID: 1},
		{Score: 0.2, Fwd: 0.01, Ts: base + 2*3600, SymbolID: 1},
		{Score: 0.1, Fwd: 0.01, Ts: base + 1*3600, SymbolID: 1},
		// symbol 2, day D
		{Score: -0.5, Fwd: -0.01, Ts: base + 7*3600, SymbolID: 2}, // latest for sym2 day D
		{Score: -0.6, Fwd: -0.01, Ts: base + 3*3600, SymbolID: 2},
		{Score: -0.7, Fwd: -0.01, Ts: base + 1*3600, SymbolID: 2},
	}
	out := dedupeIndependent(in)

	// 3 independent symbol-days: (sym1,D+1), (sym1,D), (sym2,D).
	if len(out) != 3 {
		t.Fatalf("expected 3 independent observations, got %d", len(out))
	}
	// The (sym1, day D) keeper must be the LATEST that day (score 0.5 @ newest).
	var found bool
	for _, p := range out {
		if p.SymbolID == 1 && p.Ts/day == base/day {
			found = true
			if p.Score != 0.5 || p.Ts != newest {
				t.Fatalf("sym1 day-D keeper should be the latest (score 0.5 @ %d), got score %.2f @ %d",
					newest, p.Score, p.Ts)
			}
		}
	}
	if !found {
		t.Fatal("sym1 day-D observation missing from deduped set")
	}
	if got := dedupeIndependent(nil); got != nil {
		t.Fatalf("nil input should return nil, got %v", got)
	}
}

// TestHonestyGate_BelowMinIndependentN: when the independent count is under
// minIndependentN, the handler must WITHHOLD the IC (ic=null) and return the
// "insufficient independent resolutions (k/min)" note — and report both the raw
// row count and the (much smaller) independent count.
func TestHonestyGate_BelowMinIndependentN(t *testing.T) {
	srv, st := newHonestyServer(t)
	ctx := context.Background()

	// One symbol, MANY minute rows, but all on only a few UTC-days → far below
	// the independence gate even though rawN is large.
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 3 distinct days, 40 rows each = 120 raw rows, 3 independent obs.
	seedOutcomes(t, st, sym.ID, md.H1d, 3, 40)

	body := getJSON(t, srv, "/api/honesty?horizon=1d")

	if got := jnum(body, "rawN"); got < 100 {
		t.Fatalf("rawN should reflect the ~120 raw rows, got %.0f", got)
	}
	if got := jnum(body, "independentN"); got != 3 {
		t.Fatalf("independentN should be 3 distinct symbol-days, got %.0f", got)
	}
	if body["ic"] != nil {
		t.Fatalf("IC must be withheld below the gate, got %v", body["ic"])
	}
	if body["icGated"] != true {
		t.Fatalf("icGated should be true below the gate, got %v", body["icGated"])
	}
	if note, _ := body["icNote"].(string); note == "" {
		t.Fatalf("expected an icNote explaining insufficient independent resolutions, got %q", note)
	}
	// Labeling: must be flagged NOT live.
	if body["live"] != false {
		t.Fatalf("honesty payload must be flagged live=false, got %v", body["live"])
	}
}

// TestHonestyGate_AboveMinIndependentN: with enough distinct symbol-days the
// gate opens and a numeric IC is returned.
func TestHonestyGate_AboveMinIndependentN(t *testing.T) {
	srv, st := newHonestyServer(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 40 distinct days (> minIndependentN=30), 3 rows each. 120 raw, 40 indep.
	seedOutcomes(t, st, sym.ID, md.H1d, 40, 3)

	body := getJSON(t, srv, "/api/honesty?horizon=1d")

	if got := jnum(body, "independentN"); got != 40 {
		t.Fatalf("independentN should be 40 distinct days, got %.0f", got)
	}
	if body["icGated"] != false {
		t.Fatalf("icGated should be false above the gate, got %v", body["icGated"])
	}
	if body["ic"] == nil {
		t.Fatal("IC must be a number above the gate, got null")
	}
	if _, ok := body["ic"].(float64); !ok {
		t.Fatalf("IC should be a float64 above the gate, got %T", body["ic"])
	}
}

// seedOutcomes inserts `days` distinct UTC-days of resolved score_outcomes for
// one symbol, `perDay` rows each (varied intraday ts + score), so the raw row
// count is days*perDay but the independent count is `days`.
func seedOutcomes(t *testing.T, st *store.Store, symbolID int64, h md.Horizon, days, perDay int) {
	t.Helper()
	ctx := context.Background()
	const day = int64(86400)
	base := int64(1_600_000_000)
	base -= base % day
	for di := 0; di < days; di++ {
		dayStart := base + int64(di)*day
		for ri := 0; ri < perDay; ri++ {
			ts := dayStart + int64(ri*600) // 10-min spacing within the day
			score := -0.9 + 1.8*float64((di*perDay+ri)%10)/9.0
			sc := md.Score{SymbolID: symbolID, Horizon: h, Ts: ts, Score: score}
			if err := st.InsertScore(ctx, sc); err != nil {
				t.Fatalf("InsertScore: %v", err)
			}
			fwd := score * 0.01 // some monotone-ish signal
			if err := st.ResolveOutcome(ctx, symbolID, h, ts, fwd); err != nil {
				t.Fatalf("ResolveOutcome: %v", err)
			}
		}
	}
}

// newHonestyServer stands up a temp store behind the real secure() middleware
// with ONLY the honesty route wired (self-contained; does not touch the shared
// newTestServer mux). Returns the started server and its store.
func newHonestyServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "honesty.db"))
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
	mux.HandleFunc("GET /api/honesty", d.honesty)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

// getJSON GETs a path with the allowed Origin the middleware requires and
// decodes the JSON body into a map.
func getJSON(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "http://app.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d body %s", path, resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %s: %v (body %s)", path, err, raw)
	}
	return body
}

func jnum(m map[string]any, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}
