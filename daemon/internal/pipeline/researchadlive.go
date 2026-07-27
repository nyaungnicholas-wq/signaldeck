// ═══ AD-* LIVE REPLICATION (#17, appended wave) ══════════════════════════════
//
// Known gap recorded in the research-engine wave: auto-discovered hypotheses
// (AD-* ids, machine-readable spec stored on the Hypothesis row) received ONE
// capped in-sample experiment at discovery and then could never accrue
// post-discovery evidence — the era grader's live-window guard skips every
// era once an experiment window covers the full history, and the live ledger
// grader has no spec registry.
//
// This step closes the loop: for every AD-* hypothesis with a parseable spec
// and a status other than rejected, when NEW research_weeks rows exist beyond
// the hypothesis's last evidence window (max window_to over all graded kinds,
// plus the standard two-week clearance so forward windows never straddle the
// boundary), the spec is graded on ONLY those new disjoint weeks — the exact
// researchx.GradeWeeks week-trial discipline and the exact attack battery the
// era grader runs — and the evidence (KindReplication: a fresh-window grade
// is the real test discovery could never be) is appended and the posterior
// recomputed. Capped at adMaxGradesPerPass per pass so one tick can never
// flood the ledger.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
)

// adMaxGradesPerPass caps how many AD-* hypotheses accrue a fresh-window grade
// in one engine pass.
const adMaxGradesPerPass = 4

// adHypPrefix marks auto-discovered hypotheses (researchx.Discover ids).
const adHypPrefix = "AD-"

// adReplicate grades spec-carrying AD-* hypotheses on research_weeks data
// strictly beyond their last evidence window. Returns how many grades landed.
// Per-hypothesis failures are dq events, never a pass failure.
func (w *ResearchEngineWorker) adReplicate(ctx context.Context, obs []researchx.Obs, now int64, minWeekObs, minWeeksPerEra int) int {
	hyps, err := w.St.LedgerHypotheses(ctx)
	if err != nil {
		w.dq(ctx, researchEngineDQKind, now, "ad-replicate: load hypotheses: "+err.Error())
		return 0
	}
	sort.Slice(hyps, func(i, j int) bool { return hyps[i].ID < hyps[j].ID })
	graded := 0
	for _, hyp := range hyps {
		if graded >= adMaxGradesPerPass {
			break
		}
		if !strings.HasPrefix(hyp.ID, adHypPrefix) || hyp.Spec == "" || hyp.Status == rl.StatusRejected {
			continue
		}
		var rule researchx.Rule
		if err := json.Unmarshal([]byte(hyp.Spec), &rule); err != nil || rule.Call == "" {
			continue // an unintelligible spec grades nothing, never wrong
		}
		did, err := w.adReplicateOne(ctx, hyp, rule, obs, now, minWeekObs, minWeeksPerEra)
		if err != nil {
			w.dq(ctx, researchEngineDQKind, now, fmt.Sprintf("ad-replicate %s: %v", hyp.ID, err))
			continue
		}
		if did {
			graded++
		}
	}
	return graded
}

// adReplicateOne grades one AD hypothesis on the obs strictly beyond its
// window cursor. did=false is the honest "not enough new weeks yet".
func (w *ResearchEngineWorker) adReplicateOne(ctx context.Context, hyp rl.Hypothesis, rule researchx.Rule, obs []researchx.Obs, now int64, minWeekObs, minWeeksPerEra int) (bool, error) {
	cursor, err := w.St.LedgerEvidenceMaxWindow(ctx, hyp.ID)
	if err != nil {
		return false, err
	}
	if cursor == 0 {
		return false, nil // never graded: discovery owns the first (capped) grade
	}
	// Two-week clearance mirrors the live grader: the discovery window's 1w
	// forward outcomes can extend past its nominal boundary, so new obs must
	// start clear of it — sequential updates on DISJOINT samples only.
	boundary := cursor + 2*histfeat.WeekSecs
	var fresh []researchx.Obs
	for _, o := range obs {
		if o.Ts >= boundary {
			fresh = append(fresh, o)
		}
	}
	if len(fresh) == 0 {
		return false, nil
	}
	g := researchx.GradeWeeks(fresh, rule, minWeekObs)
	if g.Weeks < minWeeksPerEra {
		return false, nil // too thin to acquit OR convict — no row
	}
	// Null MEASURED on the same matched observations (direction randomized),
	// not assumed to be 0.5 — the week-trial no-skill rate is not 0.5 because
	// a week is won only by beating its own folded majority. If the null arm
	// grades no weeks there is no null, and therefore no grade: write nothing.
	null, ok := measuredWeekNull(fresh, rule, minWeekObs, minWeeksPerEra)
	if !ok {
		return false, nil
	}
	bf, ok := rl.BayesFactorAbove(g.WinWeeks, g.Weeks, null, rl.WeekTrialMaxEdge)
	if !ok {
		return false, nil
	}
	winFrom, winTo := obsSpan(fresh)
	evidence := engineGradeAttacks(hyp.ID, rule, fresh, g, "live-fresh", now, winFrom, winTo,
		minWeekObs, minWeeksPerEra)
	evidence = append(evidence, rl.Evidence{
		HypID: hyp.ID, Ts: now, Kind: rl.KindReplication,
		K: g.WinWeeks, N: g.Weeks, P0: null.P0(), BF: bf,
		Note: fmt.Sprintf("post-discovery fresh-window replication: %d/%d winning weeks (%d obs) on data strictly after the discovery window — the out-of-sample test the capped in-sample experiment could not be; graded against measured null %s",
			g.WinWeeks, g.Weeks, g.TotalObs, null),
		WindowFrom: winFrom, WindowTo: winTo,
	})
	// Attacks precede the grade row (same crash discipline as gradeHypEras):
	// an interrupted write leaves the cursor unadvanced and orphans biasing
	// conservative.
	for _, e := range evidence {
		if err := w.St.InsertLedgerEvidence(ctx, e); err != nil {
			return false, err
		}
	}
	regimes := hyp.Regimes
	if g.HighVolObs >= minHighVolObs && regimes < 2 {
		regimes = 2
	}
	return true, w.recomputeAndDecay(ctx, hyp, regimes, now)
}
