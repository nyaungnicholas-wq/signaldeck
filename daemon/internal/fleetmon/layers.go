package fleetmon

import "fmt"

// ── ARCHITECTURAL LAYER COVERAGE ────────────────────────────────────────────
//
// The metrics above answer "is the platform healthy". This answers a different
// question the platform could not previously answer about itself: "which parts of
// the intended architecture actually exist, and which of those are actually being
// consulted".
//
// It exists because of a failure mode that is invisible to every other check
// here. A subsystem can be complete, tested, and correct while nothing in the
// serving path ever calls it — at which point every metric it would have produced
// is simply absent, and absence renders as a quiet gap rather than as a fault. A
// risk engine that computes limits nobody enforces and a monitoring surface
// nobody reads both look exactly like features from the outside.
//
// So Live is a first-class field, separate from Implemented, and the pair
// Implemented && !Live is reported as a BREACH rather than as a note. That is the
// one judgement this file makes, and it is the whole reason it is not just a
// static list in a README: a README cannot notice that it has gone stale.

// Layer is one architectural layer's state.
type Layer struct {
	// Name is the layer as the architecture refers to it.
	Name string `json:"name"`
	// Implemented — the code exists and is tested.
	Implemented bool `json:"implemented"`
	// Live — something in the serving or scheduled path actually calls it. A
	// layer that is implemented but not live produces no measurements, and its
	// silence must not be mistaken for health.
	Live bool `json:"live"`
	// Enforcing — the layer can CHANGE an outcome (refuse a trade, drop a
	// feature, stop a model emitting) rather than only describing one. Nil where
	// the distinction does not apply: a regime classifier is not supposed to
	// enforce anything.
	Enforcing *bool `json:"enforcing,omitempty"`
	// Detail is where it lives and what it does, in one line.
	Detail string `json:"detail"`
	// Evidence points at what proves Live — the worker name, the route, the call
	// site. Present so a Live=true claim is checkable rather than asserted.
	Evidence string `json:"evidence,omitempty"`
}

// Bool is a helper for the Enforcing tri-state, so a caller must write the nil
// deliberately rather than getting it from a zero value.
func Bool(v bool) *bool { return &v }

// LayerCoverage summarizes the layer list.
type LayerCoverage struct {
	Layers []Layer `json:"layers"`

	Implemented int `json:"implemented"`
	LiveCount   int `json:"live"`
	// BuiltNotLive is the count of implemented layers nothing calls — the gap
	// this section exists to surface.
	BuiltNotLive int `json:"builtNotLive"`
	// NotImplemented counts layers deliberately or not yet built. Deliberate
	// absence is still absence and is reported, not hidden.
	NotImplemented int `json:"notImplemented"`

	Note string `json:"note"`
}

// SummarizeLayers computes the coverage counts and the breaches an
// implemented-but-dormant layer implies.
//
// Returned breaches are DEGRADED-grade, not critical: a dormant subsystem is a
// gap in what the platform can see or enforce, which is serious, but it is not
// the platform actively doing the wrong thing. Assemble folds them in at that
// severity.
func SummarizeLayers(layers []Layer) (LayerCoverage, []string) {
	c := LayerCoverage{Layers: layers}
	var breaches []string

	for _, l := range layers {
		switch {
		case !l.Implemented:
			c.NotImplemented++
		case l.Live:
			c.Implemented++
			c.LiveCount++
		default:
			c.Implemented++
			c.BuiltNotLive++
			breaches = append(breaches, fmt.Sprintf(
				"%s is built but nothing calls it — every measurement it would produce is absent, and absence is not health",
				l.Name))
		}
		// An enforcement layer that is live but cannot change an outcome is the
		// subtler version of the same problem: it runs, it reports, and it stops
		// nothing.
		if l.Implemented && l.Live && l.Enforcing != nil && !*l.Enforcing {
			breaches = append(breaches, fmt.Sprintf(
				"%s runs but enforces nothing — it can describe an outcome it cannot change", l.Name))
		}
	}

	c.Note = fmt.Sprintf(
		"%d of %d implemented layers are actually consulted; %d built-but-dormant, %d not implemented. Live is asserted with the worker or route that proves it, so this list cannot go stale the way a design document does.",
		c.LiveCount, c.Implemented, c.BuiltNotLive, c.NotImplemented)
	return c, breaches
}
