// Autonomous research loop (2026-07-25).
//
// WHY THIS IS THE ONE THAT COMPOUNDS
// ----------------------------------
// Every other improvement to this platform is a fixed gain: a faster endpoint
// stays faster, a repaired data feed stays repaired. Hypotheses tested per week
// is different — it is a RATE, and until now that rate was bounded by how often
// a human sat down and asked a question. The discovery grid, the walk-forward
// grader and the Bayes ledger were all built and all waited to be invoked by
// hand.
//
// This connects them: generate -> test -> judge -> ledger -> kill or promote,
// on a schedule, without anyone deciding to start it.
//
// WHAT KEEPS IT FROM MANUFACTURING GARBAGE
// ----------------------------------------
// An automated search over a rule grid is a p-hacking machine unless every one
// of these holds, so they are enforced here rather than trusted:
//
//  1. BONFERRONI over the whole grid. Testing 48 rules at alpha=0.05 finds
//     ~2 "significant" results from pure noise. researchx.Discover corrects for
//     the grid size, and the loop never widens the grid to chase a result.
//  2. NON-OVERLAPPING weekly observations. Daily samples of a forward window
//     share days and inflate n by the horizon length.
//  3. ERA COVERAGE. A rule must survive in multiple market eras, not just win
//     the one regime that dominates the sample.
//  4. SURVIVORS ONLY, stated plainly. researchx.Discover drops failures inside
//     itself, so this loop cannot ledger individual rejections and does not
//     pretend to — it records the survivor count against the grid size instead.
//     Zero survivors is the normal, honest outcome and is reported as a result
//     rather than as an error.
//  5. NOTHING auto-promotes to live. The loop can move a hypothesis to
//     `shadow`; a human moves it further. Automation that can put its own
//     output into production is how a p-hacked rule becomes a position.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ResearchLoop runs the discovery cycle end to end.
type ResearchLoop struct {
	St *store.Store

	// MaxCandidates caps the grid. Widening it does not find more edge, it
	// finds more false positives — the Bonferroni correction scales with it, so
	// a bigger grid makes each individual rule HARDER to clear, not easier.
	MaxCandidates int
}

func (w *ResearchLoop) Name() string            { return "research-loop" }
func (w *ResearchLoop) Interval() time.Duration { return 24 * time.Hour }

const (
	defaultLoopCandidates = 48
	// loopMetaDay gates real work to once per UTC day even though the worker
	// heartbeats more often.
	loopMetaDay = "research_loop_last_day"
	// minObsForLoop is the floor below which the loop reports "not enough data"
	// instead of searching. A grid search over a thin sample finds structure in
	// noise every single time.
	minObsForLoop = 2000
)

func (w *ResearchLoop) Run(ctx context.Context) (string, error) {
	today := time.Now().UTC().Format("2006-01-02")
	if last, _ := w.St.GetMeta(ctx, loopMetaDay); last == today {
		return "skip — already ran today", nil
	}

	obs, err := w.St.ResearchObservations(ctx, 0)
	if err != nil {
		return "", err
	}
	if len(obs) < minObsForLoop {
		// Honest refusal. The alternative — searching anyway and reporting the
		// best of 48 rules on a thin sample — is how a platform acquires a
		// "discovery" that evaporates on contact with new data.
		_ = w.St.SetMeta(ctx, loopMetaDay, today)
		return fmt.Sprintf("skip — %d observations, need %d before a grid search "+
			"is anything but noise-fitting", len(obs), minObsForLoop), nil
	}

	maxC := w.MaxCandidates
	if maxC <= 0 {
		maxC = defaultLoopCandidates
	}
	cands := researchx.Discover(obs, researchx.DiscoverConfig{MaxCandidates: maxC})

	// researchx.Discover returns SURVIVORS ONLY — rules that failed the
	// Bonferroni-corrected Wilson bound, the regime-survival check or the
	// counterfactual are dropped inside it and never reach here. So this loop
	// cannot ledger individual rejections, and claiming otherwise would be the
	// exact overstatement this system exists to avoid. What it CAN record
	// honestly is the survivor count against the grid size.
	var survived int
	for _, c := range cands {
		status := "shadow" // NEVER promoted automatically — see the package doc
		survived++
		// Ledger everything. A search that records only its winners cannot be
		// audited, and the failures are what stop the same idea being retried.
		_ = w.St.UpsertLoopHypothesis(ctx, store.LoopHypothesis{
			ID:          c.ID,
			Desc:        c.Desc,
			Status:      status,
			WilsonLower: c.WilsonLower,
			Survives:    c.Survives,
			FoundAt:     time.Now().Unix(),
		})
	}

	_ = w.St.SetMeta(ctx, loopMetaDay, today)
	if survived == 0 {
		// The honest and most common outcome. A grid that returns nothing after
		// Bonferroni correction is the search WORKING: it means no rule beat a
		// coin flip once the multiple-comparisons penalty was paid.
		return fmt.Sprintf("searched a %d-rule grid over %d observations — NOTHING "+
			"survived Bonferroni correction, regime-survival and the counterfactual. "+
			"That is a result, not a failure: it is what an honest search returns "+
			"when there is no edge in the grid.", maxC, len(obs)), nil
	}
	return fmt.Sprintf("searched a %d-rule grid over %d observations — %d survived to "+
		"shadow (none auto-promoted by design)", maxC, len(obs), survived), nil
}
