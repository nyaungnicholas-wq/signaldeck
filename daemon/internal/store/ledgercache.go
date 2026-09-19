// ═══ COLD-LOAD PRECOMPUTE WAVE — incremental ledger verification (appended) ═══
//
// VerifyLedger walks the ENTIRE hash chain (~200k rows, measured ~12s) on
// every /api/track-record and /api/ledger/verify request. Hash chains verify
// incrementally by design: once rows 1..K are proven intact, a later check
// only needs to (a) confirm the row at seq K still carries the hash recorded
// at proof time — the tamper anchor — and (b) walk the suffix K+1..head.
//
// VerifyLedgerCached does exactly that, checkpointing each intact result in
// the meta table keyed by (last row seq + count):
//
//   - checkpoint row missing, its stored hash differing from the anchored
//     one, or the row count up to the checkpoint differing from the recorded
//     count ⇒ TAMPER SIGNAL: a full re-verify runs, and the result is forced
//     intact=false even if the rewritten chain is internally consistent — a
//     consistent rewrite is precisely what the external anchor exists to
//     catch (the chain alone cannot).
//   - otherwise only rows after the checkpoint are re-hashed and the
//     checkpoint advances.
//
// Known limit (documented, not hidden): a row mutated BEFORE the checkpoint
// without touching the checkpoint row's hash leaves the chain internally
// broken but is only caught by a full walk; any rewrite that keeps the chain
// consistent MUST change every downstream hash including the anchor, so the
// realistic tamper path is caught.
package store

import (
	"context"
	"database/sql"
	"encoding/json"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// metaLedgerVerifyCkpt stores the last intact verification checkpoint.
const metaLedgerVerifyCkpt = "ledger_verify_checkpoint"

// ledgerCkpt is the persisted checkpoint: the head row proven intact, how many
// rows the intact prefix held, and that head's entry hash (the anchor).
type ledgerCkpt struct {
	Seq   int64  `json:"seq"`
	Count int64  `json:"count"`
	Hash  string `json:"hash"`
}

// VerifyLedgerCached is VerifyLedger with the incremental checkpoint contract
// above. The returned verification is byte-for-byte what a full walk would
// report on an untampered chain; fullWalk reports whether the slow path ran.
func (s *Store) VerifyLedgerCached(ctx context.Context) (LedgerVerification, bool, error) {
	raw, err := s.GetMeta(ctx, metaLedgerVerifyCkpt)
	if err != nil {
		return LedgerVerification{}, false, err
	}
	var ck ledgerCkpt
	if raw == "" || json.Unmarshal([]byte(raw), &ck) != nil || ck.Seq <= 0 {
		return s.verifyFullAndCheckpoint(ctx, false)
	}

	// Anchor check: the checkpoint row must still exist with the recorded hash,
	// and the prefix row count must still match — either mismatch is tamper.
	var storedHash string
	err = s.db.QueryRowContext(ctx,
		`SELECT entry_hash FROM prediction_ledger WHERE seq=?`, ck.Seq).Scan(&storedHash)
	if err == sql.ErrNoRows {
		return s.verifyFullAndCheckpoint(ctx, true)
	}
	if err != nil {
		return LedgerVerification{}, false, err
	}
	var prefixCount int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM prediction_ledger WHERE seq<=?`, ck.Seq).Scan(&prefixCount); err != nil {
		return LedgerVerification{}, false, err
	}
	if storedHash != ck.Hash || prefixCount != ck.Count {
		return s.verifyFullAndCheckpoint(ctx, true)
	}

	// Suffix walk: rows after the checkpoint, chained from the anchored hash.
	res := LedgerVerification{Intact: true, Count: ck.Count, HeadHash: ck.Hash}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob,
		       feature_hash, model_version, prev_hash, entry_hash
		FROM prediction_ledger WHERE seq>? ORDER BY seq ASC`, ck.Seq)
	if err != nil {
		return LedgerVerification{}, false, err
	}
	defer rows.Close() //nolint:errcheck
	running := ck.Hash
	for rows.Next() {
		var e LedgerEntry
		var hz string
		var symID sql.NullInt64
		if err := rows.Scan(&e.Seq, &e.PredictedAt, &symID, &hz, &e.BarTs,
			&e.RawProb, &e.CalProb, &e.FeatureHash, &e.ModelVersion,
			&e.PrevHash, &e.EntryHash); err != nil {
			return LedgerVerification{}, false, err
		}
		e.SymbolID = symID.Int64
		e.Horizon = md.Horizon(hz)
		want := hashEntry(running, e)
		if e.PrevHash != running || e.EntryHash != want {
			seq := e.Seq
			res.Intact = false
			res.BrokenAtSeq = &seq
			res.Count++
			res.HeadHash = e.EntryHash
			return res, false, rows.Err() // broken suffix: no checkpoint advance
		}
		running = e.EntryHash
		res.Count++
		res.HeadHash = e.EntryHash
	}
	if err := rows.Err(); err != nil {
		return LedgerVerification{}, false, err
	}
	if err := s.saveLedgerCkpt(ctx, res); err != nil {
		return LedgerVerification{}, false, err
	}
	return res, false, nil
}

// verifyFullAndCheckpoint runs the full chain walk. tamperSignal marks that the
// stored checkpoint anchor no longer matched: in that case the result is
// forced intact=false (broken at the checkpoint seq when the walk itself found
// nothing) — an anchor mismatch is tamper evidence even when the rewritten
// chain is internally consistent.
func (s *Store) verifyFullAndCheckpoint(ctx context.Context, tamperSignal bool) (LedgerVerification, bool, error) {
	res, err := s.VerifyLedger(ctx)
	if err != nil {
		return res, true, err
	}
	if tamperSignal && res.Intact {
		res.Intact = false
		// The anchor row is where the divergence was detected.
		var ck ledgerCkpt
		if raw, gerr := s.GetMeta(ctx, metaLedgerVerifyCkpt); gerr == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &ck)
		}
		seq := ck.Seq
		res.BrokenAtSeq = &seq
		return res, true, nil // never checkpoint a tampered state
	}
	if res.Intact && res.Count > 0 {
		if err := s.saveLedgerCkpt(ctx, res); err != nil {
			return res, true, err
		}
	}
	return res, true, nil
}

// saveLedgerCkpt persists an intact verification as the new checkpoint. The
// head seq is read back from the table (LedgerVerification carries the head
// hash and count; seq of the head row completes the (seq, count, hash) key).
func (s *Store) saveLedgerCkpt(ctx context.Context, res LedgerVerification) error {
	if !res.Intact || res.Count == 0 {
		return nil
	}
	head, ok, err := s.LedgerHead(ctx)
	if err != nil || !ok {
		return err
	}
	if head.EntryHash != res.HeadHash {
		return nil // the chain grew mid-verify — the next call re-checks
	}
	b, err := json.Marshal(ledgerCkpt{Seq: head.Seq, Count: res.Count, Hash: res.HeadHash})
	if err != nil {
		return err
	}
	// NOTHING CHANGED, SO WRITE NOTHING. SetMeta goes through the SINGLE WORKER
	// connection, and the fleet keeps it busy: measured 2026-09-16, three
	// consecutive BEGIN IMMEDIATE attempts on the live database waited 0.33s,
	// 17.16s and 4.54s, and during post-restart catch-up the log recorded
	// "fleet quiesced ... blocked=43 fleet=103 drainTimedOut=true".
	//
	// A verification that found no new rows was still queueing behind that to
	// rewrite a byte-identical row, so a PUBLIC READ-ONLY endpoint took the
	// writer lock and the handler's 30s deadline expired while it waited:
	// /api/ledger/verify answered 503 "exceeded 30s" on a chain whose
	// checkpoint was already current and whose suffix walk was empty.
	if cur, err := s.GetMeta(ctx, metaLedgerVerifyCkpt); err == nil && cur == string(b) {
		return nil
	}
	return s.SetMeta(ctx, metaLedgerVerifyCkpt, string(b))
}
