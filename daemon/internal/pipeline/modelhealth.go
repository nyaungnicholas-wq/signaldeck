// Model-health worker (2026-07-24): grade every emitting model against its own
// live record and switch off the ones the record no longer supports.
//
// The directional ensemble is why this exists. It accumulated a live record
// significantly BELOW its majority-class baseline and never stopped shipping
// predictions, because no component had the authority to disable a model. Health
// scoring without an off-switch is just a nicer way to describe the same failure,
// so this worker writes a verdict the prediction path is required to honour.
//
// Figures are not typed here: data/accuracy_registry.json is the one source.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/modelhealth"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
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
	// graded counts verdicts actually WRITTEN; failed counts those that were
	// computed and then lost on the way to the store. Reported separately so a
	// pass that persisted nothing cannot read as a pass that graded everything.
	var graded, retired, failed int
	var summary []string

	// The pre-registered FAILED-forward retire flags from the accuracy
	// registry (auto-retire rule, chained 2026-07-26 while every directional
	// verdict was still INSUFFICIENT). A flag here outranks the composite
	// score below: a row whose whole effective-N interval sits below the
	// prequential null is retired the grade it happens, not when the score
	// catches up. The registry's revision gate outranks BOTH — see regFlag.
	// A worker that cannot read the kill switch must not grade. Withholding a
	// verdict is recoverable; publishing one computed as though nothing were
	// retired is not.
	regFlags, err := w.registryFlags()
	if err != nil {
		return "", fmt.Errorf("kill switch unreadable, no model graded: %v: %w", err, workers.ErrDegraded)
	}

	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		model := "directional-ensemble-" + string(h)

		full, err := w.St.DirectionalRecord(ctx, h, 0)
		if err != nil {
			// A DB read failure is not "this horizon had nothing to grade": the
			// verdict for it is simply MISSING, and /api/modelhealth keeps
			// serving the previous pass's answer for a model that may have been
			// retired since. Bare `continue` made that indistinguishable from a
			// clean empty horizon and left the failed>0 gate below unarmed, so
			// the worker filed status=ok. Same doctrine as the persist paths
			// twenty lines down: COUNT THE OUTCOME, NOT THE INTENT.
			failed++
			slog.Warn("model-health: directional record unreadable", "horizon", h, "err", err)
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
		// Attribution first. A gated row means the grader deleted its own
		// verdict because contributing rows name builds this repo does not
		// contain, so there is no graded claim left to act on — not FAILED,
		// and not clean. Stop emitting without asserting a record: a model
		// whose grade nobody can reproduce must not keep publishing while the
		// question is open, and must not be recorded as condemned either.
		if f := regFlags[model]; f.Unattributable {
			score.Verdict = modelhealth.VerdictUnattributable
			score.Emitting = false
			score.Reasons = append(score.Reasons,
				"accuracy-registry revision gate: contributing rows written by "+
					strings.Join(f.Offenders, ", ")+" — not commits in this repository, "+
					"so the grader stripped this row's verdict and no graded claim stands")
		} else if f.Retire {
			score.Verdict = modelhealth.VerdictRetired
			score.Emitting = false
			score.Reasons = append(score.Reasons,
				"accuracy-registry retire flag: effective-N Wilson upper bound below the "+
					"prequential null at the pre-registered evidence floors (auto-retire rule)")
		}
		// CROSS-SECTION GATE, surfaced. The prediction runner already refuses to
		// publish a horizon whose cross-section collapsed, but it says so only in
		// its log line. An operator watching /api/fleethealth would have seen a
		// model reporting healthy while nothing it produced reached the wire.
		//
		// This does not re-decide anything: the runner owns the refusal, and this
		// reports the same record the runner acted on. Emitting goes false because
		// that is the literal truth for the horizon while the gate holds.
		if rec, err := loadCrossSection(ctx, w.St, h); err == nil && rec != nil {
			if ok, reason := rec.Usable(); !ok {
				score.Emitting = false
				if score.Verdict == modelhealth.VerdictHealthy ||
					score.Verdict == modelhealth.VerdictWatch {
					score.Verdict = modelhealth.VerdictDegraded
				}
				score.Reasons = append(score.Reasons,
					"cross-section gate ("+rec.Day+"): "+reason+
						" — predictions are being withheld for this horizon")
			}
		}
		blob, err := json.Marshal(map[string]any{
			"model":                  model,
			"verdict":                score.Verdict,
			"emitting":               score.Emitting,
			"overall":                score.Overall,
			"components":             score.Components,
			"reasons":                score.Reasons,
			"observations":           score.Observations,
			"accuracy":               full.Accuracy,
			"baseline":               full.BaselineAcc,
			"registryRetire":         regFlags[model].Retire,
			"registryUnattributable": regFlags[model].Unattributable,
			"registryRevisionGate":   regFlags[model].Offenders,
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
		// COUNT THE OUTCOME, NOT THE INTENT. graded++ used to run before this
		// marshal/persist pair, and both failure paths `continue` — so a
		// contended meta write (a hazard this codebase documents at
		// internal/workers/workers.go:404-407) had the worker report
		// "graded 5, retired 2" with status=ok while zero verdicts reached the
		// store and /api/modelhealth kept serving the PREVIOUS pass. A model
		// retired this pass would have gone on publishing.
		if err != nil {
			failed++
			slog.Warn("model-health: verdict not persisted (marshal)", "model", model, "err", err)
			continue
		}
		if err := w.St.SetMeta(ctx, MetaKeyPrefix+model, string(blob)); err != nil {
			failed++
			slog.Warn("model-health: verdict not persisted (store)", "model", model, "err", err)
			continue
		}
		graded++
		if !score.Emitting {
			retired++
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
	sg, sr, sf := w.gradeStructural(ctx)
	graded += sg
	retired += sr
	failed += sf

	if failed > 0 {
		// DEGRADED, not ok. A verdict that never reached the store is a verdict
		// the API will not serve, so the pass did not do what its name says —
		// and a worker reporting ok here is exactly how a model retired this
		// pass would have kept publishing.
		return fmt.Sprintf("graded %d, retired %d, %d verdict(s) NOT PERSISTED — %v",
				graded, retired, failed, summary),
			fmt.Errorf("%d model verdict(s) computed but not written to the store: %w",
				failed, workers.ErrDegraded)
	}
	return fmt.Sprintf("graded %d, retired %d — %v", graded, retired, summary), nil
}

// gradeStructural grades each structural predictor against its own live record
// and its own shipped claim, and persists a verdict the API can honour.
func (w *ModelHealthWorker) gradeStructural(ctx context.Context) (graded, retired, failed int) {
	recs, err := w.St.StructuralRecords(ctx, 0)
	if err != nil {
		// (0,0,0) reads to the caller as "no structural predictors to grade",
		// which is what a healthy empty fleet returns — so an unreadable table
		// produced status=ok and the API went on serving the prior pass's
		// structural verdicts. Report it as a failure so the caller degrades.
		slog.Warn("model-health: structural records unreadable", "err", err)
		return 0, 0, 1
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
		// Same rule as the directional path: count what was PERSISTED. This one
		// also discarded the SetMeta error outright (`_ =`), so a failed write
		// left no trace at all behind a count that had already been incremented.
		if err != nil {
			failed++
			slog.Warn("model-health: structural verdict not persisted (marshal)",
				"kind", r.Kind, "err", err)
			continue
		}
		if err := w.St.SetMeta(ctx, MetaKeyPrefix+"structural-"+r.Kind, string(blob)); err != nil {
			failed++
			slog.Warn("model-health: structural verdict not persisted (store)",
				"kind", r.Kind, "err", err)
			continue
		}
		graded++
		if !score.Emitting {
			retired++
		}
	}
	return graded, retired, failed
}

// featureDrift returns the fraction of features whose distribution has moved
// materially between an older reference window and the recent one.
//
// Returns nil — NOT 0 — when it cannot be computed. A drift number nobody can
// compute must not retire a model, which is why the old code returned 0; but 0
// means "nothing drifted" and scores stability at a perfect 1.0, so the guard
// against a false retirement was silently awarding full marks on the axis meant
// to catch exactly this. That is the defect internal/modelhealth/drift.go was
// written to close, re-entered through the fix's own error path. Grade now
// WITHHOLDS the component for a nil, which condemns nothing and claims nothing.
func (w *ModelHealthWorker) featureDrift(ctx context.Context) *float64 {
	version, err := w.St.LatestFeatureVersion(ctx)
	if err != nil {
		slog.Warn("model-health: feature version unreadable — stability withheld", "err", err)
		return nil
	}
	if version == 0 {
		return nil // no feature store yet: unmeasured, not stable
	}
	now := time.Now()
	liveTo := now.Unix()
	liveFrom := now.AddDate(0, 0, -driftLiveDays).Unix()
	refTo := liveFrom
	refFrom := now.AddDate(0, 0, -driftLiveDays-driftRefDays).Unix()

	ref, live, err := w.St.FeatureWindows(ctx, version, refFrom, refTo, liveFrom, liveTo, 0)
	if err != nil {
		slog.Warn("model-health: feature windows unreadable — stability withheld", "err", err)
		return nil
	}
	var results []modelhealth.DriftResult
	for name, refVals := range ref {
		results = append(results, modelhealth.DriftFor(name, refVals, live[name]))
	}
	// DriftFraction also collapses "too little data to judge" to 0 (it returns 0
	// when judged == 0), so ask it how many features it actually judged rather
	// than reading a 0 that could mean either thing.
	frac, judged := modelhealth.DriftFractionJudged(results)
	if judged == 0 {
		return nil
	}
	return &frac
}

// registryFlags resolves the registry path and returns the kill switch state.
// Candidates mirror PreregRegistrar.fileDigest: launchd runs the daemon from
// <repo>/daemon, and tools run from the repo root.
func (w *ModelHealthWorker) registryFlags() (map[string]regFlag, error) {
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
	return registryFlagsFrom(path)
}

// regFlag is what one directional registry row tells the kill switch.
type regFlag struct {
	// Retire is the pre-registered FAILED-forward flag. Only meaningful when
	// Unattributable is false: retire is derived from a verdict, and a verdict
	// the revision gate deleted cannot leave a live flag behind it.
	Retire bool
	// Unattributable means the grader's revision gate found contributing rows
	// written by builds this repository does not contain, and therefore
	// stripped the row's verdict. The rows that produced the record cannot be
	// tied to any released code, so neither the retire flag nor its absence
	// carries information.
	Unattributable bool
	// Offenders are the unresolvable build stamps, for the operator reason
	// line. Empty when Unattributable is false.
	Offenders []string
}

// registryFlagsFrom maps model name -> the directional registry row's kill
// switch state. An unreadable file or malformed JSON returns an empty map —
// the kill switch must never fire on evidence nobody can read, the same
// posture featureDrift takes — and the opposite failure is guarded by Grade's
// own skill gate, which still retires on the store's record without the
// registry.
//
// Both fields matter, and for opposite reasons. `retire` is the pre-registered
// FAILED-forward flag (auto-retire rule, chained under prereg kind
// "auto-retire-rule"). `revision_gate` is the grader refusing to stand behind
// the row at all: apply_revision_gate in tools/accuracy_registry.py DELETES
// the verdict when a contributing row names a build that is not a commit here,
// while leaving `retire` — a value computed from that same now-deleted verdict
// — sitting in the file. Reading `retire` alone therefore acts on evidence the
// grader has formally disowned, in whichever direction the stale flag happens
// to point. So the gate is read too, and it outranks the flag.
// It returns an error rather than an empty map on failure. An empty map means
// NO model is retired and none is unattributable -- an affirmative all-clear --
// and this returned exactly that on three silent paths: no path resolved, the
// file could not be read, and the JSON did not parse. The daemon's working
// directory decides whether the switch is found at all (registryFlags probes two
// relative candidates), and the grader rewrites this file in place, so a
// mid-rewrite read is a normal event. A retired model would then be regraded
// without its flag, come out healthy and emitting, and the prediction path would
// readmit it -- while /api/modelhealth published registryRetire:false as a
// positive assertion of a check that never ran.
//
// featureDrift, 50 lines up, already handles this class correctly: it returns
// nil WITH an explicit warning so Grade withholds the component. This is the
// same rule for the switch that outranks the score.
func registryFlagsFrom(path string) (map[string]regFlag, error) {
	out := map[string]regFlag{}
	if path == "" {
		return nil, fmt.Errorf("no accuracy registry found at any candidate path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read accuracy registry %s: %w", path, err)
	}
	var reg struct {
		Rows []struct {
			Predictor    string   `json:"predictor"`
			Family       string   `json:"family"`
			Retire       bool     `json:"retire"`
			RevisionGate []string `json:"revision_gate"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("parse accuracy registry %s: %w", path, err)
	}
	for _, r := range reg.Rows {
		gated := len(r.RevisionGate) > 0
		if r.Family != "direction" || (!r.Retire && !gated) {
			continue
		}
		// "directional-ensemble (1d)" / "directional-ensemble (1w, high
		// conviction)" -> directional-ensemble-1d / -1w. A FAILED tier retires
		// its horizon: the horizon is the unit that publishes. A gated tier
		// disqualifies its horizon for the same reason.
		i := strings.IndexByte(r.Predictor, '(')
		if i < 0 {
			continue
		}
		h := r.Predictor[i+1:]
		if j := strings.IndexAny(h, ",)"); j >= 0 {
			h = h[:j]
		}
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		key := "directional-ensemble-" + h
		f := out[key]
		if gated {
			// A gate on ANY tier of a horizon disqualifies the horizon, and it
			// also voids the retire flag the same row carries: that flag was
			// computed from the verdict the gate deleted.
			f.Unattributable = true
			f.Offenders = r.RevisionGate
			f.Retire = false
		} else if r.Retire && !f.Unattributable {
			f.Retire = true
		}
		out[key] = f
	}
	return out, nil
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
