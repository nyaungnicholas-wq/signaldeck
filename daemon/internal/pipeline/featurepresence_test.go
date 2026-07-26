// ZERO-FILL FINDING (hostile review, SECTION 2 "also high"): `flatten`
// zero-filled absent features, so a feature the pipeline deliberately OMITTED
// ("absence is information, not zero" — buildFeatureVector, macrofeat.FromSeries,
// alphaxfeat, trendfeat all say so in prose) arrived at the model as the number
// 0. Zero is a real, common value for macro_*_chg and micro_*, so the model
// could not tell "the provider was down" from "the policy rate did not move".
//
// MEASURED on the live DB (data/signaldeck.db, mode=ro, 2026-07-26) — these are
// the numbers that make the collision concrete, not hypothetical:
//
//	macro_fedfunds_chg : 312 of 312 v11 rows carry EXACTLY 0.0 (DFF is flat
//	                     between FOMC meetings, so 0 is its MODAL value)
//	vix_high_vol       : 100,503 of 100,503 v10 rows carry EXACTLY 0.0
//	micro_spread_bps   : BTC/USD v10 — 749 measured 0.0, 203 non-zero, and 14
//	                     rows where the book read failed and the key is ABSENT
//
// A FRED read failure omits macro_fedfunds_chg; the old flatten then handed the
// tree 0.0 — byte-identical to the 312 rows where the rate genuinely held. Same
// collision for the 14 absent BTC book reads against 749 measured zero spreads.
//
// The outage itself is on record: v3's vix_* keys are absent across a
// contiguous 2026-07-04 09:00:00–23:03:00 UTC window (1,272 rows, no present
// row inside it) while vix_high_vol reads exactly 0.0 on all 30,826 rows FRED
// did answer — so that whole block flattened to the same input as a real
// "vol regime not elevated" reading.
//
// The fix is the presence indicator the pooled cross-sectional engine already
// uses (alphax.Flatten / gbm.PresenceSuffix): for base key K, K+"__has" is 1
// when K was in the raw row and 0 when it was not. Absent still flattens to 0 —
// the bit beside it is what carries the missingness.
package pipeline

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A missing feature and an observed zero must not produce the same model input.
// This is the finding, reproduced with the live DB's own worst case: a row where
// the Fed funds change was MEASURED at 0.0 against a row where the FRED read
// failed and the key is absent.
func TestFlattenSeparatesAbsentFromMeasuredZero(t *testing.T) {
	measuredZero := map[string]float64{
		"pressure_score":     0.4,
		"macro_fedfunds_chg": 0, // FRED answered: the policy rate held
	}
	fredDown := map[string]float64{
		"pressure_score": 0.4,
		// macro_fedfunds_chg omitted: the read failed, so the pipeline left it out
	}

	rows := []store.LabeledFeature{{Vec: measuredZero}, {Vec: fredDown}}
	keys := modelFeatureKeys(rows)

	a := flatten(measuredZero, keys)
	b := flatten(fredDown, keys)
	if len(a) != len(b) {
		t.Fatalf("flatten width differs between rows: %d vs %d", len(a), len(b))
	}
	same := true
	for i := range a {
		if a[i] != b[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("a measured 0 and an absent feature flattened to the SAME vector %v — "+
			"the tree cannot tell 'the rate held' from 'FRED was down', and will split on "+
			"the outage's date range", a)
	}

	// And the separation must be carried by the presence bit specifically, not
	// by some incidental key-order difference.
	idx := -1
	for i, k := range keys {
		if k == "macro_fedfunds_chg"+gbm.PresenceSuffix {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no presence indicator for macro_fedfunds_chg in %v", keys)
	}
	if a[idx] != 1 || b[idx] != 0 {
		t.Fatalf("presence bit = %v (observed) / %v (absent), want 1 / 0", a[idx], b[idx])
	}
	// The base value still flattens to 0 in both — the bit is what differs.
	base := -1
	for i, k := range keys {
		if k == "macro_fedfunds_chg" {
			base = i
		}
	}
	if a[base] != 0 || b[base] != 0 {
		t.Fatalf("base value = %v / %v, want 0 / 0 (the indicator carries missingness, "+
			"not a sentinel in the value)", a[base], b[base])
	}
}

// Every base key must carry exactly one presence companion, and the layout must
// stay sorted + deterministic: the GBM is index-based, so the training samples
// and the live "latest" vector are only comparable when both flatten through
// the identical key order.
func TestModelFeatureKeysPairsEveryBaseWithAnIndicator(t *testing.T) {
	rows := []store.LabeledFeature{
		{Vec: map[string]float64{"pressure_score": 0.1, "micro_spread_bps": 0}},
		{Vec: map[string]float64{"vix_high_vol": 0, "pressure_score": 0.2}},
	}
	keys := modelFeatureKeys(rows)

	want := []string{
		"micro_spread_bps", "micro_spread_bps__has",
		"pressure_score", "pressure_score__has",
		"vix_high_vol", "vix_high_vol__has",
	}
	if len(keys) != len(want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("key order not the sorted base+indicator union: got %v, want %v", keys, want)
		}
	}
}

// A presence bit for a model output leaks exactly the self-reference its value
// does ("the blend had an opinion here"), so the exclusion must match by BASE
// name — the same rule gbm.SelfReferentialKey and alphax.excludedKey enforce.
func TestModelFeatureKeysExcludesSelfReferentialPresenceBits(t *testing.T) {
	rows := []store.LabeledFeature{{Vec: map[string]float64{
		"pressure_score": 0.1,
		"pred_raw":       0.6, "pred_cal": 0.6,
		"gbm_prob": 0.55, "meanrev_prob": 0.45, "alphax_prob": 0.61,
	}}}
	keys := modelFeatureKeys(rows)
	for _, k := range keys {
		switch k {
		case "pred_raw", "pred_cal", "gbm_prob", "meanrev_prob", "alphax_prob",
			"pred_raw__has", "pred_cal__has", "gbm_prob__has", "meanrev_prob__has", "alphax_prob__has":
			t.Fatalf("self-referential key %q reached the model layout: %v", k, keys)
		}
	}
	if len(keys) != 2 || keys[0] != "pressure_score" || keys[1] != "pressure_score__has" {
		t.Fatalf("got %v, want [pressure_score pressure_score__has]", keys)
	}
}

// The "__has" suffix is RESERVED for derived indicators. A stored vector that
// smuggles a literal K__has key must not shadow the derived bit (which would
// make presence mean whatever the raw value happened to be) nor breed a
// K__has__has column.
func TestModelFeatureKeysReservesTheSuffix(t *testing.T) {
	rows := []store.LabeledFeature{{Vec: map[string]float64{
		"x": 1, "x__has": 5,
	}}}
	keys := modelFeatureKeys(rows)
	for _, k := range keys {
		if k == "x__has__has" {
			t.Fatalf("derived indicator applied to a raw __has key: %v", keys)
		}
	}
	if len(keys) != 2 || keys[0] != "x" || keys[1] != "x__has" {
		t.Fatalf("got %v, want [x x__has]", keys)
	}
	// The derived bit wins: 1 because "x" is present, NOT the stored 5.
	v := flatten(rows[0].Vec, keys)
	if v[1] != 1 {
		t.Fatalf("presence bit = %v, want 1 (derived from the raw map, never read from it)", v[1])
	}
}

// One spelling across the repo. gbm.SelfReferentialKey trims this exact suffix
// to match exclusions by base name; a second spelling here would silently
// re-open the self-reference hole that predicate exists to close.
func TestPresenceSuffixMatchesTheSharedConstant(t *testing.T) {
	if presenceSuffix != gbm.PresenceSuffix {
		t.Fatalf("presence suffix %q != gbm.PresenceSuffix %q", presenceSuffix, gbm.PresenceSuffix)
	}
	if !strings.HasSuffix("x"+presenceSuffix, gbm.PresenceSuffix) {
		t.Fatalf("suffix %q does not compose", presenceSuffix)
	}
}

// canonicalFeatureKeys keeps its BASE-ONLY contract. The feature-redundancy
// surface (honestygaps.go) passes it as the allowlist of genuine DATA SOURCES
// to correlate; a presence bit is not a data source, and adding one there would
// pad that surface's field count with columns its samples never contain.
func TestCanonicalFeatureKeysStaysBaseOnly(t *testing.T) {
	rows := []store.LabeledFeature{{Vec: map[string]float64{"a": 1, "b": 0}}}
	keys := canonicalFeatureKeys(rows)
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("canonicalFeatureKeys must stay the base union, got %v", keys)
	}
}
