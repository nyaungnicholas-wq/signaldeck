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

// structuralHighConviction is the band whose claim is largest (97%+ for
// trend21) and therefore the one most worth holding to account separately.
const structuralHighConviction = 0.9

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

	// STRUCTURAL MODELS (2026-07-25). The gate was aimed only at the directional
	// ensemble — the model that is now retired. trend21/vol21/liquidity21 are the
	// three that survived validation, and until something holds their SHIPPED
	// accuracy claim against what actually happened, that claim is a backtest
	// number wearing a live label.
	//
	// They are graded differently from direction on purpose. A structural call's
	// honest null is not 50% but the PERSISTENCE base rate: "the regime
	// continued" is what a predictor scores by doing nothing, so edge is measured
	// against that. This is the same correction the survivorship re-validation
	// forced — the 83% headline was the base rate, and only the spread between
	// conviction bands was ever the product.
	sg, sr := w.gradeStructural(ctx)
	graded += sg
	retired += sr

	return fmt.Sprintf("graded %d, retired %d — %v", graded, retired, summary), nil
}

// gradeStructural grades each structural predictor against its own live record
// and its own shipped claim, and persists a verdict the API can honour.
func (w *ModelHealthWorker) gradeStructural(ctx context.Context) (graded, retired int) {
	recs, err := w.St.StructuralRecords(ctx, 0)
	if err != nil {
		return 0, 0
	}
	// High-conviction slice: the tier a user would actually act on, and the one
	// carrying the biggest claim (97%+ for trend21).
	hi, _ := w.St.StructuralRecords(ctx, structuralHighConviction)
	hiByKind := map[string]store.StructuralRecordRow{}
	for _, r := range hi {
		hiByKind[r.Kind] = r
	}

	for _, r := range recs {
		score := modelhealth.Grade(modelhealth.Inputs{
			Observations: r.N,
			Accuracy:     r.Accuracy,
			// The honest null for a persistence forecast.
			BaselineAcc: r.PersistenceBase,
			RecentAcc:   r.Accuracy,
			RecentN:     r.N,
			// A structural call emits a regime, not a probability, so Brier and
			// calibration error are not measurable here. Left at zero rather
			// than invented — Grade weights skill highest for exactly this kind
			// of case.
			CalibrationErr: 0,
		})
		graded++
		if !score.Emitting {
			retired++
		}

		h := hiByKind[r.Kind]
		blob, err := json.Marshal(map[string]any{
			"model":           "structural-" + r.Kind,
			"kind":            r.Kind,
			"verdict":         score.Verdict,
			"emitting":        score.Emitting,
			"overall":         score.Overall,
			"components":      score.Components,
			"reasons":         score.Reasons,
			"observations":    r.N,
			"distinctDays":    r.DistinctDays,
			"liveAccuracy":    r.Accuracy,
			"claimedAccuracy": r.ClaimedAccuracy,
			"persistenceBase": r.PersistenceBase,
			// Edge is live accuracy MINUS the persistence base rate. This is the
			// number that says whether the predictor does anything at all.
			"edgeVsPersistence": r.Accuracy - r.PersistenceBase,
			// Claim drift: did the shipped number survive contact with reality?
			"claimGap":       r.Accuracy - r.ClaimedAccuracy,
			"highConviction": map[string]any{"n": h.N, "accuracy": h.Accuracy, "claimed": h.ClaimedAccuracy},
			"gradedAt":       time.Now().Unix(),
		})
		if err != nil {
			continue
		}
		_ = w.St.SetMeta(ctx, MetaKeyPrefix+"structural-"+r.Kind, string(blob))
	}
	return graded, retired
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
