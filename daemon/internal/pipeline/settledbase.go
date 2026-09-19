package pipeline

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// settledBaseSinceTs is the cutoff timestamp (2026-09-07T18:20:00Z) after which
// predictions were built from settled daily bars only and are graded from the
// settled base; earlier rows keep the prior rule (base = bar at/before ts,
// even if forming).
const settledBaseSinceTs = int64(1788807600)

// trimFormingDaily removes the newest daily bar if it is still forming.
// daily must be sorted ascending by Ts; only the last bar can be forming.
// Returns the possibly trimmed slice and a bool indicating whether a bar was dropped.
func trimFormingDaily(market md.Market, daily []md.Bar, now int64) ([]md.Bar, bool) {
	if len(daily) == 0 {
		return daily, false
	}
	last := daily[len(daily)-1]
	if !md.DailyBarSettled(market, last.Ts, now) {
		return daily[:len(daily)-1], true
	}
	return daily, false
}

func symbolMarkets(ctx context.Context, st *store.Store) (map[int64]md.Market, error) { // inactive included: their rows still resolve
	syms, err := st.ListSymbols(ctx, false)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]md.Market, len(syms))
	for _, s := range syms {
		m[s.ID] = s.Market
	}
	return m, nil
}

// settledBase returns the settled daily bar to use as the base for a prediction
// made at timestamp ts. For ts < settledBaseSinceTs the base is simply the bar
// at-or-before ts (even if that bar is still forming). For ts >= settledBaseSinceTs
// the base must be a settled bar; if the at-or-before bar is still forming we
// step back one second and look again, guaranteeing we never regrade history
// under a rule it was not frozen under.
func settledBase(ctx context.Context, st *store.Store, market md.Market, symbolID int64, ts int64) (md.Bar, bool, error) {
	base, ok, err := st.BarAtOrBefore(ctx, symbolID, md.TF1d, ts)
	if err != nil || !ok {
		return base, ok, err
	}
	if ts >= settledBaseSinceTs && !md.DailyBarSettled(market, base.Ts, ts) {
		return st.BarAtOrBefore(ctx, symbolID, md.TF1d, base.Ts-1)
	}
	return base, true, nil
}
