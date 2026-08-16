package main

import (
	"context"
	"database/sql"
	"fmt"
)

// alreadyFiled reports whether the chain already carries a record of this kind,
// and names the sequence numbers when it does.
//
// WHY THIS EXISTS. Every amendment kind here states one specific, one-time
// fact: that a corpus was repaired, that an off-path record has an explanation,
// that a schedule was corrected. Filing a second copy adds no information and
// makes the log ambiguous about how many times the underlying event happened —
// and because the chain is append-only, a duplicate can never be removed, only
// explained by yet another record.
//
// Measured 2026-08-15 immediately after filing seq 75-77: re-running
// -kind gradability-correction or -kind protocol-provenance-correction with
// -commit would have appended a DUPLICATE. Only data-integrity-amendment
// refused a second time, and then only incidentally — its premise guard
// ("no structural forecast has resolved") had since become false, which is a
// property of the calendar rather than of the record already existing. The
// dry-run default was the only thing standing between a repeated command and a
// polluted chain.
//
// There is deliberately NO override flag. If a second record of the same kind
// is ever genuinely warranted, it is warranted for a NEW reason and belongs
// under a new kind that says what that reason is.
func alreadyFiled(ctx context.Context, db *sql.DB, kind string) (bool, string, error) {
	var n int
	var seqs sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), GROUP_CONCAT(seq)
		FROM prereg_records WHERE kind = ?`, kind).Scan(&n, &seqs)
	if err != nil {
		return false, "", fmt.Errorf("count %s records: %w", kind, err)
	}
	if n == 0 {
		return false, "", nil
	}
	return true, seqs.String, nil
}
