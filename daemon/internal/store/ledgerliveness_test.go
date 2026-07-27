package store

import (
	"context"
	"strings"
	"testing"
	"time"

	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

// A 0.95 POSTERIOR WITH NO REPLICATION ROW MUST RENDER "UNREPLICATED".
//
// The live ledger on 2026-07-27 held 55 attack + 15 backtest + 16 manual rows,
// every one written 2026-07-16/17, and ZERO rows of kind experiment or
// replication — so not one of its 19 hypotheses had ever been graded on a fresh
// window, while five published 0.95-0.97 under the word "tentative". The band
// says how strong a number is and cannot say when it was last put at risk, so
// "tentative, posterior 0.952" and "seeded once, never re-tested" were the same
// sentence. This asserts they are no longer.
func TestLedgerEngineHealth_UnreplicatedPosterior(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	h := rl.Hypothesis{ID: "H015", Family: "liquidity", Statement: "s",
		Prior: 0.5, MaxEdge: 0.1, Posterior: 0.95, Status: rl.StatusTentative}
	if err := st.UpsertLedgerHypothesis(ctx, h, now.Unix()); err != nil {
		t.Fatal(err)
	}
	// A chain of attack and manual rows only — the real shape.
	for _, e := range []rl.Evidence{
		{HypID: "H015", Ts: now.Unix() - 86400, Kind: rl.KindManual, BF: 3},
		{HypID: "H015", Ts: now.Unix() - 86400, Kind: rl.KindAttack, BF: 1},
	} {
		if err := st.InsertLedgerEvidence(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.LedgerEngineHealth(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Healthy || got.State != rl.LivenessUnreplicated || got.Unreplicated != 1 {
		t.Fatalf("a 0.95 with zero replication rows must make the ledger UNREPLICATED: %+v", got)
	}
	if len(got.Hypotheses) != 1 {
		t.Fatalf("want one hypothesis row, got %+v", got.Hypotheses)
	}
	row := got.Hypotheses[0]
	if row.Verdict != rl.LivenessUnreplicated {
		t.Fatalf("verdict = %q, want UNREPLICATED — it must REPLACE the band, "+
			"not sit beside it", row.Verdict)
	}
	if row.Verdict == rl.StatusTentative {
		t.Fatal("an unreplicated posterior must not be renderable as tentative")
	}
	if row.ReplicationRows != 0 || row.ExperimentRows != 0 {
		t.Fatalf("counts wrong: %+v", row)
	}
	if row.EvidenceAgeDays != 1 || got.EvidenceRows != 2 {
		t.Fatalf("evidence age/rows wrong: %+v / %d", row, got.EvidenceRows)
	}
	if !strings.Contains(got.Detail, "ZERO replication rows") {
		t.Errorf("the state must name what is missing: %q", got.Detail)
	}

	// ONE replication on a fresh disjoint window is the only thing that clears
	// it — no re-band, no re-word, no threshold move.
	if err := st.InsertLedgerEvidence(ctx, rl.Evidence{
		HypID: "H015", Ts: now.Unix(), Kind: rl.KindReplication, BF: 2,
		WindowFrom: now.Unix() - 604800, WindowTo: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	after, err := st.LedgerEngineHealth(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Healthy || after.Hypotheses[0].Verdict != rl.StatusTentative {
		t.Fatalf("a replicated hypothesis renders its band again: %+v", after.Hypotheses[0])
	}
}
