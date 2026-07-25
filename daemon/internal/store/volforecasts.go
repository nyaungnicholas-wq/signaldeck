// Persistence for the volatility-regime forecast (internal/volregime).
package store

import (
	"context"
	"database/sql"

	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

// VolForecast is one stored volatility-regime forecast joined to its symbol.
type VolForecast struct {
	Symbol string `json:"symbol"`
	Market string `json:"market"`
	Ts     int64  `json:"ts"`
	volregime.Forecast
}

// UpsertVolForecast writes one symbol's latest regime forecast in place.
func (s *Store) UpsertVolForecast(ctx context.Context, symbolID, ts int64, f volregime.Forecast) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO vol_forecasts
		  (symbol_id, ts, regime, conviction, historical_accuracy, tier, rank, n)
		VALUES (?,?,?,?,?,?,?,?)`,
		symbolID, ts, f.Regime, f.Conviction, f.HistoricalAccuracy, f.Tier, f.Rank, f.N)
	return err
}

// VolForecastForSymbol returns one symbol's current quarterly vol-regime
// forecast, ok=false when none is stored.
func (s *Store) VolForecastForSymbol(ctx context.Context, symbolID int64) (VolForecast, bool, error) {
	var v VolForecast
	err := s.db.QueryRowContext(ctx, `
		SELECT s.symbol, s.market, f.ts, f.regime, f.conviction, f.historical_accuracy,
		       f.tier, f.rank, f.n
		FROM vol_forecasts f JOIN symbols s ON s.id=f.symbol_id
		WHERE f.symbol_id=?`, symbolID).Scan(&v.Symbol, &v.Market, &v.Ts, &v.Regime,
		&v.Conviction, &v.HistoricalAccuracy, &v.Tier, &v.Rank, &v.N)
	if err == sql.ErrNoRows {
		return VolForecast{}, false, nil
	}
	if err != nil {
		return VolForecast{}, false, err
	}
	return v, true, nil
}

// DeleteVolForecast removes a symbol's forecast — used when the predictor now
// refuses the symbol (wild-move guard, thin history); a stale row would lie.
func (s *Store) DeleteVolForecast(ctx context.Context, symbolID int64) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM vol_forecasts WHERE symbol_id=?`, symbolID)
	return err
}

// VolForecasts returns every stored forecast, highest conviction first — the
// list the API serves (the confident calls are the ones worth surfacing).
func (s *Store) VolForecasts(ctx context.Context) ([]VolForecast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.symbol, s.market, v.ts, v.regime, v.conviction, v.historical_accuracy,
		       v.tier, v.rank, v.n
		FROM vol_forecasts v JOIN symbols s ON s.id=v.symbol_id
		ORDER BY v.conviction DESC, s.symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []VolForecast
	for rows.Next() {
		var v VolForecast
		if err := rows.Scan(&v.Symbol, &v.Market, &v.Ts, &v.Regime, &v.Conviction,
			&v.HistoricalAccuracy, &v.Tier, &v.Rank, &v.N); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
