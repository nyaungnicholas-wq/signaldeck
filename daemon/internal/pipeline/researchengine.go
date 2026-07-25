// Research discovery engine — the research-engine worker (nightly).
//
// It grades the ledger's machine-readable hypotheses against the historical
// research_weeks evidence base era by era (regime-survival discipline),
// writes each grade as kind=backtest evidence behind its attack battery,
// seeds the extension-conditioned H002-R1 refinement, runs bounded
// auto-discovery over the same observations, and sweeps posterior decay.
//
// DISCIPLINE (matching the live research-ledger worker):
//   - Backtest grades are labeled backtest and always carry the survivorship
//     penalty: the backfilled universe is today's survivor set, never
//     presented as a live forward record.
//   - Cursor: era grades advance oldest-first through history and never
//     re-grade a consumed window (backtest max window_to), and never touch
//     data the live graders have consumed (live min window_from, two-week
//     clearance). No live evidence at all ⇒ no upper bound.
//   - Attacks are inserted BEFORE their grade row, so an interrupted write
//     leaves the cursor unadvanced and any orphan rows biasing conservative.
//   - Honest empty state: zero historical rows ⇒ "no historical weeks yet".
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	researchEngineDayKey    = "research_engine_last_day"
	researchEngineSeedV2Key = "research_ledger_seed_v2"

	// researchEngineDQKind labels step failures; researchSentinelDQKind labels
	// structurally invalid rows the sentinel dropped before grading.
	researchEngineDQKind   = "research_engine_error"
	researchSentinelDQKind = "research_sentinel"
)

// engineObsKeys is the Vec projection the graders + discovery need — the
// engine loads years of rows at once and must not hold anything else.
var engineObsKeys = []string{
	"pressure_score", "pressure_abs", "comp_rsi", "rsi_pct", "rsi14",
	"ext_score", "vol_pct", "vol_anomaly", "price_accel", "consec_dir",
	"vwap_dist_atr", "atr_ext_20", "vix_high_vol", "mkt_trend",
}

// h002R1Rule is the extension-conditioned refinement of H002: take the
// inverse-pressure call only where |pressure| is cross-sectionally high AND
// the extension composite is extreme.
var h002R1Rule = researchx.Rule{
	Conds: []researchx.Cond{
		{Key: "pressure_abs", Op: ">=", Val: 0.5, Pct: true},
		{Key: "ext_score", Op: ">=", Val: 0.7},
	},
	Call: "inverse_pressure",
}

// engineRule binds a ledger hypothesis ID to its machine-gradable rule.
type engineRule struct {
	hypID string
	rule  researchx.Rule
}

// engineRegistry is the static registry of machine-graded hypotheses. H008's
// |comp_rsi| trigger is expressed on the derived comp_rsi_abs key the engine
// adds to every obs at load time (researchx.Cond has no abs operator).
var engineRegistry = []engineRule{
	{hypID: "H002", rule: researchx.Rule{Call: "inverse_pressure"}},
	{hypID: "H008", rule: researchx.Rule{
		Conds: []researchx.Cond{{Key: "comp_rsi_abs", Op: ">=", Val: rsiExtremeMin}},
		Call:  "inverse_pressure",
	}},
	{hypID: "H002-R1", rule: h002R1Rule},
}

// ResearchEngineWorker runs the historical grading + discovery pass once per
// UTC day. Zero limits take the documented defaults.
type ResearchEngineWorker struct {
	St  *store.Store
	Now func() time.Time // injectable clock; nil ⇒ time.Now

	MinWeekObs     int // min cross-sectional obs for a measurable week trial (default 10)
	MinWeeks       int // min total week trials before discovery judges a rule (default 30)
	MinWeeksPerEra int // min week trials before an era grade is written (default 8)
	MaxNewHyps     int // auto-discovered hypotheses admitted per run (default 6)
}

// compile-time worker-contract check (internal/workers.Worker, mirrored so
// the pipeline package doesn't import the runner).
var _ interface {
	Name() string
	Interval() time.Duration
	Run(context.Context) (string, error)
} = (*ResearchEngineWorker)(nil)

// Name implements workers.Worker.
func (w *ResearchEngineWorker) Name() string { return "research-engine" }

// Interval implements workers.Worker.
func (w *ResearchEngineWorker) Interval() time.Duration { return 6 * time.Hour }

func (w *ResearchEngineWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *ResearchEngineWorker) params() (minWeekObs, minWeeks, minWeeksPerEra, maxNewHyps int) {
	minWeekObs, minWeeks, minWeeksPerEra, maxNewHyps =
		w.MinWeekObs, w.MinWeeks, w.MinWeeksPerEra, w.MaxNewHyps
	if minWeekObs <= 0 {
		minWeekObs = 10
	}
	if minWeeks <= 0 {
		minWeeks = 30
	}
	if minWeeksPerEra <= 0 {
		minWeeksPerEra = 8
	}
	if maxNewHyps <= 0 {
		maxNewHyps = 6
	}
	return
}

// Run executes the engine's steps. Each step is independent: a step failure
// is recorded as a research_engine_error dq event and the pass continues —
// the day meta is set on every terminal path EXCEPT the zero-rows empty state,
// which retries on the next heartbeat (see the stats gate).
func (w *ResearchEngineWorker) Run(ctx context.Context) (string, error) {
	now := w.now()
	nowUnix := now.Unix()
	day := now.UTC().Format("2006-01-02")
	if last, _ := w.St.GetMeta(ctx, researchEngineDayKey); last == day {
		// The day gate re-opens when a machine-gradable hypothesis has NEVER
		// been graded on any window (e.g. it was seeded moments after today's
		// grading step ran) — otherwise a same-day seed would sit ungraded a
		// full day for no reason. Hypotheses that already carry any graded
		// window (including auto-discovered ones with their capped in-sample
		// experiment) do not re-open the gate.
		if !w.pendingUngraded(ctx) {
			return "already ran today", nil
		}
	}
	minWeekObs, minWeeks, minWeeksPerEra, maxNewHyps := w.params()

	// [A] stats gate — the honest empty state before any machinery runs.
	stats, err := w.St.ResearchWeeksStats(ctx)
	if err != nil {
		return "", err
	}
	if stats.Rows == 0 {
		// Deliberately does NOT set the day cursor: on a fresh deploy the
		// engine boots before hist-backfill has produced any rows, and burning
		// the daily budget on that race would stall the first real pass a full
		// day. The stats gate is one COUNT query — retrying on the 6h heartbeat
		// (or the next restart) is free.
		return "no historical weeks yet — retrying on the next heartbeat", nil
	}

	// [B] one obs load with key projection + the structural sentinel.
	obs, dropped := w.loadObs(ctx, stats, nowUnix)

	// [C] seed the extension-conditioned refinement (once, crash-idempotent) —
	// BEFORE grading, so a hypothesis seeded by this very pass is graded by it
	// rather than waiting out the day gate.
	seeded := w.seedV2(ctx, nowUnix)

	// [D] historical era grading of the machine-readable hypotheses.
	eraGrades, hypsGraded := w.gradeEras(ctx, obs, nowUnix, minWeekObs, minWeeksPerEra)

	// [E] bounded auto-discovery over the same observations.
	discovered := w.discover(ctx, obs, nowUnix, minWeekObs, minWeeks, minWeeksPerEra, maxNewHyps)

	// [E2] AD-* live replication (#17): spec-carrying auto-discovered
	// hypotheses accrue fresh-window evidence on research_weeks rows strictly
	// beyond their last graded window (researchadlive.go; capped per pass).
	adGrades := w.adReplicate(ctx, obs, nowUnix, minWeekObs, minWeeksPerEra)

	// [F] decay sweep over every hypothesis, prose-only included.
	weakening, stale := w.decaySweep(ctx, nowUnix)

	if err := w.St.SetMeta(ctx, researchEngineDayKey, day); err != nil {
		return "", err
	}
	detail := fmt.Sprintf("graded %d era-grades over %d hyps; discovered %d; AD fresh-window grades %d; decayed/stale: %d/%d",
		eraGrades, hypsGraded, discovered, adGrades, weakening, stale)
	if seeded {
		detail = "seeded H002-R1; " + detail
	}
	if dropped > 0 {
		detail += fmt.Sprintf("; sentinel dropped %d rows", dropped)
	}
	return detail, nil
}

// pendingUngraded reports whether any machine-gradable hypothesis (static
// registry or spec-carrying) has NEVER been graded on any window — the one
// condition that re-opens the day gate. Errors read as "nothing pending":
// the gate must fail closed, never spin the pass on a store hiccup.
func (w *ResearchEngineWorker) pendingUngraded(ctx context.Context) bool {
	hyps, err := w.St.LedgerHypotheses(ctx)
	if err != nil {
		return false
	}
	known := make(map[string]bool, len(hyps))
	for _, h := range hyps {
		known[h.ID] = true
	}
	for _, reg := range engineGradingRegistry(hyps) {
		if !known[reg.hypID] {
			continue // not seeded yet — nothing to grade
		}
		maxTo, err := w.St.LedgerEvidenceMaxWindowKinds(ctx, reg.hypID,
			[]string{rl.KindExperiment, rl.KindReplication, rl.KindBacktest})
		if err != nil {
			return false
		}
		if maxTo == 0 {
			return true
		}
	}
	return false
}

// dq records one engine data-quality event (best effort — a dq write failure
// must not abort the pass it is reporting on).
func (w *ResearchEngineWorker) dq(ctx context.Context, kind string, now int64, detail string) {
	_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: now, Kind: kind, Detail: detail})
}

// loadObs loads the full research_weeks history with the grader key
// projection, derives comp_rsi_abs into every vec, and applies the
// structural sentinel: out-of-order rows, non-monotone per-symbol weeks, and
// |fwd_return|>1.5 rows are dropped and recorded as one research_sentinel dq
// event — a structurally broken row must never become evidence.
func (w *ResearchEngineWorker) loadObs(ctx context.Context, stats store.ResearchWeekStats, now int64) ([]researchx.Obs, int) {
	rows, err := w.St.ResearchWeeks(ctx, 0, stats.MaxTs/histfeat.WeekSecs, engineObsKeys)
	if err != nil {
		w.dq(ctx, researchEngineDQKind, now, "load obs: "+err.Error())
		return nil, 0
	}
	obs := make([]researchx.Obs, 0, len(rows))
	dropped := 0
	lastWeek := int64(math.MinInt64)
	perSym := map[int64]int64{}
	for _, r := range rows {
		last, seen := perSym[r.SymbolID]
		if r.Week < lastWeek || (seen && r.Week <= last) || math.Abs(r.FwdReturn) > 1.5 {
			dropped++
			continue
		}
		lastWeek = r.Week
		perSym[r.SymbolID] = r.Week
		if v, ok := r.Vec["comp_rsi"]; ok {
			r.Vec["comp_rsi_abs"] = math.Abs(v) // H008 grades on the derived key
		}
		obs = append(obs, researchx.Obs{
			SymbolID: r.SymbolID, Week: r.Week, Ts: r.Ts, Vec: r.Vec,
			Up: r.Up, FwdRet: r.FwdReturn, Era: r.Era, HighVol: r.HighVol,
		})
	}
	if dropped > 0 {
		w.dq(ctx, researchSentinelDQKind, now, fmt.Sprintf(
			"dropped %d structurally invalid rows (ordering / per-symbol week monotonicity / |fwd_return|>1.5)", dropped))
	}
	return obs, dropped
}

// gradeEras grades every registered machine-readable hypothesis era by era.
// Returns the number of era grades written and the number of hypotheses that
// received at least one.
func (w *ResearchEngineWorker) gradeEras(ctx context.Context, obs []researchx.Obs, now int64, minWeekObs, minWeeksPerEra int) (eraGrades, hypsGraded int) {
	hyps, err := w.St.LedgerHypotheses(ctx)
	if err != nil {
		w.dq(ctx, researchEngineDQKind, now, "grade: load hypotheses: "+err.Error())
		return 0, 0
	}
	hypByID := make(map[string]rl.Hypothesis, len(hyps))
	for _, h := range hyps {
		hypByID[h.ID] = h
	}
	byEra := map[string][]researchx.Obs{}
	for _, o := range obs {
		byEra[o.Era] = append(byEra[o.Era], o)
	}
	for _, reg := range engineGradingRegistry(hyps) {
		hyp, ok := hypByID[reg.hypID]
		if !ok {
			continue // not seeded yet — graded on a future run
		}
		n, err := w.gradeHypEras(ctx, hyp, reg.rule, byEra, now, minWeekObs, minWeeksPerEra)
		if err != nil {
			w.dq(ctx, researchEngineDQKind, now, fmt.Sprintf("grade %s: %v", reg.hypID, err))
			continue
		}
		if n > 0 {
			eraGrades += n
			hypsGraded++
		}
	}
	return eraGrades, hypsGraded
}

// engineGradingRegistry is the static registry plus every ledger hypothesis
// whose Spec parses into a rule (auto-discovered ones join on future runs),
// deduped by ID with a deterministic order.
func engineGradingRegistry(hyps []rl.Hypothesis) []engineRule {
	out := append([]engineRule(nil), engineRegistry...)
	seen := make(map[string]bool, len(out))
	for _, r := range out {
		seen[r.hypID] = true
	}
	var dyn []engineRule
	for _, h := range hyps {
		if h.Spec == "" || seen[h.ID] {
			continue
		}
		var rule researchx.Rule
		if err := json.Unmarshal([]byte(h.Spec), &rule); err != nil || rule.Call == "" {
			continue // an unintelligible spec grades nothing, never wrong
		}
		dyn = append(dyn, engineRule{hypID: h.ID, rule: rule})
	}
	sort.Slice(dyn, func(i, j int) bool { return dyn[i].hypID < dyn[j].hypID })
	return append(out, dyn...)
}

// gradeHypEras grades one hypothesis over the ungraded eras, oldest first.
// The backtest cursor (max backtest window_to) skips consumed eras; the
// live-evidence guard (min experiment/replication window_from, two-week
// clearance) keeps backtest windows clear of live-graded data — a zero
// live minimum means NO live evidence exists and there is no upper bound.
func (w *ResearchEngineWorker) gradeHypEras(ctx context.Context, hyp rl.Hypothesis, rule researchx.Rule, byEra map[string][]researchx.Obs, now int64, minWeekObs, minWeeksPerEra int) (int, error) {
	liveMin, err := w.St.LedgerEvidenceMinWindowKinds(ctx, hyp.ID,
		[]string{rl.KindExperiment, rl.KindReplication})
	if err != nil {
		return 0, err
	}
	btCursor, err := w.St.LedgerEvidenceMaxWindowKinds(ctx, hyp.ID, []string{rl.KindBacktest})
	if err != nil {
		return 0, err
	}
	grades, highs := 0, 0
	for _, era := range histfeat.EraOrder() {
		eraObs := byEra[era]
		if len(eraObs) == 0 {
			continue
		}
		spanFrom, spanTo := obsSpan(eraObs)
		if spanFrom <= btCursor {
			continue // already consumed by a backtest grade
		}
		if liveMin > 0 && spanTo >= liveMin-2*histfeat.WeekSecs {
			continue // stays clear of live-graded windows
		}
		g := researchx.GradeWeeks(eraObs, rule, minWeekObs)
		if g.Weeks < minWeeksPerEra {
			continue // too thin to acquit OR convict — no row
		}
		evidence := engineGradeAttacks(hyp.ID, rule, eraObs, g, era, now, spanFrom, spanTo,
			minWeekObs, minWeeksPerEra)
		evidence = append(evidence, rl.Evidence{
			HypID: hyp.ID, Ts: now, Kind: rl.KindBacktest,
			K: g.WinWeeks, N: g.Weeks, P0: 0.5,
			BF: rl.BayesFactorAbove(g.WinWeeks, g.Weeks, 0.5, rl.WeekTrialMaxEdge),
			Note: fmt.Sprintf("era=%s: %d/%d winning weeks (%d obs) — historical backfill, survivor universe",
				era, g.WinWeeks, g.Weeks, g.TotalObs),
			WindowFrom: spanFrom, WindowTo: spanTo,
		})
		for _, e := range evidence {
			if err := w.St.InsertLedgerEvidence(ctx, e); err != nil {
				return grades, err
			}
		}
		grades++
		highs += g.HighVolObs
	}
	if grades == 0 {
		return 0, nil // honest nothing-to-update: the posterior stands
	}
	regimes := hyp.Regimes
	if highs >= minHighVolObs && regimes < 2 {
		regimes = 2
	}
	return grades, w.recomputeAndDecay(ctx, hyp, regimes, now)
}

// engineGradeAttacks runs the backtest attack battery over one graded window
// (an era grade, or discovery's full in-sample window when era=="all").
// Failed attacks carry their documented penalties; passed ones are recorded
// at BF=1. The survivorship penalty is ALWAYS levied on a backfilled grade —
// it is a static-state attack and rl.EffectiveChain dedupes it to the latest
// row.
func engineGradeAttacks(hypID string, rule researchx.Rule, obs []researchx.Obs, g researchx.WeekGrade, era string, now, winFrom, winTo int64, minWeekObs, minWeeks int) []rl.Evidence {
	var out []rl.Evidence
	add := func(bf float64, note string) {
		out = append(out, rl.Evidence{
			HypID: hypID, Ts: now, Kind: rl.KindAttack, BF: bf, Note: note,
			WindowFrom: winFrom, WindowTo: winTo,
		})
	}

	// [1] time-split within the graded window (same epsilon discipline as the
	// live grader: a near-flat half is ambiguous, not a flip).
	const eps = 0.005
	s1, s2 := weekTrialSplitRates(g.Trials)
	if (s1 > eps && s2 < -eps) || (s1 < -eps && s2 > eps) {
		add(rl.PenaltyTimeSplitFail, fmt.Sprintf(
			"time-split: era=%s week-trial edge FLIPS sign between halves (%+.3f / %+.3f) — lucky-period risk", era, s1, s2))
	} else {
		add(1, fmt.Sprintf(
			"time-split: era=%s week-trial edge sign stable-or-flat (%+.3f / %+.3f)", era, s1, s2))
	}

	// [2] suspicious-edge: beyond the plausible week-trial band is more
	// likely leakage or a grading bug than skill.
	edge := float64(g.WinWeeks)/float64(g.Weeks) - 0.5
	if edge > rl.WeekTrialMaxEdge {
		add(rl.PenaltySuspiciousEdge, fmt.Sprintf(
			"suspicious-edge: era=%s week-trial edge %+.3f exceeds the plausible band %.2f — probable leakage/bug", era, edge, rl.WeekTrialMaxEdge))
	} else {
		add(1, fmt.Sprintf(
			"suspicious-edge: era=%s week-trial edge %+.3f within plausible band %.2f", era, edge, rl.WeekTrialMaxEdge))
	}

	// [3] survivorship — always levied on backfilled data.
	add(rl.PenaltySurvivorship,
		"survivorship: backfilled universe is today's survivor set — delisted losers are absent, so historical grades lean optimistic")

	// [4]+[5] multi-cond rules only: do the conditions add value, and does
	// the edge survive threshold perturbation?
	if len(rule.Conds) > 1 {
		cf := researchx.Counterfactual(obs, rule, minWeekObs, minWeeks)
		best := math.Max(cf.Base.WinRate, cf.NullMatched.WinRate)
		for _, a := range cf.Ablations {
			best = math.Max(best, a.WinRate)
		}
		note := fmt.Sprintf(
			"counterfactual: era=%s full %.3f vs best competing arm %.3f (base %.3f, null %.3f; margin %+.3f)",
			era, cf.Full.WinRate, best, cf.Base.WinRate, cf.NullMatched.WinRate, cf.Margin)
		if cf.AddsValue {
			add(1, note)
		} else {
			add(rl.PenaltyNoIncrementalValue, note+" — conditions carry no incremental value")
		}
		worst, fragile := researchx.FragileThreshold(obs, rule, minWeekObs)
		if fragile {
			add(rl.PenaltyFragileThreshold, fmt.Sprintf(
				"fragile-threshold: era=%s ±10%% threshold perturbation retains %.2f of the edge — knife-edge fit", era, worst))
		} else {
			add(1, fmt.Sprintf(
				"fragile-threshold: era=%s ±10%% threshold perturbation retains %.2f of the edge", era, worst))
		}
	}
	return out
}

// weekTrialSplitRates returns each half's winning-week rate minus 0.5, in
// week order — the time-split attack's two signals.
func weekTrialSplitRates(trials []researchx.WeekTrial) (s1, s2 float64) {
	rate := func(part []researchx.WeekTrial) float64 {
		if len(part) == 0 {
			return 0
		}
		wins := 0
		for _, t := range part {
			if t.Win {
				wins++
			}
		}
		return float64(wins)/float64(len(part)) - 0.5
	}
	half := len(trials) / 2
	return rate(trials[:half]), rate(trials[half:])
}

// seedV2 registers the H002-R1 hypothesis exactly once (meta-guarded,
// crash-idempotent: the upsert is a no-op re-run and the meta is set last).
// No evidence is seeded — the engine's own grader earns the first numbers.
func (w *ResearchEngineWorker) seedV2(ctx context.Context, now int64) bool {
	if v, _ := w.St.GetMeta(ctx, researchEngineSeedV2Key); v != "" {
		return false
	}
	spec, err := json.Marshal(h002R1Rule)
	if err != nil {
		w.dq(ctx, researchEngineDQKind, now, "seed v2: "+err.Error())
		return false
	}
	h := rl.Hypothesis{
		ID: "H002-R1", Family: "meanrev", Horizon: "1w",
		Statement: "Pressure×Extension: the weekly inverse edge concentrates where |pressure| is cross-sectionally high AND extension is extreme, and adds incremental value over pressure alone",
		Prior:     0.20, MaxEdge: 0.20,
		OpenQuestions: []string{
			"Does the conditioned edge beat unconditioned inverse-pressure after costs?",
			"Is the driver extension level or extension CHANGE?",
		},
		Spec: string(spec),
	}
	h.Posterior = h.Prior
	h.Status = rl.Status(h.Prior, 0, 1)
	h.Regimes = 1
	if err := w.St.UpsertLedgerHypothesis(ctx, h, now); err != nil {
		w.dq(ctx, researchEngineDQKind, now, "seed v2: "+err.Error())
		return false
	}
	if err := w.St.SetMeta(ctx, researchEngineSeedV2Key, fmt.Sprintf("%d", now)); err != nil {
		w.dq(ctx, researchEngineDQKind, now, "seed v2 meta: "+err.Error())
		return false
	}
	return true
}

// discover runs bounded auto-discovery and registers surviving candidates as
// new ledger hypotheses, capped at maxNewHyps per run. Already-registered
// candidate IDs are skipped, so re-runs are idempotent.
func (w *ResearchEngineWorker) discover(ctx context.Context, obs []researchx.Obs, now int64, minWeekObs, minWeeks, minWeeksPerEra, maxNewHyps int) int {
	cands := researchx.Discover(obs, researchx.DiscoverConfig{
		MinWeekObs: minWeekObs, MinWeeks: minWeeks, MinWeeksPerEra: minWeeksPerEra,
	})
	if len(cands) == 0 {
		return 0
	}
	hyps, err := w.St.LedgerHypotheses(ctx)
	if err != nil {
		w.dq(ctx, researchEngineDQKind, now, "discover: load hypotheses: "+err.Error())
		return 0
	}
	existing := make(map[string]bool, len(hyps))
	for _, h := range hyps {
		existing[h.ID] = true
	}
	winFrom, winTo := obsSpan(obs)
	inserted := 0
	for _, c := range cands {
		if existing[c.ID] {
			continue
		}
		if inserted >= maxNewHyps {
			break
		}
		if err := w.insertCandidate(ctx, c, obs, now, winFrom, winTo, minWeekObs, minWeeks); err != nil {
			w.dq(ctx, researchEngineDQKind, now, fmt.Sprintf("discover %s: %v", c.ID, err))
			continue
		}
		inserted++
	}
	return inserted
}

// insertCandidate registers one discovery survivor: the hypothesis (family
// auto, prior 0.15, Spec = its rule JSON), the attack battery, then ONE
// capped in-sample experiment row — the discovery window is the very data
// the rule was found on, so the grade never carries full evidence weight.
func (w *ResearchEngineWorker) insertCandidate(ctx context.Context, c researchx.Candidate, obs []researchx.Obs, now, winFrom, winTo int64, minWeekObs, minWeeks int) error {
	spec, err := json.Marshal(c.Rule)
	if err != nil {
		return err
	}
	h := rl.Hypothesis{
		ID: c.ID, Family: "auto", Horizon: "1w", Statement: c.Desc,
		Prior: 0.15, MaxEdge: 0.20, Spec: string(spec),
	}
	h.Posterior = h.Prior
	h.Status = rl.Status(h.Prior, 0, 1)
	h.Regimes = 1
	if err := w.St.UpsertLedgerHypothesis(ctx, h, now); err != nil {
		return err
	}
	evidence := engineGradeAttacks(c.ID, c.Rule, obs, c.Grade, "all", now, winFrom, winTo,
		minWeekObs, minWeeks)
	bf := math.Min(
		rl.BayesFactorAbove(c.Grade.WinWeeks, c.Grade.Weeks, 0.5, rl.WeekTrialMaxEdge),
		rl.DiscoveryMaxBF)
	evidence = append(evidence, rl.Evidence{
		HypID: c.ID, Ts: now, Kind: rl.KindExperiment,
		K: c.Grade.WinWeeks, N: c.Grade.Weeks, P0: 0.5, BF: bf,
		Note: fmt.Sprintf("era=all: %d/%d winning weeks (%d obs) — auto-discovered on this very window — in-sample; disjoint-era/live replications are the real test; BF capped",
			c.Grade.WinWeeks, c.Grade.Weeks, c.Grade.TotalObs),
		WindowFrom: winFrom, WindowTo: winTo,
	})
	for _, e := range evidence {
		if err := w.St.InsertLedgerEvidence(ctx, e); err != nil {
			return err
		}
	}
	regimes := 1
	if c.Grade.HighVolObs >= minHighVolObs {
		regimes = 2
	}
	return w.recomputeAndDecay(ctx, h, regimes, now)
}

// decaySweep replays every hypothesis's chain through researchx.Decay and
// writes the peak/last-grade fields back. Returns how many beliefs are
// weakening off their peak and how many have gone stale.
func (w *ResearchEngineWorker) decaySweep(ctx context.Context, now int64) (weakening, stale int) {
	hyps, err := w.St.LedgerHypotheses(ctx)
	if err != nil {
		w.dq(ctx, researchEngineDQKind, now, "decay: load hypotheses: "+err.Error())
		return 0, 0
	}
	evidence, err := w.St.LedgerEvidence(ctx, "")
	if err != nil {
		w.dq(ctx, researchEngineDQKind, now, "decay: load evidence: "+err.Error())
		return 0, 0
	}
	byHyp := map[string][]rl.Evidence{}
	for _, e := range evidence {
		byHyp[e.HypID] = append(byHyp[e.HypID], e)
	}
	for _, h := range hyps {
		rep := researchx.Decay(h.Prior, byHyp[h.ID], now)
		if err := w.St.UpdateLedgerDecay(ctx, h.ID, rep.Peak, rep.PeakTs, rep.LastGradeTs); err != nil {
			w.dq(ctx, researchEngineDQKind, now, fmt.Sprintf("decay %s: %v", h.ID, err))
			continue
		}
		if rep.EdgeWeakening {
			weakening++
		}
		if rep.Stale {
			stale++
		}
	}
	return weakening, stale
}

// recomputeAndDecay reloads one hypothesis's chain, writes the derived
// posterior/status/counters, and refreshes its decay fields.
func (w *ResearchEngineWorker) recomputeAndDecay(ctx context.Context, hyp rl.Hypothesis, regimes int, now int64) error {
	chain, err := w.St.LedgerEvidence(ctx, hyp.ID)
	if err != nil {
		return err
	}
	post := rl.Posterior(hyp.Prior, rl.EffectiveChain(chain))
	reps, contras := rl.Counters(chain)
	// StatusWithGates, not Status: promotion also requires the hypothesis to
	// have been stated as a position and that position graded net of costs.
	status := rl.StatusWithGates(post, rl.Gates{
		Replications: reps, Regimes: regimes,
		TradableForm: hyp.TradableForm, EconomicTest: hyp.EconomicTest,
	})
	if err := w.St.UpdateLedgerDerived(ctx, hyp.ID, post, status, reps, contras, regimes, now); err != nil {
		return err
	}
	d := researchx.Decay(hyp.Prior, chain, now)
	return w.St.UpdateLedgerDecay(ctx, hyp.ID, d.Peak, d.PeakTs, d.LastGradeTs)
}

// obsSpan returns the min/max anchor ts over obs (obs must be non-empty).
func obsSpan(obs []researchx.Obs) (from, to int64) {
	from, to = obs[0].Ts, obs[0].Ts
	for _, o := range obs[1:] {
		if o.Ts < from {
			from = o.Ts
		}
		if o.Ts > to {
			to = o.Ts
		}
	}
	return from, to
}
