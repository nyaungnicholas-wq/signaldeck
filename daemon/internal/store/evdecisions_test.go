package store

import (
	"context"
	"testing"
)

// TestEVDecisions_RoundTrip: the decision ledger stores what it was given —
// including a NULL net EV, which must come back nil, never zero.
func TestEVDecisions_RoundTrip(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	nev := 0.0042
	rows := []EVDecision{
		{Ts: 100, Strategy: "flagship-1d", SymbolID: 1, Symbol: "AAA", Horizon: "1d",
			Decision: "BUY", Reason: "positive-net-ev", NetEV: &nev, Rank: 1, RankOf: 2, InputsJSON: `{"symbol":"AAA"}`},
		{Ts: 100, Strategy: "flagship-1d", SymbolID: 2, Symbol: "BBB", Horizon: "1d",
			Decision: "DO_NOTHING", Reason: "missing-required-input", NetEV: nil, Rank: 2, RankOf: 2, InputsJSON: `{"symbol":"BBB"}`},
	}
	for _, d := range rows {
		if err := st.InsertEVDecision(ctx, d); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	got, err := st.EVDecisions(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	// Newest first: the refusal was inserted second.
	if got[0].Decision != "DO_NOTHING" || got[0].NetEV != nil {
		t.Fatalf("refusal row: %+v (NetEV must be nil, not zero)", got[0])
	}
	if got[1].Decision != "BUY" || got[1].NetEV == nil || *got[1].NetEV != nev {
		t.Fatalf("buy row: %+v", got[1])
	}

	// Filters: only refusals; only one symbol.
	refusals, _ := st.EVDecisions(ctx, "DO_NOTHING", "", 10)
	if len(refusals) != 1 || refusals[0].Symbol != "BBB" {
		t.Fatalf("decision filter: %+v", refusals)
	}
	bySym, _ := st.EVDecisions(ctx, "", "AAA", 10)
	if len(bySym) != 1 || bySym[0].Decision != "BUY" {
		t.Fatalf("symbol filter: %+v", bySym)
	}
}
