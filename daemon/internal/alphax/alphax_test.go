package alphax

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
)

// dayTs returns a timestamp inside UTC day `d` (days counted from a fixed
// epoch day) offset by `sec` seconds — fixture rows land on exact days.
func dayTs(d int, sec int64) int64 {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	return base + int64(d)*86400 + sec
}

// LABEL CONSTRUCTION — hand-built two-day fixture with KNOWN medians.
//
//	Day 0: 10 rows, fwd = 0.01..0.10  → median 0.055 → 0.06..0.10 labeled 1.
//	Day 1: 11 rows, fwd = -0.05..0.05 → median 0.00  → 0.01..0.05 labeled 1
//	       (the median row itself is NOT strictly above → labeled 0).
//	Day 2: 3 rows → below MinCrossSection → dropped entirely.
func TestBuildDataset_LabelsVsSameDayMedian(t *testing.T) {
	var rows []LabeledRow
	for i := 0; i < 10; i++ { // day 0
		rows = append(rows, LabeledRow{
			SymbolID: int64(i + 1), Ts: dayTs(0, int64(i)),
			Features:  map[string]float64{"x": float64(i)},
			FwdReturn: 0.01 * float64(i+1),
		})
	}
	for i := 0; i < 11; i++ { // day 1
		rows = append(rows, LabeledRow{
			SymbolID: int64(i + 1), Ts: dayTs(1, int64(i)),
			Features:  map[string]float64{"x": float64(i)},
			FwdReturn: -0.05 + 0.01*float64(i),
		})
	}
	for i := 0; i < 3; i++ { // day 2 — thin, must be dropped
		rows = append(rows, LabeledRow{
			SymbolID: int64(i + 1), Ts: dayTs(2, int64(i)),
			Features:  map[string]float64{"x": 1},
			FwdReturn: 0.5,
		})
	}

	ds := BuildDataset(rows)
	if len(ds.Days) != 2 {
		t.Fatalf("want 2 surviving days, got %v", ds.Days)
	}
	if ds.ThinDaysDropped != 1 || ds.RowsDropped != 3 {
		t.Fatalf("thin-day accounting wrong: days=%d rows=%d", ds.ThinDaysDropped, ds.RowsDropped)
	}
	if len(ds.Samples) != 21 {
		t.Fatalf("want 21 samples, got %d", len(ds.Samples))
	}
	// Day 0 median = (0.05+0.06)/2 = 0.055: fwd 0.06..0.10 (symbols 6..10) → 1.
	// Day 1 median = 0.00: fwd 0.01..0.05 (symbols 8..11) wait — symbols 7..11
	// carry fwd 0.01..0.05? fwd(i) = -0.05+0.01i → >0 ⇔ i>=6 (symbols 7..11).
	wantOnes := map[string]bool{}
	for i := 6; i <= 10; i++ { // day 0: fwd 0.01*(i+1) > 0.055 ⇔ i+1 >= 6
		wantOnes[fmt.Sprintf("%s/%d", ds.Days[0], i)] = true
	}
	for i := 6; i <= 10; i++ { // day 1: fwd -0.05+0.01i > 0 ⇔ i >= 6
		wantOnes[fmt.Sprintf("%s/%d", ds.Days[1], i+1)] = true
	}
	ones := 0
	for _, s := range ds.Samples {
		key := fmt.Sprintf("%s/%d", s.Day, s.SymbolID)
		if s.Y == 1 {
			ones++
			if !wantOnes[key] {
				t.Fatalf("row %s labeled 1 but is not strictly above its day median", key)
			}
		} else if wantOnes[key] {
			t.Fatalf("row %s labeled 0 but IS strictly above its day median", key)
		}
	}
	if ones != 10 {
		t.Fatalf("want 10 top-half labels (5 per day), got %d", ones)
	}
}

// Day-0 label sanity pinned separately: symbol 6 (fwd 0.06) is 1 and symbol 5
// (fwd 0.05, below the 0.055 median) is 0 — the exact boundary.
func TestBuildDataset_MedianBoundary(t *testing.T) {
	var rows []LabeledRow
	for i := 0; i < 10; i++ {
		rows = append(rows, LabeledRow{
			SymbolID: int64(i + 1), Ts: dayTs(0, int64(i)),
			Features:  map[string]float64{"x": 1},
			FwdReturn: 0.01 * float64(i+1),
		})
	}
	ds := BuildDataset(rows)
	got := map[int64]float64{}
	for _, s := range ds.Samples {
		got[s.SymbolID] = s.Y
	}
	if got[5] != 0 || got[6] != 1 {
		t.Fatalf("median boundary wrong: fwd=0.05→%v (want 0), fwd=0.06→%v (want 1)", got[5], got[6])
	}
}

// The canonical key union must EXCLUDE the blend's own outputs — training on
// pred_raw/pred_cal/gbm_prob/meanrev_prob would be self-reference, not
// learning — and alphax_prob above all: the model must never train on its
// OWN output now that it rides the feature vector as a blend leg.
func TestBuildDataset_ExcludesSelfReferenceKeys(t *testing.T) {
	var rows []LabeledRow
	for i := 0; i < MinCrossSection; i++ {
		rows = append(rows, LabeledRow{
			SymbolID: int64(i + 1), Ts: dayTs(0, int64(i)),
			Features: map[string]float64{
				"pressure_score": 0.1, "x": 1,
				"pred_raw": 0.6, "pred_cal": 0.6, "gbm_prob": 0.55, "meanrev_prob": 0.45,
				"alphax_prob": 0.61,
			},
			FwdReturn: float64(i),
		})
	}
	ds := BuildDataset(rows)
	for _, k := range ds.Keys {
		switch k {
		case "pred_raw", "pred_cal", "gbm_prob", "meanrev_prob", "alphax_prob",
			// M2: the presence indicators of excluded keys leak the same
			// self-reference their values would — excluded by base name.
			"pred_raw__has", "pred_cal__has", "gbm_prob__has", "meanrev_prob__has", "alphax_prob__has":
			t.Fatalf("canonical keys must exclude %q", k)
		}
	}
	// Each surviving base key also carries its __has presence indicator (M2).
	if len(ds.Keys) != 4 {
		t.Fatalf("want keys [pressure_score pressure_score__has x x__has], got %v", ds.Keys)
	}
}

// synthDataset builds a learnable cross-sectional fixture: nSyms rows per day
// over nDays days; even symbols carry x=+1 and beat the day median, odd carry
// x=-1 and miss it. A day-level shift is added to every forward return to
// prove the label cancels the market component (the shift never changes who
// is above the median).
func synthDataset(nSyms, nDays int) Dataset {
	var rows []LabeledRow
	for d := 0; d < nDays; d++ {
		shift := 0.03 * math.Sin(float64(d)) // market-wide component
		for s := 0; s < nSyms; s++ {
			x := -1.0
			if s%2 == 0 {
				x = 1.0
			}
			rows = append(rows, LabeledRow{
				SymbolID: int64(s + 1), Ts: dayTs(d, int64(s)),
				Features:  map[string]float64{"x": x, "noise": float64((s*7+d)%5) * 0.1},
				FwdReturn: shift + 0.01*x,
			})
		}
	}
	return BuildDataset(rows)
}

// PURGED SPLIT GEOMETRY — blocks tile the day list contiguously, and no day
// within EmbargoDays of a test block ever enters that block's train set.
func TestPurgedSplitGeometry(t *testing.T) {
	blocks := splitDayBlocks(60, 5)
	if len(blocks) != 5 || blocks[0].Start != 0 || blocks[len(blocks)-1].End != 60 {
		t.Fatalf("blocks must tile [0,60): %+v", blocks)
	}
	for i := 1; i < len(blocks); i++ {
		if blocks[i].Start != blocks[i-1].End {
			t.Fatalf("blocks not contiguous: %+v", blocks)
		}
	}

	ds := synthDataset(30, 60)
	results := evalBlocks(ds, blocks[1:], EmbargoDays, gbm.Defaults())
	scored := 0
	for _, r := range results {
		if r.Skipped {
			continue
		}
		scored++
		if r.MaxTrainDayIdx >= r.Block.Start-EmbargoDays {
			t.Fatalf("EMBARGO VIOLATED: train day %d within %d days of test block start %d",
				r.MaxTrainDayIdx, EmbargoDays, r.Block.Start)
		}
	}
	if scored == 0 {
		t.Fatal("no block was scored — fixture too small to exercise the purge")
	}
}

// NO LOOKAHEAD — appending LATER days to the dataset must not change an
// earlier block's out-of-sample predictions (its train set is defined purely
// by "day index < blockStart - embargo").
func TestNoLookahead_LaterDaysDontChangeEarlierFolds(t *testing.T) {
	small := synthDataset(30, 40)
	large := synthDataset(30, 48) // same first 40 days + 8 later days

	// The same explicit early block evaluated against both datasets. (Train =
	// days < 36-2 = 34 → 34×30 = 1020 rows, above the MinTrainRows floor.)
	blocks := []dayBlock{{Start: 36, End: 40}}
	rs := evalBlocks(small, blocks, EmbargoDays, gbm.Defaults())
	rl := evalBlocks(large, blocks, EmbargoDays, gbm.Defaults())
	if len(rs) != 1 || len(rl) != 1 || rs[0].Skipped || rl[0].Skipped {
		t.Fatalf("block should be scored in both datasets: %+v / %+v", rs, rl)
	}
	if len(rs[0].Preds) != len(rl[0].Preds) {
		t.Fatalf("pred counts differ: %d vs %d", len(rs[0].Preds), len(rl[0].Preds))
	}
	for i := range rs[0].Preds {
		if rs[0].Preds[i] != rl[0].Preds[i] {
			t.Fatalf("pred %d changed when later days were appended: %v -> %v (lookahead!)",
				i, rs[0].Preds[i], rl[0].Preds[i])
		}
	}
}

// Full Evaluate on a learnable cross-section: positive lift, honest AUC.
func TestEvaluate_LearnableSetShowsLift(t *testing.T) {
	ds := synthDataset(30, 60)
	g, ok, reason := Evaluate(ds, DefaultFolds, EmbargoDays, gbm.Defaults())
	if !ok {
		t.Fatalf("Evaluate refused a %d-row learnable set: %s", len(ds.Samples), reason)
	}
	if g.N < MinTestRows {
		t.Fatalf("scored too few OOS rows: %d", g.N)
	}
	if g.Lift <= 0 || g.AUC <= 0.5 {
		t.Fatalf("learnable set should grade positive: lift=%.3f auc=%.3f", g.Lift, g.AUC)
	}
}

// GATES — thin data must be an honest refusal (ok=false, stated reason),
// never a noise grade.
func TestEvaluate_ThinDataRefused(t *testing.T) {
	// Too few rows overall.
	small := synthDataset(12, 8)
	if _, ok, reason := Evaluate(small, DefaultFolds, EmbargoDays, gbm.Defaults()); ok || reason == "" {
		t.Fatalf("tiny set must be refused with a reason, ok=%v reason=%q", ok, reason)
	}
	// Enough rows but too few distinct days for purged folds.
	fat := synthDataset(300, 5)
	if _, ok, reason := Evaluate(fat, DefaultFolds, EmbargoDays, gbm.Defaults()); ok || reason == "" {
		t.Fatalf("5-day set must be refused (folds+embargo need more days), ok=%v reason=%q", ok, reason)
	}
	// Empty dataset.
	if _, ok, _ := Evaluate(Dataset{}, DefaultFolds, EmbargoDays, gbm.Defaults()); ok {
		t.Fatal("empty dataset must be refused")
	}
	// An embargo below the 2-day floor can never purge even 1d labels.
	if _, ok, reason := Evaluate(synthDataset(30, 60), DefaultFolds, 1, gbm.Defaults()); ok || reason == "" {
		t.Fatalf("sub-floor embargo must be refused, ok=%v reason=%q", ok, reason)
	}
}

// ── ADVERSARIAL-REVIEW regression tests ─────────────────────────────────

// H1 — HORIZON-AWARE EMBARGO. For 1-week geometry the embargo must be 8 days
// (label span 7 + 1): for every scored fold, last-train-day + label-span must
// land STRICTLY BEFORE the first test day, so no train label is computed from
// a close inside the test block. The same check run with the old fixed 2-day
// embargo must show the violation this fix removes.
func TestEmbargo_HorizonAware_1wPurgesLabelSpan(t *testing.T) {
	const labelSpanDays = 7             // a 1w label's close lies up to 7 days ahead
	const embargo1w = labelSpanDays + 1 // what the trainer passes for H1w

	ds := synthDataset(30, 80)
	blocks := splitDayBlocks(len(ds.Days), 5)

	scored := 0
	for _, r := range evalBlocks(ds, blocks[1:], embargo1w, gbm.Defaults()) {
		if r.Skipped {
			continue
		}
		scored++
		if r.MaxTrainDayIdx+labelSpanDays >= r.Block.Start {
			t.Fatalf("1w LABEL LEAKS INTO TEST BLOCK: last train day %d + span %d >= first test day %d",
				r.MaxTrainDayIdx, labelSpanDays, r.Block.Start)
		}
	}
	if scored == 0 {
		t.Fatal("no block was scored — fixture too small to exercise the 8-day purge")
	}

	// Prove the test bites: the OLD fixed 2-day embargo violates the same
	// property (train labels reach into the test block) on this geometry.
	violated := false
	for _, r := range evalBlocks(ds, blocks[1:], EmbargoDays, gbm.Defaults()) {
		if !r.Skipped && r.MaxTrainDayIdx+labelSpanDays >= r.Block.Start {
			violated = true
		}
	}
	if !violated {
		t.Fatal("fixture no longer demonstrates the 2-day-embargo leak — regression test is dead")
	}
}

// H2 — ONE ROW PER (SYMBOL, UTC-DAY). Replicating every row ~4× intraday
// (identical resolved fwd_return, as the 10-minute PredictionRunner does) must
// change NOTHING: same sample count (= unique outcomes, so grade.N stops
// counting one outcome 144×), same labels (the day median is no longer
// row-frequency-weighted), and the kept row is the symbol's LAST of the day.
func TestBuildDataset_DedupesToOneRowPerSymbolDay(t *testing.T) {
	base := func(dup bool) []LabeledRow {
		var rows []LabeledRow
		for d := 0; d < 3; d++ {
			for s := 0; s < 12; s++ {
				fwd := 0.01 * float64(s-6)
				n := 1
				if dup && s < 4 { // "hot" symbols write intraday repeats
					n = 4
				}
				for r := 0; r < n; r++ {
					rows = append(rows, LabeledRow{
						SymbolID: int64(s + 1), Ts: dayTs(d, int64(600*r+s)),
						// Marker so the max-ts row is identifiable: only the
						// symbol's LAST row of the day carries last=1.
						Features:  map[string]float64{"x": float64(s), "last": float64(r+1) / float64(n)},
						FwdReturn: fwd, // one resolved outcome, repeated verbatim
					})
				}
			}
		}
		return rows
	}

	clean := BuildDataset(base(false))
	dup := BuildDataset(base(true))

	if dup.DupRowsDropped != 3*4*3 { // 3 days × 4 hot symbols × 3 extra rows
		t.Fatalf("DupRowsDropped = %d, want 36", dup.DupRowsDropped)
	}
	if len(dup.Samples) != len(clean.Samples) || len(dup.Samples) != 3*12 {
		t.Fatalf("N must equal unique (symbol,day) outcomes: dup=%d clean=%d want 36",
			len(dup.Samples), len(clean.Samples))
	}
	// Same (day, symbol) → same label: duplication moved neither the median
	// nor any row's side of it.
	labels := func(ds Dataset) map[string]float64 {
		m := map[string]float64{}
		for _, s := range ds.Samples {
			m[fmt.Sprintf("%s/%d", s.Day, s.SymbolID)] = s.Y
		}
		return m
	}
	cl, dl := labels(clean), labels(dup)
	for k, v := range cl {
		if dl[k] != v {
			t.Fatalf("label for %s changed under duplication: %v -> %v", k, v, dl[k])
		}
	}
	// The surviving row is the LAST of the day (max ts), not an arbitrary one.
	lastIdx := -1
	for i, k := range dup.Keys {
		if k == "last" {
			lastIdx = i
		}
	}
	if lastIdx < 0 {
		t.Fatalf("marker key missing from %v", dup.Keys)
	}
	for _, s := range dup.Samples {
		if s.Feat[lastIdx] != 1 {
			t.Fatalf("symbol %d day %s kept a non-final intraday row (last=%v)", s.SymbolID, s.Day, s.Feat[lastIdx])
		}
	}
}

// M2 — PRESENCE INDICATORS. An absent feature and a measured zero must
// produce DIFFERENT flattened vectors (K__has 1 vs 0), and the live-scoring
// path — which flattens an arbitrary raw map with ds.Keys through the same
// Flatten — must carry the indicator identically.
func TestFlatten_PresenceIndicatorsDistinguishAbsentFromZero(t *testing.T) {
	var rows []LabeledRow
	for i := 0; i < MinCrossSection; i++ {
		vec := map[string]float64{"x": float64(i)}
		if i%2 == 0 {
			vec["stocktwits_bull_ratio"] = 0 // measured 100%-bearish crowd
		} // odd rows: source absent — NOT the same thing
		rows = append(rows, LabeledRow{
			SymbolID: int64(i + 1), Ts: dayTs(0, int64(i)),
			Features: vec, FwdReturn: float64(i),
		})
	}
	ds := BuildDataset(rows)

	hasIdx, valIdx := -1, -1
	for i, k := range ds.Keys {
		switch k {
		case "stocktwits_bull_ratio":
			valIdx = i
		case "stocktwits_bull_ratio__has":
			hasIdx = i
		}
	}
	if hasIdx < 0 || valIdx < 0 {
		t.Fatalf("key union must carry the feature AND its __has indicator, got %v", ds.Keys)
	}

	present := Flatten(map[string]float64{"x": 1, "stocktwits_bull_ratio": 0}, ds.Keys)
	absent := Flatten(map[string]float64{"x": 1}, ds.Keys)
	if present[valIdx] != 0 || absent[valIdx] != 0 {
		t.Fatalf("base value flattening changed: present=%v absent=%v", present[valIdx], absent[valIdx])
	}
	if present[hasIdx] != 1 || absent[hasIdx] != 0 {
		t.Fatalf("__has must be 1 when present / 0 when absent, got present=%v absent=%v",
			present[hasIdx], absent[hasIdx])
	}
	// The whole point: the two vectors must differ.
	same := true
	for i := range present {
		if present[i] != absent[i] {
			same = false
		}
	}
	if same {
		t.Fatal("present-zero and absent flatten to identical vectors — model cannot see missingness")
	}
	// A raw map may never smuggle a literal __has key into the union.
	for _, k := range BuildDataset([]LabeledRow{{
		SymbolID: 1, Ts: dayTs(0, 0),
		Features: map[string]float64{"x__has": 5, "x": 1}, FwdReturn: 0,
	}}).Keys {
		if k == "x__has__has" {
			t.Fatalf("reserved suffix leaked into the union: %v", k)
		}
	}
	// Training samples themselves carry the indicator (even rows present=1,
	// odd rows absent=0).
	for _, s := range ds.Samples {
		want := 0.0
		if (s.SymbolID-1)%2 == 0 {
			want = 1
		}
		if s.Feat[hasIdx] != want {
			t.Fatalf("sample sym=%d __has=%v want %v", s.SymbolID, s.Feat[hasIdx], want)
		}
	}
}
