// META-LABELING WAVE (2026-07-25) — the worker behind internal/metalabel.
//
// The question it answers is not "which way?" but "is this call worth taking at
// all?". The primary model keeps the side; a secondary model, trained on whether
// the primary's call cleared cost, keeps the trigger.
//
// It is a MEASUREMENT surface, on purpose. Nothing here gates a live decision:
// the runner grades the filter and publishes the verdict, and a human decides
// whether it may ever size a trade. That restraint is not caution for its own
// sake — the primary this currently grades is the auto-retired directional
// ensemble, and a filter wired into a signal with no cost-net edge would produce
// fewer trades with the same lack of edge while looking like an improvement.
// When it was first graded live it showed a large precision gain and was still
// correctly rejected for exactly that reason.
//
// # Why both horizons currently publish a REFUSAL rather than a grade
//
// Those early grades were computed on a walk-forward with no purge, and this
// runner's candidates are symbol-days: measured on the live database
// (2026-07-25, read-only), ~5,870 1d candidates span ELEVEN distinct days and
// ~5,610 1w candidates span twelve. With a one-day label and ~530 rows per day,
// 57.8% of the training rows across folds carried outcomes realised inside the
// block they were about to be graded on; at the 1w horizon it was 94.1%. Purging
// them honestly removes 5,436 training rows at 1d and 7,181 at 1w and leaves 2 of
// 7 fold boundaries trainable at 1d, 1 of 7 at 1w, against the 4 retrains
// demanded — so metalabel.Evaluate now withholds both grades with the reason, and
// this worker publishes that reason. (Candidate counts are approximate because
// the 40,000-row cap cuts mid-day; the contamination and purge counts are exact.)
//
// That is the correct output, not a regression to fix: the history is shorter
// than a handful of its own label spans. The withheld numbers were not neutral
// either — the last unpurged 1d grade reported a filtered expectancy of +0.19%
// per decision against a primary at -0.22%, i.e. a filter that appeared to turn a
// losing signal profitable. It was still rejected, but only because the
// edgeless-primary gate fired first. Both horizons become gradable once the
// candidate set spans enough distinct days to survive its own purge.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/metalabel"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// metaLabelSampleCap bounds the labeled rows pulled per horizon before
	// symbol-day deduplication.
	metaLabelSampleCap = 40000
	// metaLabelFolds is the minimum number of walk-forward retrain boundaries
	// demanded before a grade is considered structurally sound.
	metaLabelFolds = 4
	// metaLabelCost is the round-trip cost in return units (10bps). It matches
	// distTau and meanRevCost — three different costs for one platform would
	// make their verdicts incomparable.
	metaLabelCost = 0.001
)

// metaLabelHorizons are the horizons graded. 1h is excluded for the same reason
// the return distribution excludes it: the daily bars behind these labels cannot
// express an hourly forward return.
var metaLabelHorizons = []md.Horizon{md.H1d, md.H1w}

// MetaLabelRunner grades whether meta-labeling earns its place on top of the
// platform's own directional calls.
type MetaLabelRunner struct {
	St  *store.Store
	Now func() time.Time
}

func (w *MetaLabelRunner) Name() string { return "metalabel-runner" }

// Interval is 12h: this is a grading study over accumulated resolved outcomes,
// and its inputs move on the scale of days, not minutes. Running it hot would
// burn a write connection the fleet needs without changing the answer.
func (w *MetaLabelRunner) Interval() time.Duration { return 12 * time.Hour }

// publishedMetaLabel is one horizon's grade as stored and served.
type publishedMetaLabel struct {
	Horizon string `json:"horizon"`
	// Grade is a POINTER and omitted entirely when the horizon was not graded.
	// A zero Grade would render a full set of 0.00% expectancies and an empty
	// verdict beside the reason it could not be measured, and a withheld number
	// must be absent, never zero. Skipped horizons became the common case once
	// the walk-forward started purging: on the live data neither horizon's
	// history is long enough to support four purged retrains.
	Grade      *metalabel.Grade `json:"grade,omitempty"`
	ComputedAt int64            `json:"computedAt"`
	// Skipped carries the honest reason a horizon produced no grade at all,
	// so an empty result is never mistaken for a passing one.
	Skipped string `json:"skipped,omitempty"`
}

func (w *MetaLabelRunner) Run(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	if w.Now != nil {
		now = w.Now().Unix()
	}

	var out []publishedMetaLabel
	var summary []string

	for _, h := range metaLabelHorizons {
		rows, err := w.St.LabeledFeaturesAll(ctx, h, 0, metaLabelSampleCap)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			out = append(out, publishedMetaLabel{
				Horizon: string(h), ComputedAt: now,
				Skipped: "no resolved labeled features yet",
			})
			continue
		}

		// horizonSecs is the same source of truth the GBM leg's trainer declares
		// with, so the two purges are sized identically and their grades stay
		// comparable.
		samples, ctxKeys := metaLabelSamples(rows, horizonSecs(h))
		if len(samples) == 0 {
			out = append(out, publishedMetaLabel{
				Horizon: string(h), ComputedAt: now,
				Skipped: "no directional calls with usable context",
			})
			continue
		}

		g, err := metalabel.Evaluate(samples, metaLabelFolds, metaLabelCost, metalabel.DefaultThreshold)
		if err != nil {
			// A refusal to grade is a result, not a failure: it must not stall
			// the fleet, and it must be visible rather than silently absent.
			out = append(out, publishedMetaLabel{
				Horizon: string(h), ComputedAt: now,
				Skipped: err.Error(),
			})
			summary = append(summary, fmt.Sprintf("%s: %v", h, err))
			continue
		}

		out = append(out, publishedMetaLabel{Horizon: string(h), Grade: &g, ComputedAt: now})
		summary = append(summary, fmt.Sprintf("%s: %s (n=%d over %d days, took %d on %d days, purged %d train rows)",
			h, g.Verdict, g.N, g.TotalDays, g.TakenN, g.TakenDays, g.PurgedTrainRows))
		_ = ctxKeys
	}

	blob, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	if err := w.St.SetMeta(ctx, store.MetaMetaLabel, string(blob)); err != nil {
		return "", err
	}
	if len(summary) == 0 {
		return "no gradable horizons yet", nil
	}
	return strings.Join(summary, "; "), nil
}

// metaLabelSamples turns labeled feature rows into meta-label candidates.
// labelSpan is the primary's forward horizon in seconds.
//
// Three disciplines are enforced here rather than trusted:
//
//   - ONE OBSERVATION PER SYMBOL PER UTC DAY. Pooling intraday rows inflates n
//     roughly 60x on this platform's data and has manufactured false
//     significance twice already. The latest row of each symbol-day is kept.
//   - CONTEXT EXCLUDES THE CALL. The meta-model may see the CIRCUMSTANCES of a
//     prediction but never the prediction itself: canonicalFeatureKeys already
//     drops pred_raw/pred_cal/gbm_prob/meanrev_prob/alphax_prob, so a filter
//     cannot score well by simply re-reading the primary's own output.
//   - THE LABEL HORIZON IS DECLARED. Every candidate records when its outcome
//     resolved, which is what lets the walk-forward PURGE training rows whose
//     answer lands inside the block they are about to be graded on. It is set
//     here rather than at the call site so no path can build candidates and
//     forget: metalabel refuses to grade an undeclared set, so forgetting would
//     turn the whole surface off silently.
func metaLabelSamples(rows []store.LabeledFeature, labelSpan int64) ([]metalabel.Sample, []string) {
	ctxKeys := canonicalFeatureKeys(rows)
	if len(ctxKeys) == 0 {
		return nil, nil
	}

	// Keep the latest row per (symbol, UTC-day).
	type key struct {
		sym int64
		day int64
	}
	best := map[key]store.LabeledFeature{}
	for _, r := range rows {
		k := key{sym: r.SymbolID, day: md.TradingDay(r.Ts)}
		if cur, ok := best[k]; !ok || r.Ts > cur.Ts {
			best[k] = r
		}
	}

	samples := make([]metalabel.Sample, 0, len(best))
	for _, r := range best {
		p, ok := r.Vec["pred_cal"]
		if !ok {
			if p, ok = r.Vec["pred_raw"]; !ok {
				continue
			}
		}
		vec := flatten(r.Vec, ctxKeys)
		samples = append(samples, metalabel.Sample{
			Ts:          r.Ts,
			LabelEnd:    r.Ts + labelSpan,
			PrimaryProb: p,
			Context:     vec,
			FwdReturn:   r.FwdReturn,
		})
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Ts < samples[j].Ts })
	return samples, ctxKeys
}
