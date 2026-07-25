// Credibility-wave API tests: the track-record "regimes" section gate (per-kind
// "not yet significant — k/30" below 30 resolutions, live accuracy + Wilson CI
// at 30+), the earnings-window endpoint's honest nulls + window-flag math, and
// the regime-postmortems feed. t.TempDir store only; handlers called directly
// through recorders (no middleware under test here).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

func newRegimeTrackDeps(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "regimetrack.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}, st
}

// seedResolvedRegimeOutcomes freezes n outcomes for one kind on distinct UTC
// days and resolves them with the given number correct.
func seedResolvedRegimeOutcomes(t *testing.T, st *store.Store, symID int64,
	kind structregime.Kind, n, correct int, claimed float64,
) {
	t.Helper()
	ctx := context.Background()
	base := int64(1_700_000_000)
	for i := 0; i < n; i++ {
		ts := base + int64(i)*86400
		if _, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
			SymbolID: symID, Kind: kind, Ts: ts, HorizonDays: 21,
			Regime: "uptrend", Conviction: 0.9, HistoricalAccuracy: claimed, Rank: 0.9,
		}); err != nil {
			t.Fatalf("freeze %d: %v", i, err)
		}
	}
	due, err := st.DueRegimeOutcomes(ctx, 1<<60, 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	for i, row := range due {
		actual := "uptrend"
		if i >= correct {
			actual = "downtrend"
		}
		if err := st.ResolveRegimeOutcome(ctx, row.ID, actual, actual == row.Regime, base); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
}

// Below 30 resolutions a kind is gated with the honesty note; live accuracy is
// withheld while the CLAIMED mean still shows (what was promised, not earned).
func TestRegimeTrackRecordGatedBelow30(t *testing.T) {
	d, st := newRegimeTrackDeps(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seedResolvedRegimeOutcomes(t, st, sym.ID, structregime.KindTrend21, 5, 3, 0.972)

	sec := d.regimeTrackRecord(ctx)
	kinds := sec["kinds"].(map[string]any)
	e := kinds["trend21"].(map[string]any)
	if e["gated"] != true || e["liveAccuracy"] != nil {
		t.Fatalf("want gated with nil accuracy, got %+v", e)
	}
	if note := e["note"].(string); note != "not yet significant — 5/30 independent resolutions" {
		t.Fatalf("wrong gate note: %q", note)
	}
	if c := e["claimed"].(float64); c < 0.971 || c > 0.973 {
		t.Fatalf("claimed mean wrong: %v", c)
	}
}

// At 30+ resolutions the live accuracy + Wilson CI unlock, and the whole
// /api/track-record payload carries the regimes section.
func TestRegimeTrackRecordUngatedAt30(t *testing.T) {
	d, st := newRegimeTrackDeps(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	seedResolvedRegimeOutcomes(t, st, sym.ID, structregime.KindVol21, 30, 24, 0.720)

	sec := d.regimeTrackRecord(ctx)
	e := sec["kinds"].(map[string]any)["vol21"].(map[string]any)
	if e["gated"] != false {
		t.Fatalf("want ungated at 30, got %+v", e)
	}
	acc := e["liveAccuracy"].(float64)
	if acc < 0.799 || acc > 0.801 {
		t.Fatalf("live accuracy want 0.8, got %v", acc)
	}
	ci := e["liveAccuracyCI"].([2]float64)
	if !(ci[0] < acc && acc < ci[1]) {
		t.Fatalf("Wilson CI must bracket the point estimate: %v around %v", ci, acc)
	}

	// full handler carries the section
	rec := httptest.NewRecorder()
	d.trackRecord(rec, httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
	if rec.Code != 200 {
		t.Fatalf("track-record status %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	regimes, ok := resp["regimes"].(map[string]any)
	if !ok || regimes["available"] != true {
		t.Fatalf("track-record payload missing regimes section: %v", resp["regimes"])
	}
}

// The dedup guard: two resolved outcomes for the same (symbol, kind, UTC-day)
// can't exist by schema, but re-counting must stay one-per-day regardless.
func TestRegimeTrackRecordDedupPerSymbolKindDay(t *testing.T) {
	d, st := newRegimeTrackDeps(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")
	// same UTC day twice: second freeze must be ignored → n stays 1
	ts := int64(1_700_000_000)
	for i := 0; i < 2; i++ {
		_, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
			SymbolID: sym.ID, Kind: structregime.KindTrend21, Ts: ts + int64(i)*3600,
			HorizonDays: 21, Regime: "uptrend", Conviction: 0.9, HistoricalAccuracy: 0.972, Rank: 0.9,
		})
		if err != nil {
			t.Fatalf("freeze: %v", err)
		}
	}
	due, _ := st.DueRegimeOutcomes(ctx, 1<<60, 0)
	if len(due) != 1 {
		t.Fatalf("dedup index must keep 1 row per (symbol,kind,day), got %d", len(due))
	}
	_ = st.ResolveRegimeOutcome(ctx, due[0].ID, "downtrend", false, ts)
	e := d.regimeTrackRecord(ctx)["kinds"].(map[string]any)["trend21"].(map[string]any)
	if e["resolvedN"].(int) != 1 {
		t.Fatalf("want 1 independent obs, got %v", e["resolvedN"])
	}
}

// ── earnings window ──

// Unknown next-earnings is served as honest nulls, never a fabricated date.
func TestEarningsWindowHonestNull(t *testing.T) {
	d, st := newRegimeTrackDeps(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	rec := httptest.NewRecorder()
	d.earningsWindow(rec, httptest.NewRequest("GET", "/api/earnings-window?symbol=AAA&market=stocks", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["estimatedNext"] != nil || resp["daysUntil"] != nil {
		t.Fatalf("no filing on record ⇒ nulls, got %+v", resp)
	}
	if resp["method"] != "filing-cadence heuristic" || resp["caveat"] == "" {
		t.Fatalf("missing method/caveat: %+v", resp)
	}

	// with a 10-Q filed 85 days ago the estimate lands ~6 days out → in window
	filed := time.Now().Unix() - 85*86400
	if _, err := st.InsertFiling(ctx, store.FilingRow{ID: "acc-1", SymbolID: sym.ID,
		Form: "10-Q", FiledTs: filed, Title: "10-Q", URL: "u", Label: "quarterly report"}); err != nil {
		t.Fatalf("filing: %v", err)
	}
	rec = httptest.NewRecorder()
	d.earningsWindow(rec, httptest.NewRequest("GET", "/api/earnings-window?symbol=AAA&market=stocks", nil))
	resp = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["estimatedNext"] == nil || resp["withinWindow"] != true {
		t.Fatalf("want in-window estimate, got %+v", resp)
	}
	if days := resp["daysUntil"].(float64); days < 5 || days > 7 {
		t.Fatalf("daysUntil want ~6, got %v", days)
	}
}

// Pure window math: ceil days, [0,7] inclusive window, negative = overdue.
func TestEarningsWindowFlagMath(t *testing.T) {
	now := int64(1_700_000_000)
	cases := []struct {
		agoDays int64
		days    int
		within  bool
	}{
		{85, 6, true},   // est 6d out
		{84, 7, true},   // est exactly 7d out — inclusive edge
		{83, 8, false},  // est 8d out — outside
		{91, 0, true},   // est today
		{98, -7, false}, // estimate already passed: cadence slipped, not "soon"
		{50, 41, false}, // far out
	}
	for _, c := range cases {
		last := now - c.agoDays*86400
		_, days, within := earningsWindowFrom(last, now)
		if days != c.days || within != c.within {
			t.Fatalf("ago=%dd: got (days=%d within=%v), want (%d %v)",
				c.agoDays, days, within, c.days, c.within)
		}
	}
}

// ── regime postmortems feed ──

func TestRegimePostmortemsEndpoint(t *testing.T) {
	d, st := newRegimeTrackDeps(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "DDD", md.Stocks, "")
	// a real frozen+resolved outcome first — the postmortem FK points at it
	if _, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
		SymbolID: sym.ID, Kind: structregime.KindTrend21, Ts: 1000, HorizonDays: 21,
		Regime: "uptrend", Conviction: 0.91, HistoricalAccuracy: 0.972, Rank: 0.91,
	}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	due, _ := st.DueRegimeOutcomes(ctx, 1<<60, 0)
	if len(due) != 1 {
		t.Fatalf("want 1 outcome, got %d", len(due))
	}
	_ = st.ResolveRegimeOutcome(ctx, due[0].ID, "downtrend", false, 2000)
	o := store.RegimeOutcomeRow{Kind: structregime.KindTrend21, Ts: 1000,
		Regime: "uptrend", Conviction: 0.91, HistoricalAccuracy: 0.972, Actual: "downtrend"}
	if err := st.InsertRegimePostmortem(ctx, due[0].ID, sym.ID, o,
		"sma200_distance_pct_at_horizon", -3.2,
		fmt.Sprintf("trend21 called %q…", o.Regime), 2000); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rec := httptest.NewRecorder()
	d.regimePostmortems(rec, httptest.NewRequest("GET", "/api/regime-postmortems", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Postmortems []store.RegimePostmortem `json:"postmortems"`
		Count       int                      `json:"count"`
		Note        string                   `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 1 || resp.Postmortems[0].Symbol != "DDD" || resp.Note == "" {
		t.Fatalf("bad payload: %+v", resp)
	}
}
