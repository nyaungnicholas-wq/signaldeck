// Prediction ledger (Stage 3): an append-only, hash-chained audit log.
//
// Every flagship prediction appends exactly one ledger row committing its
// identity (symbol, horizon, bar, raw/cal probability, feature-vector hash,
// model version) at emit time — BEFORE any outcome can exist. Each row is
// cryptographically chained to the previous:
//
//	entry_hash = sha256( prev_hash ‖ canonical-json(entry fields) )
//
// where canonical-json is a byte-stable, fixed-field-order encoding (NOT
// encoding/json map ordering) so the same logical entry always hashes to the
// same digest on any machine. The chain gives tamper-evidence: recomputing it
// reproduces every head, and any silent UPDATE/DELETE of a historical row
// breaks the recomputation at that exact seq. Rows are WRITE-ONCE — this file
// only ever INSERTs (append), never UPDATE/REPLACE/DELETE.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// LedgerEntry is one prediction committed to the append-only chain. seq,
// PrevHash and EntryHash are assigned by AppendLedger (chain-managed); callers
// fill only the prediction-identity fields.
type LedgerEntry struct {
	Seq          int64      `json:"seq"`
	PredictedAt  int64      `json:"predictedAt"`  // wall-clock unix seconds
	SymbolID     int64      `json:"symbolId"`     // 0 allowed but always set in practice
	Horizon      md.Horizon `json:"horizon"`      // 1d | 1w
	BarTs        int64      `json:"barTs"`        // prediction's bar timestamp
	RawProb      float64    `json:"rawProb"`      // uncalibrated ensemble probability
	CalProb      float64    `json:"calProb"`      // calibrated probability
	FeatureHash  string     `json:"featureHash"`  // sha256 of the persisted feature-vector JSON
	ModelVersion int        `json:"modelVersion"` // ledger model version at emit time
	PrevHash     string     `json:"prevHash"`     // entry_hash of seq-1 ("" for genesis)
	EntryHash    string     `json:"entryHash"`    // the chain link
}

// HashFeatureVector returns the sha256 (hex) of a feature vector's canonical
// JSON. It re-encodes with the SAME canonical rule the ledger's entry hash uses
// (sorted keys, stable float formatting) so the feature_hash is reproducible
// from the persisted vector regardless of Go map iteration order.
func HashFeatureVector(vec map[string]float64) string {
	sum := sha256.Sum256([]byte(canonicalFeatureJSON(vec)))
	return hex.EncodeToString(sum[:])
}

// canonicalFeatureJSON encodes a name->float map with keys in ascending order
// and each value via strconv.FormatFloat(v,'g',-1,64) — the shortest exact
// round-trip form, identical across platforms. Deterministic by construction.
func canonicalFeatureJSON(vec map[string]float64) string {
	keys := make([]string, 0, len(vec))
	for k := range vec {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var b []byte
	b = append(b, '{')
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		kb, _ := json.Marshal(k) // JSON-escape the key
		b = append(b, kb...)
		b = append(b, ':')
		b = append(b, strconv.FormatFloat(vec[k], 'g', -1, 64)...)
	}
	b = append(b, '}')
	return string(b)
}

// sortStrings is a tiny insertion sort (no new import; slices are small feature
// maps) giving a stable ascending order for the canonical encoding.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// canonicalEntryPayload is the byte-stable serialization of the entry's
// identity fields that gets hashed. Fixed field order + the same float
// formatting as the feature hash — NOT encoding/json (whose map/struct handling
// we avoid for determinism guarantees). prev_hash is prepended by the caller,
// not included here, so the payload describes only THIS entry.
func canonicalEntryPayload(e LedgerEntry) string {
	var b []byte
	b = append(b, "predicted_at="...)
	b = strconv.AppendInt(b, e.PredictedAt, 10)
	b = append(b, "|symbol_id="...)
	b = strconv.AppendInt(b, e.SymbolID, 10)
	b = append(b, "|horizon="...)
	b = append(b, e.Horizon...)
	b = append(b, "|bar_ts="...)
	b = strconv.AppendInt(b, e.BarTs, 10)
	b = append(b, "|raw_prob="...)
	b = strconv.AppendFloat(b, e.RawProb, 'g', -1, 64)
	b = append(b, "|cal_prob="...)
	b = strconv.AppendFloat(b, e.CalProb, 'g', -1, 64)
	b = append(b, "|feature_hash="...)
	b = append(b, e.FeatureHash...)
	b = append(b, "|model_version="...)
	b = strconv.AppendInt(b, int64(e.ModelVersion), 10)
	return string(b)
}

// hashEntry computes entry_hash = sha256(prev_hash ‖ canonical payload). This is
// the single source of truth for the chain link — AppendLedger and VerifyLedger
// both call it, so a tamper is caught iff the stored hash disagrees with a fresh
// recomputation.
func hashEntry(prevHash string, e LedgerEntry) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte("\x1e")) // record separator between prev_hash and payload
	h.Write([]byte(canonicalEntryPayload(e)))
	return hex.EncodeToString(h.Sum(nil))
}

// AppendLedger appends one entry to the chain and returns it with its assigned
// seq, PrevHash and EntryHash filled in. It reads the current head and inserts
// in ONE transaction on the single-writer connection (s.w, MaxOpenConns=1), so
// no two appends can interleave between head-read and insert — the chain stays
// strictly linear. Never updates or replaces an existing row.
func (s *Store) AppendLedger(ctx context.Context, e LedgerEntry) (LedgerEntry, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return e, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Current head: prev_hash is the entry_hash of the highest seq ("" if empty).
	var prevHash string
	err = tx.QueryRowContext(ctx, `
		SELECT entry_hash FROM prediction_ledger ORDER BY seq DESC LIMIT 1`).Scan(&prevHash)
	if err != nil && err != sql.ErrNoRows {
		return e, err
	}
	e.PrevHash = prevHash
	e.EntryHash = hashEntry(prevHash, e)

	res, err := tx.ExecContext(ctx, `
		INSERT INTO prediction_ledger
		  (predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob,
		   feature_hash, model_version, prev_hash, entry_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.PredictedAt, e.SymbolID, string(e.Horizon), e.BarTs, e.RawProb, e.CalProb,
		e.FeatureHash, e.ModelVersion, e.PrevHash, e.EntryHash)
	if err != nil {
		return e, err
	}
	if seq, err := res.LastInsertId(); err == nil {
		e.Seq = seq
	}
	if err := tx.Commit(); err != nil {
		return e, err
	}
	return e, nil
}

// LedgerVerification is the result of walking the whole chain.
type LedgerVerification struct {
	Intact      bool   `json:"intact"`               // true iff every recomputed hash matches
	Count       int64  `json:"count"`                // rows examined
	HeadHash    string `json:"headHash"`             // entry_hash of the last row ("" if empty)
	BrokenAtSeq *int64 `json:"brokenAtSeq,omitempty"` // first seq whose stored/linkage hash disagrees
}

// VerifyLedger walks the ledger seq-ascending, recomputing each entry_hash from
// the previous row's hash and the row's own fields. It returns intact=false with
// BrokenAtSeq set to the FIRST seq where either (a) the stored prev_hash does
// not equal the running chain head, or (b) the recomputed entry_hash does not
// equal the stored one. Either failure means a historical row was mutated,
// deleted (breaking the prev_hash linkage), or reordered. An empty ledger is
// trivially intact.
func (s *Store) VerifyLedger(ctx context.Context) (LedgerVerification, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob,
		       feature_hash, model_version, prev_hash, entry_hash
		FROM prediction_ledger ORDER BY seq ASC`)
	if err != nil {
		return LedgerVerification{}, err
	}
	defer rows.Close() //nolint:errcheck

	res := LedgerVerification{Intact: true}
	running := "" // expected prev_hash of the next row (genesis links to "")
	for rows.Next() {
		var e LedgerEntry
		var hz string
		var symID sql.NullInt64
		if err := rows.Scan(&e.Seq, &e.PredictedAt, &symID, &hz, &e.BarTs,
			&e.RawProb, &e.CalProb, &e.FeatureHash, &e.ModelVersion,
			&e.PrevHash, &e.EntryHash); err != nil {
			return LedgerVerification{}, err
		}
		e.SymbolID = symID.Int64
		e.Horizon = md.Horizon(hz)

		want := hashEntry(running, e)
		if e.PrevHash != running || e.EntryHash != want {
			seq := e.Seq
			res.Intact = false
			res.BrokenAtSeq = &seq
			// Stop at the first break: everything after it is untrustworthy.
			// Still report count of rows examined up to and including the break.
			res.Count++
			res.HeadHash = e.EntryHash
			return res, rows.Err()
		}
		running = e.EntryHash
		res.Count++
		res.HeadHash = e.EntryHash
	}
	return res, rows.Err()
}

// LedgerHead returns the newest ledger entry (highest seq). ok=false on an
// empty ledger.
func (s *Store) LedgerHead(ctx context.Context) (LedgerEntry, bool, error) {
	var e LedgerEntry
	var hz string
	var symID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT seq, predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob,
		       feature_hash, model_version, prev_hash, entry_hash
		FROM prediction_ledger ORDER BY seq DESC LIMIT 1`).
		Scan(&e.Seq, &e.PredictedAt, &symID, &hz, &e.BarTs, &e.RawProb, &e.CalProb,
			&e.FeatureHash, &e.ModelVersion, &e.PrevHash, &e.EntryHash)
	if err == sql.ErrNoRows {
		return e, false, nil
	}
	if err != nil {
		return e, false, err
	}
	e.SymbolID = symID.Int64
	e.Horizon = md.Horizon(hz)
	return e, true, nil
}

// LedgerFor returns the most recent ledger entries for one symbol+horizon,
// newest first, capped at limit (limit<=0 → default 100). Read-only.
func (s *Store) LedgerFor(ctx context.Context, symbolID int64, h md.Horizon, limit int) ([]LedgerEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob,
		       feature_hash, model_version, prev_hash, entry_hash
		FROM prediction_ledger
		WHERE symbol_id=? AND horizon=?
		ORDER BY seq DESC LIMIT ?`, symbolID, string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		var hz string
		var symID sql.NullInt64
		if err := rows.Scan(&e.Seq, &e.PredictedAt, &symID, &hz, &e.BarTs,
			&e.RawProb, &e.CalProb, &e.FeatureHash, &e.ModelVersion,
			&e.PrevHash, &e.EntryHash); err != nil {
			return nil, err
		}
		e.SymbolID = symID.Int64
		e.Horizon = md.Horizon(hz)
		out = append(out, e)
	}
	return out, rows.Err()
}
