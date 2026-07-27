// HONESTY-GAP WAVE (2026-07-25) — the workers behind the four gaps
// PREDICTION_PROCESS.md left open, plus the two that had no runtime at all.
//
//   - feature-redundancy-runner (24h): measures how many INDEPENDENT inputs the
//     feature vector really has, and publishes the clustering so the honest
//     count is visible instead of the field count.
//   - return-distribution-runner (6h): the replacement for the binary up/down
//     target — a cost-aware conditional return DISTRIBUTION per symbol+horizon,
//     graded walk-forward against its own climatology so conditioning has to
//     earn its place exactly like every other leg.
//   - dataset-version-runner (24h): content-hashes each symbol's daily bars and
//     raises a data-quality event when a provider REWRITES history inside a
//     range a claim was already measured on.
//   - canary-runner (1h): grades the newest feature/model version against the
//     previous one and refuses to let a new version inherit production
//     automatically.
//   - price-validator (24h, opt-in): compares stored closes against an
//     independent second source and lowers confidence on disagreement.
//
// Every one of these is FAIL-QUIET by construction: a read error, a thin
// sample or a missing source ends in an honest absence (and a dq event where a
// human should know), never in a fabricated number and never in a failed pass
// that stalls the fleet.
package pipeline

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
	"github.com/nyaungnicholas-wq/signaldeck/internal/datasetver"
	"github.com/nyaungnicholas-wq/signaldeck/internal/distribution"
	"github.com/nyaungnicholas-wq/signaldeck/internal/featureredundancy"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pricecheck"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// distHorizons are the horizons a return distribution is computed for. 1h is
// excluded deliberately: the daily bars this uses cannot express an hourly
// forward return, and estimating one from them would be the look-ahead error
// this platform already fixed once.
var distHorizons = []md.Horizon{md.H1d, md.H1w}

// horizonBars maps a horizon to its forward span in TRADING DAYS.
func horizonBars(h md.Horizon) int {
	switch h {
	case md.H1w:
		return 5
	default:
		return 1
	}
}

const (
	// distTau is the round-trip cost threshold that defines the no-trade band,
	// in return units (10bps). It matches meanRevCost so the mean-reversion leg
	// and the distribution price the same friction — two different costs for one
	// platform would make their outputs incomparable.
	distTau = 0.001
	// distLookbackDays of daily bars loaded per symbol.
	distLookbackDays = 1200
	// volWindow is the trailing window whose median splits elevated from calm.
	// It matches the validated vol-regime work's ranking window.
	volWindow = 200
	// volShort is the realized-vol estimator's window.
	volShort = 20
	// maxEvalPoints caps the walk-forward grade's evaluation points per symbol
	// and horizon. The grade is a skill measurement, not a backtest; a few
	// hundred causal points settle it and keep a 500-symbol pass affordable.
	maxEvalPoints = 200
	// minGradeTrain is the least history required before a walk-forward
	// evaluation point may be graded.
	minGradeTrain = 250
	// maxSaneDailyReturn refuses windows containing a split-corruption artifact,
	// the same guard the structural predictors use.
	maxSaneDailyReturn = 0.65
	// distSymbolsPerPass / datasetSymbolsPerPass bound one rotating sweep. With
	// a ~330-symbol universe these cover the fleet in roughly a day of passes,
	// which is the right cadence for inputs that only move daily.
	distSymbolsPerPass    = 60
	datasetSymbolsPerPass = 40
	// Rotation cursors (meta keys).
	metaDistCursor    = "return_distribution_cursor"
	metaDatasetCursor = "dataset_version_cursor"
)

// ─────────────────────────────────────────────────────────────────────────
// Bounded sweeps.
//
// MEASURED 2026-07-25 on the live 2.1GB database: a naive
// every-symbol-every-pass sweep wrote only two rows in eighteen minutes. The
// cost is not arithmetic — one symbol's distribution benchmarks at 110ms — it
// is that ~40 workers share ONE SQLite write connection (and the pure-Go driver
// is 10-60x slower than C), so a worker that queues 500 reads and 500 writes in
// a tight loop starves behind the rest of the fleet and never finishes.
//
// Both heavy sweeps are therefore ROTATING and TIME-BOXED, the pattern this
// repo already uses for the EDGAR pollers: each pass takes the next slice of
// symbols from a persisted cursor, stops when its wall-clock budget is spent,
// and leaves the cursor where it stopped so the next pass resumes there. Full
// coverage takes several passes instead of one, which is the correct trade for
// a forecast whose inputs only move daily — and it is bounded on ANY machine,
// rather than bounded by an assumption about this one.

// sweepBudget is the wall-clock a single heavy pass may consume before it
// yields to the rest of the fleet.
const sweepBudget = 90 * time.Second

// rotate returns the next `size` symbols starting from the persisted cursor,
// together with the cursor value to store once the pass completes. A cursor
// past the end wraps to the beginning, so coverage is round-robin and no symbol
// can be starved indefinitely.
func rotate(ctx context.Context, st *store.Store, metaKey string, syms []md.Symbol, size int) ([]md.Symbol, func(processed int) string) {
	if len(syms) == 0 {
		return nil, func(int) string { return "0" }
	}
	raw, _ := st.GetMeta(ctx, metaKey)
	start, _ := strconv.Atoi(strings.TrimSpace(raw))
	if start < 0 || start >= len(syms) {
		start = 0
	}
	end := start + size
	if end > len(syms) {
		end = len(syms)
	}
	slice := syms[start:end]
	return slice, func(processed int) string {
		next := start + processed
		if next >= len(syms) {
			next = 0 // wrapped: start the next lap
		}
		return strconv.Itoa(next)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// A. Return distributions.

// ReturnDistributionRunner computes the cost-aware conditional return
// distribution per symbol+horizon.
type ReturnDistributionRunner struct {
	St  *store.Store
	Now func() time.Time
}

func (w *ReturnDistributionRunner) Name() string { return "return-distribution-runner" }

// Interval is 2h, not 6h: each pass sweeps a bounded slice of the universe, so
// a shorter cadence is what gives the rotation a full lap inside a day.
func (w *ReturnDistributionRunner) Interval() time.Duration { return 2 * time.Hour }

func (w *ReturnDistributionRunner) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *ReturnDistributionRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := w.now()
	from := now.AddDate(0, 0, -distLookbackDays).Unix()
	var stored, refused, graded, processed int
	slice, cursorFor := rotate(ctx, w.St, metaDistCursor, syms, distSymbolsPerPass)
	deadline := time.Now().Add(sweepBudget)
	for _, s := range slice {
		if time.Now().After(deadline) {
			break // time-boxed: yield to the fleet, resume here next pass
		}
		processed++
		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, from, now.Unix()+1, 0)
		if err != nil {
			return "", err
		}
		closes := closeSeries(bars)
		any := false
		for _, h := range distHorizons {
			f, ok := buildReturnForecast(closes, h, now.Unix())
			if !ok {
				continue
			}
			f.SymbolID = s.ID
			if f.Skill != nil {
				graded++
			}
			if err := w.St.UpsertReturnForecast(ctx, f); err != nil {
				return "", err
			}
			stored++
			any = true
		}
		if !any {
			// Honest absence: clear anything stale so a refusal cannot leave an
			// old distribution standing as if it were current.
			refused++
			if err := w.St.DeleteReturnForecasts(ctx, s.ID); err != nil {
				return "", err
			}
		}
	}
	if err := w.St.SetMeta(ctx, metaDistCursor, cursorFor(processed)); err != nil {
		return "", err
	}
	return fmt.Sprintf("distributions stored=%d graded=%d refused=%d (swept %d of %d this pass)",
		stored, graded, refused, processed, len(syms)), nil
}

// closeSeries extracts ascending closes, dropping non-positive prices (a
// different defect, guarded elsewhere) so no return is computed against zero.
func closeSeries(bars []md.Bar) []float64 {
	out := make([]float64, 0, len(bars))
	sorted := append([]md.Bar(nil), bars...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ts < sorted[j].Ts })
	for _, b := range sorted {
		if b.Close > 0 {
			out = append(out, b.Close)
		}
	}
	return out
}

// buildReturnForecast produces one symbol+horizon's distribution, conditioned on
// the CURRENT volatility regime, plus a walk-forward grade of whether that
// conditioning beats climatology. ok=false when the history is too thin or
// contaminated — the caller then stores nothing.
func buildReturnForecast(closes []float64, h md.Horizon, ts int64) (store.ReturnForecast, bool) {
	span := horizonBars(h)
	if len(closes) < volWindow+volShort+span+distribution.MinSample {
		return store.ReturnForecast{}, false
	}
	rets := simpleReturns(closes)
	for _, r := range rets {
		if math.Abs(r) > maxSaneDailyReturn {
			// Split-corruption artifact: refuse the window rather than forecast
			// a distribution whose width is an accounting error.
			return store.ReturnForecast{}, false
		}
	}
	regimes := volRegimeSeries(rets)

	// Forward returns indexed by their DECISION bar: fwd[i] is the return an
	// observer standing at bar i would go on to realize. Only i where the whole
	// forward window exists are defined, so nothing here reads a bar that had
	// not happened.
	fwd := make([]float64, len(closes))
	defined := make([]bool, len(closes))
	for i := 0; i+span < len(closes); i++ {
		fwd[i] = closes[i+span]/closes[i] - 1
		defined[i] = true
	}

	cur := regimes[len(regimes)-1]
	var cond []float64
	for i := volWindow + volShort; i < len(closes); i++ {
		if !defined[i] || i >= len(regimes) {
			continue
		}
		if regimes[i] == cur {
			cond = append(cond, fwd[i])
		}
	}
	d, ok := distribution.Forecast(cond, distTau)
	if !ok {
		return store.ReturnForecast{}, false
	}

	f := store.ReturnForecast{
		Horizon: h, Ts: ts, Regime: cur, N: d.N, Tau: d.Tau, Mean: d.Mean,
		Sigma: d.Sigma, Q10: d.Q10, Q50: d.Q50, Q90: d.Q90, PUp: d.PUp,
		PDown: d.PDown, PInside: d.PInside, Edge: d.Edge,
		ExpectedValue: d.ExpectedValue,
	}
	if g, ok := gradeConditioning(closes, rets, regimes, fwd, defined, span); ok {
		skill, cov := g.Skill, g.Coverage80
		f.Skill, f.Coverage80, f.GradedN = &skill, &cov, g.N
	}
	return f, true
}

// gradeConditioning walk-forward grades the conditional distribution against
// climatology. At each evaluation point the two forecasts are built from bars
// STRICTLY BEFORE it, then scored on the return that followed — so the grade is
// out-of-sample by construction and no evaluation point contributes to the
// forecast that graded it.
func gradeConditioning(closes, rets []float64, regimes []string, fwd []float64, defined []bool, span int) (distribution.QuantileGrade, bool) {
	last := len(closes) - span - 1
	if last <= minGradeTrain {
		return distribution.QuantileGrade{}, false
	}
	step := 1
	if n := last - minGradeTrain; n > maxEvalPoints {
		step = n / maxEvalPoints
	}
	var pairs []distribution.QPair
	for i := minGradeTrain; i <= last; i += step {
		if !defined[i] || i >= len(regimes) {
			continue
		}
		// Training set: decision bars whose forward window CLOSED before i, so
		// no label overlaps the point being graded (the embargo discipline the
		// purged walk-forward gate uses).
		var cond, clim []float64
		cut := i - span
		for j := volWindow + volShort; j < cut; j++ {
			if !defined[j] || j >= len(regimes) {
				continue
			}
			clim = append(clim, fwd[j])
			if regimes[j] == regimes[i] {
				cond = append(cond, fwd[j])
			}
		}
		cd, okc := distribution.Forecast(cond, distTau)
		bd, okb := distribution.Forecast(clim, distTau)
		if !okc || !okb {
			continue
		}
		pairs = append(pairs, distribution.QPair{Cond: cd, Clim: bd, Realized: fwd[i]})
	}
	if len(pairs) == 0 {
		return distribution.QuantileGrade{}, false
	}
	g := distribution.GradeQuantiles(pairs)
	return g, g.Meaningful
}

// simpleReturns is the close-to-close return series.
func simpleReturns(closes []float64) []float64 {
	out := make([]float64, len(closes))
	for i := 1; i < len(closes); i++ {
		out[i] = closes[i]/closes[i-1] - 1
	}
	return out
}

// volRegimeSeries labels each bar "elevated" or "calm" by ranking a trailing
// short-window realized vol inside its own trailing distribution — the same
// causal construction as the validated vol-regime predictor, and the axis this
// platform has actually demonstrated skill on. Bars before the warm-up are
// labeled "" and are never used as decision bars.
func volRegimeSeries(rets []float64) []string {
	out := make([]string, len(rets))
	vol := make([]float64, len(rets))
	for i := range rets {
		if i < volShort {
			continue
		}
		var ss float64
		for j := i - volShort + 1; j <= i; j++ {
			ss += rets[j] * rets[j]
		}
		vol[i] = math.Sqrt(ss / float64(volShort))
	}
	for i := range rets {
		if i < volShort+volWindow {
			continue
		}
		var below, total int
		for j := i - volWindow; j < i; j++ {
			if vol[j] == 0 {
				continue
			}
			total++
			if vol[j] < vol[i] {
				below++
			}
		}
		if total == 0 {
			continue
		}
		if float64(below)/float64(total) >= 0.5 {
			out[i] = "elevated"
		} else {
			out[i] = "calm"
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────
// B. Feature redundancy.

// FeatureRedundancyRunner publishes the honest count of independent inputs.
type FeatureRedundancyRunner struct {
	St  *store.Store
	Now func() time.Time
}

func (w *FeatureRedundancyRunner) Name() string            { return "feature-redundancy-runner" }
func (w *FeatureRedundancyRunner) Interval() time.Duration { return 24 * time.Hour }

// redundancySampleCap bounds the labeled rows pulled per horizon.
const redundancySampleCap = 6000

func (w *FeatureRedundancyRunner) Run(ctx context.Context) (string, error) {
	type published struct {
		Horizon    string                   `json:"horizon"`
		Report     featureredundancy.Report `json:"report"`
		ComputedAt int64                    `json:"computedAt"`
	}
	var out []published
	now := time.Now().Unix()
	if w.Now != nil {
		now = w.Now().Unix()
	}
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		rows, err := w.St.LabeledFeaturesAll(ctx, h, 0, redundancySampleCap)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			continue
		}
		samples := make([]featureredundancy.Sample, 0, len(rows))
		for _, r := range rows {
			samples = append(samples, featureredundancy.Sample{Vec: r.Vec, Fwd: r.FwdReturn})
		}
		cfg := featureredundancy.Defaults()
		// Only genuine INPUTS are analyzed: canonicalFeatureKeys already
		// excludes the blend's own outputs, so a model's prediction can never be
		// clustered as if it were a data source.
		cfg.Allow = canonicalFeatureKeys(rows)
		out = append(out, published{Horizon: string(h), Report: featureredundancy.Analyze(samples, cfg), ComputedAt: now})
	}
	if len(out) == 0 {
		return "no labeled features yet — nothing to analyze", nil
	}
	blob, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	if err := w.St.SetMeta(ctx, store.MetaFeatureRedundancy, string(blob)); err != nil {
		return "", err
	}
	var summary []string
	for _, p := range out {
		summary = append(summary, fmt.Sprintf("%s: %d fields → %d independent (%.0f%% redundant)",
			p.Horizon, p.Report.FieldCount, p.Report.EffectiveCount, p.Report.RedundancyRatio*100))
	}
	return strings.Join(summary, "; "), nil
}

// ─────────────────────────────────────────────────────────────────────────
// C. Dataset versioning.

// DatasetVersionRunner content-hashes each symbol's daily bars and detects
// rewritten history.
type DatasetVersionRunner struct {
	St  *store.Store
	Now func() time.Time
}

func (w *DatasetVersionRunner) Name() string { return "dataset-version-runner" }

// Interval is 6h for the same reason as the distribution sweep: the pass is
// bounded, so coverage comes from cadence. A full lap lands in about two days,
// which is ample for detecting a provider rewriting history.
func (w *DatasetVersionRunner) Interval() time.Duration { return 6 * time.Hour }

func (w *DatasetVersionRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	var checked, revised, extended, first, processed int
	slice, cursorFor := rotate(ctx, w.St, metaDatasetCursor, syms, datasetSymbolsPerPass)
	deadline := time.Now().Add(sweepBudget)
	for _, s := range slice {
		if time.Now().After(deadline) {
			break // time-boxed; the cursor resumes here next pass
		}
		processed++
		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, 0, now.Unix()+1, 0)
		if err != nil {
			return "", err
		}
		if len(bars) == 0 {
			continue
		}
		rows := make([]datasetver.Row, 0, len(bars))
		for _, b := range bars {
			rows = append(rows, datasetver.Row{
				Ts: b.Ts, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume,
			})
		}
		fresh := datasetver.Hash(s.Symbol, string(md.TF1d), rows)
		checked++

		stored, ok, err := w.St.DatasetVersion(ctx, s.ID, string(md.TF1d))
		if err != nil {
			return "", err
		}
		rec := store.DatasetVersion{
			SymbolID: s.ID, Timeframe: string(md.TF1d), FirstTs: fresh.FirstTs,
			LastTs: fresh.LastTs, N: fresh.N, Hash: fresh.Hash, CheckedAt: now.Unix(),
		}
		if !ok {
			// First observation of a slice can never be a revision.
			first++
			if err := w.St.UpsertDatasetVersion(ctx, rec); err != nil {
				return "", err
			}
			continue
		}
		// Restrict the fresh rows to the stored range to tell an append apart
		// from a rewrite.
		var overlap []datasetver.Row
		for _, r := range rows {
			if r.Ts >= stored.FirstTs && r.Ts <= stored.LastTs {
				overlap = append(overlap, r)
			}
		}
		ov := datasetver.Hash(s.Symbol, string(md.TF1d), overlap)
		rev := datasetver.Compare(
			datasetver.Version{Symbol: s.Symbol, Timeframe: stored.Timeframe, FirstTs: stored.FirstTs,
				LastTs: stored.LastTs, N: stored.N, Hash: stored.Hash},
			fresh, ov.Hash)
		rec.Revisions = stored.Revisions
		switch {
		case rev.Extended:
			extended++
		case rev.Changed:
			revised++
			rec.Revisions++
			sid := s.ID
			// A rewritten range invalidates reproducibility of anything measured
			// on it, so a human has to know.
			if err := w.St.InsertDQ(ctx, md.DQEvent{
				SymbolID: &sid, Ts: now.Unix(), Kind: "dataset_revised",
				Detail: fmt.Sprintf("%s %s: provider rewrote history inside %d..%d (was n=%d, now n=%d) — claims measured on it must be re-graded",
					s.Symbol, md.TF1d, stored.FirstTs, stored.LastTs, stored.N, fresh.N),
			}); err != nil {
				return "", err
			}
		}
		if err := w.St.UpsertDatasetVersion(ctx, rec); err != nil {
			return "", err
		}
	}
	if err := w.St.SetMeta(ctx, metaDatasetCursor, cursorFor(processed)); err != nil {
		return "", err
	}
	return fmt.Sprintf("hashed=%d new=%d extended=%d REVISED=%d (swept %d of %d this pass)",
		checked, first, extended, revised, processed, len(syms)), nil
}

// ─────────────────────────────────────────────────────────────────────────
// D. Canary.

// CanaryRunner grades the newest model version against the previous one and
// records whether it may serve. It NEVER flips serving by itself: the verdict is
// persisted for the surfaces to honor, because automation that can promote its
// own output is the failure this whole layer exists to prevent.
type CanaryRunner struct {
	St  *store.Store
	Now func() time.Time
}

func (w *CanaryRunner) Name() string            { return "canary-runner" }
func (w *CanaryRunner) Interval() time.Duration { return time.Hour }

func (w *CanaryRunner) Run(ctx context.Context) (string, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	var lines []string
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		// The whole record, deliberately: this is a HEAD-TO-HEAD grade of one
		// feature version against another, and survivorship contamination sits in
		// both arms alike, so bounding the window here would only shrink the
		// comparison without making it cleaner. The gate that compares an
		// absolute record against an absolute null — re-admission, in
		// internal/api — is the one that must start at the epoch, and does.
		rows, err := w.St.VersionedOutcomes(ctx, h, 200000, 0)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			continue
		}
		// Group by feature version — a changed vector IS a changed model.
		type agg struct {
			n, correct  int
			first, last int64
			// days tallies the arm per UTC day. The canary interval resamples
			// days, so a row total alone is not enough to grade an arm: ~1,000
			// symbols on one day share one market move, and an interval that
			// counts them as independent trials is ~4x too tight at exactly the
			// moment it decides which model serves.
			days map[int64]*canary.DayTally
			// dayUps counts up-moves per UTC day so the prequential baseline
			// can replay the majority guess in call order.
			dayUps map[int64]int
		}
		byVer := map[int]*agg{}
		for _, r := range rows {
			a := byVer[r.Version]
			if a == nil {
				a = &agg{first: r.Ts, last: r.Ts,
					days: map[int64]*canary.DayTally{}, dayUps: map[int64]int{}}
				byVer[r.Version] = a
			}
			a.n++
			if r.Correct {
				a.correct++
			}
			if r.Ts < a.first {
				a.first = r.Ts
			}
			if r.Ts > a.last {
				a.last = r.Ts
			}
			d := r.Ts / 86400
			t := a.days[d]
			if t == nil {
				t = &canary.DayTally{Day: d}
				a.days[d] = t
			}
			t.N++
			if r.Correct {
				t.Hits++
			}
			if r.Up {
				a.dayUps[d]++
			}
		}
		tallies := func(a *agg) []canary.DayTally {
			out := make([]canary.DayTally, 0, len(a.days))
			for _, t := range a.days {
				out = append(out, *t)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
			return out
		}
		var vers []int
		for v := range byVer {
			vers = append(vers, v)
		}
		sort.Ints(vers)
		if len(vers) < 2 {
			continue // nothing to compare against yet
		}
		chV, incV := vers[len(vers)-1], vers[len(vers)-2]
		ch, inc := byVer[chV], byVer[incV]
		// Majority-class baseline over the CHALLENGER's own observations, so an
		// imbalanced up-rate cannot masquerade as edge — and PREQUENTIAL, so
		// the baseline cannot cheat either: each day's constant guess is the
		// majority class over the days strictly before it. The old
		// max(base, 1-base) over the finished window handed the null hindsight
		// the models never had, retroactively crediting it with any mid-window
		// class flip.
		base := canary.PrequentialBaseline(tallies(ch), ch.dayUps)
		v := canary.Evaluate(
			canary.Record{Version: fmt.Sprintf("v%d", incV), N: inc.n, Correct: inc.correct,
				Days: len(inc.days), DayTallies: tallies(inc),
				FirstTs: inc.first, LastTs: inc.last, BaselineAccuracy: base},
			canary.Record{Version: fmt.Sprintf("v%d", chV), N: ch.n, Correct: ch.correct,
				Days: len(ch.days), DayTallies: tallies(ch),
				FirstTs: ch.first, LastTs: ch.last, BaselineAccuracy: base},
		)
		model := "directional-ensemble-" + string(h)
		if err := w.St.UpsertCanaryTrial(ctx, store.CanaryTrial{
			Model: model, Incumbent: fmt.Sprintf("v%d", incV), Challenger: fmt.Sprintf("v%d", chV),
			Decision: string(v.Decision), Serving: v.Serving, Reason: v.Reason,
			IncN: inc.n, IncAcc: v.IncumbentAccuracy, ChN: ch.n, ChAcc: v.ChallengerAccuracy,
			ChLower: v.ChallengerLower, ChUpper: v.ChallengerUpper, Baseline: base,
			DecidedAt: now.Unix(),
		}); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s: v%d→v%d %s (%.1f%%→%.1f%%, base %.1f%%)",
			h, incV, chV, v.Decision, v.IncumbentAccuracy*100, v.ChallengerAccuracy*100, base*100))
	}
	if len(lines) == 0 {
		return "no version pair with resolved outcomes yet — nothing to canary", nil
	}
	return strings.Join(lines, "; "), nil
}

// The prequential baseline lives in the canary package (canary.
// PrequentialBaseline) so this runner and the model-health re-admission gate
// replay the SAME hindsight-free null — the two decisions must never disagree
// about the same numbers.

// ─────────────────────────────────────────────────────────────────────────
// E. Second-source price validation.

// PriceValidator compares stored daily closes against an INDEPENDENT provider.
//
// Opt-in by design. SIGNALDECK_PRICE_VALIDATION_URL is a template containing
// {symbol}, returning CSV with Date and Close columns (the shape every free
// daily-bar endpoint already speaks). With it unset the worker no-ops and says
// so, because silently checking nothing is worse than not checking.
//
// The second provider's prices are compared and DROPPED. Nothing from them is
// persisted beyond derived statistics — deviation in basis points and counts —
// which keeps this inside the platform's no-redistribution rule while still
// catching the one failure no internal test can see.
type PriceValidator struct {
	St     *store.Store
	Client *http.Client
	Now    func() time.Time
}

func (w *PriceValidator) Name() string            { return "price-validator" }
func (w *PriceValidator) Interval() time.Duration { return 24 * time.Hour }

// priceValidationSymbols caps how many symbols one pass checks, so an opt-in
// diagnostic cannot turn into a sweep against a third party.
const priceValidationSymbols = 25

// validationLookbackDays is the compared span.
const validationLookbackDays = 180

func (w *PriceValidator) Run(ctx context.Context) (string, error) {
	tmpl := strings.TrimSpace(os.Getenv("SIGNALDECK_PRICE_VALIDATION_URL"))
	if tmpl == "" {
		return "second-source validation disabled (set SIGNALDECK_PRICE_VALIDATION_URL to enable)", nil
	}
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	from := now.AddDate(0, 0, -validationLookbackDays).Unix()
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	type entry struct {
		Symbol string            `json:"symbol"`
		Result pricecheck.Result `json:"result"`
	}
	var entries []entry
	var disagreeing, checked, unreachable int
	for _, s := range syms {
		if checked >= priceValidationSymbols {
			break
		}
		if s.Market != md.Stocks {
			continue // the template speaks one market's symbology
		}
		theirs, err := fetchSecondSource(ctx, client, tmpl, s.Symbol)
		if err != nil || len(theirs) == 0 {
			unreachable++
			continue
		}
		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, from, now.Unix()+1, 0)
		if err != nil {
			return "", err
		}
		ours := make([]pricecheck.Point, 0, len(bars))
		for _, b := range bars {
			ours = append(ours, pricecheck.Point{Ts: b.Ts, Close: b.Close})
		}
		res := pricecheck.Compare(ours, theirs, pricecheck.DefaultToleranceBps)
		checked++
		entries = append(entries, entry{Symbol: s.Symbol, Result: res})
		if res.Confident && !res.Agree {
			disagreeing++
			sid := s.ID
			kind := "price_disagreement"
			if res.Systematic {
				// A one-sided offset is an adjustment-convention difference, not
				// corruption. Labeling them the same trains an operator to
				// ignore both.
				kind = "price_convention_diff"
			}
			if err := w.St.InsertDQ(ctx, md.DQEvent{
				SymbolID: &sid, Ts: now.Unix(), Kind: kind,
				Detail: fmt.Sprintf("%s: %d/%d days differ from the second source beyond %.0fbps (max %.0fbps, median %.0fbps, mean signed %.0fbps)",
					s.Symbol, res.DisagreeCount, res.Compared, res.ToleranceBps,
					res.MaxDevBps, res.MedianDevBps, res.MeanSignedBps),
			}); err != nil {
				return "", err
			}
		}
	}
	blob, err := json.Marshal(map[string]any{
		"checkedAt":    now.Unix(),
		"checked":      checked,
		"disagreeing":  disagreeing,
		"unreachable":  unreachable,
		"toleranceBps": pricecheck.DefaultToleranceBps,
		"symbols":      entries,
		"note":         "only derived comparison statistics are stored; the second provider's prices are read, compared and discarded",
	})
	if err != nil {
		return "", err
	}
	if err := w.St.SetMeta(ctx, store.MetaPriceValidation, string(blob)); err != nil {
		return "", err
	}
	return fmt.Sprintf("validated=%d disagreeing=%d unreachable=%d", checked, disagreeing, unreachable), nil
}

// fetchSecondSource reads a CSV of daily bars from the configured template and
// returns its closes. It is deliberately tolerant about column order and
// permissive about extra columns — and strict about needing a real date and a
// positive close, because a half-parsed series would manufacture disagreements.
func fetchSecondSource(ctx context.Context, client *http.Client, tmpl, symbol string) ([]pricecheck.Point, error) {
	url := strings.ReplaceAll(tmpl, "{symbol}", symbol)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "signaldeck-price-validation")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("second source returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseSecondSourceCSV(string(body))
}

// parseSecondSourceCSV extracts (day, close) pairs from a daily-bar CSV.
func parseSecondSourceCSV(body string) ([]pricecheck.Point, error) {
	r := csv.NewReader(strings.NewReader(body))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil || len(recs) < 2 {
		return nil, fmt.Errorf("unparseable second-source CSV")
	}
	dateCol, closeCol := -1, -1
	for i, h := range recs[0] {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "date", "timestamp", "day":
			dateCol = i
		case "close", "adj close", "close/last":
			if closeCol == -1 {
				closeCol = i
			}
		}
	}
	if dateCol < 0 || closeCol < 0 {
		return nil, fmt.Errorf("second-source CSV lacks Date/Close columns")
	}
	var out []pricecheck.Point
	for _, rec := range recs[1:] {
		if dateCol >= len(rec) || closeCol >= len(rec) {
			continue
		}
		t, err := time.Parse("2006-01-02", strings.TrimSpace(rec[dateCol]))
		if err != nil {
			continue
		}
		c, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(rec[closeCol]), "$"), 64)
		if err != nil || c <= 0 {
			continue
		}
		out = append(out, pricecheck.Point{Ts: t.UTC().Unix(), Close: c})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("second-source CSV contained no usable rows")
	}
	return out, nil
}
