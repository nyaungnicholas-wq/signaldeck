// Fleet health API: GET /api/fleet-health — the ONE call that answers "is this
// platform healthy", assembled from the trading book, the model grades, the
// worker fleet, and the architectural layer coverage.
//
// This is the read side of internal/fleetmon. Everything it reports was already
// computed somewhere in the daemon; what it adds is assembly and a derived
// verdict, plus two properties that only exist because they are assembled:
//
//   - A model auto-retired for drift and a book still reporting a cheerful
//     Sharpe can now be seen in the same payload, which is where that
//     contradiction becomes visible at all.
//   - Layer coverage reports which parts of the intended architecture are
//     actually CONSULTED, so a complete-but-dormant subsystem reads as a breach
//     rather than as a set of quietly absent metrics.
//
// HONESTY: every statistic is nil when the sample cannot support it and is named
// in `withheld` with the reason. An empty fleet reports status "unknown", never
// "healthy" — a monitoring surface that renders no-data as green is the failure
// mode this whole file is designed against.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/fleetmon"
	"github.com/nyaungnicholas-wq/signaldeck/internal/health"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
)

const fleetHealthNote = "One assembled read of platform health: the simulated book's realized performance, each model's out-of-sample grade, the worker fleet, and which architectural layers are actually consulted. Every metric the sample cannot support is null and named in `withheld` — never shown as zero. Trading figures are from the INTERNAL SIMULATED book (no broker, no real money)."

// fleetHealthStrategies are the paper books surfaced. Same ids the paper-trading
// worker drives, so the monitor and the book cannot disagree about what exists.
var fleetHealthStrategies = []string{"flagship-1d", "flagship-1w"}

// fleetHealthModels are the model-health meta keys read. Mirrors the set
// internal/api/modelhealth.go publishes so one list governs "which models".
var fleetHealthModels = []string{"ensemble-1d", "ensemble-1w"}

// staleWorkerFactor is how many times its own cadence a worker may exceed before
// it counts as stale. Matches internal/health's watchdog (3x its interval) so the
// two surfaces agree about the word "stale".
const staleWorkerFactor = 3

// minRunsForCadence is how many recorded runs a worker needs before its cadence
// can be inferred. A worker seen once has no observable period, and guessing one
// would manufacture a staleness verdict out of a single data point.
const minRunsForCadence = 3

func (d Deps) fleetHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	trading := make([]fleetmon.Trading, 0, len(fleetHealthStrategies))
	for _, strat := range fleetHealthStrategies {
		t, err := d.tradingHealth(ctx, strat)
		if err != nil {
			httpInternal(w, err)
			return
		}
		trading = append(trading, t)
	}

	models := make([]fleetmon.Model, 0, len(fleetHealthModels))
	for _, name := range fleetHealthModels {
		if m, ok := d.modelHealthFor(ctx, name); ok {
			models = append(models, m)
		}
	}

	sys, err := d.systemHealth(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}

	snap := fleetmon.Assemble(trading, models, sys, d.layerCoverage(ctx), fleetmon.DefaultThresholds())
	writeJSON(w, map[string]any{
		"note":        fleetHealthNote,
		"generatedTs": time.Now().Unix(),
		"snapshot":    snap,
	})
}

// tradingHealth measures one paper book: the equity curve gives return, Sharpe,
// Sortino and both drawdowns; the fill log gives the realized round trips that
// profit factor and hit rate need.
//
// Every statistic is left nil unless its own gate passed. The round-trip payoff in
// particular is withheld until both tails exist — a profit factor computed with no
// losing trade is infinite, not excellent.
func (d Deps) tradingHealth(ctx context.Context, strategy string) (fleetmon.Trading, error) {
	t := fleetmon.Trading{Strategy: strategy}

	rows, err := d.St.PaperEquityCurve(ctx, strategy, 5000)
	if err != nil {
		return t, err
	}
	curve := make([]papertrade.EquityPoint, 0, len(rows))
	for _, row := range rows {
		curve = append(curve, papertrade.EquityPoint{
			Ts: row.Ts, Cash: row.Cash, PositionsValue: row.PositionsValue, Equity: row.Equity,
		})
	}
	t.EquityMarks = len(curve)

	trades, err := d.St.AllPaperTradesAsc(ctx, strategy)
	if err != nil {
		return t, err
	}
	fills := make([]papertrade.FillRecord, 0, len(trades))
	var tradedNotional float64
	for _, tr := range trades {
		fills = append(fills, papertrade.FillRecord{
			SymbolID: tr.SymbolID, Side: tr.Side, Qty: tr.Qty, Px: tr.Px, Cost: tr.Cost, Ts: tr.Ts,
		})
		tradedNotional += tr.Qty * tr.Px
	}
	matched := papertrade.MatchRoundTrips(fills)
	payoff := papertrade.Payoffs(matched.Closed)
	t.ClosedTrades = payoff.N

	// Summarize owns the gated return/Sharpe/turnover; only lift the fields whose
	// own validity flags passed.
	closed := make([]papertrade.Trade, 0, len(matched.Closed))
	for _, rt := range matched.Closed {
		closed = append(closed, papertrade.Trade{Won: rt.Won, Notional: rt.Qty * rt.ExitPx})
	}
	sum := papertrade.Summarize(curve, closed, len(trades), tradedNotional)
	if len(curve) > 0 {
		t.TotalReturn = fleetmon.Ptr(sum.TotalReturn)
		t.MaxDrawdown = fleetmon.Ptr(sum.MaxDrawdown)
		t.Turnover = fleetmon.Ptr(sum.Turnover)
	}
	if sum.SharpeValid {
		t.Sharpe = fleetmon.Ptr(sum.Sharpe)
	}
	if v, ok := papertrade.Sortino(curve); ok {
		t.Sortino = fleetmon.Ptr(v)
	}
	if v, ok := papertrade.CurrentDrawdown(curve); ok {
		t.CurrentDrawdown = fleetmon.Ptr(v)
	}
	// Hit rate and profit factor come from the ROUND TRIPS, both gated on the
	// payoff shape being measurable at all.
	if payoff.Valid {
		t.HitRate = fleetmon.Ptr(payoff.WinRate)
		t.ProfitFactor = fleetmon.Ptr(payoff.ProfitFactor)
	}
	return t, nil
}

// modelHealthFor reads one model's stored grade. ok is false when the grader has
// not run for it — an ungraded model is omitted rather than shown as healthy.
func (d Deps) modelHealthFor(ctx context.Context, name string) (fleetmon.Model, bool) {
	raw, err := d.St.GetMeta(ctx, pipeline.MetaKeyPrefix+name)
	if err != nil || raw == "" {
		return fleetmon.Model{}, false
	}
	var stored struct {
		Model        string             `json:"model"`
		Verdict      string             `json:"verdict"`
		Emitting     bool               `json:"emitting"`
		Overall      float64            `json:"overall"`
		Components   map[string]float64 `json:"components"`
		Observations int                `json:"observations"`
		Accuracy     float64            `json:"accuracy"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return fleetmon.Model{}, false
	}
	m := fleetmon.Model{
		Name:         name,
		Verdict:      stored.Verdict,
		Overall:      fleetmon.Ptr(stored.Overall),
		Observations: stored.Observations,
		Emitting:     stored.Emitting,
	}
	if stored.Observations > 0 {
		m.Accuracy = fleetmon.Ptr(stored.Accuracy)
	}
	// The grader stores its component scores rather than the raw calibration and
	// drift measurements, so only lift what is actually present. A component that
	// was never written stays nil instead of arriving as a confident zero.
	if v, ok := stored.Components["calibration"]; ok {
		m.CalibrationErr = fleetmon.Ptr(1 - v) // component is a score; the error is its complement
	}
	if v, ok := stored.Components["drift"]; ok {
		m.FeatureDrift = fleetmon.Ptr(1 - v)
	}
	if v, ok := stored.Components["skill"]; ok {
		m.BrierSkill = fleetmon.Ptr(v)
	}
	return m, true
}

// systemHealth measures the operational layer: which workers are behind their own
// cadence, and how old the freshest market data is.
//
// WHY CADENCE IS INFERRED RATHER THAN READ. internal/health judges staleness
// against each worker's DECLARED interval, which it gets from the worker registry
// — and the registry lives in the daemon's wiring, not in the store this handler
// reads. Rather than duplicate the registry here (two lists, one of them
// eventually wrong) or invent a single fixed deadline for workers whose real
// periods span a minute to a day, each worker's cadence is measured from the
// MEDIAN gap between its own recorded runs. That is an observation of what the
// worker actually does, and it degrades honestly: a worker without enough history
// to establish a period is SKIPPED, not declared healthy and not declared stale.
func (d Deps) systemHealth(ctx context.Context) (fleetmon.System, error) {
	var sys fleetmon.System

	runs, err := d.St.RecentWorkerRuns(ctx, 2000)
	if err != nil {
		return sys, err
	}
	// RecentWorkerRuns returns newest-first. Group start times per worker,
	// preserving that order.
	starts := map[string][]int64{}
	// ...and the STATUSES in the same order, because a start time alone cannot
	// tell a working worker from a broken one. This handler judged staleness on
	// ts[0] — the newest run's START, whatever it did — so a worker that ran
	// exactly on cadence and errored every single time was healthy by
	// construction. Measured 2026-08-11: forecast-monitor, 15 of 15 runs
	// status='error', never once successful, reported healthy here and absent
	// from data/health.json at the same time.
	statuses := map[string][]string{}
	for _, run := range runs {
		starts[run.Worker] = append(starts[run.Worker], run.StartedAt)
		statuses[run.Worker] = append(statuses[run.Worker], run.Status)
	}
	sys.TotalWorkers = len(starts)
	sys.FailingWorkers = health.FailingWorkers(statuses)

	now := time.Now().Unix()
	for name, ts := range starts {
		cadence, ok := medianGap(ts)
		if !ok {
			continue // too little history to establish a period — not judged
		}
		if now-ts[0] > staleWorkerFactor*cadence {
			sys.StaleWorkers = append(sys.StaleWorkers, name)
		}
	}
	sort.Strings(sys.StaleWorkers)

	if age, ok, err := d.freshestDataAge(ctx, now); err != nil {
		return sys, err
	} else if ok {
		sys.DataAgeSeconds = fleetmon.Ptr(age)
	}
	return sys, nil
}

// medianGap is the median spacing between consecutive run starts, in seconds.
// Median rather than mean so one long outage or a burst of retries does not
// redefine the worker's normal period. ok is false below minRunsForCadence.
func medianGap(newestFirst []int64) (int64, bool) {
	if len(newestFirst) < minRunsForCadence {
		return 0, false
	}
	gaps := make([]int64, 0, len(newestFirst)-1)
	for i := 1; i < len(newestFirst); i++ {
		if g := newestFirst[i-1] - newestFirst[i]; g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return 0, false
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	med := gaps[len(gaps)/2]
	if med <= 0 {
		return 0, false
	}
	return med, true
}

// freshestDataAge is how old the newest daily bar in the tracked universe is. ok
// is false when nothing is tracked or no bar exists — "we have no data" is not the
// same as "our data is current", and the caller must not collapse them.
func (d Deps) freshestDataAge(ctx context.Context, now int64) (float64, bool, error) {
	syms, err := d.St.ListSymbols(ctx, true)
	if err != nil {
		return 0, false, err
	}
	var newest int64
	for _, s := range syms {
		ts, err := d.St.LatestBarTs(ctx, s.ID, md.TF1d)
		if err != nil {
			return 0, false, err
		}
		if ts > newest {
			newest = ts
		}
	}
	if newest <= 0 {
		return 0, false, nil
	}
	age := float64(now - newest)
	if age < 0 {
		age = 0
	}
	return age, true, nil
}

// layerCoverage declares which architectural layers exist and which are actually
// consulted.
//
// Live is not a static claim: each entry below is accompanied by the worker or
// route that proves it, and the two entries whose liveness is DATA-dependent —
// feature retirement and the research promotion gate — are probed against the
// store rather than asserted, because "the code exists" and "it has ever produced
// a verdict" are different facts and only the second one means anything.
func (d Deps) layerCoverage(ctx context.Context) []fleetmon.Layer {
	// Feature retirement is live only once the grader has actually published a
	// report the trainer can consult.
	featureGraded := false
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		if _, ok, _ := pipeline.FeatureHealthFor(ctx, d.St, h); ok {
			featureGraded = true
			break
		}
	}
	// The promotion gate is live once it has recorded a trial.
	canaryRun := false
	if trials, err := d.St.CanaryTrials(ctx); err == nil && len(trials) > 0 {
		canaryRun = true
	}

	return []fleetmon.Layer{
		{
			Name: "data platform", Implemented: true, Live: true,
			Detail:   "versioned datasets where overwrite would corrupt something load-bearing: hash-chained prediction ledger, content-hashed OHLCV rewrite detection",
			Evidence: "internal/datasetver + prediction_ledger; workers: dataset-version, ledger-anchor",
		},
		{
			Name: "feature factory", Implemented: true, Live: featureGraded,
			Enforcing: fleetmon.Bool(featureGraded),
			Detail:    "per-feature IC/decay/stability grading with automatic retirement from the training vector",
			Evidence:  "internal/featurehealth; worker: feature-health; enforced in pipeline.modelFeatureKeysExcluding",
		},
		{
			Name: "research engine", Implemented: true, Live: true,
			Enforcing: fleetmon.Bool(true),
			Detail:    "hypothesis-first promotion: purged/embargoed walk-forward, Bonferroni-corrected significance, shadow period, hash-chained pre-registration",
			Evidence:  "internal/researchlab + internal/prereg; worker: research-lab",
		},
		{
			Name: "model zoo", Implemented: false, Live: false,
			Detail:   "DELIBERATELY NARROW: two from-scratch learners (logistic, GBM) plus per-target statistical predictors. A wide zoo would multiply the multiple-testing burden on the same finite data, raising the significance bar for the legs that already work",
			Evidence: "internal/forecast + internal/gbm",
		},
		{
			Name: "meta learner", Implemented: true, Live: true,
			Enforcing: fleetmon.Bool(true),
			Detail:    "regime-conditional blend weights derived from each leg's measured per-cell edge, gated on day-count floors and Bonferroni-corrected",
			Evidence:  "internal/adaptive; consulted by pipeline.PredictionRunner",
		},
		{
			Name: "regime detection", Implemented: true, Live: true,
			Detail:   "trend/range/squeeze classification plus validated volatility- and structural-regime forecasters with quarter-clustered out-of-sample accuracy",
			Evidence: "internal/regime + internal/volregime + internal/structregime",
		},
		{
			Name: "confidence engine", Implemented: true, Live: true,
			Detail:   "one gated object per prediction: probability, expected return, expected drawdown from measured adverse excursion, confidence and Wilson uncertainty, each null when unmeasurable",
			Evidence: "internal/confidence; route: GET /api/confidence",
		},
		{
			Name: "risk engine", Implemented: true, Live: true,
			Enforcing: fleetmon.Bool(true),
			Detail:    "pretrade gate that refuses or shrinks entries: drawdown breaker, daily-loss limit, position and sector caps, fractional-Kelly sizing off realized round trips, minimum ticket. Exits are never gated",
			Evidence:  "internal/riskgate; enforced in pipeline.PaperTrader.buildStep",
		},
		{
			Name: "continuous learning", Implemented: true, Live: canaryRun,
			Enforcing: fleetmon.Bool(canaryRun),
			Detail:    "append-only hash-chained prediction record with controlled promotion: a challenger must beat the incumbent's Wilson lower bound over a minimum day count before it serves",
			Evidence:  "internal/canary + prediction_ledger; worker: canary",
		},
		{
			Name: "institutional monitoring", Implemented: true, Live: true,
			Detail:   "this surface: trading, model, system and architectural coverage in one gated read, with auto-disable made visible",
			Evidence: "internal/fleetmon; route: GET /api/fleet-health",
		},
	}
}

// registerFleetHealth wires the assembled fleet-health read.
func (d Deps) registerFleetHealth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleet-health", d.fleetHealth)
}

