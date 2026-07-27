package gbm

import (
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

// ── H6 · the self-reference doctrine, made testable ──────────────────────────
//
// The adversarial review found the mean-reversion leg's sample builder reading
// pred_raw — the blend output that CONTAINS the mean-reversion leg — in the same
// file that declares pred_raw off-limits. The list was correct and the builder
// ignored it, because the list was a private switch statement rather than a
// shared predicate anything could be held to. These tests pin the shared one.

func TestSelfReferentialKey_CoversEveryBlendAndLegOutput(t *testing.T) {
	// pred_raw heads this list deliberately: it is the blend output the
	// mean-reversion leg consumes and simultaneously contributes to, so a builder
	// reading it closes a loop on itself.
	for _, k := range []string{"pred_raw", "pred_cal", "gbm_prob", "meanrev_prob", "alphax_prob"} {
		if !SelfReferentialKey(k) {
			t.Fatalf("%q is a model output and must be refused as a model input", k)
		}
		// The presence bit leaks the same self-reference the value does.
		if !SelfReferentialKey(k + PresenceSuffix) {
			t.Fatalf("%q presence indicator must be excluded with its base key", k)
		}
	}
}

// ── A7 · the four outputs the original five-name list let through ────────────
//
// The 2026-07-26 re-audit found the list naming only the legs that had been
// caught, not the CLASS. Four more model outputs were reaching the training set
// on the live DB (counts re-measured 2026-07-26, mode=ro, over the features
// table): forecast_prob 231,981 rows, forecast_lift 231,981,
// expectancy_hit_rate 238,910, n_used 248,391.
//
// Point-in-time is preserved for all four, so this is not look-ahead — it is
// DOUBLE COUNTING. The GBM can copy the forecast leg, and the copy then enters
// the blend beside the original as if it were independent evidence.
// forecast_lift is the worst of them: an accuracy statistic computed FROM THE
// LABELS has no business being an input at all.
func TestSelfReferentialKey_CoversLegOutputsAndLabelDerivedStats(t *testing.T) {
	for _, k := range []string{
		"forecast_prob",        // the walk-forward logistic leg's own output
		"forecast_lift",        // that leg's OOS accuracy — computed from labels
		"expectancy_hit_rate",  // the expectancy leg's output, also a label statistic
		"n_used",               // how many legs cleared their (label-graded) gates
		"gbm_lift", "meanrev_lift", "alphax_lift", // class rule, not yet stored
		"pressure_hit_rate", // class rule: any hit rate is a label statistic
	} {
		if !SelfReferentialKey(k) {
			t.Fatalf("%q is a model output or a label-derived statistic and must be refused as a model input", k)
		}
		if !SelfReferentialKey(k + PresenceSuffix) {
			t.Fatalf("%q presence indicator must be excluded with its base key", k)
		}
	}
}

func TestSelfReferentialKey_AllowsGenuineInputs(t *testing.T) {
	// Real observations must NOT be swept up: over-excluding starves the model
	// as surely as under-excluding corrupts it.
	//
	// pressure_score and rank_pct are deliberately here. Both were considered
	// for exclusion under A7 and kept: pressure_score is a fixed-weight sum of
	// technical components (no weight is fit to an outcome), and rank_pct is
	// cross-sectional relative strength computed by ranking.FromBars off daily
	// bars. Neither reads a label nor a model probability.
	for _, k := range []string{
		"pressure_score", "rank_pct", "sentiment_score", "sentiment_n",
		"vix_level", "micro_spread", "adx14", "pattern_bias", "pred_rawness",
		"comp_rsi", "trend_class", "tv_reco",
	} {
		if SelfReferentialKey(k) {
			t.Fatalf("%q is an observation, not a model output — must remain trainable", k)
		}
	}
}

// ── the census pin: every live feature key, classified ───────────────────────
//
// A7's lesson was that the exclusion list only ever named the keys someone had
// already CAUGHT: four model outputs sat in the live training set for weeks
// because nothing compared the predicate against what the pipeline actually
// logs. featureKeyCensus is that comparison, pinned: every distinct key
// observed in the live features table (all versions, censused 2026-07-26 over
// json_each(features.vec)), each classified excluded (model output /
// label-derived) or trainable (genuine observation).
//
// Two tests hold it:
//   - TestSelfReferentialKey_MatchesCensusClassification asserts the predicate
//     agrees with every classification — the predicate can neither leak a known
//     output nor starve a known observation.
//   - TestSelfReferentialKey_LiveKeysAllCensused re-runs the census against the
//     live DB (newest rows, where a new key first appears) and FAILS on any key
//     absent from this map. A newly-logged model-output feature therefore
//     breaks the suite the day it ships, not the day the next auditor greps:
//     whoever adds the key must classify it here, and classifying an output as
//     trainable then fails the first test against the predicate's class rules —
//     or against the reviewer the diff is now in front of.
var featureKeyCensus = map[string]bool{ // key -> excluded?
	// Model outputs and label-derived statistics (the predicate must refuse).
	"pred_raw": true, "pred_cal": true, // the blend's own probabilities
	"gbm_prob": true, "meanrev_prob": true, "alphax_prob": true, // leg outputs
	"forecast_prob": true, "forecast_lift": true, // A7: leg output + its OOS accuracy
	"expectancy_hit_rate": true, // A7: label statistic
	"n_used":              true, // A7: count of legs past their label-graded gates

	// Genuine observations (the predicate must keep trainable).
	"adx14": false, "bb_pctb": false, "cci20": false,
	"comp_imbalance": false, "comp_macd": false, "comp_momentum_roc": false,
	"comp_rsi": false, "comp_rvol_confirm": false, "comp_trend_sma": false,
	"comp_vol_regime": false, "comp_vol_regime_value": false, "comp_vwap_dist": false,
	"cot_spx_net": false, "funding_rate": false, "insider_net_ratio": false,
	"macro_curve_10y2y_chg": false, "macro_curve_10y2y_pct": false,
	"macro_curve_10y3m_chg": false, "macro_curve_10y3m_pct": false,
	"macro_dgs10_chg": false, "macro_dgs10_pct": false,
	"macro_dgs2_chg": false, "macro_dgs2_pct": false,
	"macro_fedfunds_chg": false, "macro_fedfunds_pct": false,
	"macro_hy_spread_chg": false, "macro_hy_spread_pct": false,
	"macro_nfci_chg": false, "macro_nfci_pct": false,
	"macro_oil_chg": false, "macro_oil_pct": false,
	"micro_imbalance": false, "micro_imbalance_last": false, "micro_signed_vol": false,
	"micro_spread_bps": false, "micro_wmid_mid_bps": false,
	"news_vol_z": false, "pattern_bias": false, "pc_total": false,
	"pressure_score": false, "rank_pct": false,
	"regime_downtrend": false, "regime_range": false, "regime_squeeze": false, "regime_uptrend": false,
	"sentiment_n": false, "sentiment_score": false,
	"short_int_dtc": false, "short_vol_z": false, "stoch_k": false,
	"stocktwits_bull_ratio": false, "supertrend_dir": false,
	"trend_channel": false, "trend_class": false,
	"trend_dist_resistance": false, "trend_dist_support": false, "trend_slope": false,
	"tv_reco": false, "tv_webhook_signal": false, // external signals observed, not our outputs
	"vix_high_vol": false, "vix_level": false, "vix_regime": false,
	"wiki_z": false,
}

func TestSelfReferentialKey_MatchesCensusClassification(t *testing.T) {
	for k, excluded := range featureKeyCensus {
		if got := SelfReferentialKey(k); got != excluded {
			if excluded {
				t.Errorf("%q is censused as a model output / label statistic but SelfReferentialKey admits it", k)
			} else {
				t.Errorf("%q is censused as a genuine observation but SelfReferentialKey refuses it", k)
			}
		}
	}
}

// liveCensusRows bounds the live re-census to the newest feature rows. A new
// key can only first appear in fresh rows, and 50k rows is several days of
// fleet-wide logging — wide enough to catch anything current, small enough to
// keep the suite fast against the full multi-hundred-thousand-row table.
const liveCensusRows = 50000

// testDBPath locates the live DB: $SIGNALDECK_DB wins, else the repo-layout
// default relative to this package (daemon/internal/gbm → repo root/data).
func testDBPath() string {
	if p := os.Getenv("SIGNALDECK_DB"); p != "" {
		return p
	}
	return "../../../data/signaldeck.db"
}

func TestSelfReferentialKey_LiveKeysAllCensused(t *testing.T) {
	path := testDBPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("live DB not present at %s (set SIGNALDECK_DB to point elsewhere)", path)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open %s read-only: %v", path, err)
	}
	defer db.Close() //nolint:errcheck

	rows, err := db.Query(`
		SELECT DISTINCT j.key
		FROM (SELECT vec FROM features ORDER BY id DESC LIMIT ?) f, json_each(f.vec) j
		ORDER BY 1`, liveCensusRows)
	if err != nil {
		t.Fatalf("census query: %v", err)
	}
	defer rows.Close() //nolint:errcheck

	seen := 0
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if _, censused := featureKeyCensus[k]; !censused {
			t.Errorf("live feature key %q is not in featureKeyCensus — classify it: if it is a model output or label-derived statistic it must ALSO be refused by SelfReferentialKey (excluded=%v today)", k, SelfReferentialKey(k))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("census rows: %v", err)
	}
	if seen == 0 {
		t.Fatal("live census returned zero keys — features table empty or query broken; the guard is not guarding")
	}
}

func TestFilterSelfReferential_DropsOutputsKeepsOrder(t *testing.T) {
	in := []string{"adx14", "pred_raw", "forecast_prob", "meanrev_prob", "vix_level", "pred_cal__has", "forecast_lift", "n_used"}
	got := FilterSelfReferential(in)
	want := []string{"adx14", "vix_level"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
