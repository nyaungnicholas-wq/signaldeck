// Tests for the pre-registration chain persistence: strictly-linear appends,
// the registrar's kind/hash reads, and VerifyPrereg catching an after-the-fact
// edit — the one thing the chain exists to make impossible to do quietly.
package store

import (
	"context"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
)

func TestPreregChain_AppendVerifyAndTamper(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Empty chain verifies clean.
	if ok, at, err := st.VerifyPrereg(ctx); err != nil || !ok || at != 0 {
		t.Fatalf("empty verify = %v, %d, %v; want ok", ok, at, err)
	}

	r1, err := st.AppendPrereg(ctx, prereg.Record{Ts: 100, Kind: "trend21",
		SpecJSON: `{"claim":"a"}`, SpecHash: "h-a", Note: "initial"})
	if err != nil {
		t.Fatalf("append genesis: %v", err)
	}
	if r1.Seq == 0 || r1.PrevHash != "" || r1.EntryHash == "" {
		t.Fatalf("genesis record wrong: %+v", r1)
	}
	r2, err := st.AppendPrereg(ctx, prereg.Record{Ts: 200, Kind: "vol21",
		SpecJSON: `{"claim":"b"}`, SpecHash: "h-b", Note: "initial"})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if r2.PrevHash != r1.EntryHash {
		t.Fatalf("chain not linear: prev=%q want %q", r2.PrevHash, r1.EntryHash)
	}
	// An amendment appends — the kind's newest hash moves, history stays.
	if _, err := st.AppendPrereg(ctx, prereg.Record{Ts: 300, Kind: "trend21",
		SpecJSON: `{"claim":"a2"}`, SpecHash: "h-a2", Note: "AMENDMENT"}); err != nil {
		t.Fatalf("append amendment: %v", err)
	}

	recs, err := st.PreregRecords(ctx)
	if err != nil || len(recs) != 3 || recs[0].Seq >= recs[1].Seq {
		t.Fatalf("PreregRecords = %d rows, %v; want 3 oldest-first", len(recs), err)
	}
	kinds, err := st.PreregKinds(ctx)
	if err != nil || len(kinds) != 2 || !kinds["trend21"] || !kinds["vol21"] {
		t.Fatalf("PreregKinds = %v, %v", kinds, err)
	}
	hashes, err := st.LatestPreregHashes(ctx)
	if err != nil || hashes["trend21"] != "h-a2" || hashes["vol21"] != "h-b" {
		t.Fatalf("LatestPreregHashes = %v, %v; want amendment hash for trend21", hashes, err)
	}

	if ok, at, err := st.VerifyPrereg(ctx); err != nil || !ok {
		t.Fatalf("verify intact chain = %v, %d, %v; want ok", ok, at, err)
	}

	// Quietly rewrite a registered claim's hash — verification must name the
	// row (spec_hash is what HashEntry commits to).
	if _, err := st.w.ExecContext(ctx,
		`UPDATE prereg_records SET spec_hash='h-rewritten' WHERE seq=?`,
		r2.Seq); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	ok, at, err := st.VerifyPrereg(ctx)
	if err != nil || ok || at != r2.Seq {
		t.Fatalf("tampered verify = %v, %d, %v; want broken at seq %d", ok, at, err, r2.Seq)
	}
}
