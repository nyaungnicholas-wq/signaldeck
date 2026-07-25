// Package pricecheck compares the platform's stored closes against a SECOND,
// independent provider and reports disagreement.
//
// # Why a second opinion is the only check that works here
//
// Every other data check in this system is an INTERNAL consistency test:
// staleness gates, split-corruption detection, NaN guards, sane-move limits.
// All of them share one blind spot — a provider that is quietly, plausibly
// wrong. A close that is off by 40 basis points, or adjusted on a different
// convention, or stamped to the wrong session, passes every internal test
// because nothing internal contradicts it. The only thing that catches it is a
// different source disagreeing.
//
// # What is compared, and what is kept
//
// ONLY daily closes, and ONLY the derived comparison statistics — deviation in
// basis points, counts, timestamps. The second provider's prices are never
// persisted, never exported and never used to correct a bar; they are read,
// compared, and dropped. That is deliberate: this platform's data-licensing
// discipline forbids storing or redistributing vendor bars, and a validation
// check needs no such storage to do its job. A disagreement raises a data-
// quality event and lowers confidence; it does not overwrite anything, because
// "the other source said something different" is not evidence that the other
// source is right.
//
// # Alignment
//
// Series are compared on the INTERSECTION of their timestamps, normalized to
// UTC days. A timestamp present in one series and absent from the other is not
// a disagreement — it is a coverage difference, counted separately. Comparing
// unaligned series is how a check like this manufactures false alarms and
// trains its operator to ignore it.
package pricecheck

import (
	"math"
	"sort"
)

// DefaultToleranceBps is the deviation at which two closes stop being the same
// number. 25bps (0.25%) is wide enough to absorb ordinary vendor rounding and
// consolidated-vs-primary close differences, and narrow enough that a genuine
// error — a missed split, a wrong session, a stale carry-forward — clears it.
const DefaultToleranceBps = 25.0

// MinCompared is the least overlapping days needed before a verdict is issued.
// Below this, one odd day dominates the statistics.
const MinCompared = 20

// Point is one close on one day. Ts is a Unix timestamp; only its UTC day is
// used for alignment.
type Point struct {
	Ts    int64
	Close float64
}

// Disagreement is one day where the two providers materially differ.
type Disagreement struct {
	Day  int64   `json:"day"`
	Ours float64 `json:"ours"`
	// Theirs is reported for a human to adjudicate. It is a comparison
	// statistic in a diagnostic payload, not stored market data.
	Theirs    float64 `json:"theirs"`
	DevBps    float64 `json:"devBps"`
	SignedBps float64 `json:"signedBps"`
}

// Result is the verdict on one symbol.
type Result struct {
	// Compared is the number of overlapping UTC days actually checked.
	Compared int `json:"compared"`
	// OnlyOurs / OnlyTheirs are coverage differences, NOT disagreements.
	OnlyOurs   int `json:"onlyOurs"`
	OnlyTheirs int `json:"onlyTheirs"`
	// Disagreements exceeding the tolerance, worst first, capped by MaxReported.
	Disagreements []Disagreement `json:"disagreements"`
	// DisagreeCount is the full count even when the list is capped.
	DisagreeCount int `json:"disagreeCount"`
	// MaxDevBps and MedianDevBps summarize the whole compared set.
	MaxDevBps    float64 `json:"maxDevBps"`
	MedianDevBps float64 `json:"medianDevBps"`
	// MeanSignedBps is the average SIGNED deviation. A large value with a
	// consistent sign is the signature of a systematic difference — a different
	// adjustment convention — rather than sporadic errors, and the two call for
	// completely different responses.
	MeanSignedBps float64 `json:"meanSignedBps"`
	// Systematic is true when nearly every deviation shares one sign, which
	// means "these providers adjust differently", not "our data is corrupt".
	Systematic bool `json:"systematic"`
	// Agree is the headline: true when nothing exceeded tolerance.
	Agree bool `json:"agree"`
	// Confident is false when there was not enough overlap to judge. An
	// unconfident result must never be read as agreement.
	Confident bool `json:"confident"`
	// ToleranceBps actually applied.
	ToleranceBps float64 `json:"toleranceBps"`
}

// MaxReported caps the disagreement list so one badly-broken symbol cannot
// produce an unbounded payload.
const MaxReported = 20

// systematicShare is the fraction of same-signed deviations above which a
// difference is called systematic rather than sporadic.
const systematicShare = 0.9

// Compare aligns two daily close series on their shared UTC days and reports
// where they disagree. Neither input is mutated. A zero or negative tolerance
// falls back to DefaultToleranceBps — a comparison with no tolerance would flag
// float noise as a data incident.
func Compare(ours, theirs []Point, toleranceBps float64) Result {
	if toleranceBps <= 0 {
		toleranceBps = DefaultToleranceBps
	}
	res := Result{ToleranceBps: toleranceBps}

	byDayOurs := indexByDay(ours)
	byDayTheirs := indexByDay(theirs)

	var days []int64
	for d := range byDayOurs {
		if _, ok := byDayTheirs[d]; ok {
			days = append(days, d)
		} else {
			res.OnlyOurs++
		}
	}
	for d := range byDayTheirs {
		if _, ok := byDayOurs[d]; !ok {
			res.OnlyTheirs++
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	var devs []float64
	var signedSum float64
	var positive, negative int
	for _, d := range days {
		a, b := byDayOurs[d], byDayTheirs[d]
		if a <= 0 || b <= 0 {
			continue // a non-positive close is a different bug; not this check's
		}
		res.Compared++
		signed := (a - b) / b * 10000
		dev := math.Abs(signed)
		devs = append(devs, dev)
		signedSum += signed
		if signed > 0 {
			positive++
		} else if signed < 0 {
			negative++
		}
		if dev > toleranceBps {
			res.DisagreeCount++
			res.Disagreements = append(res.Disagreements, Disagreement{
				Day: d, Ours: a, Theirs: b, DevBps: dev, SignedBps: signed,
			})
		}
	}
	if res.Compared == 0 {
		return res
	}
	sort.Slice(res.Disagreements, func(i, j int) bool {
		if res.Disagreements[i].DevBps != res.Disagreements[j].DevBps {
			return res.Disagreements[i].DevBps > res.Disagreements[j].DevBps
		}
		return res.Disagreements[i].Day < res.Disagreements[j].Day
	})
	if len(res.Disagreements) > MaxReported {
		res.Disagreements = res.Disagreements[:MaxReported]
	}

	sorted := append([]float64(nil), devs...)
	sort.Float64s(sorted)
	res.MaxDevBps = sorted[len(sorted)-1]
	res.MedianDevBps = sorted[len(sorted)/2]
	res.MeanSignedBps = signedSum / float64(res.Compared)
	dominant := math.Max(float64(positive), float64(negative))
	res.Systematic = res.DisagreeCount > 0 && dominant/float64(res.Compared) >= systematicShare
	res.Agree = res.DisagreeCount == 0
	res.Confident = res.Compared >= MinCompared
	return res
}

// indexByDay collapses a series to one close per UTC day, keeping the LATEST
// timestamp within the day — the same convention the rest of the platform uses
// when deduplicating intraday rows to a daily observation.
func indexByDay(ps []Point) map[int64]float64 {
	type held struct {
		ts    int64
		close float64
	}
	tmp := map[int64]held{}
	for _, p := range ps {
		day := p.Ts - p.Ts%86400
		if cur, ok := tmp[day]; !ok || p.Ts >= cur.ts {
			tmp[day] = held{ts: p.Ts, close: p.Close}
		}
	}
	out := make(map[int64]float64, len(tmp))
	for d, h := range tmp {
		out[d] = h.close
	}
	return out
}
