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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

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
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := st.PreregRecords(ctx)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(first) != len(prereg.Specs()) {
		t.Fatalf("registered %d records, want %d", len(first), len(prereg.Specs()))
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
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(now, 0) }}
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
	w := &PreregRegistrar{St: st, Now: func() time.Time { return time.Unix(now, 0) }}
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
