package api

import (
	"context"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A-2 (2026-08-02 re-audit): /api/honesty published an IC over 880 independent
// symbol-days spanning FIVE market days, with no distinct-day count and no
// interval. Deduplicating to one row per (symbol, UTC-day) removes intraday
// pseudo-replication and leaves the bigger problem untouched — on one day every
// symbol shares one market move.
//
// The shape that matters is MANY SYMBOLS over FEW DAYS: independentN looks
// large while the number of genuinely independent market moves is tiny. One
// symbol cannot produce it, because there independentN == distinctDays.

// seedSymbolsOverDays gives each of nSymbols the same `days` resolved UTC-days,
// so independentN = nSymbols*days while distinctDays stays `days`.
func seedSymbolsOverDays(t *testing.T, st *store.Store, nSymbols, days int) {
	t.Helper()
	ctx := context.Background()
	for s := 0; s < nSymbols; s++ {
		sym, err := st.UpsertSymbol(ctx, string(rune('A'+s/26))+string(rune('A'+s%26)), md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert symbol %d: %v", s, err)
		}
		seedOutcomes(t, st, sym.ID, md.H1d, days, 1)
	}
}

// Below the day floor the interval must be WITHHELD — and the point estimate
// must survive, because the clustering governs the strength of the claim, not
// the claim itself.
func TestHonestyWithholdsICIntervalBelowDayFloor(t *testing.T) {
	srv, st := newHonestyServer(t)
	seedSymbolsOverDays(t, st, 40, 5) // 200 independent obs, 5 market days

	body := getJSON(t, srv, "/api/honesty?horizon=1d")

	if got := jnum(body, "independentN"); got != 200 {
		t.Fatalf("independentN = %.0f, want 200", got)
	}
	if got := jnum(body, "distinctDays"); got != 5 {
		t.Fatalf("distinctDays = %.0f, want 5 — the count A-2 said was absent", got)
	}
	if body["icGated"] != false {
		t.Fatalf("IC should be ungated at 200 independent obs, got icGated=%v", body["icGated"])
	}
	if body["ic"] == nil {
		t.Fatal("point estimate must stand — clustering governs the interval, not the IC")
	}
	if body["icCI"] != nil {
		t.Errorf("icCI must be withheld at 5 days (floor %d), got %v", clusterstat.MinDistinctDays, body["icCI"])
	}
	if _, ok := body["icCINote"].(string); !ok {
		t.Error("a withheld interval must say why")
	}
}

// Above the floor the interval is published, resampled over whole days.
func TestHonestyPublishesDayBootstrappedICInterval(t *testing.T) {
	srv, st := newHonestyServer(t)
	seedSymbolsOverDays(t, st, 40, 12) // 480 independent obs, 12 market days

	body := getJSON(t, srv, "/api/honesty?horizon=1d")

	if got := jnum(body, "distinctDays"); got != 12 {
		t.Fatalf("distinctDays = %.0f, want 12", got)
	}
	raw, ok := body["icCI"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("icCI must be a 2-element interval above the day floor, got %v", body["icCI"])
	}
	lo, hi := raw[0].(float64), raw[1].(float64)
	if !(lo <= hi) {
		t.Errorf("icCI is inverted: [%g, %g]", lo, hi)
	}
	ic := jnum(body, "ic")
	if ic < lo || ic > hi {
		t.Errorf("point estimate %g falls outside its own interval [%g, %g]", ic, lo, hi)
	}
}

// The distinct-day count and the note must ship on every response, gated or
// not — their absence was the finding.
func TestHonestyAlwaysReportsDayClustering(t *testing.T) {
	srv, st := newHonestyServer(t)
	seedSymbolsOverDays(t, st, 2, 2) // deliberately tiny: IC itself is gated

	body := getJSON(t, srv, "/api/honesty?horizon=1d")

	if _, ok := body["distinctDays"]; !ok {
		t.Error("distinctDays missing on a gated response")
	}
	if _, ok := body["clusterNote"].(string); !ok {
		t.Error("clusterNote missing on a gated response")
	}
	if body["icCI"] != nil {
		t.Error("a gated IC must not carry an interval")
	}
}
