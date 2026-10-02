package store

// Member alert preferences (plan step 5): the opt-in daily read by email and
// Telegram. Every row is per user; PurgeStaleUnverified deletes it with the
// account's other rows.

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// TokenUnsubscribe is the auth_tokens kind behind the one-click unsubscribe
// link in every digest email.
const TokenUnsubscribe = "unsubscribe"

// AlertPrefs is one user's alert settings; the zero value is "nothing on".
type AlertPrefs struct {
	EmailDigest     bool
	TelegramChatID  string
	LastDigestDay   string
	TelegramPending bool // a link code is outstanding and unexpired
}

// AlertPrefs returns uid's settings (zero value when the user never set any).
func (s *Store) AlertPrefs(ctx context.Context, uid int64) (AlertPrefs, error) {
	var p AlertPrefs
	var on int
	var chat, day sql.NullString
	var exp sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT email_digest, telegram_chat_id, last_digest_day, telegram_link_expires
		FROM member_alert_prefs WHERE user_id=?`, uid).Scan(&on, &chat, &day, &exp)
	if err == sql.ErrNoRows {
		return p, nil
	}
	p.EmailDigest, p.TelegramChatID, p.LastDigestDay = on == 1, chat.String, day.String
	p.TelegramPending = exp.Valid && exp.Int64 > time.Now().Unix()
	return p, err
}

func (s *Store) ensureAlertPrefs(ctx context.Context, uid int64) error {
	_, err := s.authW().ExecContext(ctx, `INSERT OR IGNORE INTO member_alert_prefs (user_id) VALUES (?)`, uid)
	return err
}

// SetEmailDigest turns the daily email on or off for uid.
func (s *Store) SetEmailDigest(ctx context.Context, uid int64, on bool) error {
	if err := s.ensureAlertPrefs(ctx, uid); err != nil {
		return err
	}
	v := 0
	if on {
		v = 1
	}
	_, err := s.authW().ExecContext(ctx, `UPDATE member_alert_prefs SET email_digest=? WHERE user_id=?`, v, uid)
	return err
}

// SetTelegramLinkCode stores a fresh link code for uid, replacing any earlier
// one. A collision with another user's live code fails on the unique index;
// the caller draws a new code.
func (s *Store) SetTelegramLinkCode(ctx context.Context, uid int64, code string, expires time.Time) error {
	if err := s.ensureAlertPrefs(ctx, uid); err != nil {
		return err
	}
	// Expired codes hold their slot in the unique index until cleared.
	if _, err := s.authW().ExecContext(ctx, `UPDATE member_alert_prefs
		SET telegram_link_code=NULL, telegram_link_expires=NULL WHERE telegram_link_expires <= ?`,
		time.Now().Unix()); err != nil {
		return err
	}
	_, err := s.authW().ExecContext(ctx, `UPDATE member_alert_prefs
		SET telegram_link_code=?, telegram_link_expires=? WHERE user_id=?`, code, expires.Unix(), uid)
	return err
}

// UnlinkTelegram forgets uid's chat and any outstanding code.
func (s *Store) UnlinkTelegram(ctx context.Context, uid int64) error {
	_, err := s.authW().ExecContext(ctx, `UPDATE member_alert_prefs
		SET telegram_chat_id=NULL, telegram_link_code=NULL, telegram_link_expires=NULL WHERE user_id=?`, uid)
	return err
}

// UnlinkTelegramChat forgets a chat by its id ("/stop" sent from it); ok is
// false when no user had it linked.
func (s *Store) UnlinkTelegramChat(ctx context.Context, chatID string) (bool, error) {
	res, err := s.authW().ExecContext(ctx,
		`UPDATE member_alert_prefs SET telegram_chat_id=NULL WHERE telegram_chat_id=?`, chatID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// LinkTelegramByCode binds chatID to the user holding the unexpired code and
// spends the code. ok is false for an unknown or expired code.
func (s *Store) LinkTelegramByCode(ctx context.Context, code, chatID string, now time.Time) (uid int64, ok bool, err error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" || chatID == "" {
		return 0, false, nil
	}
	err = s.authW().QueryRowContext(ctx, `UPDATE member_alert_prefs
		SET telegram_chat_id=?, telegram_link_code=NULL, telegram_link_expires=NULL
		WHERE telegram_link_code=? AND telegram_link_expires > ? RETURNING user_id`,
		chatID, code, now.Unix()).Scan(&uid)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return uid, err == nil, err
}

// DigestRecipient is one user due a daily read on at least one channel.
// Email is set only when the user opted in AND the address is verified.
type DigestRecipient struct {
	UserID        int64
	Email         string
	ChatID        string
	LastDigestDay string
}

// DigestRecipients lists every user with a deliverable channel: a verified
// email with the digest on, or a linked Telegram chat (linking is itself the
// opt-in). Ordered by user id so a capped run is deterministic.
func (s *Store) DigestRecipients(ctx context.Context) ([]DigestRecipient, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.user_id,
		       CASE WHEN p.email_digest=1 AND COALESCE(u.email_verified,0)=1 THEN COALESCE(u.email,'') ELSE '' END,
		       COALESCE(p.telegram_chat_id,''), COALESCE(p.last_digest_day,'')
		FROM member_alert_prefs p JOIN users u ON u.id=p.user_id
		WHERE (p.email_digest=1 AND COALESCE(u.email_verified,0)=1 AND COALESCE(u.email,'')<>'')
		   OR COALESCE(p.telegram_chat_id,'')<>''
		ORDER BY p.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []DigestRecipient
	for rows.Next() {
		var r DigestRecipient
		if err := rows.Scan(&r.UserID, &r.Email, &r.ChatID, &r.LastDigestDay); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkDigestSent records that uid got today's read (day is the ET date).
func (s *Store) MarkDigestSent(ctx context.Context, uid int64, day string) error {
	_, err := s.authW().ExecContext(ctx,
		`UPDATE member_alert_prefs SET last_digest_day=? WHERE user_id=?`, day, uid)
	return err
}

// RedeemUnsubscribe turns the email digest off for the token's user. Unlike
// verify and reset links it ignores used_ts: an older email's link must keep
// working after a newer email voided it (CreateAuthToken) or a password reset
// spent it, and repeating an unsubscribe is harmless. Kind and expiry still
// apply, so no other token kind can be replayed here.
func (s *Store) RedeemUnsubscribe(ctx context.Context, raw string) (int64, error) {
	if raw == "" || len(raw) > 128 {
		return 0, ErrTokenInvalid
	}
	var uid int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM auth_tokens
		WHERE token_hash=? AND kind=? AND expires_ts > ?`,
		hashAuthToken(raw), TokenUnsubscribe, time.Now().Unix()).Scan(&uid)
	if err == sql.ErrNoRows {
		return 0, ErrTokenInvalid
	}
	if err != nil {
		return 0, err
	}
	_, err = s.authW().ExecContext(ctx, `UPDATE member_alert_prefs SET email_digest=0 WHERE user_id=?`, uid)
	return uid, err
}

// PurgeExpiredTokens deletes expired auth tokens of one kind. The digest mints
// an unsubscribe token per email, so without this they accumulate forever.
func (s *Store) PurgeExpiredTokens(ctx context.Context, kind string, now time.Time) error {
	_, err := s.authW().ExecContext(ctx, `DELETE FROM auth_tokens WHERE kind=? AND expires_ts < ?`, kind, now.Unix())
	return err
}
