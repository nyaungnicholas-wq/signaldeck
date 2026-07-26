// prereg-registrar — writes the frozen predictor claims into the chain, once,
// before their forecasts start resolving.
//
// Timing is the whole point: the value of a pre-registration collapses to zero
// the moment it could have been written after seeing results. So this runs at
// daemon start, appends only the kinds not already present, and never rewrites
// an existing row. If it has not run before the first grade lands, the record
// it writes is worth strictly less — and the payload says which side of that
// line it fell on rather than leaving the reader to work it out from dates.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// PreregRegistrar appends missing pre-registration records.
type PreregRegistrar struct {
	St  *store.Store
	Now func() time.Time
}

func (w *PreregRegistrar) Name() string { return "prereg-registrar" }

// Interval is long because this is a one-time write that self-heals: after the
// first pass every kind is present and each run is a single SELECT.
func (w *PreregRegistrar) Interval() time.Duration { return 12 * time.Hour }

func (w *PreregRegistrar) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *PreregRegistrar) Run(ctx context.Context) (string, error) {
	have, err := w.St.PreregKinds(ctx)
	if err != nil {
		return "", err
	}
	now := w.now().Unix()
	written := 0
	for _, spec := range prereg.Specs() {
		if have[spec.Kind] {
			continue
		}
		blob, err := json.Marshal(spec)
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", spec.Kind, err)
		}
		if _, err := w.St.AppendPrereg(ctx, prereg.Record{
			Ts: now, Kind: spec.Kind,
			SpecJSON: string(blob), SpecHash: spec.Hash(),
			Note: "initial registration — written before any forecast of this kind resolved",
		}); err != nil {
			return "", fmt.Errorf("append %s: %w", spec.Kind, err)
		}
		written++
	}
	if written == 0 {
		return "all predictor claims already pre-registered", nil
	}
	return fmt.Sprintf("pre-registered %d predictor claims (first gradable %s)",
		written, prereg.FirstGradableOn), nil
}
