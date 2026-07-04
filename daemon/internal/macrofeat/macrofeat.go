// Package macrofeat turns the free FRED macro series (Stage 2's macro_series)
// into cross-asset FEATURES for the directional model. The headline one is VIX
// (FRED series VIXCLS) — the market's "fear" level — which is present for ALL
// symbols (crypto included) because volatility regime is a market-wide state,
// not a per-symbol one.
//
// The VIX level is the same for every symbol at a given time, so on its own it
// cannot separate symbols cross-sectionally; its value is TEMPORAL — it lets the
// model condition each symbol's own features on the prevailing volatility
// regime (edges that exist in calm tape often vanish in a panic, and vice
// versa). The GBM in particular can learn such interactions.
//
// No lookahead: the caller passes the LATEST VIX observation at or before the
// decision time (store.LatestVIX). A VIX close is final once published, so there
// is no revision leak.
package macrofeat

// VIX regime thresholds (VIXCLS points). These are the conventional, widely-used
// bands, fixed as constants so the "vol_regime" flag means the same thing across
// time (a feature whose definition drifts is not a feature):
//
//	< 15         : calm       (regime 0)
//	15 .. < 25   : normal     (regime 1)
//	25 .. < 35   : elevated   (regime 2)
//	>= 35        : stressed   (regime 3)
const (
	vixCalmMax     = 15.0
	vixNormalMax   = 25.0
	vixElevatedMax = 35.0
)

// VIXFeatures is the cross-asset macro feature bundle.
type VIXFeatures struct {
	// Level is the raw VIX close, scaled to roughly O(1) by /100 so it composes
	// with the other bounded features without dominating a scale-sensitive
	// standardizer. (The GBM is scale-invariant, but the linear/adaptive legs
	// are not, so a bounded encoding is the safe shared choice.)
	Level float64
	// Regime is the ordinal vol band 0..3 (calm..stressed), also /3-scaled to
	// [0,1] so it reads as a bounded feature.
	Regime float64
	// HighVol is 1 when VIX indicates an elevated-or-worse regime (>= 25), else
	// 0 — a simple binary the linear legs can use directly.
	HighVol float64
}

// FromVIX builds the VIX feature bundle from a raw VIX close. The raw ordinal
// regime (0..3) and its scaled forms are all derived here so the encoding lives
// in exactly one place.
func FromVIX(vix float64) VIXFeatures {
	reg := regimeOrdinal(vix)
	high := 0.0
	if vix >= vixNormalMax { // >= 25 => elevated or stressed
		high = 1
	}
	return VIXFeatures{
		Level:   vix / 100.0,
		Regime:  float64(reg) / 3.0,
		HighVol: high,
	}
}

// regimeOrdinal maps a VIX level to its 0..3 band.
func regimeOrdinal(vix float64) int {
	switch {
	case vix < vixCalmMax:
		return 0
	case vix < vixNormalMax:
		return 1
	case vix < vixElevatedMax:
		return 2
	default:
		return 3
	}
}

// Map returns the VIX features as a name->value map ready to merge into the
// prediction feature vector. Keys are stable and prefixed "vix_" so they never
// collide with the per-symbol component features. Present for ALL symbols
// whenever a VIX observation exists.
func (f VIXFeatures) Map() map[string]float64 {
	return map[string]float64{
		"vix_level":    f.Level,
		"vix_regime":   f.Regime,
		"vix_high_vol": f.HighVol,
	}
}
