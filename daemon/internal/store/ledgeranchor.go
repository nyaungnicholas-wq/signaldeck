// External anchoring of the prediction ledger (finding C2).
//
// ledger.go's chain proves that no stored row was edited, deleted or reordered.
// It does NOT prove the history existed when it says it did: every anchor it
// had lived in the same SQLite file, so an operator who drops every row, drops
// meta.ledger_verify_checkpoint and regenerates a fabricated chain gets
// intact=true from both verify paths. That break is reproduced in
// TestLedger_ChainProvesConsistencyNotAnteriority.
//
// This file adds the missing half: signed commitments to the chain head, made
// with an Ed25519 key held outside the database (internal/ledgeranchor), and a
// verification that reports — per anchor — whether the CURRENT chain still
// reproduces that head. Anteriority then holds up to the newest reproducing
// anchor. It does not hold before the first anchor, and it does not hold
// against an adversary holding the signing key unless the anchor digest was
// published externally. Every one of those limits is carried in the API payload
// rather than left for the reader to discover.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// prefixCountsFor returns COUNT(*) WHERE seq<=s for every s in seqs, computed
// in ONE ordered pass over the range instead of one full scan per seq.
//
// The stored-mode anchor check used to run `COUNT(*) WHERE seq<=?` per anchor,
// and each of those is an index scan of the ENTIRE prefix — so the cost was
// anchors x rows, on a public endpoint, growing every time either number grew.
// Measured 2026-09-16 against the live ledger, 25 anchors over 506,934 rows:
// 1.912s for the per-anchor loop, 0.093s for this, identical answers. That was
// half the endpoint's runtime, and it is what put the request close enough to
// the handler's 30s ceiling that writer contention could push it over.
//
// Counting the DELTAS between consecutive anchor seqs sums to a single pass:
// count(<=s2) = count(<=s1) + count(s1<seq<=s2). Duplicate seqs resolve from
// the map without advancing the walk.
func (s *Store) prefixCountsFor(ctx context.Context, seqs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(seqs))
	sorted := append([]int64(nil), seqs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var running, prev int64
	for _, seq := range sorted {
		if _, done := out[seq]; done {
			continue
		}
		var n int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM prediction_ledger WHERE seq>? AND seq<=?`, prev, seq).Scan(&n); err != nil {
			return nil, err
		}
		running += n
		out[seq] = running
		prev = seq
	}
	return out, nil
}

// AnchorPolicy is the cadence at which new anchors are written. Anchoring every
// append would be pointless work (an anchor pins a PREFIX, so a later anchor
// subsumes an earlier one) and would turn a read path into a hot writer.
type AnchorPolicy struct {
	MinInterval   time.Duration // minimum wall-clock gap between anchors
	MinNewEntries int64         // minimum new ledger rows since the last anchor
}

// DefaultAnchorPolicy anchors at most every 6 hours and only when the chain
// actually grew. At the flagship's emit rate that leaves at most a few hours of
// head that no anchor covers yet — the window in which regeneration is still
// undetectable, which the payload states explicitly.
func DefaultAnchorPolicy() AnchorPolicy {
	return AnchorPolicy{MinInterval: 6 * time.Hour, MinNewEntries: 1}
}

// LedgerAnchorCheck is one anchor's standing against the CURRENT database.
type LedgerAnchorCheck struct {
	RowSeq       int64               `json:"rowSeq"` // ledger_anchors.seq
	Record       ledgeranchor.Record `json:"record"`
	SignatureOK  bool                `json:"signatureOK"`  // signed by the holder of PubKey
	HeadMatches  bool                `json:"headMatches"`  // chain still yields this head at LedgerSeq
	CountMatches bool                `json:"countMatches"` // prefix row count still matches
	OK           bool                `json:"ok"`
	Reason       string              `json:"reason,omitempty"` // why OK is false
	Publish      string              `json:"publish"`          // the short line to post externally
}

// LedgerAnchorVerification is the anchor-level answer: what the signatures
// prove about the chain that is in the database right now.
//
// ProvenThrough* are POINTERS: with no reproducing anchor there is no proven
// prefix, and the honest rendering of that is null — never 0, which would read
// as "proven through the beginning of time".
type LedgerAnchorVerification struct {
	Mode               string              `json:"mode"`        // "recomputed" | "stored"
	AnchorCount        int64               `json:"anchorCount"` // anchors in the DB
	Checked            int                 `json:"checked"`
	Anchors            []LedgerAnchorCheck `json:"anchors"`
	ProvenThroughSeq   *int64              `json:"provenThroughSeq"`
	ProvenThroughCount *int64              `json:"provenThroughCount"`
	ProvenThroughTs    *int64              `json:"provenThroughTs"`
	DistinctPubKeys    []string            `json:"distinctPubKeys"`
	// FailingAnchors counts checked anchors that NO LONGER REPRODUCE, and
	// FirstFailingSeq is the oldest such anchor's ledger seq.
	//
	// These matter more than ProvenThrough*, and the reason is the attack they
	// close. Checking only the NEWEST anchor lets a regenerated chain read
	// green: an operator deletes the history, re-appends a fabricated chain,
	// waits for the anchor cadence, and the fresh anchor over the fabricated
	// head reproduces perfectly — while the honest older anchor sits in the
	// same table reporting that its history is gone. A summary that reads only
	// the newest anchor would never mention it. A FAILING ANCHOR IS POSITIVE
	// EVIDENCE OF TAMPERING and has to dominate the summary, not be outvoted
	// by a newer one.
	FailingAnchors  int    `json:"failingAnchors"`
	FirstFailingSeq *int64 `json:"firstFailingSeq"`
}

// AppendLedgerAnchor writes one signed anchor and reports whether a row was
// added. Append-only: this file never UPDATEs or DELETEs an anchor, because a
// retractable anchor proves nothing.
//
// The insert is conditional on no anchor already existing for this
// (ledger_seq, pub_key). Two reasons: concurrent verify requests can both pass
// the cadence check before either writes, and a later anchor over a head that
// is already anchored by the same key is STRICTLY WEAKER evidence — the claim
// is "this head existed by time T", so the earliest timestamp is the one worth
// keeping. A different key (rotation) is allowed through, since that is a new
// fact rather than a repeat of an old one.
func (s *Store) AppendLedgerAnchor(ctx context.Context, r ledgeranchor.Record) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO ledger_anchors
		  (created_at, ledger_seq, ledger_count, head_hash, alg, pub_key, sig, digest)
		SELECT ?,?,?,?,?,?,?,?
		WHERE NOT EXISTS (
		  SELECT 1 FROM ledger_anchors WHERE ledger_seq=? AND pub_key=?)`,
		r.CreatedAt, r.LedgerSeq, r.LedgerCount, r.HeadHash, r.Alg, r.PubKey, r.Sig, r.Digest(),
		r.LedgerSeq, r.PubKey)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// LedgerAnchorRow is a stored anchor as read back, with NO verification verdict
// attached. It is deliberately a separate type from LedgerAnchorCheck: handing
// callers a check struct whose OK field is merely unset would let an unverified
// anchor read as a FAILED one, which is the wrong number rather than no number.
type LedgerAnchorRow struct {
	RowSeq int64               `json:"rowSeq"`
	Record ledgeranchor.Record `json:"record"`
}

// LedgerAnchors returns anchors newest-first (limit<=0 → all).
func (s *Store) LedgerAnchors(ctx context.Context, limit int) ([]LedgerAnchorRow, error) {
	q := `SELECT seq, created_at, ledger_seq, ledger_count, head_hash, alg, pub_key, sig
	      FROM ledger_anchors ORDER BY seq DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LedgerAnchorRow
	for rows.Next() {
		var c LedgerAnchorRow
		if err := rows.Scan(&c.RowSeq, &c.Record.CreatedAt, &c.Record.LedgerSeq,
			&c.Record.LedgerCount, &c.Record.HeadHash, &c.Record.Alg,
			&c.Record.PubKey, &c.Record.Sig); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LatestLedgerAnchor returns the newest anchor. ok=false when none exists —
// which is the state in which the platform can claim edit-detection and nothing
// more.
func (s *Store) LatestLedgerAnchor(ctx context.Context) (LedgerAnchorRow, bool, error) {
	got, err := s.LedgerAnchors(ctx, 1)
	if err != nil || len(got) == 0 {
		return LedgerAnchorRow{}, false, err
	}
	return got[0], true, nil
}

// VerifyLedgerAnchors checks the newest `limit` anchors against the live chain
// (limit<=0 → all).
//
// recompute=true is the auditor's path: the chain is re-derived from the stored
// PAYLOADS from genesis, ignoring the stored hashes entirely, and the derived
// head at each anchored seq is compared to the signed one. That catches a
// payload mutation which was rewritten consistently as well as one that was not
// — a strictly stronger check than VerifyLedger, which trusts stored hashes to
// agree with recomputation and stops at the first place they do not.
//
// recompute=false compares the anchor against the STORED head hash and prefix
// count (two indexed lookups per anchor). It is the cheap summary path; on a
// chain that VerifyLedger reports intact the two modes agree, and the mode is
// reported so a reader is never guessing which ran.
func (s *Store) VerifyLedgerAnchors(ctx context.Context, limit int, recompute bool) (LedgerAnchorVerification, error) {
	out := LedgerAnchorVerification{Mode: "stored", Anchors: []LedgerAnchorCheck{}, DistinctPubKeys: []string{}}
	if recompute {
		out.Mode = "recomputed"
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_anchors`).Scan(&out.AnchorCount); err != nil {
		return out, err
	}
	anchors, err := s.LedgerAnchors(ctx, limit)
	if err != nil {
		return out, err
	}
	if len(anchors) == 0 {
		return out, nil
	}

	// derived[seq] = (head hash, row count) the chain yields at that seq.
	var derived map[int64]headAt
	var prefix map[int64]int64
	if recompute {
		want := make(map[int64]bool, len(anchors))
		for _, a := range anchors {
			want[a.Record.LedgerSeq] = true
		}
		if derived, err = s.deriveChainHeads(ctx, want); err != nil {
			return out, err
		}
	} else {
		// One pass for every anchor's row count, rather than a full prefix
		// scan each. See prefixCountsFor for what this cost before.
		seqs := make([]int64, 0, len(anchors))
		for _, a := range anchors {
			seqs = append(seqs, a.Record.LedgerSeq)
		}
		if prefix, err = s.prefixCountsFor(ctx, seqs); err != nil {
			return out, err
		}
	}

	seen := map[string]bool{}
	// Counted inline, NOT in a defer: this function returns unnamed results, so
	// a deferred mutation of `out` would be written after the return value was
	// already copied and would silently vanish. It did, on the first attempt —
	// the tamper signal read zero while the anchor it described was failing.
	countFailures := func() {
		out.FailingAnchors = 0
		out.FirstFailingSeq = nil
		for _, a := range out.Anchors {
			if a.OK {
				continue
			}
			out.FailingAnchors++
			if out.FirstFailingSeq == nil || a.Record.LedgerSeq < *out.FirstFailingSeq {
				seq := a.Record.LedgerSeq
				out.FirstFailingSeq = &seq
			}
		}
	}
	for _, row := range anchors {
		a := LedgerAnchorCheck{RowSeq: row.RowSeq, Record: row.Record}
		a.Publish = a.Record.PublishLine()
		a.SignatureOK = a.Record.Verify()

		if recompute {
			h, ok := derived[a.Record.LedgerSeq]
			a.HeadMatches = ok && h.hash == a.Record.HeadHash
			a.CountMatches = ok && h.count == a.Record.LedgerCount
			if !ok {
				a.Reason = fmt.Sprintf("ledger seq %d no longer exists — the anchored history is gone", a.Record.LedgerSeq)
			}
		} else {
			var stored string
			err := s.db.QueryRowContext(ctx,
				`SELECT entry_hash FROM prediction_ledger WHERE seq=?`, a.Record.LedgerSeq).Scan(&stored)
			switch {
			case err == sql.ErrNoRows:
				a.Reason = fmt.Sprintf("ledger seq %d no longer exists — the anchored history is gone", a.Record.LedgerSeq)
			case err != nil:
				return out, err
			default:
				a.HeadMatches = stored == a.Record.HeadHash
				a.CountMatches = prefix[a.Record.LedgerSeq] == a.Record.LedgerCount
			}
		}

		a.OK = a.SignatureOK && a.HeadMatches && a.CountMatches
		if !a.OK && a.Reason == "" {
			switch {
			case !a.SignatureOK:
				a.Reason = "signature does not verify under the recorded public key"
			case !a.HeadMatches:
				a.Reason = "the chain no longer reproduces the signed head at this seq — history was rewritten after it was anchored"
			default:
				a.Reason = fmt.Sprintf("row count to seq %d is not the anchored %d — rows were inserted or removed before this point",
					a.Record.LedgerSeq, a.Record.LedgerCount)
			}
		}
		if !seen[a.Record.PubKey] {
			seen[a.Record.PubKey] = true
			out.DistinctPubKeys = append(out.DistinctPubKeys, a.Record.PubKey)
		}
		// Anchors arrive newest-first, so the first OK one is the newest and
		// subsumes every older anchor (a reproduced head fixes the whole
		// prefix that produced it).
		if a.OK && out.ProvenThroughSeq == nil {
			seq, count, ts := a.Record.LedgerSeq, a.Record.LedgerCount, a.Record.CreatedAt
			out.ProvenThroughSeq, out.ProvenThroughCount, out.ProvenThroughTs = &seq, &count, &ts
		}
		out.Anchors = append(out.Anchors, a)
	}
	out.Checked = len(out.Anchors)
	countFailures()
	return out, nil
}

// headAt is the chain state derived at one seq: the running hash and how many
// rows produced it.
type headAt struct {
	hash  string
	count int64
}

// deriveChainHeads walks the ledger seq-ascending and re-derives the running
// chain hash purely from row PAYLOADS, recording it at each requested seq. It
// deliberately ignores the stored prev_hash/entry_hash columns: an anchor asks
// "does this data still produce the head I signed", and reading the stored hash
// back would answer a weaker question.
func (s *Store) deriveChainHeads(ctx context.Context, want map[int64]bool) (map[int64]headAt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob,
		       feature_hash, model_version
		FROM prediction_ledger ORDER BY seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make(map[int64]headAt, len(want))
	running := ""
	var count int64
	for rows.Next() {
		var e LedgerEntry
		var hz string
		var symID sql.NullInt64
		if err := rows.Scan(&e.Seq, &e.PredictedAt, &symID, &hz, &e.BarTs,
			&e.RawProb, &e.CalProb, &e.FeatureHash, &e.ModelVersion); err != nil {
			return nil, err
		}
		e.SymbolID = symID.Int64
		e.Horizon = md.Horizon(hz)
		running = hashEntry(running, e)
		count++
		if want[e.Seq] {
			out[e.Seq] = headAt{hash: running, count: count}
		}
	}
	return out, rows.Err()
}

// AnchorDue reports whether a new anchor should be written now, the head it
// would commit to, and — when it should not — the reason. It needs no key, so a
// caller can decide whether to touch the signing key at all; loading a key
// (which CREATES one on first use) for a request that was never going to anchor
// would scatter key material around test and tooling runs.
//
// It refuses on a chain that did not verify intact: signing a head the platform
// cannot vouch for would convert a detected break into a signed claim. The
// verification `v` is passed in rather than recomputed so the anchor commits to
// exactly the head the caller just reported to the user.
func (s *Store) AnchorDue(ctx context.Context, v LedgerVerification, p AnchorPolicy, now time.Time) (LedgerEntry, bool, string, error) {
	if !v.Intact {
		return LedgerEntry{}, false, "chain is not intact — refusing to sign a head the chain itself rejects", nil
	}
	if v.Count == 0 {
		return LedgerEntry{}, false, "ledger is empty", nil
	}
	head, ok, err := s.LedgerHead(ctx)
	if err != nil {
		return LedgerEntry{}, false, "", err
	}
	if !ok {
		return LedgerEntry{}, false, "ledger is empty", nil
	}
	if head.EntryHash != v.HeadHash {
		// The chain grew between the walk and now; anchoring the newer head
		// would sign a prefix nobody verified in this pass.
		return head, false, "chain grew during verification — anchoring on the next pass", nil
	}
	last, hasLast, err := s.LatestLedgerAnchor(ctx)
	if err != nil {
		return head, false, "", err
	}
	if hasLast {
		if elapsed := now.Sub(time.Unix(last.Record.CreatedAt, 0)); elapsed < p.MinInterval {
			return head, false,
				fmt.Sprintf("cadence: %s since the last anchor, minimum %s", elapsed.Truncate(time.Second), p.MinInterval), nil
		}
		if head.Seq-last.Record.LedgerSeq < p.MinNewEntries {
			return head, false, "no new ledger entries since the last anchor", nil
		}
	}
	return head, true, "", nil
}

// MaybeAnchorLedger signs and appends a new anchor when AnchorDue allows. It
// returns the written record, whether anything was written, and — when nothing
// was — the REASON, so a caller renders "why is there no fresh anchor" instead
// of an empty field.
func (s *Store) MaybeAnchorLedger(ctx context.Context, sg *ledgeranchor.Signer, v LedgerVerification, p AnchorPolicy, now time.Time) (ledgeranchor.Record, bool, string, error) {
	if sg == nil {
		return ledgeranchor.Record{}, false, "no signing key available", nil
	}
	head, due, reason, err := s.AnchorDue(ctx, v, p, now)
	if err != nil || !due {
		return ledgeranchor.Record{}, false, reason, err
	}
	rec := sg.Sign(now.Unix(), head.Seq, v.Count, head.EntryHash)
	wrote, err := s.AppendLedgerAnchor(ctx, rec)
	if err != nil {
		return ledgeranchor.Record{}, false, "", err
	}
	if !wrote {
		return ledgeranchor.Record{}, false, "this head is already anchored by this key — the existing anchor has the earlier timestamp and is the stronger evidence", nil
	}
	return rec, true, "", nil
}
