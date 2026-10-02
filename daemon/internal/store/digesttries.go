package store

import (
	"context"
)

// DigestTry is one member's digest bookkeeping for one ET day and channel:
// the tries spent and whether the read was delivered.
type DigestTry struct {
	UserID    int64
	Day       string // ET date, "2006-01-02"
	Channel   string // "email" or "telegram"
	Tries     int
	Delivered bool
}

// DigestTries returns every row recorded for day, ordered by user_id, channel.
func (s *Store) DigestTries(ctx context.Context, day string) ([]DigestTry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, day, channel, tries, delivered
		FROM member_digest_tries
		WHERE day = ?
		ORDER BY user_id, channel`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []DigestTry
	for rows.Next() {
		var t DigestTry
		var delivered int
		if err := rows.Scan(&t.UserID, &t.Day, &t.Channel, &t.Tries, &delivered); err != nil {
			return nil, err
		}
		t.Delivered = delivered != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveDigestTry records t (an upsert of the absolute values, never an
// increment), so the worker's in-memory state and the table cannot drift.
func (s *Store) SaveDigestTry(ctx context.Context, t DigestTry) error {
	delivered := 0
	if t.Delivered {
		delivered = 1
	}
	_, err := s.authW().ExecContext(ctx, `
		INSERT INTO member_digest_tries (user_id, day, channel, tries, delivered)
		VALUES (?,?,?,?,?)
		ON CONFLICT (user_id, day, channel)
		DO UPDATE SET tries = excluded.tries, delivered = excluded.delivered`,
		t.UserID, t.Day, t.Channel, t.Tries, delivered)
	return err
}

// PruneDigestTries deletes the rows of every day before day (string compare
// of ET dates, which sorts correctly for "2006-01-02").
func (s *Store) PruneDigestTries(ctx context.Context, day string) error {
	_, err := s.authW().ExecContext(ctx, `
		DELETE FROM member_digest_tries
		WHERE day < ?`, day)
	return err
}
