// Stage 5 — FINRA Reg SHO store methods: daily short sale volume, universe-
// scoped (rows exist only for symbols we track; the finra-shorts worker does
// the filtering). All writes go through the single write connection s.w;
// reads use s.db.
//
// HONESTY: short_pct is the daily short sale VOLUME ratio — NOT short
// interest; it includes market-maker activity and a high ratio is NOT
// directly bearish. The API carries that caveat verbatim.
package store

import (
	"context"
	"fmt"
)

// ShortVolumeRow is one symbol's Reg SHO daily short-sale volume for one day.
// Symbol is populated only by queries that join symbols (extremes).
type ShortVolumeRow struct {
	SymbolID    int64   `json:"symbolId"`
	Symbol      string  `json:"symbol,omitempty"`
	Day         string  `json:"day"` // YYYY-MM-DD
	ShortVol    float64 `json:"shortVol"`
	ShortExempt float64 `json:"shortExempt"`
	TotalVol    float64 `json:"totalVol"`
	ShortPct    float64 `json:"shortPct"` // short_vol/total_vol, 0 when total 0
}

// UpsertShortVolume writes a batch of daily rows in ONE transaction on the
// write connection. INSERT OR REPLACE on the (symbol_id, day) PK makes
// re-ingesting a day (backfill overlap, FINRA's rare "Updated" re-posts)
// idempotent — the newest file wins.
func (s *Store) UpsertShortVolume(ctx context.Context, rows []ShortVolumeRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO short_volume
		  (symbol_id, day, short_vol, short_exempt, total_vol, short_pct)
		VALUES (?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.SymbolID, r.Day,
			r.ShortVol, r.ShortExempt, r.TotalVol, r.ShortPct); err != nil {
			return fmt.Errorf("upsert short_volume %d/%s: %w", r.SymbolID, r.Day, err)
		}
	}
	return tx.Commit()
}

// ShortVolumeSeries returns one symbol's last `days` rows in ASCENDING day
// order (chart/sparkline-ready).
func (s *Store) ShortVolumeSeries(ctx context.Context, symbolID int64, days int) ([]ShortVolumeRow, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, day, short_vol, short_exempt, total_vol, short_pct
		FROM short_volume WHERE symbol_id = ?
		ORDER BY day DESC LIMIT ?`, symbolID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ShortVolumeRow
	for rows.Next() {
		var r ShortVolumeRow
		if err := rows.Scan(&r.SymbolID, &r.Day, &r.ShortVol, &r.ShortExempt,
			&r.TotalVol, &r.ShortPct); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse DESC → ASC.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// LatestShortVolumeDay is the most recent day with any stored rows ("" when
// the table is empty — honest absence, not an error).
func (s *Store) LatestShortVolumeDay(ctx context.Context) (string, error) {
	var day string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(day), '') FROM short_volume`).Scan(&day)
	return day, err
}

// ShortVolumeExtremes returns the tracked symbols with the HIGHEST short
// sale volume ratio on `day`, joined to their tickers. minTotal filters out
// tiny-volume names whose ratios are noise (the API labels the floor —
// nothing is hidden silently). Descriptive, not a signal: see the caveat.
func (s *Store) ShortVolumeExtremes(ctx context.Context, day string, minTotal float64, limit int) ([]ShortVolumeRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sv.symbol_id, sy.symbol, sv.day, sv.short_vol, sv.short_exempt,
		       sv.total_vol, sv.short_pct
		FROM short_volume sv JOIN symbols sy ON sy.id = sv.symbol_id
		WHERE sv.day = ? AND sv.total_vol >= ?
		ORDER BY sv.short_pct DESC, sy.symbol LIMIT ?`, day, minTotal, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ShortVolumeRow
	for rows.Next() {
		var r ShortVolumeRow
		if err := rows.Scan(&r.SymbolID, &r.Symbol, &r.Day, &r.ShortVol,
			&r.ShortExempt, &r.TotalVol, &r.ShortPct); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HasShortVolumeDay reports whether any rows are stored for a day — the
// worker's cheap idempotence check beyond the meta day-key.
func (s *Store) HasShortVolumeDay(ctx context.Context, day string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM short_volume WHERE day = ? LIMIT 1`, day).Scan(&n)
	return n > 0, err
}
