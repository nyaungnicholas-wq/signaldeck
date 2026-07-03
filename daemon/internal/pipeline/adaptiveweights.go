package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// adaptiveMaxRows caps how many labeled examples per horizon one attribution
// pass consumes (newest first — the labeled set only grows).
const adaptiveMaxRows = 20000

// adaptiveShiftThreshold is the per-leg weight move that counts as a
// "material" change worth an insight.
const adaptiveShiftThreshold = 0.15

// AdaptiveWeightsWorker is the nightly learning pass: it re-attributes every
// resolved outcome to the component legs that produced it (per regime cell),
// recomputes the gated blend weights, and persists them versioned in meta.
// This is the step that makes the flywheel turn — the ensemble's weights are
// re-learned from the system's own graded history as data accrues.
type AdaptiveWeightsWorker struct {
	St *store.Store
}

func (w *AdaptiveWeightsWorker) Name() string            { return "adaptive-weights" }
func (w *AdaptiveWeightsWorker) Interval() time.Duration { return 6 * time.Hour }

func (w *AdaptiveWeightsWorker) Run(ctx context.Context) (string, error) {
	// Pool labeled examples across the predicted horizons: weights are keyed
	// by regime cell (the plan's unit of learning), and pooling reaches the
	// n>=30 honesty gate sooner without changing what is measured.
	var examples []adaptive.Example
	for _, h := range predHorizons {
		rows, err := w.St.LabeledFeatures(ctx, h, adaptiveMaxRows)
		if err != nil {
			return "", fmt.Errorf("labeled features %s: %w", h, err)
		}
		for _, r := range rows {
			legs, regime := adaptive.FromVector(r.Vec)
			examples = append(examples, adaptive.Example{
				Legs: legs, Regime: regime, Up: r.Up, FwdReturn: r.FwdReturn,
			})
		}
	}
	if len(examples) == 0 {
		return "no labeled outcomes yet — weights stay static", nil
	}

	next := adaptive.Compute(examples, time.Now().Unix())

	// Diff against the previously stored weights BEFORE overwriting, so a
	// material shift can be surfaced as an insight.
	var prev adaptive.Weights
	if raw, err := w.St.GetMeta(ctx, adaptive.MetaKey); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &prev)
	}
	shift := adaptive.MaxWeightShift(prev, next)

	if err := w.St.SetJSON(ctx, adaptive.MetaKey, next); err != nil {
		return "", fmt.Errorf("persist weights: %w", err)
	}

	learned := 0
	for _, c := range next.Cells {
		if len(c.Weights) > 0 {
			learned++
		}
	}
	if shift > adaptiveShiftThreshold && len(prev.Cells) > 0 {
		if err := w.St.InsertInsight(ctx, adaptiveShiftInsight(next, shift)); err != nil {
			return "", fmt.Errorf("insight: %w", err)
		}
	}
	return fmt.Sprintf("attributed %d labeled example(s) across %d cell(s); %d cell(s) passed the n>=%d gate (max weight shift %.2f)",
		len(examples), len(next.Cells), learned, adaptive.MinCellSamples, shift), nil
}

// adaptiveShiftInsight composes the "weights materially changed" insight with
// the evidence that justifies it (the new per-cell weights).
func adaptiveShiftInsight(w adaptive.Weights, shift float64) md.Insight {
	names := make([]string, 0, len(w.Cells))
	for n := range w.Cells {
		names = append(names, n)
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		c := w.Cells[n]
		if len(c.Weights) == 0 {
			continue
		}
		legs := make([]string, 0, len(c.Weights))
		for leg := range c.Weights {
			legs = append(legs, leg)
		}
		sort.Strings(legs)
		var ws []string
		for _, leg := range legs {
			ws = append(ws, fmt.Sprintf("%s %.2f", leg, c.Weights[leg]))
		}
		parts = append(parts, fmt.Sprintf("%s (n=%d): %s", n, c.N, strings.Join(ws, ", ")))
	}
	data, _ := json.Marshal(map[string]any{"kind": "adaptive_weights_shift", "maxShift": shift, "weights": w})
	return md.Insight{
		Scope:    "market",
		Ts:       time.Now().Unix(),
		Headline: fmt.Sprintf("Ensemble weights shifted (max move %.2f)", shift),
		Body: "The adaptive learning pass re-measured per-component edge from resolved outcomes and the blend weights moved materially. New learned weights — " +
			strings.Join(parts, "; ") +
			". Cells without enough samples keep the static equal prior (honesty gate).",
		Data: string(data),
	}
}
