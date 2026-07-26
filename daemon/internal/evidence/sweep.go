// Staleness sweep — the mechanism that makes revalidate_by a real promise.
//
// Rules, applied to every non-retired claim:
//
//  1. Refuting evidence retires the claim (tier=refuted, status=retired),
//     regardless of dates. Refutation does not expire.
//  2. A claim past its revalidate_by is marked stale AND loses one tier —
//     unmaintained certainty decays instead of persisting. The sweep is
//     idempotent per tier step: an already-stale claim is not re-downgraded
//     on every pass (one tier per missed deadline, not one per night); a
//     claim re-validated (fresh last_validated + future revalidate_by via
//     Put) returns to active on its own because Put replaces the row.
package evidence

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// SweepResult summarizes one pass, for worker detail lines and tests.
type SweepResult struct {
	Checked int
	Staled  int
	Retired int
}

// Sweep applies the staleness/refutation rules once. Fail-quiet per claim:
// a malformed row is skipped, never fabricated around.
func Sweep(ctx context.Context, st *store.Store, now time.Time) (SweepResult, error) {
	var res SweepResult
	claims, err := List(ctx, st, "", "")
	if err != nil {
		return res, err
	}
	for _, c := range claims {
		if c.Status == StatusRetired {
			continue
		}
		res.Checked++
		if JustifiedTier(c.Items) == TierRefuted {
			if err := st.SetEvidenceClaimState(ctx, c.ID, string(StatusRetired), string(TierRefuted)); err != nil {
				return res, err
			}
			res.Retired++
			continue
		}
		if c.Status == StatusActive && now.Unix() > c.RevalidateBy {
			if err := st.SetEvidenceClaimState(ctx, c.ID, string(StatusStale), string(Downgrade(c.Tier))); err != nil {
				return res, err
			}
			res.Staled++
		}
	}
	return res, nil
}

// SweepRunner is the in-app agent wrapper (workers.Worker). Its first act on
// every run is EnsureSeeds, so wiring the runner is the only integration a
// deployment needs; seeding is idempotent and never overwrites a swept row.
type SweepRunner struct {
	St *store.Store
}

func (w *SweepRunner) Name() string { return "evidence-staleness-runner" }

// Interval is 24h — staleness is measured in days, and a nightly cadence
// matches the honesty-gap runners this sits beside.
func (w *SweepRunner) Interval() time.Duration { return 24 * time.Hour }

func (w *SweepRunner) Run(ctx context.Context) (string, error) {
	seeded, err := EnsureSeeds(ctx, w.St)
	if err != nil {
		return "", fmt.Errorf("seed: %w", err)
	}
	res, err := Sweep(ctx, w.St, time.Now())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("checked %d claims: %d went stale (tier -1), %d retired on refuting evidence, %d seeded",
		res.Checked, res.Staled, res.Retired, seeded), nil
}
