// What the ledger verifier may CLAIM, as opposed to what it checked.
//
// The defect (audit F09, 2026-09-20): tamperEvidence derived
// detectsOperatorRegeneration=true from a locally reproducing anchor, in the
// same payload whose own prose says "an operator holding the signing key can
// re-sign a fabricated chain; only the externally published anchor digest
// defeats that". A field that names the operator was being set by a check the
// operator passes trivially.
//
// These tests pin the four levels apart: a stored-head comparison, a payload
// recomputation, a valid local signature, and an externally witnessed digest.
// Only the last one is evidence against the key holder, and nothing in this
// daemon can currently produce it.
//
// Every fixture runs against the test server's own temporary database. Nothing
// here touches the real ledger.
package api

import (
	"context"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A signature this machine made, over a chain this machine holds, verified by
// this machine. That is not anteriority against the operator, however many
// anchors reproduce.
func TestLedgerVerify_LocalAnchorIsNotOperatorResistant(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 20, 0.5)

	v := getLedgerVerify(t, srv, "")
	if !v.Tamper.Anchoring.Wrote {
		t.Fatalf("no anchor written: %q", v.Tamper.Anchoring.Reason)
	}
	// The local check succeeded ...
	if !v.Tamper.LocalAnchorsReproduce {
		t.Fatal("the anchor just written must reproduce over the chain it signed")
	}
	if v.Tamper.ProvenAnteriorThroughSeq == nil || *v.Tamper.ProvenAnteriorThroughSeq != 20 {
		t.Fatalf("provenAnteriorThroughSeq = %v, want 20", v.Tamper.ProvenAnteriorThroughSeq)
	}
	// ... and the operator claim must STILL be false, because no receipt was
	// verified. This is the regression: it used to be true here.
	if v.Tamper.DetectsOperatorRegeneration {
		t.Fatal("detectsOperatorRegeneration=true from a local signature alone — " +
			"the operator holds the key, so he passes this check by re-signing")
	}
	if v.Tamper.ExternalWitness.Verified {
		t.Fatal("externalWitness.verified=true with no receipt wired through")
	}
	if !strings.Contains(v.Tamper.ExternalWitness.Reason, "receipt") {
		t.Fatalf("externalWitness must say why it is not established, got %q",
			v.Tamper.ExternalWitness.Reason)
	}
	// The anteriority numbers must carry their scope with them, so a client that
	// renders the number without the prose cannot overstate it.
	if !strings.Contains(v.Tamper.AnteriorityScope, "WITHOUT the signing key") {
		t.Fatalf("anteriorityScope = %q", v.Tamper.AnteriorityScope)
	}
	// And the prose must name the operator as the case it does NOT cover.
	if !strings.Contains(v.Tamper.Claim, "Against the OPERATOR") {
		t.Fatalf("claim does not separate the key holder: %q", v.Tamper.Claim)
	}
}

// A local publication MARKER is not a receipt. meta.anchor_last_published is an
// integer this machine wrote about its own publish script; an operator who can
// rewrite the ledger can write that integer too. Setting it must not promote
// anything.
func TestLedgerVerify_LocalPublicationMarkerProvesNothing(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 12, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote {
		t.Fatalf("no anchor: %q", v.Tamper.Anchoring.Reason)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT OR REPLACE INTO meta(k, v) VALUES('anchor_last_published', '4000000000')`); err != nil {
		t.Fatal(err)
	}

	v := getLedgerVerify(t, srv, "?full=1")
	if v.Tamper.DetectsOperatorRegeneration || v.Tamper.ExternalWitness.Verified {
		t.Fatal("a self-written publication timestamp was treated as an external receipt")
	}
	if !v.Tamper.LocalAnchorsReproduce {
		t.Fatal("the local anchor should still reproduce; only the CLAIM changed")
	}
}

// The mode a reader is told about must be the mode that ran: stored-head
// comparison and payload recomputation are different checks with different
// coverage, and ?full=1 is the one an auditor should run.
func TestLedgerVerify_ReportsWhichCheckProducedTheAnswer(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 8, 0.5)

	fast := getLedgerVerify(t, srv, "")
	if !fast.Tamper.StoredHeadComparison || fast.Tamper.PayloadRecomputed {
		t.Fatalf("default verify: stored=%v recomputed=%v, want true/false",
			fast.Tamper.StoredHeadComparison, fast.Tamper.PayloadRecomputed)
	}
	full := getLedgerVerify(t, srv, "?full=1")
	if full.Tamper.StoredHeadComparison || !full.Tamper.PayloadRecomputed {
		t.Fatalf("?full=1: stored=%v recomputed=%v, want false/true",
			full.Tamper.StoredHeadComparison, full.Tamper.PayloadRecomputed)
	}
	// The two booleans must agree with the string they were derived from.
	if (fast.Tamper.StoredHeadComparison) != (fast.Tamper.AnchorCheckMode == "stored") {
		t.Fatal("storedHeadComparison disagrees with anchorCheckMode")
	}
}

// A payload mutated in place while its stored hashes are left alone: the fast
// path cannot see it, the full walk can, and neither may report anteriority
// against the operator. Runs on the test database only.
func TestLedgerVerify_StoredHashPreservingMutation(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 15, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote {
		t.Fatalf("no anchor: %q", v.Tamper.Anchoring.Reason)
	}

	// Rewrite what was CLAIMED, leaving entry_hash untouched.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE prediction_ledger SET cal_prob = 0.99 WHERE seq = 3`); err != nil {
		t.Fatal(err)
	}

	full := getLedgerVerify(t, srv, "?full=1")
	if full.Intact {
		t.Fatal("the full walk must catch a payload mutation that kept the stored hashes")
	}
	if full.Tamper.DetectsOperatorRegeneration {
		t.Fatal("a broken chain must not claim operator-resistant anteriority")
	}
}
