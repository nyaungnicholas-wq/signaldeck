package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newShortsServer wires only the Stage-5 Reg SHO route behind the real
// middleware (separate harness so parallel edits never collide).
func newShortsServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerShorts(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func shortsGet(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return res.StatusCode
}

// TestShortsEndpoint covers both shapes — per-symbol series (with the
// descriptive z) and the fleet-wide extremes (with the stated volume floor) —
// and, non-negotiably, that the NOT-short-interest caveat ships VERBATIM.
func TestShortsEndpoint(t *testing.T) {
	srv, st := newShortsServer(t)
	ctx := context.Background()

	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("seed AAPL: %v", err)
	}
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "SPDR")
	if err != nil {
		t.Fatalf("seed SPY: %v", err)
	}

	// 12 days for AAPL: 11 flat-ish prior ratios + a spike on the last day so
	// the z is computable (needs 10+ prior) and clearly positive.
	var rows []store.ShortVolumeRow
	for i := 0; i < 11; i++ {
		day := "2026-06-" + string([]byte{byte('0' + (i+10)/10), byte('0' + (i+10)%10)}) // 2026-06-10 … 2026-06-20
		ratio := 0.30
		if i%2 == 1 {
			ratio = 0.34
		}
		rows = append(rows, store.ShortVolumeRow{
			SymbolID: aapl.ID, Day: day,
			ShortVol: ratio * 1e6, ShortExempt: 0, TotalVol: 1e6, ShortPct: ratio,
		})
	}
	rows = append(rows, store.ShortVolumeRow{
		SymbolID: aapl.ID, Day: "2026-06-22",
		ShortVol: 8e5, ShortExempt: 0, TotalVol: 1e6, ShortPct: 0.8,
	})
	// SPY on the latest day: over the floor, lower ratio than AAPL.
	rows = append(rows,
		store.ShortVolumeRow{SymbolID: spy.ID, Day: "2026-06-22",
			ShortVol: 2e5, ShortExempt: 0, TotalVol: 1e6, ShortPct: 0.2},
		// Under-the-floor row that must NOT appear in extremes.
		store.ShortVolumeRow{SymbolID: spy.ID, Day: "2026-06-21",
			ShortVol: 9, ShortExempt: 0, TotalVol: 10, ShortPct: 0.9},
	)
	if err := st.UpsertShortVolume(ctx, rows); err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	// ── per-symbol series ────────────────────────────────────────────
	var symResp struct {
		Caveat  string                 `json:"caveat"`
		Symbol  string                 `json:"symbol"`
		Series  []store.ShortVolumeRow `json:"series"`
		LatestZ *float64               `json:"latestZ"`
		ZNote   string                 `json:"zNote"`
	}
	if code := shortsGet(t, srv.URL+"/api/shorts?symbol=aapl&days=30", &symResp); code != 200 {
		t.Fatalf("symbol GET = %d", code)
	}
	if symResp.Caveat != shortsCaveat {
		t.Fatalf("caveat = %q — must ship verbatim", symResp.Caveat)
	}
	if symResp.Symbol != "AAPL" || len(symResp.Series) != 12 {
		t.Fatalf("series: sym=%q n=%d, want AAPL/12", symResp.Symbol, len(symResp.Series))
	}
	if symResp.Series[0].Day != "2026-06-10" || symResp.Series[11].Day != "2026-06-22" {
		t.Fatalf("series not ASC: %s … %s", symResp.Series[0].Day, symResp.Series[11].Day)
	}
	if symResp.LatestZ == nil || *symResp.LatestZ < 3 {
		t.Fatalf("latestZ = %v, want a clearly positive z for the spike", symResp.LatestZ)
	}
	if symResp.ZNote == "" {
		t.Fatal("zNote missing — the descriptive label is mandatory")
	}

	// ── fleet-wide extremes ──────────────────────────────────────────
	var extResp struct {
		Caveat      string  `json:"caveat"`
		Day         string  `json:"day"`
		MinTotalVol float64 `json:"minTotalVol"`
		FloorNote   string  `json:"floorNote"`
		Extremes    []struct {
			Symbol   string    `json:"symbol"`
			ShortPct float64   `json:"shortPct"`
			Spark    []float64 `json:"spark"`
		} `json:"extremes"`
	}
	if code := shortsGet(t, srv.URL+"/api/shorts", &extResp); code != 200 {
		t.Fatalf("extremes GET = %d", code)
	}
	if extResp.Caveat != shortsCaveat || extResp.FloorNote == "" || extResp.MinTotalVol != shortsMinTotalVol {
		t.Fatalf("extremes honesty fields wrong: %+v", extResp)
	}
	if extResp.Day != "2026-06-22" {
		t.Fatalf("day = %q, want 2026-06-22", extResp.Day)
	}
	if len(extResp.Extremes) != 2 || extResp.Extremes[0].Symbol != "AAPL" || extResp.Extremes[1].Symbol != "SPY" {
		t.Fatalf("extremes order = %+v, want AAPL(0.8) then SPY(0.2)", extResp.Extremes)
	}
	if len(extResp.Extremes[0].Spark) != 12 || extResp.Extremes[0].Spark[11] != 0.8 {
		t.Fatalf("AAPL spark = %v", extResp.Extremes[0].Spark)
	}

	// ── unknown symbol: honest 404, universe-scoped ──────────────────
	var dummy map[string]any
	if code := shortsGet(t, srv.URL+"/api/shorts?symbol=NOPE", &dummy); code != 404 {
		t.Fatalf("unknown symbol = %d, want 404", code)
	}
}

// TestShortsEndpoint_EmptyStore: before the worker's first ingest the
// extremes payload states honest absence (empty list + emptyNote), never an
// error or a fabricated day.
func TestShortsEndpoint_EmptyStore(t *testing.T) {
	srv, _ := newShortsServer(t)
	var resp struct {
		Day       string `json:"day"`
		Extremes  []any  `json:"extremes"`
		EmptyNote string `json:"emptyNote"`
		Caveat    string `json:"caveat"`
	}
	if code := shortsGet(t, srv.URL+"/api/shorts", &resp); code != 200 {
		t.Fatalf("GET = %d", code)
	}
	if resp.Day != "" || len(resp.Extremes) != 0 || resp.EmptyNote == "" || resp.Caveat != shortsCaveat {
		t.Fatalf("empty-store payload = %+v", resp)
	}
}

// TestShortsZ: the pure z helper — gate below 10 prior points, no z on a
// flat baseline (sd~0), correct sign/magnitude otherwise.
func TestShortsZ(t *testing.T) {
	if _, ok := shortsZ([]float64{0.3, 0.4, 0.5}); ok {
		t.Fatal("z computed on < 10 prior points")
	}
	flat := make([]float64, 12)
	for i := range flat {
		flat[i] = 0.3
	}
	if _, ok := shortsZ(flat); ok {
		t.Fatal("z computed on a flat baseline (sd=0)")
	}
	// 10 prior alternating 0.3/0.5 (mean 0.4, sd 0.1), latest 0.6 → z = 2.
	series := []float64{0.3, 0.5, 0.3, 0.5, 0.3, 0.5, 0.3, 0.5, 0.3, 0.5, 0.6}
	z, ok := shortsZ(series)
	if !ok || math.Abs(z-2.0) > 1e-9 {
		t.Fatalf("z = %v ok=%v, want 2.0", z, ok)
	}
}
