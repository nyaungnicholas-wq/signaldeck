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
	"os"
	"time"

	_ "modernc.org/sqlite"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the database. Reads go through the pooled db handle; ALL writes
// go through w, a single-connection handle, so concurrent writers queue in Go
// instead of racing for the SQLite write lock (no SQLITE_BUSY under load).
type Store struct {
	db   *sql.DB // read pool
	w    *sql.DB // dedicated single-connection write path
	path string  // database file path (for size accounting in DataStats)
	dsn  string  // connection string (so a reader clone opens identically)
	// borrowedWriter marks a ReaderClone: it shares the parent's write
	// connection, so Close must not close the writer out from under the parent.
	borrowedWriter bool
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	// journal_size_limit caps the WAL FILE: once a checkpoint completes, SQLite
	// truncates the WAL back to this bound instead of letting it grow without
	// limit. Before this, a slow read (e.g. the old full-universe screener) held
	// old WAL frames long enough for concurrent writes to push the file past
	// 1GB, which the live governor's TRUNCATE checkpoint could never reclaim
	// while readers stayed active. 256MB is comfortably above the working set.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=journal_size_limit(268435456)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Read pool: WAL readers don't block each other.
	db.SetMaxOpenConns(4)
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		db.Close() //nolint:errcheck
		return nil, err
	}
	// SQLite allows exactly one writer — serialize writes on one connection.
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(schemaSQL); err != nil {
		db.Close() //nolint:errcheck
		w.Close()  //nolint:errcheck
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(w); err != nil {
		db.Close() //nolint:errcheck
		w.Close()  //nolint:errcheck
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db, w: w, path: path, dsn: dsn}, nil
}

// migrate applies in-place column additions that CREATE TABLE IF NOT EXISTS
// can't express (idempotent against an existing database).
func migrate(w *sql.DB) error {
	// positions.user_id (multi-user scoping); NULL = legacy/unowned.
	var n int
	if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('positions') WHERE name='user_id'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := w.Exec(`ALTER TABLE positions ADD COLUMN user_id INTEGER`); err != nil {
			return err
		}
	}
	// broad-universe wave: symbols.stream distinguishes the STREAMED HOT SET
	// (stream=1: live ws + full 1m pipeline, bounded by the free ws cap) from
	// the BROAD DAILY-ONLY universe (stream=0: REST daily bars only, hundreds
	// of names). Least-invasive design: the daily universe still lives in the
	// one `symbols` table (so bars/rankings/correlation/regime/per-symbol daily
	// models all key on symbol_id) — a bool column, not a second table. Legacy
	// rows default to 0; the seeded hot set is promoted to 1 on boot.
	if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('symbols') WHERE name='stream'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := w.Exec(`ALTER TABLE symbols ADD COLUMN stream INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
		// One-time transition: every STOCK that was ALREADY active before the
		// hot/broad split existed WAS being streamed, so promote them to
		// stream=1. This runs exactly once (only when the column is first
		// added), before any daily-only universe symbol exists — so it can
		// never accidentally stream a broad-universe name. Scoped to stocks
		// because the stream cap models Alpaca's stock ws (crypto streams via
		// TickStream). New daily-only symbols afterwards default to stream=0.
		if _, err := w.Exec(`UPDATE symbols SET stream=1 WHERE active=1 AND market='stocks'`); err != nil {
			return err
		}
	}
	// Survivorship wave (2026-07-24): delisted_at records a MARKET fact (the
	// symbol stopped trading), distinct from active=0 which is a SUBSCRIPTION
	// decision. Research iterated active=1 and silently dropped every name that
	// died — the bias that most inflates oversold/mean-reversion studies. With
	// this column a point-in-time universe is reconstructable (TradableAt).
	if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('symbols') WHERE name='delisted_at'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := w.Exec(`ALTER TABLE symbols ADD COLUMN delisted_at INTEGER`); err != nil {
			return err
		}
	}
	// research discovery engine wave: decay-tracker fields + a machine-readable
	// rule spec on ledger hypotheses. The table already exists on live DBs (the
	// ledger shipped before this wave), so these ride the same
	// pragma_table_info-guarded ALTER path as the columns above.
	for _, col := range []struct{ name, ddl string }{
		{"peak_posterior", `ALTER TABLE research_ledger_hypotheses ADD COLUMN peak_posterior REAL NOT NULL DEFAULT 0`},
		{"peak_ts", `ALTER TABLE research_ledger_hypotheses ADD COLUMN peak_ts INTEGER NOT NULL DEFAULT 0`},
		{"last_grade_ts", `ALTER TABLE research_ledger_hypotheses ADD COLUMN last_grade_ts INTEGER NOT NULL DEFAULT 0`},
		{"spec", `ALTER TABLE research_ledger_hypotheses ADD COLUMN spec TEXT NOT NULL DEFAULT ''`},
		// Tradability gate: the position a belief implies, and the run that
		// graded it net of costs. Empty on every pre-existing row, which is the
		// truthful state — those hypotheses were never stated as trades — and
		// which holds them at "tentative" until someone states one.
		{"tradable_form", `ALTER TABLE research_ledger_hypotheses ADD COLUMN tradable_form TEXT NOT NULL DEFAULT ''`},
		{"economic_test", `ALTER TABLE research_ledger_hypotheses ADD COLUMN economic_test TEXT NOT NULL DEFAULT ''`},
	} {
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('research_ledger_hypotheses') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	// sentiment-correlation wave: the deterministic LEXICON score lives beside
	// the LLM tagger's verdict rather than overwriting it. The two answer
	// different questions — the LLM one is better per headline, the lexicon one
	// is the only one that can score twelve years of archive reproducibly — and
	// keeping both means the study can be re-run without a 2,000-call/day cap
	// and the news feed keeps showing the richer read.
	//
	// lex_polar is the load-bearing column: 0 means the headline expressed NO
	// opinion, which is not the same as an opinion of zero, and the feature
	// builder drops those rows rather than averaging them in.
	for _, col := range []struct{ name, ddl string }{
		{"lex_score", `ALTER TABLE news ADD COLUMN lex_score REAL NOT NULL DEFAULT 0`},
		{"lex_ver", `ALTER TABLE news ADD COLUMN lex_ver INTEGER NOT NULL DEFAULT 0`},
		{"lex_polar", `ALTER TABLE news ADD COLUMN lex_polar INTEGER NOT NULL DEFAULT 0`},
		{"lex_hedged", `ALTER TABLE news ADD COLUMN lex_hedged INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('news') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	// lex_ver=0 marks a headline the current lexicon has never scored, so the
	// scorer's work queue is an index lookup rather than a full-table scan of a
	// growing archive.
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS idx_news_lex_pending ON news (lex_ver)`); err != nil {
		return err
	}
	return nil
}

// ReaderClone returns a Store that READS through its own private connection
// pool while SHARING this Store's single write connection.
//
// WHY (measured 2026-07-16): one 4-connection read pool served ~30 background
// workers AND every interactive API handler. When the worker fleet scanned the
// multi-GB database concurrently, all four connections were held and API reads
// queued behind multi-second scans — a 61s /api/honesty. Giving interactive
// traffic its own pool means batch work cannot starve it. WAL readers never
// block each other in SQLite, so extra read connections are cheap and safe.
//
// The writer is deliberately SHARED (the same *sql.DB pointer, not a new one):
// the single-writer discipline is what keeps SQLITE_BUSY off this database, and
// a second write connection would reintroduce exactly that. A clone's Close
// therefore closes only its own read pool.
func (s *Store) ReaderClone(maxConns int) (*Store, error) {
	if maxConns <= 0 {
		maxConns = 4
	}
	db, err := sql.Open("sqlite", s.dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxConns)
	return &Store{db: db, w: s.w, path: s.path, dsn: s.dsn, borrowedWriter: true}, nil
}

// Close closes the database. A ReaderClone closes only its own read pool — the
// write connection belongs to the parent Store.
func (s *Store) Close() error {
	err := s.db.Close()
	if s.borrowedWriter {
		return err
	}
	if werr := s.w.Close(); err == nil {
		err = werr
	}
	return err
}

// DB exposes the raw handle for read-only ad-hoc queries (export endpoints).
func (s *Store) DB() *sql.DB { return s.db }

// ── symbols ─────────────────────────────────────────────────────────────

// UpsertSymbol inserts or reactivates a symbol and returns its row.
func (s *Store) UpsertSymbol(ctx context.Context, symbol string, market md.Market, name string) (md.Symbol, error) {
	now := time.Now().Unix()
	_, err := s.w.ExecContext(ctx, `
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
	var active, stream int
	var mkt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, symbol, market, name, active, added_at, stream FROM symbols WHERE symbol=? AND market=?`,
		symbol, string(market)).Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt, &stream)
	sym.Market, sym.Active, sym.Stream = md.Market(mkt), active == 1, stream == 1
	return sym, err
}

// GetSymbolByID fetches one symbol by id.
func (s *Store) GetSymbolByID(ctx context.Context, id int64) (md.Symbol, error) {
	var sym md.Symbol
	var active, stream int
	var mkt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, symbol, market, name, active, added_at, stream FROM symbols WHERE id=?`,
		id).Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt, &stream)
	sym.Market, sym.Active, sym.Stream = md.Market(mkt), active == 1, stream == 1
	return sym, err
}

// ListSymbols returns all symbols (activeOnly filters to live subscriptions).
func (s *Store) ListSymbols(ctx context.Context, activeOnly bool) ([]md.Symbol, error) {
	q := `SELECT id, symbol, market, name, active, added_at, stream FROM symbols`
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
		var active, stream int
		var mkt string
		if err := rows.Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt, &stream); err != nil {
			return nil, err
		}
		sym.Market, sym.Active, sym.Stream = md.Market(mkt), active == 1, stream == 1
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
	_, err := s.w.ExecContext(ctx, `UPDATE symbols SET active=? WHERE id=?`, v, id)
	return err
}

// ── bars ────────────────────────────────────────────────────────────────

// UpsertBars writes bars idempotently (REPLACE on the composite key).
func (s *Store) UpsertBars(ctx context.Context, bars []md.Bar) error {
	if len(bars) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
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

// BarAtOrBefore returns the last bar with ts <= t (base price for outcome
// resolution).
func (s *Store) BarAtOrBefore(ctx context.Context, symbolID int64, tf md.Timeframe, t int64) (md.Bar, bool, error) {
	b := md.Bar{SymbolID: symbolID, TF: tf}
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, open, high, low, close, volume FROM bars
		WHERE symbol_id=? AND tf=? AND ts<=? ORDER BY ts DESC LIMIT 1`,
		symbolID, string(tf), t).Scan(&b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume)
	if err == sql.ErrNoRows {
		return b, false, nil
	}
	return b, err == nil, err
}

// Rollup aggregates a finer timeframe into a coarser one over [from, to).
// bucket is the coarse bar length in seconds (3600 for 1h, 86400 for 1d).
func (s *Store) Rollup(ctx context.Context, symbolID int64, src, dst md.Timeframe, bucket, from, to int64) error {
	_, err := s.w.ExecContext(ctx, `
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

// RollupMissing is Rollup with INSERT OR IGNORE: it fills coarse buckets that
// do not exist yet and never overwrites ones that do. Used by retention
// compaction, where an already-present 1h bar (e.g. backfilled directly from
// the source) is authoritative and must not be replaced by an aggregate of
// possibly-partial finer bars.
func (s *Store) RollupMissing(ctx context.Context, symbolID int64, src, dst md.Timeframe, bucket, from, to int64) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
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
// Daily bars are the permanent record and are NEVER pruned — enforced here in
// code (not just by caller convention) so no future caller can violate it.
func (s *Store) PruneBars(ctx context.Context, tf md.Timeframe, cutoff int64) (int64, error) {
	if tf == md.TF1d {
		return 0, fmt.Errorf("store: daily bars are never pruned (permanence guarantee)")
	}
	res, err := s.w.ExecContext(ctx, `DELETE FROM bars WHERE tf=? AND ts<?`, string(tf), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── crypto 1s snapshots ─────────────────────────────────────────────────

// InsertSnap1s writes one microstructure snapshot (idempotent per second).
func (s *Store) InsertSnap1s(ctx context.Context, sn md.Snap1s) error {
	_, err := s.w.ExecContext(ctx, `
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
	res, err := s.w.ExecContext(ctx, `DELETE FROM snapshots_1s WHERE ts<?`, cutoff)
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
	tx, err := s.w.BeginTx(ctx, nil)
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

// UnresolvedOutcomesByHorizon returns pending outcomes for ONE horizon whose
// ts is at or before cutoff. Querying per-horizon (rather than one ts-ordered
// queue across all horizons) is essential: at steady state each symbol carries
// ~10k immature 1w rows spanning a week, which would otherwise sit ahead of
// every freshly-mature 1h/1d row in ts order and starve them indefinitely.
func (s *Store) UnresolvedOutcomesByHorizon(ctx context.Context, h md.Horizon, cutoff int64, limit int) ([]md.ScoreOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, horizon, ts, score FROM score_outcomes
		WHERE resolved_at IS NULL AND horizon=? AND ts<=? ORDER BY ts LIMIT ?`,
		string(h), cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.ScoreOutcome
	for rows.Next() {
		var o md.ScoreOutcome
		var hz string
		if err := rows.Scan(&o.SymbolID, &hz, &o.Ts, &o.Score); err != nil {
			return nil, err
		}
		o.Horizon = md.Horizon(hz)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ResolveOutcome records the realized forward return for one score.
func (s *Store) ResolveOutcome(ctx context.Context, symbolID int64, h md.Horizon, ts int64, fwdReturn float64) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE score_outcomes SET fwd_return=?, resolved_at=?
		WHERE symbol_id=? AND horizon=? AND ts=?`,
		fwdReturn, time.Now().Unix(), symbolID, string(h), ts)
	return err
}

// ResolveOutcomeVoid marks an outcome permanently unresolvable (no forward
// data ever arrived — delisted symbol, dead feed) so it stops clogging the
// unresolved queue. fwd_return stays NULL; the honesty page excludes it.
func (s *Store) ResolveOutcomeVoid(ctx context.Context, symbolID int64, h md.Horizon, ts int64) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE score_outcomes SET resolved_at=? WHERE symbol_id=? AND horizon=? AND ts=?`,
		time.Now().Unix(), symbolID, string(h), ts)
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
	tx, err := s.w.BeginTx(ctx, nil)
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

// InsertInsight stores one readable insight. An unset Data ("" — the Go zero
// value, e.g. the Risk watcher persists no evidence blob) is stored as '{}'
// (the schema's own DEFAULT; the column is NOT NULL): SQLite's json_extract
// raises "malformed JSON" on ” and one such row made every json-filtered
// insights query (InsightsByKind → /api/dashboard feed) fail outright
// (found in Stage 6 verify).
func (s *Store) InsertInsight(ctx context.Context, in md.Insight) error {
	data := in.Data
	if data == "" {
		data = "{}"
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO insights (scope, symbol_id, ts, headline, body, data)
		VALUES (?,?,?,?,?,?)`,
		in.Scope, in.SymbolID, in.Ts, in.Headline, in.Body, data)
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
	res, err := s.w.ExecContext(ctx,
		`INSERT INTO worker_runs (worker, started_at, status) VALUES (?,?,'running')`,
		worker, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishWorkerRun closes a run record.
func (s *Store) FinishWorkerRun(ctx context.Context, id int64, status, detail string) error {
	_, err := s.w.ExecContext(ctx,
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

// PruneWorkerRuns keeps the run log bounded. Two retention rules combine:
// the newest `keep` rows globally, PLUS the newest 20 rows PER WORKER. The
// per-worker floor keeps rare-cadence agents (13f-poller: 24h, backup: 24h,
// congress-poller: 12h, …) visible on the Agents page — without it, one
// flapping high-frequency worker (e.g. crypto-live erroring every ~5s while
// tickstream is down) floods the global window within hours and erases every
// trace that the slow workers ever ran.
func (s *Store) PruneWorkerRuns(ctx context.Context, keep int) error {
	_, err := s.w.ExecContext(ctx, `
		DELETE FROM worker_runs WHERE id NOT IN
		  (SELECT id FROM worker_runs ORDER BY started_at DESC, id DESC LIMIT ?)
		AND id NOT IN
		  (SELECT id FROM (
		     SELECT id, ROW_NUMBER() OVER
		       (PARTITION BY worker ORDER BY started_at DESC, id DESC) AS rn
		     FROM worker_runs) WHERE rn <= 20)`, keep)
	return err
}

// ── data quality ────────────────────────────────────────────────────────

// InsertDQ records a data-quality incident.
func (s *Store) InsertDQ(ctx context.Context, ev md.DQEvent) error {
	_, err := s.w.ExecContext(ctx,
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
	_, err := s.w.ExecContext(ctx, `
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
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO meta (k, v) VALUES (?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// SetJSON stores a JSON-marshaled value under a meta key (small blobs only).
func (s *Store) SetJSON(ctx context.Context, k string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.SetMeta(ctx, k, string(b))
}

// GetJSONRaw returns the raw JSON string stored under a meta key ("" if absent).
func (s *Store) GetJSONRaw(ctx context.Context, k string) (string, error) {
	return s.GetMeta(ctx, k)
}

// IncrAndGetSpend implements llm.SpendStore: it atomically increments and
// returns the persisted daily LLM call counter (meta key "llm_spend:<day>"),
// so a daemon restart cannot reset the spend cap. Old day keys are pruned
// opportunistically on the first call of a new day.
func (s *Store) IncrAndGetSpend(ctx context.Context, day string) (int, error) {
	key := "llm_spend:" + day
	var n int
	err := s.w.QueryRowContext(ctx, `
		INSERT INTO meta (k, v) VALUES (?, '1')
		ON CONFLICT(k) DO UPDATE SET v=CAST(CAST(v AS INTEGER)+1 AS TEXT)
		RETURNING CAST(v AS INTEGER)`, key).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n == 1 { // first call today: sweep stale day counters
		_, _ = s.w.ExecContext(ctx, `DELETE FROM meta WHERE k LIKE 'llm_spend:%' AND k < ?`, key)
	}
	return n, nil
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

// ─────────────────────────────────────────────────────────────────────────
// TIERED-STORAGE WAVE (appended block — keep at END of store.go so parallel
// edits by other agents never collide). Read helpers that feed the cold
// archive before a retention prune, a symbol-name map for archive filenames,
// and storage-governor primitives (WAL checkpoint + VACUUM + size probes).
// ─────────────────────────────────────────────────────────────────────────

// SymbolNameMap returns id → symbol for every symbol (active or not). Used to
// name cold-archive files by their human symbol rather than a raw id.
func (s *Store) SymbolNameMap(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, symbol FROM symbols`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var sym string
		if err := rows.Scan(&id, &sym); err != nil {
			return nil, err
		}
		out[id] = sym
	}
	return out, rows.Err()
}

// BarsBelow returns up to limit bars of timeframe tf with ts < cutoff, ordered
// by ts ascending (oldest first). This is the archive-before-prune read: the
// caller archives the returned rows, then prunes the SAME [<cutoff) predicate.
// A positive limit lets the caller archive+prune in bounded batches so a huge
// backlog never buffers the whole table in memory.
func (s *Store) BarsBelow(ctx context.Context, tf md.Timeframe, cutoff int64, limit int) ([]md.Bar, error) {
	q := `SELECT symbol_id, ts, open, high, low, close, volume FROM bars
	      WHERE tf=? AND ts<? ORDER BY ts LIMIT ?`
	if limit <= 0 {
		limit = 1 << 30 // effectively unbounded, but keeps the LIMIT clause
	}
	rows, err := s.db.QueryContext(ctx, q, string(tf), cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Bar
	for rows.Next() {
		b := md.Bar{TF: tf}
		if err := rows.Scan(&b.SymbolID, &b.Ts, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SnapsBelow returns up to limit snapshots_1s rows with ts < cutoff, ts asc.
// Same archive-before-prune contract as BarsBelow.
func (s *Store) SnapsBelow(ctx context.Context, cutoff int64, limit int) ([]md.Snap1s, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, bid, ask, mid, wmid, imb_signed, spread, apply_lat_ns
		FROM snapshots_1s WHERE ts<? ORDER BY ts LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Snap1s
	for rows.Next() {
		var sn md.Snap1s
		if err := rows.Scan(&sn.SymbolID, &sn.Ts, &sn.Bid, &sn.Ask, &sn.Mid, &sn.WMid, &sn.ImbSigned, &sn.Spread, &sn.ApplyLatNs); err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// FileSizes returns the current on-disk sizes of the database file and its WAL
// sidecar. Missing files count as 0 (not an error) — a checkpoint can legally
// leave a 0-byte WAL.
func (s *Store) FileSizes() (dbBytes, walBytes int64) {
	if fi, err := os.Stat(s.path); err == nil {
		dbBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		walBytes = fi.Size()
	}
	return
}

// WALCheckpointResult reports what a checkpoint ACTUALLY did — SQLite reports
// partial and blocked checkpoints through the pragma's result row, not through
// an error, so a caller that ignores the row cannot tell "truncated" from
// "did nothing".
type WALCheckpointResult struct {
	// Busy is true when the checkpoint could NOT complete: a TRUNCATE needs a
	// moment with no active readers, and a busy fleet may never grant one.
	Busy bool
	// LogFrames is the WAL length in frames; Checkpointed is how many were
	// moved back into the database. Busy && Checkpointed==0 means nothing
	// happened at all.
	LogFrames, Checkpointed int
}

// Truncated reports whether the WAL was actually flushed AND truncated.
func (r WALCheckpointResult) Truncated() bool { return !r.Busy }

// WALCheckpointTruncate runs PRAGMA wal_checkpoint(TRUNCATE): it flushes the
// WAL into the main database and then truncates the WAL file to zero, bounding
// the single biggest source of unbounded disk growth in a busy WAL database.
// Run on the WRITE connection so it can't race a concurrent writer.
//
// It RETURNS THE OUTCOME rather than discarding it. The previous form scanned
// (busy, log, checkpointed) and threw all three away with the comment "we only
// care about errors" — so the governor logged "checkpointed wal" every hour
// while a reader-starved TRUNCATE did nothing and the WAL grew unbounded
// (observed live 2026-07-16: three consecutive passes reporting success with
// the WAL frozen at exactly 254.1MB). Reporting an action the return value
// says did not happen is precisely the honesty failure this codebase forbids.
func (s *Store) WALCheckpointTruncate(ctx context.Context) (WALCheckpointResult, error) {
	var busy, logFrames, ckpt int
	err := s.w.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &ckpt)
	if err == sql.ErrNoRows {
		return WALCheckpointResult{}, nil
	}
	if err != nil {
		return WALCheckpointResult{}, err
	}
	return WALCheckpointResult{Busy: busy == 1, LogFrames: logFrames, Checkpointed: ckpt}, nil
}

// Vacuum runs a full VACUUM to reclaim free pages left behind by retention
// deletes (the DB is auto_vacuum=NONE, so freed pages are otherwise only
// reused, never returned to the filesystem). VACUUM briefly takes a write lock
// and rewrites the file, so the governor gates it behind a size threshold and
// runs it rarely. On the write connection.
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx, `VACUUM`)
	return err
}
