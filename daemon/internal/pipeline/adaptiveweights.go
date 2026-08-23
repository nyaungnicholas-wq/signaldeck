package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// adaptiveLookbackDays is how many TRADING DAYS of labeled examples one
// attribution pass reads per horizon. Days, not rows, because that is the unit
// adaptive.Compute gates on.
//
// This read was capped at 20,000 ROWS per horizon. At roughly 4,300 labeled
// rows per trading day that bought 4.6 days, so the pooled pass spanned about 8
// distinct days against a 20-day floor and EVERY cell came back gated — the
// learner produced no weights, adaptive.Pick returned nil for every symbol, and
// the entire fleet blended on the static equal prior while 47 days of history
// sat in the table unread. Raising the constant would only postpone the next
// crossing: daily volume grows, so any row number silently becomes too small.
// Counting in the gate's own unit cannot drift that way.
//
// 1.5x the floor. The margin is for the PER-LEG floor, which is checked against
// the same MinCellDays: a leg absent on some days spans fewer than the cell
// does, and a leg that still falls short at 30 days is genuinely sparse rather
// than starved — which is a refusal worth keeping.
const adaptiveLookbackDays = 3 * adaptive.MinCellDays / 2

// adaptiveMaxRows is a SAFETY ceiling on one horizon's read, not the selection
// rule. It exists so a corpus far larger than today's cannot exhaust memory
// unnoticed; if it ever binds, capBound reports it and the remedy is to lower
// adaptiveLookbackDays deliberately rather than to truncate a span by accident.
const adaptiveMaxRows = 250000

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
	// honesty gates sooner without changing what is measured. Pooling adds
	// rows, not days — the gates count days, so this cannot buy a gate pass.
	var examples []adaptive.Example
	// capBound is now reported BY THE READ rather than inferred from its length.
	// LabeledFeaturesRecentDays drops whole days when the safety ceiling binds
	// and says so, which is the only way to tell a shortened span from a short
	// history — the exact distinction that made the old row cap invisible.
	capBound := false
	for _, h := range predHorizons {
		rows, ceilingBound, err := w.St.LabeledFeaturesRecentDays(ctx, h, adaptiveLookbackDays, adaptiveMaxRows)
		if err != nil {
			return "", fmt.Errorf("labeled features %s: %w", h, err)
		}
		if ceilingBound {
			capBound = true
		}
		for _, r := range rows {
			legs, regime := adaptive.FromVector(r.Vec)
			examples = append(examples, adaptive.Example{
				// Ts is what makes the attribution's floors and standard errors
				// count DISTINCT UTC DAYS instead of rows. Dropping it here
				// would silently restore the row-counting defect: this pass
				// pools ~40,000 rows that span only 12 days.
				Legs: legs, Regime: regime, Ts: r.Ts, Up: r.Up, FwdReturn: r.FwdReturn,
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

	// Model-evolution wave: meta keeps only the LATEST weights, so also append
	// this run's learned weights to weight_history (one row per non-empty
	// (cell, leg)) — the append-only series behind /api/model-evolution. Only
	// cells that actually yielded learned weights are recorded; a cell held to
	// the static prior has nothing to plot (honest gap, no synthetic zero).
	if hist := weightHistoryRows(next); len(hist) > 0 {
		if err := w.St.InsertWeightHistory(ctx, hist); err != nil {
			return "", fmt.Errorf("append weight history: %w", err)
		}
	}

	learned, days := 0, 0
	for _, c := range next.Cells {
		if len(c.Weights) > 0 {
			learned++
		}
		if c.Days > days {
			days = c.Days
		}
	}
	if shift > adaptiveShiftThreshold && len(prev.Cells) > 0 {
		if err := w.St.InsertInsight(ctx, adaptiveShiftInsight(next, shift)); err != nil {
			return "", fmt.Errorf("insight: %w", err)
		}
	}
	// Report the DAY count beside the row count on every surface: a row total
	// with no day total is the number that made three days of evidence look
	// like a sample of 40,000.
	msg := fmt.Sprintf("attributed %d labeled row(s) spanning %d distinct day(s) across %d cell(s); %d cell(s) yielded learned weights (floors: %d rows AND %d days; %d panel test(s) at family-wise alpha %.2f; max weight shift %.2f)",
		len(examples), days, len(next.Cells), learned,
		adaptive.MinCellSamples, adaptive.MinCellDays,
		next.Panel.Tests, next.Panel.Alpha, shift)
	return msg + capBindingNote(capBound, learned, days), nil
}

// capBindingNote names the one state in which "below the N-day floor" is
// misleading: every cell gated on days while the read came back truncated.
// Read plainly, the floor message sends an operator away to wait for history
// that is already there — a row cap is hiding it. Empty string when the day
// count is honest, so the normal detail line is unchanged.
func capBindingNote(capBound bool, learned, days int) string {
	if !capBound || learned > 0 || days >= adaptive.MinCellDays {
		return ""
	}
	return fmt.Sprintf("; ROW CAP BINDING — the per-horizon read returned its full %d-row limit, so %d day(s) is what the cap ALLOWED, not what exists. A day floor cannot be reached by waiting while its input is capped by rows; widen the read to a day window before reading this as insufficient history",
		adaptiveMaxRows, days)
}

// weightHistoryRows flattens a computed weight set into append-only history
// rows (one per non-empty (cell, leg)), stamped with the weights' computed ts so
// the snapshot lines up with the meta version. Cells with no learned weights
// contribute nothing — an honest gap, never a synthetic zero.
func weightHistoryRows(next adaptive.Weights) []store.WeightHistoryRow {
	names := make([]string, 0, len(next.Cells))
	for n := range next.Cells {
		names = append(names, n)
	}
	sort.Strings(names)
	var rows []store.WeightHistoryRow
	for _, name := range names {
		c := next.Cells[name]
		for _, leg := range ensemble.LegNames {
			if wgt, ok := c.Weights[leg]; ok {
				rows = append(rows, store.WeightHistoryRow{
					Ts: next.ComputedTs, Regime: name, Leg: leg, Weight: wgt,
				})
			}
		}
	}
	return rows
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
		parts = append(parts, fmt.Sprintf("%s (n=%d rows over %d days): %s", n, c.N, c.Days, strings.Join(ws, ", ")))
	}
	data, _ := json.Marshal(map[string]any{"kind": "adaptive_weights_shift", "maxShift": shift, "weights": w})
	return md.Insight{
		Scope:    "market",
		Ts:       time.Now().Unix(),
		Headline: fmt.Sprintf("Ensemble weights shifted (max move %.2f)", shift),
		Body: "The adaptive learning pass re-measured per-component edge from resolved outcomes and the blend weights moved materially. New learned weights — " +
			strings.Join(parts, "; ") +
			". Cells without enough independent days keep the static equal prior (honesty gate).",
		Data: string(data),
	}
}
