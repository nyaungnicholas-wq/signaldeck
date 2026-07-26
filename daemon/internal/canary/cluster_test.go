package canary

import "testing"

// clusteredArm builds an arm whose observations are strongly clustered by day:
// each day is internally near-uniform (the market moved one way and the model
// called one way), which is exactly the shape of this platform's live record —
// ~1,000 symbols per day sharing one market move.
//
// It returns a Record carrying the per-day tallies, so the evaluator has the
// information it needs to resample days rather than rows.
func clusteredArm(version string, goodDays, badDays, perDay, goodHits, badHits int, baseline float64) Record {
	var tallies []DayTally
	var n, correct int
	for i := 0; i < goodDays+badDays; i++ {
		hits := goodHits
		if i >= goodDays {
			hits = badHits
		}
		tallies = append(tallies, DayTally{Day: int64(i), N: perDay, Hits: hits})
		n += perDay
		correct += hits
	}
	days := goodDays + badDays
	return Record{
		Version: version, N: n, Correct: correct, Days: days,
		FirstTs: 0, LastTs: int64(days) * day,
		BaselineAccuracy: baseline,
		DayTallies:       tallies,
	}
}

// A challenger whose apparent edge lives entirely in between-day variance must
// NOT be promoted.
//
// The data below is 20 days x 500 symbols = 10,000 observations. Eleven days
// went 90% right and nine went 5% right, pooling to 51.75%. Against a 50.0%
// incumbent the pooled Wilson lower bound is ~50.8%, which clears the 0.5pp
// promotion margin and promotes the challenger. But the accuracy is not 51.75%
// +/- 1pp: it is "the model was right on 11 of 20 market days", which a
// day-resampled interval reports as roughly a coin flip. Promoting here is
// promoting on the direction of twenty days of noise.
//
// This is the same defect clusterstat was written to remove from the display
// surfaces, still live on the gate that decides which model SERVES.
func TestClusteredChallengerIsNotPromoted(t *testing.T) {
	inc := rec("v1", 10000, 5000, 200, 0.50) // 50.0% incumbent, amply gradable
	ch := clusteredArm("v2", 11, 9, 500, 450, 25, 0.50)

	if got := ch.Accuracy(); got < 0.51 || got > 0.525 {
		t.Fatalf("fixture drifted: challenger accuracy = %.4f, want ~0.5175", got)
	}

	v := Evaluate(inc, ch)
	if v.Decision == DecisionPromote {
		t.Fatalf("challenger PROMOTED on day-clustered noise: acc=%.4f interval=[%.4f,%.4f] "+
			"vs incumbent %.4f — the interval is a row-count interval, not a day-count one",
			v.ChallengerAccuracy, v.ChallengerLower, v.ChallengerUpper, v.IncumbentAccuracy)
	}
}

// The published interval must widen to reflect day clustering. A 10,000-row
// sample spread over 20 highly-correlated days cannot carry a ~2pp interval.
func TestClusteredIntervalIsWiderThanPooled(t *testing.T) {
	ch := clusteredArm("v2", 11, 9, 500, 450, 25, 0.50)
	inc := rec("v1", 10000, 5000, 200, 0.50)

	pooledLo, pooledHi := WilsonInterval(ch.Correct, ch.N)
	v := Evaluate(inc, ch)
	got := v.ChallengerUpper - v.ChallengerLower
	pooled := pooledHi - pooledLo

	if got <= pooled*2 {
		t.Fatalf("published interval width %.4f is not meaningfully wider than the pooled "+
			"row-count width %.4f — day clustering was not accounted for", got, pooled)
	}
}

// An arm that does not report per-day tallies cannot have a cluster-corrected
// interval computed, so it must not be promotable at all. Falling back to the
// pooled interval here would reintroduce the defect by the back door; the
// honest answer is to withhold and say why.
func TestMissingDayTalliesCannotPromote(t *testing.T) {
	inc := rec("v1", 10000, 5000, 200, 0.50)
	ch := rec("v2", 10000, 6000, 200, 0.50) // 60% — would trivially promote on pooled Wilson
	ch.DayTallies = nil

	v := Evaluate(inc, ch)
	if v.Decision == DecisionPromote {
		t.Fatalf("promoted an arm with no per-day tallies: interval=[%.4f,%.4f]",
			v.ChallengerLower, v.ChallengerUpper)
	}
	if v.Reason == "" {
		t.Fatal("withheld promotion without stating a reason")
	}
}

// Too few distinct days to estimate between-day variance must withhold the
// interval rather than publish a flattering one labelled as corrected. This is
// the live v8 incumbent's shape: 1,046 rows on 2 days, which the design-effect
// estimator floors to 1.0x and would otherwise dress up as day-clustered.
func TestTooFewDaysWithholdsTheInterval(t *testing.T) {
	r := Record{
		Version: "v8", N: 1046, Correct: 483, Days: 2,
		FirstTs: 0, LastTs: 2 * day, BaselineAccuracy: 0.5,
		DayTallies: []DayTally{{Day: 0, N: 523, Hits: 240}, {Day: 1, N: 523, Hits: 243}},
	}
	lo, hi, method, deff, effN := r.Interval()
	if method != "withheld" {
		t.Fatalf("method = %q on a 2-day arm, want withheld (deff=%.2f effN=%.0f interval=[%.4f,%.4f])",
			method, deff, effN, lo, hi)
	}
	if lo != 0 || hi != 1 {
		t.Fatalf("withheld interval = [%v,%v], want [0,1]", lo, hi)
	}
}

// A genuinely good challenger — one that wins consistently ACROSS days rather
// than on a handful of lucky ones — must still be promotable. A correction that
// makes promotion impossible is not a correction, it is a broken gate.
func TestConsistentChallengerStillPromotes(t *testing.T) {
	inc := rec("v1", 10000, 5000, 200, 0.50)
	// 20 days, 500/day, every day 65% right: large edge, almost no between-day
	// variance, so day-resampling should still find it.
	ch := clusteredArm("v2", 20, 0, 500, 325, 0, 0.50)

	v := Evaluate(inc, ch)
	if v.Decision != DecisionPromote {
		t.Fatalf("a consistent 65%%-every-day challenger was not promoted: %s (%s) interval=[%.4f,%.4f]",
			v.Decision, v.Reason, v.ChallengerLower, v.ChallengerUpper)
	}
}
