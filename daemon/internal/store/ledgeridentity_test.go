package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// More than one entry per (symbol_id, horizon, bar_ts) is LEGAL in this table,
// and until now no rule said which one counted.
//
// The audit finding proposed a UNIQUE constraint on the identity key. That
// would be the wrong repair twice over: it cannot remove the 335 second-claims
// already recorded (the table is write-once), and going forward it would
// silently drop the very second claim the ledger exists to record. The schema's
// own contract is "an append-only log of EVERY prediction the runner emits...
// the immutable proof of WHAT WAS CLAIMED, WHEN" -- a log that refuses the
// second claim asserts that fewer claims were made than actually were.
//
// So the fix is a documented READING rule, and this is the test of it: the
// highest seq is the operative claim, earlier ones are superseded and retained.

// TestLedger_HighestSeqIsTheOperativeClaim pins the rule a reader needs in
// order to interpret a duplicated identity at all. Both claims survive, the
// later one is returned first, and -- the part that makes the ledger still
// worth trusting -- the chain verifies with the duplicate in it.
func TestLedger_HighestSeqIsTheOperativeClaim(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	id := LedgerEntry{
		PredictedAt: 1_700_000_000, SymbolID: 7, Horizon: md.H1d, BarTs: 1_600_000_000,
		RawProb: 0.41, CalProb: 0.42,
		FeatureHash: HashFeatureVector(map[string]float64{"a": 1}), ModelVersion: 1,
	}
	first, err := st.AppendLedger(ctx, id)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}

	// A genuinely CONFLICTING second claim for the same bar -- the 15-group
	// case from the finding, not a harmless replay.
	superseding := id
	superseding.PredictedAt = 1_700_000_060
	superseding.RawProb, superseding.CalProb = 0.58, 0.61
	superseding.FeatureHash = HashFeatureVector(map[string]float64{"a": 2})
	second, err := st.AppendLedger(ctx, superseding)
	if err != nil {
		t.Fatalf("second append: %v", err)
	}

	if second.Seq <= first.Seq {
		t.Fatalf("second seq %d did not grow past %d", second.Seq, first.Seq)
	}

	// Both claims are retained. Refusing or collapsing the second would make
	// the log under-report what was claimed.
	entries, err := st.LedgerFor(ctx, id.SymbolID, id.Horizon, 10)
	if err != nil {
		t.Fatalf("LedgerFor: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (both claims retained)", len(entries))
	}

	// THE RULE: highest seq first, so the operative claim is entries[0].
	if entries[0].Seq != second.Seq {
		t.Fatalf("first row seq = %d, want the highest (%d) -- the operative claim must read first",
			entries[0].Seq, second.Seq)
	}
	if entries[0].CalProb != 0.61 {
		t.Fatalf("operative cal_prob = %v, want 0.61", entries[0].CalProb)
	}
	if entries[1].Seq != first.Seq || entries[1].CalProb != 0.42 {
		t.Fatalf("superseded row = seq %d cal_prob %v, want seq %d / 0.42",
			entries[1].Seq, entries[1].CalProb, first.Seq)
	}

	// A duplicated identity must not break tamper-evidence. VerifyLedger
	// rehashes each row from its STORED feature_hash, so the link holds even
	// though the superseded entry's hash is no longer re-derivable from
	// `features` (which keeps only the latest vector).
	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatalf("VerifyLedger: %v", err)
	}
	if !v.Intact {
		t.Fatalf("chain not intact with a duplicated identity (broken at seq %v)", v.BrokenAtSeq)
	}
	if v.Count != 2 {
		t.Fatalf("verified count = %d, want 2", v.Count)
	}
}
