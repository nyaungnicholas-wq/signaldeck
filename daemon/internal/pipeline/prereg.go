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
	// Newest stored spec hash per kind. A kind already registered whose spec
	// hash has CHANGED is not a no-op: the claim in code no longer matches the
	// claim on the chain, and skipping it would let the two diverge silently —
	// which is the exact failure pre-registration exists to prevent. It gets an
	// AMENDMENT record instead, so the chain carries both the original claim and
	// the correction, in order, and neither can be mistaken for the other.
	latest, err := w.St.LatestPreregHashes(ctx)
	if err != nil {
		return "", err
	}
	now := w.now().Unix()
	written, amended := 0, 0
	for _, spec := range prereg.Specs() {
		if have[spec.Kind] {
			if prior, ok := latest[spec.Kind]; ok && prior != spec.Hash() {
				blob, err := json.Marshal(spec)
				if err != nil {
					return "", fmt.Errorf("marshal amendment %s: %w", spec.Kind, err)
				}
				if _, err := w.St.AppendPrereg(ctx, prereg.Record{
					Ts: now, Kind: spec.Kind,
					SpecJSON: string(blob), SpecHash: spec.Hash(),
					Note: "AMENDMENT — the claim in code changed after registration. The prior record " +
						"stands unaltered above this one; read them in order. An amendment is evidence of a " +
						"correction, not a replacement of the original commitment.",
				}); err != nil {
					return "", fmt.Errorf("append amendment %s: %w", spec.Kind, err)
				}
				amended++
			}
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
	if written == 0 && amended == 0 {
		return "all predictor claims already pre-registered", nil
	}
	if written == 0 {
		return fmt.Sprintf("appended %d AMENDMENT record(s) — a registered claim changed in code", amended), nil
	}
	return fmt.Sprintf("pre-registered %d predictor claims (first gradable %s), %d amendment(s)",
		written, prereg.FirstGradableOn, amended), nil
}
