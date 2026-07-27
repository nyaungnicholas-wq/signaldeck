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
	"os"
	"path/filepath"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/modelhealth"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ModelHealthWorker grades models and persists their verdicts.
type ModelHealthWorker struct {
	St *store.Store
	// RegistryPath overrides where data/accuracy_registry.json is read from
	// (tests, unusual layouts). Empty means the standard candidates.
	RegistryPath string
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

	// The pre-registered FAILED-forward retire flags from the accuracy
	// registry (auto-retire rule, chained 2026-07-26 while every directional
	// verdict was still INSUFFICIENT). A flag here outranks the composite
	// score below: a row whose whole effective-N interval sits below the
	// prequential null is retired the grade it happens, not when the score
	// catches up.
	regRetired := w.registryRetired()

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

		// HEAD-TO-HEAD vs the tracked prequential-majority benchmark — the
		// live "<horizon>#pm" rows the prediction pipeline commits under
		// identical dedup/survivorship/resolution rules. Graded by the SAME
		// accessor, and the ensemble side is re-sliced to the benchmark's own
		// resolution window so the two accuracies cover the same market days
		// (the benchmark only exists since the tracked-benchmark wave).
		// skillVsBenchmark <= 0 is the deficit the offline registry keeps
		// finding, now visible in the daemon's own health output every pass.
		bench, berr := w.St.DirectionalRecord(ctx, benchmarkHorizon(h), 0)
		if berr != nil {
			bench = store.DirectionalRecordRow{}
		}
		var aligned store.DirectionalRecordRow
		if bench.N > 0 {
			if first, ok, err := w.St.FirstResolutionAt(ctx, benchmarkHorizon(h)); err == nil && ok {
				if a, err := w.St.DirectionalRecord(ctx, h, first); err == nil {
					aligned = a
				}
			}
		}
		var skillVsBenchmark any // nil until both sides have aligned rows
		if bench.N > 0 && aligned.N > 0 {
			skillVsBenchmark = aligned.Accuracy - bench.Accuracy
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
		if regRetired[model] {
			score.Verdict = modelhealth.VerdictRetired
			score.Emitting = false
			score.Reasons = append(score.Reasons,
				"accuracy-registry retire flag: effective-N Wilson upper bound below the "+
					"prequential null at the pre-registered evidence floors (auto-retire rule)")
		}
		graded++
		if !score.Emitting {
			retired++
		}

		blob, err := json.Marshal(map[string]any{
			"model":          model,
			"verdict":        score.Verdict,
			"emitting":       score.Emitting,
			"overall":        score.Overall,
			"components":     score.Components,
			"reasons":        score.Reasons,
			"observations":   score.Observations,
			"accuracy":       full.Accuracy,
			"baseline":       full.BaselineAcc,
			"registryRetire": regRetired[model],
			// The tracked benchmark's own record plus the ensemble re-graded
			// over the benchmark's window — same days, same rules, one number
			// (skillVsBenchmark) that says whether the ensemble is beating a
			// bettor who just follows the running majority.
			"benchmark": map[string]any{
				"model":              "prequential-majority-" + string(h),
				"n":                  bench.N,
				"accuracy":           bench.Accuracy,
				"ensembleAlignedN":   aligned.N,
				"ensembleAlignedAcc": aligned.Accuracy,
			},
			"skillVsBenchmark": skillVsBenchmark,
			"gradedAt":         time.Now().Unix(),
		})
		if err != nil {
			continue
		}
		if err := w.St.SetMeta(ctx, MetaKeyPrefix+model, string(blob)); err != nil {
			continue
		}
		line := fmt.Sprintf("%s=%s(%.2f)", h, score.Verdict, score.Overall)
		if bench.N > 0 && aligned.N > 0 {
			line += fmt.Sprintf(" vsPM%+.1fpp/n%d", (aligned.Accuracy-bench.Accuracy)*100, bench.N)
		}
		summary = append(summary, line)
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

// registryRetired resolves the registry path and returns the retire flags.
// Candidates mirror PreregRegistrar.fileDigest: launchd runs the daemon from
// <repo>/daemon, and tools run from the repo root.
func (w *ModelHealthWorker) registryRetired() map[string]bool {
	path := w.RegistryPath
	if path == "" {
		for _, p := range []string{
			filepath.Join("..", "data", "accuracy_registry.json"),
			filepath.Join("data", "accuracy_registry.json"),
		} {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}
	return retiredFromRegistry(path)
}

// retiredFromRegistry maps model name -> true for every directional registry
// row carrying the pre-registered FAILED-forward retire flag (the auto-retire
// rule chained under prereg kind "auto-retire-rule"). An unreadable file or
// malformed JSON returns an empty map — the kill switch must never fire on
// evidence nobody can read, the same posture featureDrift takes — and the
// opposite failure is guarded by Grade's own skill gate, which still retires
// on the store's record without the registry.
func retiredFromRegistry(path string) map[string]bool {
	out := map[string]bool{}
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var reg struct {
		Rows []struct {
			Predictor string `json:"predictor"`
			Family    string `json:"family"`
			Retire    bool   `json:"retire"`
		} `json:"rows"`
	}
	if json.Unmarshal(raw, &reg) != nil {
		return out
	}
	for _, r := range reg.Rows {
		if r.Family != "direction" || !r.Retire {
			continue
		}
		// "directional-ensemble (1d)" / "directional-ensemble (1w, high
		// conviction)" -> directional-ensemble-1d / -1w. A FAILED tier retires
		// its horizon: the horizon is the unit that publishes.
		i := strings.IndexByte(r.Predictor, '(')
		if i < 0 {
			continue
		}
		h := r.Predictor[i+1:]
		if j := strings.IndexAny(h, ",)"); j >= 0 {
			h = h[:j]
		}
		if h = strings.TrimSpace(h); h != "" {
			out["directional-ensemble-"+h] = true
		}
	}
	return out
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
