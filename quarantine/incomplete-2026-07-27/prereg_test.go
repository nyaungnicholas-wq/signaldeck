// The write-side half of the monotone-strictness rule: a grading-protocol
// record that drops a floor must be refused at APPEND time. Every reader
// already refuses such a record, but the chain is append-only, so a reader can
// only ever report a breach it cannot undo — which is how seq 18 took the
// graded surfaces offline permanently.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
)

func appendProtocol(t *testing.T, st *Store, ctx context.Context, ts int64, p prereg.Protocol, note string) error {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal protocol: %v", err)
	}
	_, err = st.AppendPrereg(ctx, prereg.Record{Ts: ts, Kind: prereg.ProtocolKind,
		SpecJSON: string(b), SpecHash: p.Hash(), Note: note})
	return err
}

func TestAppendPreregRefusesAWeakeningRecord(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	strict := prereg.GradingProtocol("deadbeef", "abc1234")
	if err := appendProtocol(t, st, ctx, 100, strict, "initial"); err != nil {
		t.Fatalf("append strict protocol: %v", err)
	}

	countRows := func() int {
		var n int
		if err := st.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM prereg_records`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	before := countRows()

	// The seq-18 shape: same grader digest, block floor and error budget gone.
	weaker := strict
	weaker.MinDistinctBlocks = 0
	weaker.MaxAlpha = 0
	err := appendProtocol(t, st, ctx, 200, weaker, "AMENDMENT")
	if !errors.Is(err, prereg.ErrProtocolWeakens) {
		t.Fatalf("append weakening = %v; want ErrProtocolWeakens", err)
	}
	if got := countRows(); got != before {
		t.Fatalf("chain grew on a refused append: %d -> %d", before, got)
	}

	// A record that is at least as strict in every registered dimension still
	// appends — the guard refuses loosening, not amendment.
	tighter := strict
	tighter.MinDistinctBlocks = strict.MinDistinctBlocks + 1
	if err := appendProtocol(t, st, ctx, 300, tighter, "AMENDMENT"); err != nil {
		t.Fatalf("append tightening: %v", err)
	}
	if got := countRows(); got != before+1 {
		t.Fatalf("tightening append did not land: %d -> %d", before, got)
	}
	if ws, err := st.PreregProtocolWeakenings(ctx); err != nil || len(ws) != 0 {
		t.Fatalf("chain weakenings = %v, %v; want none", ws, err)
	}
}
