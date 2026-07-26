// Research Lab — research-lab worker (nightly).
//
// This is the automated research loop the postmortem engine feeds:
//
//	postmortem clusters ─┐
//	labeled feature store ┴─▶ generate hypotheses ─▶ walk-forward OOS grade each
//	  ─▶ Bonferroni-corrected Wilson test (reject most) ─▶ survivors to SHADOW
//	  ─▶ re-evaluate every shadow on fresh data ─▶ promote only on a sustained
//	     streak of OOS wins ─▶ emit an ADVISORY feedback signal for adaptive weights.
//
// DISCIPLINE (why this is not a p-hacking machine):
//   - Grading is strict expanding-window walk-forward (internal/gbm.Evaluate).
//   - Survival requires beating the incumbent baseline by a Wilson lower bound
//     that is Bonferroni-corrected by EVERY look the lab has taken — tonight's
//     batch plus a monotone count of all prior nights' gradings. A wider search
//     makes each bar HIGHER, and so does asking the same question again, because
//     each re-look is another chance for noise to clear it.
//   - The significance level is a constant (researchlab.MaxNominalAlpha) and the
//     divisor is derived from counts of tests conducted. Neither is a worker
//     field: a p-hacking guardrail an operator can widen is not a guardrail.
//   - One night's win only earns SHADOW. Promotion needs promoteStreak consecutive
//     wins re-measured on data that keeps arriving — the guard a spurious subset
//     cannot fake. Repeated failure rejects it.
//   - Nothing here mutates live predictions. Promotion emits an advisory meta
//     signal (research_feedback) that adaptive-weight adoption stays gated on.
//
// Honest empty state: with too little labeled data to grade, the worker records
// "NO EDGE DETECTED — insufficient data" and does nothing else. That is the
// correct output, not a failure.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchlab"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ResearchLabWorker runs the nightly hypothesis loop.
type ResearchLabWorker struct {
	St  *store.Store
	Now func() time.Time // injectable clock; nil ⇒ time.Now

	// tunables (defaults set by NewResearchLabWorker)
	//
	// The significance level is deliberately NOT among them: it is
	// researchlab.MaxNominalAlpha, a constant, and the Bonferroni divisor is
	// derived from tests actually conducted (researchlab.Multiplicity). A
	// guardrail an operator can widen is not a guardrail.
	MaxHyps       int
	PromoteStreak int
	RejectStreak  int
	MinRows       int
	OncePerDay    bool // gate to one real run per UTC day via meta cursor
}

// NewResearchLabWorker builds the worker with disciplined defaults.
func NewResearchLabWorker(st *store.Store) *ResearchLabWorker {
	return &ResearchLabWorker{
		St: st, MaxHyps: 24,
		PromoteStreak: 5, RejectStreak: 3, MinRows: 80, OncePerDay: true,
	}
}

func (w *ResearchLabWorker) Name() string            { return "research-lab" }
func (w *ResearchLabWorker) Interval() time.Duration { return 6 * time.Hour } // heartbeat; OncePerDay gates real work

func (w *ResearchLabWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

const researchLabDayKey = "research_lab_last_day"

// researchLabTestsKey holds the CUMULATIVE count of hypothesis-gradings the lab
// has ever conducted. It is the PriorTests term of the Bonferroni divisor.
//
// It lives in meta rather than being recomputed from the hypothesis table on
// purpose: rows leave that table when they are promoted or rejected, and a
// divisor that forgets the tests behind those rows is exactly the shrinking-pool
// bug — the bar would drop every time a hypothesis was killed off. A monotone
// counter cannot forget.
const researchLabTestsKey = "research_lab_tests_total"

// labTests reads the cumulative grading count; a missing or corrupt value reads
// as 0, which is the FIRST-NIGHT claim and therefore the loosest correction —
// so the write below is the thing that must not be skipped.
func (w *ResearchLabWorker) labTests(ctx context.Context) int {
	v, _ := w.St.GetMeta(ctx, researchLabTestsKey)
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (w *ResearchLabWorker) Run(ctx context.Context) (string, error) {
	now := w.now()
	nowUnix := now.Unix()
	day := now.UTC().Format("2006-01-02")

	if w.OncePerDay {
		if last, _ := w.St.GetMeta(ctx, researchLabDayKey); last == day {
			return "already ran today", nil
		}
	}

	// Assemble the labeled training set: pool across predicted horizons so the
	// walk-forward grade reaches an honest sample size sooner. Every row is a
	// RESOLVED outcome (no lookahead — LabeledFeatures only joins resolved rows).
	var rows []researchlab.Row
	for _, h := range predHorizons {
		lfs, err := w.St.LabeledFeatures(ctx, h, 20000)
		if err != nil {
			return "", fmt.Errorf("labeled features %s: %w", h, err)
		}
		for _, lf := range lfs {
			rows = append(rows, researchlab.Row{Ts: lf.Ts, Vec: lf.Vec, Y: float64(lf.Up)})
		}
	}
	if len(rows) < w.MinRows {
		_ = w.St.SetMeta(ctx, researchLabDayKey, day)
		return fmt.Sprintf("NO EDGE DETECTED — insufficient data (%d/%d labeled rows)", len(rows), w.MinRows), nil
	}

	keys := researchlab.CanonicalKeys(rows)
	cfg := researchlab.DefaultEvalConfig()
	// Rows are pooled across horizons above, so a single purge span has to
	// cover the WIDEST label in the pool. Purging more than strictly necessary
	// costs training rows; purging less admits a row whose label resolved
	// inside the block it is about to be graded on, which is the leak this
	// exists to close. The conservative direction is the only defensible one.
	for _, h := range predHorizons {
		if secs := horizonSecs(h); secs > cfg.LabelSpan {
			cfg.LabelSpan = secs
		}
	}
	baseline, err := researchlab.Baseline(rows, keys, cfg)
	if err != nil {
		_ = w.St.SetMeta(ctx, researchLabDayKey, day)
		return fmt.Sprintf("NO EDGE DETECTED — baseline not gradable (%v)", err), nil
	}

	// Every decision tonight is corrected for every look the lab has EVER
	// taken, not just tonight's batch — read once so both steps share the same
	// prior count, and advanced once at the end.
	priorTests := w.labTests(ctx)

	// STEP A — re-evaluate existing shadows on fresh data (promotion path).
	promoted, rejected, resurveyed := w.reEvaluateShadows(ctx, rows, keys, cfg, baseline, priorTests, nowUnix)

	// STEP B — mine new hypotheses from the current failure clusters.
	priority := w.clusterPriority(ctx, nowUnix)
	hyps := researchlab.GenerateHypotheses(keys, priority, w.MaxHyps)
	nTested := len(hyps)
	mult := researchlab.Multiplicity{Batch: nTested, PriorTests: priorTests}
	newShadows := 0
	for _, h := range hyps {
		g, err := researchlab.EvaluateHypothesis(h, rows, keys, cfg)
		if err != nil {
			continue // insufficient data for this variant ⇒ silently skip (honest)
		}
		d := researchlab.Judge(h, g, baseline, mult)
		if !d.Survives {
			continue
		}
		spec, _ := json.Marshal(h)
		if err := w.St.InsertShadowHypothesis(ctx, store.HypothesisRow{
			ID: h.ID, Kind: string(h.Kind), Spec: string(spec), Description: h.Desc,
			Status: "shadow", DiscoveredAt: nowUnix,
			BaseLift: baseline.Lift, DiscLift: g.Lift,
			LastLift: g.Lift, LastWilson: d.WilsonLower, LastN: g.N,
			PassStreak: 1, Evals: 1, UpdatedAt: nowUnix,
		}); err != nil {
			return "", fmt.Errorf("insert shadow: %w", err)
		}
		newShadows++
	}

	// STEP C — advisory feedback for adaptive weights (never auto-applied).
	w.emitFeedback(ctx, priority, promoted, nowUnix)

	// Advance the cumulative look counter LAST, and by the number of gradings
	// actually conducted. A crash before this point re-runs tonight's looks
	// under tonight's (lower) divisor, which is the pre-existing behaviour;
	// double-counting them would silently tighten a bar nobody paid for.
	_ = w.St.SetMeta(ctx, researchLabTestsKey, strconv.Itoa(priorTests+nTested+resurveyed))

	_ = w.St.SetMeta(ctx, researchLabDayKey, day)
	return fmt.Sprintf(
		"baseline lift %.3f (n=%d); tested %d hyps → %d new shadows; re-surveyed %d shadows → %d promoted, %d rejected "+
			"(Bonferroni divisor %d = tonight's batch + %d prior looks)",
		baseline.Lift, baseline.N, nTested, newShadows, resurveyed, promoted, rejected,
		mult.Divisor(), priorTests), nil
}

// reEvaluateShadows re-grades every current shadow on the fresh data set and
// advances/resets its streak, promoting on a sustained streak and rejecting on
// repeated failure.
//
// Correction is by tonight's pool size PLUS every look the lab has already
// taken. Correcting by the pool size alone was the defect: the pool shrinks as
// members are promoted or rejected, so a hypothesis that cleared a 24-way bar
// on night 1 faced a 3-way bar a week later while being asked the same question
// of largely the same data — re-testing lowered the bar instead of raising it.
func (w *ResearchLabWorker) reEvaluateShadows(ctx context.Context, rows []researchlab.Row, keys []string, cfg researchlab.EvalConfig, baseline researchlab.Grade, priorTests int, nowUnix int64) (promoted, rejected, surveyed int) {
	shadows, err := w.St.HypothesesByStatus(ctx, "shadow", 1000)
	if err != nil || len(shadows) == 0 {
		return 0, 0, 0
	}
	mult := researchlab.Multiplicity{Batch: len(shadows), PriorTests: priorTests}
	for _, row := range shadows {
		var h researchlab.Hypothesis
		if err := json.Unmarshal([]byte(row.Spec), &h); err != nil {
			continue
		}
		g, err := researchlab.EvaluateHypothesis(h, rows, keys, cfg)
		if err != nil {
			continue // not gradable on today's data ⇒ leave untouched
		}
		surveyed++
		d := researchlab.Judge(h, g, baseline, mult)
		row.LastLift = g.Lift
		row.LastWilson = d.WilsonLower
		row.LastN = g.N
		row.Evals++
		row.UpdatedAt = nowUnix
		if d.Survives {
			row.PassStreak++
			row.FailStreak = 0
			if row.PassStreak >= w.PromoteStreak {
				row.Status = "promoted"
				row.PromotedAt = nowUnix
				promoted++
			}
		} else {
			row.FailStreak++
			row.PassStreak = 0
			if row.FailStreak >= w.RejectStreak {
				row.Status = "rejected"
				rejected++
			}
		}
		_ = w.St.UpdateHypothesisEval(ctx, row)
	}
	return promoted, rejected, surveyed
}

// clusterPriority builds a reason → share map from the last 30 days of
// postmortems so hypothesis generation is steered toward the dominant failures.
func (w *ResearchLabWorker) clusterPriority(ctx context.Context, nowUnix int64) map[string]float64 {
	since := nowUnix - 30*86400
	clusters, _, err := w.St.PostmortemClusters(ctx, since)
	if err != nil || len(clusters) == 0 {
		return nil
	}
	pr := map[string]float64{}
	for _, c := range clusters {
		pr[c.Code] = c.Share
	}
	return pr
}

// researchFeedback is the advisory signal stored for the adaptive layer. It is
// NEVER auto-applied to live weights — adoption is gated (env/human) so a
// research artifact cannot silently degrade the live model.
type researchFeedback struct {
	UpdatedAt        int64              `json:"updatedAt"`
	DominantFailures map[string]float64 `json:"dominantFailures"` // reason → share
	PromotedTonight  int                `json:"promotedTonight"`
	Note             string             `json:"note"`
}

const researchFeedbackKey = "research_feedback:v1"

func (w *ResearchLabWorker) emitFeedback(ctx context.Context, priority map[string]float64, promoted int, nowUnix int64) {
	fb := researchFeedback{
		UpdatedAt:        nowUnix,
		DominantFailures: priority,
		PromotedTonight:  promoted,
		Note:             "advisory only — recurring failure clusters + promoted hypotheses; adaptive-weight adoption is gated, never auto-applied",
	}
	if b, err := json.Marshal(fb); err == nil {
		_ = w.St.SetMeta(ctx, researchFeedbackKey, string(b))
	}
}

// ensure the worker matches the fleet interface at compile time.
var _ interface {
	Name() string
	Interval() time.Duration
	Run(context.Context) (string, error)
} = (*ResearchLabWorker)(nil)
