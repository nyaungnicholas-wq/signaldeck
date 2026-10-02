package store

import (
	"context"
	"database/sql"
	"strconv"
)

// OpenQueryOnly opens a small read pool on this database in which EVERY
// connection runs with PRAGMA query_only=ON: SQLite refuses any write through
// it (SQLITE_READONLY), whatever SQL arrives. The "ask the data" copilot runs
// its catalog here, so a read-only guarantee does not rest on the catalog text
// alone. The caller closes it.
func (s *Store) OpenQueryOnly() (*sql.DB, error) {
	db, err := sql.Open("sqlite", s.dsn+"&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	boundReadConns(db)
	return db, nil
}

// AskCount is uid's "ask the data" count for the UTC day, without counting.
func (s *Store) AskCount(ctx context.Context, uid int64, day string) (int, error) {
	v, err := s.GetMeta(ctx, "copilot_ask:"+day+":"+strconv.FormatInt(uid, 10))
	if err != nil || v == "" {
		return 0, err
	}
	return strconv.Atoi(v)
}

// IncrAskCount counts one "ask the data" question for uid on the UTC day
// (YYYY-MM-DD) and returns the day's total, persisted in meta so a restart
// cannot reset a member's daily cap. It runs on the account writer: a person
// is waiting on it.
func (s *Store) IncrAskCount(ctx context.Context, uid int64, day string) (int, error) {
	key := "copilot_ask:" + day + ":" + strconv.FormatInt(uid, 10)
	var n int
	err := s.authW().QueryRowContext(ctx, `
		INSERT INTO meta (k, v) VALUES (?, '1')
		ON CONFLICT(k) DO UPDATE SET v=CAST(CAST(v AS INTEGER)+1 AS TEXT)
		RETURNING CAST(v AS INTEGER)`, key).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n == 1 { // first ask of the day for this user: sweep older days
		_, _ = s.authW().ExecContext(ctx, `DELETE FROM meta WHERE k LIKE 'copilot_ask:%' AND k < ?`, "copilot_ask:"+day+":")
	}
	return n, nil
}

// UndoAskCount takes back one IncrAskCount for uid on the UTC day, for an ask
// that was counted and then refused before any LLM call. It never goes below
// zero.
func (s *Store) UndoAskCount(ctx context.Context, uid int64, day string) error {
	_, err := s.authW().ExecContext(ctx, `UPDATE meta SET v=CAST(CAST(v AS INTEGER)-1 AS TEXT)
		WHERE k=? AND CAST(v AS INTEGER) > 0`, "copilot_ask:"+day+":"+strconv.FormatInt(uid, 10))
	return err
}
