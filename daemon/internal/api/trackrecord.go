package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
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
//     collapse to ONE observation per (symbol, trading day) keeping the LATEST
//     prediction that day before computing ANY skill number, so a handful of
//     independent bets can't masquerade as thousands.
//   - Cluster-robust intervals: deduplicating to one row per symbol-day removes
//     the intraday pseudo-replication and leaves the LARGER problem untouched —
//     ~1,000 symbols on one day share ONE market move. Every interval published
//     here therefore comes from internal/clusterstat with the DAY as the unit of
//     resampling, corrected by a design effect measured from the data. On the
//     SUPERSEDED-SNAPSHOT 2026-07-25 measurement that sized this — a dated
//     example, not the current record — the effect was 14.7x on
//     the live 1d record: effective N 887, not 13,058. The raw count never sets
//     an interval's width, and it is never presented alone as a sample size.
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

// trackMinIndependentN is the floor of independent (symbol, trading day) resolutions
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
	// settleTs is the base bar this row was graded from. Carried alongside ts so
	// the dedup key, the cluster-resampling unit and the IC day buckets all fold
	// on the SAME thing; they drifted apart the moment one of them moved.
	settleTs int64
	market   md.Market
}

// trackRecord is the UNCACHED handler — tests drive it directly so seeded rows
// are always visible. Production traffic goes through registerTrackRecord's
// stale-while-revalidate wrapper below.
func (d Deps) trackRecord(w http.ResponseWriter, r *http.Request) {
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	resp, err := d.buildTrackRecord(r.Context(), h)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, resp)
}

// trackRecordCached serves the same payload through the SWR cache. Perf wave
// 2026-07-24: the full grade over the 120k-row window costs ~22s per request
// in the pure-Go driver and is identical for every user; the 2m TTL sits well
// under the 10m resolver cadence that changes the underlying rows, so
// staleness is bounded by design and nobody waits behind a rebuild.
func (d Deps) trackRecordCached(w http.ResponseWriter, r *http.Request) {
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1h && h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	resp, err := sharedTrackCache.get(r.Context(), string(h),
		func(ctx context.Context) (map[string]any, error) {
			return d.buildTrackRecord(ctx, h)
		})
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, resp)
}

// buildTrackRecord computes the full track-record payload for one horizon.
// Pure build — no HTTP — so the response cache can rebuild it off-request.
func (d Deps) buildTrackRecord(ctx context.Context, h md.Horizon) (map[string]any, error) {
	// Same wide window as fleetEdgeSkill: at ~3k resolutions/day a 20k cap spans
	// only ~7 days and wrongly RE-GATES the record now that the universe is large.
	rows, err := d.St.ResolvedPredictionOutcomes(ctx, h, fleetSkillWindow)
	if err != nil {
		return nil, err
	}
	rawN := len(rows)

	// Collapse to ONE independent observation per (symbol, trading day), keeping the
	// LATEST prediction that day (rows are ts DESC, so the first seen per key is
	// the latest). No skill number is computed on the raw, pseudo-replicated set.
	seen := map[[2]int64]bool{}
	var pts []trackPt
	for _, o := range rows {
		key := [2]int64{o.SymbolID, md.SettleDay(o.SettleTs, o.Ts)}
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
			settleTs: o.SettleTs,
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

	// COLLAPSED CROSS-SECTIONS. The two floors above count evidence; they cannot
	// see that the evidence is one market-wide call repeated per symbol. That is
	// what CollapsedGradingWindow measures, and until now /api/accuracy was its
	// ONLY caller — so the accuracy surface refused over a window while this one
	// published over the very same days, and the five surfaces that read THIS
	// payload (/proof, /lab/track-record, ProofStrip, the desk recommendation
	// card, and the raw endpoint) showed skill numbers with nothing marking them.
	// That is the "refused on one document, published on six" divergence this
	// repo has already been bitten by, one endpoint out.
	//
	// FAIL OPEN on a read error, exactly as the HTTP handler does and as the
	// gate's own doc requires: refusing on a transient database error would wedge
	// publication shut on something that is not evidence.
	var collapseReason string
	if reg, rerr := loadRegistry(d.RegistryPath); rerr == nil {
		if reason, collapsed, cerr := d.collapsedGradingWindow(ctx, reg, time.Now()); cerr == nil && collapsed {
			gated = true
			collapseReason = reason
		}
	}

	resp := map[string]any{
		"horizon":         h,
		"rawN":            rawN,
		"independentN":    indepN,
		"minIndependentN": trackMinIndependentN,
		"distinctDays":    distinctDays,
		"minDistinctDays": trackMinDistinctDays,
		"clusterNote": "observations on the same market day are cross-sectionally correlated (one market move); " +
			"skill unlocks only after both gates (independent obs AND distinct days), and every interval " +
			"published here is then corrected by a MEASURED design effect with the day as the unit of " +
			"resampling — see the 'cluster' block for the design effect, the effective N and the distinct-day count",
		"gated": gated,
		// This IS a live forward record (calibrated prob frozen at prediction
		// time, graded against realized bars) — but until it clears the gate it
		// carries no claimable skill, so we still frame it honestly.
		"live":       true,
		"trackLabel": "live out-of-sample — calibrated predictions vs realized outcomes",
		// C-2 (2026-08-02 re-audit): /api/calibration publishes the same record
		// at a wider, ungated scope and therefore different numbers. Naming that
		// here is what stops the pair reading as a contradiction.
		"scopeNote": trackRecordScopeNote,
		// #20: the predictions record is graded on the currently-tracked
		// universe's bars — the survivorship label travels with the stats.
		"survivorship": survivorshipBlock(),
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

	// Credibility wave: LIVE regime-forecast grading per kind (claimed vs
	// realized, own 30-resolution gate per kind — appended block below). Not
	// affected by the directional gate: regimes are their own record.
	resp["regimes"] = d.regimeTrackRecord(ctx)

	// Self-verifying links: ledger integrity (Stage 3, incremental checkpoint
	// path — cold-load precompute wave) + paper equity (Stage 4).
	if v, _, verr := d.St.VerifyLedgerCached(ctx); verr == nil {
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
		// A collapse outranks the sample-size note: the sample is large enough
		// and is still not evidence, which is a different statement and the one
		// a reader needs. Same reason string /api/accuracy refuses with, so the
		// two surfaces cannot describe the same window differently.
		if collapseReason != "" {
			note = collapseReason
		}
		resp["note"] = note
		// Sample-size facts only. A gated record may say HOW MUCH evidence it
		// holds — raw N, distinct days, the measured design effect and the
		// effective N those imply — but not one number that reads as a skill
		// claim, so every interval is stripped before this ships.
		resp["cluster"] = trackClusterDescriptive(clusterstat.Grade(trackClusterObs(pts)))
		// Still return the (empty-ish) reliability scaffold + regime buckets so
		// the page can render its honest, mostly-empty shape.
		resp["reliability"] = reliabilityCurve(pts)
		resp["byRegime"] = nil
		resp["byMarket"] = trackByMarket(pts) // descriptive only; not skill claims
		return resp, nil
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

	// C4 — the accuracy interval is CLUSTER-ROBUST, never Wilson at indepN.
	// indepN counts symbol-days; the symbols on one day share one market move,
	// so the honest sample size is indepN/designEffect. Measured on the live 1d
	// record the correction is 14.7x, which widened the published interval by
	// 3.8x. clusterstat refuses outright below its day floor, and a refusal
	// means winRateCI is null — a withheld interval beats a narrow one.
	cl := clusterstat.Grade(trackClusterObs(pts))
	resp["cluster"] = cl
	if cl.CI != nil {
		resp["winRateCI"] = [2]float64{cl.CI.Lo, cl.CI.Hi}
	} else {
		resp["winRateCI"] = nil
	}

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
	// The IC interval is the SAME defect on a different statistic: a Fisher-z
	// interval at n assumes n independent pairs. Re-derive it by resampling
	// whole days; the Fisher version survives only as the fallback for samples
	// too thin for the bootstrap, and is labeled as such in the payload.
	icMethod := "Fisher-z at raw symbol-day count — NOT cluster-corrected (too few days to resample)"
	if lo, hi, ok := icDayClusteredCI(pts); ok {
		icLo, icHi = lo, hi
		icMethod = "day-clustered bootstrap (whole days resampled with replacement)"
	}

	resp["winRate"] = winRate
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
	resp["icCIMethod"] = icMethod
	resp["reliability"] = reliabilityCurve(pts)
	resp["byRegime"] = trackByRegime(ctx, pts, h)
	resp["byMarket"] = trackByMarket(pts)
	resp["reliabilityScore"] = ensemble.ReliabilityScore(pairs)

	return resp, nil
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

// ── cluster-robust plumbing (C4) ──────────────────────────────────────────────

// trackClusterObs projects the independent record into the one shape
// internal/clusterstat grades: an outcome plus the DAY that clusters it. The
// day index is md.SettleDay — the same key the (symbol, settled-move) dedup
// above uses, so the resampling unit and the dedup unit cannot drift apart.
func trackClusterObs(pts []trackPt) []clusterstat.Obs {
	out := make([]clusterstat.Obs, len(pts))
	for i, p := range pts {
		bullish := p.prob >= 0.5
		dir := clusterstat.DirDown
		if bullish {
			dir = clusterstat.DirUp
		}
		out[i] = clusterstat.Obs{
			Day: md.SettleDay(p.settleTs, p.ts),
			Hit: bullish == (p.up > 0.5), // same rule as the winRate loop above
			Dir: dir,
		}
	}
	return out
}

// trackClusterDescriptive strips every interval and every point estimate from a
// cluster grade, leaving only the sample-size facts. It is what a GATED record
// is allowed to publish: how much evidence exists, with nothing that reads as a
// skill claim. Without this, wiring the cluster block into the gated branch
// would quietly route an accuracy and its interval around the gate.
func trackClusterDescriptive(cl clusterstat.Result) clusterstat.Result {
	cl.CI, cl.NaiveCIDiscredited, cl.BootstrapCI, cl.DayBet = nil, nil, nil, nil
	cl.P, cl.WidthRatio, cl.DailyAgreement = 0, 0, 0
	gateReason := "intervals withheld: the record has not cleared the track-record gates"
	if cl.Refused && cl.Reason != "" {
		gateReason = cl.Reason + " (and the record is gated)"
	}
	cl.Refused, cl.Reason = true, gateReason
	return cl
}

// icDayClusteredCI re-derives the IC interval by resampling WHOLE DAYS. The
// Fisher-z interval it replaces assumes n independent (signal, return) pairs;
// the pairs on one day share one market move, so that interval is too narrow by
// the same factor as the accuracy interval was. ok is false when there are too
// few days to resample, and the caller then labels the Fisher fallback plainly
// rather than presenting it as corrected.
func icDayClusteredCI(pts []trackPt) (lo, hi float64, ok bool) {
	type xy struct{ x, y float64 }
	byDay := map[int64][]xy{}
	for _, p := range pts {
		d := md.SettleDay(p.settleTs, p.ts)
		byDay[d] = append(byDay[d], xy{x: p.prob - 0.5, y: p.fwd})
	}
	days := make([]int64, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	if len(days) < clusterstat.MinDistinctDays {
		return 0, 0, false
	}
	// Deterministic order, so the resample draws map to the same days on every
	// run and the interval is reproducible.
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	// Buffers hoisted out of the closure: the bootstrap rebuilds a sample the
	// size of the whole record on every draw, and reallocating it 1,000 times
	// is the difference between milliseconds and seconds on this endpoint.
	xs := make([]float64, 0, len(pts))
	ys := make([]float64, 0, len(pts))
	iv, ok := clusterstat.BootstrapStat(len(days), 1000, 0.05, func(idx []int) (float64, bool) {
		xs, ys = xs[:0], ys[:0]
		for _, i := range idx {
			for _, v := range byDay[days[i]] {
				xs = append(xs, v.x)
				ys = append(ys, v.y)
			}
		}
		if len(xs) < 4 {
			return 0, false
		}
		// A resample with no variance left in the signal or the outcome has an
		// UNDEFINED correlation, not a zero one. Counting it as 0 would pull the
		// percentile interval toward a spurious [0,0] — false certainty, which
		// is the same defect as a too-narrow interval wearing different clothes.
		return pearsonOK(xs, ys)
	})
	if !ok {
		return 0, 0, false
	}
	return iv.Lo, iv.Hi, true
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
	r, _ := pearsonOK(xs, ys)
	return r
}

// pearsonOK is pearsonF with the degenerate case made VISIBLE: ok is false when
// either series has no variance, so a caller that must not conflate "no
// correlation" with "no correlation is defined" can tell them apart. The
// day-clustered bootstrap needs that distinction — averaging degenerate draws in
// as zeros collapses its interval to a spuriously certain [0,0].
func pearsonOK(xs, ys []float64) (float64, bool) {
	n := float64(len(xs))
	if n < 3 {
		return 0, false
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
		return 0, false
	}
	return (n*sxy - sx*sy) / math.Sqrt(den), true
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

// wilson returns the Wilson 95% score interval for a binomial proportion at the
// RAW count. Nothing in this file publishes it any more: it assumes n
// independent trials, and on this platform n counts symbol-days that share one
// market move, which made every interval it produced 3.8-4.9x too narrow. Use
// clusterstat.Grade instead. It survives only for its regression tests — no
// production code calls it — and it delegates to clusterstat.WilsonEffAt so
// the tree holds ONE Wilson implementation (gates_test.go enforces both).
func wilson(wins, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	iv := clusterstat.WilsonEffAt(float64(wins)/float64(n), float64(n), 1.959963985)
	return iv.Lo, iv.Hi
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

// registerTrackRecord wires the Stage-7 live track-record read route (cached).
func (d Deps) registerTrackRecord(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/track-record", d.trackRecordCached)
}

// ─────────────────────────────────────────────────────────────────────────────
// STAGE 2 — GATE COUNTDOWN (appended block).
//
// The independent-N gate used to be a dead-feeling "not yet significant"
// notice. This block makes the WAIT itself visible: how many independent
// (symbol, trading day) resolutions exist, how many remain to the threshold, and a
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

// ─────────────────────────────────────────────────────────────────────────────
// CREDIBILITY WAVE — LIVE REGIME-FORECAST GRADING (appended block).
//
// The regime forecasts ship with MEASURED walk-forward accuracy tiers; this
// section is where those claims meet reality: for every kind, the resolved
// live record (regime_outcomes, frozen at call time by the regime-outcome
// worker) is graded — live accuracy with a Wilson 95% CI next to the MEAN
// CLAIMED accuracy of the same resolved calls. Same honesty pattern as the
// directional record: below regimeMinResolutions resolutions PER KIND the
// accuracy is withheld with a "not yet significant — k/30" note, never a
// number that overstates a thin sample.

// regimeMinResolutions is the per-kind floor of resolved regime calls below
// which live accuracy is withheld.
const regimeMinResolutions = 30

// regimeTrackRecord assembles the per-kind live regime grading payload.
// Best-effort: on a store error it returns an "available:false" stub rather
// than failing the whole track-record page.
func (d Deps) regimeTrackRecord(ctx context.Context) map[string]any {
	rows, err := d.St.ResolvedRegimeOutcomes(ctx, 50000)
	if err != nil {
		return map[string]any{"available": false, "error": err.Error()}
	}
	// One independent observation per (symbol, kind, UTC-day). The dedup unique
	// index already guarantees this at write time; the re-check here is
	// defensive so a future schema change can't silently pseudo-replicate.
	type agg struct {
		n, correct int
		sumClaimed float64
		// obs carries the day each call resolved on. Regime calls cluster the
		// same way directional ones do — every symbol's vol regime moves with
		// the same market — so this record gets the same cluster-robust
		// treatment rather than a Wilson interval at the raw call count.
		obs []clusterstat.Obs
	}
	seen := map[[3]int64]bool{}
	byKind := map[string]*agg{}
	for _, r := range rows {
		k := string(r.Kind)
		dk := [3]int64{r.SymbolID, kindOrdinal(k), r.Day}
		if seen[dk] {
			continue
		}
		seen[dk] = true
		a := byKind[k]
		if a == nil {
			a = &agg{}
			byKind[k] = a
		}
		a.n++
		if r.Correct == 1 {
			a.correct++
		}
		a.sumClaimed += r.HistoricalAccuracy
		// Dir is DirNone: "was this regime call right" is not a directional bet,
		// so the market-breadth diagnostic must not be computed for it.
		a.obs = append(a.obs, clusterstat.Obs{Day: r.Day, Hit: r.Correct == 1})
	}
	kinds := map[string]any{}
	for k, a := range byKind {
		e := map[string]any{
			"resolvedN":      a.n,
			"minResolutions": regimeMinResolutions,
			"claimed":        a.sumClaimed / float64(a.n), // mean claimed acc of resolved calls
		}
		if a.n < regimeMinResolutions {
			e["gated"] = true
			e["liveAccuracy"] = nil
			e["liveAccuracyCI"] = nil
			e["cluster"] = trackClusterDescriptive(clusterstat.Grade(a.obs))
			e["note"] = notSignificant(a.n, regimeMinResolutions)
		} else {
			cl := clusterstat.Grade(a.obs)
			e["gated"] = false
			e["liveAccuracy"] = float64(a.correct) / float64(a.n)
			e["cluster"] = cl
			// A refusal here means the calls span too few days to support any
			// interval. Publishing null with the reason beats publishing the
			// Wilson interval this branch used to compute at the raw count.
			if cl.CI != nil {
				e["liveAccuracyCI"] = [2]float64{cl.CI.Lo, cl.CI.Hi}
			} else {
				e["liveAccuracyCI"] = nil
				e["ciNote"] = cl.Reason
			}
		}
		kinds[k] = e
	}
	return map[string]any{
		"available": true,
		"kinds":     kinds,
		"dedupNote": "one observation per (symbol, kind, UTC-day); conviction and claimed accuracy frozen at call time, realized labels recomputed with the exact engine math",
		// #20: regime claims were measured on (and are resolved against) the
		// currently-tracked universe's bars.
		"survivorship": survivorshipBlock(),
	}
}

// kindOrdinal maps a regime kind to a stable small int for the dedup key.
func kindOrdinal(k string) int64 {
	switch k {
	case "trend21":
		return 1
	case "trend63":
		return 2
	case "liquidity21":
		return 3
	case "vol21":
		return 4
	case "trend21-crypto":
		return 5
	case "liquidity21-crypto":
		return 6
	default:
		return 99
	}
}
