package store

// Public accounts (2026-09-29): email addresses, email verification and
// password reset. The users table predates all three; the columns are added in
// migrateAccounts, called from migrate(), because a UNIQUE partial index on a
// column added by ALTER cannot live in schema.sql — it passes on every existing
// database and fails only on a fresh one.
//
// Tokens follow the session-token rule: only a SHA-256 digest is stored, so
// reading the database file never yields a usable link. Each token is single
// use and expires.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Token kinds.
const (
	TokenVerify = "verify"
	TokenReset  = "reset"
)

// ErrTokenInvalid covers unknown, used, expired and wrong-kind tokens alike, so
// a caller cannot tell which one it was.
var ErrTokenInvalid = errors.New("this link is invalid or has expired")

func migrateAccounts(w *sql.DB) error {
	for _, col := range []struct{ name, ddl string }{
		{"email", `ALTER TABLE users ADD COLUMN email TEXT`},
		{"email_verified", `ALTER TABLE users ADD COLUMN email_verified INTEGER DEFAULT 0`},
	} {
		var n int
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	for _, ddl := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users (lower(email)) WHERE email IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS auth_tokens (
		   token_hash TEXT PRIMARY KEY,
		   user_id    INTEGER NOT NULL REFERENCES users(id),
		   kind       TEXT NOT NULL,
		   created_ts INTEGER NOT NULL,
		   expires_ts INTEGER NOT NULL,
		   used_ts    INTEGER
		 )`,
		`CREATE INDEX IF NOT EXISTS idx_auth_tokens_user ON auth_tokens (user_id, kind)`,
	} {
		if _, err := w.Exec(ddl); err != nil {
			return err
		}
	}
	return nil
}

func hashAuthToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CreateUserWithEmail inserts a non-admin, unverified public account.
func (s *Store) CreateUserWithEmail(ctx context.Context, username, email, passHash string) (int64, error) {
	res, err := s.w.ExecContext(ctx,
		`INSERT INTO users (username, pass_hash, created_ts, is_admin, email, email_verified)
		 VALUES (?,?,?,0,?,0)`,
		username, passHash, time.Now().Unix(), strings.ToLower(email))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// EmailTaken reports whether an account already uses this address.
func (s *Store) EmailTaken(ctx context.Context, email string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE lower(email)=lower(?)`, email).Scan(&n)
	return n > 0, err
}

// AccountByEmail returns id, username and verified for an address.
func (s *Store) AccountByEmail(ctx context.Context, email string) (id int64, username string, verified bool, ok bool, err error) {
	var v int
	err = s.db.QueryRowContext(ctx,
		`SELECT id, username, COALESCE(email_verified,0) FROM users WHERE lower(email)=lower(?)`, email).
		Scan(&id, &username, &v)
	if err == sql.ErrNoRows {
		return 0, "", false, false, nil
	}
	return id, username, v == 1, err == nil, err
}

// AccountEmail returns the address and verified flag for a user. hasEmail is
// false for legacy accounts created before emails existed.
func (s *Store) AccountEmail(ctx context.Context, uid int64) (email string, verified, hasEmail bool, err error) {
	var e sql.NullString
	var v int
	err = s.db.QueryRowContext(ctx,
		`SELECT email, COALESCE(email_verified,0) FROM users WHERE id=?`, uid).Scan(&e, &v)
	if err == sql.ErrNoRows {
		return "", false, false, nil
	}
	return e.String, v == 1, e.Valid && e.String != "", err
}

// SetEmailVerified marks an account's address as confirmed.
func (s *Store) SetEmailVerified(ctx context.Context, uid int64) error {
	_, err := s.w.ExecContext(ctx, `UPDATE users SET email_verified=1 WHERE id=?`, uid)
	return err
}

// SetPassword replaces an account's hash and ends every session it holds, so a
// reset also evicts whoever had the old password.
func (s *Store) SetPassword(ctx context.Context, uid int64, passHash string) error {
	if _, err := s.w.ExecContext(ctx, `UPDATE users SET pass_hash=? WHERE id=?`, passHash, uid); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, uid)
	return err
}

// CreateAuthToken issues a single-use token and returns the plaintext for the
// emailed link. Earlier unused tokens of the same kind for the user are voided,
// so only the newest link works.
func (s *Store) CreateAuthToken(ctx context.Context, uid int64, kind string, ttl time.Duration) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	raw := hex.EncodeToString(b[:])
	now := time.Now().Unix()
	if _, err := s.w.ExecContext(ctx,
		`UPDATE auth_tokens SET used_ts=? WHERE user_id=? AND kind=? AND used_ts IS NULL`,
		now, uid, kind); err != nil {
		return "", err
	}
	if _, err := s.w.ExecContext(ctx,
		`INSERT INTO auth_tokens (token_hash, user_id, kind, created_ts, expires_ts) VALUES (?,?,?,?,?)`,
		hashAuthToken(raw), uid, kind, now, now+int64(ttl.Seconds())); err != nil {
		return "", err
	}
	return raw, nil
}

// ConsumeAuthToken redeems a token of the given kind exactly once. The UPDATE
// is the check: it only matches an unused, unexpired row, so two concurrent
// redemptions cannot both succeed.
func (s *Store) ConsumeAuthToken(ctx context.Context, raw, kind string) (int64, error) {
	if len(raw) != 64 {
		return 0, ErrTokenInvalid
	}
	h := hashAuthToken(raw)
	now := time.Now().Unix()
	res, err := s.w.ExecContext(ctx,
		`UPDATE auth_tokens SET used_ts=? WHERE token_hash=? AND kind=? AND used_ts IS NULL AND expires_ts>?`,
		now, h, kind, now)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrTokenInvalid
	}
	var uid int64
	if err := s.w.QueryRowContext(ctx, `SELECT user_id FROM auth_tokens WHERE token_hash=?`, h).Scan(&uid); err != nil {
		return 0, err
	}
	return uid, nil
}
