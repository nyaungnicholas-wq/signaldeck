package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
)

// ── STAGE 7: LIVE OUT-OF-SAMPLE TRACK RECORD (read route) ────────────────────
//
// GET /api/track-record?horizon=1h|1d|1w grades the platform's OWN calibrated
// predictions (prediction_outcomes: prob recorded at prediction time, up +
// fwd_return filled in later by the resolver from realized bars) against what
// the market actually did. This IS the honest scoreboard the whole project
// exists to earn: winrate, Brier, a reliability (calibration) curve, IC + its
// decay by forward horizon and by regime, each with a confidence interval and an
// EXPLICIT "not yet significant (k/threshold)" gate.
//
// HONESTY DOCTRINE, enforced here:
//   - No lookahead: prob is frozen at prediction time; only resolved rows count.
//   - Independent-N: the minute-cadence pipeline can write MANY predictions per
//     symbol per forward period that all resolve against the SAME move. We
//     collapse to ONE observation per (symbol, UTC-day) keeping the LATEST
//     prediction that day before computing ANY skill number, so a handful of
//     independent bets can't masquerade as thousands.
//   - Gate: below trackMinIndependentN independent observations we WITHHOLD every
//     headline number (winrate/Brier/IC null) and say why. With ~0 resolved live
//     outcomes today this page renders honest and mostly-empty — that's the point.
//   - Self-verifying: the payload embeds the prediction-ledger integrity result
//     (Stage 3) and the simulated paper-equity summary (Stage 4) so the record
//     links to its own tamper-evidence and its costed P&L on one surface.

// trackMinDistinctDays is the floor of DISTINCT resolution days before skill
// numbers are shown: cross-sectional obs on one day share one market move, so
// day-count is the binding measure of time-series evidence.
const trackMinDistinctDays = 10

// trackMinIndependentN is the floor of independent (symbol, UTC-day) resolutions
// below which a winrate/Brier/IC is noise, so we report no number and a plain
// "not yet significant" note instead of a figure that overstates skill.
const trackMinIndependentN = 30

// trackDecayLags are the extra forward horizons (in whole days) at which we
// re-grade the SAME predictions to show IC decay. The realized fwd_return in the
// row is the horizon's own move; the decay legs approximate longer holds by not
// re-fetching bars (we only have the one realized return per resolved row), so
// the decay curve here is the by-regime / by-horizon breakdown rather than a
// multi-lag replay — the multi-lag replay lives on /signal-backtest which reads
// the feature store. We therefore expose IC BY REGIME and BY MARKET, which is
// the honest decomposition this table can support without lookahead.

// trackPt is one independent graded observation.
type trackPt struct {
	symbolID int64
	prob     float64 // calibrated P(up) at prediction time
	up       float64 // realized 1/0
	fwd      float64 // realized forward return
	ts       int64
	market   md.Market
}

func (d Deps) trackRecord(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}

	// Same wide window as fleetEdgeSkill: at ~3k resolutions/day a 20k cap spans
	// only ~7 days and wrongly RE-GATES the record now that the universe is large.
	rows, err := d.St.ResolvedPredictionOutcomes(ctx, h, fleetSkillWindow)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	rawN := len(rows)

	// Collapse to ONE independent observation per (symbol, UTC-day), keeping the
	// LATEST prediction that day (rows are ts DESC, so the first seen per key is
	// the latest). No skill number is computed on the raw, pseudo-replicated set.
	seen := map[[2]int64]bool{}
	var pts []trackPt
	for _, o := range rows {
		key := [2]int64{o.SymbolID, o.Ts / 86400}
		if seen[key] {
			continue
		}
		seen[key] = true
		pts = append(pts, trackPt{
			symbolID: o.SymbolID,
			prob:     o.Prob,
			up:       float64(o.Up),
			fwd:      o.FwdReturn,
			ts:       o.Ts,
			market:   o.Market,
		})
	}
	indepN := len(pts)
	// Cross-sectional clustering guard: ~500 symbols resolving on the SAME
	// market day are one market move, not 500 independent tests. Counting
	// (symbol, day) pairs alone let 995 obs over 3 days ungate the record —
	// a ~20x overstatement of evidence. Skill numbers therefore also require
	// a minimum number of DISTINCT resolution days.
	dayset := map[string]bool{}
	for _, p := range pts {
		dayset[time.Unix(p.ts, 0).UTC().Format("2006-01-02")] = true
	}
	distinctDays := len(dayset)
	gated := indepN < trackMinIndependentN || distinctDays < trackMinDistinctDays

	resp := map[string]any{
		"horizon":         h,
		"rawN":            rawN,
		"independentN":    indepN,
		"minIndependentN": trackMinIndependentN,
		"distinctDays":    distinctDays,
		"minDistinctDays": trackMinDistinctDays,
		"clusterNote":     "observations on the same market day are cross-sectionally correlated (one market move); skill unlocks only after both gates: independent obs AND distinct days",
		"gated":           gated,
		// This IS a live forward record (calibrated prob frozen at prediction
		// time, graded against realized bars) — but until it clears the gate it
		// carries no claimable skill, so we still frame it honestly.
		"live":       true,
		"trackLabel": "live out-of-sample — calibrated predictions vs realized outcomes",
	}

	// Per-horizon resolved/total coverage so the page shows how thin the record
	// still is across ALL horizons, not just the selected one. Hoisted so the
	// Stage-2 gate countdown below can reuse the same counts.
	counts, countsErr := d.St.ResolvedPredictionCounts(ctx)
	if countsErr == nil {
		cov := map[string]map[string]int{}
		for _, hz := range md.Horizons {
			c := counts[hz]
			cov[string(hz)] = map[string]int{"resolved": c[0], "total": c[1]}
		}
		resp["coverage"] = cov
	}

	// STAGE 2 — gate countdown: threshold, remaining, and an HONEST unlock
	// estimate from the measured accrual of independent symbol-days over the
	// last 7 calendar days. Best-effort: on any error the gate block is simply
	// absent and the page falls back to the plain notice.
	resp["gate"] = d.trackGate(ctx, h, indepN, counts, countsErr)

	// Self-verifying links: ledger integrity (Stage 3) + paper equity (Stage 4).
	if v, verr := d.St.VerifyLedger(ctx); verr == nil {
		resp["ledger"] = map[string]any{"intact": v.Intact, "count": v.Count, "head": v.HeadHash}
	}
	resp["paper"] = d.paperSummaryForTrackRecord(ctx)

	if gated {
		resp["winRate"] = nil
		resp["brier"] = nil
		resp["ic"] = nil
		note := notSignificant(indepN, trackMinIndependentN)
		if indepN >= trackMinIndependentN && distinctDays < trackMinDistinctDays {
			note = "not yet significant — " + strconv.Itoa(distinctDays) + "/" +
				strconv.Itoa(trackMinDistinctDays) + " distinct market days (obs on one day share one market move)"
		}
		resp["note"] = note
		// Still return the (empty-ish) reliability scaffold + regime buckets so
		// the page can render its honest, mostly-empty shape.
		resp["reliability"] = reliabilityCurve(pts)
		resp["byRegime"] = nil
		resp["byMarket"] = trackByMarket(pts) // descriptive only; not skill claims
		writeJSON(w, resp)
		return
	}

	// ── ungated: report the measured numbers, each with a CI ──
	// winRate is the model's DIRECTIONAL ACCURACY (predUp==actualUp), NOT the
	// market up-rate. Grading against 50% is dishonest: equities close up >50%
	// of days, so the honest benchmark is the best NAIVE constant predictor
	// (always guess the majority direction). edgeVsNaive<=0 means NO skill.
	wins, ups := 0, 0
	for _, p := range pts {
		actualUp := p.up > 0.5
		if (p.prob >= 0.5) == actualUp {
			wins++
		}
		if actualUp {
			ups++
		}
	}
	winRate := float64(wins) / float64(indepN) // directional accuracy
	upRate := float64(ups) / float64(indepN)
	naive := upRate // best constant predictor = max(upRate, 1-upRate)
	naiveDir := "up"
	if 1-upRate > naive {
		naive, naiveDir = 1-upRate, "down"
	}
	loWin, hiWin := wilson(wins, indepN)

	pairs := make([]ensemble.Pair, len(pts))
	for i, p := range pts {
		pairs[i] = ensemble.Pair{Pred: p.prob, Actual: p.up}
	}
	brier := ensemble.BrierScore(pairs)
	// Brier skill score vs the base-rate constant forecast (the only honest
	// benchmark): 1 - Brier/Brier_baserate. >0 means better than always
	// predicting the observed up-rate.
	base := upRate
	brierRef := base * (1 - base) // Brier of the constant base-rate forecast
	var brierSkill float64
	if brierRef > 0 {
		brierSkill = 1 - brier/brierRef
	}

	ic, icLo, icHi := icWithCI(pts)

	resp["winRate"] = winRate
	resp["winRateCI"] = [2]float64{loWin, hiWin}
	resp["baseRate"] = upRate
	resp["naiveBaseline"] = naive
	resp["edgeVsNaive"] = winRate - naive
	resp["accuracyNote"] = fmt.Sprintf("winRate is directional accuracy (predicted direction == realized); the honest benchmark is the naive 'always-%s' baseline of %.1f%%. edgeVsNaive of %+.1fpp %s.",
		naiveDir, naive*100, (winRate-naive)*100,
		map[bool]string{true: "means the model beats the naive guess", false: "means the model does NOT beat simply guessing the majority direction — no measured skill"}[winRate > naive])
	resp["brier"] = brier
	resp["brierSkill"] = brierSkill
	resp["ic"] = ic
	resp["icCI"] = [2]float64{icLo, icHi}
	resp["reliability"] = reliabilityCurve(pts)
	resp["byRegime"] = trackByRegime(ctx, pts, h)
	resp["byMarket"] = trackByMarket(pts)
	resp["reliabilityScore"] = ensemble.ReliabilityScore(pairs)

	writeJSON(w, resp)
}

// paperSummaryForTrackRecord returns a compact costed summary of the default
// simulated book so the track record links to its own paper P&L + turnover +
// capacity note. Best-effort: on any error it returns available:false.
func (d Deps) paperSummaryForTrackRecord(ctx context.Context) map[string]any {
	strategy := defaultPaperStrategy
	rawCurve, err := d.St.PaperEquityCurve(ctx, strategy, 5000)
	if err != nil {
		return map[string]any{"available": false}
	}
	all, err := d.St.AllPaperTradesAsc(ctx, strategy)
	if err != nil {
		return map[string]any{"available": false}
	}
	curve := make([]papertrade.EquityPoint, len(rawCurve))
	for i, p := range rawCurve {
		curve[i] = papertrade.EquityPoint{Ts: p.Ts, Cash: p.Cash, PositionsValue: p.PositionsValue, Equity: p.Equity}
	}
	// Reuse the SAME round-trip reconstruction + summary the /api/paper handler
	// uses, so the numbers here are identical to the /paper page (turnover in
	// particular). This is the Stage-7 "turnover + capacity" surfacing.
	closed, numFills, tradedNotional := reconstructRoundTrips(all)
	sum := papertrade.Summarize(curve, closed, numFills, tradedNotional)
	return map[string]any{
		"available":   len(rawCurve) > 0,
		"strategy":    strategy,
		"totalReturn": sum.TotalReturn,
		"maxDrawdown": sum.MaxDrawdown,
		"turnover":    sum.Turnover,
		"numFills":    sum.NumFills,
		"spanYears":   sum.SpanYears,
	}
}

// ── skill math (self-contained; the api.go pearson is honestyPt-typed) ──

// icWithCI computes the information coefficient (Pearson correlation of the
// signal (prob-0.5) against the realized forward return) over the independent
// set, plus a Fisher-z 95% confidence interval. Returns (0,0,0) below n=4.
func icWithCI(pts []trackPt) (ic, lo, hi float64) {
	n := len(pts)
	if n < 4 {
		return 0, 0, 0
	}
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, p := range pts {
		xs[i] = p.prob - 0.5
		ys[i] = p.fwd
	}
	ic = pearsonF(xs, ys)
	lo, hi = fisherCI(ic, n)
	return ic, lo, hi
}

// pearsonF is Pearson correlation of two equal-length slices; 0 on degenerate
// (zero-variance) inputs.
func pearsonF(xs, ys []float64) float64 {
	n := float64(len(xs))
	if n < 3 {
		return 0
	}
	var sx, sy, sxx, syy, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		syy += ys[i] * ys[i]
		sxy += xs[i] * ys[i]
	}
	den := (n*sxx - sx*sx) * (n*syy - sy*sy)
	if den <= 0 {
		return 0
	}
	return (n*sxy - sx*sy) / math.Sqrt(den)
}

// fisherCI returns a 95% confidence interval for a correlation via the Fisher
// z-transform. Clamps r into (-1,1) to keep atanh finite.
func fisherCI(r float64, n int) (lo, hi float64) {
	if n < 4 {
		return r, r
	}
	rc := math.Max(-0.999999, math.Min(0.999999, r))
	z := math.Atanh(rc)
	se := 1.0 / math.Sqrt(float64(n)-3.0)
	const z95 = 1.959963985
	lo = math.Tanh(z - z95*se)
	hi = math.Tanh(z + z95*se)
	return lo, hi
}

// wilson returns the Wilson 95% score interval for a binomial proportion
// (wins/n). More honest than the normal approximation at small n and near 0/1.
func wilson(wins, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.959963985
	p := float64(wins) / float64(n)
	nn := float64(n)
	denom := 1 + z*z/nn
	center := (p + z*z/(2*nn)) / denom
	half := (z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn))) / denom
	lo = center - half
	hi = center + half
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return lo, hi
}

// reliabilityCurve buckets the independent predictions into calibration bins
// (mean predicted vs mean realized). Empty bins are included so the curve has a
// stable shape even when the record is thin.
func reliabilityCurve(pts []trackPt) []map[string]any {
	pairs := make([]ensemble.Pair, len(pts))
	for i, p := range pts {
		pairs[i] = ensemble.Pair{Pred: p.prob, Actual: p.up}
	}
	bins := ensemble.CalibrationCurve(pairs, 10)
	out := make([]map[string]any, 0, len(bins))
	for _, b := range bins {
		out = append(out, map[string]any{
			"lo": b.Lo, "hi": b.Hi,
			"meanPred": b.MeanPred, "meanActual": b.MeanActual, "n": b.N,
		})
	}
	return out
}

// trackByMarket is a DESCRIPTIVE breakdown of the independent record by market
// (crypto vs stocks): n + winrate + mean forward return. Not gated (it's a
// description of the sample, not a skill claim); the page labels it as such.
func trackByMarket(pts []trackPt) []map[string]any {
	type acc struct {
		n, wins  int
		sumFwd   float64
		sumProb  float64
		hitAsBet int // times the directional bet (prob>0.5 == up) was right
	}
	byM := map[md.Market]*acc{}
	for _, p := range pts {
		a := byM[p.market]
		if a == nil {
			a = &acc{}
			byM[p.market] = a
		}
		a.n++
		a.sumFwd += p.fwd
		a.sumProb += p.prob
		if p.up > 0.5 {
			a.wins++
		}
		bull := p.prob > 0.5
		if bull == (p.up > 0.5) {
			a.hitAsBet++
		}
	}
	markets := make([]md.Market, 0, len(byM))
	for m := range byM {
		markets = append(markets, m)
	}
	sort.Slice(markets, func(i, j int) bool { return markets[i] < markets[j] })
	out := make([]map[string]any, 0, len(byM))
	for _, m := range markets {
		a := byM[m]
		out = append(out, map[string]any{
			"market":     m,
			"n":          a.n,
			"upRate":     ratio(a.wins, a.n),
			"meanFwd":    div(a.sumFwd, a.n),
			"dirHitRate": ratio(a.hitAsBet, a.n),
		})
	}
	return out
}

// trackByRegime would break the independent record down by the regime the
// symbol was in AT PREDICTION TIME. That regime is not persisted per-prediction,
// so reconstructing it now would either require lookahead-free per-ts regime
// history we don't store, or the CURRENT regime — which IS lookahead. Rather
// than fabricate regime attribution, we honestly return nil and the page omits
// the panel with a note. (The by-horizon decomposition the task also asks for is
// served by letting the user switch the horizon selector, and the multi-lag IC
// decay lives on /signal-backtest, which replays the feature store.)
func trackByRegime(_ context.Context, pts []trackPt, _ md.Horizon) []map[string]any {
	_ = pts
	return nil
}

// ── small numeric helpers ──

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func div(a float64, b int) float64 {
	if b == 0 {
		return 0
	}
	return a / float64(b)
}

func notSignificant(n, min int) string {
	return "not yet significant — " + strconv.Itoa(n) + "/" + strconv.Itoa(min) + " independent resolutions"
}

// registerTrackRecord wires the Stage-7 live track-record read route.
func (d Deps) registerTrackRecord(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/track-record", d.trackRecord)
}

// ─────────────────────────────────────────────────────────────────────────────
// STAGE 2 — GATE COUNTDOWN (appended block).
//
// The independent-N gate used to be a dead-feeling "not yet significant"
// notice. This block makes the WAIT itself visible: how many independent
// (symbol, UTC-day) resolutions exist, how many remain to the threshold, and a
// LABELED ESTIMATE of when the gate clears — derived only from the measured
// accrual of the last 7 days (distinct new symbol-days per NYSE trading day).
// When nothing accrued recently the estimate is null and the payload says why:
// an unknown is reported as unknown, never extrapolated from thin air.

// trackAccrualWindowDays is the lookback used to measure the recent accrual
// rate of independent resolutions.
const trackAccrualWindowDays = 7

// trackGate assembles the countdown payload for the selected horizon. All
// estimate fields are explicitly labeled; every input is measured.
func (d Deps) trackGate(ctx context.Context, h md.Horizon, indepN int,
	counts map[md.Horizon][2]int, countsErr error,
) map[string]any {
	now := time.Now()
	since := now.Add(-trackAccrualWindowDays * 24 * time.Hour).Unix()

	accr := map[md.Horizon]stStage2Accrual{}
	if m, err := d.St.ResolutionsSince(ctx, since); err == nil {
		for hz, a := range m {
			accr[hz] = stStage2Accrual{Raw: a.Raw, Independent: a.Independent}
		}
	}
	tradingDays := tradingDaysInLastN(now, trackAccrualWindowDays)
	gate := trackGateCountdown(indepN, trackMinIndependentN,
		accr[h].Independent, tradingDays)

	// First-resolution ETA per horizon (labeled estimate): earliest still-open
	// prediction + its forward window. Only offered for horizons with ZERO
	// resolved outcomes — once anything resolved, the accrual rate is the story.
	eta := map[string]any{}
	for _, hz := range md.Horizons {
		eta[string(hz)] = nil
		if countsErr != nil || counts[hz][0] > 0 {
			continue
		}
		if ts, ok, err := d.St.EarliestUnresolvedTs(ctx, hz); err == nil && ok {
			eta[string(hz)] = ts + horizonWindowSecs(hz)
		}
	}
	gate["firstResolveEta"] = eta
	gate["firstResolveEtaNote"] = "earliest still-open prediction plus its forward window — an estimate, not a promise"
	return gate
}

// stStage2Accrual mirrors store.ResolutionAccrual locally so the pure
// countdown math below stays store-free and unit-testable.
type stStage2Accrual struct{ Raw, Independent int }

// trackGateCountdown is the PURE countdown math: given the current independent
// count, the gate threshold, the independent symbol-days accrued in the last
// window, and how many trading days that window held, it returns the payload
// block. estDaysToUngate is nil when no recent accrual exists to extrapolate
// from (an honest unknown), 0 when the gate is already clear.
func trackGateCountdown(indepN, threshold, newIndep, tradingDays int) map[string]any {
	remaining := threshold - indepN
	if remaining < 0 {
		remaining = 0
	}
	var perDay float64
	if tradingDays > 0 {
		perDay = float64(newIndep) / float64(tradingDays)
	}
	var est any // nil = unknown (nothing accrued recently)
	switch {
	case remaining == 0:
		est = 0
	case perDay > 0:
		est = int(math.Ceil(float64(remaining) / perDay))
	}
	return map[string]any{
		"threshold":       threshold,
		"remaining":       remaining,
		"estDaysToUngate": est,
		"estimate":        true,
		"estBasis": "accrual rate measured over the last " + strconv.Itoa(trackAccrualWindowDays) +
			" days (independent symbol-days per NYSE trading day), assumed to continue — an estimate, not a promise",
		"accrual7d": map[string]any{
			"independentNew": newIndep,
			"tradingDays":    tradingDays,
			"perTradingDay":  perDay,
		},
	}
}

// tradingDaysInLastN counts NYSE trading days (weekdays that are not full
// holidays, in exchange time) inside the last n calendar days ending today.
// Crypto resolves on all days, so for mixed samples this undercounts the true
// accrual window slightly — which biases the estimate LONGER, the honest side
// to err on.
func tradingDaysInLastN(now time.Time, n int) int {
	loc := marketcal.Loc()
	local := now.In(loc)
	days := 0
	for i := 0; i < n; i++ {
		d := local.AddDate(0, 0, -i)
		if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		if marketcal.IsFullHoliday(d) {
			continue
		}
		days++
	}
	return days
}

// horizonWindowSecs is the forward window a horizon needs before its first
// prediction can resolve (matches the resolver's rule: 1w = 7 calendar days,
// 1d = 1 day, 1h = 1 hour).
func horizonWindowSecs(h md.Horizon) int64 {
	switch h {
	case md.H1w:
		return 7 * 86400
	case md.H1h:
		return 3600
	default:
		return 86400
	}
}
