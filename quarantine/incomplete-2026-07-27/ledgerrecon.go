// Ledger reconciliation — the hole the hash chain cannot see.
//
// /api/ledger reports intact=true and a head hash. Both are statements about
// the rows that ARE in the chain. A prediction whose ledger append failed
// leaves no row at all, and the chain over everything else still verifies
// perfectly — so a reader who takes "intact" to mean "every prediction is
// committed" is reading a claim the chain does not make. On this database the
// difference is 21 predictions.
//
// This worker measures that difference on a cadence, records a dq_event
// whenever the count MOVES (a growing hole is a live append failure, which is
// operationally different from a historical one that stopped growing), and
// publishes the standing count beside the head so the disclosure travels with
// the claim.
//
// IT DOES NOT BACKFILL, and that refusal is the design, not a limitation. A
// ledger entry appended today for a prediction made in April would sit at a
// seq between two entries from today, hash cleanly, and assert an ordering
// that never happened. The chain's entire value is ordering; an entry in the
// wrong position is worth less than no entry, because the missing entry is
// visibly missing while the misplaced one is not. A disclosed hole is
// therefore strictly more honest than a repaired chain.
package pipeline

import (
	"context"
	"fmt"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// LedgerReconWorker measures and publishes the prediction-ledger gap.
type LedgerReconWorker struct {
	St  *store.Store
	Now func() time.Time // injectable clock; nil ⇒ time.Now
}

func (w *LedgerReconWorker) Name() string            { return "ledger-recon" }
func (w *LedgerReconWorker) Interval() time.Duration { return 6 * time.Hour }

func (w *LedgerReconWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// ledgerGapDQKind is raised only on a CHANGE. A standing hole that is already
// published is not news every six hours; a hole that grew is.
const ledgerGapDQKind = "ledger_gap_moved"

func (w *LedgerReconWorker) Run(ctx context.Context) (string, error) {
	rep, err := w.St.MeasureLedgerGap(ctx)
	if err != nil {
		return "", fmt.Errorf("measure ledger gap: %w", err)
	}
	rep.CheckedAt = w.now().Unix()

	prev, had, err := w.St.StoredLedgerGap(ctx)
	if err != nil {
		return "", fmt.Errorf("read stored ledger gap: %w", err)
	}
	if had && prev.Missing != rep.Missing {
		detail := fmt.Sprintf("predictions with no ledger entry moved %d → %d "+
			"(chain genesis bar %d, %d predictions in scope); NOT backfilled — a late "+
			"append would place a prediction at the wrong position in the chain",
			prev.Missing, rep.Missing, rep.GenesisBarTs, rep.InScope)
		if err := w.St.InsertDQ(ctx, md.DQEvent{
			Ts: rep.CheckedAt, Kind: ledgerGapDQKind, Detail: detail,
		}); err != nil {
			return "", fmt.Errorf("record gap movement: %w", err)
		}
	}
	if err := w.St.SetJSON(ctx, store.LedgerGapMetaKey, rep); err != nil {
		return "", fmt.Errorf("publish ledger gap: %w", err)
	}
	return fmt.Sprintf("%d of %d predictions at/after chain genesis have no ledger entry "+
		"(published, never backfilled)", rep.Missing, rep.InScope), nil
}
