package pipeline

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/meanrev"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// gbmMaxRows caps how many of a symbol's own labeled examples one training pass
// consumes per horizon (newest-first from the store, then re-ordered ascending).
const gbmMaxRows = 5000

// meanRevCost is the round-trip cost (fraction) the mean-reversion leg is graded
// NET OF — a contrarian trade must clear it to count as a win. 0.001 = 10 bps
// round-trip, a deliberately conservative all-in estimate for a liquid name; the
// leg only enters the blend if its edge survives this bar.
const meanRevCost = 0.001

// gbmFolds / meanRevFolds are the walk-forward fold counts (mirrors
// forecast.defaultFolds = 5).
const (
	gbmFolds     = 5
	meanRevFolds = 5
)

// presenceSuffix marks a DERIVED per-feature presence indicator: for base key
// K, K+presenceSuffix is 1 when K was in the row's raw map and 0 when it was
// absent. Aliased to the shared constant so this package, the pooled
// cross-sectional engine (alphax) and the self-reference predicate that trims
// it (gbm.SelfReferentialKey) can never drift to two spellings — a second
// spelling would silently re-open the exclusion hole that predicate closes.
const presenceSuffix = gbm.PresenceSuffix

// canonicalFeatureKeys returns a STABLE, sorted union of feature-vector keys
// across the given labeled rows, EXCLUDING the model's own outputs (pred_raw /
// pred_cal) so the GBM cannot trivially copy the blend it is trying to
// out-predict — training on pred_raw would be a leakage shortcut, not learning.
// A deterministic ordering is essential: the GBM is index-based, so every
// sample (and the live latest vector) must be flattened with the identical key
// order.
//
// These are BASE keys only — the genuine data sources. The feature-redundancy
// surface (honestygaps.go) passes them as its allowlist of fields to correlate,
// and a presence indicator is not a data source. Callers building a MODEL INPUT
// layout want modelFeatureKeys instead.
func canonicalFeatureKeys(rows []store.LabeledFeature) []string {
	set := map[string]struct{}{}
	for _, r := range rows {
		for k := range r.Vec {
			if excludedGBMKey(k) {
				continue
			}
			set[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// excludedGBMKey reports feature keys the GBM must NOT train on:
//   - the blend's own probabilities (pred_raw/pred_cal) — training on them would
//     let the tree learn "copy the ensemble" instead of finding independent
//     structure, and would couple the GBM leg to the very blend it's meant to
//     diversify;
//   - the model legs' OWN prior outputs (gbm_prob/meanrev_prob/alphax_prob) — a
//     leg must not be fed its own past prediction, both to avoid a
//     self-referential shortcut and to keep the GBM leg independent of the other
//     model legs (and itself).
//
// It delegates to the shared predicate rather than keeping a private copy of
// the list: finding H6 is what a private copy cost, and the shared predicate
// also matches by BASE name, so a presence indicator (pred_raw__has) is
// excluded with its value — a bit saying "the blend had an opinion here" leaks
// the same self-reference the probability does.
//
// The four A7 shortcut keys (forecast_prob / forecast_lift /
// expectancy_hit_rate / n_used) are no longer written by buildFeatureVector at
// all — the ablation harness measured them worthless, so they were deleted at
// construction instead of built-then-gated. The predicate still bans them here
// because the labeled sets this trainer reads reach back into pre-deletion
// rows that carry them.
func excludedGBMKey(k string) bool {
	return gbm.SelfReferentialKey(k)
}

// modelFeatureKeys returns the MODEL INPUT layout: every base key from
// canonicalFeatureKeys followed by its derived K+presenceSuffix indicator,
// sorted so the ordering stays deterministic (the GBM is index-based).
//
// WHY the indicators exist. The pipeline is careful to OMIT a feature it could
// not observe — buildFeatureVector, macrofeat.FromSeries, alphaxfeat and
// trendfeat all say "absence is information, not zero" in prose — and flatten
// then destroyed that distinction by filling absent keys with 0. Zero is a real
// and often MODAL value for these fields, measured on the live DB (mode=ro) on
// 2026-07-26:
//
//   - macro_fedfunds_chg is exactly 0.0 on 312 of 312 v11 rows — the policy rate
//     does not move between FOMC meetings, so 0 IS the normal reading;
//   - vix_high_vol is exactly 0.0 on all 100,503 v10 rows;
//   - BTC/USD v10 carries 749 measured-zero micro_spread_bps against 14 rows
//     where the book read failed and the key is absent.
//
// And the outage is not hypothetical: v3's vix_* keys are absent across a
// perfectly contiguous window, 2026-07-04 09:00:00 to 23:03:00 UTC, 1,272 rows
// with not one present row inside it, while vix_high_vol reads exactly 0.0 on
// all 30,826 rows where FRED did answer. Under the old flatten those 1,272
// outage rows were byte-identical to a measured "vol regime not elevated" — a
// tree splitting there learns the hours the data provider was down, not a
// market state.
//
// Raw keys already ending in presenceSuffix are dropped from the base set: the
// suffix is RESERVED for these derived bits, so a stored key can never shadow
// one and make "present" mean whatever value happened to be written.
//
// NOT a featureVersion bump, deliberately. featureVersion stamps the vector
// PERSISTED by InsertFeatures, and this changes nothing about what is stored —
// only the layout derived from stored rows at training time. Bumping would
// orphan the v11 rows and stall every per-symbol GBM for weeks (the cost
// maxModelForecastAgeSecs below documents), to protect models that do not exist:
// no trained model is persisted anywhere, gbm.Run retrains from the rows on
// every pass, and modelFeatureKeys is recomputed in the same call that consumes
// it. There is no stored artifact whose width could disagree.
func modelFeatureKeys(rows []store.LabeledFeature) []string {
	return modelFeatureKeysExcluding(rows, nil)
}

// modelFeatureKeysExcluding is modelFeatureKeys with the feature-health retire
// set applied. This is the ENFORCEMENT point for per-feature retirement: a
// feature the grader retired stops being trained on here, rather than staying in
// the vector forever while a report says it is dead.
//
// The filter runs on the BASE set, before the presence indicators are derived, so
// retiring "foo" drops both "foo" and "foo__present". Filtering the expanded list
// instead would leave an orphaned indicator whose base value is no longer read —
// a column that is always 1 and means nothing.
func modelFeatureKeysExcluding(rows []store.LabeledFeature, retired map[string]bool) []string {
	base, _ := dropRetired(canonicalFeatureKeys(rows), retired)
	keys := make([]string, 0, 2*len(base))
	for _, k := range base {
		if strings.HasSuffix(k, presenceSuffix) {
			continue
		}
		keys = append(keys, k, k+presenceSuffix)
	}
	sort.Strings(keys)
	return keys
}

// flatten turns a feature map into a fixed-dimension vector in the given key
// order. A key ending in presenceSuffix is DERIVED from the raw map — 1 when
// its base key is present, 0 when it is not — and is never read out of the map,
// so the training path and the live-scoring path (which flatten with the same
// key slice) agree by construction. Base values still flatten to 0 when absent;
// the indicator beside them is what carries the missingness, which is why a
// missing feature is no longer the same model input as an observed zero.
//
// Callers passing a base-only key slice (canonicalFeatureKeys) get the original
// behaviour unchanged.
func flatten(vec map[string]float64, keys []string) []float64 {
	out := make([]float64, len(keys))
	for i, k := range keys {
		if b, ok := strings.CutSuffix(k, presenceSuffix); ok {
			if _, present := vec[b]; present {
				out[i] = 1
			}
			continue
		}
		out[i] = vec[k] // zero if absent — the __has bit says which
	}
	return out
}

// minGradeDays is how many DISTINCT UTC days an out-of-sample record must span
// before its Lift may be published and used to admit a leg to the live blend.
//
// N is not evidence here — days are. Every row inside one day resolves to the
// same forward move, so a 300-row grade over 8 days holds 8 independent
// observations, and a Lift computed on it is a reading of eight coin flips
// dressed up as three hundred. Measured 2026-08-05, that was every per-symbol
// grade in production.
//
// 10 is the same evidence floor the accuracy registry (min_distinct_blocks) and
// canary.ReadmitMinDistinctDays already enforce, so no surface gets to ADMIT a
// leg on less evidence than another surface needs to BELIEVE it.
const minGradeDays = 10

// gradeHasEvidence reports whether a grade spans enough independent days to be
// published at all. A grade below the floor is not published as a negative lift
// — that would be indistinguishable from a measured anti-predictive verdict.
// Nothing is written, and the leg stays UNMEASURED, which the ensemble's
// fail-safe admission treats as "no evidence yet" rather than "no edge".
func gradeHasEvidence(distinctDays int) bool { return distinctDays >= minGradeDays }

// gbmSamplesFromLabeled builds time-ASCENDING gbm.Samples from labeled feature
// rows (which arrive newest-first). The caller passes the canonical key order so
// every sample — and the latest live vector — share one feature layout.
func gbmSamplesFromLabeled(rows []store.LabeledFeature, keys []string) []gbm.Sample {
	out := make([]gbm.Sample, 0, len(rows))
	// rows are newest-first; iterate in reverse for ascending ts.
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		out = append(out, gbm.Sample{
			Ts:   r.Ts,
			Feat: flatten(r.Vec, keys),
			Y:    float64(r.Up),
		})
	}
	return out
}

// meanRevSamplesFromLabeled builds time-ASCENDING meanrev.Samples from labeled
// rows, carrying the momentum lean this leg exists to invert plus the realized
// outcome + forward return for cost-net grading.
//
// IT MUST NOT READ pred_raw, and that is the whole point of this comment.
// pred_raw is the BLEND output, and the blend contains the mean-reversion leg
// whenever its lift is positive — so training the leg on pred_raw closes a loop
// in which the leg learns to invert a number that already contains itself. The
// file forty lines above this one excludes pred_raw from GBM training for
// exactly that reason; this builder quietly read it anyway, and 1,093 live
// predictions across 12 symbols were emitted through that loop.
//
// The leg's own doctrine is "invert an extreme pressure score", so it reads the
// pressure feature directly and applies the same [-1,1] -> [0,1] conversion
// ensemble.LegProbabilities uses for LegPressure. That is an input, not an
// output. Rows lacking it are skipped, exactly as rows lacking pred_raw were.
// meanRevLatestInput derives the momentum probability the mean-reversion leg
// inverts when it SERVES, using the same construction as the samples it is
// GRADED on.
//
// It used to read rows[0].Vec["pred_raw"], which was wrong twice over.
// gbm/selfref.go names the first fault in its own doctrine comment: pred_raw is
// the blend output computed WITH the mean-reversion leg inside it, so the leg
// inverted a number that already contained its own inversion. The sample
// builder was moved onto pressure_score when that was found; this serve path,
// forty lines away, was not.
//
// The second fault is quieter and just as bad: the OOS lift that admits this
// leg into the blend was measured on (pressure_score+1)/2 while the probability
// actually published inverted pred_raw. The gate was validating a signal that
// was never served. Live on 2026-07-26, 7 of 37 graded meanrev rows carried
// lift>0 and were therefore in the blend on that basis.
//
// It refuses rather than falling back when the newest row has no pressure
// score: a fallback to pred_raw would quietly restore the self-reference during
// exactly the source outage that makes it hardest to notice.
func meanRevLatestInput(rows []store.LabeledFeature) (float64, bool) {
	if len(rows) == 0 {
		return 0, false
	}
	pressure, ok := rows[0].Vec["pressure_score"]
	if !ok {
		return 0, false
	}
	return (pressure + 1) / 2, true
}

func meanRevSamplesFromLabeled(rows []store.LabeledFeature) []meanrev.Sample {
	out := make([]meanrev.Sample, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		pressure, ok := r.Vec["pressure_score"]
		if !ok {
			continue
		}
		raw := (pressure + 1) / 2
		out = append(out, meanrev.Sample{
			Ts:        r.Ts,
			RawProb:   raw,
			Up:        r.Up,
			FwdReturn: r.FwdReturn,
		})
	}
	return out
}

// GBMTrainer trains the STAGE 6 non-linear (GBM) and mean-reversion model legs
// for every active symbol+horizon from the feature store, walk-forward and
// out-of-sample, and upserts each leg's latest probability + OOS grade into
// model_forecasts. The PredictionRunner then includes a leg in the blend ONLY
// when its stored lift > 0 (the same honesty gate the linear forecast passes).
//
// It never blocks predictions: a symbol with too little resolved history simply
// produces no GBM/mean-rev row (Run returns ok=false), and the ensemble falls
// back to the legs that do have measured edge.
type GBMTrainer struct {
	St *store.Store
}

func (w *GBMTrainer) Name() string            { return "gbm-trainer" }
func (w *GBMTrainer) Interval() time.Duration { return time.Hour }

func (w *GBMTrainer) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()

	// Per-feature retirement, read ONCE per horizon rather than per
	// (symbol, horizon): the verdict is fleet-wide, and re-reading it inside the
	// inner loop would hit the meta table once per symbol to learn the same
	// answer. An absent report retires nothing (see retiredFeatureKeys).
	retiredByHorizon := make(map[md.Horizon]map[string]bool, len(predHorizons))
	droppedNames := map[string]bool{}
	for _, h := range predHorizons {
		set := retiredFeatureKeys(ctx, w.St, h)
		retiredByHorizon[h] = set
		for k := range set {
			droppedNames[k] = true
		}
	}

	gbmEdged, mrEdged, trained := 0, 0, 0
	for _, s := range syms {
		for _, h := range predHorizons {
			retired := retiredByHorizon[h]
			// Version-pinned: the GBM flattens the key union across rows, so
			// mixing feature-schema versions would dilute absent-vs-zero
			// (review finding). Train on the current layout only.
			rows, err := w.St.LabeledFeaturesBySymbolVersion(ctx, s.ID, h, featureVersion, gbmMaxRows)
			if err != nil {
				return "", fmt.Errorf("labeled %s %s: %w", s.Symbol, h, err)
			}
			if len(rows) == 0 {
				continue
			}

			// ── GBM leg ──────────────────────────────────────────────────
			// Model layout, not the base union: every field carries a
			// presence indicator so an unobserved feature cannot arrive as
			// the zero it is often genuinely measured at.
			keys := modelFeatureKeysExcluding(rows, retired)
			if len(keys) > 0 {
				// Declare the label horizon so gbm.Evaluate can PURGE training
				// rows whose label resolves inside the test block. Without it
				// Evaluate refuses to grade rather than publish a Lift computed
				// across overlapping labels — the gate that admits this leg to
				// the live blend must not be measured on leaked rows.
				samples := gbm.WithLabelSpan(gbmSamplesFromLabeled(rows, keys), horizonSecs(h))
				// Latest live vector = newest row (rows[0]) flattened with the
				// same key order. It IS in the training set (its outcome already
				// resolved, so it's a legitimate labeled example); Run grades
				// out-of-sample via walk-forward before predicting it.
				latest := flatten(rows[0].Vec, keys)
				if prob, g, ok := gbm.Run(samples, latest, gbmFolds, gbm.Defaults()); ok && gradeHasEvidence(g.DistinctDays) {
					if err := w.St.UpsertModelForecast(ctx, store.ModelForecast{
						SymbolID: s.ID, Horizon: h, Model: store.ModelGBM, Ts: now,
						Prob: prob, Accuracy: g.Accuracy, Brier: g.BrierScore, AUC: g.AUC,
						BaseRate: g.BaseRate, Lift: g.Lift, NTrain: len(samples), NEval: g.N,
					}); err != nil {
						return "", fmt.Errorf("upsert gbm %s %s: %w", s.Symbol, h, err)
					}
					trained++
					if g.Lift > 0 {
						gbmEdged++
					}
				}
			}

			// ── mean-reversion leg ───────────────────────────────────────
			mrSamples := meanRevSamplesFromLabeled(rows)
			latestRaw, hasRaw := meanRevLatestInput(rows)
			if hasRaw {
				if prob, g, ok := meanrev.Run(mrSamples, latestRaw, meanRevFolds, meanrev.DefaultStrength, meanRevCost); ok && gradeHasEvidence(g.DistinctDays) {
					if err := w.St.UpsertModelForecast(ctx, store.ModelForecast{
						SymbolID: s.ID, Horizon: h, Model: store.ModelMeanRev, Ts: now,
						Prob: prob, Accuracy: g.Accuracy, Brier: g.BrierScore, AUC: g.AUC,
						BaseRate: g.BaseRate, Lift: g.Lift, NTrain: len(mrSamples), NEval: g.N,
					}); err != nil {
						return "", fmt.Errorf("upsert meanrev %s %s: %w", s.Symbol, h, err)
					}
					trained++
					if g.Lift > 0 {
						mrEdged++
					}
				}
			}
		}
	}
	msg := fmt.Sprintf("trained %d model legs over %d symbols (%d GBM + %d mean-rev with OOS edge)",
		trained, len(syms), gbmEdged, mrEdged)
	if len(droppedNames) > 0 {
		// Name them. A feature leaving the model silently is how a model becomes
		// unexplainable six months later.
		names := make([]string, 0, len(droppedNames))
		for k := range droppedNames {
			names = append(names, k)
		}
		sort.Strings(names)
		msg += fmt.Sprintf("; feature-health retired %d input(s): %s", len(names), strings.Join(names, ","))
	}
	return msg, nil
}

// maxModelForecastAgeSecs caps how old a stored model_forecasts row may be and
// still feed the live blend (M3). Without it a row entered every 10-minute
// prediction pass FOREVER: the prob was computed from bars at training time,
// and the featureVersion 6 bump means universe symbols won't accumulate enough
// v6 rows for the per-symbol GBM to retrain for weeks — so "latest" rows can
// be arbitrarily stale. 3 days is generous against the actual retrain
// cadences (alphax re-scores every 6h, the GBM trainer runs hourly whenever a
// symbol is trainable): a leg that hasn't refreshed in 3 days is a leg whose
// trainer has stopped vouching for it.
const maxModelForecastAgeSecs = 3 * 86400

// modelLegProbLift pulls one model's stored prob+lift for a symbol+horizon from
// a preloaded slice, returning ok=false when the leg isn't present OR its row
// is older than maxModelForecastAgeSecs (stale probs must not keep entering
// the blend). Used by the PredictionRunner to feed the gated legs into the
// ensemble.
func modelLegProbLift(models []store.ModelForecast, h md.Horizon, name string, now int64) (prob, lift float64, ok bool) {
	for _, m := range models {
		if m.Horizon == h && m.Model == name {
			if now-m.Ts > maxModelForecastAgeSecs {
				return 0, 0, false
			}
			return m.Prob, m.Lift, true
		}
	}
	return 0, 0, false
}

// modelLegRankEdge is modelLegProbLift's companion for the RANKING gate: it
// returns clusterstat.RankEdge of the same stored row's out-of-sample AUC.
// The AUC has been graded, stored and displayed since the model legs shipped;
// only the admission decision was still being taken on threshold-dependent
// lift. Same staleness rule — an old row vouches for nothing.
func modelLegRankEdge(fleet map[string]float64, models []store.ModelForecast, h md.Horizon, name string, now int64) (float64, bool) {
	for _, m := range models {
		if m.Horizon == h && m.Model == name {
			if now-m.Ts > maxModelForecastAgeSecs {
				return 0, false
			}
			return rankGate(fleet, name, h, m.AUC, m.NEval)
		}
	}
	return 0, false
}
