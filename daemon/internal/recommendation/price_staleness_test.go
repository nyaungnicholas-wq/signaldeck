package recommendation

import (
	"strings"
	"testing"
)

// F-2 (adversarial re-audit 2026-08-03): the recommendation API served a stale
// price under a current timestamp. Build's own comment promised expected return
// was "only defensible when BOTH a heuristic fair value and a live price
// exist", but the code only ever checked that a price EXISTED — never that it
// was live. Measured 2026-08-12: of 329 active symbols, EA's newest 1d bar was
// 9 days old and MVO's 20 days, each served with a current timestamp and no way
// for a consumer to tell.
//
// The price leg now carries PriceAgeSec, mirroring how PredAgeSec already
// reaches conviction, and expected return degrades to unavailable rather than
// quoting a return computed off a three-week-old close.

// freshDays builds the canonical bullish read with a price of a given age.
func priceAged(days int64) Inputs {
	in := bullishHigh()
	in.PriceAgeSec = days * 24 * 3600
	return in
}

func TestExpectedReturnRefusedOnStalePrice(t *testing.T) {
	rec := Build(priceAged(20))
	if rec.HasExpectedReturn {
		t.Fatalf("expected return must be unavailable on a 20-day-old price, got %.2f%%", rec.ExpectedReturnPct)
	}
	if rec.ExpectedReturnPct != 0 {
		t.Fatalf("withheld expected return must stay zero-valued, got %.2f", rec.ExpectedReturnPct)
	}
}

func TestExpectedReturnSurvivesFreshPrice(t *testing.T) {
	rec := Build(priceAged(1))
	if !rec.HasExpectedReturn {
		t.Fatal("a 1-day-old daily bar is fresh; expected return must still be available")
	}
}

// A long weekend or market holiday legitimately ages a daily bar several days.
// The gate must not fire on those or it would withhold a defensible number
// every Monday — the false positive is as bad as the defect.
func TestExpectedReturnSurvivesLongWeekend(t *testing.T) {
	for _, d := range []int64{2, 3, 4} {
		if rec := Build(priceAged(d)); !rec.HasExpectedReturn {
			t.Fatalf("%d-day-old price is a normal holiday gap; expected return must survive", d)
		}
	}
}

// PriceAgeSec is the zero value everywhere the caller does not supply it. Zero
// must mean "age unknown", never "infinitely stale", or every existing caller
// silently loses its expected return.
func TestUnsuppliedPriceAgeIsNotStale(t *testing.T) {
	in := bullishHigh()
	if in.PriceAgeSec != 0 {
		t.Fatalf("fixture should leave PriceAgeSec unset, got %d", in.PriceAgeSec)
	}
	if rec := Build(in); !rec.HasExpectedReturn {
		t.Fatal("unsupplied price age must preserve prior behaviour, not withhold")
	}
}

// The stale price is still the last known price and is still worth showing —
// what must not happen is showing it without saying how old it is.
func TestStalePriceIsDisclosedNotHidden(t *testing.T) {
	rec := Build(priceAged(20))
	if rec.CurrentPrice == 0 {
		t.Fatal("last known price should still be reported, only labelled stale")
	}
	joined := strings.ToLower(strings.Join(append(append([]string{}, rec.Risks...), rec.Assumptions...), " | "))
	if !strings.Contains(joined, "stale") && !strings.Contains(joined, "day") {
		t.Fatalf("staleness must be disclosed in risks or assumptions; got risks=%v assumptions=%v",
			rec.Risks, rec.Assumptions)
	}
}
