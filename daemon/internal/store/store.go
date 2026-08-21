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
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
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
	// id is this instance's process-unique identity, for callers that key a
	// cache by "which store produced this". See CacheKey.
	id uint64
}

// storeSeq issues store identities. It is monotonic on purpose: the identity it
// replaces was the store's ADDRESS, and Go reuses addresses as soon as an
// allocation is unreachable.
var storeSeq atomic.Uint64

// CacheKey is a process-unique identity for this store instance, for keying
// package-level caches that must never serve one store's aggregation to
// another.
//
// It exists because the obvious identity — fmt.Sprintf("%p", st) — is not one.
// Measured 2026-07-27: opening and closing 40 stores in sequence yielded 26
// distinct addresses, so a third of them inherited a dead predecessor's cache
// entries. Live, with one store per process, that never bites; in the test
// suite it made internal/api fail at random on whichever test happened to reuse
// an address, which is how a suite's failures stop being read.
func (s *Store) CacheKey() string {
	if s == nil {
		return "nil-store"
	}
	return "st" + strconv.FormatUint(s.id, 10)
}

// ReadConnMaxLifetime / ReadConnMaxIdleTime bound how long ANY read connection
// in ANY read pool (the daemon's own and every ReaderClone) may live.
//
// WHY (measured 2026-07-26/27): a WAL frame cannot be checkpointed past the
// oldest read snapshot still open, so a long-lived reader pins the WAL
// indefinitely — TRUNCATE stalled at the IDENTICAL frame index 581124 at 05:06,
// 06:06 and 08:09 while the WAL reached 5,396MB against a 2,233MB database, and
// journal_size_limit cannot help because it only applies once a checkpoint
// COMPLETES. database/sql recycles connections only when told to, and nothing
// in this tree told it to: every read connection was unbounded, so the daemon
// could not even rule its own pools out as the starver.
//
// The bound is chosen to sit far above any legitimate query (the slowest
// full-universe scans are seconds; worker deadlines are 15m at the outside for
// work that does not hold one snapshot throughout) and far below the hourly
// checkpoint window, so by the time a checkpoint runs, no reader from the
// previous pass can still be holding frames. It bounds a LIFETIME; it does not
// interrupt a query in flight — database/sql retires the connection only once
// it is returned to the pool.
//
// The single WRITE connection is deliberately left unbounded: it is the
// serialization point for the whole daemon and recycling it buys nothing (a
// writer does not pin old WAL frames the way a read snapshot does).
const (
	ReadConnMaxLifetime = 3 * time.Minute
	ReadConnMaxIdleTime = 1 * time.Minute
)

// boundReadConns applies the read-connection lifetime bounds. Every read pool
// in the process — Open's and every ReaderClone's — goes through here so the
// bound is a property of the store, not of any one call site.
func boundReadConns(db *sql.DB) {
	db.SetConnMaxLifetime(ReadConnMaxLifetime)
	db.SetConnMaxIdleTime(ReadConnMaxIdleTime)
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	// journal_size_limit caps the WAL FILE: once a checkpoint completes, SQLite
	// truncates the WAL back to this bound instead of letting it grow without
	// limit. Before this, a slow read (e.g. the old full-universe screener) held
	// old WAL frames long enough for concurrent writes to push the file past
	// 1GB, which the live governor's TRUNCATE checkpoint could never reclaim
	// while readers stayed active. The first bound (256MB) just let the WAL sit
	// at 256MB forever (observed live 2026-07-26): the limit is a ceiling the
	// file settles AT, not below. 64MB is still far above the per-checkpoint
	// working set while returning ~200MB to disk.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=journal_size_limit(67108864)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Read pool: WAL readers don't block each other.
	db.SetMaxOpenConns(4)
	boundReadConns(db)
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
	// After migrate, the declared schema and the live database MUST agree. They
	// can silently disagree because schema.sql is CREATE TABLE IF NOT EXISTS: a
	// column added to a CREATE statement rather than to migrate() lands on a
	// fresh test DB and never on production. Refusing to open is the point —
	// the alternative is workers reporting status='ok' while the units they
	// claim to record have nowhere to go.
	if err := verifySchema(w); err != nil {
		db.Close() //nolint:errcheck
		w.Close()  //nolint:errcheck
		return nil, err
	}
	st := &Store{db: db, w: w, path: path, dsn: dsn, id: storeSeq.Add(1)}
	// A worker whose only product is an audit record must not be allowed to
	// start when it has nowhere to write that record (see AuditRecordWorkers).
	// verifySchema catches divergence from the DECLARATION; this catches the
	// case where the contracted object is absent for any reason at all.
	if err := st.VerifyAuditContract(context.Background()); err != nil {
		db.Close() //nolint:errcheck
		w.Close()  //nolint:errcheck
		return nil, err
	}
	// Deploy-time reconstruction of the research loop's look count. Those
	// nightly searches were taken and their multiplicity spent; the durable
	// ledger arrived later, and starting it at zero would refund every look.
	// Best-effort by design: it seeds a counter that is a max over sources, so
	// a failure here can only under-charge, never manufacture a survivor.
	_, _ = st.BackfillLoopRuns(context.Background())
	return st, nil
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
	// time-integrity wave: the sub-second part of a pre-registration instant, so
	// freeze order is provable when one registrar pass writes several records
	// inside the same second. Not an input to entry_hash, so adding it leaves
	// every existing chain link verifying exactly as before; pre-existing rows
	// keep 0, which reads as "sub-second order unrecorded".
	for _, col := range []struct{ name, ddl string }{
		{"ts_nanos", `ALTER TABLE prereg_records ADD COLUMN ts_nanos INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('prereg_records') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	// leg-audit wave: the blend's WEIGHTS and the tier that supplied them.
	// predictions.components already records what each leg said; nothing
	// recorded how much each was believed, so a retired ensemble could not be
	// diagnosed leg by leg — which is why it had to be retired whole. Existing
	// rows keep '', which reads as "not recorded", never as "equal weights".
	for _, col := range []struct{ name, ddl string }{
		{"weights", `ALTER TABLE predictions ADD COLUMN weights TEXT NOT NULL DEFAULT ''`},
		{"basis", `ALTER TABLE predictions ADD COLUMN basis TEXT NOT NULL DEFAULT ''`},
	} {
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('predictions') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	// basis-marker wave: which label-and-signal basis produced an outcome row.
	// The live DB already carries this column (it was added out of band and left
	// 100% NULL on 525,301 rows), so the guard below is what makes the schema
	// file and the live schema agree instead of drifting. Pre-existing rows stay
	// NULL, which reads as "basis predates the marker" — the honest answer, since
	// nothing can retroactively know which build wrote them. See BasisEpoch.
	for _, col := range []struct{ name, ddl string }{
		{"basis_epoch", `ALTER TABLE prediction_outcomes ADD COLUMN basis_epoch INTEGER`},
		// settle_ts is the BASE BAR this row was graded from, and it is the true
		// independence unit — two predictions share an outcome exactly when they
		// share a base bar.
		//
		// trading_day(ts) is not that unit off a 24/7 market. base is
		// BarAtOrBefore(ts), so a Saturday prediction takes Friday's base and
		// Monday's forward, identical to Friday's own. Measured 2026-08-08:
		// 32.9% of consecutive stock symbol-day pairs carried an IDENTICAL
		// label (Sun 84.7%, Sat 64.9%, crypto 0.0%) while every day-clustered
		// statistic counted them as separate observations. Folding weekends into
		// trading_day() would be wrong in the other direction — for crypto,
		// Saturday IS an independent session.
		//
		// NULL on pre-existing rows, which reads as "unknown settle bar" rather
		// than as a claim; BackfillSettleTs fills them from the bars table.
		{"settle_ts", `ALTER TABLE prediction_outcomes ADD COLUMN settle_ts INTEGER`},
	} {
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('prediction_outcomes') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	// settled-move wave, part 2: the SAME independence key on the other two
	// outcome tables whose day-clustered statistics feed a published surface.
	// The reasoning is identical to prediction_outcomes above and is not repeated
	// here — see that comment and md.SettleDay.
	//
	// Deliberately NOT extended to two tables that also fold by day:
	//   - prediction_ledger is an append-only HASH CHAIN (prev_hash/entry_hash)
	//     backing the pre-registration record. Its day counts are a reporting
	//     convenience; churning its schema to improve them trades a real
	//     integrity guarantee for a cosmetic one.
	//   - regime_outcomes stores `day` as part of a UNIQUE dedup index
	//     (symbol_id, kind, day), so redefining it changes which rows are
	//     WRITTEN, not merely how they are counted. That needs its own staged
	//     change with an index rebuild, not a column bolted on beside it.
	for _, t := range []struct{ table, ddl string }{
		{"score_outcomes", `ALTER TABLE score_outcomes ADD COLUMN settle_ts INTEGER`},
		{"confluence_outcomes", `ALTER TABLE confluence_outcomes ADD COLUMN settle_ts INTEGER`},
	} {
		if err := w.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='settle_ts'`, t.table).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(t.ddl); err != nil {
				return err
			}
		}
	}
	// confluence pseudo-replication wave (2026-08-21): two columns that make the
	// published confluence population defensible.
	//
	// entry_ts records WHICH bar the graded return's entry leg came from. The
	// resolver read the entry with BarAtOrBefore and no lower bound, so a symbol
	// with no bar in its own bucket day was graded against a bar from days
	// earlier — and because the forward read has the same fallback, three
	// calendar buckets could resolve to ONE (entry, exit) pair. RNWWW booked the
	// identical +93.33% move on 2026-07-17, 07-18 and 07-19: one price
	// observation entering the mean three times as three "independent bets".
	//
	// episode_ts groups consecutive same-direction days into ONE episode. A setup
	// that persists is one position held, not one new bet per day.
	for _, col := range []struct{ name, ddl string }{
		{"entry_ts", `ALTER TABLE confluence_outcomes ADD COLUMN entry_ts INTEGER`},
		{"episode_ts", `ALTER TABLE confluence_outcomes ADD COLUMN episode_ts INTEGER`},
		// The CONSTRAINED basis needs price LEVELS, not just the ratio: a
		// tradable-minimum test cannot be run on a return. These are stamped by
		// the resolver, which already holds both bars, so the public read costs
		// no extra lookups and cannot drift if a bar is later revised.
		{"entry_close", `ALTER TABLE confluence_outcomes ADD COLUMN entry_close REAL`},
		{"exit_low", `ALTER TABLE confluence_outcomes ADD COLUMN exit_low REAL`},
		{"exit_high", `ALTER TABLE confluence_outcomes ADD COLUMN exit_high REAL`},
	} {
		if err := w.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('confluence_outcomes') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	if _, err := w.Exec(
		`CREATE INDEX IF NOT EXISTS idx_confl_out_episode ON confluence_outcomes(symbol_id, direction, ts)`); err != nil {
		return err
	}
	// multiplicity wave: the corrected divisor a loop hypothesis cleared. Live
	// DBs already hold rows from before the loop fed PriorSearches, and those
	// rows keep divisor=0 — the truthful state, meaning "correction unrecorded",
	// not a claim of a divisor of one.
	for _, col := range []struct{ name, ddl string }{
		{"divisor", `ALTER TABLE research_loop_hypotheses ADD COLUMN divisor INTEGER NOT NULL DEFAULT 0`},
		// rejection-ledger wave: Discover now returns every JUDGED rule, not
		// only its winners, so the row must carry how wide the search was
		// (grid_size), how much history judged it (weeks), which gate killed
		// it (rejected_by, '' for survivors) and the corpus span searched
		// (obs_window). Pre-existing rows keep the zero/empty defaults, which
		// truthfully read as "unrecorded" rather than as a claim.
		{"grid_size", `ALTER TABLE research_loop_hypotheses ADD COLUMN grid_size INTEGER NOT NULL DEFAULT 0`},
		{"weeks", `ALTER TABLE research_loop_hypotheses ADD COLUMN weeks INTEGER NOT NULL DEFAULT 0`},
		{"rejected_by", `ALTER TABLE research_loop_hypotheses ADD COLUMN rejected_by TEXT NOT NULL DEFAULT ''`},
		{"obs_window", `ALTER TABLE research_loop_hypotheses ADD COLUMN obs_window TEXT NOT NULL DEFAULT ''`},
		// measured-null wave: the win rate the Wilson bound was compared
		// against (null_p0) and how many week-trials measured it
		// (null_weeks). Pre-existing rows keep 0, which truthfully reads as
		// "the null this rule was judged against was not recorded" — those
		// rows were judged against the 0.5 literal.
		{"null_p0", `ALTER TABLE research_loop_hypotheses ADD COLUMN null_p0 REAL NOT NULL DEFAULT 0`},
		{"null_weeks", `ALTER TABLE research_loop_hypotheses ADD COLUMN null_weeks INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('research_loop_hypotheses') WHERE name=?`, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	// Indexed only now that the column is guaranteed to exist — see the note in
	// schema.sql. Rejections are the rows this table is most often queried by
	// (has this dead rule been re-tested?), so the index belongs with them.
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS idx_loop_hyp_rejected
		ON research_loop_hypotheses (rejected_by)`); err != nil {
		return err
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
	// naive-persistence null wave: the frozen "nothing changes" baseline beside
	// every structural regime call. Live DBs already hold regime_outcomes rows,
	// so it rides the same guarded ALTER path; pre-existing rows keep NULL,
	// which is the truthful state — no baseline was frozen for them.
	if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('regime_outcomes') WHERE name='naive_label'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := w.Exec(`ALTER TABLE regime_outcomes ADD COLUMN naive_label TEXT`); err != nil {
			return err
		}
	}
	// CODE-REVISION stamping wave: the first question anyone asks about a
	// published number is which code produced it, and until now the database
	// could not answer it for a single row. Same guarded ALTER path; existing
	// rows keep NULL/'' because their revision is genuinely unrecoverable, and
	// the grader treats that absence as a reason to refuse rather than as a
	// pass. Adding the column can only cause fewer verdicts, never more.
	for _, col := range []struct{ table, name, ddl string }{
		{"regime_outcomes", "revision", `ALTER TABLE regime_outcomes ADD COLUMN revision TEXT`},
		{"prediction_ledger", "revision", `ALTER TABLE prediction_ledger ADD COLUMN revision TEXT`},
		{"worker_runs", "revision", `ALTER TABLE worker_runs ADD COLUMN revision TEXT NOT NULL DEFAULT ''`},
		// Point-in-time corpus coverage of the searched evidence base. Rows
		// written before the measurement existed keep 0, which reads as
		// "unmeasured" — the same honest-absence convention as the columns
		// above, and the reason it is not defaulted to 1.
		{"research_loop_runs", "corpus_coverage", `ALTER TABLE research_loop_runs ADD COLUMN corpus_coverage REAL NOT NULL DEFAULT 0`},
	} {
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`,
			col.table, col.name).Scan(&n); err != nil {
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
	if err := migrateRegimeOutcomesToTradingDay(w); err != nil {
		return err
	}
	// Strictly after the trading-day fold: this one re-folds the SAME column a
	// second time and relies on superseded_by and the partial dedup index that
	// migration introduces.
	if err := migrateRegimeOutcomesToSettleDay(w); err != nil {
		return err
	}
	return nil
}

// migrateRegimeOutcomesToSettleDay re-folds regime_outcomes.day a second time,
// from the trading day onto the SETTLED MOVE, WITHOUT deleting anything.
//
// The trading-day fold fixed the after-close tail but not the weekend: a regime
// call frozen on Friday evening, Saturday and Sunday all describe the same
// Friday base bar and the same Friday->Monday move, yet the calendar fold
// admitted three. Measured on the live corpus before this ran: 32,688 live rows
// folding to 23,698 distinct (symbol, kind, settled-move) observations — 8,990
// rows, 27.5%, were the same call counted again. Every one of them had a 1d bar
// at or before it, so the fold is fully determined here and never falls back.
//
// Same repair shape as the trading-day migration above, and for the same reason:
// this table is the pre-registration audit trail with ts, regime, conviction and
// historical_accuracy frozen at call time, so a loser is marked superseded_by
// rather than deleted. Deleting would drop ungraded forecasts because the key
// that admitted them was wrong, which is a file drawer and a worse defect than
// the one being repaired. The EARLIEST call of the settled move wins, ties on
// the lower id, reproducing what INSERT OR IGNORE would have written had the key
// been right from the start.
//
// Two things it deliberately does NOT touch:
//   - regime_outcome_quarantine.day. Its rows are a frozen snapshot of what was
//     quarantined under the fold in force at freeze time, and quarantineMembers
//     digests outcome_id:symbol_id:kind:day into a pinned manifest. Rewriting
//     them would invalidate that digest to make a historical record agree with a
//     fold it predates. It reads only its own table, so leaving it alone keeps
//     the manifest verifiable.
//   - rows already carrying superseded_by. They lost an earlier dedup and stay
//     lost; re-examining them could resurrect a row the previous fold retired.
//
// Idempotent: guarded on settle_ts's absence, and one transaction so a crash
// midway leaves the trading-day shape intact rather than a half-folded table.
func migrateRegimeOutcomesToSettleDay(w *sql.DB) error {
	var n int
	if err := w.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('regime_outcomes') WHERE name='settle_ts'`).
		Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	tx, err := w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	stmts := []string{
		`ALTER TABLE regime_outcomes ADD COLUMN settle_ts INTEGER`,
		// The base bar the call describes. Derived exactly as every other
		// settle_ts on the platform is, so all four tables agree by construction.
		`UPDATE regime_outcomes SET settle_ts = (
		   SELECT MAX(b.ts) FROM bars b
		    WHERE b.symbol_id = regime_outcomes.symbol_id
		      AND b.tf = '1d' AND b.ts <= regime_outcomes.ts)`,
		// Drop first: re-folding `day` collides the weekend clusters under the
		// existing unique index.
		`DROP INDEX IF EXISTS idx_regime_outcomes_dedup`,
		`UPDATE regime_outcomes SET day = settle_day(settle_ts, ts)`,
		// Winners are materialised BEFORE any supersede write. Deciding the winner
		// inside the UPDATE would read superseded_by while the same statement is
		// setting it, so a row's fate could depend on how far the scan had got.
		`CREATE TEMP TABLE regime_settle_keep AS
		   SELECT id FROM (
		     SELECT id, ROW_NUMBER() OVER (
		              PARTITION BY symbol_id, kind, day ORDER BY ts ASC, id ASC) rn
		       FROM regime_outcomes WHERE superseded_by IS NULL)
		    WHERE rn = 1`,
		`UPDATE regime_outcomes AS r
		    SET superseded_by = (
		          SELECT k.id FROM regime_settle_keep k
		            JOIN regime_outcomes kw ON kw.id = k.id
		           WHERE kw.symbol_id = r.symbol_id AND kw.kind = r.kind
		             AND kw.day = r.day)
		  WHERE r.superseded_by IS NULL
		    AND r.id NOT IN (SELECT id FROM regime_settle_keep)`,
		// Collapse supersede CHAINS. A row retired by the earlier trading-day fold
		// points at the winner of THAT fold, and this fold can retire that winner
		// in turn — leaving the first row pointing at a row which is itself no
		// longer the representative. Measured on the live corpus: 4 such chains.
		// superseded_by is meant to answer "which row represents this observation
		// now", so it is repointed at the surviving winner of the group.
		//
		// Guarded on a live winner EXISTING: without that, a group with no live row
		// would have its members' superseded_by set to NULL, silently resurrecting
		// rows the dedup retired. Writes only to rows that are already superseded
		// and reads only rows that are not, so the read set cannot shift underneath
		// the statement.
		`UPDATE regime_outcomes AS r
		    SET superseded_by = (
		          SELECT w.id FROM regime_outcomes w
		           WHERE w.superseded_by IS NULL
		             AND w.symbol_id = r.symbol_id AND w.kind = r.kind AND w.day = r.day)
		  WHERE r.superseded_by IS NOT NULL
		    AND EXISTS (
		          SELECT 1 FROM regime_outcomes w
		           WHERE w.superseded_by IS NULL
		             AND w.symbol_id = r.symbol_id AND w.kind = r.kind AND w.day = r.day)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_regime_outcomes_dedup
		   ON regime_outcomes (symbol_id, kind, day) WHERE superseded_by IS NULL`,
		`DROP TABLE regime_settle_keep`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("regime_outcomes settled-move fold: %w", err)
		}
	}
	return tx.Commit()
}

// migrateRegimeOutcomesToTradingDay re-folds regime_outcomes.day from the old
// UTC-midnight cut onto the trading day, WITHOUT deleting anything.
//
// The problem it repairs: a US extended session closes 20:00 ET, which is 00:00Z
// under EDT and 01:00Z under EST, so under ts/86400 the tail of one trading day
// was frozen as a second call on the next day. Measured on the live corpus,
// 2,817 pairs — one row near 21:00Z and its partner between 01:00Z and 05:00Z,
// agreeing on the regime 98.2% of the time. Under the corrected fold they are
// one observation, and re-folding `day` therefore collides them on the dedup
// key.
//
// The repair keeps BOTH rows. This table is the pre-registration audit trail
// (ts, regime, conviction and historical_accuracy are all stamped frozen at call
// time) and every loser is UNRESOLVED, so deleting them would drop ungraded
// forecasts because the key that admitted them was wrong — a file drawer, and a
// worse defect than the one being fixed. The loser is marked superseded_by and
// the unique index becomes partial.
//
// The winner is the EARLIEST call in the trading day: had the fold been correct
// from the start, INSERT OR IGNORE would have kept that row and rejected its
// partner, so this reproduces the history the right key would have written.
//
// Idempotent: guarded on the column's absence, and runs as one transaction so a
// crash midway leaves the old shape intact rather than a half-folded table.
func migrateRegimeOutcomesToTradingDay(w *sql.DB) error {
	var n int
	if err := w.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('regime_outcomes') WHERE name='superseded_by'`).
		Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	tx, err := w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	stmts := []string{
		`ALTER TABLE regime_outcomes ADD COLUMN superseded_by INTEGER REFERENCES regime_outcomes(id)`,
		// Drop first: re-folding `day` collides pairs under the old total index.
		`DROP INDEX IF EXISTS idx_regime_outcomes_dedup`,
		`UPDATE regime_outcomes SET day = trading_day(ts)`,
		// Earliest call of the trading day wins; ties break on the lower id so
		// the choice is total and reproducible.
		`UPDATE regime_outcomes AS r
		    SET superseded_by = (
		          SELECT w.id FROM regime_outcomes w
		           WHERE w.symbol_id = r.symbol_id AND w.kind = r.kind AND w.day = r.day
		           ORDER BY w.ts ASC, w.id ASC LIMIT 1)
		  WHERE EXISTS (
		          SELECT 1 FROM regime_outcomes w
		           WHERE w.symbol_id = r.symbol_id AND w.kind = r.kind AND w.day = r.day
		             AND (w.ts < r.ts OR (w.ts = r.ts AND w.id < r.id)))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_regime_outcomes_dedup
		   ON regime_outcomes (symbol_id, kind, day) WHERE superseded_by IS NULL`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("regime_outcomes trading-day fold: %w", err)
		}
	}
	return tx.Commit()
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
	boundReadConns(db)
	return &Store{db: db, w: s.w, path: s.path, dsn: s.dsn, borrowedWriter: true, id: storeSeq.Add(1)}, nil
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
	// A row carrying delisted_at is NOT reactivated. That column records a
	// MARKET fact: this ticker's company stopped trading. When an exchange
	// recycles the ticker, the new company's data arrives addressed to the same
	// string, and an unguarded `DO UPDATE SET active=1` silently resurrects the
	// dead row and splices two securities into one price series — measured on
	// ATC, which held Atotech's 2021-22 tape and a 2026 GraniteShares ETF on
	// one row with a 1,365-day hole in the middle. 716 rows carry a delisting
	// stamp today and every one of them was reachable this way, so the guard
	// belongs here, at the single point every caller routes through, rather
	// than in whichever caller happens to notice.
	//
	// A genuine re-listing under the same ticker therefore needs an operator to
	// clear delisted_at deliberately. That is the intended cost: resurrection
	// should be a decision, not a side effect of a poll.
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO symbols (symbol, market, name, active, added_at) VALUES (?,?,?,1,?)
		ON CONFLICT(symbol, market) DO UPDATE SET
		  active = CASE WHEN symbols.delisted_at IS NULL OR symbols.delisted_at = 0 THEN 1 ELSE symbols.active END,
		  name   = CASE WHEN excluded.name != '' THEN excluded.name ELSE symbols.name END`,
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

// BarsBefore returns the most recent n bars STRICTLY BEFORE t, ascending.
//
// The strictness is the point. This exists for measurements that must not see
// the bar they are about to act on — a barrier level sized from volatility at
// entry may only use bars that had already closed when the entry filled, and
// the entry fills at the open of bar t, whose own high, low and close are still
// unknown at that moment. `ts < t` is that rule expressed in SQL, where it
// cannot be forgotten by a caller.
func (s *Store) BarsBefore(ctx context.Context, symbolID int64, tf md.Timeframe, t int64, n int) ([]md.Bar, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, open, high, low, close, volume FROM
		  (SELECT * FROM bars WHERE symbol_id=? AND tf=? AND ts<? ORDER BY ts DESC LIMIT ?)
		ORDER BY ts`, symbolID, string(tf), t, n)
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
//
// settle_ts is stamped here for the same reason as in ResolvePrediction: it is
// the unit of independent evidence (md.SettleDay), and leaving it for the
// periodic backfill meant the newest rows — the ones live statistics lean on —
// were the last to carry the right key. Derivation identical to
// BackfillScoreSettleTs, so the two agree by construction.
func (s *Store) ResolveOutcome(ctx context.Context, symbolID int64, h md.Horizon, ts int64, fwdReturn float64) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE score_outcomes SET fwd_return=?, resolved_at=?,
		  settle_ts = (
		    SELECT MAX(b.ts) FROM bars b
		    WHERE b.symbol_id = score_outcomes.symbol_id
		      AND b.tf = '1d' AND b.ts <= score_outcomes.ts
		  )
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
	q := `SELECT symbol_id, horizon, ts, score, fwd_return, resolved_at, settle_ts
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
		var settle sql.NullInt64
		if err := rows.Scan(&o.SymbolID, &hz, &o.Ts, &o.Score, &fwd, &res, &settle); err != nil {
			return nil, err
		}
		o.SettleTs = settle.Int64 // 0 when NULL
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
		`INSERT INTO worker_runs (worker, started_at, status, revision) VALUES (?,?,'running',?)`,
		worker, time.Now().Unix(), CodeRevision())
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

// ReconcileOrphanRuns closes every run left non-terminal by a PREVIOUS process
// and returns how many it closed. A daemon that dies mid-run leaves its rows at
// 'running' forever; 77 such rows had accumulated before this existed.
//
// They are marked 'orphaned', never 'ok' — the outcome is genuinely unknown —
// and never deleted: ResearchLoop derives the Bonferroni multiplicity divisor
// from a max over worker_runs, so dropping a row refunds a look the fleet
// actually spent and LOOSENS the correction.
//
// Only rows started before bootUnix are touched, so this process's own
// in-flight runs are never mistaken for wreckage.
func (s *Store) ReconcileOrphanRuns(ctx context.Context, bootUnix int64) (int, error) {
	res, err := s.w.ExecContext(ctx,
		`UPDATE worker_runs SET finished_at=?, status='orphaned', detail=?
		   WHERE status='running' AND started_at < ?`,
		time.Now().Unix(),
		fmt.Sprintf("process died mid-run; swept at boot %d", bootUnix),
		bootUnix)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// LastWorkerRunAt returns when the named worker last STARTED a run, or the zero
// time if it never has (or the row has been pruned). It is what lets a calendar
// worker's NextFire survive a daemon restart: without it, every restart looks
// like a first boot and a weekly job would re-fire on each one.
//
// Deliberately "started", not "finished ok": a run that failed still consumed
// its slot, and the retry policy belongs to the worker's own NextFire, not to a
// silent re-fire from the scheduler.
func (s *Store) LastWorkerRunAt(ctx context.Context, worker string) (time.Time, error) {
	var started sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(started_at) FROM worker_runs WHERE worker = ?`, worker).Scan(&started)
	if err != nil || !started.Valid {
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		return time.Time{}, err
	}
	return time.Unix(started.Int64, 0), nil
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
//
// A third rule is an EXEMPTION rather than a quota: a research-loop row whose
// detail records a completed grid search ("searched a N-rule grid …") is never
// pruned. That line is the record of a look taken at the corpus, and looks are
// what the Bonferroni divisor charges for. Deleting one refunds multiplicity
// that was actually spent, which makes the bar easier to clear the longer the
// logs rotate — the exact opposite of the monotonicity the ledger claims.
// Refusals and same-day skips ("skip — …") took no look and stay prunable.
func (s *Store) PruneWorkerRuns(ctx context.Context, keep int) error {
	_, err := s.w.ExecContext(ctx, `
		DELETE FROM worker_runs WHERE id NOT IN
		  (SELECT id FROM worker_runs ORDER BY started_at DESC, id DESC LIMIT ?)
		AND id NOT IN
		  (SELECT id FROM (
		     SELECT id, ROW_NUMBER() OVER
		       (PARTITION BY worker ORDER BY started_at DESC, id DESC) AS rn
		     FROM worker_runs) WHERE rn <= 20)
		AND NOT (worker='research-loop' AND detail LIKE 'searched a%')`, keep)
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

// DeleteMetaPrefixExcept removes every meta row whose key starts with prefix,
// except the one key to keep. It is the pruning half of a content-addressed
// cache: the writer stores under a NEW key each time its inputs change, so
// without this the superseded rows accumulate forever.
//
// The prefix is matched with LIKE, so it must not contain the wildcards % or _
// unless the caller means them. Callers here build prefixes from a namespace
// and a symbol, which contain neither.
func (s *Store) DeleteMetaPrefixExcept(ctx context.Context, prefix, keep string) error {
	_, err := s.w.ExecContext(ctx,
		`DELETE FROM meta WHERE k LIKE ? || '%' AND k <> ?`, prefix, keep)
	return err
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

// walCheckpoint runs one PRAGMA wal_checkpoint(<mode>) on the WRITE connection
// and returns the pragma's own result row. mode is a fixed literal chosen by
// the callers below — never user input.
func (s *Store) walCheckpoint(ctx context.Context, mode string) (WALCheckpointResult, error) {
	var busy, logFrames, ckpt int
	err := s.w.QueryRowContext(ctx, `PRAGMA wal_checkpoint(`+mode+`)`).Scan(&busy, &logFrames, &ckpt)
	if err == sql.ErrNoRows {
		return WALCheckpointResult{}, nil
	}
	if err != nil {
		return WALCheckpointResult{}, err
	}
	return WALCheckpointResult{Busy: busy == 1, LogFrames: logFrames, Checkpointed: ckpt}, nil
}

// WALCheckpointPassive runs PRAGMA wal_checkpoint(PASSIVE): it copies whatever
// frames it can back into the main database WITHOUT waiting for readers or
// writers, and never blocks. It does not shrink the -wal FILE, but it does
// bound how far the WAL's live region grows, and its frame count is the honest
// measure of whether anything is being reclaimed at all.
//
// This is the bottom rung of the checkpoint ladder. It exists because the
// TRUNCATE-only design measured 0 successes in 22 attempts (BUSY 21) while the
// WAL reached 5,396 MB: TRUNCATE requires a reader-free instant that a fleet of
// ~97 workers on a shared read pool plus the API's ReaderClone never provides,
// so the ONLY checkpoint the daemon ever ran was the one that could not run.
func (s *Store) WALCheckpointPassive(ctx context.Context) (WALCheckpointResult, error) {
	return s.walCheckpoint(ctx, "PASSIVE")
}

// WALCheckpointRestart runs PRAGMA wal_checkpoint(RESTART): like FULL, it
// blocks until all frames are checkpointed, then forces the next writer to
// restart the WAL from frame 1. The file is not truncated, so its size is
// capped at the current high-water mark instead of growing without bound —
// the middle rung, reachable when readers are merely busy rather than
// permanently present.
func (s *Store) WALCheckpointRestart(ctx context.Context) (WALCheckpointResult, error) {
	return s.walCheckpoint(ctx, "RESTART")
}

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
	return s.walCheckpoint(ctx, "TRUNCATE")
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
