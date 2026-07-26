// Pre-registration chain persistence — see internal/prereg for why this exists.
//
// The write path mirrors the prediction ledger's: read the chain head and
// insert in ONE transaction on the single-writer connection, so two appends
// cannot interleave between head-read and insert and the chain stays strictly
// linear. Rows are never updated.
package store

import (
	"context"
	"database/sql"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
)

// AppendPrereg appends one pre-registration record, filling in Seq, PrevHash
// and EntryHash. The caller supplies Ts, Kind, SpecJSON, SpecHash and Note.
func (s *Store) AppendPrereg(ctx context.Context, r prereg.Record) (prereg.Record, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback() //nolint:errcheck

	var prev sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT entry_hash FROM prereg_records ORDER BY seq DESC LIMIT 1`).Scan(&prev); err != nil && err != sql.ErrNoRows {
		return r, err
	}
	r.PrevHash = prev.String // "" for the genesis record, as in the ledger
	r.EntryHash = prereg.HashEntry(r.PrevHash, r)

	res, err := tx.ExecContext(ctx, `
		INSERT INTO prereg_records (ts, kind, spec_json, spec_hash, prev_hash, entry_hash, note)
		VALUES (?,?,?,?,?,?,?)`,
		r.Ts, r.Kind, r.SpecJSON, r.SpecHash, r.PrevHash, r.EntryHash, r.Note)
	if err != nil {
		return r, err
	}
	if id, err := res.LastInsertId(); err == nil {
		r.Seq = id
	}
	return r, tx.Commit()
}

// PreregRecords returns the whole chain, oldest first.
func (s *Store) PreregRecords(ctx context.Context) ([]prereg.Record, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, ts, kind, spec_json, spec_hash, prev_hash, entry_hash, note
		FROM prereg_records ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []prereg.Record
	for rows.Next() {
		var r prereg.Record
		if err := rows.Scan(&r.Seq, &r.Ts, &r.Kind, &r.SpecJSON, &r.SpecHash,
			&r.PrevHash, &r.EntryHash, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PreregKinds returns the set of kinds already registered, so the registrar can
// append only what is missing.
func (s *Store) PreregKinds(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT kind FROM prereg_records`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// VerifyPrereg recomputes every link and reports the first break, or ok=true.
// A break means a stored claim was altered after the fact — which is the exact
// thing pre-registration exists to make impossible to do quietly.
func (s *Store) VerifyPrereg(ctx context.Context) (ok bool, brokenAtSeq int64, err error) {
	recs, err := s.PreregRecords(ctx)
	if err != nil {
		return false, 0, err
	}
	prev := ""
	for _, r := range recs {
		if r.PrevHash != prev {
			return false, r.Seq, nil
		}
		if prereg.HashEntry(prev, r) != r.EntryHash {
			return false, r.Seq, nil
		}
		prev = r.EntryHash
	}
	return true, 0, nil
}

// LatestPreregHashes returns each kind's NEWEST stored spec hash.
//
// The registrar needs it to tell two states apart that look identical from a
// bare kind list: a claim already registered and unchanged (a genuine no-op),
// and a claim already registered whose text has since changed in code. The
// second is not a no-op — it means the chain and the code now disagree, and the
// honest response is an appended amendment, never a silent divergence.
func (s *Store) LatestPreregHashes(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, spec_hash FROM prereg_records
		WHERE seq IN (SELECT MAX(seq) FROM prereg_records GROUP BY kind)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]string{}
	for rows.Next() {
		var k, h string
		if err := rows.Scan(&k, &h); err != nil {
			return nil, err
		}
		out[k] = h
	}
	return out, rows.Err()
}
