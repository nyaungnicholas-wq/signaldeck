package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// /market/breadth is a member page that shows /api/market-regimes accuracies,
// and its rows carried no caveat. Each row now carries the same sentence as a
// per-symbol regime row, with its kind's current live status.
func TestMarketRegimesRowsCarryTheLiveCaveat(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	ctx := t.Context()
	base := store.GradingEpoch + 86400
	for _, ticker := range []string{"SPY", "XLK"} {
		sym, err := st.UpsertSymbol(ctx, ticker, md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []structregime.Kind{structregime.KindTrend21, structregime.KindTrend63} {
			if err := st.UpsertRegimeForecast(ctx, sym.ID, base, structregime.Forecast{
				Kind: k, HorizonDays: 21, Regime: "uptrend", Conviction: 0.9,
				HistoricalAccuracy: 0.97, Tier: "very-high conviction", Rank: 0.9, N: 100,
			}); err != nil {
				t.Fatal(err)
			}
		}
		// Two graded trend21 calls per basket on different days; trend63 none.
		for _, ts := range []int64{base, base + 3*86400} {
			if _, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
				SymbolID: sym.ID, Kind: structregime.KindTrend21, Ts: ts, HorizonDays: 21,
				Regime: "uptrend", Conviction: 0.9, HistoricalAccuracy: 0.97, Rank: 0.9, NaiveLabel: "uptrend",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	due, err := st.DueRegimeOutcomes(ctx, base+400*86400, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range due {
		if r.Kind == structregime.KindTrend21 {
			if err := st.ResolveRegimeOutcome(ctx, r.ID, "uptrend", true, base+40*86400); err != nil {
				t.Fatal(err)
			}
		}
	}

	rec := httptest.NewRecorder()
	d.marketRegimes(rec, httptest.NewRequest("GET", "/api/market-regimes", nil))
	var body struct {
		Rows []struct {
			Symbol, Kind   string
			EvidenceCaveat string `json:"evidenceCaveat"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\n%.300s", err, rec.Body.String())
	}
	want := map[string]string{
		"trend21": structregime.EvidenceCaveatFor(4),
		"trend63": structregime.EvidenceCaveatFor(0),
	}
	if len(body.Rows) != 4 {
		t.Fatalf("got %d rows, want 4 (2 baskets x 2 kinds): %.300s", len(body.Rows), rec.Body.String())
	}
	for _, r := range body.Rows {
		if r.EvidenceCaveat != want[r.Kind] {
			t.Errorf("%s %s caveat\n got %.200q\nwant %.200q", r.Symbol, r.Kind, r.EvidenceCaveat, want[r.Kind])
		}
	}
}
