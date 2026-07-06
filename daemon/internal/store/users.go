package store

import (
	"context"
	"database/sql"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// User is one account row.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	PassHash  string `json:"-"`
	CreatedTs int64  `json:"createdTs"`
	IsAdmin   bool   `json:"isAdmin"`
}

// CreateUser inserts a new account and returns its id.
func (s *Store) CreateUser(ctx context.Context, username, passHash string, isAdmin bool) (int64, error) {
	admin := 0
	if isAdmin {
		admin = 1
	}
	res, err := s.w.ExecContext(ctx,
		`INSERT INTO users (username, pass_hash, created_ts, is_admin) VALUES (?,?,?,?)`,
		username, passHash, time.Now().Unix(), admin)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetUserByName fetches an account by username (ok=false when absent).
func (s *Store) GetUserByName(ctx context.Context, username string) (User, bool, error) {
	var u User
	var admin int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, pass_hash, COALESCE(created_ts,0), COALESCE(is_admin,0)
		 FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PassHash, &u.CreatedTs, &admin)
	if err == sql.ErrNoRows {
		return u, false, nil
	}
	u.IsAdmin = admin == 1
	return u, err == nil, err
}

// GetUserByID fetches an account by id (ok=false when absent).
func (s *Store) GetUserByID(ctx context.Context, id int64) (User, bool, error) {
	var u User
	var admin int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, pass_hash, COALESCE(created_ts,0), COALESCE(is_admin,0)
		 FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.PassHash, &u.CreatedTs, &admin)
	if err == sql.ErrNoRows {
		return u, false, nil
	}
	u.IsAdmin = admin == 1
	return u, err == nil, err
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// AdminUserID returns the id of the oldest admin account (0 when none).
func (s *Store) AdminUserID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE is_admin=1 ORDER BY id LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// ── sessions ────────────────────────────────────────────────────────────

// CreateSession stores a browser session token (stored plaintext: it is a
// 256-bit random value, useless outside this DB, and the DB is the trust root).
func (s *Store) CreateSession(ctx context.Context, token string, userID int64, expiresTs int64) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, created_ts, expires_ts) VALUES (?,?,?,?)`,
		token, userID, time.Now().Unix(), expiresTs)
	return err
}

// SessionUser resolves a token to its (unexpired) user id (ok=false otherwise).
func (s *Store) SessionUser(ctx context.Context, token string) (int64, bool, error) {
	var uid int64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE token=? AND expires_ts>?`,
		token, time.Now().Unix()).Scan(&uid)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return uid, err == nil, err
}

// DeleteSession removes one session (logout).
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

// PruneSessions removes expired sessions.
func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE expires_ts<=?`, time.Now().Unix())
	return err
}

// ── per-user watchlists ─────────────────────────────────────────────────

// AddUserSymbol puts a symbol on a user's watchlist (idempotent).
func (s *Store) AddUserSymbol(ctx context.Context, userID, symbolID int64) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT OR IGNORE INTO user_symbols (user_id, symbol_id, added_ts) VALUES (?,?,?)`,
		userID, symbolID, time.Now().Unix())
	return err
}

// RemoveUserSymbol takes a symbol off a user's watchlist.
func (s *Store) RemoveUserSymbol(ctx context.Context, userID, symbolID int64) error {
	_, err := s.w.ExecContext(ctx,
		`DELETE FROM user_symbols WHERE user_id=? AND symbol_id=?`, userID, symbolID)
	return err
}

// SymbolWatcherCount returns how many users watch a symbol (drives global
// deactivation: ingestion stays on while ANY user watches).
func (s *Store) SymbolWatcherCount(ctx context.Context, symbolID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_symbols WHERE symbol_id=?`, symbolID).Scan(&n)
	return n, err
}

// ListUserSymbols returns the symbols on one user's watchlist.
func (s *Store) ListUserSymbols(ctx context.Context, userID int64) ([]md.Symbol, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.symbol, s.market, s.name, s.active, s.added_at
		FROM user_symbols us JOIN symbols s ON s.id=us.symbol_id
		WHERE us.user_id=? ORDER BY s.market, s.symbol`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Symbol
	for rows.Next() {
		var sym md.Symbol
		var active int
		var mkt string
		if err := rows.Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt); err != nil {
			return nil, err
		}
		sym.Market, sym.Active = md.Market(mkt), active == 1
		out = append(out, sym)
	}
	return out, rows.Err()
}

// WatchedSymbolIDs returns the distinct symbol ids present on ANY user's
// watchlist. It drives the news-fetch scope: a symbol somebody watches always
// gets headlines, regardless of stream flag or ranking.
func (s *Store) WatchedSymbolIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT symbol_id FROM user_symbols`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AdoptActiveSymbols puts every currently-active symbol on a user's watchlist
// and assigns any unowned positions to them (first-boot multi-user migration,
// so existing single-user behavior continues unchanged).
func (s *Store) AdoptActiveSymbols(ctx context.Context, userID int64) error {
	if _, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO user_symbols (user_id, symbol_id, added_ts)
		SELECT ?, id, ? FROM symbols WHERE active=1`, userID, time.Now().Unix()); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx,
		`UPDATE positions SET user_id=? WHERE user_id IS NULL`, userID)
	return err
}
