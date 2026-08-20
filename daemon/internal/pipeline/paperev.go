package pipeline

// Decision Engine (Layer 1) input assembly for the paper worker: everything
// internal/ev judges is MEASURED here, from the same stores the rest of the
// platform writes — the gate itself stays pure (riskgate's discipline). Every
// input that cannot be measured is handed over with its has-flag false, never
// as a silent zero; ev.Decide refuses on the required ones and records the
// advisory ones as absent in the ledgered snapshot.

import (
	"context"
	"encoding/json"
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/confidence"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ev"
	"github.com/nyaungnicholas-wq/signaldeck/internal/expectancy"
	"github.com/nyaungnicholas-wq/signaldeck/internal/killswitch"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/riskgate"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// evEpisodeCap mirrors the confidence endpoint's episode window: ~2 years of
// daily bars is what the platform stores per symbol, so a larger cap would
// only re-read the same rows.
const evEpisodeCap = 600

// evCorrBars is the daily-return window the correlation-to-book estimate uses,
// and evCorrMinOverlap the least overlapping days before it is reported at
// all — below that a correlation is an artifact of a handful of points.
const (
	evCorrBars       = 64
	evCorrMinOverlap = 20
)

// returnForecastsByID fetches the stored conditional return distributions for
// one horizon, keyed by symbol id — one read per pass, not one per candidate.
func (w *PaperTrader) returnForecastsByID(ctx context.Context, h md.Horizon) (map[int64]store.ReturnForecast, error) {
	rows, err := w.St.ReturnForecasts(ctx, string(h), "", 1_000_000)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]store.ReturnForecast, len(rows))
	for _, f := range rows {
		out[f.SymbolID] = f
	}
	return out, nil
}

// assessEntry measures every Decision Engine input for one entry candidate and
// returns the assessment. equity/cash size the intended fill the execution
// cost is modelled for; fc is the symbol's stored return distribution (nil =
// none, and the gate will refuse on the missing requirement).
func (w *PaperTrader) assessEntry(
	ctx context.Context,
	s md.Symbol,
	h md.Horizon,
	strategy string,
	pred store.Prediction,
	in papertrade.ExecInputs,
	equity, cash float64,
	fc *store.ReturnForecast,
	asof int64,
) (ev.Assessment, error) {
	inputs := ev.Inputs{
		Symbol:  s.Symbol,
		Horizon: string(h),
		CalProb: pred.CalProb,
		HasProb: true,
	}

	// Forecast distribution — the EV itself. Its Regime rides along.
	if fc != nil {
		inputs.DistMean = fc.Mean
		inputs.Tau = fc.Tau
		inputs.Sigma = fc.Sigma
		inputs.PInside = fc.PInside
		inputs.DistEV = fc.ExpectedValue
		inputs.DistN = fc.N
		inputs.HasDist = true
		if fc.Regime != "" {
			inputs.Regime, inputs.HasRegime = fc.Regime, true
		}
	}

	// One bar read serves the expectancy state key AND the tail episodes.
	bars, err := w.St.LastBars(ctx, s.ID, md.TF1d, evEpisodeCap)
	if err != nil {
		return ev.Assessment{}, err
	}

	// State-conditional expectancy prior (advisory).
	if key := expectancy.CurrentStateKeys(bars, nil)[h]; key != "" {
		rows, err := w.St.Expectancy(ctx, s.ID, h)
		if err != nil {
			return ev.Assessment{}, err
		}
		if row, found := expectancy.Lookup(rows, key); found {
			inputs.ExpMeanFwd, inputs.ExpN, inputs.HasExpectancy = row.MeanFwd, row.N, true
		}
	}

	// Tail: measured adverse excursion of every hold of this horizon's length
	// on this symbol — UNCONDITIONAL, same convention (and same honesty note)
	// as the /api/confidence read. Thin samples come back withheld, and a
	// withheld tail stays withheld here.
	if x := adverseExcursionFromBars(bars, evHoldBarsFor(h)); x.Valid {
		inputs.TailP90, inputs.TailMean, inputs.TailN, inputs.HasTail = x.P90, x.Mean, x.N, true
	}

	// Liquidity/capacity from the execution model's own inputs.
	if in.ADVUSD > 0 && !math.IsNaN(in.ADVUSD) && !math.IsInf(in.ADVUSD, 0) {
		inputs.ADVUSD = in.ADVUSD
		inputs.CapacityUSD = papertrade.MaxParticipation() * in.ADVUSD
		inputs.HasLiquidity = true
	}

	// Execution cost of the INTENDED fill, round trip: half-spread plus the
	// square-root-law impact of the equal-slice notional, both sides. The same
	// arithmetic papertrade charges (execution.go), reproduced on the intended
	// size because the gate must judge the trade it would actually take —
	// without a liquidity estimate or a usable bar range the cost is
	// unknowable and the flag stays false (fail closed, never fill free).
	if frac, ok := roundTripCostFrac(in, papertrade.PositionBudget(equity, cash)); ok {
		inputs.RoundTripCostFrac, inputs.HasCost = frac, true
	}

	// Correlation to the current book (advisory; the audit trail for Layer 7).
	if corr, ok, err := w.corrToBook(ctx, strategy, s.ID, asof); err != nil {
		return ev.Assessment{}, err
	} else if ok {
		inputs.CorrToBook, inputs.HasCorr = corr, true
	}

	return ev.Assess(inputs), nil
}

// evHoldBarsFor is the holding length in daily bars each horizon implies —
// the same convention as the confidence endpoint (1d holds one bar, 1w five).
func evHoldBarsFor(h md.Horizon) int {
	if h == md.H1w {
		return 5
	}
	return 1
}

// adverseExcursionFromBars builds next-bar-entry holding episodes from stored
// daily bars and measures their adverse excursion. Entry is each bar's OPEN
// and the window its following `hold` bars, using LOWS — the same episode
// construction the confidence endpoint documents, so the two surfaces cannot
// disagree about what "tail" means.
func adverseExcursionFromBars(bars []md.Bar, hold int) confidence.Excursion {
	if len(bars) < hold+1 {
		return confidence.Excursion{Note: "not enough stored daily bars to measure a holding window"}
	}
	eps := make([]confidence.Episode, 0, len(bars))
	for i := 0; i+hold < len(bars); i++ {
		entry := bars[i].Open
		if entry <= 0 || math.IsNaN(entry) {
			continue
		}
		lows := make([]float64, 0, hold)
		for j := i; j < i+hold; j++ {
			lows = append(lows, bars[j].Low)
		}
		eps = append(eps, confidence.Episode{
			EntryPx: entry,
			Lows:    lows,
			Fwd:     (bars[i+hold].Close - entry) / entry,
		})
	}
	return confidence.AdverseExcursion(eps)
}

// roundTripCostFrac models the ROUND-TRIP execution cost of deploying
// `notional` against the candidate's fill bar and liquidity, as a fraction of
// notional: 2 × (half-spread + square-root impact), the per-side charge
// papertrade's execution model applies. ok=false when the bar range, the
// liquidity estimate, or the notional make the cost unknowable.
func roundTripCostFrac(in papertrade.ExecInputs, notional float64) (float64, bool) {
	if notional <= 0 || in.ADVUSD <= 0 || math.IsNaN(in.ADVUSD) || math.IsInf(in.ADVUSD, 0) {
		return 0, false
	}
	if in.Bar.High <= 0 || in.Bar.Low <= 0 || in.Bar.High < in.Bar.Low {
		return 0, false
	}
	const k = 1.6651092223153954 // 2*sqrt(ln 2) — Parkinson (1980), as in execution.go
	sigma := math.Log(in.Bar.High/in.Bar.Low) / k
	if sigma < 0 || math.IsNaN(sigma) || math.IsInf(sigma, 0) {
		return 0, false
	}
	spread := papertrade.CostBpsFor(in.Market) / 1e4
	impact := papertrade.ImpactCoef() * sigma * math.Sqrt(notional/in.ADVUSD)
	frac := 2 * (spread + impact)
	if math.IsNaN(frac) || math.IsInf(frac, 0) {
		return 0, false
	}
	return frac, true
}

// corrToBook is the Pearson correlation between the candidate's daily returns
// and the equal-weight daily return of the book's CURRENT open positions, over
// the trailing evCorrBars sessions at or before asof. ok=false when the book
// is empty or the overlapping history is too thin to say anything — an empty
// book concentrates nothing, and a thin correlation would be noise wearing a
// number's clothes.
func (w *PaperTrader) corrToBook(ctx context.Context, strategy string, symbolID, asof int64) (float64, bool, error) {
	open, err := w.St.PaperPositions(ctx, strategy)
	if err != nil {
		return 0, false, err
	}
	holdings := make([]int64, 0, len(open))
	for _, p := range open {
		if p.Qty > 0 && p.SymbolID != symbolID {
			holdings = append(holdings, p.SymbolID)
		}
	}
	if len(holdings) == 0 {
		return 0, false, nil
	}

	candRet, err := w.dailyReturnsByTs(ctx, symbolID, asof)
	if err != nil {
		return 0, false, err
	}
	// Equal-weight book return per session: mean across holdings that have a
	// return for that ts. Sessions where NO holding has a return are dropped.
	sums := map[int64]float64{}
	counts := map[int64]int{}
	for _, id := range holdings {
		rets, err := w.dailyReturnsByTs(ctx, id, asof)
		if err != nil {
			return 0, false, err
		}
		for ts, r := range rets {
			sums[ts] += r
			counts[ts]++
		}
	}

	var xs, ys []float64
	for ts, r := range candRet {
		if n := counts[ts]; n > 0 {
			xs = append(xs, r)
			ys = append(ys, sums[ts]/float64(n))
		}
	}
	if len(xs) < evCorrMinOverlap {
		return 0, false, nil
	}
	corr, ok := pearson(xs, ys)
	return corr, ok, nil
}

// dailyReturnsByTs maps bar ts -> that session's close-to-close return, over
// the trailing evCorrBars sessions at or before asof.
func (w *PaperTrader) dailyReturnsByTs(ctx context.Context, symbolID, asof int64) (map[int64]float64, error) {
	bars, err := w.St.LastBars(ctx, symbolID, md.TF1d, evCorrBars+1)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]float64, len(bars))
	for i := 1; i < len(bars); i++ {
		if bars[i].Ts > asof {
			break
		}
		prev, cur := bars[i-1].Close, bars[i].Close
		if prev <= 0 || cur <= 0 {
			continue
		}
		out[bars[i].Ts] = cur/prev - 1
	}
	return out, nil
}

// pearson is the sample correlation of two aligned series; ok=false when
// either side has no variance (a flat series correlates with nothing).
func pearson(xs, ys []float64) (float64, bool) {
	n := float64(len(xs))
	if n < 2 {
		return 0, false
	}
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx /= n
	my /= n
	var sxy, sxx, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return 0, false
	}
	return sxy / math.Sqrt(sxx*syy), true
}

// minimalAssessment is the honest record for a candidate that was refused
// BEFORE its inputs were measured — a halted platform does no measuring it
// would then throw away. Every has-flag stays false, so the snapshot says "this
// was never assessed" rather than implying a pile of zeroed inputs.
func minimalAssessment(symbol string, h md.Horizon, calProb float64) ev.Assessment {
	return ev.Assessment{Inputs: ev.Inputs{
		Symbol: symbol, Horizon: string(h), CalProb: calProb, HasProb: true,
	}}
}

// gateSnapshot is what gets frozen into the ledger's inputs_json for a refusal
// from OUTSIDE the EV engine. The assessment is embedded anonymously, so its
// fields stay at the top level exactly as they were before this existed and
// every current reader keeps working; risk and halt are additive keys.
type gateSnapshot struct {
	ev.Assessment
	Risk    *riskgate.Decision      `json:"risk,omitempty"`
	Halt    *killswitch.State       `json:"halt,omitempty"`
	Barrier *papertrade.BarrierExit `json:"barrier,omitempty"`
	FillTs  int64                   `json:"fillTs,omitempty"`
}

// ledgerBarrierExit records a SELL, carrying the barrier that caused it when
// one did.
//
// The barrier snapshot holds the trigger timestamp and the fill timestamp as
// SEPARATE fields on purpose: the no-lookahead guarantee is that the fill is
// strictly later than the close that confirmed the exit, and a ledger that
// stored only one of the two could not be used to check it. Anyone auditing
// this book can assert `fillTs > barrier.triggerTs` over every row.
func (w *PaperTrader) ledgerBarrierExit(
	apply *store.PaperApply,
	strategy string,
	symbolID int64,
	a ev.Assessment,
	d ev.Decision,
	plan exitPlan,
	ts int64,
) error {
	if !plan.fromBarrier {
		return w.ledgerEVDecision(apply, strategy, symbolID, a, d, ts)
	}
	b := plan.barrier
	snap, err := json.Marshal(gateSnapshot{Assessment: a, Barrier: &b, FillTs: plan.fillBar.Ts})
	if err != nil {
		return err
	}
	apply.Decisions = append(apply.Decisions, store.EVDecision{
		Ts: ts, Strategy: strategy, SymbolID: symbolID, Symbol: a.Symbol, Horizon: a.Horizon,
		Decision: string(d.Action), Reason: string(d.Reason), InputsJSON: string(snap),
	})
	return nil
}

// ledgerGateRefusal records a DO_NOTHING that the PRETRADE RISK GATE or the
// KILL SWITCH produced, into the same ledger the EV engine writes to.
//
// One ledger, deliberately. A refusal filed in a table nobody joins against is
// a refusal nobody reads, and the property worth having — every candidate the
// book did not trade, with the reason, in one query — is only true if all three
// gates write to the same place. The full riskgate.Decision (its Reasons AND
// its Breaches) is snapshotted, so "which limit fired" survives without needing
// a schema change to hold it.
func (w *PaperTrader) ledgerGateRefusal(
	apply *store.PaperApply,
	strategy string,
	symbolID int64,
	a ev.Assessment,
	reason ev.Reason,
	risk *riskgate.Decision,
	halt *killswitch.State,
	ts int64,
) error {
	snap, err := json.Marshal(gateSnapshot{Assessment: a, Risk: risk, Halt: halt})
	if err != nil {
		return err
	}
	row := store.EVDecision{
		Ts:         ts,
		Strategy:   strategy,
		SymbolID:   symbolID,
		Symbol:     a.Symbol,
		Horizon:    a.Horizon,
		Decision:   string(ev.DO_NOTHING),
		Reason:     string(reason),
		Rank:       a.Rank,
		RankOf:     a.RankOf,
		InputsJSON: string(snap),
	}
	if a.HasNetEV {
		v := a.NetEV
		row.NetEV = &v
	}
	apply.Decisions = append(apply.Decisions, row)
	return nil
}

// ledgerEVDecision appends one verdict to the ev_decisions ledger. The full
// assessment (with its has-flags) is snapshotted as JSON so "what did the gate
// know when it refused" survives the inputs' own tables moving on.
//
// Staged on the PaperApply and committed in the SAME transaction as the book.
// It used to write directly, on the reasoning that decisions are diagnostics
// rather than book state — but that left two holes. An apply that failed (or
// merely returned an error) left the rows behind describing a step that never
// happened; and because a failed apply leaves the cursor unadvanced, the next
// pass re-rendered the identical bar and appended a second full set. ev_decisions
// has no unique key, and nothing reconciles it against paper_trades, so neither
// orphans nor duplicates were detectable after the fact.
func (w *PaperTrader) ledgerEVDecision(
	apply *store.PaperApply,
	strategy string,
	symbolID int64,
	a ev.Assessment,
	d ev.Decision,
	ts int64,
) error {
	snap, err := json.Marshal(a)
	if err != nil {
		return err
	}
	row := store.EVDecision{
		Ts:         ts,
		Strategy:   strategy,
		SymbolID:   symbolID,
		Symbol:     a.Symbol,
		Horizon:    a.Horizon,
		Decision:   string(d.Action),
		Reason:     string(d.Reason),
		Rank:       a.Rank,
		RankOf:     a.RankOf,
		InputsJSON: string(snap),
	}
	if a.HasNetEV {
		v := a.NetEV
		row.NetEV = &v
	}
	apply.Decisions = append(apply.Decisions, row)
	return nil
}
