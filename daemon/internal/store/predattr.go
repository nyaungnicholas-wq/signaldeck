// Prediction attribution (Layer 6) — persistence for the top-N named parts of
// one ledgered prediction's RAW blended probability. Each part is a
// probability delta from the neutral 0.5 prior (see internal/ensemble
// AttributeProbability for the decomposition contract); only the top-N by
// |contribution| are stored, so the persisted rows EXPLAIN the prediction but
// do not necessarily sum to raw_prob-0.5 (truncation is honest and recorded by
// the fixed N at write time).
package store

import (
	"context"
	"database/sql"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// AttributionMethodSaabas labels rows whose GBM feature parts derive from
// Saabas path attribution (an approximation with a disclosed bias for deep
// interactions — NOT exact SHAP). Component/leg parts under the same method
// label are exact by algebra; the label records the weakest link.
const AttributionMethodSaabas = "saabas"

// PredictionAttribution is one persisted part of one ledgered prediction.
type PredictionAttribution struct {
	LedgerSeq    int64      `json:"ledgerSeq"`
	Rank         int        `json:"rank"` // 0 = largest |contribution|
	SymbolID     int64      `json:"-"`
	Horizon      md.Horizon `json:"horizon"`
	Ts           int64      `json:"ts"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"` // component | gbm_feature | leg
	Contribution float64    `json:"contribution"`
	Method       string     `json:"method"`
}

// InsertPredictionAttributions writes the parts for one ledger seq in rank
// order (idempotent: re-writing a seq replaces its rows). Empty parts are a
// no-op.
func (s *Store) InsertPredictionAttributions(ctx context.Context, rows []PredictionAttribution) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM prediction_attributions WHERE ledger_seq=?`, rows[0].LedgerSeq); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO prediction_attributions
			  (ledger_seq, rank, symbol_id, horizon, ts, name, kind, contribution, method)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			r.LedgerSeq, r.Rank, r.SymbolID, string(r.Horizon), r.Ts,
			r.Name, r.Kind, r.Contribution, r.Method); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PredictionAttributions returns the stored parts for one ledger seq in rank
// order (empty when none were persisted for that seq).
func (s *Store) PredictionAttributions(ctx context.Context, ledgerSeq int64) ([]PredictionAttribution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ledger_seq, rank, symbol_id, horizon, ts, name, kind, contribution, method
		FROM prediction_attributions WHERE ledger_seq=? ORDER BY rank`, ledgerSeq)
	if err != nil {
		return nil, err
	}
	return scanPredAttrs(rows)
}

// LatestAttributedSeq returns the newest ledger seq for a symbol+horizon that
// has persisted attribution parts. ok=false when none exist.
func (s *Store) LatestAttributedSeq(ctx context.Context, symbolID int64, h md.Horizon) (int64, bool, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `
		SELECT ledger_seq FROM prediction_attributions
		WHERE symbol_id=? AND horizon=? ORDER BY ledger_seq DESC LIMIT 1`,
		symbolID, string(h)).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func scanPredAttrs(rows *sql.Rows) ([]PredictionAttribution, error) {
	defer rows.Close() //nolint:errcheck
	var out []PredictionAttribution
	for rows.Next() {
		var r PredictionAttribution
		var h string
		if err := rows.Scan(&r.LedgerSeq, &r.Rank, &r.SymbolID, &h, &r.Ts,
			&r.Name, &r.Kind, &r.Contribution, &r.Method); err != nil {
			return nil, err
		}
		r.Horizon = md.Horizon(h)
		out = append(out, r)
	}
	return out, rows.Err()
}
