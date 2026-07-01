// Package store is SignalDeck's SQLite persistence layer — the single
// contract every worker and API handler codes against.
//
// Driver: modernc.org/sqlite (pure Go, no cgo). WAL mode, busy_timeout, one
// *sql.DB shared by all goroutines (database/sql pools connections; SQLite
// serializes writers via WAL).
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a small pool avoids lock churn.
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for read-only ad-hoc queries (export endpoints).
func (s *Store) DB() *sql.DB { return s.db }

// ── symbols ─────────────────────────────────────────────────────────────

// UpsertSymbol inserts or reactivates a symbol and returns its row.
func (s *Store) UpsertSymbol(ctx context.Context, symbol string, market md.Market, name string) (md.Symbol, error) {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO symbols (symbol, market, name, active, added_at) VALUES (?,?,?,1,?)
		ON CONFLICT(symbol, market) DO UPDATE SET active=1, name=CASE WHEN excluded.name != '' THEN excluded.name ELSE symbols.name END`,
		symbol, string(market), name, now)
	if err != nil {
		return md.Symbol{}, err
	}
	return s.GetSymbol(ctx, symbol, market)
}

// GetSymbol fetches one symbol by (symbol, market).
func (s *Store) GetSymbol(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
	var sym md.Symbol
	var active int
	var mkt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, symbol, market, name, active, added_at FROM symbols WHERE symbol=? AND market=?`,
		symbol, string(market)).Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt)
	sym.Market, sym.Active = md.Market(mkt), active == 1
	return sym, err
}

// GetSymbolByID fetches one symbol by id.
func (s *Store) GetSymbolByID(ctx context.Context, id int64) (md.Symbol, error) {
	var sym md.Symbol
	var active int
	var mkt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, symbol, market, name, active, added_at FROM symbols WHERE id=?`,
		id).Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt)
	sym.Market, sym.Active = md.Market(mkt), active == 1
	return sym, err
}

// ListSymbols returns all symbols (activeOnly filters to live subscriptions).
func (s *Store) ListSymbols(ctx context.Context, activeOnly bool) ([]md.Symbol, error) {
	q := `SELECT id, symbol, market, name, active, added_at FROM symbols`
	if activeOnly {
		q += ` WHERE active=1`
	}
	q += ` ORDER BY market, symbol`
	rows, err := s.db.QueryContext(ctx, q)
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

// SetSymbolActive toggles the live subscription flag (history is kept).
func (s *Store) SetSymbolActive(ctx context.Context, id int64, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE symbols SET active=? WHERE id=?`, v, id)
	return err
}

// ── bars ────────────────────────────────────────────────────────────────

// UpsertBars writes bars idempotently (REPLACE on the composite key).
func (s *Store) UpsertBars(ctx context.Context, bars []md.Bar) error {
	if len(bars) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
		VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, b := range bars {
		if _, err := stmt.ExecContext(ctx, b.SymbolID, string(b.TF), b.Ts, b.Open, b.High, b.Low, b.Close, b.Volume); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Bars returns bars in [from, to) ascending, capped at limit (0 = no cap).
func (s *Store) Bars(ctx context.Context, symbolID int64, tf md.Timeframe, from, to int64, limit int) ([]md.Bar, error) {
	q := `SELECT ts, open, high, low, close, volume FROM bars
	      WHERE symbol_id=? AND tf=? AND ts>=? AND ts<? ORDER BY ts`
	args := []any{symbolID, string(tf), from, to}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Bar
	for rows.Next() {
		b := md.Bar{SymbolID: symbolID, TF: tf}
		if err := rows.Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LastBars returns the most recent n bars ascending.
func (s *Store) LastBars(ctx context.Context, symbolID int64, tf md.Timeframe, n int) ([]md.Bar, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, open, high, low, close, volume FROM
		  (SELECT * FROM bars WHERE symbol_id=? AND tf=? ORDER BY ts DESC LIMIT ?)
		ORDER BY ts`, symbolID, string(tf), n)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Bar
	for rows.Next() {
		b := md.Bar{SymbolID: symbolID, TF: tf}
		if err := rows.Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LatestBarTs returns the newest bar open-time, or 0 when none exist.
func (s *Store) LatestBarTs(ctx context.Context, symbolID int64, tf md.Timeframe) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(ts) FROM bars WHERE symbol_id=? AND tf=?`, symbolID, string(tf)).Scan(&ts)
	return ts.Int64, err
}

// BarAtOrAfter returns the first bar with ts >= t (for outcome resolution).
func (s *Store) BarAtOrAfter(ctx context.Context, symbolID int64, tf md.Timeframe, t int64) (md.Bar, bool, error) {
	b := md.Bar{SymbolID: symbolID, TF: tf}
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, open, high, low, close, volume FROM bars
		WHERE symbol_id=? AND tf=? AND ts>=? ORDER BY ts LIMIT 1`,
		symbolID, string(tf), t).Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume)
	if err == sql.ErrNoRows {
		return b, false, nil
	}
	return b, err == nil, err
}

// Rollup aggregates a finer timeframe into a coarser one over [from, to).
// bucket is the coarse bar length in seconds (3600 for 1h, 86400 for 1d).
func (s *Store) Rollup(ctx context.Context, symbolID int64, src, dst md.Timeframe, bucket, from, to int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
		SELECT symbol_id, ?, (ts/?)*? AS bts,
		  (SELECT open FROM bars b2 WHERE b2.symbol_id=b.symbol_id AND b2.tf=b.tf
		     AND b2.ts/? = b.ts/? ORDER BY b2.ts LIMIT 1),
		  MAX(high), MIN(low),
		  (SELECT close FROM bars b3 WHERE b3.symbol_id=b.symbol_id AND b3.tf=b.tf
		     AND b3.ts/? = b.ts/? ORDER BY b3.ts DESC LIMIT 1),
		  SUM(volume)
		FROM bars b
		WHERE symbol_id=? AND tf=? AND ts>=? AND ts<?
		GROUP BY bts`,
		string(dst), bucket, bucket, bucket, bucket, bucket, bucket,
		symbolID, string(src), from, to)
	return err
}

// PruneBars deletes bars of a timeframe older than cutoff (retention).
func (s *Store) PruneBars(ctx context.Context, tf md.Timeframe, cutoff int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bars WHERE tf=? AND ts<?`, string(tf), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── crypto 1s snapshots ─────────────────────────────────────────────────

// InsertSnap1s writes one microstructure snapshot (idempotent per second).
func (s *Store) InsertSnap1s(ctx context.Context, sn md.Snap1s) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO snapshots_1s
		  (symbol_id, ts, bid, ask, mid, wmid, imb_signed, spread, apply_lat_ns)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		sn.SymbolID, sn.Ts, sn.Bid, sn.Ask, sn.Mid, sn.WMid, sn.ImbSigned, sn.Spread, sn.ApplyLatNs)
	return err
}

// Snaps returns snapshots in [from, to) ascending.
func (s *Store) Snaps(ctx context.Context, symbolID int64, from, to int64, limit int) ([]md.Snap1s, error) {
	q := `SELECT ts, bid, ask, mid, wmid, imb_signed, spread, apply_lat_ns
	      FROM snapshots_1s WHERE symbol_id=? AND ts>=? AND ts<? ORDER BY ts`
	args := []any{symbolID, from, to}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Snap1s
	for rows.Next() {
		sn := md.Snap1s{SymbolID: symbolID}
		if err := rows.Scan(&sn.Ts, &sn.Bid, &sn.Ask, &sn.Mid, &sn.WMid, &sn.ImbSigned, &sn.Spread, &sn.ApplyLatNs); err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// PruneSnaps enforces the snapshot ring retention.
func (s *Store) PruneSnaps(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM snapshots_1s WHERE ts<?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── scores & outcomes ───────────────────────────────────────────────────

// InsertScore persists a score AND seeds its pending outcome row (the
// honesty backtest is fed at write time, never reconstructed later).
func (s *Store) InsertScore(ctx context.Context, sc md.Score) error {
	comps, err := json.Marshal(sc.Components)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO scores (symbol_id, horizon, ts, score, components)
		VALUES (?,?,?,?,?)`,
		sc.SymbolID, string(sc.Horizon), sc.Ts, sc.Score, string(comps)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO score_outcomes (symbol_id, horizon, ts, score)
		VALUES (?,?,?,?)`,
		sc.SymbolID, string(sc.Horizon), sc.Ts, sc.Score); err != nil {
		return err
	}
	return tx.Commit()
}

// LatestScore returns the newest score for (symbol, horizon).
func (s *Store) LatestScore(ctx context.Context, symbolID int64, h md.Horizon) (md.Score, bool, error) {
	sc := md.Score{SymbolID: symbolID, Horizon: h}
	var comps string
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, score, components FROM scores
		WHERE symbol_id=? AND horizon=? ORDER BY ts DESC LIMIT 1`,
		symbolID, string(h)).Scan(&sc.Ts, &sc.Score, &comps)
	if err == sql.ErrNoRows {
		return sc, false, nil
	}
	if err != nil {
		return sc, false, err
	}
	if err := json.Unmarshal([]byte(comps), &sc.Components); err != nil {
		return sc, false, err
	}
	return sc, true, nil
}

// ScoreHistory returns scores ascending in [from, to).
func (s *Store) ScoreHistory(ctx context.Context, symbolID int64, h md.Horizon, from, to int64) ([]md.Score, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, score, components FROM scores
		WHERE symbol_id=? AND horizon=? AND ts>=? AND ts<? ORDER BY ts`,
		symbolID, string(h), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Score
	for rows.Next() {
		sc := md.Score{SymbolID: symbolID, Horizon: h}
		var comps string
		if err := rows.Scan(&sc.Ts, &sc.Score, &comps); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(comps), &sc.Components); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// UnresolvedOutcomes returns pending outcomes whose ts is at or before cutoff
// (i.e. enough time has passed that the forward window may have closed).
func (s *Store) UnresolvedOutcomes(ctx context.Context, cutoff int64, limit int) ([]md.ScoreOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, horizon, ts, score FROM score_outcomes
		WHERE resolved_at IS NULL AND ts<=? ORDER BY ts LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.ScoreOutcome
	for rows.Next() {
		var o md.ScoreOutcome
		var h string
		if err := rows.Scan(&o.SymbolID, &h, &o.Ts, &o.Score); err != nil {
			return nil, err
		}
		o.Horizon = md.Horizon(h)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ResolveOutcome records the realized forward return for one score.
func (s *Store) ResolveOutcome(ctx context.Context, symbolID int64, h md.Horizon, ts int64, fwdReturn float64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE score_outcomes SET fwd_return=?, resolved_at=?
		WHERE symbol_id=? AND horizon=? AND ts=?`,
		fwdReturn, time.Now().Unix(), symbolID, string(h), ts)
	return err
}

// ResolvedOutcomes returns resolved (score, fwd_return) pairs for the honesty
// page; symbolID 0 = all symbols.
func (s *Store) ResolvedOutcomes(ctx context.Context, symbolID int64, h md.Horizon, limit int) ([]md.ScoreOutcome, error) {
	q := `SELECT symbol_id, horizon, ts, score, fwd_return, resolved_at
	      FROM score_outcomes WHERE resolved_at IS NOT NULL AND horizon=?`
	args := []any{string(h)}
	if symbolID != 0 {
		q += ` AND symbol_id=?`
		args = append(args, symbolID)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.ScoreOutcome
	for rows.Next() {
		var o md.ScoreOutcome
		var hz string
		var fwd sql.NullFloat64
		var res sql.NullInt64
		if err := rows.Scan(&o.SymbolID, &hz, &o.Ts, &o.Score, &fwd, &res); err != nil {
			return nil, err
		}
		o.Horizon = md.Horizon(hz)
		if fwd.Valid {
			o.FwdReturn = &fwd.Float64
		}
		if res.Valid {
			o.ResolvedAt = &res.Int64
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ── expectancy ──────────────────────────────────────────────────────────

// ReplaceExpectancy swaps in the freshly-computed table for one symbol+horizon.
func (s *Store) ReplaceExpectancy(ctx context.Context, symbolID int64, h md.Horizon, rows []md.Expectancy) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM expectancy WHERE symbol_id=? AND horizon=?`, symbolID, string(h)); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, e := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO expectancy (symbol_id, horizon, state_key, n, mean_fwd, median_fwd, hit_rate, stdev, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			symbolID, string(h), e.StateKey, e.N, e.MeanFwd, e.MedianFwd, e.HitRate, e.Stdev, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Expectancy returns the stored table for one symbol+horizon.
func (s *Store) Expectancy(ctx context.Context, symbolID int64, h md.Horizon) ([]md.Expectancy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT state_key, n, mean_fwd, median_fwd, hit_rate, stdev, updated_at
		FROM expectancy WHERE symbol_id=? AND horizon=? ORDER BY n DESC`,
		symbolID, string(h))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Expectancy
	for rows.Next() {
		e := md.Expectancy{SymbolID: symbolID, Horizon: h}
		if err := rows.Scan(&e.StateKey, &e.N, &e.MeanFwd, &e.MedianFwd, &e.HitRate, &e.Stdev, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ── insights ────────────────────────────────────────────────────────────

// InsertInsight stores one readable insight.
func (s *Store) InsertInsight(ctx context.Context, in md.Insight) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO insights (scope, symbol_id, ts, headline, body, data)
		VALUES (?,?,?,?,?,?)`,
		in.Scope, in.SymbolID, in.Ts, in.Headline, in.Body, in.Data)
	return err
}

// RecentInsights returns the newest insights (symbolID 0 = all).
func (s *Store) RecentInsights(ctx context.Context, symbolID int64, limit int) ([]md.Insight, error) {
	q := `SELECT i.id, i.scope, i.symbol_id, i.ts, i.headline, i.body, i.data,
	             COALESCE(sym.symbol, '')
	      FROM insights i LEFT JOIN symbols sym ON sym.id = i.symbol_id`
	args := []any{}
	if symbolID != 0 {
		q += ` WHERE i.symbol_id=?`
		args = append(args, symbolID)
	}
	q += ` ORDER BY i.ts DESC, i.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Insight
	for rows.Next() {
		var in md.Insight
		var sid sql.NullInt64
		if err := rows.Scan(&in.ID, &in.Scope, &sid, &in.Ts, &in.Headline, &in.Body, &in.Data, &in.Symbol); err != nil {
			return nil, err
		}
		if sid.Valid {
			in.SymbolID = &sid.Int64
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ── worker runs (the in-app agents' status) ─────────────────────────────

// StartWorkerRun opens a run record and returns its id.
func (s *Store) StartWorkerRun(ctx context.Context, worker string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO worker_runs (worker, started_at, status) VALUES (?,?,'running')`,
		worker, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishWorkerRun closes a run record.
func (s *Store) FinishWorkerRun(ctx context.Context, id int64, status, detail string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE worker_runs SET finished_at=?, status=?, detail=? WHERE id=?`,
		time.Now().Unix(), status, detail, id)
	return err
}

// RecentWorkerRuns returns the latest runs per worker (flat list, newest first).
func (s *Store) RecentWorkerRuns(ctx context.Context, limit int) ([]md.WorkerRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, worker, started_at, finished_at, status, detail
		FROM worker_runs ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.WorkerRun
	for rows.Next() {
		var r md.WorkerRun
		var fin sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Worker, &r.StartedAt, &fin, &r.Status, &r.Detail); err != nil {
			return nil, err
		}
		if fin.Valid {
			r.FinishedAt = &fin.Int64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneWorkerRuns keeps the run log bounded.
func (s *Store) PruneWorkerRuns(ctx context.Context, keep int) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM worker_runs WHERE id NOT IN
		  (SELECT id FROM worker_runs ORDER BY started_at DESC, id DESC LIMIT ?)`, keep)
	return err
}

// ── data quality ────────────────────────────────────────────────────────

// InsertDQ records a data-quality incident.
func (s *Store) InsertDQ(ctx context.Context, ev md.DQEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO dq_events (symbol_id, ts, kind, detail) VALUES (?,?,?,?)`,
		ev.SymbolID, ev.Ts, ev.Kind, ev.Detail)
	return err
}

// RecentDQ returns the latest incidents.
func (s *Store) RecentDQ(ctx context.Context, limit int) ([]md.DQEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.symbol_id, d.ts, d.kind, d.detail, COALESCE(sym.symbol,'')
		FROM dq_events d LEFT JOIN symbols sym ON sym.id = d.symbol_id
		ORDER BY d.ts DESC, d.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.DQEvent
	for rows.Next() {
		var ev md.DQEvent
		var sid sql.NullInt64
		if err := rows.Scan(&ev.ID, &sid, &ev.Ts, &ev.Kind, &ev.Detail, &ev.Symbol); err != nil {
			return nil, err
		}
		if sid.Valid {
			ev.SymbolID = &sid.Int64
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// BarCount returns row count + span for coverage reporting.
func (s *Store) BarCount(ctx context.Context, symbolID int64, tf md.Timeframe) (n int64, minTs, maxTs int64, err error) {
	var mn, mx sql.NullInt64
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(ts), MAX(ts) FROM bars WHERE symbol_id=? AND tf=?`,
		symbolID, string(tf)).Scan(&n, &mn, &mx)
	return n, mn.Int64, mx.Int64, err
}

// ── hud + meta ──────────────────────────────────────────────────────────

// SetHud stores the latest trader-hud summary payload.
func (s *Store) SetHud(ctx context.Context, payload string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hud_summary (id, fetched_at, payload) VALUES (1,?,?)
		ON CONFLICT(id) DO UPDATE SET fetched_at=excluded.fetched_at, payload=excluded.payload`,
		time.Now().Unix(), payload)
	return err
}

// GetHud returns the latest hud payload and its fetch time (ok=false if never).
func (s *Store) GetHud(ctx context.Context) (payload string, fetchedAt int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT payload, fetched_at FROM hud_summary WHERE id=1`).Scan(&payload, &fetchedAt)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	return payload, fetchedAt, err == nil, err
}

// SetMeta / GetMeta are small key-value helpers (schema version, cursors…).
func (s *Store) SetMeta(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (k, v) VALUES (?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// GetMeta returns "" when the key is absent.
func (s *Store) GetMeta(ctx context.Context, k string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, k).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}
