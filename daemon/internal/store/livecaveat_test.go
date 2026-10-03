package store

import (
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// E-CAVEAT-DATE (2026-10-02): a served regime row's caveat states its kind's
// current live status from regime_outcomes, one resolution per symbol per day,
// so a kind with resolved calls never reads "zero live resolutions" and a kind
// with none never reads as graded.
func TestServedRegimeCaveatStatesLiveResolutions(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	base := GradingEpoch + 86400

	symA, err := st.UpsertSymbol(ctx, "LCA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	symB, err := st.UpsertSymbol(ctx, "LCB", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range []md.Symbol{symA, symB} {
		for _, k := range []structregime.Kind{structregime.KindTrend21, structregime.KindTrend63} {
			h := 21
			if k == structregime.KindTrend63 {
				h = 63
			}
			if err := st.UpsertRegimeForecast(ctx, sym.ID, base, structregime.Forecast{
				Kind: k, HorizonDays: h, Regime: "uptrend", Conviction: 0.9,
				HistoricalAccuracy: 0.97, Tier: "very-high conviction", Rank: 0.9, N: 100,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	call := func(sym md.Symbol, k structregime.Kind, ts int64) {
		t.Helper()
		if _, err := st.InsertRegimeOutcome(ctx, RegimeCall{
			SymbolID: sym.ID, Kind: k, Ts: ts, HorizonDays: 21, Regime: "uptrend",
			Conviction: 0.9, HistoricalAccuracy: 0.97, Rank: 0.9, NaiveLabel: "uptrend",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Three trend21 calls on three (symbol, day)s, all resolved below; one
	// trend63 call left open.
	call(symA, structregime.KindTrend21, base)
	call(symA, structregime.KindTrend21, base+3*86400)
	call(symB, structregime.KindTrend21, base)
	call(symA, structregime.KindTrend63, base)
	rows, err := st.db.QueryContext(ctx, `SELECT id FROM regime_outcomes WHERE kind='trend21'`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close() //nolint:errcheck,gosec
	if len(ids) != 3 {
		t.Fatalf("seeded %d trend21 calls, want 3", len(ids))
	}
	for _, id := range ids {
		if err := st.ResolveRegimeOutcome(ctx, id, "uptrend", true, base+40*86400); err != nil {
			t.Fatal(err)
		}
	}
	// An open call does not count.
	call(symB, structregime.KindTrend21, base+6*86400)

	if m := st.LiveRegimeResolutions(ctx); m["trend21"] != 3 || m["trend63"] != 0 {
		t.Fatalf("LiveRegimeResolutions = %v, want trend21:3 trend63:0", m)
	}
	want := map[structregime.Kind]string{
		structregime.KindTrend21: structregime.EvidenceCaveatFor(3),
		structregime.KindTrend63: structregime.EvidenceCaveatFor(0),
	}
	check := func(what string, got []RegimeForecast, n int) {
		t.Helper()
		seen := map[structregime.Kind]int{}
		for _, r := range got {
			seen[r.Kind]++
			if r.EvidenceCaveat != want[r.Kind] {
				t.Errorf("%s %s: caveat\n got %.160q\nwant %.160q", what, r.Kind, r.EvidenceCaveat, want[r.Kind])
			}
		}
		if seen[structregime.KindTrend21] != n || seen[structregime.KindTrend63] != n {
			t.Errorf("%s: rows by kind %v, want %d of each", what, seen, n)
		}
	}
	all, err := st.RegimeForecasts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check("RegimeForecasts", all, 2)
	one, err := st.RegimeForecastsForSymbol(ctx, symA.ID)
	if err != nil {
		t.Fatal(err)
	}
	check("RegimeForecastsForSymbol", one, 1)
}
