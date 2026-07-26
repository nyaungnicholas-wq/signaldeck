package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// appendFabricated appends n entries whose payloads differ from appendN's, so a
// regenerated chain is byte-different from the honest one it replaced while
// still being a perfectly well-formed chain.
func appendFabricated(t *testing.T, st *Store, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := st.AppendLedger(ctx, LedgerEntry{
			PredictedAt:  int64(1_700_000_000 + i),
			SymbolID:     int64(1 + i%3),
			Horizon:      md.H1d,
			BarTs:        int64(1_600_000_000 + i*60),
			RawProb:      0.90, // the fabrication: every call was a confident winner
			CalProb:      0.88,
			FeatureHash:  HashFeatureVector(map[string]float64{"a": float64(i)}),
			ModelVersion: 1,
		}); err != nil {
			t.Fatalf("fabricated append %d: %v", i, err)
		}
	}
}

// TestLedger_ChainProvesConsistencyNotAnteriority is the REPRODUCTION of finding
// C2, kept as a permanent statement of what the hash chain alone cannot do.
//
// The chain's only anchor lives in the same SQLite file (meta
// ledger_verify_checkpoint). An operator who deletes every honest row, deletes
// that meta key, and regenerates fabricated rows through the same AppendLedger
// produces a chain that is internally perfect — and BOTH the cached verify and
// the full genesis walk report intact=true. Internal consistency is not
// anteriority, and anteriority is the only property an allocator cares about.
//
// This test must keep passing: it is not a defect to fix inside the chain, it
// is the reason the external anchor (ledger_anchors) has to exist. If it ever
// starts failing, someone has made VerifyLedger claim something the chain does
// not prove.
func TestLedger_ChainProvesConsistencyNotAnteriority(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	appendN(t, st, 200)

	// Establish the in-DB checkpoint exactly as the live daemon does.
	before, _, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Intact || before.Count != 200 {
		t.Fatalf("honest chain: intact=%v count=%d, want true/200", before.Intact, before.Count)
	}
	honestHead := before.HeadHash

	// ── The operator rewrites history ──────────────────────────────────────
	if _, err := st.w.ExecContext(ctx, `DELETE FROM prediction_ledger`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.w.ExecContext(ctx, `DELETE FROM meta WHERE k=?`, metaLedgerVerifyCkpt); err != nil {
		t.Fatal(err)
	}
	// Reset the AUTOINCREMENT counter so the fabricated rows even occupy the
	// same seq range — the strongest form of the rewrite.
	if _, err := st.w.ExecContext(ctx,
		`DELETE FROM sqlite_sequence WHERE name='prediction_ledger'`); err != nil {
		t.Fatal(err)
	}
	appendFabricated(t, st, 200)

	full, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached, _, err := st.VerifyLedgerCached(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Both report intact. That is the finding, reproduced.
	if !full.Intact || !cached.Intact {
		t.Fatalf("regenerated chain reported broken (full=%v cached=%v) — the C2 reproduction no longer holds; re-derive the claim before relying on this",
			full.Intact, cached.Intact)
	}
	if full.Count != 200 || cached.Count != 200 {
		t.Fatalf("regenerated counts: full=%d cached=%d, want 200/200", full.Count, cached.Count)
	}
	if full.HeadHash == honestHead {
		t.Fatal("fabricated head equals the honest head — the fabrication did not actually change history")
	}
}

// testSigner makes a signing key in a temp dir — never the operator's real key
// path, and never inside the database directory.
func testSigner(t *testing.T) *ledgeranchor.Signer {
	t.Helper()
	sg, err := ledgeranchor.LoadOrCreateSigner(filepath.Join(t.TempDir(), "anchor.key"))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return sg
}

// anchorNow forces one anchor to be written, failing the test if the cadence or
// a precondition refused it.
func anchorNow(t *testing.T, st *Store, sg *ledgeranchor.Signer, now time.Time) ledgeranchor.Record {
	t.Helper()
	ctx := context.Background()
	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec, wrote, reason, err := st.MaybeAnchorLedger(ctx, sg, v, AnchorPolicy{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatalf("no anchor written: %s", reason)
	}
	return rec
}

// TestLedgerAnchor_CatchesWholesaleRegeneration is the C2 fix, proven against
// the exact break reproduced in TestLedger_ChainProvesConsistencyNotAnteriority:
// an operator deletes every row, deletes the in-DB checkpoint and regenerates a
// fabricated chain. The chain still reports intact — it always will, that is
// what a self-referential anchor buys — but the SIGNED anchor no longer
// reproduces, and nothing is reported as proven.
//
// Both regeneration shapes are covered: rows that land on fresh seqs (the
// anchored seq vanishes) and rows forced back onto the ORIGINAL seq range (the
// anchored seq exists but yields a different head). A fix that only caught the
// first would be defeated by resetting sqlite_sequence.
func TestLedgerAnchor_CatchesWholesaleRegeneration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resetSeqs bool
	}{
		{"regenerated at fresh seqs", false},
		{"regenerated onto the original seq range", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openTemp(t)
			ctx := context.Background()
			sg := testSigner(t)

			appendN(t, st, 200)
			anchored := anchorNow(t, st, sg, time.Unix(1_700_000_500, 0))
			if anchored.LedgerSeq != 200 || anchored.LedgerCount != 200 {
				t.Fatalf("anchor pinned seq/count %d/%d, want 200/200", anchored.LedgerSeq, anchored.LedgerCount)
			}

			// Before the rewrite the anchor must verify, or the test proves nothing.
			pre, err := st.VerifyLedgerAnchors(ctx, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			if pre.ProvenThroughSeq == nil || *pre.ProvenThroughSeq != 200 {
				t.Fatalf("honest chain: provenThroughSeq = %v, want 200", pre.ProvenThroughSeq)
			}

			// ── The operator rewrites history ──────────────────────────────
			if _, err := st.w.ExecContext(ctx, `DELETE FROM prediction_ledger`); err != nil {
				t.Fatal(err)
			}
			if _, err := st.w.ExecContext(ctx, `DELETE FROM meta WHERE k=?`, metaLedgerVerifyCkpt); err != nil {
				t.Fatal(err)
			}
			if tc.resetSeqs {
				if _, err := st.w.ExecContext(ctx,
					`DELETE FROM sqlite_sequence WHERE name='prediction_ledger'`); err != nil {
					t.Fatal(err)
				}
			}
			appendFabricated(t, st, 200)

			// The chain is still fooled — that is the premise, not a bug here.
			chain, err := st.VerifyLedger(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !chain.Intact {
				t.Fatalf("chain reported broken; this test must run against the state where the chain is fooled")
			}

			for _, recompute := range []bool{true, false} {
				av, err := st.VerifyLedgerAnchors(ctx, 0, recompute)
				if err != nil {
					t.Fatal(err)
				}
				if len(av.Anchors) != 1 {
					t.Fatalf("recompute=%v: %d anchors, want 1", recompute, len(av.Anchors))
				}
				a := av.Anchors[0]
				if a.OK {
					t.Fatalf("recompute=%v: anchor reported OK after wholesale regeneration — C2 is NOT fixed", recompute)
				}
				if !a.SignatureOK {
					t.Errorf("recompute=%v: signature failed to verify; the break must be attributed to the CHAIN, not the signature", recompute)
				}
				if a.Reason == "" {
					t.Errorf("recompute=%v: anchor failed without a stated reason", recompute)
				}
				if av.ProvenThroughSeq != nil || av.ProvenThroughCount != nil || av.ProvenThroughTs != nil {
					t.Errorf("recompute=%v: provenThrough = %v/%v/%v, want all nil (withheld, never 0)",
						recompute, av.ProvenThroughSeq, av.ProvenThroughCount, av.ProvenThroughTs)
				}
			}

			// An operator who also deletes the anchors gets no claim either —
			// the absence of anchors must never read as "proven".
			if _, err := st.w.ExecContext(ctx, `DELETE FROM ledger_anchors`); err != nil {
				t.Fatal(err)
			}
			av, err := st.VerifyLedgerAnchors(ctx, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			if av.AnchorCount != 0 || av.ProvenThroughSeq != nil {
				t.Errorf("after deleting anchors: count=%d provenThroughSeq=%v, want 0/nil", av.AnchorCount, av.ProvenThroughSeq)
			}
		})
	}
}

// TestLedgerAnchor_HonestChainVerifiesAndPublishes: on an untouched chain every
// anchor verifies in both modes, the proven prefix is the newest anchor, and the
// publish line is a short digest that a third party can re-derive from the
// served record (that digest is the only thing an operator holding the key
// cannot rewrite after the fact).
func TestLedgerAnchor_HonestChainVerifiesAndPublishes(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sg := testSigner(t)

	appendN(t, st, 50)
	first := anchorNow(t, st, sg, time.Unix(1_700_000_000, 0))
	appendN(t, st, 25)
	second := anchorNow(t, st, sg, time.Unix(1_700_100_000, 0))

	for _, recompute := range []bool{true, false} {
		av, err := st.VerifyLedgerAnchors(ctx, 0, recompute)
		if err != nil {
			t.Fatal(err)
		}
		if av.AnchorCount != 2 || av.Checked != 2 {
			t.Fatalf("recompute=%v: anchorCount=%d checked=%d, want 2/2", recompute, av.AnchorCount, av.Checked)
		}
		for _, a := range av.Anchors {
			if !a.OK {
				t.Errorf("recompute=%v: anchor at seq %d not OK: %s", recompute, a.Record.LedgerSeq, a.Reason)
			}
		}
		// Newest first: the proven prefix is the SECOND anchor, which subsumes
		// the first (reproducing a head fixes the whole prefix behind it).
		if av.ProvenThroughSeq == nil || *av.ProvenThroughSeq != second.LedgerSeq {
			t.Fatalf("recompute=%v: provenThroughSeq = %v, want %d", recompute, av.ProvenThroughSeq, second.LedgerSeq)
		}
		if av.ProvenThroughCount == nil || *av.ProvenThroughCount != 75 {
			t.Errorf("recompute=%v: provenThroughCount = %v, want 75", recompute, av.ProvenThroughCount)
		}
		if len(av.DistinctPubKeys) != 1 || av.DistinctPubKeys[0] != sg.PublicKeyHex() {
			t.Errorf("recompute=%v: pubkeys = %v, want [%s]", recompute, av.DistinctPubKeys, sg.PublicKeyHex())
		}
		if !strings.Contains(av.Anchors[0].Publish, second.Digest()) {
			t.Errorf("publish line %q does not carry the digest %q", av.Anchors[0].Publish, second.Digest())
		}
	}
	if first.Digest() == second.Digest() {
		t.Error("two anchors over different heads share a digest — the digest does not bind the head")
	}
}

// TestLedgerAnchor_RecomputeCatchesStoredHashPreservingRewrite: a payload
// mutated BEFORE the anchor with every stored hash left untouched is the one
// tamper the incremental verify path is documented to miss. The recompute-mode
// anchor check re-derives the chain from payloads, so it catches it — and the
// cheap stored-hash mode does not, which is why the mode is reported rather
// than assumed.
func TestLedgerAnchor_RecomputeCatchesStoredHashPreservingRewrite(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sg := testSigner(t)

	appendN(t, st, 40)
	anchorNow(t, st, sg, time.Unix(1_700_000_000, 0))

	if _, err := st.w.ExecContext(ctx,
		`UPDATE prediction_ledger SET cal_prob = cal_prob + 0.25 WHERE seq=11`); err != nil {
		t.Fatal(err)
	}

	strong, err := st.VerifyLedgerAnchors(ctx, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if strong.Anchors[0].OK || strong.ProvenThroughSeq != nil {
		t.Fatal("recompute mode reported the anchor OK after a payload rewrite before it")
	}
	if strong.Anchors[0].HeadMatches {
		t.Error("recompute mode says the head still matches — it re-derived from stored hashes, not payloads")
	}

	cheap, err := st.VerifyLedgerAnchors(ctx, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !cheap.Anchors[0].HeadMatches {
		t.Error("stored mode unexpectedly caught a stored-hash-preserving mutation; if this is now true, the documented mode difference is stale and the comment must be corrected")
	}
}

// TestLedgerAnchor_RefusalsAreStated: every path that declines to anchor says
// why. A missing anchor with no reason is indistinguishable from a suppressed
// one, and the platform's rule is that a withheld value carries its reason.
func TestLedgerAnchor_RefusalsAreStated(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sg := testSigner(t)
	now := time.Unix(1_700_000_000, 0)

	// Empty ledger.
	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrote, reason, err := st.MaybeAnchorLedger(ctx, sg, v, AnchorPolicy{}, now); err != nil || wrote || reason == "" {
		t.Fatalf("empty ledger: wrote=%v reason=%q err=%v, want false/non-empty/nil", wrote, reason, err)
	}

	// No key.
	appendN(t, st, 5)
	v, err = st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrote, reason, _ := st.MaybeAnchorLedger(ctx, nil, v, AnchorPolicy{}, now); wrote || reason == "" {
		t.Errorf("no signer: wrote=%v reason=%q, want false/non-empty", wrote, reason)
	}

	// Cadence: a second anchor inside MinInterval is refused with a reason,
	// and allowed once the interval has passed and the chain has grown.
	anchorNow(t, st, sg, now)
	p := AnchorPolicy{MinInterval: 6 * time.Hour, MinNewEntries: 1}
	appendN(t, st, 3)
	v, err = st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrote, reason, _ := st.MaybeAnchorLedger(ctx, sg, v, p, now.Add(time.Hour)); wrote || !strings.Contains(reason, "cadence") {
		t.Errorf("inside cadence: wrote=%v reason=%q, want false/cadence reason", wrote, reason)
	}
	if _, wrote, reason, _ := st.MaybeAnchorLedger(ctx, sg, v, p, now.Add(7*time.Hour)); !wrote {
		t.Errorf("after the interval with new entries: wrote=false reason=%q, want an anchor", reason)
	}
	// No new entries since that anchor → refused with a reason.
	v, err = st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrote, reason, _ := st.MaybeAnchorLedger(ctx, sg, v, p, now.Add(24*time.Hour)); wrote || reason == "" {
		t.Errorf("no new entries: wrote=%v reason=%q, want false/non-empty", wrote, reason)
	}

	// A broken chain is never anchored: signing a head the chain itself
	// rejects would launder a detected break into a signed claim.
	if _, err := st.w.ExecContext(ctx, `UPDATE prediction_ledger SET raw_prob=raw_prob+1 WHERE seq=2`); err != nil {
		t.Fatal(err)
	}
	broken, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if broken.Intact {
		t.Fatal("chain still intact after tamper — test setup is wrong")
	}
	if _, wrote, reason, _ := st.MaybeAnchorLedger(ctx, sg, broken, AnchorPolicy{}, now.Add(48*time.Hour)); wrote || reason == "" {
		t.Errorf("broken chain: wrote=%v reason=%q, want false/non-empty", wrote, reason)
	}
}

// TestLedgerAnchor_NoDuplicateAnchorForTheSameHead: two verify requests racing
// through the cadence check must not both add an anchor for the same head, and
// re-anchoring an already-anchored head with the same key adds nothing (the
// earlier timestamp is the stronger claim). A rotated key IS recorded, because
// a new key over the same head is a new fact.
func TestLedgerAnchor_NoDuplicateAnchorForTheSameHead(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sg := testSigner(t)
	appendN(t, st, 10)

	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Zero policy = no cadence gate at all, so only the insert guard can stop
	// the second write. That is the concurrent-request case, made determinstic.
	if _, wrote, reason, err := st.MaybeAnchorLedger(ctx, sg, v, AnchorPolicy{}, time.Unix(1_700_000_000, 0)); err != nil || !wrote {
		t.Fatalf("first anchor: wrote=%v reason=%q err=%v", wrote, reason, err)
	}
	if _, wrote, reason, _ := st.MaybeAnchorLedger(ctx, sg, v, AnchorPolicy{}, time.Unix(1_700_000_060, 0)); wrote || reason == "" {
		t.Errorf("second anchor over the same head: wrote=%v reason=%q, want false with a reason", wrote, reason)
	}

	rotated := testSigner(t)
	if _, wrote, reason, _ := st.MaybeAnchorLedger(ctx, rotated, v, AnchorPolicy{}, time.Unix(1_700_000_120, 0)); !wrote {
		t.Errorf("rotated key over the same head: wrote=false reason=%q, want it recorded", reason)
	}

	av, err := st.VerifyLedgerAnchors(ctx, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if av.AnchorCount != 2 {
		t.Errorf("anchorCount = %d, want 2 (one per key)", av.AnchorCount)
	}
	if len(av.DistinctPubKeys) != 2 {
		t.Errorf("distinct pubkeys = %d, want 2 — a key rotation must stay visible", len(av.DistinctPubKeys))
	}
}
