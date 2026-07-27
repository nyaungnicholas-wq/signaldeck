package api

import (
	"net/http"
	"time"
)

// researchLoopMinObs mirrors pipeline.minObsForLoop — the corpus floor below
// which the loop declines to search. Duplicated rather than imported because
// api must not depend on pipeline; a drift between them only changes when
// silence is called unhealthy, never any judged result.
const researchLoopMinObs = 2000

// researchLoopLedger serves the autonomous research loop's own record: every
// pass (including the ones that refused to search), the current-state view of
// each rule it has judged, the append-only per-(day, rule) judgment history,
// and the tally of which gate did the killing.
//
// It exists because an engine whose only output is two tables nobody reads
// cannot be audited by anyone, including its author: research_loop_runs and
// research_loop_hypotheses sat empty through an entire review round precisely
// because nothing surfaced them. The rejection tally is the load-bearing part —
// "nothing survived" is only evidence if you can see how many rules were judged
// and where they died.
//
// Read-only, and deliberately so: nothing here can start a search or change a
// verdict.
func (d Deps) researchLoop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runs, err := d.St.LoopRuns(ctx, 200)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	hyps, err := d.St.LoopHypotheses(ctx, 200)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	judgments, err := d.St.LoopJudgments(ctx, 500)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	byGate, err := d.St.LoopRejectionsByGate(ctx)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Survivors are keyed under "" in the gate map (no gate killed them); lift
	// them out so a reader never mistakes the empty key for an unnamed gate.
	survivors := byGate[""]
	delete(byGate, "")

	searched := 0
	for _, run := range runs {
		if run.GridSize > 0 {
			searched++
		}
	}

	// Engine liveness is a SEPARATE verdict from any rule's. "Searched and found
	// nothing" is the corrected grid working; "no judgment recorded since X" on
	// a corpus far above the search floor means no search happened at all and
	// neither survivors nor kills were priced. Reported as its own field so a
	// reader can never mistake the second for the first.
	health, err := d.St.LoopEngineHealth(ctx, researchLoopMinObs, time.Now())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"engineHealth":       health,
		"runs":               runs,
		"hypotheses":         hyps,
		"judgments":          judgments,
		"rejectionsByGate":   byGate,
		"survivingJudgments": survivors,
		"passes":             len(runs),
		"searchedPasses":     searched,
		"discipline":         "research_loop_hypotheses is a CURRENT-STATE view keyed on rule id — the same rule judged on 200 nights collapses to one row. research_loop_judgments is the append-only record, one row per (day, rule), so 'this rule has been judged and killed N times' is a COUNT(*) rather than an assertion. The Bonferroni divisor is grid size × every search ever taken, and that search count is read from these append-only tables (max'd with the pruned worker_runs seed and the meta counter) so log retention cannot refund multiplicity: the divisor is monotone by construction and can only rise. Zero survivors is the normal, honest outcome of a corrected grid search.",
	})
}
