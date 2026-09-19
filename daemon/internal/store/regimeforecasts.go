// Persistence for the market-structure regime forecasts (internal/structregime).
package store

import (
	"context"

	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// RegimeForecast is one stored structural-regime forecast joined to its symbol.
type RegimeForecast struct {
	Symbol string `json:"symbol"`
	Market string `json:"market"`
	Ts     int64  `json:"ts"`
	structregime.Forecast
}

// UpsertRegimeForecast writes one symbol's latest forecast for a kind in place.
func (s *Store) UpsertRegimeForecast(ctx context.Context, symbolID, ts int64, f structregime.Forecast) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO regime_forecasts
		  (symbol_id, kind, ts, horizon_days, regime, conviction, historical_accuracy, tier, rank, n)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		symbolID, string(f.Kind), ts, f.HorizonDays, f.Regime, f.Conviction,
		f.HistoricalAccuracy, f.Tier, f.Rank, f.N)
	return err
}

// DeleteRegimeForecast removes a symbol's forecast for a kind — used when an
// event forecast (gap-fill) no longer applies; a stale event row would lie.
func (s *Store) DeleteRegimeForecast(ctx context.Context, symbolID int64, kind structregime.Kind) error {
	_, err := s.w.ExecContext(ctx,
		`DELETE FROM regime_forecasts WHERE symbol_id=? AND kind=?`, symbolID, string(kind))
	return err
}

// RegimeForecastsForSymbol returns one symbol's current forecasts across all
// kinds — the "regime stack" on the per-signal report page.
func (s *Store) RegimeForecastsForSymbol(ctx context.Context, symbolID int64) ([]RegimeForecast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.symbol, s.market, r.ts, r.kind, r.horizon_days, r.regime,
		       r.conviction, r.historical_accuracy, r.tier, r.rank, r.n
		FROM regime_forecasts r JOIN symbols s ON s.id=r.symbol_id
		WHERE r.symbol_id=? ORDER BY r.kind`, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeForecast
	for rows.Next() {
		var v RegimeForecast
		var kind string
		if err := rows.Scan(&v.Symbol, &v.Market, &v.Ts, &kind, &v.HorizonDays,
			&v.Regime, &v.Conviction, &v.HistoricalAccuracy, &v.Tier, &v.Rank, &v.N); err != nil {
			return nil, err
		}
		v.Kind = structregime.Kind(kind)
		hydrateForecastCaveats(&v)
		out = append(out, v)
	}
	return out, rows.Err()
}

// RegimeForecasts returns every stored forecast, highest conviction first
// within each kind.
func (s *Store) RegimeForecasts(ctx context.Context) ([]RegimeForecast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.symbol, s.market, r.ts, r.kind, r.horizon_days, r.regime,
		       r.conviction, r.historical_accuracy, r.tier, r.rank, r.n
		FROM regime_forecasts r JOIN symbols s ON s.id=r.symbol_id
		ORDER BY r.kind, r.conviction DESC, s.symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeForecast
	for rows.Next() {
		var v RegimeForecast
		var kind string
		if err := rows.Scan(&v.Symbol, &v.Market, &v.Ts, &kind, &v.HorizonDays,
			&v.Regime, &v.Conviction, &v.HistoricalAccuracy, &v.Tier, &v.Rank, &v.N); err != nil {
			return nil, err
		}
		v.Kind = structregime.Kind(kind)
		hydrateForecastCaveats(&v)
		out = append(out, v)
	}
	return out, rows.Err()
}

// hydrateForecastCaveats repopulates the honesty fields the table does not store.
//
// structregime.Forecast declares Evidence, FirstGradableOn, EvidenceCaveat and
// Tradeability, and the INSERT persists ten columns that do not include any of
// them, so every read returned them ZERO. None has omitempty, so
// /api/signal-report served `"evidence":"", "firstGradableOn":"",
// "evidenceCaveat":""` beside `"historicalAccuracy":0.972` and
// `"tier":"very-high conviction"`.
//
// An empty Evidence is worse than an absent one: the only two legal values are
// "backtest" and "live", so any consumer branching on evidence=="backtest" to
// attach a warning attached nothing. The five-line BACKTEST CLAIM caveat and the
// Tradeability line -- which says the top band is the most accurate and the
// LEAST profitable -- both vanished at the SQL boundary.
//
// They are derived, not per-row: the values come from package accessors, so
// repopulating on read is correct and needs no migration.
func hydrateForecastCaveats(v *RegimeForecast) {
	if v.Evidence == "" {
		v.Evidence = "backtest"
	}
	if v.FirstGradableOn == "" {
		v.FirstGradableOn = structregime.FirstGradableOnDate()
	}
	if v.EvidenceCaveat == "" {
		v.EvidenceCaveat = structregime.EvidenceCaveatText()
	}
	if v.Tradeability == "" {
		v.Tradeability = structregime.TradeabilityFor(v.Kind, v.Conviction)
	}
}
