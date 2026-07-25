// Package splitfix detects unadjusted-split corruption in stored daily bars.
//
// THE BUG THIS EXISTS FOR
// -----------------------
// Bars are fetched with adjustment=split, so the provider returns history
// adjusted as of the REQUEST time. Incremental fetches only pull bars newer
// than what we already stored — so when a split occurs, the provider silently
// re-adjusts its whole history while our stored history keeps the OLD basis.
// The result is a permanent discontinuity at the split date: pre-split bars at
// pre-split prices, post-split bars at post-split prices, joined into one
// series that every downstream feature reads as a real ±50-95% one-day move.
//
// The 2026-07-17 audit measured 1,079 such jumps in full history and 128
// symbols with one inside the last 400 sessions — enough to poison live
// trend/vol forecasts for those names. Predictors currently REFUSE contaminated
// windows (structregime/volregime maxSaneReturn=0.65), which is honest but
// leaves the symbol permanently unforecastable. This package finds the damage
// so a re-backfill can repair it at the source.
//
// WHY A RATIO TEST AND NOT JUST A THRESHOLD
// -----------------------------------------
// A real crash and an unadjusted split both produce a large one-day move. They
// are distinguishable: a split moves price by a near-exact simple ratio (2:1,
// 3:1, 1:10) while leaving the traded VALUE continuous, and it moves volume by
// the inverse. A crash lands on no particular ratio. Requiring proximity to a
// clean ratio is what keeps genuine market moves — including 2020-03 and
// meme-stock spikes — out of the repair queue.
package splitfix

import (
	"math"
	"sort"
	"time"
)

// Bar is the minimal shape the detector needs.
type Bar struct {
	Ts     int64
	Close  float64
	Volume float64
}

// Suspect is one detected discontinuity.
type Suspect struct {
	Ts          int64   `json:"ts"`         // the bar AT which the jump appears
	Date        string  `json:"date"`       // UTC date, for logs and issues
	PrevClose   float64 `json:"prevClose"`
	Close       float64 `json:"close"`
	Ratio       float64 `json:"ratio"`      // prevClose/close — the implied split factor
	NearestName string  `json:"nearest"`    // e.g. "2:1", "1:10 (reverse)"
	RatioError  float64 `json:"ratioError"` // |ratio - nearest| / nearest
	VolumeCorro bool    `json:"volumeCorroborated"`
	Confidence  string  `json:"confidence"` // high | medium
}

// Report is one symbol's verdict.
type Report struct {
	Suspects []Suspect `json:"suspects"`
	// Recent is true when any suspect falls inside RecentSessions of the end of
	// the series — those are the ones actively corrupting live forecasts.
	Recent bool `json:"recent"`
}

const (
	// MinJump is the smallest |1 - ratio| worth examining.
	//
	// Deliberately BELOW the predictors' maxSaneReturn=0.65 wild-move guard,
	// because that guard has a hole this package exists to close: a 2:1 split —
	// the most common kind there is — moves price exactly -50%, so it slips
	// under 0.65 entirely. Those splits are not refused and not detected; they
	// corrupt silently. 0.30 reaches down to 3:2 (-33%) and catches every
	// larger factor.
	MinJump = 0.30

	// ImpossibleJump is the move size above which no ordinary market action
	// explains the bar, so the ratio test alone is sufficient evidence.
	ImpossibleJump = 0.65

	// MaxRatioError is how far an observed ratio may sit from a known split
	// factor and still be called a split, for moves above ImpossibleJump. 4%
	// absorbs the real overnight move riding along with the split.
	MaxRatioError = 0.04

	// MaxRatioErrorSmall applies in the [MinJump, ImpossibleJump) band, where a
	// genuine crash is plausible and the prior against "split" is higher — so
	// the ratio must be tighter AND corroborated (see Detect).
	MaxRatioErrorSmall = 0.02

	// TightRatioError is precise enough to stand alone without volume: landing
	// within 0.5% of an exact integer ratio does not happen by chance.
	TightRatioError = 0.005

	// RecentSessions bounds "actively harming live forecasts" — the longest
	// lookback any shipped predictor uses (200-day SMA plus warmup).
	RecentSessions = 400
)

// knownRatios are the split factors that actually occur. Forward splits divide
// the price (ratio > 1); reverse splits multiply it (ratio < 1).
var knownRatios = []struct {
	R    float64
	Name string
}{
	{2, "2:1"}, {3, "3:1"}, {4, "4:1"}, {5, "5:1"}, {6, "6:1"},
	{7, "7:1"}, {8, "8:1"}, {10, "10:1"}, {15, "15:1"}, {20, "20:1"},
	{1.5, "3:2"}, {2.5, "5:2"},
	{1.0 / 2, "1:2 (reverse)"}, {1.0 / 3, "1:3 (reverse)"},
	{1.0 / 4, "1:4 (reverse)"}, {1.0 / 5, "1:5 (reverse)"},
	{1.0 / 6, "1:6 (reverse)"}, {1.0 / 8, "1:8 (reverse)"},
	{1.0 / 10, "1:10 (reverse)"}, {1.0 / 15, "1:15 (reverse)"},
	{1.0 / 20, "1:20 (reverse)"}, {1.0 / 25, "1:25 (reverse)"},
	{1.0 / 30, "1:30 (reverse)"}, {1.0 / 50, "1:50 (reverse)"},
}

// nearestRatio returns the closest known split factor and the relative error.
func nearestRatio(r float64) (name string, relErr float64) {
	best, bestErr := "", math.Inf(1)
	for _, k := range knownRatios {
		e := math.Abs(r-k.R) / k.R
		if e < bestErr {
			best, bestErr = k.Name, e
		}
	}
	return best, bestErr
}

// Detect scans a symbol's daily bars for unadjusted-split discontinuities.
// Bars need not be sorted. A series shorter than two bars yields no suspects.
func Detect(bars []Bar) Report {
	if len(bars) < 2 {
		return Report{}
	}
	b := append([]Bar(nil), bars...)
	sort.Slice(b, func(i, j int) bool { return b[i].Ts < b[j].Ts })

	var rep Report
	lastIdx := len(b) - 1
	for i := 1; i < len(b); i++ {
		prev, cur := b[i-1], b[i]
		if prev.Close <= 0 || cur.Close <= 0 {
			continue // a non-positive close is a different data defect
		}
		move := math.Abs(cur.Close/prev.Close - 1)
		if move < MinJump {
			continue
		}
		ratio := prev.Close / cur.Close
		name, relErr := nearestRatio(ratio)

		// Volume corroboration: a forward split multiplies share count, so
		// volume should move roughly inversely to price.
		volCorro := false
		if prev.Volume > 0 && cur.Volume > 0 {
			volRatio := cur.Volume / prev.Volume
			if ratio > 0 && math.Abs(volRatio-ratio)/ratio < 0.6 {
				volCorro = true
			}
		}

		// Evidence required scales with how explainable the move is by ordinary
		// market action. Above ImpossibleJump nothing legitimate produces the
		// bar, so a near-exact split ratio settles it. Below it, a crash is a
		// live alternative, so demand a tighter ratio AND either volume
		// corroboration or ratio precision no coincidence would reach.
		if move >= ImpossibleJump {
			if relErr > MaxRatioError {
				// A huge move on no split ratio — a real crash. Leaving it
				// alone is the entire point of the ratio test.
				continue
			}
		} else {
			if relErr > MaxRatioErrorSmall {
				continue
			}
			if !volCorro && relErr > TightRatioError {
				continue
			}
		}

		conf := "medium"
		if volCorro || relErr < TightRatioError {
			conf = "high"
		}
		rep.Suspects = append(rep.Suspects, Suspect{
			Ts:          cur.Ts,
			Date:        time.Unix(cur.Ts, 0).UTC().Format("2006-01-02"),
			PrevClose:   prev.Close,
			Close:       cur.Close,
			Ratio:       ratio,
			NearestName: name,
			RatioError:  relErr,
			VolumeCorro: volCorro,
			Confidence:  conf,
		})
		if lastIdx-i < RecentSessions {
			rep.Recent = true
		}
	}
	return rep
}
