package store

// Member call journal (plan step 8): a member's own up/down calls, graded by
// member-call-resolver (internal/memberjournal). Rows hold no price or return,
// only the grade. The schema's triggers make a call immutable; this file has
// no edit and no delete path (PurgeStaleUnverified is the only DELETE).

import (
	"context"
	"database/sql"
	"errors"
)

// Journal caps: new calls per member per ET day, and open at once.
const (
	MemberCallsPerDay = 10
	MemberCallsOpen   = 50
)

var (
	ErrCallCapDay  = errors.New("daily call limit reached")
	ErrCallCapOpen = errors.New("open call limit reached")
)

// MemberCall is one journal row. EntryTs and ExitDueTs are the session-close
// instants fixed at creation (memberjournal.Schedule).
type MemberCall struct {
	ID          int64
	UserID      int64
	SymbolID    int64
	Symbol      string
	Market      string
	Call        string // up | down
	Horizon     int    // trading sessions: 1, 5 or 21
	Note        string
	CreatedTs   int64
	EntryTs     int64
	ExitDueTs   int64
	Status      string // open | resolved | void | withdrawn
	Outcome     string // hit | miss, set only when resolved
	ResolvedTs  int64
	WithdrawnTs int64
}

// InsertMemberCall stores c under both caps in one statement, so two
// concurrent posts cannot both slip under a cap. dayStart is the unix start of
// the member's current ET day.
func (s *Store) InsertMemberCall(ctx context.Context, c MemberCall, dayStart int64) (int64, error) {
	res, err := s.authW().ExecContext(ctx, `
		INSERT INTO member_calls (user_id, symbol_id, market, call, horizon, note, created_ts, entry_ts, exit_due_ts)
		SELECT ?,?,?,?,?,?,?,?,?
		WHERE (SELECT COUNT(*) FROM member_calls WHERE user_id=? AND created_ts>=?) < ?
		  AND (SELECT COUNT(*) FROM member_calls WHERE user_id=? AND status='open') < ?`,
		c.UserID, c.SymbolID, c.Market, c.Call, c.Horizon, c.Note, c.CreatedTs, c.EntryTs, c.ExitDueTs,
		c.UserID, dayStart, MemberCallsPerDay, c.UserID, MemberCallsOpen)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return res.LastInsertId()
	}
	today, open, err := s.MemberCallCounts(ctx, c.UserID, dayStart)
	if err != nil {
		return 0, err
	}
	if today >= MemberCallsPerDay {
		return 0, ErrCallCapDay
	}
	if open >= MemberCallsOpen {
		return 0, ErrCallCapOpen
	}
	return 0, errors.New("member call not stored") // a cap freed between the two reads
}

// MemberCallCounts is uid's calls created since dayStart and currently open.
func (s *Store) MemberCallCounts(ctx context.Context, uid, dayStart int64) (today, open int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(created_ts>=?),0), COALESCE(SUM(status='open'),0)
		FROM member_calls WHERE user_id=?`, dayStart, uid).Scan(&today, &open)
	return
}

const memberCallCols = `c.id, c.user_id, c.symbol_id, s.symbol, c.market, c.call, c.horizon, c.note,
	c.created_ts, c.entry_ts, c.exit_due_ts, c.status, COALESCE(c.outcome,''),
	COALESCE(c.resolved_ts,0), COALESCE(c.withdrawn_ts,0)
	FROM member_calls c JOIN symbols s ON s.id=c.symbol_id`

func scanMemberCalls(rows *sql.Rows) ([]MemberCall, error) {
	defer rows.Close() //nolint:errcheck
	var out []MemberCall
	for rows.Next() {
		var c MemberCall
		if err := rows.Scan(&c.ID, &c.UserID, &c.SymbolID, &c.Symbol, &c.Market, &c.Call, &c.Horizon, &c.Note,
			&c.CreatedTs, &c.EntryTs, &c.ExitDueTs, &c.Status, &c.Outcome, &c.ResolvedTs, &c.WithdrawnTs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MemberCalls returns uid's calls, newest first. Only uid's: every read here
// is keyed by the session's user id.
func (s *Store) MemberCalls(ctx context.Context, uid int64) ([]MemberCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+memberCallCols+`
		WHERE c.user_id=? ORDER BY c.created_ts DESC, c.id DESC`, uid)
	if err != nil {
		return nil, err
	}
	return scanMemberCalls(rows)
}

// MemberCall returns uid's call id; ok is false when it does not exist OR
// belongs to someone else (the two are indistinguishable to the caller).
func (s *Store) MemberCall(ctx context.Context, uid, id int64) (MemberCall, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+memberCallCols+` WHERE c.user_id=? AND c.id=?`, uid, id)
	if err != nil {
		return MemberCall{}, false, err
	}
	out, err := scanMemberCalls(rows)
	if err != nil || len(out) == 0 {
		return MemberCall{}, false, err
	}
	return out[0], true, nil
}

// OpenMemberCallsDue returns every member's open calls whose exit session
// closed at or before now, oldest due first (the resolver's queue).
func (s *Store) OpenMemberCallsDue(ctx context.Context, now int64) ([]MemberCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+memberCallCols+`
		WHERE c.status='open' AND c.exit_due_ts<=? ORDER BY c.exit_due_ts, c.id`, now)
	if err != nil {
		return nil, err
	}
	return scanMemberCalls(rows)
}

// WithdrawMemberCall marks uid's open call withdrawn. ok is false when the
// call is not uid's, is no longer open, or its entry close has passed (so a
// request that checked just before the close cannot land after it). The
// caller decides whether the entry bar exists yet.
func (s *Store) WithdrawMemberCall(ctx context.Context, uid, id, now int64) (bool, error) {
	res, err := s.authW().ExecContext(ctx, `UPDATE member_calls SET status='withdrawn', withdrawn_ts=?
		WHERE id=? AND user_id=? AND status='open' AND entry_ts>?`, now, id, uid, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SettleMemberCall grades an open call: outcome "hit"/"miss" resolves it, ""
// voids it. A call already settled is never rewritten (ok false), so a rerun
// of the resolver is a no-op.
func (s *Store) SettleMemberCall(ctx context.Context, id int64, outcome string, now int64) (bool, error) {
	status, out := "resolved", any(outcome)
	if outcome == "" {
		status, out = "void", nil
	}
	res, err := s.authW().ExecContext(ctx, `UPDATE member_calls SET status=?, outcome=?, resolved_ts=?
		WHERE id=? AND status='open'`, status, out, now, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
