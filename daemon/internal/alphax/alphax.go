// Package alphax is SignalDeck's POOLED CROSS-SECTIONAL alpha engine — the
// approach every serious competitor (Danelfin's GBT ensemble, Zacks rank, the
// quant funds) actually uses: ONE model trained over the WHOLE universe that
// predicts RELATIVE outperformance vs the same-day cross-section, not
// per-symbol absolute direction. Relative rank is far more learnable (the
// market-wide component — the hardest part — cancels out of the label), and
// pooling uses ALL stored labeled feature rows at once instead of starving
// each symbol's model at min-60-rows.
//
// It reuses the EXISTING internal/gbm trees (gbm.Train / Model.Predict) and
// obeys the same three commitments that make everything in this project
// honest:
//
//   - NO LOOKAHEAD. Labels are built only from RESOLVED forward returns, and
//     grading uses PURGED WALK-FORWARD splits BY UTC DAY (de Prado): each test
//     block of days is predicted by a model trained only on days that end at
//     least embargoDays BEFORE the block starts. The embargo is HORIZON-AWARE
//     (Evaluate takes it as a parameter): a label spanning H days overlaps up
//     to H later days, so the caller must pass ceil(H)+1 — a fixed 2-day gap
//     purges 1d labels but NOT 1w labels, whose closes reach 7 days into what
//     would otherwise be the test block (adversarial finding H1).
//
//   - NEVER A PREDICTION WITHOUT ITS GRADE. Evaluate produces the same report
//     card shape as gbm.Grade (accuracy, Brier, AUC, base rate, lift =
//     accuracy − base rate) from strictly out-of-sample predictions.
//
//   - GATED BY MEASURED EDGE. The caller ships alphax scores anywhere ONLY
//     when the walk-forward Lift > 0. Below MinTrainRows / MinTestRows the
//     engine refuses to grade at all (ok=false with a stated reason) — an
//     honest "not enough data" beats a noise grade.
//
// # Label
//
// Each labeled row is one (symbol, ts) feature vector with its realized
// forward return. Rows are grouped by UTC day; a row's label is 1 when its
// forward return is STRICTLY ABOVE the same-day cross-section MEDIAN (the top
// half), else 0. Days with fewer than MinCrossSection resolved rows are
// dropped — a 3-stock "cross-section" is a coin flip, not a universe.
//
// # Costs / assumptions (stated because honesty is the brand)
//
//   - The label is RELATIVE: P(top half) says nothing about absolute
//     direction — a 0.9 score in a crashing market means "expected to fall
//     less". Every surface repeats this framing.
//   - Labels are cost-free close-to-close relative returns; a tradeable
//     long/short spread must clear costs downstream.
//   - Same-day rows share the market component, so effective N < len(rows);
//     the per-day fold construction (never splitting a day across train/test)
//     plus the embargo keep the point estimate honest even so.
package alphax

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
)

// Tunables — fixed constants so grades stay comparable across runs.
const (
	// MinCrossSection is the fewest resolved rows a UTC day needs to
	// contribute labels (a thin cross-section makes "top half" meaningless).
	MinCrossSection = 10
	// EmbargoDays is the MINIMUM purge gap between the last train day and the
	// first test day of each fold — sufficient for 1d labels (they overlap
	// adjacent days by ≤1 day) and the floor Evaluate enforces. Callers must
	// pass a horizon-aware embargo (label span in days + 1) to Evaluate: 2 is
	// NOT enough for 1w labels (adversarial finding H1).
	EmbargoDays = 2
	// MinTrainRows / MinTestRows: below these, Evaluate refuses to grade
	// (ok=false) rather than reporting a noise number.
	MinTrainRows = 1000
	MinTestRows  = 200
	// DefaultFolds mirrors gbm/forecast (5 walk-forward blocks by day).
	DefaultFolds = 5
)

// ErrInsufficientData is returned by TrainFull when the pooled dataset is too
// small to fit responsibly (delegates to gbm's own floor).
var ErrInsufficientData = errors.New("alphax: insufficient labeled data")

// LabeledRow is one pooled training example as the caller assembles it from
// the feature store: the exact vector used at prediction time joined to its
// RESOLVED forward return. Feature maps may differ in keys across rows
// (schema versions evolve) — BuildDataset takes the union and flattens with
// missing-as-zero, exactly like the per-symbol GBM trainer.
type LabeledRow struct {
	SymbolID  int64
	Ts        int64
	Features  map[string]float64
	FwdReturn float64
}

// Sample is one flattened, labeled cross-sectional example.
type Sample struct {
	SymbolID int64
	Ts       int64
	Day      string // UTC day (YYYY-MM-DD) the row belongs to
	Feat     []float64
	Y        float64 // 1 = forward return strictly above the same-day median
}

// Dataset is the assembled cross-sectional training set.
type Dataset struct {
	Keys    []string // canonical (sorted) feature-key union, self-references excluded
	Samples []Sample // ascending by (Day, Ts, SymbolID) — deterministic
	Days    []string // sorted unique UTC days that survived the thin-day gate
	// ThinDaysDropped / RowsDropped account for days below MinCrossSection —
	// surfaced so the worker's detail string states what was excluded.
	ThinDaysDropped int
	RowsDropped     int
	// DupRowsDropped counts intraday rows collapsed by the one-row-per-
	// (symbol, UTC-day) dedupe (adversarial finding H2) — surfaced so the
	// worker's detail string states how much of the raw pool was repetition.
	DupRowsDropped int
}

// excludedKey refuses model outputs and label-derived statistics as training
// inputs — the blend's own probabilities, every leg's prior output (including
// THIS model's, recorded on the vector since alphax joined the blend), and any
// accuracy statistic computed from the labels.
//
// It DELEGATES rather than restating the list. This function used to carry its
// own copy of five names, and a private copy is exactly what finding H6 cost and
// what the 2026-07-26 re-audit's A7 cost again: the shared predicate grew four
// more keys and two class rules, and this copy would have silently kept training
// alphax on forecast_prob and forecast_lift. The shared predicate also trims the
// presence suffix itself, so pred_raw__has is excluded with its base key.
func excludedKey(k string) bool {
	return gbm.SelfReferentialKey(k)
}

// hasSuffix marks the DERIVED per-feature presence indicators (M2): for every
// base key K in the dataset key union, K+"__has" is 1 when K was present in
// the row's raw map and 0 when absent. Without it, absent-as-zero makes a
// missing stocktwits_bull_ratio indistinguishable from a measured 100%-bearish
// crowd (and missing short_int_dtc/pc_total from measured zeros) — the model
// could not tell "unknown" from "extreme". Base values still flatten to 0 when
// absent; the indicator carries the missingness as its own feature.
const hasSuffix = gbm.PresenceSuffix

// Flatten turns a feature map into a fixed-dimension vector in the given key
// order; missing keys become 0 (the union key set means dimensions always
// match — same convention as the per-symbol GBM trainer). Keys ending in
// hasSuffix are DERIVED presence indicators, computed from the raw map, never
// read from it — so the training path and the live-scoring path (which both
// flatten with ds.Keys) stay consistent by construction.
func Flatten(vec map[string]float64, keys []string) []float64 {
	out := make([]float64, len(keys))
	for i, k := range keys {
		if base, ok := strings.CutSuffix(k, hasSuffix); ok {
			if _, present := vec[base]; present {
				out[i] = 1
			}
			continue
		}
		out[i] = vec[k]
	}
	return out
}

// utcDay formats a unix timestamp as its UTC calendar day.
func utcDay(ts int64) string {
	return time.Unix(ts, 0).UTC().Format("2006-01-02")
}

// BuildDataset first collapses the pool to ONE row per (symbol, UTC day) —
// keeping each symbol's LAST row of the day (max ts) — then groups by day,
// drops days with a thin cross-section (< MinCrossSection resolved rows), and
// labels each surviving row 1 when its forward return is STRICTLY ABOVE that
// day's cross-section median — relative alpha vs the same-day universe, the
// market component cancelled out.
//
// WHY the dedupe (adversarial finding H2): the PredictionRunner writes ~144
// intraday feature rows per day for a hot symbol, and every one of them
// resolves to the SAME forward return. Without collapsing, grade.N counts one
// outcome ~144 times (proven: N=480 where 60 unique outcomes existed) and the
// same-day median is row-frequency-weighted — hot symbols own the threshold,
// so a weekend "universe median" is really the crypto median. After the
// dedupe, N = unique outcomes and each symbol votes exactly once per day.
//
// KNOWN LIMITATION (M1, version pooling): the pool mixes feature-schema
// versions (v3+), so rows predating a field flatten it (and now its __has
// bit) to absent. The walk-forward grade therefore UNDER-weights the newest
// version's fields relative to the shipped TrainFull model, which sees
// proportionally more recent (newer-version) rows at the training end.
// Acceptable because the bias can only DAMPEN the grade, never flatter it —
// revisit (e.g. pin the version like the per-symbol GBM) once v6 rows
// dominate the pool and the dilution stops paying for the volume.
func BuildDataset(rows []LabeledRow) Dataset {
	var ds Dataset

	// One row per (symbol, UTC day): keep the row with the greatest ts (the
	// symbol's last word of the day; ties keep the first seen — the store
	// never emits two rows at one (symbol, horizon, ts)).
	type symDay struct {
		sym int64
		day string
	}
	lastIdx := map[symDay]int{}
	for i, r := range rows {
		k := symDay{r.SymbolID, utcDay(r.Ts)}
		if j, seen := lastIdx[k]; !seen || r.Ts > rows[j].Ts {
			if seen {
				ds.DupRowsDropped++
			}
			lastIdx[k] = i
		} else {
			ds.DupRowsDropped++
		}
	}

	byDay := map[string][]int{}
	for k, i := range lastIdx {
		byDay[k.day] = append(byDay[k.day], i)
	}

	days := make([]string, 0, len(byDay))
	for d, idx := range byDay {
		if len(idx) < MinCrossSection {
			ds.ThinDaysDropped++
			ds.RowsDropped += len(idx)
			continue
		}
		days = append(days, d)
	}
	sort.Strings(days)
	ds.Days = days

	// Canonical key union over SURVIVING rows only (a dropped day's schema
	// quirks shouldn't shape the layout). Raw keys ending in hasSuffix are
	// skipped: that suffix is reserved for the DERIVED presence indicators
	// added below, so a stored key could never shadow one.
	keySet := map[string]struct{}{}
	for _, d := range days {
		for _, i := range byDay[d] {
			for k := range rows[i].Features {
				if !excludedKey(k) && !strings.HasSuffix(k, hasSuffix) {
					keySet[k] = struct{}{}
				}
			}
		}
	}
	keys := make([]string, 0, 2*len(keySet))
	for k := range keySet {
		// M2: every base key gets a K+"__has" presence indicator so the model
		// can tell "absent" from "measured zero" (Flatten derives its value).
		keys = append(keys, k, k+hasSuffix)
	}
	sort.Strings(keys)
	ds.Keys = keys

	for _, d := range days {
		idx := byDay[d]
		med := medianFwd(rows, idx)
		for _, i := range idx {
			y := 0.0
			if rows[i].FwdReturn > med {
				y = 1
			}
			ds.Samples = append(ds.Samples, Sample{
				SymbolID: rows[i].SymbolID,
				Ts:       rows[i].Ts,
				Day:      d,
				Feat:     Flatten(rows[i].Features, keys),
				Y:        y,
			})
		}
	}
	// Deterministic order: day, then ts, then symbol.
	sort.SliceStable(ds.Samples, func(a, b int) bool {
		sa, sb := ds.Samples[a], ds.Samples[b]
		if sa.Day != sb.Day {
			return sa.Day < sb.Day
		}
		if sa.Ts != sb.Ts {
			return sa.Ts < sb.Ts
		}
		return sa.SymbolID < sb.SymbolID
	})
	return ds
}

// medianFwd is the median forward return of rows[idx].
func medianFwd(rows []LabeledRow, idx []int) float64 {
	vals := make([]float64, len(idx))
	for j, i := range idx {
		vals[j] = rows[i].FwdReturn
	}
	sort.Float64s(vals)
	n := len(vals)
	if n%2 == 1 {
		return vals[n/2]
	}
	return (vals[n/2-1] + vals[n/2]) / 2
}

// Grade is the out-of-sample report card — identical fields and meaning to
// gbm.Grade so the honesty gate and the UI treat it the same way. Lift =
// Accuracy − BaseRate; ≤ 0 means no better than always guessing the majority
// class.
type Grade struct {
	N          int     `json:"n"`
	Accuracy   float64 `json:"accuracy"`
	BrierScore float64 `json:"brierScore"`
	AUC        float64 `json:"auc"`
	BaseRate   float64 `json:"baseRate"`
	Lift       float64 `json:"lift"`
}

// dayBlock is one contiguous range of day indices [Start, End) into ds.Days.
type dayBlock struct{ Start, End int }

// splitDayBlocks slices the day list into `folds` contiguous blocks (the same
// n*f/folds boundaries gbm.Evaluate uses over samples, applied to DAYS so a
// day is never split across train and test).
func splitDayBlocks(nDays, folds int) []dayBlock {
	blocks := make([]dayBlock, 0, folds)
	for f := 0; f < folds; f++ {
		blocks = append(blocks, dayBlock{Start: nDays * f / folds, End: nDays * (f + 1) / folds})
	}
	return blocks
}

// blockPreds is one test block's out-of-sample predictions (kept per block so
// the no-lookahead property — a block's preds depend only on earlier days —
// is directly testable).
type blockPreds struct {
	Block   dayBlock
	Preds   []float64
	Actuals []float64
	Skipped bool // train slice below MinTrainRows — block not scored
	// MaxTrainDayIdx is the largest day index that entered this block's train
	// set (-1 when empty) — lets tests PROVE the embargo purge held.
	MaxTrainDayIdx int
}

// evalBlocks trains one model per test block on all samples whose day index
// ends at least `embargo` days BEFORE the block starts, then predicts the
// block. A block whose purged train set holds fewer than MinTrainRows is
// skipped (marked, not silently absorbed). Because the train filter is
// "day index < Block.Start − embargo", appending LATER days to the dataset
// cannot change an earlier block's predictions — verified in tests.
func evalBlocks(ds Dataset, blocks []dayBlock, embargo int, p gbm.Params) []blockPreds {
	dayIdx := make(map[string]int, len(ds.Days))
	for i, d := range ds.Days {
		dayIdx[d] = i
	}
	out := make([]blockPreds, 0, len(blocks))
	for _, b := range blocks {
		if b.End <= b.Start {
			continue
		}
		trainEnd := b.Start - embargo // train days: indices [0, trainEnd)
		var train []gbm.Sample
		var test []Sample
		maxTrainDay := -1
		for i := range ds.Samples {
			s := &ds.Samples[i]
			di := dayIdx[s.Day]
			switch {
			case di < trainEnd:
				train = append(train, gbm.Sample{Ts: s.Ts, Feat: s.Feat, Y: s.Y})
				if di > maxTrainDay {
					maxTrainDay = di
				}
			case di >= b.Start && di < b.End:
				test = append(test, *s)
			}
		}
		bp := blockPreds{Block: b, MaxTrainDayIdx: maxTrainDay}
		if len(train) < MinTrainRows {
			bp.Skipped = true
			out = append(out, bp)
			continue
		}
		m, err := gbm.Train(train, p)
		if err != nil {
			bp.Skipped = true
			out = append(out, bp)
			continue
		}
		for _, s := range test {
			bp.Preds = append(bp.Preds, m.Predict(s.Feat))
			bp.Actuals = append(bp.Actuals, s.Y)
		}
		out = append(out, bp)
	}
	return out
}

// Evaluate grades the pooled cross-sectional model with purged walk-forward
// splits BY DAY: the day list is cut into `folds` contiguous blocks; each
// block after the first is predicted by a model trained only on days ending
// ≥ embargoDays before it. embargoDays must be HORIZON-AWARE — the label span
// in whole days + 1 (2 for 1d, 8 for 1w): a fixed 2-day gap left 1-week train
// labels computed from closes INSIDE the test block (adversarial finding H1).
// Anything below the EmbargoDays floor is refused. ok=false (with a stated
// reason) when the dataset cannot support an honest grade — no number is
// better than a noise number.
func Evaluate(ds Dataset, folds, embargoDays int, p gbm.Params) (Grade, bool, string) {
	if folds < 2 {
		return Grade{}, false, "folds < 2"
	}
	if embargoDays < EmbargoDays {
		return Grade{}, false, "embargoDays below the 2-day floor"
	}
	if len(ds.Samples) < MinTrainRows+MinTestRows {
		return Grade{}, false, "insufficient pooled rows: need >= 1200 labeled rows across >= 10-deep days"
	}
	if len(ds.Days) < folds+embargoDays {
		return Grade{}, false, "too few distinct days for purged walk-forward folds"
	}
	blocks := splitDayBlocks(len(ds.Days), folds)
	// The first block is never tested (no earlier days to train on).
	results := evalBlocks(ds, blocks[1:], embargoDays, p)
	var preds, actuals []float64
	for _, r := range results {
		preds = append(preds, r.Preds...)
		actuals = append(actuals, r.Actuals...)
	}
	if len(preds) < MinTestRows {
		return Grade{}, false, "insufficient out-of-sample test rows after purged folds (train slices below the 1000-row floor)"
	}
	return gradeFrom(preds, actuals), true, ""
}

// TrainFull fits one gbm model on the ENTIRE dataset (for scoring the live
// cross-section AFTER Evaluate has produced the grade — never before).
func TrainFull(ds Dataset, p gbm.Params) (*gbm.Model, error) {
	if len(ds.Samples) == 0 {
		return nil, ErrInsufficientData
	}
	samples := make([]gbm.Sample, len(ds.Samples))
	for i, s := range ds.Samples {
		samples[i] = gbm.Sample{Ts: s.Ts, Feat: s.Feat, Y: s.Y}
	}
	m, err := gbm.Train(samples, p)
	if err != nil {
		return nil, ErrInsufficientData
	}
	return m, nil
}

// ── metric helpers (same math as gbm's unexported gradeFrom/aucRank, kept
// local so the grades are directly comparable without exporting gbm internals) ──

func gradeFrom(preds, actuals []float64) Grade {
	n := len(preds)
	correct := 0
	brier := 0.0
	pos := 0
	for i := range preds {
		if (preds[i] >= 0.5) == (actuals[i] >= 0.5) {
			correct++
		}
		d := preds[i] - actuals[i]
		brier += d * d
		if actuals[i] >= 0.5 {
			pos++
		}
	}
	acc := float64(correct) / float64(n)
	pPos := float64(pos) / float64(n)
	baseRate := math.Max(pPos, 1-pPos)
	return Grade{
		N:          n,
		Accuracy:   acc,
		BrierScore: brier / float64(n),
		AUC:        aucRank(preds, actuals),
		BaseRate:   baseRate,
		Lift:       acc - baseRate,
	}
}

// aucRank computes ROC AUC via the Mann-Whitney rank statistic with average
// ranks for ties (0.5 = no skill; identical method to gbm/forecast).
func aucRank(preds, actuals []float64) float64 {
	type pa struct{ p, y float64 }
	rows := make([]pa, len(preds))
	for i := range preds {
		rows[i] = pa{preds[i], actuals[i]}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].p < rows[b].p })

	ranks := make([]float64, len(rows))
	i := 0
	for i < len(rows) {
		j := i
		for j+1 < len(rows) && rows[j+1].p == rows[i].p {
			j++
		}
		avg := float64((i+1)+(j+1)) / 2.0
		for k := i; k <= j; k++ {
			ranks[k] = avg
		}
		i = j + 1
	}
	sumPosRanks, nPos, nNeg := 0.0, 0.0, 0.0
	for k, r := range rows {
		if r.y >= 0.5 {
			sumPosRanks += ranks[k]
			nPos++
		} else {
			nNeg++
		}
	}
	if nPos == 0 || nNeg == 0 {
		return 0.5
	}
	return (sumPosRanks - nPos*(nPos+1)/2) / (nPos * nNeg)
}
