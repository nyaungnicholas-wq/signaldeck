// Historical research-weeks store (research discovery engine wave).
//
// research_weeks holds one point-in-time weekly observation per (symbol,
// calendar week), computed retrospectively from daily bars with strict
// no-lookahead discipline — the evidence base the research engine grades
// hypotheses against. Rows are recompute-idempotent (REPLACE on the key).
// Writes go through s.w, reads through the pooled s.db, matching the rest of
// the store.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// WeekBucketSecs mirrors histfeat.WeekSecs — the week bucket size
// research_weeks is keyed on. Duplicated as a literal rather than imported
// because store must not depend on the feature package; exported so the two
// can be pinned together by pipeline.TestCoverageWeekBucketMatchesHistfeat.
const WeekBucketSecs = 7 * 86400

// ResearchWeek is one persisted weekly research observation.
type ResearchWeek struct {
	SymbolID  int64              `json:"symbolId"`
	Week      int64              `json:"week"` // calendar-week bucket = Ts / 604800
	Ts        int64              `json:"ts"`   // anchor daily-bar ts (last bar of the week)
	Vec       map[string]float64 `json:"vec"`
	FwdReturn float64            `json:"fwdReturn"`
	Up        bool               `json:"up"`
	Era       string             `json:"era"`
	HighVol   bool               `json:"highVol"`
}

// UpsertResearchWeeks writes a batch of weekly rows idempotently (REPLACE on
// the (symbol_id, week) key) in a single transaction.
func (s *Store) UpsertResearchWeeks(ctx context.Context, rows []ResearchWeek, now int64) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO research_weeks
		  (symbol_id, week, ts, vec, fwd_return, up, era, high_vol, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck
	for _, r := range rows {
		vec, err := json.Marshal(r.Vec)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, r.SymbolID, r.Week, r.Ts, string(vec),
			r.FwdReturn, boolToInt(r.Up), r.Era, boolToInt(r.HighVol), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResearchWeeks returns every row in the [fromWeek, toWeek] inclusive
// week-bucket range, ordered (week, symbol_id). keys != nil projects each Vec
// down to those keys after unmarshal — the engine loads years of rows at once,
// so it must not hold full vectors it will never read.
func (s *Store) ResearchWeeks(ctx context.Context, fromWeek, toWeek int64, keys []string) ([]ResearchWeek, error) {
	var keep map[string]bool
	if keys != nil {
		keep = make(map[string]bool, len(keys))
		for _, k := range keys {
			keep[k] = true
		}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, week, ts, vec, fwd_return, up, era, high_vol
		FROM research_weeks WHERE week>=? AND week<=?
		ORDER BY week, symbol_id`, fromWeek, toWeek)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ResearchWeek
	for rows.Next() {
		var r ResearchWeek
		var vec string
		var up, highVol int
		if err := rows.Scan(&r.SymbolID, &r.Week, &r.Ts, &vec, &r.FwdReturn,
			&up, &r.Era, &highVol); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(vec), &r.Vec); err != nil {
			return nil, err
		}
		if keep != nil {
			for k := range r.Vec {
				if !keep[k] {
					delete(r.Vec, k)
				}
			}
		}
		r.Up = up != 0
		r.HighVol = highVol != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResearchWeekStats summarizes the historical evidence base: total rows,
// distinct symbols/weeks, the anchor-ts span, and the per-era row counts.
type ResearchWeekStats struct {
	Rows    int            `json:"rows"`
	Symbols int            `json:"symbols"`
	Weeks   int            `json:"weeks"`
	MinTs   int64          `json:"minTs"`
	MaxTs   int64          `json:"maxTs"`
	ByEra   map[string]int `json:"byEra"`

	// POINT-IN-TIME COVERAGE. The corpus is now built from the universe that
	// EXISTED, not the one that survived — but "we included the dead" is a
	// claim, and a claim about a study population has to be measured or it is
	// just a comment. For each week, coverage is (symbols contributing a
	// research row that week) / (symbols that actually printed a daily bar
	// that week). A corpus rebuilt off today's active list scores well below 1
	// in the early weeks, because the names that later left are missing from
	// the numerator while their bars sit in the denominator.
	//
	// It corrects no result and moves no reported accuracy. It BOUNDS the one
	// bias a Bonferroni-corrected, era-gated grid search cannot correct
	// internally, by publishing how much of each week's real market the search
	// actually saw. CoverageMin is the worst week — the honest headline,
	// because a mean hides exactly the early-history hole survivorship makes.
	CoverageMean  float64 `json:"coverageMean"`
	CoverageMin   float64 `json:"coverageMin"`
	CoverageWeeks int     `json:"coverageWeeks"`
}

// CoverageSummary renders point-in-time coverage for a worker summary line.
func (st ResearchWeekStats) CoverageSummary() string {
	if st.CoverageWeeks == 0 {
		return "pit-coverage: unmeasured (no weeks)"
	}
	return fmt.Sprintf("pit-coverage: mean %.0f%% / worst week %.0f%% over %d weeks",
		st.CoverageMean*100, st.CoverageMin*100, st.CoverageWeeks)
}

// ResearchWeeksStats returns the research_weeks summary (all zero values and
// an empty ByEra map when no backfill has run — the honest empty state).
func (s *Store) ResearchWeeksStats(ctx context.Context) (ResearchWeekStats, error) {
	st := ResearchWeekStats{ByEra: map[string]int{}}
	var mn, mx sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT symbol_id), COUNT(DISTINCT week), MIN(ts), MAX(ts)
		FROM research_weeks`).Scan(&st.Rows, &st.Symbols, &st.Weeks, &mn, &mx); err != nil {
		return st, err
	}
	if mn.Valid {
		st.MinTs = mn.Int64
	}
	if mx.Valid {
		st.MaxTs = mx.Int64
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT era, COUNT(*) FROM research_weeks GROUP BY era`)
	if err != nil {
		return st, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var era string
		var n int
		if err := rows.Scan(&era, &n); err != nil {
			return st, err
		}
		st.ByEra[era] = n
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	return st, s.researchWeekCoverage(ctx, &st)
}

// researchWeekCoverage fills the point-in-time coverage fields. Weeks with no
// daily bars at all are skipped rather than scored 0/0; weeks the corpus never
// reached (outside its ts span) are skipped too, since a corpus is not
// answerable for history it does not claim to cover.
func (s *Store) researchWeekCoverage(ctx context.Context, st *ResearchWeekStats) error {
	if st.Rows == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH corpus AS (
		  SELECT week, COUNT(DISTINCT symbol_id) AS n
		  FROM research_weeks GROUP BY week
		),
		market AS (
		  SELECT b.ts / ? AS week, COUNT(DISTINCT b.symbol_id) AS n
		  FROM bars b
		  JOIN symbols sy ON sy.id = b.symbol_id AND sy.market = ?
		  WHERE b.tf = '1d' AND b.ts >= ? AND b.ts <= ?
		  GROUP BY b.ts / ?
		)
		SELECT m.week, COALESCE(c.n, 0), m.n
		FROM market m LEFT JOIN corpus c ON c.week = m.week
		WHERE m.n > 0`,
		WeekBucketSecs, string(md.Stocks), st.MinTs, st.MaxTs, WeekBucketSecs)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck
	var sum, min float64
	min = 1
	n := 0
	for rows.Next() {
		var week int64
		var have, total int
		if err := rows.Scan(&week, &have, &total); err != nil {
			return err
		}
		cov := float64(have) / float64(total)
		if cov > 1 {
			cov = 1 // a symbol can carry a row for a week whose bars were pruned
		}
		sum += cov
		if cov < min {
			min = cov
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	st.CoverageWeeks = n
	st.CoverageMean = sum / float64(n)
	st.CoverageMin = min
	return nil
}

// EarliestBarTs returns the oldest bar timestamp stored for (symbol, tf), 0
// when none — the backfill worker's "is this symbol's history shallow?" probe.
func (s *Store) EarliestBarTs(ctx context.Context, symbolID int64, tf md.Timeframe) (int64, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MIN(ts), 0) FROM bars WHERE symbol_id=? AND tf=?`,
		symbolID, string(tf)).Scan(&ts)
	return ts, err
}

// ── autonomous research loop (2026-07-25) ────────────────────────────────────

// LoopHypothesis is one rule the automated loop discovered and judged.
type LoopHypothesis struct {
	ID          string  `json:"id"`
	Desc        string  `json:"desc"`
	Status      string  `json:"status"` // shadow | rejected — never "promoted"
	WilsonLower float64 `json:"wilsonLower"`
	// NullP0 is the win rate WilsonLower had to beat: the MEASURED
	// null-matched week-win rate, floored at 0.5. Stored on the row because a
	// verdict that does not name the null it was judged against is not
	// auditable — a week trial is won against that week's own folded majority,
	// so the no-skill rate is not 0.5 and differs per corpus. NullWeeks is how
	// many week-trials measured it. Both 0 on rows written before this wave,
	// which truthfully reads as "unrecorded" (those cleared the 0.5 literal).
	NullP0    float64 `json:"nullP0"`
	NullWeeks int     `json:"nullWeeks"`
	Survives  bool    `json:"survives"`
	FoundAt   int64   `json:"foundAt"`
	// Divisor is the Bonferroni divisor this rule actually cleared — the grid
	// size times every search the loop has ever run over this corpus. Stored
	// on the row because "survived Bonferroni correction" is unauditable after
	// the fact without the number that was corrected for, and because that
	// number grows every night: a rule found on night 200 cleared a far
	// harsher bar than the same rule found on night 1.
	Divisor int `json:"divisor"`
	// GridSize is how many rules the search judged, Weeks how many week-trials
	// judged THIS rule, and ObsWindow the research_weeks span searched. Divisor
	// alone does not say how wide the grid was versus how many nights it ran.
	GridSize  int    `json:"gridSize"`
	Weeks     int    `json:"weeks"`
	ObsWindow string `json:"obsWindow"`
	// RejectedBy names the gate that killed this rule, "" when it survived.
	// Rejections are ledgered as durably as survivors: a search that keeps
	// only its winners is unauditable, and the kills are what make a silent
	// re-test of an already-dead rule detectable.
	RejectedBy string `json:"rejectedBy"`
}

// ResearchObservations loads weekly observations in the shape the discovery
// grid consumes. limit<=0 loads everything.
func (s *Store) ResearchObservations(ctx context.Context, limit int) ([]researchx.Obs, error) {
	q := `SELECT symbol_id, week, ts, vec, fwd_return, up, era, high_vol
	      FROM research_weeks ORDER BY week, symbol_id`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []researchx.Obs{}
	for rows.Next() {
		var o researchx.Obs
		var vec string
		var up, highVol int
		if err := rows.Scan(&o.SymbolID, &o.Week, &o.Ts, &vec, &o.FwdRet,
			&up, &o.Era, &highVol); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(vec), &o.Vec) != nil {
			continue // a malformed vector is skipped, never guessed at
		}
		o.Up = up == 1
		o.HighVol = highVol == 1
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpsertLoopHypothesis records one discovered rule and its verdict. Idempotent
// on ID so a re-run updates rather than duplicating — the same rule rediscovered
// tomorrow is the same hypothesis, not a new one.
func (s *Store) UpsertLoopHypothesis(ctx context.Context, h LoopHypothesis) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO research_loop_hypotheses
		  (id, descr, status, wilson_lower, survives, found_at, last_seen,
		   divisor, grid_size, weeks, rejected_by, obs_window,
		   null_p0, null_weeks)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  status=excluded.status, wilson_lower=excluded.wilson_lower,
		  survives=excluded.survives, last_seen=excluded.last_seen,
		  divisor=excluded.divisor, grid_size=excluded.grid_size,
		  weeks=excluded.weeks, rejected_by=excluded.rejected_by,
		  obs_window=excluded.obs_window, null_p0=excluded.null_p0,
		  null_weeks=excluded.null_weeks`,
		h.ID, h.Desc, h.Status, h.WilsonLower, boolToInt(h.Survives),
		h.FoundAt, h.FoundAt, h.Divisor, h.GridSize, h.Weeks,
		h.RejectedBy, h.ObsWindow, h.NullP0, h.NullWeeks)
	return err
}

// LoopJudgment is one rule's verdict on one day — the append-only record.
//
// UpsertLoopHypothesis above is a CURRENT-STATE view: keyed on rule id, so the
// same rule judged on 200 nights collapses to one row carrying the latest
// verdict. That makes the stated purpose of the rejection ledger — detecting a
// dead rule being silently re-tested until a night's noise lets it through —
// unachievable, because the re-tests overwrite each other. This row is written
// once per judged rule per pass and never rewritten except by a same-day re-run
// (the same look), so "this rule has been judged and killed 47 times" is a
// query rather than an assertion.
type LoopJudgment struct {
	Day         string  `json:"day"`
	RuleID      string  `json:"ruleId"`
	Status      string  `json:"status"`
	WilsonLower float64 `json:"wilsonLower"`
	// P0 is the null the Wilson lower bound had to beat, stored per row: a
	// bound is meaningless without the bar it was compared against.
	P0         float64 `json:"p0"`
	Divisor    int     `json:"divisor"`
	GridSize   int     `json:"gridSize"`
	Weeks      int     `json:"weeks"`
	RejectedBy string  `json:"rejectedBy"`
	ObsWindow  string  `json:"obsWindow"`
}

// InsertLoopJudgment appends one (day, rule) judgment. Idempotent on the day so
// a re-run of the same day is the same look rather than a second one.
func (s *Store) InsertLoopJudgment(ctx context.Context, j LoopJudgment) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO research_loop_judgments
		  (day, rule_id, status, wilson_lower, p0, divisor, grid_size, weeks,
		   rejected_by, obs_window)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(day, rule_id) DO UPDATE SET
		  status=excluded.status, wilson_lower=excluded.wilson_lower,
		  p0=excluded.p0, divisor=excluded.divisor,
		  grid_size=excluded.grid_size, weeks=excluded.weeks,
		  rejected_by=excluded.rejected_by, obs_window=excluded.obs_window`,
		j.Day, j.RuleID, j.Status, j.WilsonLower, j.P0, j.Divisor,
		j.GridSize, j.Weeks, j.RejectedBy, j.ObsWindow)
	return err
}

// LoopJudgments returns the append-only judgment history, newest day first.
func (s *Store) LoopJudgments(ctx context.Context, limit int) ([]LoopJudgment, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT day, rule_id, status, wilson_lower, p0, divisor, grid_size,
		       weeks, rejected_by, obs_window
		FROM research_loop_judgments ORDER BY day DESC, rule_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []LoopJudgment{}
	for rows.Next() {
		var j LoopJudgment
		if err := rows.Scan(&j.Day, &j.RuleID, &j.Status, &j.WilsonLower, &j.P0,
			&j.Divisor, &j.GridSize, &j.Weeks, &j.RejectedBy, &j.ObsWindow); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// LoopRejectionsByGate counts every judgment ever recorded, grouped by the gate
// that killed it (survivors under ""). Counting JUDGMENTS rather than rules is
// the point: it says how many times a gate has fired, including the same rule
// dying the same way on many nights.
func (s *Store) LoopRejectionsByGate(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rejected_by, COUNT(*) FROM research_loop_judgments
		GROUP BY rejected_by ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]int{}
	for rows.Next() {
		var gate string
		var n int
		if err := rows.Scan(&gate, &n); err != nil {
			return nil, err
		}
		out[gate] = n
	}
	return out, rows.Err()
}

// LoopRun is one research-loop PASS — including a pass that refused to search.
type LoopRun struct {
	Day            string  `json:"day"`
	RanAt          int64   `json:"ranAt"`
	GridSize       int     `json:"gridSize"`
	Divisor        int     `json:"divisor"`
	CorrectedAlpha float64 `json:"correctedAlpha"`
	ObsCount       int     `json:"obsCount"`
	ObsTsFrom      int64   `json:"obsTsFrom"`
	ObsTsTo        int64   `json:"obsTsTo"`
	Survivors      int     `json:"survivors"`
	Judged         int     `json:"judged"`
	// RefusalReason is non-empty when the pass declined to search at all (too
	// few observations). A refusal is a result and is recorded as one.
	RefusalReason string `json:"refusalReason"`
	GitRev        string `json:"gitRev"`

	// CorpusCoverage is the WORST-week point-in-time coverage of the corpus
	// this pass searched (ResearchWeekStats.CoverageMin). A pass row already
	// records what was searched and under which correction; without this it
	// does not record how much of the market was actually in front of the
	// search. 0 on pre-existing rows reads as "unmeasured", not as "none".
	CorpusCoverage float64 `json:"corpusCoverage"`
}

// UpsertLoopRun records one loop pass. Keyed on the UTC day the loop gates on,
// so a re-run of the same day overwrites rather than double-counting a search.
func (s *Store) UpsertLoopRun(ctx context.Context, r LoopRun) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO research_loop_runs
		  (day, ran_at, grid_size, divisor, corrected_alpha, obs_count,
		   obs_ts_from, obs_ts_to, survivors, judged, refusal_reason, git_rev,
		   corpus_coverage)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(day) DO UPDATE SET
		  ran_at=excluded.ran_at, grid_size=excluded.grid_size,
		  divisor=excluded.divisor, corrected_alpha=excluded.corrected_alpha,
		  obs_count=excluded.obs_count, obs_ts_from=excluded.obs_ts_from,
		  obs_ts_to=excluded.obs_ts_to, survivors=excluded.survivors,
		  judged=excluded.judged, refusal_reason=excluded.refusal_reason,
		  git_rev=excluded.git_rev, corpus_coverage=excluded.corpus_coverage`,
		r.Day, r.RanAt, r.GridSize, r.Divisor, r.CorrectedAlpha, r.ObsCount,
		r.ObsTsFrom, r.ObsTsTo, r.Survivors, r.Judged, r.RefusalReason, r.GitRev,
		r.CorpusCoverage)
	return err
}

// LoopRunForDay re-reads the pass row for one UTC day, reporting whether it
// exists at all.
//
// It is the READ-BACK half of the write-then-describe discipline. A pass that
// commits its ledger and then reports "NOTHING survived Bonferroni correction"
// is making a claim about ~96 judgments that only a table can substantiate; if
// the row is absent or disagrees with what the pass believes it judged, the
// summary is a story rather than evidence, and the pass must fail instead of
// telling it. Writing and then trusting the write is exactly how this loop
// reported two nulls while all three ledger tables stayed empty.
func (s *Store) LoopRunForDay(ctx context.Context, day string) (LoopRun, bool, error) {
	var r LoopRun
	err := s.db.QueryRowContext(ctx, `
		SELECT day, ran_at, grid_size, divisor, corrected_alpha, obs_count,
		       obs_ts_from, obs_ts_to, survivors, judged, refusal_reason, git_rev,
		       corpus_coverage
		FROM research_loop_runs WHERE day=?`, day).Scan(&r.Day, &r.RanAt,
		&r.GridSize, &r.Divisor, &r.CorrectedAlpha, &r.ObsCount, &r.ObsTsFrom,
		&r.ObsTsTo, &r.Survivors, &r.Judged, &r.RefusalReason, &r.GitRev,
		&r.CorpusCoverage)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	return r, true, nil
}

// MarkLoopDayFromRun sets the loop's same-day gate key, but DERIVES it from the
// durable pass row instead of writing it alongside one.
//
// The gate and the ledger were two independent writes, and the live database is
// in the state the code comments called impossible:
// meta.research_loop_last_day='2026-07-27' beside zero research_loop_runs rows
// and zero research_loop_hypotheses rows. That combination makes the loop skip
// the rest of the day on a claim about a search no table can substantiate —
// keep-the-winners-forget-the-kills with both halves missing. A guard that
// merely NOTICES the disagreement is weaker than a write that cannot produce
// it: here the meta row is a projection of the run row, written by a single
// statement whose WHERE EXISTS is the assertion. No row, no gate, and the day
// stays retryable.
//
// It can only ever WITHHOLD a gate; it never writes a run row and never
// fabricates a judgment.
func (s *Store) MarkLoopDayFromRun(ctx context.Context, metaKey, day string) error {
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO meta (k, v)
		SELECT ?, ? WHERE EXISTS (SELECT 1 FROM research_loop_runs WHERE day=?)
		ON CONFLICT(k) DO UPDATE SET v=excluded.v`, metaKey, day, day)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("refusing to gate %s off %s: research_loop_runs holds no "+
			"row for that day, so the gate would assert a pass no table records", day, metaKey)
	}
	return nil
}

// Loop engine liveness states. A dead engine and an honest null are opposite
// findings and must never render as the same sentence: "searched and found
// nothing" is the search WORKING, while "no judgment recorded since <date>"
// means no search happened at all and nothing was priced.
const (
	LoopEngineWarmingUp = "corpus-below-search-floor"
	LoopEngineLive      = "judging"
	LoopEngineSilent    = "no-judgment-recorded"
)

// LoopEngineHealth is the liveness verdict on the research engine itself,
// separate from the verdict on any rule it judged.
type LoopEngineHealth struct {
	CorpusRows int    `json:"corpusRows"`
	MinObs     int    `json:"minObs"`
	LastRunDay string `json:"lastRunDay"`
	LastRunAge int    `json:"lastRunAgeDays"` // -1 when no pass was ever recorded
	State      string `json:"state"`
	Healthy    bool   `json:"healthy"`
	Detail     string `json:"detail"`
}

// loopSilentAfterDays is how many UTC days of silence, on a corpus fat enough
// to search, mean the engine is not running rather than finding nothing. The
// loop's own interval is 24h, so three days is two missed passes.
const loopSilentAfterDays = 3

// LoopEngineHealth reports whether the research engine has produced any judgment
// recently, given a corpus large enough that it should have.
//
// The failure it names: research_weeks held 166,285 observations against a 2,000
// floor while research_loop_runs held no row at all, and the only surface anyone
// could read said "NOTHING survived Bonferroni correction" — indistinguishable
// from a live engine returning an honest null. Corpus size is the precondition
// that makes silence diagnostic: below the floor, silence is the refusal working.
func (s *Store) LoopEngineHealth(ctx context.Context, minObs int, now time.Time) (LoopEngineHealth, error) {
	h := LoopEngineHealth{MinObs: minObs, LastRunAge: -1, State: LoopEngineLive, Healthy: true}
	stats, err := s.ResearchWeeksStats(ctx)
	if err != nil {
		return h, err
	}
	h.CorpusRows = stats.Rows

	var day sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(day) FROM research_loop_runs`).Scan(&day); err != nil {
		return h, err
	}
	if day.Valid {
		h.LastRunDay = day.String
		if t, perr := time.Parse("2006-01-02", day.String); perr == nil {
			h.LastRunAge = int(now.UTC().Truncate(24*time.Hour).Sub(t).Hours() / 24)
		}
	}

	switch {
	case h.CorpusRows < minObs:
		h.State = LoopEngineWarmingUp
		h.Detail = fmt.Sprintf("corpus holds %d observations, below the %d floor — "+
			"declining to search is the refusal working, not a dead engine",
			h.CorpusRows, minObs)
	case h.LastRunDay == "" || h.LastRunAge < 0 || h.LastRunAge >= loopSilentAfterDays:
		h.State = LoopEngineSilent
		h.Healthy = false
		since := h.LastRunDay
		if since == "" {
			since = "never"
		}
		h.Detail = fmt.Sprintf("research engine has produced no judgment since %s "+
			"while the corpus holds %d observations against a %d floor — this is NOT "+
			"'searched and found nothing', it is no search on record", since,
			h.CorpusRows, minObs)
	default:
		h.Detail = fmt.Sprintf("last pass recorded %s over a %d-observation corpus",
			h.LastRunDay, h.CorpusRows)
	}
	return h, nil
}

// LoopLedgerReady reports whether this database can accept everything a grid
// search produces, BEFORE the search is taken.
//
// The failure it exists to stop already happened: the loop ran nightly for
// weeks against a database whose ledger tables had never been migrated,
// charging itself a look each night and reporting "NOTHING survived" while
// research_loop_runs did not exist and research_loop_hypotheses stayed at zero
// rows. Failing AFTER the search is not enough — the multiplicity has been
// spent by then and the judgments are gone. A search whose verdicts cannot be
// written should cost nothing rather than cost a look, so the loop probes the
// ledger first and declines to look at all when this returns an error.
//
// The columns are checked, not just the tables: a hypotheses row that cannot
// carry null_p0/null_weeks records a verdict without the bar it was judged
// against, which is not an auditable record of anything.
func (s *Store) LoopLedgerReady(ctx context.Context) error {
	for _, t := range []string{
		"research_loop_runs", "research_loop_judgments", "research_loop_hypotheses",
	} {
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`,
			t).Scan(&n); err != nil {
			return fmt.Errorf("probe %s: %w", t, err)
		}
		if n == 0 {
			return fmt.Errorf("ledger table %s does not exist", t)
		}
	}
	for _, c := range []string{"null_p0", "null_weeks"} {
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('research_loop_hypotheses') WHERE name=?`,
			c).Scan(&n); err != nil {
			return fmt.Errorf("probe research_loop_hypotheses.%s: %w", c, err)
		}
		if n == 0 {
			return fmt.Errorf("research_loop_hypotheses lacks column %s, so a "+
				"ledgered verdict could not name the null it was judged against", c)
		}
	}
	return nil
}

// RecordLoopSearch writes ONE completed search atomically: every judgment, every
// current-state hypothesis row, and exactly one research_loop_runs row, in a
// single transaction.
//
// The atomicity is the point, not an optimisation. The run row is what
// DurableLoopSearches counts as a look, and the judgments are what that look
// produced; writing them separately admits both halves of the asymmetry this
// ledger exists to prevent — a run row charging multiplicity for judgments that
// were never stored, or stored judgments that no run row admits taking a look
// for. A search either ledgers completely or does not count as having happened.
func (s *Store) RecordLoopSearch(ctx context.Context, run LoopRun,
	hyps []LoopHypothesis, judgments []LoopJudgment) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	for _, h := range hyps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO research_loop_hypotheses
			  (id, descr, status, wilson_lower, survives, found_at, last_seen,
			   divisor, grid_size, weeks, rejected_by, obs_window,
			   null_p0, null_weeks)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			  status=excluded.status, wilson_lower=excluded.wilson_lower,
			  survives=excluded.survives, last_seen=excluded.last_seen,
			  divisor=excluded.divisor, grid_size=excluded.grid_size,
			  weeks=excluded.weeks, rejected_by=excluded.rejected_by,
			  obs_window=excluded.obs_window, null_p0=excluded.null_p0,
			  null_weeks=excluded.null_weeks`,
			h.ID, h.Desc, h.Status, h.WilsonLower, boolToInt(h.Survives),
			h.FoundAt, h.FoundAt, h.Divisor, h.GridSize, h.Weeks,
			h.RejectedBy, h.ObsWindow, h.NullP0, h.NullWeeks); err != nil {
			return fmt.Errorf("ledger hypothesis %s: %w", h.ID, err)
		}
	}
	for _, j := range judgments {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO research_loop_judgments
			  (day, rule_id, status, wilson_lower, p0, divisor, grid_size, weeks,
			   rejected_by, obs_window)
			VALUES (?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(day, rule_id) DO UPDATE SET
			  status=excluded.status, wilson_lower=excluded.wilson_lower,
			  p0=excluded.p0, divisor=excluded.divisor,
			  grid_size=excluded.grid_size, weeks=excluded.weeks,
			  rejected_by=excluded.rejected_by, obs_window=excluded.obs_window`,
			j.Day, j.RuleID, j.Status, j.WilsonLower, j.P0, j.Divisor,
			j.GridSize, j.Weeks, j.RejectedBy, j.ObsWindow); err != nil {
			return fmt.Errorf("ledger judgment %s: %w", j.RuleID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO research_loop_runs
		  (day, ran_at, grid_size, divisor, corrected_alpha, obs_count,
		   obs_ts_from, obs_ts_to, survivors, judged, refusal_reason, git_rev,
		   corpus_coverage)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(day) DO UPDATE SET
		  ran_at=excluded.ran_at, grid_size=excluded.grid_size,
		  divisor=excluded.divisor, corrected_alpha=excluded.corrected_alpha,
		  obs_count=excluded.obs_count, obs_ts_from=excluded.obs_ts_from,
		  obs_ts_to=excluded.obs_ts_to, survivors=excluded.survivors,
		  judged=excluded.judged, refusal_reason=excluded.refusal_reason,
		  git_rev=excluded.git_rev, corpus_coverage=excluded.corpus_coverage`,
		run.Day, run.RanAt, run.GridSize, run.Divisor, run.CorrectedAlpha,
		run.ObsCount, run.ObsTsFrom, run.ObsTsTo, run.Survivors, run.Judged,
		run.RefusalReason, run.GitRev, run.CorpusCoverage); err != nil {
		return fmt.Errorf("record loop run: %w", err)
	}
	return tx.Commit()
}

// BackfillLoopRuns reconstructs a research_loop_runs row for every search still
// recoverable from worker_runs, so DurableLoopSearches starts from the true
// count of looks rather than from zero.
//
// The loop searched nightly long before it had a durable place to say so. Those
// looks were taken; the multiplicity was spent. Starting the durable counter at
// zero because the only surviving evidence is a log line would refund every one
// of them, which is precisely the free look the Bonferroni divisor exists to
// deny. Reconstructed rows carry git_rev='backfill:worker_runs' and judged=0:
// the look is recoverable, the judgments are not, and the row says so rather
// than inventing verdicts. Idempotent — a day that already has a real row is
// left alone, because a real record always beats a reconstructed one.
func (s *Store) BackfillLoopRuns(ctx context.Context) (int, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO research_loop_runs
		  (day, ran_at, grid_size, divisor, corrected_alpha, obs_count,
		   obs_ts_from, obs_ts_to, survivors, judged, refusal_reason, git_rev)
		SELECT date(started_at,'unixepoch'), MAX(started_at),
		       MAX(CAST(substr(detail, 12, instr(detail,'-rule')-12) AS INTEGER), 1),
		       0, 0, 0, 0, 0, 0, 0, '', 'backfill:worker_runs'
		FROM worker_runs
		WHERE worker='research-loop' AND detail LIKE 'searched a%'
		  AND instr(detail,'-rule') > 12
		GROUP BY date(started_at,'unixepoch')
		ON CONFLICT(day) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// HistoricalLoopSearches counts grid searches the loop is KNOWN to have already
// conducted, read off the free-text worker_runs record it left behind on each
// pass ("searched a N-rule grid ..."; refusals and same-day skips start with
// "skip"). It exists because the multiplicity counter was introduced long after
// the loop started running nightly: starting it at 0 would retroactively grant
// every look already taken for free, which is precisely the p-hacking the
// Bonferroni divisor is there to price.
//
// worker_runs is pruned, so this is a LOWER BOUND on the looks actually taken.
// A lower bound only ever makes the divisor smaller than the truth, never
// larger, so it cannot manufacture a survivor — it under-charges rather than
// inflating the bar with searches that never happened.
//
// It is also the WRONG primary source, which is why DurableLoopSearches exists
// below: sourcing the multiplicity term from a table the system deliberately
// truncates means log retention can quietly REFUND looks already taken, so the
// divisor could fall over time. Callers take the max of both.
func (s *Store) HistoricalLoopSearches(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM worker_runs
		WHERE worker='research-loop' AND detail LIKE 'searched a%'`).Scan(&n)
	return n, err
}

// DurableLoopSearches counts looks from the two APPEND-ONLY tables: pass rows
// that reached the grid (grid_size>0 — a refusal took no look and must not cost
// one) and distinct days on which rules were actually judged. Neither table is
// pruned by any retention tier, so this count can only ever rise, which makes
// the Bonferroni divisor monotone by construction rather than by policy.
//
// The larger of the two is returned: they measure the same event from opposite
// ends, and a source that came back short must never be able to lower a bar
// already paid for.
func (s *Store) DurableLoopSearches(ctx context.Context) (int, error) {
	var runs, days int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM research_loop_runs WHERE grid_size > 0`).Scan(&runs); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT day) FROM research_loop_judgments`).Scan(&days); err != nil {
		return runs, err
	}
	if days > runs {
		return days, nil
	}
	return runs, nil
}

// LoopRuns returns the loop's search history, newest day first.
func (s *Store) LoopRuns(ctx context.Context, limit int) ([]LoopRun, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT day, ran_at, grid_size, divisor, corrected_alpha, obs_count,
		       obs_ts_from, obs_ts_to, survivors, judged, refusal_reason, git_rev,
		       corpus_coverage
		FROM research_loop_runs ORDER BY day DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []LoopRun{}
	for rows.Next() {
		var r LoopRun
		if err := rows.Scan(&r.Day, &r.RanAt, &r.GridSize, &r.Divisor,
			&r.CorrectedAlpha, &r.ObsCount, &r.ObsTsFrom, &r.ObsTsTo,
			&r.Survivors, &r.Judged, &r.RefusalReason, &r.GitRev,
			&r.CorpusCoverage); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoopHypotheses returns what the loop has found, newest first.
func (s *Store) LoopHypotheses(ctx context.Context, limit int) ([]LoopHypothesis, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, descr, status, wilson_lower, survives, found_at,
		       COALESCE(divisor, 0), COALESCE(grid_size, 0), COALESCE(weeks, 0),
		       COALESCE(rejected_by, ''), COALESCE(obs_window, ''),
		       COALESCE(null_p0, 0), COALESCE(null_weeks, 0)
		FROM research_loop_hypotheses ORDER BY last_seen DESC, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []LoopHypothesis{}
	for rows.Next() {
		var h LoopHypothesis
		var sv int
		if err := rows.Scan(&h.ID, &h.Desc, &h.Status, &h.WilsonLower, &sv,
			&h.FoundAt, &h.Divisor, &h.GridSize, &h.Weeks, &h.RejectedBy,
			&h.ObsWindow, &h.NullP0, &h.NullWeeks); err != nil {
			return nil, err
		}
		h.Survives = sv == 1
		out = append(out, h)
	}
	return out, rows.Err()
}
