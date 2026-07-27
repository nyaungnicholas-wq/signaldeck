package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestPredictionAttributions_RoundTrip(t *testing.T) {
	st, err := Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	rows := []PredictionAttribution{
		{LedgerSeq: 7, Rank: 0, SymbolID: 3, Horizon: md.H1d, Ts: 1000,
			Name: "comp_rsi", Kind: "component", Contribution: 0.12, Method: AttributionMethodSaabas},
		{LedgerSeq: 7, Rank: 1, SymbolID: 3, Horizon: md.H1d, Ts: 1000,
			Name: "gbm", Kind: "leg", Contribution: -0.06, Method: AttributionMethodSaabas},
	}
	if err := st.InsertPredictionAttributions(ctx, rows); err != nil {
		t.Fatal(err)
	}
	// Idempotent rewrite replaces, never duplicates.
	if err := st.InsertPredictionAttributions(ctx, rows); err != nil {
		t.Fatal(err)
	}
	got, err := st.PredictionAttributions(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "comp_rsi" || got[1].Contribution != -0.06 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	seq, ok, err := st.LatestAttributedSeq(ctx, 3, md.H1d)
	if err != nil || !ok || seq != 7 {
		t.Fatalf("LatestAttributedSeq = %d %v %v", seq, ok, err)
	}
	if _, ok, _ := st.LatestAttributedSeq(ctx, 99, md.H1d); ok {
		t.Error("unknown symbol should report no attributed seq")
	}
	if err := st.InsertPredictionAttributions(ctx, nil); err != nil {
		t.Errorf("empty insert should be a no-op: %v", err)
	}
}
