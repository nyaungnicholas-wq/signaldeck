package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openPaperStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "paper.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestInitPaperBook_IdempotentInit(t *testing.T) {
	st := openPaperStore(t)
	ctx := context.Background()
	created, err := st.InitPaperBook(ctx, "flagship-1d", 100_000, 1000)
	if err != nil || !created {
		t.Fatalf("first init: created=%v err=%v", created, err)
	}
	// Second call must NOT recreate (INSERT OR IGNORE) and must not reset cash.
	created2, err := st.InitPaperBook(ctx, "flagship-1d", 999, 2000)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if created2 {
		t.Fatal("second init should be a no-op (already exists)")
	}
	cur, ok, err := st.PaperCursor(ctx, "flagship-1d")
	if err != nil || !ok {
		t.Fatalf("cursor: ok=%v err=%v", ok, err)
	}
	if cur.Cash != 100_000 {
		t.Fatalf("cash=%v want 100000 (re-init must not overwrite)", cur.Cash)
	}
	if cur.StartedTs != 1000 {
		t.Fatalf("startedTs=%v want 1000", cur.StartedTs)
	}
}

func TestApplyPaperStep_AdvancesAndPersists(t *testing.T) {
	st := openPaperStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", "stocks", "")
	if _, err := st.InitPaperBook(ctx, "flagship-1d", 100_000, 0); err != nil {
		t.Fatalf("init: %v", err)
	}

	apply := PaperApply{
		Strategy: "flagship-1d",
		BarTs:    86400,
		NewCash:  0,
		Opens: []PaperPosition{
			{Strategy: "flagship-1d", SymbolID: sym.ID, Qty: 500, AvgPx: 200, OpenedTs: 86400},
		},
		Trades: []PaperTrade{
			{Strategy: "flagship-1d", SymbolID: sym.ID, Side: "buy", Qty: 500, Px: 200, Cost: 75, Ts: 86400, Reason: "test"},
		},
		EquityTs: 86400, EquityCash: 0, EquityPositionsValue: 100_000, EquityValue: 100_000,
	}
	applied, err := st.ApplyPaperStep(ctx, apply)
	if err != nil || !applied {
		t.Fatalf("apply: applied=%v err=%v", applied, err)
	}
	// Cursor advanced.
	cur, _, _ := st.PaperCursor(ctx, "flagship-1d")
	if cur.LastBarTs != 86400 || cur.Cash != 0 {
		t.Fatalf("cursor after apply: %+v", cur)
	}
	// Position present.
	pos, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID)
	if !ok || pos.Qty != 500 {
		t.Fatalf("position: ok=%v qty=%v", ok, pos.Qty)
	}
	// Trade logged.
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 1 || trades[0].Side != "buy" {
		t.Fatalf("trades: %+v", trades)
	}
	// Equity marked.
	curve, _ := st.PaperEquityCurve(ctx, "flagship-1d", 10)
	if len(curve) != 1 || curve[0].Equity != 100_000 {
		t.Fatalf("equity curve: %+v", curve)
	}
}

// The transactional replay guard: applying a bar at/behind the cursor is a
// no-op and does NOT append trades or mark equity again.
func TestApplyPaperStep_ReplayGuard(t *testing.T) {
	st := openPaperStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", "stocks", "")
	_, _ = st.InitPaperBook(ctx, "flagship-1d", 100_000, 0)

	first := PaperApply{
		Strategy: "flagship-1d", BarTs: 200000, NewCash: 500,
		Trades:   []PaperTrade{{Strategy: "flagship-1d", SymbolID: sym.ID, Side: "buy", Qty: 1, Px: 10, Cost: 0, Ts: 200000}},
		EquityTs: 200000, EquityValue: 100000,
	}
	if applied, err := st.ApplyPaperStep(ctx, first); err != nil || !applied {
		t.Fatalf("first apply: applied=%v err=%v", applied, err)
	}

	// Replay the SAME bar ts — must be a no-op.
	replay := first
	replay.NewCash = 999999 // if it wrongly applied, cash would change
	replay.Trades = []PaperTrade{{Strategy: "flagship-1d", SymbolID: sym.ID, Side: "buy", Qty: 99, Px: 10, Ts: 200000}}
	applied, err := st.ApplyPaperStep(ctx, replay)
	if err != nil {
		t.Fatalf("replay err: %v", err)
	}
	if applied {
		t.Fatal("replay of same bar must NOT apply")
	}
	// An OLDER bar must also be rejected.
	older := first
	older.BarTs = 100000
	if applied, _ := st.ApplyPaperStep(ctx, older); applied {
		t.Fatal("older bar must NOT apply")
	}

	// State unchanged: exactly one trade, cursor cash still 500.
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 1 {
		t.Fatalf("replay double-traded: %d trades", len(trades))
	}
	cur, _, _ := st.PaperCursor(ctx, "flagship-1d")
	if cur.Cash != 500 || cur.LastBarTs != 200000 {
		t.Fatalf("cursor mutated by replay: %+v", cur)
	}
}

func TestApplyPaperStep_ClosePosition(t *testing.T) {
	st := openPaperStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", "stocks", "")
	_, _ = st.InitPaperBook(ctx, "flagship-1d", 100_000, 0)

	// Open.
	_, _ = st.ApplyPaperStep(ctx, PaperApply{
		Strategy: "flagship-1d", BarTs: 1, NewCash: 0,
		Opens:    []PaperPosition{{Strategy: "flagship-1d", SymbolID: sym.ID, Qty: 10, AvgPx: 100, OpenedTs: 1}},
		EquityTs: 1, EquityValue: 100000,
	})
	// Close.
	applied, err := st.ApplyPaperStep(ctx, PaperApply{
		Strategy: "flagship-1d", BarTs: 2, NewCash: 101000,
		CloseSymbolIDs: []int64{sym.ID},
		Trades:         []PaperTrade{{Strategy: "flagship-1d", SymbolID: sym.ID, Side: "sell", Qty: 10, Px: 110, Cost: 5, Ts: 2}},
		EquityTs:       2, EquityCash: 101000, EquityValue: 101000,
	})
	if err != nil || !applied {
		t.Fatalf("close apply: applied=%v err=%v", applied, err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("position should be gone after close")
	}
	positions, _ := st.PaperPositions(ctx, "flagship-1d")
	if len(positions) != 0 {
		t.Fatalf("expected flat, got %d positions", len(positions))
	}
}

// The decision ledger must ride the book's transaction, not precede it.
//
// ev_decisions rows used to be written directly by the worker as it assembled
// the step, before ApplyPaperStep was even called. Two holes followed. An apply
// that failed — or merely returned an error — left the rows behind describing a
// step that never happened; and because a failed apply leaves the cursor
// unadvanced, the next pass re-rendered the identical bar and appended a SECOND
// full set. ev_decisions has no unique key and nothing reconciles it against
// paper_trades, so neither orphans nor duplicates were detectable afterwards.
func TestApplyPaperStep_ReplayWritesNoLedgerRows(t *testing.T) {
	st := openPaperStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", "stocks", "")
	_, _ = st.InitPaperBook(ctx, "flagship-1d", 100_000, 0)

	step := PaperApply{
		Strategy: "flagship-1d", BarTs: 200000, NewCash: 500,
		Trades:   []PaperTrade{{Strategy: "flagship-1d", SymbolID: sym.ID, Side: "buy", Qty: 1, Px: 10, Ts: 200000}},
		Decisions: []EVDecision{{
			Ts: 200000, Strategy: "flagship-1d", SymbolID: sym.ID, Symbol: "AAA",
			Horizon: "1d", Decision: "BUY", Reason: "positive-net-ev", InputsJSON: "{}",
		}},
		EquityTs: 200000, EquityValue: 100000,
	}
	if applied, err := st.ApplyPaperStep(ctx, step); err != nil || !applied {
		t.Fatalf("first apply: applied=%v err=%v", applied, err)
	}
	rows, err := st.EVDecisions(ctx, "", "", 100)
	if err != nil {
		t.Fatalf("read decisions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("after one applied step: %d ledger row(s), want 1", len(rows))
	}

	// Re-render the SAME bar, as the worker does when a previous apply failed and
	// left the cursor unadvanced. The guard rejects it, so nothing may be logged.
	applied, err := st.ApplyPaperStep(ctx, step)
	if err != nil {
		t.Fatalf("replay err: %v", err)
	}
	if applied {
		t.Fatal("fixture broken: the replay guard should have rejected this bar")
	}
	rows, err = st.EVDecisions(ctx, "", "", 100)
	if err != nil {
		t.Fatalf("read decisions after replay: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("a REJECTED step wrote %d ledger row(s), want the original 1 — "+
			"the ledger is recording decisions for a step the book never applied", len(rows))
	}
}
