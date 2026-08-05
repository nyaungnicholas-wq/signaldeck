package pipeline

// Pipeline-level proof of P4B and the kill switch: risk is an ADMISSION GATE
// that runs before the EV go/no-go, not a sizer applied after one, and the
// file-based kill switch actually halts this repository's only executor.
//
// The scenario in every test here is deliberately one the EV engine WANTS to
// trade — TestEVGate_LedgersBuyAndExit proves the identical seed fills — so a
// non-fill can only be the control under test and never a weak candidate.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/killswitch"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedTradeable plants the candidate the EV engine buys on sight.
func seedTradeable(t *testing.T, st *store.Store, ctx context.Context, symbol string) int64 {
	t.Helper()
	sym, _ := st.UpsertSymbol(ctx, symbol, md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)
	return sym.ID
}

// HALT SIMULATION, end to end. The switch is tripped, the worker runs, and the
// entry the EV engine would otherwise have bought does not happen — and the
// refusal is in the ledger with its enumerated reason.
func TestKillSwitch_HaltsEntriesAndLedgersTheRefusal(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	symID := seedTradeable(t, st, ctx, "AAA")

	halt := filepath.Join(t.TempDir(), "HALT")
	if err := os.WriteFile(halt, []byte("halt simulation"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(killswitch.EnvPath, halt)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", symID); ok {
		t.Fatal("the kill switch was tripped and an entry filled anyway")
	}
	if trades, _ := st.PaperTrades(ctx, "flagship-1d", 10); len(trades) != 0 {
		t.Fatalf("want no trades under a halt, got %+v", trades)
	}

	decs, err := st.EVDecisions(ctx, "DO_NOTHING", "AAA", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decs) == 0 {
		t.Fatal("the halt refusal was not ledgered — a refusal nobody can query is not auditable")
	}
	if decs[0].Reason != "kill-switch-halted" {
		t.Fatalf("reason=%q want kill-switch-halted", decs[0].Reason)
	}
	if !strings.Contains(decs[0].InputsJSON, `"halted":true`) {
		t.Fatalf("the halt state must be snapshotted into the ledger, got %s", decs[0].InputsJSON)
	}

	// CLEARING IT RESUMES. A halt that cannot be lifted without a restart is an
	// outage, not a control. The worker's cursor has already consumed the bar
	// the halted pass looked at, so the resume needs a fresh one — the same
	// no-op-on-a-re-run guard every pass has.
	if err := os.Remove(halt); err != nil {
		t.Fatal(err)
	}
	seedDailyPx(t, st, symID, [][3]float64{{4, 100, 100}, {5, 100, 100}})
	seedPrediction(t, st, symID, md.H1d, 4*86400, 0.90)
	seedGoodForecast(t, st, symID, md.H1d, 4*86400)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", symID); !ok {
		t.Fatal("clearing the halt did not resume trading")
	}
}

// FAIL CLOSED at the pipeline boundary, not just inside the package: a halt
// instruction the switch cannot parse still halts the worker. (The unreadable-
// PATH branch is covered in killswitch's own tests — a NUL byte cannot be put
// through t.Setenv.)
func TestKillSwitch_UnparseableHaltInstructionHaltsTheWorker(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	symID := seedTradeable(t, st, ctx, "BBB")

	t.Setenv(killswitch.EnvHalt, "banana")

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", symID); ok {
		t.Fatal("an unparseable halt instruction permitted an entry — the control failed OPEN")
	}
}

// EXITS SURVIVE A HALT. This is the property that stops the kill switch from
// being a bigger risk than the one it controls: a halt must never trap the book
// inside the position it was tripped by.
func TestKillSwitch_ExitsStillExecuteWhileHalted(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	symID := seedTradeable(t, st, ctx, "CCC")

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", symID); !ok {
		t.Fatal("setup failed: the entry did not fill before the halt")
	}

	// Now halt the platform AND flip the signal flat.
	halt := filepath.Join(t.TempDir(), "HALT")
	if err := os.WriteFile(halt, []byte("halt with an open position"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(killswitch.EnvPath, halt)
	seedDailyPx(t, st, symID, [][3]float64{{4, 100, 100}, {5, 100, 100}})
	seedPrediction(t, st, symID, md.H1d, 4*86400, 0.10)

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", symID); ok {
		t.Fatal("the halt blocked an EXIT — the book is trapped in the position the halt was tripped by")
	}
	// The exit is still ledgered. Its reason is whichever control closed the
	// position — with barriers in force on a 1-bar horizon that is the expiry
	// barrier, which makes this the stronger case: a RISK CONTROL fired while
	// the platform was halted, and the halt did not stop it.
	sells, _ := st.EVDecisions(ctx, "SELL", "CCC", 10)
	if len(sells) != 1 {
		t.Fatalf("the exit must still be ledgered, got %+v", sells)
	}
	switch sells[0].Reason {
	case "exit-never-blocked", "barrier-expiry", "barrier-adverse", "barrier-favorable":
	default:
		t.Fatalf("unexpected exit reason %q", sells[0].Reason)
	}
}

// P4B: the risk gate can REJECT, and it rejects BEFORE the EV verdict is
// rendered. A book already at its position cap admits nothing, so the
// tradeable candidate is refused with the risk reason — not with an EV reason,
// which would mean EV had decided first and risk had merely sized after.
func TestRiskGate_RejectsBeforeEVDecides(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	symID := seedTradeable(t, st, ctx, "DDD")

	// Squeeze the envelope so the book is closed to new risk: a zero-name cap
	// is the smallest change that makes riskgate.Admit refuse for certain.
	t.Setenv("SIGNALDECK_RISK_MAX_GROSS_EXPOSURE", "0.000001")

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", symID); ok {
		t.Fatal("a candidate the risk gate refused was traded anyway")
	}
	decs, err := st.EVDecisions(ctx, "DO_NOTHING", "DDD", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decs) == 0 {
		t.Fatal("the risk refusal was not ledgered")
	}
	if decs[0].Reason != "risk-gate-refused" {
		t.Fatalf("reason=%q want risk-gate-refused — an EV reason here would mean EV decided first", decs[0].Reason)
	}
	// The riskgate.Decision rides along, so an operator can see WHICH limit
	// fired without re-deriving it.
	if !strings.Contains(decs[0].InputsJSON, `"risk"`) || !strings.Contains(decs[0].InputsJSON, "breaches") {
		t.Fatalf("the refusal must snapshot which limit fired, got %s", decs[0].InputsJSON)
	}
}
