// Recommendation audit trail: an append-only, hash-chained record of every
// distinct recommendation the Desk produces — the "transparent audit trail"
// surface. Each entry commits, at generation time, the recommendation's
// identity and the exact inputs that produced it:
//
//	content_hash = sha256(canonical(decision ‖ confidence ‖ sources ‖ versions ‖ assumptions ‖ input digest))
//	entry_hash   = sha256(prev_hash ‖ canonical(entry fields))
//
// content_hash makes a recommendation REPRODUCIBLE: rebuild it from the same
// stored inputs and the digest matches. entry_hash chains the entries so any
// silent mutation of a historical record is detectable (same tamper-evidence as
// the prediction ledger). Rows are WRITE-ONCE — this file only ever INSERTs.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// RecAuditEntry is one recommendation committed to the append-only chain. Seq,
// PrevHash and EntryHash are chain-managed (assigned by AppendRecAudit); callers
// fill the identity + content fields. Sources/ModelVersions/Assumptions are
// stored as pre-serialized JSON strings (the API layer owns their shape).
type RecAuditEntry struct {
	Seq           int64     `json:"seq"`
	CreatedAt     int64     `json:"createdAt"`
	SymbolID      int64     `json:"symbolId"`
	Symbol        string    `json:"symbol"`
	Market        md.Market `json:"market"`
	Decision      string    `json:"decision"`
	Confidence    string    `json:"confidence"`
	ContentHash   string    `json:"contentHash"`   // reproducibility digest of the rec + inputs
	SourcesJSON   string    `json:"sources"`       // JSON array of data sources used
	VersionsJSON  string    `json:"modelVersions"` // JSON object of model versions
	AssumptionsJSON string  `json:"assumptions"`   // JSON array of stated assumptions
	PrevHash      string    `json:"prevHash"`
	EntryHash     string    `json:"entryHash"`
}

// canonicalRecAuditPayload is the byte-stable serialization hashed into the
// chain link. Fixed field order + stable formatting (NOT encoding/json map
// ordering). prev_hash is prepended by hashRecAudit, not included here.
func canonicalRecAuditPayload(e RecAuditEntry) string {
	var b []byte
	b = append(b, "created_at="...)
	b = strconv.AppendInt(b, e.CreatedAt, 10)
	b = append(b, "|symbol_id="...)
	b = strconv.AppendInt(b, e.SymbolID, 10)
	b = append(b, "|symbol="...)
	b = append(b, e.Symbol...)
	b = append(b, "|market="...)
	b = append(b, string(e.Market)...)
	b = append(b, "|decision="...)
	b = append(b, e.Decision...)
	b = append(b, "|confidence="...)
	b = append(b, e.Confidence...)
	b = append(b, "|content_hash="...)
	b = append(b, e.ContentHash...)
	b = append(b, "|sources="...)
	b = append(b, e.SourcesJSON...)
	b = append(b, "|versions="...)
	b = append(b, e.VersionsJSON...)
	b = append(b, "|assumptions="...)
	b = append(b, e.AssumptionsJSON...)
	return string(b)
}

// hashRecAudit computes entry_hash = sha256(prev_hash ‖ canonical payload) —
// the single source of truth for the chain link.
func hashRecAudit(prevHash string, e RecAuditEntry) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte("\x1e"))
	h.Write([]byte(canonicalRecAuditPayload(e)))
	return hex.EncodeToString(h.Sum(nil))
}

// RecAuditContentHash hashes the fields that define a recommendation's identity
// for reproducibility. Passing the same decision/confidence/inputDigest yields
// the same hash, so a freshly-rebuilt rec can be checked byte-for-byte against
// what was recorded. inputDigest is a caller-built stable string of the numeric
// inputs (score, edge, price, eps, …).
func RecAuditContentHash(decision, confidence, inputDigest string) string {
	h := sha256.New()
	h.Write([]byte(decision))
	h.Write([]byte("\x1f"))
	h.Write([]byte(confidence))
	h.Write([]byte("\x1f"))
	h.Write([]byte(inputDigest))
	return hex.EncodeToString(h.Sum(nil))
}

// AppendRecAudit appends one entry to the chain (single-writer conn, one tx —
// strictly linear like the prediction ledger). It DEDUPES: if the symbol's most
// recent entry has the identical content_hash, nothing is appended and that
// existing head entry is returned (appended=false) — so polling an unchanged
// recommendation never grows the chain.
func (s *Store) AppendRecAudit(ctx context.Context, e RecAuditEntry) (RecAuditEntry, bool, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return e, false, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Dedup: latest entry for this symbol with the same content_hash → reuse.
	var existing RecAuditEntry
	err = tx.QueryRowContext(ctx, `
		SELECT seq, created_at, symbol_id, symbol, market, decision, confidence,
		       content_hash, sources, model_versions, assumptions, prev_hash, entry_hash
		FROM recommendation_audit
		WHERE symbol_id=? ORDER BY seq DESC LIMIT 1`, e.SymbolID).Scan(
		&existing.Seq, &existing.CreatedAt, &existing.SymbolID, &existing.Symbol, &existing.Market,
		&existing.Decision, &existing.Confidence, &existing.ContentHash, &existing.SourcesJSON,
		&existing.VersionsJSON, &existing.AssumptionsJSON, &existing.PrevHash, &existing.EntryHash)
	if err != nil && err != sql.ErrNoRows {
		return e, false, err
	}
	if err == nil && existing.ContentHash == e.ContentHash {
		return existing, false, nil // unchanged recommendation — no new row
	}

	var prevHash string
	err = tx.QueryRowContext(ctx, `
		SELECT entry_hash FROM recommendation_audit ORDER BY seq DESC LIMIT 1`).Scan(&prevHash)
	if err != nil && err != sql.ErrNoRows {
		return e, false, err
	}
	e.PrevHash = prevHash
	e.EntryHash = hashRecAudit(prevHash, e)

	res, err := tx.ExecContext(ctx, `
		INSERT INTO recommendation_audit
		  (created_at, symbol_id, symbol, market, decision, confidence,
		   content_hash, sources, model_versions, assumptions, prev_hash, entry_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.CreatedAt, e.SymbolID, e.Symbol, string(e.Market), e.Decision, e.Confidence,
		e.ContentHash, e.SourcesJSON, e.VersionsJSON, e.AssumptionsJSON, e.PrevHash, e.EntryHash)
	if err != nil {
		return e, false, err
	}
	if seq, err := res.LastInsertId(); err == nil {
		e.Seq = seq
	}
	if err := tx.Commit(); err != nil {
		return e, false, err
	}
	return e, true, nil
}

// LatestRecAuditForSymbol returns the most recent audit entry for a symbol.
func (s *Store) LatestRecAuditForSymbol(ctx context.Context, symbolID int64) (RecAuditEntry, bool, error) {
	var e RecAuditEntry
	var mkt string
	err := s.db.QueryRowContext(ctx, `
		SELECT seq, created_at, symbol_id, symbol, market, decision, confidence,
		       content_hash, sources, model_versions, assumptions, prev_hash, entry_hash
		FROM recommendation_audit
		WHERE symbol_id=? ORDER BY seq DESC LIMIT 1`, symbolID).Scan(
		&e.Seq, &e.CreatedAt, &e.SymbolID, &e.Symbol, &mkt, &e.Decision, &e.Confidence,
		&e.ContentHash, &e.SourcesJSON, &e.VersionsJSON, &e.AssumptionsJSON, &e.PrevHash, &e.EntryHash)
	if err == sql.ErrNoRows {
		return e, false, nil
	}
	if err != nil {
		return e, false, err
	}
	e.Market = md.Market(mkt)
	return e, true, nil
}

// RecAuditVerification is the result of walking the whole audit chain.
type RecAuditVerification struct {
	Intact      bool   `json:"intact"`
	Count       int64  `json:"count"`
	HeadHash    string `json:"headHash"`
	BrokenAtSeq *int64 `json:"brokenAtSeq,omitempty"`
}

// VerifyRecAudit walks the chain seq-ascending, recomputing each entry_hash from
// the previous row's hash and the row's own fields — tamper-evidence identical
// to VerifyLedger. An empty chain is trivially intact.
func (s *Store) VerifyRecAudit(ctx context.Context) (RecAuditVerification, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, created_at, symbol_id, symbol, market, decision, confidence,
		       content_hash, sources, model_versions, assumptions, prev_hash, entry_hash
		FROM recommendation_audit ORDER BY seq ASC`)
	if err != nil {
		return RecAuditVerification{}, err
	}
	defer rows.Close() //nolint:errcheck

	v := RecAuditVerification{Intact: true}
	running := ""
	for rows.Next() {
		var e RecAuditEntry
		var mkt string
		if err := rows.Scan(&e.Seq, &e.CreatedAt, &e.SymbolID, &e.Symbol, &mkt, &e.Decision,
			&e.Confidence, &e.ContentHash, &e.SourcesJSON, &e.VersionsJSON, &e.AssumptionsJSON,
			&e.PrevHash, &e.EntryHash); err != nil {
			return v, err
		}
		e.Market = md.Market(mkt)
		want := hashRecAudit(running, e)
		if e.PrevHash != running || e.EntryHash != want {
			if v.BrokenAtSeq == nil {
				seq := e.Seq
				v.BrokenAtSeq = &seq
			}
			v.Intact = false
		}
		running = e.EntryHash
		v.Count++
	}
	v.HeadHash = running
	return v, rows.Err()
}
