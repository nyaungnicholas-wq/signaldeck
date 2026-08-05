package publication

import (
	"testing"
	"time"
)

var now = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// sufficient is a row that clears every floor and has a healthy interval, so
// each test below can express exactly one deviation from publishable.
func sufficient() GraderResult {
	return GraderResult{
		Predictor:      "directional-ensemble",
		Horizon:        "1d",
		HasInterval:    true,
		NEff:           120,
		DistinctBlocks: 14,
		WilsonUpper:    0.61,
		NullRate:       0.53,
		Observations:   500,
		HasBaseline:    true,
		CIType:         "day-clustered Wilson",
	}
}

// THE 2026-08-03 DEFECT. The graded window contracted to 9 distinct days, one
// short of the floor, so no interval published and the registry's retire flag
// read false — while the evidence store still carried the model as refuted.
// A model the record has condemned must never read as merely INSUFFICIENT.
func TestRetiredOutranksInsufficient(t *testing.T) {
	g := sufficient()
	g.HasInterval = false
	g.DistinctBlocks = 9
	g.NEff = 2257
	g.CIType = "withheld"

	v := BuildVerdict(g, []EvidenceClaim{
		{ID: "directional-ensemble-1d", Status: "refuted"},
	}, PriorVerdict{}, now)

	if v.PublicationStatus != StatusRetired {
		t.Fatalf("a refuted model with a thin current window must publish as %s, got %s",
			StatusRetired, v.PublicationStatus)
	}
	if !v.Retired || !v.RetirementSticky {
		t.Fatalf("retired=%v sticky=%v; both must be true", v.Retired, v.RetirementSticky)
	}
	if len(v.EvidenceRefs) == 0 {
		t.Fatal("the claim that condemned this row must be citable from the verdict")
	}
}

// Retirement survives the evidence store forgetting. History alone is enough.
func TestPriorRetirementIsSticky(t *testing.T) {
	v := BuildVerdict(sufficient(), nil, PriorVerdict{
		Retired:      true,
		RetireReason: "clustered Wilson upper bound below prequential null",
	}, now)

	if v.PublicationStatus != StatusRetired {
		t.Fatalf("history alone must keep a model retired, got %s", v.PublicationStatus)
	}
	if v.RetirementSource != SourceHistory {
		t.Fatalf("retirement source must name history, got %q", v.RetirementSource)
	}
}

// A healthy-looking current pass must not resurrect a condemned model. This is
// the un-retire path, and it is the one that would quietly restore a model that
// lost to guessing.
func TestGoodCurrentWindowCannotUnretire(t *testing.T) {
	g := sufficient()
	g.WilsonUpper = 0.99 // as flattering as it gets

	v := BuildVerdict(g, []EvidenceClaim{
		{ID: "directional-ensemble-1d", Status: "retired"},
	}, PriorVerdict{}, now)

	if v.Retired != true || v.PublicationStatus != StatusRetired {
		t.Fatalf("a flattering window must not un-retire: status=%s retired=%v",
			v.PublicationStatus, v.Retired)
	}
}

// Floors are authoritative even when an interval exists. The draft spec checked
// them only inside the has-interval branch, so this row reached OK.
func TestIntervalDoesNotExcuseFloors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*GraderResult)
	}{
		{"n_eff below floor", func(g *GraderResult) { g.NEff = 29 }},
		{"blocks below floor", func(g *GraderResult) { g.DistinctBlocks = 9 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sufficient()
			tc.mutate(&g)
			v := BuildVerdict(g, nil, PriorVerdict{}, now)
			if v.PublicationStatus != StatusInsufficient {
				t.Fatalf("row under a floor must publish as %s, got %s",
					StatusInsufficient, v.PublicationStatus)
			}
		})
	}
}

// A stale grader may not speak for the present — but the retirement flag it
// carries must still be correct, or a stale page reads as exoneration.
func TestStaleRefusesButKeepsRetirement(t *testing.T) {
	g := sufficient()
	g.Stale = true

	v := BuildVerdict(g, []EvidenceClaim{{ID: "c1", Status: "refuted"}}, PriorVerdict{}, now)

	if v.PublicationStatus != StatusRefusedStale {
		t.Fatalf("stale grader must refuse, got %s", v.PublicationStatus)
	}
	if !v.Retired {
		t.Fatal("a stale refusal must not drop the retirement flag")
	}
}

// Retirement outranks quarantine and a missing baseline too. Found live on
// 2026-08-04: these two checks originally sat ABOVE the retirement check, so a
// retired row that also lacked a baseline published NO_BASELINE and its
// retirement disappeared — the same defect wearing a different status.
func TestRetirementOutranksBaselineAndQuarantine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*GraderResult)
	}{
		{"no baseline", func(g *GraderResult) { g.HasBaseline = false }},
		{"quarantined", func(g *GraderResult) { g.Quarantined = true }},
		{"no observations", func(g *GraderResult) { g.Observations = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sufficient()
			tc.mutate(&g)
			v := BuildVerdict(g, []EvidenceClaim{{ID: "c", Status: "refuted"}}, PriorVerdict{}, now)
			if v.PublicationStatus != StatusRetired {
				t.Fatalf("a retired row must publish RETIRED regardless of %s, got %s",
					tc.name, v.PublicationStatus)
			}
		})
	}
}

// A predictor with nothing resolved has not STARTED, which is a different fact
// from having data whose null is unavailable. The structural predictors sit at
// live_n=0 until their forecasts mature and were reading NO_BASELINE, which
// invites "we cannot benchmark this" when the truth is "nothing has resolved".
func TestNoObservationsIsInsufficientNotNoBaseline(t *testing.T) {
	g := sufficient()
	g.Observations = 0
	g.HasBaseline = false
	g.HasInterval = false
	g.CIType = ""

	v := BuildVerdict(g, nil, PriorVerdict{}, now)

	if v.PublicationStatus != StatusInsufficient {
		t.Fatalf("a predictor with no resolved observations must publish %s, got %s",
			StatusInsufficient, v.PublicationStatus)
	}
}

// A row with no comparable null is not evidence in either direction, and must
// not be scored against an assumed 0.5.
func TestNoBaselineAndQuarantine(t *testing.T) {
	g := sufficient()
	g.HasBaseline = false
	if v := BuildVerdict(g, nil, PriorVerdict{}, now); v.PublicationStatus != StatusNoBaseline {
		t.Fatalf("missing baseline must publish as %s, got %s", StatusNoBaseline, v.PublicationStatus)
	}

	g = sufficient()
	g.Quarantined = true
	if v := BuildVerdict(g, nil, PriorVerdict{}, now); v.PublicationStatus != StatusQuarantined {
		t.Fatalf("quarantined row must publish as %s, got %s", StatusQuarantined, v.PublicationStatus)
	}
}

// The condemning path itself: floors met, interval below the null.
func TestWilsonBelowNullFails(t *testing.T) {
	g := sufficient()
	g.WilsonUpper = 0.51
	g.NullRate = 0.53

	v := BuildVerdict(g, nil, PriorVerdict{}, now)

	if v.PublicationStatus != StatusFailed {
		t.Fatalf("interval below the null must publish as %s, got %s", StatusFailed, v.PublicationStatus)
	}
	if !v.Retired || v.RetirementSource != SourceWilson {
		t.Fatalf("a FAILED row must retire and name wilson: retired=%v source=%q",
			v.Retired, v.RetirementSource)
	}
}

// The only path to OK, so that a regression anywhere above shows up as a row
// that can no longer be published rather than one that always can.
func TestCleanRowPublishes(t *testing.T) {
	v := BuildVerdict(sufficient(), []EvidenceClaim{
		{ID: "active-claim", Status: "active"},
	}, PriorVerdict{}, now)

	if v.PublicationStatus != StatusOK {
		t.Fatalf("a clean sufficient row must publish, got %s (%v)", v.PublicationStatus, v.Reasons)
	}
	if v.Retired {
		t.Fatal("a clean row must not be retired")
	}
}

// Every refusal must say which floor failed. A status a reader cannot act on is
// barely better than silence.
func TestRefusalsCarryReasons(t *testing.T) {
	g := sufficient()
	g.HasInterval = false
	g.DistinctBlocks = 9
	g.CIType = "withheld"

	v := BuildVerdict(g, nil, PriorVerdict{}, now)
	if len(v.Reasons) == 0 {
		t.Fatal("an INSUFFICIENT verdict must carry a reason")
	}
}
