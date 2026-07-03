// Store methods for the daily-briefing worker (see internal/briefing).
// Separate file so the briefing wave never touches core store.go paths.
package store

import (
	"context"
	"database/sql"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// InsightsByKind returns the newest insights whose evidence blob carries
// data.kind == kind (e.g. "daily_briefing"), newest first. Uses SQLite's
// json_extract, so only insights written with a JSON `data` object match.
func (s *Store) InsightsByKind(ctx context.Context, kind string, limit int) ([]md.Insight, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.scope, i.symbol_id, i.ts, i.headline, i.body, i.data,
		       COALESCE(sym.symbol, '')
		FROM insights i LEFT JOIN symbols sym ON sym.id = i.symbol_id
		WHERE json_extract(i.data, '$.kind') = ?
		ORDER BY i.ts DESC, i.id DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Insight
	for rows.Next() {
		var in md.Insight
		var sid sql.NullInt64
		if err := rows.Scan(&in.ID, &in.Scope, &sid, &in.Ts, &in.Headline, &in.Body, &in.Data, &in.Symbol); err != nil {
			return nil, err
		}
		if sid.Valid {
			in.SymbolID = &sid.Int64
		}
		out = append(out, in)
	}
	return out, rows.Err()
}
