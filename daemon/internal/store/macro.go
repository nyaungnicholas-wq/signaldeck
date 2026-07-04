// Free-data store methods (Stage 2): FRED macro series + SEC EDGAR
// fundamentals. Both are zero-cost, no-vendor context sources. Macro rows are a
// long thin (series, ts, value) time series; fundamentals are per-symbol facts
// keyed by (symbol_id, metric, as_of). All writes go through the single write
// connection s.w; reads use the pool s.db, matching the rest of the store.
package store

import (
	"context"
	"database/sql"
)

// MacroPoint is one observation of a FRED series.
type MacroPoint struct {
	Series string  `json:"series"`
	Ts     int64   `json:"ts"`    // observation day, UTC-midnight epoch seconds
	Value  float64 `json:"value"` // observed level
}

// FundamentalRow is one SEC EDGAR company-fact for a symbol.
type FundamentalRow struct {
	SymbolID  int64   `json:"symbolId"`
	Symbol    string  `json:"symbol,omitempty"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	AsOf      int64   `json:"asOf"`      // period-end / effective date, epoch seconds
	FetchedAt int64   `json:"fetchedAt"` // when fetched, epoch seconds
}

// ── FRED macro series ─────────────────────────────────────────────────────

// InsertMacro upserts one macro observation. INSERT OR IGNORE keeps a re-poll
// idempotent — an already-recorded (series, ts) is never overwritten, so a
// revised value from a later fetch won't silently change history (FRED daily
// levels for VIX/yields are final once published; the tiny cost of ignoring a
// rare revision is worth the idempotency guarantee).
func (s *Store) InsertMacro(ctx context.Context, series string, ts int64, value float64) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT OR IGNORE INTO macro_series (series, ts, value) VALUES (?,?,?)`,
		series, ts, value)
	return err
}

// LatestMacro returns the most-recent observation of series (ok=false when the
// series has no rows yet). This is the primary accessor the feature layer
// (Stage 6) uses to consume a macro level as a cross-asset feature.
func (s *Store) LatestMacro(ctx context.Context, series string) (MacroPoint, bool, error) {
	var p MacroPoint
	p.Series = series
	err := s.db.QueryRowContext(ctx,
		`SELECT ts, value FROM macro_series WHERE series=? ORDER BY ts DESC LIMIT 1`,
		series).Scan(&p.Ts, &p.Value)
	if err == sql.ErrNoRows {
		return MacroPoint{}, false, nil
	}
	if err != nil {
		return MacroPoint{}, false, err
	}
	return p, true, nil
}

// LatestVIX is a convenience wrapper for the VIX close (FRED series VIXCLS) —
// the headline cross-asset "fear" level the feature layer wants first.
func (s *Store) LatestVIX(ctx context.Context) (float64, bool, error) {
	p, ok, err := s.LatestMacro(ctx, "VIXCLS")
	return p.Value, ok, err
}

// MacroSeries returns up to limit most-recent observations of series, oldest
// first (chart-ready). limit<=0 returns all rows.
func (s *Store) MacroSeries(ctx context.Context, series string, limit int) ([]MacroPoint, error) {
	// Fetch newest-first with an optional cap, then reverse to oldest-first so
	// the caller always gets a chronological series regardless of limit.
	q := `SELECT ts, value FROM macro_series WHERE series=? ORDER BY ts DESC`
	args := []any{series}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []MacroPoint
	for rows.Next() {
		p := MacroPoint{Series: series}
		if err := rows.Scan(&p.Ts, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse in place -> oldest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// LatestMacroAll returns the latest value of each distinct series present, in a
// map keyed by series id. Used by the /api/macro-series overview.
func (s *Store) LatestMacroAll(ctx context.Context) (map[string]MacroPoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.series, m.ts, m.value
		FROM macro_series m
		JOIN (SELECT series, MAX(ts) AS mx FROM macro_series GROUP BY series) t
		  ON t.series = m.series AND t.mx = m.ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]MacroPoint{}
	for rows.Next() {
		var p MacroPoint
		if err := rows.Scan(&p.Series, &p.Ts, &p.Value); err != nil {
			return nil, err
		}
		out[p.Series] = p
	}
	return out, rows.Err()
}

// ── SEC EDGAR fundamentals ─────────────────────────────────────────────────

// UpsertFundamental writes (or refreshes) one company-fact. INSERT OR REPLACE
// on the (symbol_id, metric, as_of) key: a re-fetch of the same period-end
// updates only fetched_at (and value, should EDGAR restate it), while a new
// period accumulates as a new row — so a metric's history grows over time.
func (s *Store) UpsertFundamental(ctx context.Context, f FundamentalRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO fundamentals (symbol_id, metric, value, as_of, fetched_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(symbol_id, metric, as_of) DO UPDATE SET
		  value = excluded.value,
		  fetched_at = excluded.fetched_at`,
		f.SymbolID, f.Metric, f.Value, f.AsOf, f.FetchedAt)
	return err
}

// LatestFundamentals returns the most-recent value of each metric for one
// symbol (one row per metric, newest as_of). This is what /api/fundamentals
// serves as the current snapshot.
func (s *Store) LatestFundamentals(ctx context.Context, symbolID int64) ([]FundamentalRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.metric, f.value, f.as_of, f.fetched_at
		FROM fundamentals f
		JOIN (SELECT metric, MAX(as_of) AS mx FROM fundamentals
		      WHERE symbol_id=? GROUP BY metric) t
		  ON t.metric = f.metric AND t.mx = f.as_of
		WHERE f.symbol_id=?
		ORDER BY f.metric`, symbolID, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []FundamentalRow
	for rows.Next() {
		r := FundamentalRow{SymbolID: symbolID}
		if err := rows.Scan(&r.Metric, &r.Value, &r.AsOf, &r.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FundamentalHistory returns every stored value of one metric for a symbol,
// oldest first (for a small time series, e.g. quarterly revenue).
func (s *Store) FundamentalHistory(ctx context.Context, symbolID int64, metric string) ([]FundamentalRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT value, as_of, fetched_at FROM fundamentals
		WHERE symbol_id=? AND metric=? ORDER BY as_of ASC`, symbolID, metric)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []FundamentalRow
	for rows.Next() {
		r := FundamentalRow{SymbolID: symbolID, Metric: metric}
		if err := rows.Scan(&r.Value, &r.AsOf, &r.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
