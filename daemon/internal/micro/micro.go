// Package micro derives order-book MICROSTRUCTURE features from a window of
// crypto 1-second snapshots (snapshots_1s). Crypto 1h is SignalDeck's cleanest
// high-N channel: the book updates every second, 24/7, on a free feed, so
// microstructure carries real information the daily-bar features cannot see.
//
// Everything here is a pure function of a snapshot window ending at (or before)
// the decision time — NO LOOKAHEAD. The caller passes only snapshots at ts <=
// the prediction bar; a feature can never depend on a future snapshot.
//
// # Features (all bounded / scale-free so they compose with the other legs)
//
//   - imbalance      : mean signed order-book imbalance over the window, in
//     [-1,+1] (bid-heavy positive). Persistent buy/sell
//     pressure.
//   - imbalance_last : the most-recent snapshot's signed imbalance (the current
//     book lean, not the average).
//   - signed_vol     : signed "flow" proxy = mean of sign(mid change) weighted by
//     |mid change|, normalized by mid — a cheap tick-rule
//     order-flow estimate from mid moves (we have no per-trade
//     tape on the free feed, so mid-move direction is the honest
//     available proxy). In [-1,+1]-ish, clamped.
//   - spread_bps     : mean bid/ask spread in basis points of mid (liquidity /
//     cost proxy). Non-negative.
//   - wmid_mid_bps   : mean (weighted-mid − mid) in basis points of mid — the
//     micro-price skew, a well-known short-horizon predictor of
//     the next mid move.
//
// Absent / thin data yields ok=false; the caller then simply omits these
// features (absence is information, not zero).
package micro

import "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"

// MinSnaps is the fewest snapshots in the window before microstructure features
// are trusted. Below this the window is too thin to average meaningfully.
const MinSnaps = 5

// Features is the derived microstructure feature bundle. All fields are on
// scale-free, bounded scales so they blend with the daily-bar features without
// dominating. Names map 1:1 to the feature-vector keys the pipeline writes.
type Features struct {
	Imbalance     float64 // mean signed imbalance, [-1,1]
	ImbalanceLast float64 // latest signed imbalance, [-1,1]
	SignedVol     float64 // tick-rule signed flow proxy, ~[-1,1]
	SpreadBps     float64 // mean spread in bps of mid, >=0
	WmidMidBps    float64 // mean (wmid-mid) in bps of mid
	N             int     // snapshots used
}

// Extract derives microstructure Features from a time-ordered window of
// snapshots (ascending ts, all at or before the decision time). ok=false when
// there are fewer than MinSnaps usable snapshots. Snapshots with a non-positive
// mid are skipped for the mid-relative features but still counted for imbalance.
//
// No value depends on any snapshot after the last element, so passing only
// snapshots up to the prediction bar guarantees no lookahead.
func Extract(snaps []marketdata.Snap1s) (Features, bool) {
	if len(snaps) < MinSnaps {
		return Features{}, false
	}
	var (
		imbSum        float64
		imbN          int
		spreadSum     float64
		spreadN       int
		wmidSum       float64
		wmidN         int
		signedFlowSum float64
		signedFlowW   float64
		prevMid       float64
		havePrev      bool
		lastValidImb  float64
		haveLastValid bool
	)
	for _, s := range snaps {
		// Imbalance is defined even without a positive mid.
		imbSum += clamp1(s.ImbSigned)
		imbN++
		lastValidImb = clamp1(s.ImbSigned)
		haveLastValid = true

		if s.Mid > 0 {
			// Spread in bps of mid. Prefer the recorded spread; fall back to
			// ask-bid when spread wasn't populated but a book was.
			spread := s.Spread
			if spread <= 0 && s.Ask > 0 && s.Bid > 0 && s.Ask >= s.Bid {
				spread = s.Ask - s.Bid
			}
			if spread > 0 {
				spreadSum += spread / s.Mid * 10_000
				spreadN++
			}
			// Micro-price skew in bps of mid.
			wmidSum += (s.WMid - s.Mid) / s.Mid * 10_000
			wmidN++
			// Tick-rule signed flow: sign of the mid move, weighted by its size.
			if havePrev && prevMid > 0 {
				d := (s.Mid - prevMid) / prevMid
				signedFlowSum += d     // magnitude carries the weight
				signedFlowW += absf(d) // for normalization
			}
			prevMid = s.Mid
			havePrev = true
		}
	}
	if imbN == 0 || !haveLastValid {
		return Features{}, false
	}
	f := Features{N: len(snaps), ImbalanceLast: lastValidImb}
	f.Imbalance = imbSum / float64(imbN)
	if spreadN > 0 {
		f.SpreadBps = spreadSum / float64(spreadN)
	}
	if wmidN > 0 {
		f.WmidMidBps = wmidSum / float64(wmidN)
	}
	// Signed flow: net directional mid drift normalized by total absolute drift,
	// giving a [-1,1] "how one-directional was the move" score. Zero when the
	// window had no mid movement (honest: no flow information).
	if signedFlowW > 0 {
		f.SignedVol = clamp1(signedFlowSum / signedFlowW)
	}
	return f, true
}

// Map returns the microstructure features as a name->value map ready to merge
// into the prediction feature vector. Keys are stable and prefixed "micro_" so
// they never collide with the daily-bar component features.
func (f Features) Map() map[string]float64 {
	return map[string]float64{
		"micro_imbalance":      f.Imbalance,
		"micro_imbalance_last": f.ImbalanceLast,
		"micro_signed_vol":     f.SignedVol,
		"micro_spread_bps":     f.SpreadBps,
		"micro_wmid_mid_bps":   f.WmidMidBps,
	}
}

func clamp1(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
