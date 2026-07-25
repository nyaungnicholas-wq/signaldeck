// Model-health worker (2026-07-24): grade every emitting model against its own
// live record and switch off the ones the record no longer supports.
//
// The directional ensemble is why this exists. It accumulated 18,762
// independent symbol-days at 48.0% accuracy — significantly BELOW its
// majority-class baseline — and never stopped shipping predictions, because no
// component had the authority to disable a model. Health scoring without an
// off-switch is just a nicer way to describe the same failure, so this worker
// writes a verdict the prediction path is required to honour.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/modelhealth"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ModelHealthWorker grades models and persists their verdicts.
type ModelHealthWorker struct {
	St *store.Store
}

func (w *ModelHealthWorker) Name() string            { return "model-health" }
func (w *ModelHealthWorker) Interval() time.Duration { return 1 * time.Hour }

// MetaKeyPrefix namespaces one stored verdict per model.
const MetaKeyPrefix = "model_health:"

// recentWindowDays bounds the "is it decaying" comparison.
const recentWindowDays = 14

// Drift windows: the live sample is what the model is being asked to predict
// from now; the reference is the older stretch it was effectively fit against.
const (
	driftLiveDays = 14
	driftRefDays  = 60
)

func (w *ModelHealthWorker) Run(ctx context.Context) (string, error) {
	var graded, retired int
	var summary []string

	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		model := "directional-ensemble-" + string(h)

		full, err := w.St.DirectionalRecord(ctx, h, 0)
		if err != nil {
			continue
		}
		if full.N == 0 {
			continue
		}
		since := time.Now().AddDate(0, 0, -recentWindowDays).Unix()
		recent, err := w.St.DirectionalRecord(ctx, h, since)
		if err != nil {
			recent = store.DirectionalRecordRow{}
		}

		// FEATURE DRIFT (2026-07-25): compare the recent feature distribution
		// against an older reference window. Split by TIME, not randomly — a
		// random split compares a model to itself and always looks stable.
		drift := w.featureDrift(ctx)

		score := modelhealth.Grade(modelhealth.Inputs{
			FeatureDriftPct: drift,
			Observations:    full.N,
			Accuracy:        full.Accuracy,
			BaselineAcc:     full.BaselineAcc,
			RecentAcc:       recent.Accuracy,
			RecentN:         recent.N,
			BrierSkill:      full.BrierSkill,
			CalibrationErr:  full.CalibrationErr,
			// The ensemble retrains continuously off resolved outcomes, so age
			// is not a meaningful axis for it; leaving it zero scores freshness
			// full rather than inventing a retrain date.
		})
		graded++
		if !score.Emitting {
			retired++
		}

		blob, err := json.Marshal(map[string]any{
			"model":        model,
			"verdict":      score.Verdict,
			"emitting":     score.Emitting,
			"overall":      score.Overall,
			"components":   score.Components,
			"reasons":      score.Reasons,
			"observations": score.Observations,
			"accuracy":     full.Accuracy,
			"baseline":     full.BaselineAcc,
			"gradedAt":     time.Now().Unix(),
		})
		if err != nil {
			continue
		}
		if err := w.St.SetMeta(ctx, MetaKeyPrefix+model, string(blob)); err != nil {
			continue
		}
		summary = append(summary, fmt.Sprintf("%s=%s(%.2f)", h, score.Verdict, score.Overall))
	}

	return fmt.Sprintf("graded %d, retired %d — %v", graded, retired, summary), nil
}

// featureDrift returns the fraction of features whose distribution has moved
// materially between an older reference window and the recent one. Returns 0
// on any failure — a drift number nobody can compute must not retire a model,
// and the observation floor in Grade guards the opposite direction.
func (w *ModelHealthWorker) featureDrift(ctx context.Context) float64 {
	version, err := w.St.LatestFeatureVersion(ctx)
	if err != nil || version == 0 {
		return 0
	}
	now := time.Now()
	liveTo := now.Unix()
	liveFrom := now.AddDate(0, 0, -driftLiveDays).Unix()
	refTo := liveFrom
	refFrom := now.AddDate(0, 0, -driftLiveDays-driftRefDays).Unix()

	ref, live, err := w.St.FeatureWindows(ctx, version, refFrom, refTo, liveFrom, liveTo, 0)
	if err != nil {
		return 0
	}
	var results []modelhealth.DriftResult
	for name, refVals := range ref {
		results = append(results, modelhealth.DriftFor(name, refVals, live[name]))
	}
	return modelhealth.DriftFraction(results)
}

// ModelEmitting reports whether a model is currently cleared to emit. Unknown
// models default to TRUE: this gate exists to switch off what the record
// condemns, not to silence anything it has not yet judged.
func ModelEmitting(ctx context.Context, st *store.Store, model string) (bool, string) {
	raw, err := st.GetMeta(ctx, MetaKeyPrefix+model)
	if err != nil || raw == "" {
		return true, ""
	}
	var v struct {
		Emitting bool   `json:"emitting"`
		Verdict  string `json:"verdict"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return true, ""
	}
	return v.Emitting, v.Verdict
}
