// Decision Engine (Layer 1) persistence: the EV gate's decision ledger.
//
// Every verdict internal/ev renders on a candidate — BUY, SELL, and every
// DO_NOTHING — is appended here so a refusal is a queryable record, not an
// entry that silently never happened. NetEV is a pointer: nil means the value
// was not measurable, and storing NULL instead of 0 is the same fail-closed
// honesty rule the rest of the store follows.
//
// Reads use the pooled s.db handle; writes go through s.w, matching the rest
// of the store's write discipline.
package store

import (
	"context"
	"database/sql"
)

// EVDecision is one row of the Decision Engine's ledger.
type EVDecision struct {
	Seq        int64    `json:"seq"`
	Ts         int64    `json:"ts"`
	Strategy   string   `json:"strategy"`
	SymbolID   int64    `json:"-"`
	Symbol     string   `json:"symbol"`
	Horizon    string   `json:"horizon"`
	Decision   string   `json:"decision"` // BUY | SELL | DO_NOTHING
	Reason     string   `json:"reason"`   // enumerated ev.Reason label
	NetEV      *float64 `json:"netEV"`    // nil = unmeasurable, never a silent zero
	Rank       int      `json:"rank"`     // 1-based net-EV rank within the pass (0 = unranked)
	RankOf     int      `json:"rankOf"`
	InputsJSON string   `json:"inputs"` // full ev.Assessment snapshot incl. has-flags
}

// InsertEVDecision appends one decision. APPEND-ONLY by convention: a decision,
// once rendered, is history — corrections are new rows, not edits.
func (s *Store) InsertEVDecision(ctx context.Context, d EVDecision) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO ev_decisions
		  (ts, strategy, symbol_id, symbol, horizon, decision, reason, net_ev, rank, rank_of, inputs_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		d.Ts, d.Strategy, d.SymbolID, d.Symbol, d.Horizon, d.Decision, d.Reason,
		d.NetEV, d.Rank, d.RankOf, d.InputsJSON)
	return err
}

// EVDecisions returns recent decisions, newest first. decision and symbol
// filters are optional ("" = all); limit defaults to 200.
func (s *Store) EVDecisions(ctx context.Context, decision, symbol string, limit int) ([]EVDecision, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, ts, strategy, symbol_id, symbol, horizon, decision, reason,
		       net_ev, rank, rank_of, inputs_json
		FROM ev_decisions
		WHERE (?='' OR decision=?) AND (?='' OR symbol=?)
		ORDER BY seq DESC
		LIMIT ?`, decision, decision, symbol, symbol, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []EVDecision
	for rows.Next() {
		var d EVDecision
		var nev sql.NullFloat64
		if err := rows.Scan(&d.Seq, &d.Ts, &d.Strategy, &d.SymbolID, &d.Symbol,
			&d.Horizon, &d.Decision, &d.Reason, &nev, &d.Rank, &d.RankOf, &d.InputsJSON); err != nil {
			return nil, err
		}
		if nev.Valid {
			v := nev.Float64
			d.NetEV = &v
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
