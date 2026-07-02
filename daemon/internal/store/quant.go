package store

import (
	"context"
	"database/sql"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Forecast is a stored directional forecast + its out-of-sample grade.
type Forecast struct {
	SymbolID int64      `json:"-"`
	Horizon  md.Horizon `json:"horizon"`
	Ts       int64      `json:"ts"`
	Prob     float64    `json:"prob"`
	Accuracy float64    `json:"accuracy"`
	Brier    float64    `json:"brier"`
	AUC      float64    `json:"auc"`
	BaseRate float64    `json:"baseRate"`
	Lift     float64    `json:"lift"`
	NTrain   int        `json:"nTrain"`
	NEval    int        `json:"nEval"`
}

// UpsertForecast stores the latest forecast for a symbol+horizon.
func (s *Store) UpsertForecast(ctx context.Context, f Forecast) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO forecasts
		  (symbol_id, horizon, ts, prob, accuracy, brier, auc, base_rate, lift, n_train, n_eval)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		f.SymbolID, string(f.Horizon), f.Ts, f.Prob, f.Accuracy, f.Brier, f.AUC,
		f.BaseRate, f.Lift, f.NTrain, f.NEval)
	return err
}

// Forecasts returns the stored forecasts for a symbol (both horizons).
func (s *Store) Forecasts(ctx context.Context, symbolID int64) ([]Forecast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT horizon, ts, prob, accuracy, brier, auc, base_rate, lift, n_train, n_eval
		FROM forecasts WHERE symbol_id=? ORDER BY horizon`, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []Forecast
	for rows.Next() {
		f := Forecast{SymbolID: symbolID}
		var h string
		if err := rows.Scan(&h, &f.Ts, &f.Prob, &f.Accuracy, &f.Brier, &f.AUC,
			&f.BaseRate, &f.Lift, &f.NTrain, &f.NEval); err != nil {
			return nil, err
		}
		f.Horizon = md.Horizon(h)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Position is a paper position (open or closed).
type Position struct {
	ID           int64    `json:"id"`
	SymbolID     int64    `json:"-"`
	Symbol       string   `json:"symbol"`
	Market       string   `json:"market"`
	Qty          float64  `json:"qty"`
	EntryPrice   float64  `json:"entryPrice"`
	EntryTs      int64    `json:"entryTs"`
	Note         string   `json:"note"`
	ScoreAtEntry float64  `json:"scoreAtEntry"`
	Open         bool     `json:"open"`
	ExitPrice    *float64 `json:"exitPrice"`
	ExitTs       *int64   `json:"exitTs"`
}

// InsertPosition logs a new open paper position.
func (s *Store) InsertPosition(ctx context.Context, p Position) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO positions (symbol_id, qty, entry_price, entry_ts, note, score_at_entry, open)
		VALUES (?,?,?,?,?,?,1)`,
		p.SymbolID, p.Qty, p.EntryPrice, p.EntryTs, p.Note, p.ScoreAtEntry)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ClosePosition marks a position closed at the given price/time.
func (s *Store) ClosePosition(ctx context.Context, id int64, exitPrice float64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE positions SET open=0, exit_price=?, exit_ts=? WHERE id=?`,
		exitPrice, time.Now().Unix(), id)
	return err
}

// Positions returns positions joined to their symbol (openOnly filters).
func (s *Store) Positions(ctx context.Context, openOnly bool) ([]Position, error) {
	q := `SELECT p.id, p.symbol_id, sym.symbol, sym.market, p.qty, p.entry_price,
	             p.entry_ts, p.note, p.score_at_entry, p.open, p.exit_price, p.exit_ts
	      FROM positions p JOIN symbols sym ON sym.id = p.symbol_id`
	if openOnly {
		q += ` WHERE p.open=1`
	}
	q += ` ORDER BY p.entry_ts DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []Position
	for rows.Next() {
		var p Position
		var open int
		var exitP sql.NullFloat64
		var exitT sql.NullInt64
		if err := rows.Scan(&p.ID, &p.SymbolID, &p.Symbol, &p.Market, &p.Qty, &p.EntryPrice,
			&p.EntryTs, &p.Note, &p.ScoreAtEntry, &open, &exitP, &exitT); err != nil {
			return nil, err
		}
		p.Open = open == 1
		if exitP.Valid {
			p.ExitPrice = &exitP.Float64
		}
		if exitT.Valid {
			p.ExitTs = &exitT.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
