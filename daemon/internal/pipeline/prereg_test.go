// The amendment path is the only honest way a frozen claim may change.
//
// Editing prereg.Specs() without appending an amendment would leave the chain
// asserting one number while the code serves another — the precise divergence
// pre-registration exists to make impossible. These tests pin that: a changed
// spec APPENDS, the original record survives byte-for-byte, and an unchanged
// spec stays a no-op so the chain does not fill with duplicates.
package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// cleanGrader stands in for git so these tests assert the registrar's
// behaviour rather than the state of whoever's working tree they run in.
func cleanGrader() (string, bool, error) {
	return "0123456789abcdef0123456789abcdef01234567", false, nil
}

// committedGrader mirrors what `git show <commit>:tools/accuracy_registry.py`
// returns for a commit that really CONTAINS the working-tree grader, so these
// tests exercise the registrar's joint-pin check instead of the history of
// whichever checkout they happen to run in.
func committedGrader(t *testing.T) func(string) (string, error) {
	t.Helper()
	d, err := (&PreregRegistrar{}).graderDigest()
	if err != nil {
		t.Fatalf("digest grader: %v", err)
	}
	return func(string) (string, error) { return d, nil }
}

func newPreregStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "prereg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestPreregRegistrarIsIdempotentWhenNothingChanged(t *testing.T) {
	ctx := context.Background()
	st := newPreregStore(t)
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
		GraderVersion: cleanGrader, GraderAtCommit: committedGrader(t)}

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := st.PreregRecords(ctx)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	// One record per claim spec, plus the filings-drift hypothesis, the grading
	// protocol, the protocol document (PREREGISTRATION.md), and the directional
	// auto-retire rule.
	if want := len(prereg.Specs()) + 4; len(first) != want {
		t.Fatalf("registered %d records, want %d", len(first), want)
	}
	var haveProtocol, haveDoc, haveDrift, haveRule bool
	for _, r := range first {
		if r.Kind == prereg.ProtocolKind {
			haveProtocol = true
		}
		if r.Kind == docKind {
			haveDoc = true
		}
		if r.Kind == FilingsDriftKind {
			haveDrift = true
		}
		if r.Kind == prereg.RetireRuleKind {
			haveRule = true
		}
	}
	if !haveDrift {
		t.Error("the filings-drift hypothesis was not registered — the research loop's " +
			"second orthogonal signal would be gated off forever (or, worse, graded unregistered)")
	}
	if !haveProtocol {
		t.Error("the grading protocol was not registered alongside the claims — the grader " +
			"could change before 2026-08-07 without leaving a trace in the chain")
	}
	if !haveDoc {
		t.Error("PREREGISTRATION.md was not registered alongside the claims — the prose " +
			"protocol could be rewritten before the 2026-08-14 grading without leaving a trace")
	}
	if !haveRule {
		t.Error("the auto-retire rule was not registered — the directional kill criterion " +
			"would stay revisable after the evidence arrives, the post-hoc failure " +
			"pre-registration exists to prevent")
	}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	again, _ := st.PreregRecords(ctx)
	if len(again) != len(first) {
		t.Errorf("second run appended %d duplicate record(s): %q", len(again)-len(first), msg)
	}
}

// A claim that changes in code must APPEND, and the original must survive
// untouched — an amendment is evidence of a correction, not a replacement.
func TestPreregAmendmentAppendsAndPreservesTheOriginal(t *testing.T) {
	ctx := context.Background()
	st := newPreregStore(t)
	now := int64(1_700_000_000)
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(now, 0) },
		GraderVersion: cleanGrader, GraderAtCommit: committedGrader(t)}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("register: %v", err)
	}

	before, _ := st.PreregRecords(ctx)
	var original prereg.Record
	for _, r := range before {
		if r.Kind == "trend21" {
			original = r
		}
	}
	if original.SpecHash == "" {
		t.Fatal("no trend21 record registered")
	}

	// Simulate the claim changing in code by appending a record whose hash
	// differs, exactly as the registrar would on a spec edit.
	amended := original
	amended.Ts = now + 86400
	amended.SpecHash = "deadbeef"
	amended.Note = "AMENDMENT — test"
	if _, err := st.AppendPrereg(ctx, amended); err != nil {
		t.Fatalf("append amendment: %v", err)
	}

	after, _ := st.PreregRecords(ctx)
	if len(after) != len(before)+1 {
		t.Fatalf("amendment did not append: %d records, want %d", len(after), len(before)+1)
	}
	// The original must be byte-identical — an amended chain that quietly
	// rewrote history would prove nothing at all.
	var stillThere bool
	for _, r := range after {
		if r.Seq == original.Seq {
			stillThere = true
			if r.SpecHash != original.SpecHash || r.EntryHash != original.EntryHash || r.Note != original.Note {
				t.Errorf("the original record was MUTATED by the amendment: %+v", r)
			}
		}
	}
	if !stillThere {
		t.Error("the original record disappeared when the claim was amended")
	}

	ok, brokenAt, err := st.VerifyPrereg(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Errorf("chain broken at seq %d after a legitimate amendment", brokenAt)
	}

	// And the newest hash per kind must now be the amendment, or the registrar
	// would append a second amendment on every subsequent pass.
	latest, err := st.LatestPreregHashes(ctx)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest["trend21"] != "deadbeef" {
		t.Errorf("latest trend21 hash = %q, want the amendment's", latest["trend21"])
	}
}

// The registrar must actually NOTICE a changed claim. This is the regression
// that would silently reintroduce the divergence.
func TestPreregRegistrarDetectsAChangedClaim(t *testing.T) {
	ctx := context.Background()
	st := newPreregStore(t)
	now := int64(1_700_000_000)
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(now, 0) },
		GraderVersion: cleanGrader, GraderAtCommit: committedGrader(t)}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Corrupt the stored hash for one kind, which is indistinguishable from the
	// code's spec having changed underneath it.
	rec, _ := st.PreregRecords(ctx)
	var trend prereg.Record
	for _, r := range rec {
		if r.Kind == "vol21" {
			trend = r
		}
	}
	trend.Ts = now + 1
	trend.SpecHash = "0000stale"
	trend.Note = "simulated drift"
	if _, err := st.AppendPrereg(ctx, trend); err != nil {
		t.Fatalf("append: %v", err)
	}

	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "AMENDMENT") {
		t.Errorf("registrar did not amend a drifted claim; said %q", msg)
	}
	latest, _ := st.LatestPreregHashes(ctx)
	want := ""
	for _, s := range prereg.Specs() {
		if s.Kind == "vol21" {
			want = s.Hash()
		}
	}
	if latest["vol21"] != want {
		t.Errorf("after amending, latest vol21 hash = %q, want the code's %q", latest["vol21"], want)
	}
}

// A grader with uncommitted changes must NOT be registered. Digesting a
// working-tree file pins a version that exists nowhere but one machine's disk,
// so no verifier — not even the operator later — could fetch the grader the
// chain claims decided the verdicts. Refusing is strictly restrictive: fewer
// records get written, and only ones that resolve to a real commit. The
// refusal is still visible, as a DQ event.
func TestDirtyGraderIsRefusedNotRegistered(t *testing.T) {
	ctx := context.Background()
	st := newPreregStore(t)
	w := &PreregRegistrar{
		St: st, Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
		GraderVersion: func() (string, bool, error) {
			return "0123456789abcdef0123456789abcdef01234567", true, nil
		},
	}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "grading protocol NOT registered") {
		t.Errorf("the refusal was not reported: %q", msg)
	}
	recs, err := st.PreregRecords(ctx)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	for _, r := range recs {
		if r.Kind == prereg.ProtocolKind {
			t.Fatal("a grading protocol was chained from a dirty working tree — the pinned " +
				"grader version is unreproducible by anyone, including its author")
		}
	}
	dq, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatalf("dq: %v", err)
	}
	var saw bool
	for _, e := range dq {
		if strings.Contains(e.Detail, "refused to register the grading protocol") {
			saw = true
		}
	}
	if !saw {
		t.Error("the refusal left no DQ event — a silent refusal is indistinguishable from " +
			"a registrar that never ran")
	}
}

// The chained protocol must carry the BLOCK gate, since PREREGISTRATION.md
// makes the chain authoritative wherever prose and chain disagree.
func TestChainedProtocolCarriesTheBlockGate(t *testing.T) {
	ctx := context.Background()
	st := newPreregStore(t)
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
		GraderVersion: cleanGrader, GraderAtCommit: committedGrader(t)}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	recs, err := st.PreregRecords(ctx)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	for _, r := range recs {
		if r.Kind != prereg.ProtocolKind {
			continue
		}
		var got prereg.Protocol
		if err := json.Unmarshal([]byte(r.SpecJSON), &got); err != nil {
			t.Fatalf("unmarshal protocol: %v", err)
		}
		if got.MinDistinctBlocks != 10 || got.ClusterUnit == "" {
			t.Errorf("chained protocol omits the block gate: %+v", got)
		}
		if len(got.GraderCommit) != 40 {
			t.Errorf("chained graderCommit %q is not a resolvable commit", got.GraderCommit)
		}
		return
	}
	t.Fatal("no grading-protocol record was chained")
}

// THE JOINT PIN. A protocol record names a commit AND a content digest, and
// PREREGISTRATION.md §2 tells a verifier to fetch the grader at that commit and
// check the digest. Until the two were tied together they were independent
// measurements — the live chain's seq 11 pins a commit whose grader hashes
// dafc5213 against a registered digest of 25cd8923, so the documented
// verification cannot be completed at all. A record whose commit does not
// contain its digest must therefore never reach the chain.
func TestCommitNotContainingTheDigestIsRefused(t *testing.T) {
	ctx := context.Background()
	st := newPreregStore(t)
	w := &PreregRegistrar{
		St: st, Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
		GraderVersion: cleanGrader,
		// The commit resolves and the tree is clean, but the grader AT that
		// commit is different bytes — exactly the live chain's condition.
		GraderAtCommit: func(string) (string, error) {
			return "dafc5213dafc5213dafc5213dafc5213dafc5213dafc5213dafc5213dafc5213", nil
		},
	}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "grading protocol NOT registered") {
		t.Errorf("the refusal was not reported: %q", msg)
	}
	recs, err := st.PreregRecords(ctx)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	for _, r := range recs {
		if r.Kind == prereg.ProtocolKind {
			t.Fatal("a grading protocol was chained whose pinned commit does not contain its " +
				"own digest — the record names a grader version that exists nowhere, so no " +
				"verifier can complete the procedure the chain advertises")
		}
	}
	dq, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatalf("dq: %v", err)
	}
	var saw bool
	for _, e := range dq {
		if strings.Contains(e.Detail, "does not exist at the pinned commit") {
			saw = true
		}
	}
	if !saw {
		t.Error("the mismatch left no DQ event — an unverifiable pin must be visible, not silent")
	}
}

// THE LOOK COUNTER. Every grade of the accuracy registry is another look at the
// same accruing rows, and the grader widens its published interval by the
// number of looks taken. So a look must be charged exactly once per GRADE —
// never per registrar wakeup, and never refundable.
func TestGradingLookIsChargedOncePerGradeAndNeverRefunded(t *testing.T) {
	ctx := context.Background()
	st := newPreregStore(t)
	reg := filepath.Join(t.TempDir(), "accuracy_registry.json")
	writeRegistry := func(stamp string) {
		if err := os.WriteFile(reg, []byte(`{"graded_at":"`+stamp+`"}`), 0o600); err != nil {
			t.Fatalf("write registry: %v", err)
		}
	}
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
		GraderVersion: cleanGrader, GraderAtCommit: committedGrader(t), RegistryPath: reg}

	looks := func() []prereg.Look {
		recs, err := st.PreregRecords(ctx)
		if err != nil {
			t.Fatalf("records: %v", err)
		}
		var out []prereg.Look
		for _, r := range recs {
			if r.Kind != prereg.LookKind {
				continue
			}
			var l prereg.Look
			if err := json.Unmarshal([]byte(r.SpecJSON), &l); err != nil {
				t.Fatalf("unmarshal look: %v", err)
			}
			out = append(out, l)
		}
		return out
	}

	writeRegistry("2026-08-07T06:00:00")
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := looks(); len(got) != 1 || got[0].Counter != 1 {
		t.Fatalf("first grade charged %+v, want exactly one look numbered 1", got)
	}
	// Same grade, another registrar wakeup: the look was already paid for.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := looks(); len(got) != 1 {
		t.Fatalf("a registrar wakeup with no new grade charged another look: %+v", got)
	}
	// A new grade over the same accruing rows is a new look.
	writeRegistry("2026-08-08T06:00:00")
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("third run: %v", err)
	}
	got := looks()
	if len(got) != 2 || got[1].Counter != 2 {
		t.Fatalf("a second grade charged %+v, want a second look numbered 2", got)
	}
	// MONOTONE. The counter each record carries is what makes a truncated chain
	// unable to hand a look back: even if only the newest record survived, the
	// count read off it is still 2.
	if got[1].Counter <= got[0].Counter {
		t.Error("the look counter did not increase — a chain read that came back short " +
			"could refund a look already taken, which is exactly what it must not do")
	}
}
