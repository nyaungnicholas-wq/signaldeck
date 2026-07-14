// CANDLESTICK-PATTERNS wave — persistence for the MEASURED candlestick edge.
//
// pattern_stats holds one row per (symbol, pattern, horizon): the pattern's
// measured hit rate + mean forward return on THIS symbol's own daily history,
// computed by the pattern-stats worker (internal/candles.MeasureEdges) and
// read back by the /api/candle-patterns handler to annotate each firing with
// "has this shape ever paid here?". Only directional patterns with n >=
// MinPatternN are ever stored — a thin sample is withheld upstream, so a stored
// row always rests on a real sample.
//
// Reads use the pooled s.db handle; the single upsert goes through s.w, matching
// the rest of the store's write discipline.
package store

import "context"

// PatternStat is one candlestick pattern's measured forward-return statistics
// on a symbol's own history.
type PatternStat struct {
	SymbolID int64   `json:"-"`
	Pattern  string  `json:"pattern"`
	Horizon  int     `json:"horizon"`
	HitRate  float64 `json:"hitRate"`
	MeanFwd  float64 `json:"meanFwd"`
	N        int     `json:"n"`
}

// UpsertPatternStat writes one pattern's latest measured edge in place
// (idempotent on the (symbol, pattern, horizon) key).
func (s *Store) UpsertPatternStat(ctx context.Context, p PatternStat) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO pattern_stats (symbol_id, pattern, horizon, hit_rate, mean_fwd, n)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(symbol_id, pattern, horizon) DO UPDATE SET
		  hit_rate=excluded.hit_rate, mean_fwd=excluded.mean_fwd, n=excluded.n`,
		p.SymbolID, p.Pattern, p.Horizon, p.HitRate, p.MeanFwd, p.N)
	return err
}

// PatternStatsForSymbol returns every stored measured-edge row for a symbol
// (all patterns + horizons), ordered by pattern then horizon for determinism.
func (s *Store) PatternStatsForSymbol(ctx context.Context, symbolID int64) ([]PatternStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pattern, horizon, hit_rate, mean_fwd, n
		FROM pattern_stats WHERE symbol_id=? ORDER BY pattern, horizon`, symbolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PatternStat
	for rows.Next() {
		p := PatternStat{SymbolID: symbolID}
		if err := rows.Scan(&p.Pattern, &p.Horizon, &p.HitRate, &p.MeanFwd, &p.N); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
