package api

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
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

	rows, err := d.St.ResolvedPredictionOutcomes(ctx, h, 20000)
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
	gated := indepN < trackMinIndependentN

	resp := map[string]any{
		"horizon":         h,
		"rawN":            rawN,
		"independentN":    indepN,
		"minIndependentN": trackMinIndependentN,
		"gated":           gated,
		// This IS a live forward record (calibrated prob frozen at prediction
		// time, graded against realized bars) — but until it clears the gate it
		// carries no claimable skill, so we still frame it honestly.
		"live":       true,
		"trackLabel": "live out-of-sample — calibrated predictions vs realized outcomes",
	}

	// Per-horizon resolved/total coverage so the page shows how thin the record
	// still is across ALL horizons, not just the selected one.
	if counts, cerr := d.St.ResolvedPredictionCounts(ctx); cerr == nil {
		cov := map[string]map[string]int{}
		for _, hz := range md.Horizons {
			c := counts[hz]
			cov[string(hz)] = map[string]int{"resolved": c[0], "total": c[1]}
		}
		resp["coverage"] = cov
	}

	// Self-verifying links: ledger integrity (Stage 3) + paper equity (Stage 4).
	if v, verr := d.St.VerifyLedger(ctx); verr == nil {
		resp["ledger"] = map[string]any{"intact": v.Intact, "count": v.Count, "head": v.HeadHash}
	}
	resp["paper"] = d.paperSummaryForTrackRecord(ctx)

	if gated {
		resp["winRate"] = nil
		resp["brier"] = nil
		resp["ic"] = nil
		resp["note"] = notSignificant(indepN, trackMinIndependentN)
		// Still return the (empty-ish) reliability scaffold + regime buckets so
		// the page can render its honest, mostly-empty shape.
		resp["reliability"] = reliabilityCurve(pts)
		resp["byRegime"] = nil
		resp["byMarket"] = trackByMarket(pts) // descriptive only; not skill claims
		writeJSON(w, resp)
		return
	}

	// ── ungated: report the measured numbers, each with a CI ──
	wins := 0
	for _, p := range pts {
		if p.up > 0.5 {
			wins++
		}
	}
	winRate := float64(wins) / float64(indepN)
	loWin, hiWin := wilson(wins, indepN)

	pairs := make([]ensemble.Pair, len(pts))
	for i, p := range pts {
		pairs[i] = ensemble.Pair{Pred: p.prob, Actual: p.up}
	}
	brier := ensemble.BrierScore(pairs)
	// Brier skill score vs the base-rate constant forecast (the only honest
	// benchmark): 1 - Brier/Brier_baserate. >0 means better than always
	// predicting the observed up-rate.
	base := winRate
	brierRef := base * (1 - base) // Brier of the constant base-rate forecast
	var brierSkill float64
	if brierRef > 0 {
		brierSkill = 1 - brier/brierRef
	}

	ic, icLo, icHi := icWithCI(pts)

	resp["winRate"] = winRate
	resp["winRateCI"] = [2]float64{loWin, hiWin}
	resp["baseRate"] = base
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
