package forecast

// ── Cross-evaluator purge invariant (the A15 defect CLASS) ──────────────────
//
// A15 existed because the purged/embargoed walk-forward invariant lived in
// three packages (gbm, alphax, metalabel) and the fourth — this one — forgot
// it. The defect class is not "a purge is computed wrong" but "an evaluator
// exists that never joined the discipline at all". purge_test.go pins this
// package's own purge; this file pins the other direction: every evaluator in
// the daemon, present or future, is enumerated and held to the same invariant
// — the train/test gap of every fold must cover the label horizon (fwdBars).
//
// Two tests share one registry (purgedEvaluatorCases):
//
//   1. TestPurgedEvaluators_EnumerationIsClosed discovers, from source, every
//      package under daemon/internal that grades with an embargoed
//      walk-forward — it declares `func Evaluate(` and its sources speak of a
//      purge or embargo — and fails on COUNT unless each discovered package is
//      registered below. A fifth evaluator added later trips this test until
//      it registers a gap assertion of its own. (An evaluator that omits the
//      purge wholesale is beyond textual discovery; the registry at least
//      guarantees no conforming evaluator is graded without a pinned gap.)
//
//   2. TestPurgedEvaluators_TrainTestGapCoversLabelHorizon runs, per
//      registered evaluator, its REAL exported grading path on an
//      overlapping-label fixture and asserts the split construction holds a
//      train→test gap of at least the label horizon.

import (
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/alphax"
	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	"github.com/nyaungnicholas-wq/signaldeck/internal/metalabel"
)

// purgedEvaluatorCase registers one evaluator: the package directory name the
// source scan must find, and the assertion that its split enforces the gap.
type purgedEvaluatorCase struct {
	pkg       string
	assertGap func(t *testing.T)
}

// purgedEvaluatorCases is the registry. Adding an evaluator package to the
// daemon means adding an entry here — the enumeration test enforces exactly
// that, in both directions (unregistered discovery and stale registration).
func purgedEvaluatorCases() []purgedEvaluatorCase {
	return []purgedEvaluatorCase{
		{pkg: "forecast", assertGap: assertForecastGap},
		{pkg: "gbm", assertGap: assertGBMGap},
		{pkg: "metalabel", assertGap: assertMetalabelGap},
		{pkg: "alphax", assertGap: assertAlphaxGap},
	}
}

var (
	// evaluateDecl marks a package as an evaluator: a top-level exported
	// Evaluate is the house-wide grading entry point in all four packages.
	evaluateDecl = regexp.MustCompile(`(?m)^func Evaluate\(`)
	// purgeWord marks it as one that claims the purge discipline.
	purgeWord = regexp.MustCompile(`(?i)embargo|purge`)
)

// discoverPurgedEvaluators scans every non-test .go file under daemon/internal
// (the test binary runs in this package's directory, so that is "../") and
// returns the sorted package directories that both declare func Evaluate and
// mention the purge/embargo. Today that is exactly the four registered below.
func discoverPurgedEvaluators(t *testing.T) []string {
	t.Helper()
	type marks struct{ evaluate, purge bool }
	perDir := map[string]*marks{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("..", filepath.Dir(path))
		if err != nil {
			return err
		}
		m := perDir[rel]
		if m == nil {
			m = &marks{}
			perDir[rel] = m
		}
		if evaluateDecl.Match(src) {
			m.evaluate = true
		}
		if purgeWord.Match(src) {
			m.purge = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking daemon/internal: %v", err)
	}
	var out []string
	for dir, m := range perDir {
		if m.evaluate && m.purge {
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

func TestPurgedEvaluators_EnumerationIsClosed(t *testing.T) {
	discovered := discoverPurgedEvaluators(t)
	var registered []string
	for _, c := range purgedEvaluatorCases() {
		registered = append(registered, c.pkg)
	}
	sort.Strings(registered)

	if len(discovered) != len(registered) {
		t.Fatalf("daemon/internal holds %d purged-walk-forward evaluators %v but %d are registered %v — "+
			"a new evaluator must add a purgedEvaluatorCases entry asserting its train/test gap covers its label horizon (A15's defect class)",
			len(discovered), discovered, len(registered), registered)
	}
	for i := range discovered {
		if discovered[i] != registered[i] {
			t.Fatalf("evaluator enumeration mismatch: discovered %v, registered %v", discovered, registered)
		}
	}
}

func TestPurgedEvaluators_TrainTestGapCoversLabelHorizon(t *testing.T) {
	for _, c := range purgedEvaluatorCases() {
		t.Run(c.pkg, c.assertGap)
	}
}

// assertReportedGap checks the arithmetic that makes a reported purge a real
// gap, for the three evaluators that read the horizon off their samples and
// report the purge in the grade: the evaluator must have measured the declared
// horizon (LabelSpan == horizon), kept every training label at least
// LabelSpan+EmbargoSpan short of its test block (gap >= horizon by
// construction), and physically removed at least horizon-1 straddling rows per
// graded boundary — on a one-row-per-tick fixture a smaller count cannot
// coexist with the gap.
func assertReportedGap(t *testing.T, labelSpan, embargoSpan int64, purged int, horizon int64, boundaries int) {
	t.Helper()
	if labelSpan != horizon {
		t.Fatalf("read label span %d off the data, want the declared horizon %d", labelSpan, horizon)
	}
	if gap := labelSpan + embargoSpan; gap < horizon {
		t.Fatalf("enforced train/test gap %d < label horizon %d — LEAKAGE", gap, horizon)
	}
	if want := boundaries * int(horizon-1); purged < want || purged == 0 {
		t.Fatalf("purged %d training rows, need >= %d (%d boundaries x (horizon-1)) and > 0 for the gap to physically hold",
			purged, want, boundaries)
	}
}

func assertForecastGap(t *testing.T) {
	const fwd, folds = 5, 5
	g, err := Evaluate(barsFromCloses(noiseCloses(900, 21)), fwd, folds)
	if err != nil {
		t.Fatalf("forecast fixture must grade: %v", err)
	}
	assertReportedGap(t, int64(g.LabelSpan), int64(g.EmbargoSpan), g.PurgedTrainRows, fwd, folds-1)
}

func assertGBMGap(t *testing.T) {
	const n, horizon, folds = 600, 5, 5
	rng := rand.New(rand.NewSource(4))
	samples := make([]gbm.Sample, n)
	for i := range samples {
		samples[i] = gbm.Sample{
			Ts:   int64(i), // one row per tick, labels overlap every boundary
			Feat: []float64{rng.Float64(), rng.Float64(), rng.Float64()},
			Y:    float64(rng.Intn(2)),
		}
	}
	g, err := gbm.Evaluate(gbm.WithLabelSpan(samples, horizon), folds, gbm.Defaults())
	if err != nil {
		t.Fatalf("gbm fixture must grade: %v", err)
	}
	assertReportedGap(t, g.LabelSpan, g.EmbargoSpan, g.PurgedTrainRows, horizon, folds-1)
}

func assertMetalabelGap(t *testing.T) {
	const n, horizon = 1000, 5
	// The geometric boundary at 60 purges its own training set below gbm's
	// floor and is skipped, so demand the 4 retrains that can actually train.
	const folds = 4
	rng := rand.New(rand.NewSource(9))
	cands := make([]metalabel.Sample, n)
	for i := range cands {
		p := 0.55 // alternate sides so every row is a directional candidate
		if i%2 == 1 {
			p = 0.45
		}
		cands[i] = metalabel.Sample{
			Ts:          int64(i),
			PrimaryProb: p,
			Context:     []float64{rng.Float64(), rng.Float64(), rng.Float64()},
			FwdReturn:   (rng.Float64() - 0.5) * 0.04,
		}
	}
	g, err := metalabel.Evaluate(metalabel.WithLabelSpan(cands, horizon), folds, 0.001, metalabel.DefaultThreshold)
	if err != nil {
		t.Fatalf("metalabel fixture must grade: %v", err)
	}
	assertReportedGap(t, g.LabelSpan, g.EmbargoSpan, g.PurgedTrainRows, horizon, g.TrainedFolds)
}

// assertAlphaxGap is the odd one out because alphax's rows declare no
// LabelEnd: the gap is the caller-passed embargoDays, floored at EmbargoDays
// and required by contract to be the label span in days + 1 (finding H1). So
// the invariant is asserted behaviorally: (a) a gap below the floor is
// refused; (b) the split really ignores every day inside the gap — rewriting
// their labels cannot move the grade — while (c) rewriting a TRAIN day's
// labels must move it, proving (b) had teeth.
func assertAlphaxGap(t *testing.T) {
	if _, ok, reason := alphax.Evaluate(alphax.Dataset{}, 2, alphax.EmbargoDays-1, gbm.Defaults()); ok || !strings.Contains(reason, "embargo") {
		t.Fatalf("alphax: embargoDays below the floor must be refused, got ok=%v reason=%q", ok, reason)
	}

	const days, syms, horizonDays = 60, 50, 5
	const embargoDays = horizonDays + 1 // the horizon-aware contract
	// folds=2 puts the single tested block at day 30 with train days [0, 24),
	// so the gap days are exactly 24..29.
	base := alphaxRows(days, syms)
	gapMut := alphaxRows(days, syms)
	mutateDayRange(gapMut, 24, 30)
	trainMut := alphaxRows(days, syms)
	mutateDayRange(trainMut, 10, 18)

	g0 := mustAlphaxGrade(t, base, embargoDays)
	g1 := mustAlphaxGrade(t, gapMut, embargoDays)
	g2 := mustAlphaxGrade(t, trainMut, embargoDays)
	if g0 != g1 {
		t.Fatalf("alphax: rewriting labels INSIDE the %d-day gap moved the grade — the split reached into the gap:\n before %+v\n after  %+v",
			embargoDays, g0, g1)
	}
	if g0 == g2 {
		t.Fatalf("alphax: control failed — rewriting TRAIN-day labels left the grade identical, so the gap identity above proves nothing")
	}
}

// alphaxRows builds one labeled row per (symbol, UTC day): `syms`-deep
// cross-sections over `days` days, features and forward returns pure noise.
// Deterministic so the mutation comparisons compare models, not seeds.
func alphaxRows(days, syms int) []alphax.LabeledRow {
	rng := rand.New(rand.NewSource(17))
	rows := make([]alphax.LabeledRow, 0, days*syms)
	for d := 0; d < days; d++ {
		for s := 0; s < syms; s++ {
			rows = append(rows, alphax.LabeledRow{
				SymbolID:  int64(s + 1),
				Ts:        int64(d)*86400 + int64(s),
				Features:  map[string]float64{"f1": rng.Float64(), "f2": rng.Float64(), "f3": rng.Float64()},
				FwdReturn: (rng.Float64() - 0.5) * 0.02,
			})
		}
	}
	return rows
}

// mutateDayRange rewrites the forward returns — and therefore the labels —
// of every row whose day index falls in [lo, hi).
func mutateDayRange(rows []alphax.LabeledRow, lo, hi int) {
	for i := range rows {
		if d := int(rows[i].Ts / 86400); d >= lo && d < hi {
			rows[i].FwdReturn = -rows[i].FwdReturn + 0.0137
		}
	}
}

func mustAlphaxGrade(t *testing.T, rows []alphax.LabeledRow, embargoDays int) alphax.Grade {
	t.Helper()
	g, ok, reason := alphax.Evaluate(alphax.BuildDataset(rows), 2, embargoDays, gbm.Defaults())
	if !ok {
		t.Fatalf("alphax fixture must grade, refused: %s", reason)
	}
	return g
}
