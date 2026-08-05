// prereg-amend appends ONE amendment record to the hash-chained
// pre-registration log and does nothing else. It exists because the chain is
// tamper-evident: a record written by hand, with a hash computed by anything
// other than prereg.HashEntry, is indistinguishable from tampering.
//
// WHY A SEPARATE COMMAND RATHER THAN store.AppendPrereg. The daemon holds the
// store's single writer, and store's DSN opens transactions DEFERRED. AppendPrereg
// reads the chain head and then inserts; under a deferred transaction those two
// steps are not atomic against another writer, so a concurrent registrar pass
// could read the same head and fork the chain — two records sharing one
// prev_hash, which is exactly what a tamper-evident log is built to make
// impossible. This command opens its own connection with _txlock=immediate, so
// the write lock is taken BEFORE the head is read and the head-read/insert pair
// is atomic against the running daemon. Nothing has to be stopped.
//
// It is dry-run by default. -commit is the only way to write.
package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
)

// GradabilityKind is a record ABOUT the schedule of the frozen claims, never
// about their content. It is deliberately its own kind: amending a structural
// kind would change that claim's spec hash and read as "the claim moved", and
// no claim, band table or accuracy figure changes here.
const GradabilityKind = "gradability-correction"

func main() {
	var (
		dbPath = flag.String("db", "data/signaldeck.db", "path to signaldeck.db")
		commit = flag.Bool("commit", false, "actually append (default is dry-run)")
		kind   = flag.String("kind", GradabilityKind,
			"which amendment to file: "+GradabilityKind+", "+RevisionEpochKind+" or "+ProvenanceKind)
	)
	flag.Parse()
	if *kind != GradabilityKind && *kind != RevisionEpochKind && *kind != ProvenanceKind {
		die("unknown -kind %q (want %s, %s or %s)", *kind, GradabilityKind, RevisionEpochKind, ProvenanceKind)
	}

	db, err := sql.Open("sqlite", "file:"+*dbPath+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(15000)&_pragma=foreign_keys(ON)&_txlock=immediate")
	if err != nil {
		die("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	ctx := context.Background()

	if err := verifyChain(ctx, db); err != nil {
		die("PRE-FLIGHT chain verification failed, refusing to append: %v", err)
	}
	fmt.Println("pre-flight: chain verified INTACT")

	var spec, note string
	switch *kind {
	case GradabilityKind:
		m, err := measureState(ctx, db)
		if err != nil {
			die("measure state: %v", err)
		}
		// A record claiming zero resolutions must not be filed once any exist:
		// the whole value of this amendment is that it predates every outcome.
		if m.Resolved != 0 {
			die("REFUSING to file: %d structural forecast(s) have resolved. This record asserts it was "+
				"written before any outcome was known, and that is no longer true.", m.Resolved)
		}
		spec, note = gradabilitySpec(m), amendmentNote

	case RevisionEpochKind:
		m, err := measureRevisionState(ctx, db)
		if err != nil {
			die("measure revision state: %v", err)
		}
		// This record's entire defence is that it moves the boundary PAST the
		// contamination and no further. Filing it while an unattributable row
		// exists on or after the new epoch would make that false, and would
		// quietly exempt whatever wrote it.
		if m.OnOrAfterNewEpoch != 0 {
			die("REFUSING to file: %d unattributable row(s) exist on or after the new epoch "+
				"(2026-08-04). This record asserts the boundary clears every contaminated row, "+
				"and that is not true — fix the build first, then file.", m.OnOrAfterNewEpoch)
		}
		spec, note = revisionEpochSpec(m), revisionEpochNote

	case ProvenanceKind:
		p, err := measureProvenance(ctx, db)
		if err != nil {
			die("measure provenance: %v", err)
		}
		// The record's premise is that an off-path protocol record exists and is
		// unexplained. Filing it when none does would put a false statement on the
		// chain, and the chain is the one thing that cannot be quietly corrected.
		if p.OffendingSeq == 0 {
			die("REFUSING to file: every grading-protocol record after the genesis registration " +
				"already carries an AMENDMENT note. There is no off-path record to explain.")
		}
		// The defence of the off-path record is that it TIGHTENED the protocol. If
		// the database does not show restored floors, that defence is unproven and
		// this record must not assert it.
		if len(p.RestoredFields) == 0 {
			die("REFUSING to file: seq %d restored none of the frozen floors relative to seq %d. "+
				"This record's whole claim is that the off-path change was strictly tightening, "+
				"and that is not what the chain shows.", p.OffendingSeq, p.PriorSeq)
		}
		if len(p.DroppedFields) != 0 {
			die("REFUSING to file: seq %d DROPPED %v. That is a loosening, not a repair, and it "+
				"needs a decision rather than an explanatory record.", p.OffendingSeq, p.DroppedFields)
		}
		spec, note = provenanceSpec(p), provenanceNote
	}

	rec := prereg.Record{
		Ts:       time.Now().UTC().Unix(),
		TsNanos:  int64(time.Now().UTC().Nanosecond()),
		Kind:     *kind,
		SpecJSON: spec,
		SpecHash: sha256Hex(spec),
		Note:     note,
	}

	if !*commit {
		fmt.Printf("\nDRY RUN — nothing written. Pass -commit to append.\n\n")
		fmt.Printf("kind:     %s\nspecHash: %s\nnote:     %s\n\nspec:\n%s\n",
			rec.Kind, rec.SpecHash, rec.Note, rec.SpecJSON)
		return
	}

	// BEGIN IMMEDIATE via _txlock=immediate: the write lock is held before the
	// head is read, so no other writer can interleave between read and insert.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		die("begin immediate: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var prev sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT entry_hash FROM prereg_records ORDER BY seq DESC LIMIT 1`).Scan(&prev); err != nil && err != sql.ErrNoRows {
		die("read chain head: %v", err)
	}
	rec.PrevHash = prev.String
	rec.EntryHash = prereg.HashEntry(rec.PrevHash, rec)

	res, err := tx.ExecContext(ctx, `
		INSERT INTO prereg_records (ts, ts_nanos, kind, spec_json, spec_hash, prev_hash, entry_hash, note)
		VALUES (?,?,?,?,?,?,?,?)`,
		rec.Ts, rec.TsNanos, rec.Kind, rec.SpecJSON, rec.SpecHash, rec.PrevHash, rec.EntryHash, rec.Note)
	if err != nil {
		die("insert: %v", err)
	}
	seq, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		die("commit: %v", err)
	}

	if err := verifyChain(ctx, db); err != nil {
		die("POST-WRITE chain verification FAILED: %v", err)
	}
	fmt.Printf("appended seq=%d kind=%s\n  prevHash=%s\n  entryHash=%s\npost-write: chain verified INTACT\n",
		seq, rec.Kind, rec.PrevHash, rec.EntryHash)
}

// verifyChain recomputes every entry hash with prereg.HashEntry and checks the
// prev_hash linkage, so a broken chain is never appended onto.
func verifyChain(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT seq, ts, kind, spec_hash, prev_hash, entry_hash, note
		FROM prereg_records ORDER BY seq`)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck

	prev := ""
	n := 0
	for rows.Next() {
		var r prereg.Record
		if err := rows.Scan(&r.Seq, &r.Ts, &r.Kind, &r.SpecHash, &r.PrevHash, &r.EntryHash, &r.Note); err != nil {
			return err
		}
		if r.PrevHash != prev {
			return fmt.Errorf("seq %d: prev_hash %q does not match the previous entry_hash %q", r.Seq, r.PrevHash, prev)
		}
		if got := prereg.HashEntry(prev, r); got != r.EntryHash {
			return fmt.Errorf("seq %d: entry_hash %q does not match recomputed %q", r.Seq, r.EntryHash, got)
		}
		prev = r.EntryHash
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("chain is empty")
	}
	return nil
}

// measureState reads the live counts the record reports as its state at filing.
func measureState(ctx context.Context, db *sql.DB) (measured, error) {
	var m measured
	row := db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN resolved_at IS NOT NULL THEN 1 ELSE 0 END),
		       SUM(CASE WHEN naive_label IS NULL THEN 1 ELSE 0 END)
		  FROM regime_outcomes`)
	if err := row.Scan(&m.Rows, &m.Resolved, &m.Quarantined); err != nil {
		return m, err
	}
	// Blocks are per kind; report the largest any kind has accrued, which is the
	// most favourable reading and still far short of the gate.
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(b), 0) FROM (
		  SELECT COUNT(DISTINCT day / horizon_days) AS b
		    FROM regime_outcomes GROUP BY kind)`).Scan(&m.Blocks); err != nil {
		return m, err
	}
	return m, nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "prereg-amend: "+format+"\n", a...)
	os.Exit(1)
}
