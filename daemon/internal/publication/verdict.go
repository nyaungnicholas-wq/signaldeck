// Package publication decides what a grading result is allowed to say in
// public, and in particular whether a model the record has already contradicted
// can ever read as healthy again.
//
// WHY THIS EXISTS. On 2026-08-03 the two surfaces that describe the flagship
// model disagreed. The accuracy registry carried retire=false on every
// directional row, because the graded window had contracted to 9 distinct
// UTC days — one short of the 10-block floor — so no interval published, so the
// auto-retire rule (which needs an interval to compare against the null) could
// not fire. Meanwhile evidence_claims still carried those same models as
// refuted/retired from the larger window that had condemned them. A reader of
// the registry saw a model with no verdict; a reader of the evidence store saw
// a model that had failed. Nothing was lying, and the composite was false.
//
// The failure mode is specific and worth naming, because it will recur: a
// shrinking sample silently withdraws a verdict. Every honest refusal floor in
// this system has that shape — it declines to speak — and a refusal to speak
// about a model that has already been condemned reads, to anyone who is not
// holding the history in their head, as exoneration. Retirement therefore has
// to be a property of the record rather than of the current window.
//
// The rule this package enforces: retirement is one-way. Evidence retires a
// model, a failed interval retires a model, and history keeps it retired. No
// amount of subsequent thin data un-retires anything. Only the deliberate,
// visible act of deleting the record can.
package publication

import "time"

// Floors. These are the same numbers the grader publishes in
// data/accuracy_registry.json (min_independent_n, min_distinct_blocks) and they
// are duplicated here on purpose: this package must be able to refuse a row on
// its own, without trusting the caller to have applied them. Raising them is a
// judgement call; lowering them is a doctrine violation.
const (
	MinIndependentN   = 30.0
	MinDistinctBlocks = 10
)

// Publication statuses. These are the strings that reach the API and the UI.
const (
	StatusOK           = "OK"
	StatusInsufficient = "INSUFFICIENT"
	StatusFailed       = "FAILED"
	StatusRetired      = "RETIRED"
	StatusRefusedStale = "REFUSED_STALE"
	StatusNoBaseline   = "NO_BASELINE"
	StatusQuarantined  = "QUARANTINED"
)

// Retirement sources, recorded so a reader can tell WHICH gate condemned a row.
const (
	SourceGrader   = "grader"
	SourceEvidence = "evidence"
	SourceHistory  = "history"
	SourceWilson   = "wilson"
)

// GraderResult is one row of the current grading pass.
type GraderResult struct {
	Predictor      string
	Horizon        string
	Variant        string
	Stale          bool
	HasInterval    bool
	NEff           float64
	DistinctBlocks int
	WilsonUpper    float64
	NullRate       float64
	// Observations is the raw resolved-row count. Zero means the predictor has
	// not started, which must not be confused with having data whose baseline
	// is unavailable.
	Observations int
	// HasBaseline is false when the row has no comparable null at all (the
	// quarantined NULL-label rows). Such a row is not evidence in either
	// direction and must never be scored against an assumed 0.5.
	HasBaseline  bool
	Quarantined  bool
	Retired      bool
	CIType       string
	GraderSHA256 string
}

// EvidenceClaim is one claim from the evidence store.
type EvidenceClaim struct {
	ID     string
	Status string // "refuted" | "retired" | "active" | "weak"
}

// PriorVerdict is what this (predictor, horizon, variant) was last told to be.
// Passing the retirement history in is what makes retirement survive a claim
// being edited, a window shrinking, or an evidence row being seeded away.
type PriorVerdict struct {
	Retired       bool
	RetireReason  string
	EvidenceRefs  []string
	RetirementFor string // the source that first condemned it
}

// Verdict is the publishable state of one row.
type Verdict struct {
	Predictor         string    `json:"predictor"`
	Horizon           string    `json:"horizon"`
	Variant           string    `json:"variant"`
	PublicationStatus string    `json:"publication_status"`
	Retired           bool      `json:"retired"`
	RetirementSticky  bool      `json:"retirement_sticky"`
	RetirementSource  string    `json:"retirement_source,omitempty"`
	Reasons           []string  `json:"reasons"`
	EvidenceRefs      []string  `json:"evidence_refs"`
	AsOf              time.Time `json:"as_of"`
}

// meetsFloors reports whether the row carries enough independent evidence to be
// spoken about at all. Both floors, always — an interval computed over 9
// distinct days is still an interval, and it is still not evidence.
func meetsFloors(g GraderResult) bool {
	return g.NEff >= MinIndependentN && g.DistinctBlocks >= MinDistinctBlocks
}

// BuildVerdict resolves one row's publication state.
//
// Order matters and is deliberate:
//
//  1. Retirement is computed FIRST, from every source (history, evidence, the
//     grader's own flag), so that whatever branch returns below, the Retired
//     flag on the returned Verdict is already correct. A refused or insufficient
//     row must still carry retired=true if the record condemned it.
//  2. Staleness refuses publication outright. A stale grader knows nothing about
//     now, so nothing it says may be published as current.
//  3. Quarantine and missing baseline are declared before any accuracy talk:
//     a row with no comparable null is not evidence in either direction.
//  4. Retirement outranks thin data. A model already condemned reads RETIRED,
//     never INSUFFICIENT — that substitution is precisely the 2026-08-03 defect.
//  5. Floors are authoritative and are checked independently of HasInterval.
//     The spec draft checked them only inside the has-interval branch, which let
//     a row with an interval but n_eff < 30 fall through to OK.
//  6. Only then may a sufficient row be condemned by its own interval, or pass.
func BuildVerdict(current GraderResult, evidence []EvidenceClaim, prior PriorVerdict, now time.Time) Verdict {
	reasons := []string{}
	evidenceRefs := []string{}
	retired := false
	source := ""

	// (1) Retirement, from every source that can establish it.
	if prior.Retired {
		retired = true
		source = SourceHistory
		reasons = append(reasons, "retirement is sticky: this row was retired by an earlier grade and cannot be un-retired by a later one")
		if prior.RetireReason != "" {
			reasons = append(reasons, "original reason: "+prior.RetireReason)
		}
		evidenceRefs = append(evidenceRefs, prior.EvidenceRefs...)
	}
	for _, claim := range evidence {
		if claim.Status == "refuted" || claim.Status == "retired" {
			retired = true
			if source == "" {
				source = SourceEvidence
			}
			reasons = append(reasons, "historical evidence claim marks this model refuted/retired")
			evidenceRefs = append(evidenceRefs, claim.ID)
		}
	}
	if current.Retired {
		retired = true
		if source == "" {
			source = SourceGrader
		}
		reasons = append(reasons, "current grader marked this row retired")
	}

	v := Verdict{
		Predictor:        current.Predictor,
		Horizon:          current.Horizon,
		Variant:          current.Variant,
		Retired:          retired,
		RetirementSticky: retired,
		RetirementSource: source,
		Reasons:          reasons,
		EvidenceRefs:     dedupe(evidenceRefs),
		AsOf:             now.UTC(),
	}

	// (2) A stale grader may not speak for the present.
	if current.Stale {
		v.PublicationStatus = StatusRefusedStale
		v.Reasons = append(v.Reasons, "grader result is stale; no current number is published")
		return v
	}

	// (3) Condemned outranks EVERYTHING below. This is the 2026-08-03 fix, and
	// it sits this high deliberately: it was originally placed below the
	// quarantine and baseline checks, which meant a retired row that also
	// lacked a baseline published as NO_BASELINE and its retirement vanished —
	// the same defect in a different costume. Every status below this line is a
	// statement about the CURRENT window, and no statement about the current
	// window may overwrite what the record already established.
	if v.Retired {
		v.PublicationStatus = StatusRetired
		v.Reasons = append(v.Reasons,
			"model is retired; the current window may be too thin to publish an interval, which does not restore it")
		return v
	}

	// (4) A row with no observations at all has not started, which is a
	// different fact from having observations whose null is unavailable.
	// Reporting the first as NO_BASELINE reads as "we cannot benchmark this"
	// when the truth is "nothing has resolved yet" — the structural predictors,
	// which sit at live_n=0 until their forecasts mature, are exactly this case.
	if current.Observations <= 0 {
		v.PublicationStatus = StatusInsufficient
		v.Reasons = append(v.Reasons,
			"no resolved observations yet; this predictor's forecasts have not matured")
		return v
	}

	if current.Quarantined {
		v.PublicationStatus = StatusQuarantined
		v.Reasons = append(v.Reasons, "row is quarantined and is excluded from every benchmark denominator")
		return v
	}
	if !current.HasBaseline {
		v.PublicationStatus = StatusNoBaseline
		v.Reasons = append(v.Reasons, "no comparable null for this row; accuracy alone is not evidence")
		return v
	}

	// (5) Floors, authoritative, independent of whether an interval exists.
	if !meetsFloors(current) || !current.HasInterval {
		v.PublicationStatus = StatusInsufficient
		v.Reasons = append(v.Reasons, insufficientReason(current))
		return v
	}

	// (6) A sufficient row may now be condemned by its own interval.
	if current.WilsonUpper < current.NullRate {
		v.PublicationStatus = StatusFailed
		v.Retired = true
		v.RetirementSticky = true
		v.RetirementSource = SourceWilson
		v.Reasons = append(v.Reasons, "clustered Wilson upper bound below prequential null")
		return v
	}

	v.PublicationStatus = StatusOK
	return v
}

// insufficientReason names WHICH floor failed, so the refusal is legible rather
// than a bare status. A refusal a reader cannot act on is barely better than
// silence.
func insufficientReason(g GraderResult) string {
	switch {
	case !g.HasInterval:
		return "no interval published for this row: " + ciNote(g.CIType)
	case g.NEff < MinIndependentN && g.DistinctBlocks < MinDistinctBlocks:
		return "below both floors: effective n and distinct blocks are each short of the minimum"
	case g.NEff < MinIndependentN:
		return "below the independent-observation floor"
	default:
		return "below the distinct-block floor: the observations do not span enough independent periods"
	}
}

func ciNote(ciType string) string {
	if ciType == "" {
		return "interval withheld"
	}
	return "interval " + ciType
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
