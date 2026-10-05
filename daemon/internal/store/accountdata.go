package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrAdminAccount refuses self-service deletion of the operator account.
var ErrAdminAccount = errors.New("the operator account cannot be deleted here")

// exportTables hold a member's own data and nothing derived from licensed
// prices. accountTables adds the operator-feature tables keyed by user_id
// (alerts, positions: their detail and prices are vendor-derived, so they are
// erased with an account but never exported). Sessions and emailed-link
// tokens are erased and never exported either.
var exportTables = []string{
	"member_symbols", "member_calls", "member_alert_prefs", "member_digest_tries",
}

var accountTables = append(append([]string{}, exportTables...), "user_symbols", "alerts", "positions")

// exportSkipColumns are credentials that sit beside a member's data.
var exportSkipColumns = map[string]bool{"telegram_link_code": true}

// AccountExport returns everything SignalDeck holds about one account, table
// by table, for the member's own download (2026-10-05 audit, AUD-05: there was
// no way to see it). The password hash and the session and emailed-link
// tokens are never included.
func (s *Store) AccountExport(ctx context.Context, uid int64) (map[string]any, error) {
	var username, email sql.NullString
	var created, verified, admin sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT username, email, created_ts, COALESCE(email_verified,0), COALESCE(is_admin,0) FROM users WHERE id=?`,
		uid).Scan(&username, &email, &created, &verified, &admin); err != nil {
		return nil, err
	}
	out := map[string]any{
		"account": map[string]any{
			"username":      username.String,
			"email":         email.String,
			"emailVerified": verified.Int64 == 1,
			"createdTs":     created.Int64,
			"isAdmin":       admin.Int64 == 1,
		},
	}
	for _, t := range exportTables {
		list, err := s.exportRows(ctx, t, uid)
		if err != nil {
			return nil, err
		}
		out[t] = list
	}
	return out, nil
}

// exportRows reads one table's rows for uid and closes them before returning,
// so the export never holds more than one read connection at a time.
func (s *Store) exportRows(ctx context.Context, table string, uid int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM `+table+` WHERE user_id=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if exportSkipColumns[c] {
				continue
			}
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// DeleteAccount removes one non-admin account and every row keyed to it in a
// single transaction, so a failure leaves the account whole rather than half
// erased. Member calls are the member's own private journal, not part of the
// public ledger, so they go with the account.
func (s *Store) DeleteAccount(ctx context.Context, uid int64) error {
	tx, err := s.authW().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var admin int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(is_admin,0) FROM users WHERE id=?`, uid).Scan(&admin); err != nil {
		return err
	}
	if admin != 0 {
		return ErrAdminAccount
	}
	for _, t := range append([]string{"auth_tokens", "sessions"}, accountTables...) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE user_id=?`, uid); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, uid); err != nil {
		return err
	}
	return tx.Commit()
}
