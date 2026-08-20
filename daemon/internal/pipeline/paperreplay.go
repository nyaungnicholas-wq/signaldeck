package pipeline

import (
	"context"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── RECONSTRUCTION MODE ──────────────────────────────────────────────────────
//
// A replay re-runs the book over past bars with the CURRENT code, to answer
// "what would the fixed code have done?". That answer is only worth having if
// every input is bounded to the bar being replayed. An input read at its current
// value is information from the future, and a replay that leaks one produces a
// clean-looking track record that is a tidier fabrication than whatever defect
// it was built to repair.
//
// WHAT THIS IS NOT. The output is a RECONSTRUCTION, not a live track record. It
// was not accumulated in real time, it is computed with today's code rather than
// the code that ran then, and two of its inputs cannot be reconstructed at all
// (below). It is written under its own strategy names so it can never be
// mistaken for, or overwrite, the book that actually ran.
//
// BOUNDED BY CONSTRUCTION, needing no mode flag: every bar read in the step path
// is bounded to the bar being judged in SQL (advUSD, assessEntry's shared bar
// window, dailyReturnsByTs), because live the bar being judged IS the newest bar
// and the bound is a no-op there.
//
// BOOK STATE is correct by construction: the replay writes paper_cursor,
// paper_positions, paper_trades and paper_equity in ascending bar order, so a
// read of its own tables at bar T only ever sees what it wrote for bars < T.
//
// TWO INPUTS CANNOT BE RECONSTRUCTED, and both are declared rather than faked:
//
//  1. EXPECTANCY. The table is keyed (symbol_id, horizon, state_key) with no ts
//     and is overwritten in place; 99.9% of rows have been rewritten since the
//     window of interest, so the values that fed a past decision are gone. It is
//     an ADVISORY prior (paperev.go marks it so) and the gate already has a
//     not-found path — so a replay WITHHOLDS it rather than feeding a past bar
//     today's value. That is a stated, conservative deviation instead of silent
//     lookahead: the reconstruction decides without a prior the live book had.
//
//  2. THE UNIVERSE, outside the membership record's coverage. symbols.active is
//     one mutable flag holding today's answer and must never stand in (the
//     repo's own note: deactivating is a subscription decision, delisting is a
//     market fact, and conflating them is what made the bias invisible).
//     universe_membership is consulted instead, and a day with no record REFUSES
//     rather than falling back.
//
// The RETURN DISTRIBUTION is reconstructible and is reconstructed, not read:
// buildReturnForecast is a pure function of a close series, so the replay re-fits
// it from bars at or before the bar. The stored table could not have served — it
// is keyed (symbol_id, horizon) with one row per key, upserted in place, and its
// oldest surviving row postdates the window this was built for.

// ReplayConfig puts a PaperTrader in reconstruction mode. Nil means live.
type ReplayConfig struct {
	// AsOf is the bar being reconstructed. Run uses it instead of the newest
	// daily bar across the universe.
	AsOf int64

	// MaxPredictionAge bounds how stale a signal may be, in seconds. As-of-ness
	// alone is not enough: the newest usable prediction can be weeks old during a
	// starved stretch, and acting on one is the defect that back-dated 46 fills.
	// Zero disables the bound, which is almost never what a replay wants.
	MaxPredictionAge int64

	// StrategySuffix keeps the reconstruction in its own tables. Required, so a
	// replay cannot overwrite the book that actually ran.
	StrategySuffix string
}

// replaying reports reconstruction mode.
func (w *PaperTrader) replaying() bool { return w.Replay != nil }

// replayAsOf is the bar being reconstructed, or 0 when live (where universeAt
// ignores it).
func (w *PaperTrader) replayAsOf() int64 {
	if w.replaying() {
		return w.Replay.AsOf
	}
	return 0
}

// strategyName is the book a pass writes to: the live name, or the
// reconstruction's own name when replaying.
func (w *PaperTrader) strategyName(base string) string {
	if w.replaying() {
		return base + w.Replay.StrategySuffix
	}
	return base
}

// predictionFor reads the signal, bounded to the bar when replaying.
//
// LatestPrediction is untouched for live callers — six other production readers
// legitimately want "newest". In reconstruction the bound is STRICT: the bar
// being replayed is the FILL bar and the fill anchor is BarAtOrAfter(pred.Ts+1),
// so a prediction stamped at or after that bar is same-bar lookahead.
func (w *PaperTrader) predictionFor(ctx context.Context, symbolID int64, h md.Horizon, asof int64) (store.Prediction, bool, error) {
	if w.replaying() {
		return w.St.PredictionBefore(ctx, symbolID, h, asof, w.Replay.MaxPredictionAge)
	}
	return w.St.LatestPrediction(ctx, symbolID, h)
}

// returnForecastsFor supplies the conditional return distribution per symbol.
//
// Live: the stored table, one read per pass. Replaying: RE-FIT from bars at or
// before the bar, because the stored table holds exactly one row per
// (symbol, horizon), upserted in place, with no history to select from.
func (w *PaperTrader) returnForecastsFor(ctx context.Context, h md.Horizon, syms []md.Symbol, asof int64) (map[int64]store.ReturnForecast, error) {
	if !w.replaying() {
		return w.returnForecastsByID(ctx, h)
	}
	from := asof - int64(distLookbackDays)*86400
	out := make(map[int64]store.ReturnForecast, len(syms))
	for _, s := range syms {
		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, from, asof+1, 0)
		if err != nil {
			return nil, err
		}
		f, ok := buildReturnForecast(closeSeries(bars), h, asof)
		if !ok {
			continue // insufficient history at this bar — the gate refuses, honestly
		}
		f.SymbolID = s.ID
		out[s.ID] = f
	}
	return out, nil
}

// universeAt is the set of symbols the pass may consider.
//
// Live: the current active set. Replaying: the membership recorded for that day,
// and an unrecorded day is an ERROR rather than a fallback — falling back to
// symbols.active would judge a past day against today's survivors, which is the
// bias the membership table exists to remove.
func (w *PaperTrader) universeAt(ctx context.Context, asof int64) ([]md.Symbol, error) {
	if !w.replaying() {
		return w.St.ListSymbols(ctx, true)
	}
	members, ok, err := w.St.UniverseMembersAt(ctx, asof)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &ReplayGapError{AsOf: asof}
	}
	all, err := w.St.ListSymbols(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]md.Symbol, 0, len(members))
	for _, s := range all {
		if members[s.ID] {
			s.Active = true // membership on the day IS the active decision for it
			out = append(out, s)
		}
	}
	return out, nil
}

// ReplayGapError says a bar cannot be reconstructed because the universe on that
// day was never recorded. It is deliberately fatal to the run: skipping the day
// silently would leave a hole in a curve presented as continuous.
type ReplayGapError struct{ AsOf int64 }

func (e *ReplayGapError) Error() string {
	return fmt.Sprintf("no universe_membership record for the day containing ts=%d: "+
		"this bar cannot be reconstructed, and falling back to symbols.active would "+
		"judge a past day against today's survivors", e.AsOf)
}
