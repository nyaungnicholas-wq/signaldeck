package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/featurehealth"
	"github.com/nyaungnicholas-wq/signaldeck/internal/featureredundancy"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// ── FEATURE HEALTH GRADER ───────────────────────────────────────────────────
//
// Grades every INPUT in the feature store on decay, stability, coverage and
// redundancy, and publishes a retire set that the GBM trainer actually honors
// (see retiredFeatureKeys, consulted in gbmtrain.go). The platform already
// auto-retired MODELS; this is the same discipline one level down, and the reason
// it needed writing is that a feature whose information coefficient decayed to
// nothing previously stayed in the vector forever, because the only thing looking
// at features was a report a human read.
//
// TWO DELIBERATE SAFETY PROPERTIES, both in internal/featurehealth and both worth
// repeating where the worker runs:
//
//   - A feature below the evidence floor is KEPT, not dropped. Retiring a feature
//     for being new would make the vector unable to ever adopt anything.
//   - If more than half the judged features grade out AT ONCE, retirement is
//     reported and NOT applied. A whole vector dying together points at the
//     forward-return label that feeds every IC, not at every feature
//     independently dying, and emptying the vector on that basis would turn one
//     upstream defect into a total outage.

// FeatureHealthMetaPrefix is the meta key the per-horizon report is stored under.
// Same convention as MetaKeyPrefix for model health, so both are readable the
// same way.
const FeatureHealthMetaPrefix = "feature_health:"

// featureHealthSampleCap bounds the labeled rows pulled per horizon. Matches
// redundancySampleCap: the two analyses answer adjacent questions over the same
// table and there is no reason for them to disagree about how much history counts.
const featureHealthSampleCap = 6000

// FeatureHealthGrader grades individual features and publishes the retire set.
type FeatureHealthGrader struct {
	St *store.Store
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

func (w *FeatureHealthGrader) Name() string { return "feature-health" }

// Interval: every 6 hours. Feature decay is a slow phenomenon measured over
// months of resolved outcomes, so grading more often would just re-derive the
// same verdict; less often and a genuinely dead input keeps training the model
// for another day.
func (w *FeatureHealthGrader) Interval() time.Duration { return 6 * time.Hour }

func (w *FeatureHealthGrader) Run(ctx context.Context) (string, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}

	var summary []string
	graded, failed := 0, 0
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		rows, err := w.St.LabeledFeaturesAll(ctx, h, 0, featureHealthSampleCap)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			continue
		}

		// Only genuine INPUTS are graded. canonicalFeatureKeys already excludes
		// the blend's own probabilities, so a model output can never be retired
		// as if it were a data source — or, worse, kept as if it were one.
		allow := canonicalFeatureKeys(rows)

		// Redundancy comes from the existing analysis rather than being
		// recomputed here, so "redundant" means exactly one thing platform-wide.
		redundant := map[string]bool{}
		rcfg := featureredundancy.Defaults()
		rcfg.Allow = allow
		rsamples := make([]featureredundancy.Sample, 0, len(rows))
		for _, r := range rows {
			rsamples = append(rsamples, featureredundancy.Sample{Vec: r.Vec, Fwd: r.FwdReturn})
		}
		rrep := featureredundancy.Analyze(rsamples, rcfg)
		// Only trust the clustering when it had enough data to conclude anything;
		// an unmeaningful report's Redundant list would retire features on noise.
		if rrep.Meaningful {
			for _, name := range rrep.Redundant {
				redundant[name] = true
			}
		}

		samples := make([]featurehealth.Sample, 0, len(rows))
		for _, r := range rows {
			samples = append(samples, featurehealth.Sample{Ts: r.Ts, Vec: r.Vec, Fwd: r.FwdReturn})
		}
		cfg := featurehealth.DefaultSampleConfig()
		cfg.Allow = allow
		cfg.Redundant = redundant

		report := featurehealth.Analyze(featurehealth.FromSamples(samples, cfg))

		blob, err := json.Marshal(map[string]any{
			"horizon":         string(h),
			"scores":          report.Scores,
			"keep":            report.Keep,
			"retire":          report.Retire,
			"retiredPct":      report.RetiredPct,
			"note":            report.Note,
			"rows":            len(rows),
			"redundancyKnown": rrep.Meaningful,
			"gradedAt":        now.Unix(),
		})
		if err != nil {
			// COUNT THE OUTCOME, NOT THE INTENT — the fix
			// internal/pipeline/modelhealth.go:198 documents, which this
			// sibling never got. graded++ used to run BEFORE this marshal and
			// the failure path was a bare `continue`: no log, no counter, no
			// summary line, nil returned. A horizon that persisted nothing
			// still counted itself graded and the run filed status=ok.
			failed++
			slog.Warn("feature-health: marshal report", "horizon", h, "err", err)
			continue
		}
		if err := w.St.SetMeta(ctx, FeatureHealthMetaPrefix+string(h), string(blob)); err != nil {
			return "", err
		}
		graded++
		summary = append(summary, fmt.Sprintf("%s: %d kept, %d retired of %d rows",
			h, len(report.Keep), len(report.Retire), len(rows)))
	}

	if failed > 0 {
		// A horizon whose report never reached the store is a horizon the
		// enforcement helper and the API will read stale, so the pass did not
		// do what its name says.
		return fmt.Sprintf("%s (%d horizon(s) NOT PERSISTED)", strings.Join(summary, "; "), failed),
			fmt.Errorf("%d feature-health report(s) computed but not written: %w",
				failed, workers.ErrDegraded)
	}
	if graded == 0 {
		return "no labeled features yet — nothing to grade", nil
	}
	return strings.Join(summary, "; "), nil
}

// featureHealthReport is the stored shape, read back by both the enforcement
// helper and the API.
type featureHealthReport struct {
	Horizon    string                `json:"horizon"`
	Scores     []featurehealth.Score `json:"scores"`
	Keep       []string              `json:"keep"`
	Retire     []string              `json:"retire"`
	RetiredPct float64               `json:"retiredPct"`
	Note       string                `json:"note"`
	Rows       int                   `json:"rows"`
	GradedAt   int64                 `json:"gradedAt"`
}

// FeatureHealthFor reads the stored report for a horizon. ok is false when the
// grader has not run yet — which callers must treat as "no opinion", never as
// "nothing to retire is a verdict".
func FeatureHealthFor(ctx context.Context, st *store.Store, h md.Horizon) (featureHealthReport, bool, error) {
	raw, err := st.GetMeta(ctx, FeatureHealthMetaPrefix+string(h))
	if err != nil || raw == "" {
		return featureHealthReport{}, false, err
	}
	var rep featureHealthReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		return featureHealthReport{}, false, nil
	}
	return rep, true, nil
}

// retiredFeatureKeys returns the set of features the grader retired for this
// horizon. THIS is where per-feature retirement stops being a report: gbmtrain
// consults it and drops these keys from the training vector.
//
// It fails OPEN — an unreadable or absent report retires nothing. That direction
// is deliberate: the cost of training on a stale feature for another six hours is
// a slightly worse model, while the cost of a bad read emptying the vector is no
// model at all. A gate whose failure mode is "train on everything" is recoverable;
// one whose failure mode is "train on nothing" is an outage.
func retiredFeatureKeys(ctx context.Context, st *store.Store, h md.Horizon) map[string]bool {
	out := map[string]bool{}
	rep, ok, err := FeatureHealthFor(ctx, st, h)
	if err != nil || !ok {
		return out
	}
	for _, name := range rep.Retire {
		out[name] = true
	}
	return out
}

// dropRetired filters keys through the retire set, returning the survivors and
// the names dropped (for the worker log — a feature leaving the model quietly is
// how a model becomes unexplainable).
//
// It refuses to return an EMPTY key set: if every candidate was retired, the
// original list is returned unchanged. featurehealth.Analyze already declines to
// apply a wholesale retirement, so reaching this state means the two disagree,
// and training on a stale vector beats training on no features at all.
func dropRetired(keys []string, retired map[string]bool) (kept, dropped []string) {
	if len(retired) == 0 {
		return keys, nil
	}
	for _, k := range keys {
		if retired[k] {
			dropped = append(dropped, k)
			continue
		}
		kept = append(kept, k)
	}
	if len(kept) == 0 {
		return keys, nil
	}
	return kept, dropped
}
