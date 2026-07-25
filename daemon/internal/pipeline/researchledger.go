// Bayesian Research Ledger — research-ledger worker (nightly).
//
// The ledger layer of the research engine:
//
//	Pressure chapter (closed)  ─▶ seeded once with its measured evidence
//	open discoveries (H002/H008) ─▶ graded LIVE by registered graders on fresh,
//	  DISJOINT data windows ─▶ each grade + its attack battery becomes evidence
//	  ─▶ Bayesian posterior update ─▶ status band ─▶ meta-analysis per family.
//
// DISCIPLINE:
//   - Replications grade ONLY data that arrived after the previous evidence
//     window (cursor via LedgerEvidenceMaxWindow, plus a one-horizon gap so
//     forward windows never straddle the boundary) — sequential updates are on
//     disjoint samples, never re-grades of the same rows.
//   - Every grade runs the attack battery (time-split, regime coverage,
//     suspicious-edge); failed attacks enter the SAME evidence chain as BF<1
//     rows. Passed attacks are recorded at BF=1 so lethality stats count runs.
//   - Priors are fixed at seed time and never edited. Nothing here mutates
//     live predictions — the ledger is an audit surface.
//
// Honest empty state: with no new resolved data a grader writes nothing and
// the posterior simply stands. That is the correct output, not a failure.
package pipeline

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ResearchLedgerWorker maintains the Bayesian research ledger.
type ResearchLedgerWorker struct {
	St  *store.Store
	Now func() time.Time // injectable clock; nil ⇒ time.Now

	MinNewObs  int  // non-overlapping obs required before a new grade
	OncePerDay bool // gate real work to once per UTC day
}

// NewResearchLedgerWorker builds the worker with disciplined defaults.
func NewResearchLedgerWorker(st *store.Store) *ResearchLedgerWorker {
	return &ResearchLedgerWorker{St: st, MinNewObs: 150, OncePerDay: true}
}

func (w *ResearchLedgerWorker) Name() string            { return "research-ledger" }
func (w *ResearchLedgerWorker) Interval() time.Duration { return 6 * time.Hour }

func (w *ResearchLedgerWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

const (
	researchLedgerDayKey  = "research_ledger_last_day"
	researchLedgerSeedKey = "research_ledger_seed_v1"

	// weekSecs is the non-overlapping window for 1w grades: one observation per
	// symbol per calendar week, so consecutive forward windows never share bars.
	weekSecs = 7 * 86400

	// ledgerMaxRows caps one grader pass's row load.
	ledgerMaxRows = 100000

	// minNewWeeks is the least number of qualifying calendar-week trials a
	// grade needs. The week is the independence unit (see replicate), so this
	// — not raw obs count — is the real sample-size gate.
	minNewWeeks = 8

	// minWeekObs is the least cross-sectional obs for a week to count as a
	// measurable trial.
	minWeekObs = 10

	// minHighVolObs is the least high-vol obs a window needs before it counts
	// as stressed-regime coverage (house n>=30 convention).
	minHighVolObs = 30

	// rsiExtremeMin is H008's trigger: |comp_rsi| at or beyond this counts as
	// overextended. NOTE the scale: the stored comp_rsi is the WEIGHT-SCALED
	// contribution (norm × 0.15), not the raw norm — measured 1w distribution
	// (2026-07-16, n≈9.7k): p50 0.022, p75 0.043, max 0.235. 0.02 ≈ the median
	// = the Q3/Q4 concentration zone from the decomposition study. Fixed (not
	// quantile-relative) so replications grade the same rule forever; the
	// threshold was calibrated on the same history the FIRST experiment grades
	// — only the later fresh-window replications are the real test.
	rsiExtremeMin = 0.02
)

func (w *ResearchLedgerWorker) Run(ctx context.Context) (string, error) {
	now := w.now()
	day := now.UTC().Format("2006-01-02")
	if w.OncePerDay {
		if last, _ := w.St.GetMeta(ctx, researchLedgerDayKey); last == day {
			return "already ran today", nil
		}
	}

	seeded, err := w.seedOnce(ctx, now.Unix())
	if err != nil {
		return "", fmt.Errorf("seed: %w", err)
	}
	seeded2, err := w.seedWave2(ctx, now.Unix())
	if err != nil {
		return "", fmt.Errorf("seed wave2: %w", err)
	}
	seeded3, err := w.seedWave3(ctx, now.Unix())
	if err != nil {
		return "", fmt.Errorf("seed wave3: %w", err)
	}
	seeded = seeded || seeded2 || seeded3

	graded := 0
	for _, g := range ledgerGraders {
		did, err := w.replicate(ctx, g, now.Unix())
		if err != nil {
			return "", fmt.Errorf("grade %s: %w", g.hypID, err)
		}
		if did {
			graded++
		}
	}

	if err := w.St.SetMeta(ctx, researchLedgerDayKey, day); err != nil {
		return "", err
	}

	// Meta-analysis summary — the engine learning about research itself.
	hyps, err := w.St.LedgerHypotheses(ctx)
	if err != nil {
		return "", err
	}
	meta := rl.Meta(hyps)
	top := "none"
	if len(meta) > 0 {
		top = fmt.Sprintf("%s (avg posterior %.2f over %d)", meta[0].Family, meta[0].AvgPosterior, meta[0].N)
	}
	msg := fmt.Sprintf("%d hypotheses, %d new grades, strongest family: %s", len(hyps), graded, top)
	if seeded {
		msg = "seeded Pressure chapter + open program; " + msg
	}
	return msg, nil
}

// ── replication graders ──────────────────────────────────────────────────

// ledgerObs is one deduplicated, non-overlapping graded observation.
type ledgerObs struct {
	ts   int64
	week int64 // calendar-week cluster (ts / weekSecs)
	up   bool
	win  bool
	high bool // vix_high_vol regime flag at prediction time
}

// ledgerGrader grades one open hypothesis on fresh labeled 1w rows. filter
// selects qualifying rows; call returns the directional call (true = long).
type ledgerGrader struct {
	hypID   string
	horizon md.Horizon
	filter  func(vec map[string]float64) bool
	call    func(vec map[string]float64) bool // true ⇒ predict up
}

// The registered graders — one per OPEN discovery. Closed chapters have no
// grader (their evidence is the transcribed record).
var ledgerGraders = []ledgerGrader{
	{
		// H002 — weekly pressure-inverse: long when the composite pressure
		// score is negative, short when positive.
		hypID:   "H002",
		horizon: md.H1w,
		filter: func(vec map[string]float64) bool {
			v, ok := vec["pressure_score"]
			return ok && v != 0
		},
		call: func(vec map[string]float64) bool { return vec["pressure_score"] < 0 },
	},
	{
		// H008 — weekly RSI-overextension reversion: the same inverse call,
		// but ONLY when the RSI component is extreme (the measured
		// concentration zone). Rarer trigger, wider plausible band.
		hypID:   "H008",
		horizon: md.H1w,
		filter: func(vec map[string]float64) bool {
			v, ok := vec["pressure_score"]
			r, rok := vec["comp_rsi"]
			return ok && v != 0 && rok && math.Abs(r) >= rsiExtremeMin
		},
		call: func(vec map[string]float64) bool { return vec["pressure_score"] < 0 },
	},
}

// replicate grades one hypothesis on data STRICTLY AFTER its last evidence
// window (+ a 2-horizon gap), writes the grade + attack battery as evidence,
// and recomputes the posterior. Returns false when there is not yet enough new
// data — the honest "nothing to update" state.
//
// THE EVIDENCE UNIT IS THE CALENDAR WEEK, NOT THE SYMBOL-WEEK. All symbols
// graded in the same week share that week's market move, so treating N
// symbol-weeks as N independent Bernoulli trials overstates evidence by the
// cluster size — adversarial Monte Carlo measured percent-level FALSE capped
// BFs under the null for a 75-symbol × 2-week grade. Instead each calendar
// week is ONE trial: win = the week's cross-sectional win rate beats that
// week's OWN naive baseline (max(upRate, 1-upRate); the per-week folded
// baseline absorbs the week's common move and makes the trial's null win
// probability <= 0.5 — conservative). The BF is then over (winning weeks,
// total weeks) against null 0.5 with the week-scale band.
func (w *ResearchLedgerWorker) replicate(ctx context.Context, g ledgerGrader, now int64) (bool, error) {
	hyp, ok, err := w.ledgerHyp(ctx, g.hypID)
	if err != nil || !ok {
		return false, err // unknown hypothesis: seed not run yet
	}
	cursor, err := w.St.LedgerEvidenceMaxWindow(ctx, g.hypID)
	if err != nil {
		return false, err
	}
	since := cursor
	if cursor > 0 {
		// 2-horizon gap: the outcome resolver's bar snap can extend a "1w"
		// forward window well past its nominal 7 days, so a single-horizon gap
		// does not guarantee the new window's outcomes are disjoint from the
		// graded past.
		since = cursor + 2*horizonGapSecs(g.horizon)
	}
	rows, err := w.St.LabeledFeaturesSince(ctx, g.horizon, since, ledgerMaxRows)
	if err != nil {
		return false, err
	}

	// Per-symbol TRUE >=7d spacing (sequential, not bucket-based: two obs in
	// adjacent calendar buckets can sit <7d apart, overlapping their forward
	// windows). Rows arrive ts ASC, so keep-first is deterministic.
	lastKept := map[int64]int64{}
	var os []ledgerObs
	for _, r := range rows {
		if !g.filter(r.Vec) {
			continue
		}
		if last, ok := lastKept[r.SymbolID]; ok && r.Ts < last+weekSecs {
			continue
		}
		lastKept[r.SymbolID] = r.Ts
		up := r.Up == 1
		os = append(os, ledgerObs{
			ts:   r.Ts,
			week: r.Ts / weekSecs,
			up:   up,
			win:  g.call(r.Vec) == up,
			high: r.Vec["vix_high_vol"] == 1,
		})
	}

	// Cluster into calendar-week trials.
	byWeek := map[int64]*weekAgg{}
	for _, o := range os {
		wa, ok := byWeek[o.week]
		if !ok {
			wa = &weekAgg{week: o.week}
			byWeek[o.week] = wa
		}
		wa.n++
		if o.win {
			wa.wins++
		}
		if o.up {
			wa.ups++
		}
		if o.high {
			wa.hi++
		}
	}
	var weeks []*weekAgg
	totalObs, highs := 0, 0
	for _, wa := range byWeek {
		if wa.n < minWeekObs {
			continue // a near-empty week is not a measurable cross-section
		}
		weeks = append(weeks, wa)
		totalObs += wa.n
		highs += wa.hi
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].week < weeks[j].week })
	if len(weeks) < minNewWeeks || totalObs < w.MinNewObs {
		return false, nil // not enough NEW independent market-weeks yet
	}

	// Week trials.
	n, k := len(weeks), 0
	for _, wa := range weeks {
		if wa.winsTrial() {
			k++
		}
	}
	bf := rl.BayesFactorAbove(k, n, 0.5, rl.WeekTrialMaxEdge)

	kind := rl.KindReplication
	note := fmt.Sprintf("live grade: %d/%d winning weeks (%d obs; win = week's cross-sectional win rate beats its own naive baseline)", k, n, totalObs)
	if cursor == 0 {
		// The first grade re-measures the discovery window the prior was
		// formed on — cap its weight (rl.DiscoveryMaxBF) and say so. It also
		// does NOT count as a replication (rl.Counters counts KindReplication
		// only), so the supported-gate needs fresh-window evidence.
		kind = rl.KindExperiment
		bf = math.Min(bf, rl.DiscoveryMaxBF)
		note += " — IN-SAMPLE discovery window (the prior was informed by this data); BF capped"
	}
	winFrom, winTo := os[0].ts, os[len(os)-1].ts

	// Attacks are inserted BEFORE the grade row: if the write is interrupted,
	// the cursor (max window_to over experiment/replication rows) has NOT
	// advanced, the next run re-grades the window, and any orphaned penalty
	// rows bias conservative — never optimistic.
	evidence := w.attacks(g.hypID, now, weeks, k, n, highs, winFrom, winTo)
	evidence = append(evidence, rl.Evidence{
		HypID: g.hypID, Ts: now, Kind: kind, K: k, N: n, P0: 0.5, BF: bf,
		Note: note, WindowFrom: winFrom, WindowTo: winTo,
	})
	for _, e := range evidence {
		if err := w.St.InsertLedgerEvidence(ctx, e); err != nil {
			return false, err
		}
	}

	// Regime coverage accumulates on the hypothesis — but a token high-vol
	// presence is not coverage: the gate opens only on a meaningful stressed
	// sample (minHighVolObs), consistent with the house n>=30 convention.
	regimes := hyp.Regimes
	if highs >= minHighVolObs && regimes < 2 {
		regimes = 2
	}
	return true, w.recompute(ctx, g.hypID, regimes, now)
}

// weekAgg is one calendar week's cross-section; winsTrial is the week-trial
// outcome: the cross-sectional win rate strictly beats the week's own folded
// naive baseline.
type weekAgg struct {
	week             int64
	n, wins, ups, hi int
}

func (wa *weekAgg) winsTrial() bool {
	upRate := float64(wa.ups) / float64(wa.n)
	p0 := math.Max(upRate, 1-upRate)
	return float64(wa.wins)/float64(wa.n) > p0
}

// attacks runs the self-attack battery over one graded window of week trials.
// Passed attacks are recorded at BF=1 (they count as runs in lethality stats
// without moving the posterior); failed ones carry their documented penalty.
func (w *ResearchLedgerWorker) attacks(hypID string, now int64, weeks []*weekAgg, k, n, highs int, winFrom, winTo int64) []rl.Evidence {
	var out []rl.Evidence
	add := func(bf float64, note string) {
		out = append(out, rl.Evidence{
			HypID: hypID, Ts: now, Kind: rl.KindAttack, BF: bf, Note: note,
			WindowFrom: winFrom, WindowTo: winTo,
		})
	}

	// [1] time-split: the week-trial win rate must not FLIP SIGN between the
	// window's halves (in week order). A near-zero half is ambiguous, not a
	// flip — the epsilon keeps a flat half from counting as reversal.
	const eps = 0.005
	half := len(weeks) / 2
	rate := func(part []*weekAgg) float64 {
		if len(part) == 0 {
			return 0
		}
		wins := 0
		for _, wa := range part {
			if wa.winsTrial() {
				wins++
			}
		}
		return float64(wins)/float64(len(part)) - 0.5
	}
	s1, s2 := rate(weeks[:half]), rate(weeks[half:])
	switch {
	case (s1 > eps && s2 < -eps) || (s1 < -eps && s2 > eps):
		add(rl.PenaltyTimeSplitFail, fmt.Sprintf("time-split: week-trial edge FLIPS sign between halves (%+.3f / %+.3f) — lucky-period risk", s1, s2))
	default:
		add(1, fmt.Sprintf("time-split: week-trial edge sign stable-or-flat (%+.3f / %+.3f)", s1, s2))
	}

	// [2] regime coverage: a window without a MEANINGFUL stressed sample has
	// never been tested under stress (token high-vol obs do not count).
	if highs >= minHighVolObs {
		add(1, fmt.Sprintf("single-regime: window includes %d high-vol obs (>=%d)", highs, minHighVolObs))
	} else {
		add(rl.PenaltySingleRegime, fmt.Sprintf("single-regime: %d high-vol obs (<%d) — effectively calm-vol only, untested under stress", highs, minHighVolObs))
	}

	// [3] suspicious-edge: a week-trial win rate beyond the plausible band is
	// more likely leakage or a grading bug than skill.
	if edge := float64(k)/float64(n) - 0.5; edge > rl.WeekTrialMaxEdge {
		add(rl.PenaltySuspiciousEdge, fmt.Sprintf("suspicious-edge: week-trial edge %+.3f exceeds the plausible band %.2f — probable leakage/bug", edge, rl.WeekTrialMaxEdge))
	} else {
		add(1, fmt.Sprintf("suspicious-edge: week-trial edge %+.3f within plausible band %.2f", float64(k)/float64(n)-0.5, rl.WeekTrialMaxEdge))
	}
	return out
}

// recompute reloads a hypothesis's evidence chain and writes back the derived
// posterior / status / counters.
func (w *ResearchLedgerWorker) recompute(ctx context.Context, hypID string, regimes int, now int64) error {
	hyp, ok, err := w.ledgerHyp(ctx, hypID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("recompute: unknown hypothesis %s", hypID)
	}
	chain, err := w.St.LedgerEvidence(ctx, hypID)
	if err != nil {
		return err
	}
	// EffectiveChain dedupes static-state attacks (single-regime) to the
	// latest row so an unchanged concern is levied once, not once per grade.
	post := rl.Posterior(hyp.Prior, rl.EffectiveChain(chain))
	reps, contras := rl.Counters(chain)
	status := rl.Status(post, reps, regimes)
	return w.St.UpdateLedgerDerived(ctx, hypID, post, status, reps, contras, regimes, now)
}

func (w *ResearchLedgerWorker) ledgerHyp(ctx context.Context, id string) (rl.Hypothesis, bool, error) {
	hyps, err := w.St.LedgerHypotheses(ctx)
	if err != nil {
		return rl.Hypothesis{}, false, err
	}
	for _, h := range hyps {
		if h.ID == id {
			return h, true, nil
		}
	}
	return rl.Hypothesis{}, false, nil
}

func horizonGapSecs(h md.Horizon) int64 {
	switch h {
	case md.H1w:
		return weekSecs
	case md.H1d:
		return 86400
	default:
		return 86400
	}
}

// ── the seed: the Pressure research program becomes the ledger's first chapter ──

// seedOnce writes the initial hypothesis set exactly once (meta-guarded).
// CLOSED chapters carry transcribed evidence from the 2026-07-15 research
// reports (k/n/p0 recorded verbatim; BFs computed here, not copied). OPEN
// hypotheses get NO transcribed evidence — their graders compute the first
// experiment from the database itself, so no number enters the ledger that the
// engine cannot reproduce.
func (w *ResearchLedgerWorker) seedOnce(ctx context.Context, now int64) (bool, error) {
	if v, _ := w.St.GetMeta(ctx, researchLedgerSeedKey); v != "" {
		return false, nil
	}

	type seedEv struct {
		kind string
		k, n int
		p0   float64
		bf   float64 // 0 ⇒ compute BayesFactorAbove(k,n,p0,maxEdge)
		note string
	}
	seeds := []struct {
		hyp rl.Hypothesis
		ev  []seedEv
	}{
		{
			hyp: rl.Hypothesis{
				ID: "H001", Family: "momentum", Horizon: "1d+1w",
				Statement: "The fixed-weight composite Pressure score (direct) predicts direction",
				Prior:     0.50, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{"Which market regime, if any, makes the DIRECT reading non-harmful? (squeeze was neutral)"},
			},
			ev: []seedEv{
				// KindManual: transcribed same-period measurements, NOT disjoint
				// data windows — they inform the posterior but never the
				// replication counter. NOTE the two horizons share the sample
				// period, so their BFs are correlated; both floor at MinBF, so
				// the dependence cannot overstate the (already decisive) result.
				{kind: rl.KindManual, k: 3375, n: 7117, p0: 0.5093,
					note: "transcribed 2026-07-15: 1d independent symbol-days, direct acc 47.4% vs naive 50.9% (reproducible: rerun the measurement)"},
				{kind: rl.KindManual, k: 1199, n: 2997, p0: 0.5479,
					note: "transcribed 2026-07-15: 1w independent symbol-days, direct acc 40.0% vs naive 54.8% (same period as the 1d row — correlated evidence)"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H003", Family: "momentum",
				Statement: "One component (RSI) causes most of Pressure's damage",
				Prior:     0.50, MaxEdge: rl.DefaultMaxEdge,
			},
			ev: []seedEv{
				{kind: rl.KindManual, bf: rl.MinBF,
					note: "STATED JUDGMENT from the 2026-07-15 decomposition (not a single reproducible binomial): ALL 7 components anti-predictive; Momentum(ROC) worst (-4.5pp 1d / -17.3pp 1w), RSI third — damage systemic, not localized"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H005", Family: "ensemble",
				Statement: "Pressure contaminates profitable partner legs in a blend (drags blend below the partner alone)",
				Prior:     0.50, MaxEdge: 0.15,
				OpenQuestions: []string{"Does contamination persist after the lift-gate benches Pressure? (should vanish)"},
			},
			ev: []seedEv{
				// KindManual: same window, two partner pairings — correlated
				// (the same pressure calls drive both), so they must not read
				// as two independent replications.
				{kind: rl.KindManual, k: 1330, n: 2984, p0: 0.5563, bf: -1, // below-band: BayesFactorBelow
					note: "transcribed 2026-07-15: 1w pressure+forecast blend 44.6% vs forecast alone 55.6%"},
				{kind: rl.KindManual, k: 1289, n: 2988, p0: 0.5318, bf: -1,
					note: "transcribed 2026-07-15: 1w pressure+expectancy blend 43.1% vs expectancy alone 53.2% (same window as the forecast row — correlated evidence)"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H006", Family: "meanrev", Horizon: "1d->1w",
				Statement: "The pressure-inverse edge grows with horizon",
				Prior:     0.50, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{"Does the growth continue past 1w (2w/1m), or peak?"},
			},
			ev: []seedEv{
				{kind: rl.KindManual, bf: 3,
					note: "STATED JUDGMENT (BF=3 is a declared weak-moderate weight, not a computed statistic): inverse edge -3.5pp->+5.2pp and PF 1.09->1.80 from 1d to 1w — monotone over only two points, same period"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H002", Family: "meanrev", Horizon: "1w",
				Statement: "The weekly pressure-INVERSE call has directional edge over naive",
				Prior:     0.25, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{
					"Does the edge survive a high-VIX regime?",
					"Does it survive on a disjoint symbol universe?",
				},
			},
			// No transcribed evidence: the H002 grader computes the first
			// experiment from the DB (non-overlapping weekly windows).
		},
		{
			hyp: rl.Hypothesis{
				ID: "H007", Family: "ensemble",
				Statement: "The OOS gate mis-benches Forecast/Expectancy (both show standalone edge on independent days yet are gated off)",
				Prior:     0.50, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{"Is the gate's grading window / fold geometry the cause, or the per-symbol thinness?"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H008", Family: "meanrev", Horizon: "1w",
				Statement: "The weekly reversion concentrates in RSI overextension (|comp_rsi| extreme)",
				Prior:     0.25, MaxEdge: 0.20, // conditioned, rare-trigger: wider plausible band
				OpenQuestions: []string{
					"Is the driver time-since-extension rather than the RSI level itself?",
					"Does the concentration replicate on fresh non-overlapping windows? (the rsiExtremeMin trigger was calibrated in-sample — replications are the real test)",
				},
			},
			// Graded live (rsiExtremeMin trigger); no transcribed evidence.
		},
		{
			hyp: rl.Hypothesis{
				ID: "H010", Family: "meanrev", Horizon: "1w",
				Statement: "The weekly reversion survives regime change (holds in high-vol markets)",
				Prior:     0.35, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{"Blocked on data: the stored history contains zero high-VIX observations"},
			},
		},
		// ── the frontier: behaviors the engine should investigate next ──
		{
			hyp: rl.Hypothesis{
				ID: "H011", Family: "overnight",
				Statement: "Overnight gaps partially reverse intraday (overnight-reversal)",
				Prior:     0.30, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{"Grader needed: overnight vs intraday return split from 1m/1h bars (data exists)"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H012", Family: "gapfill",
				Statement: "Opening gaps beyond 1% fill within 5 sessions more often than naive",
				Prior:     0.30, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{"Grader needed: gap detection + forward fill scan from daily bars (data exists)"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H013", Family: "volatility",
				Statement: "Squeeze regimes precede realized-volatility expansion",
				Prior:     0.35, MaxEdge: 0.15,
				OpenQuestions: []string{"Grader needed: regime_squeeze -> forward realized-vol ratio (data exists); vol targets are the documented learnable"},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H014", Family: "rotation",
				Statement: "Weekly sector-relative momentum persists (rotation)",
				Prior:     0.30, MaxEdge: rl.DefaultMaxEdge,
				OpenQuestions: []string{"Grader needed: sector aggregates exist (internal/sectors); needs cross-sectional weekly grade"},
			},
		},
	}

	for _, s := range seeds {
		h := s.hyp
		// Derived fields start at the prior; recompute below applies evidence.
		h.Posterior = h.Prior
		h.Status = rl.Status(h.Prior, 0, 1)
		h.Regimes = 1
		if err := w.St.UpsertLedgerHypothesis(ctx, h, now); err != nil {
			return false, err
		}
		// Crash-safe idempotency: a retry after a mid-seed interruption must
		// not duplicate transcribed evidence (each duplicate would multiply
		// its BF into the posterior again).
		if existing, err := w.St.LedgerEvidence(ctx, h.ID); err != nil {
			return false, err
		} else if len(existing) > 0 {
			continue
		}
		for _, e := range s.ev {
			bf := e.bf
			switch {
			case bf == 0: // compute one-sided above-band BF from the recorded observation
				bf = rl.BayesFactorAbove(e.k, e.n, e.p0, h.MaxEdge)
			case bf == -1: // below-band (the hypothesis predicts a rate BELOW p0)
				bf = rl.BayesFactorBelow(e.k, e.n, e.p0, h.MaxEdge)
			}
			if err := w.St.InsertLedgerEvidence(ctx, rl.Evidence{
				HypID: h.ID, Ts: now, Kind: e.kind, K: e.k, N: e.n, P0: e.p0, BF: bf,
				Note: e.note, WindowFrom: 0, WindowTo: 0,
			}); err != nil {
				return false, err
			}
		}
		if err := w.recompute(ctx, h.ID, 1, now); err != nil {
			return false, err
		}
	}
	return true, w.St.SetMeta(ctx, researchLedgerSeedKey, fmt.Sprintf("%d", now))
}

// ═══ WAVE 2 SEED — 2026-07-17 alpha-discovery loop (appended) ════════════════
//
// Transcribes the exhaustive-until-dry discovery loop run 2026-07-17 over the
// bars table (~900 stocks / 7.5y, walk-forward, non-overlapping windows,
// quarter/month-block-clustered CIs; harness + full battery in the session
// scratchpad, results reproduced in internal/structregime docs). New
// hypotheses H015-H020 plus KindManual evidence for the pre-existing frontier
// rows H011/H012/H013. All rows are same-period, same-universe measurements —
// correlated evidence, flagged in every note, exactly like the wave-1
// transcriptions.

// NOTE: "research_ledger_seed_v2" is already claimed by researchengine.go —
// this wave gates on v3.
const researchLedgerSeedV2Key = "research_ledger_seed_v3"

// alphaLoopTag marks every wave-2 evidence note for crash-safe idempotency on
// hypotheses that already carry other evidence.
const alphaLoopTag = "2026-07-17 alpha-loop"

func (w *ResearchLedgerWorker) seedWave2(ctx context.Context, now int64) (bool, error) {
	if v, _ := w.St.GetMeta(ctx, researchLedgerSeedV2Key); v != "" {
		return false, nil
	}

	type ev struct {
		k, n int
		p0   float64
		note string
	}
	newHyps := []struct {
		hyp     rl.Hypothesis
		regimes int
		ev      []ev
	}{
		{
			hyp: rl.Hypothesis{
				ID: "H015", Family: "volatility", Horizon: "21d",
				Statement: "Realized-vol regime is predictable at the MONTHLY horizon too (EWMA-rank persistence, next-21d vol vs trailing median)",
				Prior:     0.50, MaxEdge: 0.25,
				OpenQuestions: []string{"Does a longer-memory blend (dual-rank) significantly beat the single EWMA rank? (2026-07-17: point-better, CIs overlap — unresolved)"},
			},
			regimes: 2, // measured in both VIX<20 and VIX>=20 macro states (63d variant)
			ev: []ev{
				{k: 7879, n: 10943, p0: 0.5,
					note: alphaLoopTag + ": conv>0.9 tier 72.0% (quarter-clustered CI 0.674-0.759, 26 clusters; within-quarter obs correlated). Same universe/period as the shipped 63d result — correlated evidence. Tiers 62.2/67.2/70.1/72.0 shipped in internal/structregime."},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H016", Family: "liquidity", Horizon: "21d",
				Statement: "Dollar-volume (liquidity) regime persists: next-21d mean dollar volume stays on its current side of the trailing-200d median",
				Prior:     0.50, MaxEdge: 0.30,
				OpenQuestions: []string{"The predictor adds ~nothing over naive persistence (0.876 vs 0.876) — is there ANY signal that beats persistence here?"},
			},
			regimes: 1,
			ev: []ev{
				{k: 9662, n: 11030, p0: 0.610,
					note: alphaLoopTag + ": conv>0.9 tier 87.6% (CI 0.857-0.892) vs majority-class 61.0% — but persistence baseline SCORES THE SAME 0.876: the skill IS liquidity persistence. Labels imbalanced by secular volume drift (labelUp 0.56-0.61). Accuracy claim honest; novelty claim would not be."},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H017", Family: "trend", Horizon: "21d",
				Statement: "Above/below-SMA200 state persists 21 trading days ahead, strongest at extreme |distance| (distance-ranked conviction)",
				Prior:     0.50, MaxEdge: 0.45,
				OpenQuestions: []string{
					"Survivorship: universe is currently-tracked stocks — downtrend persistence into delisting unobserved. Does the accuracy hold on a delisted-inclusive universe?",
					"63d variant measured 70.0% all / 83.7% top tier — worth its own shipped horizon?",
				},
			},
			regimes: 1,
			ev: []ev{
				{k: 9129, n: 9392, p0: 0.565,
					note: alphaLoopTag + ": conv>0.9 tier 97.2% (CI 0.965-0.978) vs majority-class 56.5%; all-decisions 83.3% vs 53.9%. Quarter-clustered, 26 clusters. Tiers 83.3/93.1/96.2/97.2 shipped in internal/structregime. Calm-vol stacking NOT significant (97.8 vs 97.2, CIs overlap)."},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H018", Family: "correlation", Horizon: "63d",
				Statement: "63d SPY-correlation regime persists (trailing-rank predictable)",
				Prior:     0.50, MaxEdge: 0.25,
				OpenQuestions: []string{"CI lower bound 0.671 straddles the 0.70 product bar — needs more independent quarters before shipping. Beta regime FAILED (64.1% top tier) — why does corr persist but beta not?"},
			},
			regimes: 1,
			ev: []ev{
				{k: 3146, n: 4303, p0: 0.5,
					note: alphaLoopTag + ": conv>0.9 tier 73.1% (quarter-clustered CI 0.671-0.795, 25 clusters, labelUp 0.466). Real but tentative — NOT shipped as a product surface."},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H019", Family: "anchoring", Horizon: "21d",
				Statement: "The 52-week high acts as a magnet: near-high stocks make new highs beyond matched base rates",
				Prior:     0.30, MaxEdge: rl.DefaultMaxEdge,
			},
			regimes: 1,
			ev: []ev{
				{k: 12231, n: 16093, p0: 0.850,
					note: alphaLoopTag + ": REJECTED by matched null. Near-high (<=2% below) hit 76.0-83.3% looks strong vs the 21.5% unconditional — but far-from-high stocks rise the same required distance 85.0-87.8% of the time (vol-tercile matched). Skill -3 to -9pp: proximity mechanics, not alpha. Textbook example of an unmatched-null fake."},
			},
		},
		{
			hyp: rl.Hypothesis{
				ID: "H020", Family: "momentum", Horizon: "63d",
				Statement: "Cross-sectional relative-strength rank persists (past-63d return rank predicts next-63d top-half membership)",
				Prior:     0.35, MaxEdge: rl.DefaultMaxEdge,
			},
			regimes: 1,
			ev: []ev{
				{k: 10200, n: 20607, p0: 0.5,
					note: alphaLoopTag + ": DEAD FLAT — 49.5% all decisions (CI 0.471-0.518), 51.1% at top conviction decile. Cross-sectional momentum has no ACCURACY edge at 63d on this universe/period."},
			},
		},
	}

	for _, s := range newHyps {
		h := s.hyp
		h.Posterior = h.Prior
		h.Status = rl.Status(h.Prior, 0, 1)
		h.Regimes = s.regimes
		if err := w.St.UpsertLedgerHypothesis(ctx, h, now); err != nil {
			return false, err
		}
		if existing, err := w.St.LedgerEvidence(ctx, h.ID); err != nil {
			return false, err
		} else if len(existing) > 0 {
			continue // crash-safe: never duplicate transcribed evidence
		}
		for _, e := range s.ev {
			bf := rl.BayesFactorAbove(e.k, e.n, e.p0, h.MaxEdge)
			if err := w.St.InsertLedgerEvidence(ctx, rl.Evidence{
				HypID: h.ID, Ts: now, Kind: rl.KindManual, K: e.k, N: e.n, P0: e.p0,
				BF: bf, Note: e.note,
			}); err != nil {
				return false, err
			}
		}
		if err := w.recompute(ctx, h.ID, s.regimes, now); err != nil {
			return false, err
		}
	}

	// Evidence for the pre-existing frontier hypotheses, guarded per-hypothesis
	// against duplication by the alphaLoopTag in the note.
	frontier := []struct {
		hypID   string
		e       ev
		regimes int
	}{
		{"H011", ev{5500, 9735, 0.5,
			alphaLoopTag + ": daily-bar open/close split, |gap|>0.5% of prior close, top |gap|/vol conviction tier: intraday move opposes the gap 56.5% (month-clustered CI 0.541-0.589, n=9,735 of 705k events — 1% coverage). Small REAL reversal edge; also the 5th independent confirmation that directional accuracy tops out mid-50s."}, 1},
		{"H012", ev{106747, 136157, 0.635,
			alphaLoopTag + ": CONFIRMED with matched null. Up-gaps 1-2%: fill within 5 sessions 78.4% vs 63.5% matched no-gap retrace (skill +14.9pp); 2-4%: 74.3% vs 42.9% (+31.4pp); 0.5-1%: 84.9% vs 77.2%. 802k events, month-clustered; within-month obs correlated. Shipped as gapfill5 in internal/structregime."}, 1},
		{"H013", ev{4109, 12605, 0.5,
			alphaLoopTag + ": CONTRADICTED at the stock level. After an extreme EWMA-rank squeeze (rank<0.1), next-5d vol EXPANDS above its trailing median only 32.6-36.0% of the time — persistence dominates even at the extreme; predicting continued calm would score 64-67%. Squeeze does NOT precede expansion at 5d on ~42k squeeze events."}, 1},
	}
	for _, f := range frontier {
		existing, err := w.St.LedgerEvidence(ctx, f.hypID)
		if err != nil {
			return false, err
		}
		dup := false
		for _, x := range existing {
			if strings.Contains(x.Note, alphaLoopTag) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		hyp, ok, err := w.ledgerHyp(ctx, f.hypID)
		if err != nil || !ok {
			continue // frontier row absent (fresh DB mid-seed) — skip, not fatal
		}
		bf := rl.BayesFactorAbove(f.e.k, f.e.n, f.e.p0, hyp.MaxEdge)
		if err := w.St.InsertLedgerEvidence(ctx, rl.Evidence{
			HypID: f.hypID, Ts: now, Kind: rl.KindManual, K: f.e.k, N: f.e.n,
			P0: f.e.p0, BF: bf, Note: f.e.note,
		}); err != nil {
			return false, err
		}
		if err := w.recompute(ctx, f.hypID, f.regimes, now); err != nil {
			return false, err
		}
	}

	return true, w.St.SetMeta(ctx, researchLedgerSeedV2Key, fmt.Sprintf("%d", now))
}

// ═══ WAVE 3 SEED — 2026-07-17 verification pass correction (appended) ════════
//
// The independent re-verification of the alpha-loop replicated trend/
// liquidity/vol21 within noise, but exposed a conditioning error in how H012
// (gap-fill) had been PRODUCTIZED: the measured 72-87% fill rates include
// day-0 fills and are only true at the open of the gap day. For gaps that
// survive day 0 unfilled — the only population an end-of-day worker can
// forecast — the fill rate over the remaining window is 44-61%. The
// unconditional H012 statement stays confirmed; the live gapfill5 kind was
// pulled. Recorded at BF=1 (a note, not new binomial evidence) so the
// correction is on the record without moving the posterior.

const researchLedgerSeedV4Key = "research_ledger_seed_v4"

func (w *ResearchLedgerWorker) seedWave3(ctx context.Context, now int64) (bool, error) {
	if v, _ := w.St.GetMeta(ctx, researchLedgerSeedV4Key); v != "" {
		return false, nil
	}
	const tag = "2026-07-17 verification pass"
	existing, err := w.St.LedgerEvidence(ctx, "H012")
	if err != nil {
		return false, err
	}
	for _, x := range existing {
		if strings.Contains(x.Note, tag) {
			return true, w.St.SetMeta(ctx, researchLedgerSeedV4Key, fmt.Sprintf("%d", now))
		}
	}
	if err := w.St.InsertLedgerEvidence(ctx, rl.Evidence{
		HypID: "H012", Ts: now, Kind: rl.KindManual, BF: 1,
		Note: tag + ": CONDITIONING CORRECTION. The 72-87% fill rates include day-0 fills (true only at the gap-day OPEN). Conditional on surviving day 0 unfilled, remaining-window fill = 55.5/60.6% (0.5-1% up/dn), 52.8/57.4% (1-2%), 51.4/51.0% (2-4%), 45.1/43.9% (4-10%) — every bucket below the 70% product bar. Live gapfill5 forecasts PULLED from the platform; unconditional statement stands. BF=1: correction note, not new binomial evidence.",
	}); err != nil {
		return false, err
	}
	if err := w.recompute(ctx, "H012", 1, now); err != nil {
		return false, err
	}
	return true, w.St.SetMeta(ctx, researchLedgerSeedV4Key, fmt.Sprintf("%d", now))
}
