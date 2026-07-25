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
//     that is Bonferroni-corrected by the number of hypotheses tested that night,
//     so a wider search makes each bar HIGHER, not lower.
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
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchlab"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ResearchLabWorker runs the nightly hypothesis loop.
type ResearchLabWorker struct {
	St  *store.Store
	Now func() time.Time // injectable clock; nil ⇒ time.Now

	// tunables (defaults set by NewResearchLabWorker)
	MaxHyps       int
	NominalAlpha  float64
	PromoteStreak int
	RejectStreak  int
	MinRows       int
	OncePerDay    bool // gate to one real run per UTC day via meta cursor
}

// NewResearchLabWorker builds the worker with disciplined defaults.
func NewResearchLabWorker(st *store.Store) *ResearchLabWorker {
	return &ResearchLabWorker{
		St: st, MaxHyps: 24, NominalAlpha: 0.05,
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
	baseline, err := researchlab.Baseline(rows, keys, cfg)
	if err != nil {
		_ = w.St.SetMeta(ctx, researchLabDayKey, day)
		return fmt.Sprintf("NO EDGE DETECTED — baseline not gradable (%v)", err), nil
	}

	// STEP A — re-evaluate existing shadows on fresh data (promotion path).
	promoted, rejected, resurveyed := w.reEvaluateShadows(ctx, rows, keys, cfg, baseline, nowUnix)

	// STEP B — mine new hypotheses from the current failure clusters.
	priority := w.clusterPriority(ctx, nowUnix)
	hyps := researchlab.GenerateHypotheses(keys, priority, w.MaxHyps)
	nTested := len(hyps)
	newShadows := 0
	for _, h := range hyps {
		g, err := researchlab.EvaluateHypothesis(h, rows, keys, cfg)
		if err != nil {
			continue // insufficient data for this variant ⇒ silently skip (honest)
		}
		d := researchlab.Judge(h, g, baseline, nTested, w.NominalAlpha)
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

	_ = w.St.SetMeta(ctx, researchLabDayKey, day)
	return fmt.Sprintf(
		"baseline lift %.3f (n=%d); tested %d hyps → %d new shadows; re-surveyed %d shadows → %d promoted, %d rejected",
		baseline.Lift, baseline.N, nTested, newShadows, resurveyed, promoted, rejected), nil
}

// reEvaluateShadows re-grades every current shadow on the fresh data set and
// advances/resets its streak, promoting on a sustained streak and rejecting on
// repeated failure. Correction is by the number of shadows re-tested tonight.
func (w *ResearchLabWorker) reEvaluateShadows(ctx context.Context, rows []researchlab.Row, keys []string, cfg researchlab.EvalConfig, baseline researchlab.Grade, nowUnix int64) (promoted, rejected, surveyed int) {
	shadows, err := w.St.HypothesesByStatus(ctx, "shadow", 1000)
	if err != nil || len(shadows) == 0 {
		return 0, 0, 0
	}
	nTested := len(shadows)
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
		d := researchlab.Judge(h, g, baseline, nTested, w.NominalAlpha)
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
