package store

import (
	"context"
	"errors"
	"time"
)

// AddWaitlist records an email, idempotently.
//
// The caller has already validated and normalised the address (see
// api.normaliseEmail); this layer does not re-parse it, but it does refuse an
// empty string so a validation bug upstream cannot write a blank primary key.
//
// INSERT OR IGNORE, not a SELECT-then-INSERT: the duplicate case is the common
// one (people click twice), and doing it in one statement means there is no
// window where two concurrent submissions of the same address both decide they
// are the first.
//
// Returns added=false when the address was already present. The HTTP layer
// deliberately does NOT tell the caller which it was -- that difference is
// list-membership disclosure.
// ErrEmptyEmail guards the primary key against an upstream validation bug.
var ErrEmptyEmail = errors.New("waitlist: empty email")

func (s *Store) AddWaitlist(ctx context.Context, email, source string, now time.Time) (added bool, err error) {
	if email == "" {
		return false, ErrEmptyEmail
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO waitlist (email, created_ts, source) VALUES (?, ?, ?)`,
		email, now.Unix(), source)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CountWaitlist is for the operator, over SSH or an authenticated read. There
// is deliberately no endpoint that lists the addresses.
func (s *Store) CountWaitlist(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist`).Scan(&n)
	return n, err
}
