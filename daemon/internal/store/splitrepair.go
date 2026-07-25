// Persistence for the split-corruption repair worker (2026-07-24).
//
// The log is the audit trail: which symbol, which split, how many
// discontinuities its series carried, whether the re-backfill succeeded. Kept
// so a repair that silently fails to clear the corruption is visible as a
// repeated row rather than an invisible retry loop.
package store

import "context"

// SplitRepairRow is one recorded repair attempt.
type SplitRepairRow struct {
	SymbolID   int64  `json:"symbolId"`
	Symbol     string `json:"symbol"`
	SplitDate  string `json:"splitDate"`
	Ratio      string `json:"ratio"`
	Suspects   int    `json:"suspects"`
	OK         bool   `json:"ok"`
	Err        string `json:"err"`
	RepairedAt int64  `json:"repairedAt"`
}

// RecordSplitRepair appends one attempt.
func (s *Store) RecordSplitRepair(ctx context.Context, symbolID int64,
	splitDate, ratio string, suspects int, ok bool, errMsg string, ts int64) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO split_repairs
		  (symbol_id, split_date, ratio, suspects, ok, err, repaired_at)
		VALUES (?,?,?,?,?,?,?)`,
		symbolID, splitDate, ratio, suspects, boolToInt(ok), errMsg, ts)
	return err
}

// LastSplitRepair returns when this symbol was last attempted.
func (s *Store) LastSplitRepair(ctx context.Context, symbolID int64) (int64, bool, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(repaired_at) FROM split_repairs WHERE symbol_id=?`, symbolID).Scan(&ts)
	if err != nil || ts == 0 {
		return 0, false, nil // no row is not an error — it means never attempted
	}
	return ts, true, nil
}

// SplitRepairs returns the most recent attempts, newest first.
func (s *Store) SplitRepairs(ctx context.Context, limit int) ([]SplitRepairRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.symbol_id, sy.symbol, r.split_date, r.ratio, r.suspects,
		       r.ok, r.err, r.repaired_at
		FROM split_repairs r JOIN symbols sy ON sy.id = r.symbol_id
		ORDER BY r.repaired_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []SplitRepairRow
	for rows.Next() {
		var v SplitRepairRow
		var okInt int
		if err := rows.Scan(&v.SymbolID, &v.Symbol, &v.SplitDate, &v.Ratio,
			&v.Suspects, &okInt, &v.Err, &v.RepairedAt); err != nil {
			return nil, err
		}
		v.OK = okInt == 1
		out = append(out, v)
	}
	return out, rows.Err()
}
