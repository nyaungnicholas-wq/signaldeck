package api

import (
	"context"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// CollapsedGradingWindow exposes the publication gate to callers outside the
// HTTP surface — specifically ops/accuracy-registry.sh, which generates the
// live-accuracy block that README.md and eight other documents include.
//
// WHY THIS IS EXPORTED RATHER THAN REIMPLEMENTED. The two surfaces disagreed:
// /api/accuracy refused this window (503 REFUSED, zero rows, "figures over this
// window are withheld until it clears") while the generated markdown published
// the same rows with no caveat, and README's freeze notice asserted the block
// "currently reads GRADING REFUSED" when it in fact carried a full verdict
// table. One system, two opposite answers about whether the numbers may be
// published at all.
//
// collapsedGradingWindow's own comment already states the requirement this
// satisfies — it refuses "on the SAME evidence internal/forecastmon uses, so the
// publication surface and the monitor cannot disagree about whether a day was
// usable". A second implementation in shell or python would drift from this one
// the first time either changed, which is the failure being closed here.
//
// The registry's per-horizon distinct_days defines the window, so callers must
// pass the registry the grader just wrote, not a fixed lookback. cmd/forecastmon
// takes a --days lookback and is NOT a substitute: this gate exists because a
// fixed window "would either miss a collapse just outside it or refuse forever
// because of a collapse the grader never touched".
//
// An unreadable window returns an error and reports collapsed=false. That is
// "not judged", never "passed": callers must WITHHOLD on it and say the check
// was unavailable rather than claim a collapse. /api/accuracy answers
// REFUSED_UNAVAILABLE, /api/track-record gates with the same sentence, and
// cmd/collapsecheck exits 2, which ops/accuracy-registry.sh publishes as CHECK
// UNAVAILABLE. This comment used to say callers must FAIL OPEN; the handler
// stopped doing that on 2026-09-13 and the comment outlived it.
func CollapsedGradingWindow(
	ctx context.Context,
	st *store.Store,
	registryPath string,
	now time.Time,
) (reason string, collapsed bool, err error) {
	reg, err := loadRegistry(registryPath)
	if err != nil {
		return "", false, err
	}
	return Deps{St: st, RegistryPath: registryPath}.collapsedGradingWindow(ctx, reg, now)
}
