package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/xsfactor"
)

// xsFactorBars is the trailing depth every seeded symbol gets — past
// momentum-12-1's 253-close requirement so every leg is computable.
const xsFactorBars = 260

// seedXSFactorSymbol writes one symbol plus xsFactorBars daily bars. wobble sets
// realized volatility (alternating ±wobble around a flat 100) and dollarVol sets
// the median dollar volume, so a caller can dial each factor leg independently.
func seedXSFactorSymbol(t *testing.T, st *store.Store, sym string, wobble, dollarVol float64) md.Symbol {
	t.Helper()
	ctx := context.Background()
	s, err := st.UpsertSymbol(ctx, sym, md.Stocks, sym+" Inc")
	if err != nil {
		t.Fatalf("seed %s: %v", sym, err)
	}
	bars := make([]md.Bar, xsFactorBars)
	for i := range bars {
		c := 100.0
		if i%2 == 1 {
			c -= wobble
		} else {
			c += wobble
		}
		bars[i] = md.Bar{
			SymbolID: s.ID, TF: md.TF1d, Ts: int64(i+1) * 86400,
			Open: c, High: c, Low: c, Close: c, Volume: dollarVol / c,
		}
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed bars %s: %v", sym, err)
	}
	return s
}

// seedXSFactorSplit writes a symbol whose trailing series carries an
// uncorrected 4:1 split artifact (-75% in one day) — the guard must refuse it.
func seedXSFactorSplit(t *testing.T, st *store.Store, sym string) {
	t.Helper()
	seedXSFactorSymbol(t, st, sym, 0.5, 1e6)
	ctx := context.Background()
	s, err := st.GetSymbol(ctx, sym, md.Stocks)
	if err != nil {
		t.Fatalf("get %s: %v", sym, err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: s.ID, TF: md.TF1d, Ts: 101 * 86400,
		Open: 25, High: 25, Low: 25, Close: 25, Volume: 40000,
	}}); err != nil {
		t.Fatalf("seed split bar: %v", err)
	}
}

// xsFactorPayload is the response shape the web app consumes.
type xsFactorPayload struct {
	Horizon           string             `json:"horizon"`
	HorizonsAvailable []string           `json:"horizonsAvailable"`
	Market            string             `json:"market"`
	AsOfTs            int64              `json:"asOfTs"`
	UniverseN         int                `json:"universeN"`
	SymbolsConsidered int                `json:"symbolsConsidered"`
	CompositeLegs     []string           `json:"compositeLegs"`
	Edge              []xsfactor.LegEdge `json:"edge"`
	EdgeNote          string             `json:"edgeNote"`
	MethodNote        string             `json:"methodNote"`
	UniverseNote      string             `json:"universeNote"`
	SplitRejected     int                `json:"splitRejected"`
	SplitNote         string             `json:"splitNote"`
	SkippedTotal      int                `json:"skippedTotal"`
	Skipped           []xsfactor.Skipped `json:"skipped"`
	Caveat            string             `json:"caveat"`
	Gated             bool               `json:"gated"`
	GateReason        string             `json:"gateReason"`
	Rows              []xsfactor.Row     `json:"rows"`
	N                 int                `json:"n"`
	Total             int                `json:"total"`
}

// callXSFactor drives the handler DIRECTLY (bypassing the package-level SWR
// cache) so each assertion sees a freshly built payload — the cache itself is
// covered by TestXSFactorRouteCacheIsolation.
func callXSFactor(t *testing.T, d Deps, query string) xsFactorPayload {
	t.Helper()
	rec := httptest.NewRecorder()
	d.xsFactor(rec, httptest.NewRequest(http.MethodGet, "/api/xs-factor"+query, nil))
	if rec.Code != 200 {
		t.Fatalf("GET /api/xs-factor%s → %d: %s", query, rec.Code, rec.Body.String())
	}
	var out xsFactorPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return out
}

// TestXSFactorRanking seeds a monotonic 24-name cross-section — symbol k is
// both the k-th calmest and the k-th thinnest — so the correct composite order
// is exactly S00..S23, and asserts the honest payload contract around it.
func TestXSFactorRanking(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	const n = 24
	for k := 0; k < n; k++ {
		seedXSFactorSymbol(t, st, fmt.Sprintf("S%02d", k), 0.05*float64(k+1), 1e5*float64(k+1))
	}
	seedXSFactorSplit(t, st, "SPLITX")

	got := callXSFactor(t, d, "")

	// Defaults: 21d horizon, stocks cross-section, ungated at 24 names.
	if got.Horizon != string(xsfactor.H21d) {
		t.Fatalf("horizon=%q, want 21d by default", got.Horizon)
	}
	if got.Market != string(md.Stocks) {
		t.Fatalf("market=%q, want stocks by default", got.Market)
	}
	if got.Gated {
		t.Fatalf("gated with a %d-name cross-section: %s", got.UniverseN, got.GateReason)
	}
	if got.UniverseN != n || got.Total != n {
		t.Fatalf("universeN=%d total=%d, want %d (the split name must be excluded)", got.UniverseN, got.Total, n)
	}
	if got.SymbolsConsidered != n+1 {
		t.Fatalf("symbolsConsidered=%d, want %d", got.SymbolsConsidered, n+1)
	}
	if got.AsOfTs != int64(xsFactorBars)*86400 {
		t.Fatalf("asOfTs=%d, want the newest bar ts %d", got.AsOfTs, int64(xsFactorBars)*86400)
	}

	// Monotonic ranking: calmest + thinnest first, ranks dense from 1.
	for i, row := range got.Rows {
		want := fmt.Sprintf("S%02d", i)
		if row.Symbol != want {
			t.Fatalf("rank %d = %q, want %q (composite %.4f)", i+1, row.Symbol, want, row.Composite)
		}
		if row.Rank != i+1 {
			t.Fatalf("%s rank field=%d, want %d", row.Symbol, row.Rank, i+1)
		}
		if i > 0 && row.Composite > got.Rows[i-1].Composite {
			t.Fatalf("composite not descending at %s: %.4f > %.4f", row.Symbol, row.Composite, got.Rows[i-1].Composite)
		}
		if row.Composite < 0 || row.Composite > 1 {
			t.Fatalf("%s composite=%.4f out of [0,1]", row.Symbol, row.Composite)
		}
		if row.Vol21dAnn == nil || row.LowVolPct == nil || row.LiquidityPct == nil {
			t.Fatalf("%s missing a leg it should have: %+v", row.Symbol, row)
		}
	}
	if *got.Rows[0].LowVolPct != 1 || *got.Rows[0].LiquidityPct != 1 {
		t.Fatalf("rank 1 should be the calmest AND thinnest: lowVol=%v liq=%v",
			*got.Rows[0].LowVolPct, *got.Rows[0].LiquidityPct)
	}

	// Split guard: named, counted, and absent from the ranking.
	if got.SplitRejected != 1 {
		t.Fatalf("splitRejected=%d, want 1", got.SplitRejected)
	}
	var found bool
	for _, s := range got.Skipped {
		if s.Symbol == "SPLITX" {
			found = true
			if !containsStr(s.Reason, "split/data artifact") || !containsStr(s.Reason, "0.65") {
				t.Fatalf("SPLITX reason=%q, want the stated split-guard reason", s.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("SPLITX not named in skipped: %+v", got.Skipped)
	}
	for _, row := range got.Rows {
		if row.Symbol == "SPLITX" {
			t.Fatal("SPLITX was ranked despite the split artifact")
		}
	}

	// Honesty block ships VERBATIM.
	if got.Caveat != xsfactor.Caveat {
		t.Fatalf("caveat not verbatim:\n got %q\nwant %q", got.Caveat, xsfactor.Caveat)
	}
	if got.EdgeNote != xsfactor.EvidenceNote || got.MethodNote != xsfactor.MethodNote {
		t.Fatal("edgeNote/methodNote not shipped verbatim")
	}
	if got.SplitNote == "" || got.UniverseNote == "" {
		t.Fatal("splitNote/universeNote missing")
	}

	// Measured edge at 21d: liquidity + low-vol only, exactly as measured.
	if len(got.Edge) != 2 {
		t.Fatalf("edge legs=%d, want 2 at 21d: %+v", len(got.Edge), got.Edge)
	}
	wantEdge := map[string][3]float64{
		xsfactor.LegLiquidity: {2.50, 1.61, 3.37},
		xsfactor.LegLowVol:    {2.80, 0.81, 4.66},
	}
	for _, e := range got.Edge {
		w, ok := wantEdge[e.Leg]
		if !ok {
			t.Fatalf("unmeasured leg %q in the 21d edge block", e.Leg)
		}
		if e.EdgePP != w[0] || e.CILow != w[1] || e.CIHigh != w[2] {
			t.Fatalf("21d/%s = %+v, want %v", e.Leg, e, w)
		}
	}
	if len(got.CompositeLegs) != 2 {
		t.Fatalf("compositeLegs=%v, want liquidity+lowVol at 21d", got.CompositeLegs)
	}
	for _, row := range got.Rows {
		for _, leg := range row.LegsUsed {
			if leg == xsfactor.LegMom121 {
				t.Fatalf("%s weighted momentum at 21d, where it has no measured edge", row.Symbol)
			}
		}
		if row.Mom121Pct == nil {
			t.Fatalf("%s: momentum should still ship as a diagnostic percentile", row.Symbol)
		}
	}
	if len(got.HorizonsAvailable) != 3 {
		t.Fatalf("horizonsAvailable=%v, want the 3 measured horizons", got.HorizonsAvailable)
	}
}

// TestXSFactorHorizonsAndLimit covers horizon selection (including that an
// unmeasured horizon falls back rather than inventing an edge block) and that
// limit truncates the view without changing anyone's rank.
func TestXSFactorHorizonsAndLimit(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	const n = 22
	for k := 0; k < n; k++ {
		seedXSFactorSymbol(t, st, fmt.Sprintf("T%02d", k), 0.05*float64(k+1), 1e5*float64(k+1))
	}

	cases := []struct {
		query    string
		horizon  string
		legs     int
		wantMom  bool
		edgeLegs int
	}{
		{"?horizon=5d", "5d", 3, true, 3},
		{"?horizon=21d", "21d", 2, false, 2},
		{"?horizon=63d", "63d", 2, false, 2},
		{"?horizon=1d", "21d", 2, false, 2}, // unmeasured → default, never invented
		{"?horizon=", "21d", 2, false, 2},   // empty → default
		{"?horizon=252d", "21d", 2, false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got := callXSFactor(t, d, tc.query)
			if got.Horizon != tc.horizon {
				t.Fatalf("horizon=%q, want %q", got.Horizon, tc.horizon)
			}
			if len(got.CompositeLegs) != tc.legs || len(got.Edge) != tc.edgeLegs {
				t.Fatalf("compositeLegs=%v edge=%d, want %d legs", got.CompositeLegs, len(got.Edge), tc.legs)
			}
			var weighted bool
			for _, leg := range got.Rows[0].LegsUsed {
				if leg == xsfactor.LegMom121 {
					weighted = true
				}
			}
			if weighted != tc.wantMom {
				t.Fatalf("momentum weighted=%v, want %v (legsUsed=%v)", weighted, tc.wantMom, got.Rows[0].LegsUsed)
			}
			// The calmest+thinnest name leads at every horizon.
			if got.Rows[0].Symbol != "T00" {
				t.Fatalf("rank 1 = %q, want T00", got.Rows[0].Symbol)
			}
		})
	}

	full := callXSFactor(t, d, "?limit=500")
	small := callXSFactor(t, d, "?limit=5")
	if len(full.Rows) != n || len(small.Rows) != 5 {
		t.Fatalf("rows full=%d small=%d, want %d and 5", len(full.Rows), len(small.Rows), n)
	}
	if small.Total != n || small.UniverseN != n {
		t.Fatalf("limited read changed the cross-section: total=%d universeN=%d, want %d", small.Total, small.UniverseN, n)
	}
	for i, row := range small.Rows {
		if row.Symbol != full.Rows[i].Symbol || row.Rank != full.Rows[i].Rank ||
			row.Composite != full.Rows[i].Composite {
			t.Fatalf("limit changed rank %d: %+v vs %+v", i+1, row, full.Rows[i])
		}
	}
	// An out-of-range limit falls back to the default rather than erroring.
	if got := callXSFactor(t, d, "?limit=99999"); len(got.Rows) != min(xsFactorDefaultLimit, n) {
		t.Fatalf("limit=99999 → %d rows, want the default bound", len(got.Rows))
	}
}

// TestXSFactorThinUniverseGate: below the cross-section floor a percentile is
// noise, so the endpoint must show its gate and a stated reason — never a rank.
func TestXSFactorThinUniverseGate(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	for k := 0; k < 3; k++ {
		seedXSFactorSymbol(t, st, fmt.Sprintf("U%02d", k), 0.05*float64(k+1), 1e5*float64(k+1))
	}
	got := callXSFactor(t, d, "")
	if !got.Gated {
		t.Fatalf("a 3-name cross-section must be gated, got %d rows", len(got.Rows))
	}
	if len(got.Rows) != 0 || got.N != 0 {
		t.Fatalf("gated payload still shipped %d rows", len(got.Rows))
	}
	if got.GateReason == "" || got.Caveat != xsfactor.Caveat {
		t.Fatalf("gated payload must still state its reason and caveat: %+v", got)
	}
	if got.UniverseN != 3 {
		t.Fatalf("universeN=%d, want 3", got.UniverseN)
	}

	// An empty store is gated too, not a 500 and not an empty-but-ungated rank.
	_, _, empty := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	if e := callXSFactor(t, empty, ""); !e.Gated || e.UniverseN != 0 {
		t.Fatalf("empty store: gated=%v universeN=%d, want true/0", e.Gated, e.UniverseN)
	}
}

// TestXSFactorRouteCacheIsolation exercises the REGISTERED route: the shared SWR
// body cache must serve a hit on the second request, and — because the key is
// scoped to the store instance — must never leak one isolated store's body into
// another's response.
func TestXSFactorRouteCacheIsolation(t *testing.T) {
	newServer := func(t *testing.T, symbols int, prefix string) *httptest.Server {
		t.Helper()
		srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
		for k := 0; k < symbols; k++ {
			seedXSFactorSymbol(t, st, fmt.Sprintf("%s%02d", prefix, k), 0.05*float64(k+1), 1e5*float64(k+1))
		}
		mux := http.NewServeMux()
		d.registerXSFactor(mux)
		srv.Config.Handler = d.secure(mux)
		return srv
	}
	get := func(t *testing.T, srv *httptest.Server) (xsFactorPayload, string) {
		t.Helper()
		res, err := newClient(t).Get(srv.URL + "/api/xs-factor")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer res.Body.Close() //nolint:errcheck
		if res.StatusCode != 200 {
			t.Fatalf("status=%d", res.StatusCode)
		}
		var out xsFactorPayload
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out, res.Header.Get("X-Cache")
	}

	big := newServer(t, 21, "A")
	small := newServer(t, 4, "B")

	first, cache1 := get(t, big)
	if first.UniverseN != 21 || first.Gated {
		t.Fatalf("big store: universeN=%d gated=%v, want 21/false", first.UniverseN, first.Gated)
	}
	if cache1 == "hit" {
		t.Fatal("first request should be a cold build, not a hit")
	}
	second, cache2 := get(t, big)
	if cache2 != "hit" {
		t.Fatalf("X-Cache=%q on the second request, want hit (the SWR cache is not wired)", cache2)
	}
	if second.UniverseN != first.UniverseN {
		t.Fatalf("cached body changed: %d vs %d", second.UniverseN, first.UniverseN)
	}

	// The other store must get its OWN answer, not the big store's cached body.
	other, _ := get(t, small)
	if other.UniverseN != 4 || !other.Gated {
		t.Fatalf("isolated store leaked: universeN=%d gated=%v, want 4/true", other.UniverseN, other.Gated)
	}
}

func containsStr(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
