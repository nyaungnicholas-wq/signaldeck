// Sentiment-correlation wave — persistence for lexicon-scored headlines, the
// as-of-aligned daily sentiment features built from them, and the study results.
//
// The one subtle thing in this file is TIMING, and it is the difference between
// a real measurement and a fake one. Sentiment features are keyed to the first
// trading session on which the headline was already public — never to the
// headline's own calendar day. Most financial headlines are published outside
// the 09:30-16:00 ET window; keying those to their own day lets a study "predict"
// a move that had already happened by the time the article existed.
//
// Reads use the pooled s.db handle; writes go through s.w, matching the store's
// single-writer discipline.
package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
)

// PendingLexNews is one headline awaiting a lexicon score.
type PendingLexNews struct {
	ID       string
	SymbolID int64
	Ts       int64
	Headline string
}

// UnscoredNews returns headlines the current lexicon version has not scored.
// Ordered oldest-first so a long backfill makes monotonic progress and a
// crash resumes where it stopped.
func (s *Store) UnscoredNews(ctx context.Context, version, limit int) ([]PendingLexNews, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol_id, ts, headline FROM news
		WHERE lex_ver <> ? ORDER BY ts LIMIT ?`, version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PendingLexNews
	for rows.Next() {
		var p PendingLexNews
		if err := rows.Scan(&p.ID, &p.SymbolID, &p.Ts, &p.Headline); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LexScore is one headline's lexicon verdict, ready to persist.
type LexScore struct {
	ID     string
	Score  float64
	Polar  bool
	Hedged bool
}

// SetLexScores writes a batch of lexicon scores in ONE transaction. Batching
// matters: the archive is tens of thousands of rows and one statement per row
// through the single write connection would hold it for minutes.
func (s *Store) SetLexScores(ctx context.Context, version int, scores []LexScore) error {
	if len(scores) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	st, err := tx.PrepareContext(ctx, `
		UPDATE news SET lex_score=?, lex_polar=?, lex_hedged=?, lex_ver=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer st.Close() //nolint:errcheck
	for _, sc := range scores {
		if _, err := st.ExecContext(ctx, sc.Score, boolInt(sc.Polar), boolInt(sc.Hedged), version, sc.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// LexNewsRow is one scored headline, used by the feature builder.
type LexNewsRow struct {
	SymbolID int64
	Ts       int64
	Score    float64
	Polar    bool
	Hedged   bool
}

// ReconcileNewsSymbols copies any article->symbol pair implied by news.symbol_id
// into the normalised mapping table. Idempotent, and cheap because the insert
// collides on the primary key for everything already there.
//
// Needed because the LIVE news fetcher writes only news.symbol_id (it fetches
// per symbol, so the mapping is implicit), while the historical backfill writes
// the full multi-ticker mapping. Running this each pass means the feature builder
// can read one authoritative source instead of unioning two.
func (s *Store) ReconcileNewsSymbols(ctx context.Context) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO news_symbols (news_id, symbol_id)
		SELECT id, symbol_id FROM news WHERE symbol_id > 0`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// InsertNewsSymbols records the tracked symbols an article mentions.
func (s *Store) InsertNewsSymbols(ctx context.Context, newsID string, symbolIDs []int64) error {
	if len(symbolIDs) == 0 {
		return nil
	}
	for _, id := range symbolIDs {
		if _, err := s.w.ExecContext(ctx,
			`INSERT OR IGNORE INTO news_symbols (news_id, symbol_id) VALUES (?,?)`, newsID, id); err != nil {
			return err
		}
	}
	return nil
}

// ScoredNewsSince returns every lexicon-scored headline at or after sinceTs,
// ordered by symbol then time — joined through news_symbols, so an article that
// tags five companies contributes to all five rather than only to the one it
// happened to be filed under.
//
// Includes non-polar rows: n_all (how much coverage a symbol had) is a feature in
// its own right, separate from the mean of the opinions.
func (s *Store) ScoredNewsSince(ctx context.Context, version int, sinceTs int64) ([]LexNewsRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ns.symbol_id, n.ts, n.lex_score, n.lex_polar, n.lex_hedged
		FROM news n JOIN news_symbols ns ON ns.news_id = n.id
		WHERE n.lex_ver = ? AND n.ts >= ? ORDER BY ns.symbol_id, n.ts`, version, sinceTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LexNewsRow
	for rows.Next() {
		var r LexNewsRow
		var polar, hedged int
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.Score, &polar, &hedged); err != nil {
			return nil, err
		}
		r.Polar, r.Hedged = polar == 1, hedged == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ScoredNewsBetween returns scored headlines whose timestamp falls in
// [fromTs, toTs). The half-open interval is what lets the feature backfill walk
// backwards in windows without double-counting a boundary headline.
func (s *Store) ScoredNewsBetween(ctx context.Context, version int, fromTs, toTs int64) ([]LexNewsRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ns.symbol_id, n.ts, n.lex_score, n.lex_polar, n.lex_hedged
		FROM news n JOIN news_symbols ns ON ns.news_id = n.id
		WHERE n.lex_ver = ? AND n.ts >= ? AND n.ts < ? ORDER BY ns.symbol_id, n.ts`,
		version, fromTs, toTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LexNewsRow
	for rows.Next() {
		var r LexNewsRow
		var polar, hedged int
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.Score, &polar, &hedged); err != nil {
			return nil, err
		}
		r.Polar, r.Hedged = polar == 1, hedged == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// OldestScoredNewsTs is the timestamp of the oldest headline the current
// lexicon has scored, or 0 when none is scored yet. The feature backfill stops
// there: walking past it would grind over empty windows forever.
func (s *Store) OldestScoredNewsTs(ctx context.Context, version int) (int64, error) {
	var ts sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MIN(ts) FROM news WHERE lex_ver = ?`, version).Scan(&ts); err != nil {
		return 0, err
	}
	return ts.Int64, nil
}

// SentimentFeature is one as-of-aligned daily sentiment row.
type SentimentFeature struct {
	SymbolID  int64   `json:"-"`
	Day       string  `json:"day"`
	NPolar    int     `json:"nPolar"`
	NAll      int     `json:"nAll"`
	MeanScore float64 `json:"meanScore"`
	Pos       int     `json:"pos"`
	Neg       int     `json:"neg"`
	Hedged    int     `json:"hedged"`
	Ver       int     `json:"ver"`
}

// UpsertSentimentFeatures writes a batch of daily features idempotently.
func (s *Store) UpsertSentimentFeatures(ctx context.Context, fs []SentimentFeature) error {
	if len(fs) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	st, err := tx.PrepareContext(ctx, `
		INSERT INTO sentiment_features
		  (symbol_id, day, n_polar, n_all, mean_score, pos, neg, hedged, ver)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id, day) DO UPDATE SET
		  n_polar=excluded.n_polar, n_all=excluded.n_all, mean_score=excluded.mean_score,
		  pos=excluded.pos, neg=excluded.neg, hedged=excluded.hedged, ver=excluded.ver`)
	if err != nil {
		return err
	}
	defer st.Close() //nolint:errcheck
	for _, f := range fs {
		if _, err := st.ExecContext(ctx, f.SymbolID, f.Day, f.NPolar, f.NAll,
			f.MeanScore, f.Pos, f.Neg, f.Hedged, f.Ver); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SentimentFeatureStats describes the feature table's coverage — the honest
// answer to "can this study say anything yet".
type SentimentFeatureStats struct {
	Rows       int    `json:"rows"`
	Symbols    int    `json:"symbols"`
	Days       int    `json:"days"`
	FirstDay   string `json:"firstDay"`
	LastDay    string `json:"lastDay"`
	PolarRows  int    `json:"polarRows"`
	Headlines  int    `json:"headlinesScored"`
	NewsRows   int    `json:"newsRowsTotal"`
	NewsPolar  int     `json:"newsRowsPolar"`
	NewsFirst  string `json:"newsFirstDay"`
	NewsLast   string `json:"newsLastDay"`
	LexVersion int    `json:"lexiconVersion"`
}

// SentimentFeatureStats reports coverage for both the raw archive and the
// derived features.
func (s *Store) SentimentFeatureStats(ctx context.Context, version int) (SentimentFeatureStats, error) {
	var st SentimentFeatureStats
	st.LexVersion = version
	var first, last sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT symbol_id), COUNT(DISTINCT day),
		       MIN(day), MAX(day), COALESCE(SUM(n_polar),0), COALESCE(SUM(n_all),0)
		FROM sentiment_features`).
		Scan(&st.Rows, &st.Symbols, &st.Days, &first, &last, &st.PolarRows, &st.Headlines)
	if err != nil {
		return st, err
	}
	st.FirstDay, st.LastDay = first.String, last.String

	var nf, nl sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(lex_polar),0),
		       MIN(date(ts,'unixepoch')), MAX(date(ts,'unixepoch'))
		FROM news`).Scan(&st.NewsRows, &st.NewsPolar, &nf, &nl); err != nil {
		return st, err
	}
	st.NewsFirst, st.NewsLast = nf.String, nl.String
	return st, nil
}

// SentCorrObsRow is one joined observation: an as-of-aligned sentiment feature
// beside the price context needed to control for momentum and the forward
// return it is being tested against.
type SentCorrObsRow struct {
	SymbolID   int64
	Day        string
	SessionIdx int
	MeanScore  float64
	NPolar     int
	Ret0       float64
	RetTrail   float64
	Fwd        float64
}

// trailSessions is the trailing-return control window, in sessions.
const trailSessions = 5

// SentCorrObservations assembles the study sample for one forward horizon.
//
// It walks each symbol's DAILY BARS (the only series long enough to measure
// anything — the resolved-prediction tables cover weeks, the bars cover years),
// indexes the sessions, and joins the sentiment features onto them. For a
// feature dated session i it emits:
//
//	Ret0     = close[i]/close[i-1]-1        (contemporaneous — a control)
//	RetTrail = close[i]/close[i-1-trail]-1  (momentum — a control)
//	Fwd      = close[i+h]/close[i]-1        (the thing being predicted)
//
// Fwd starts at session i's close, which is strictly after the sentiment became
// public. An observation is emitted only when the full forward window exists, so
// the most recent h sessions are correctly absent rather than truncated.
//
// The wild-move guard mirrors the structural predictors: a >65% one-session move
// in this data is a split-adjustment artifact, not a return, and the symbol's
// affected observations are skipped rather than fed to the study as real moves.
func (s *Store) SentCorrObservations(ctx context.Context, horizon int, minPolar int) ([]SentCorrObsRow, error) {
	if horizon <= 0 {
		horizon = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.symbol_id, date(b.ts,'unixepoch'), b.close
		FROM bars b
		JOIN symbols sy ON sy.id = b.symbol_id
		WHERE b.tf='1d' AND sy.market='stocks' AND b.close > 0
		ORDER BY b.symbol_id, b.ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	type session struct {
		day   string
		close float64
	}
	bySym := map[int64][]session{}
	order := []int64{}
	for rows.Next() {
		var id int64
		var day string
		var c float64
		if err := rows.Scan(&id, &day, &c); err != nil {
			return nil, err
		}
		if _, seen := bySym[id]; !seen {
			order = append(order, id)
		}
		bySym[id] = append(bySym[id], session{day, c})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	feat, err := s.sentimentFeatureMap(ctx, minPolar)
	if err != nil {
		return nil, err
	}

	const maxSaneReturn = 0.65
	var out []SentCorrObsRow
	for _, id := range order {
		ss := bySym[id]
		perSym, ok := feat[id]
		if !ok {
			continue // no sentiment for this symbol: nothing to observe
		}
		idx := make(map[string]int, len(ss))
		for i, sn := range ss {
			idx[sn.day] = i
		}
		for day, f := range perSym {
			i, ok := idx[day]
			if !ok {
				// The aligned session is not a session for THIS symbol (listed
				// later, halted, or a gap). Dropping it is correct: there is no
				// forward return to measure.
				continue
			}
			if i < trailSessions+1 || i+horizon >= len(ss) {
				continue
			}
			ret0 := ss[i].close/ss[i-1].close - 1
			trail := ss[i].close/ss[i-1-trailSessions].close - 1
			fwd := ss[i+horizon].close/ss[i].close - 1
			if absf(ret0) > maxSaneReturn || absf(fwd) > maxSaneReturn || absf(trail) > maxSaneReturn {
				continue // split-adjustment artifact, not a return
			}
			out = append(out, SentCorrObsRow{
				SymbolID: id, Day: day, SessionIdx: i,
				MeanScore: f.MeanScore, NPolar: f.NPolar,
				Ret0: ret0, RetTrail: trail, Fwd: fwd,
			})
		}
	}
	return out, nil
}

// sentimentFeatureMap loads features with at least minPolar polar headlines,
// grouped symbol -> day.
func (s *Store) sentimentFeatureMap(ctx context.Context, minPolar int) (map[int64]map[string]SentimentFeature, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, day, n_polar, n_all, mean_score FROM sentiment_features
		WHERE n_polar >= ?`, minPolar)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]map[string]SentimentFeature{}
	for rows.Next() {
		var f SentimentFeature
		if err := rows.Scan(&f.SymbolID, &f.Day, &f.NPolar, &f.NAll, &f.MeanScore); err != nil {
			return nil, err
		}
		if out[f.SymbolID] == nil {
			out[f.SymbolID] = map[string]SentimentFeature{}
		}
		out[f.SymbolID][f.Day] = f
	}
	return out, rows.Err()
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// UpsertSentCorrResult stores one study result in place.
func (s *Store) UpsertSentCorrResult(ctx context.Context, feature string, horizon int, ts int64, obs int, gated bool, payload string) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO sentiment_corr (feature, horizon, ts, obs, gated, payload)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(feature, horizon) DO UPDATE SET
		  ts=excluded.ts, obs=excluded.obs, gated=excluded.gated, payload=excluded.payload`,
		feature, horizon, ts, obs, boolInt(gated), payload)
	return err
}

// SentCorrRow is one stored study result.
type SentCorrRow struct {
	Feature string `json:"feature"`
	Horizon int    `json:"horizon"`
	Ts      int64  `json:"ts"`
	Obs     int    `json:"obs"`
	Gated   bool   `json:"gated"`
	Payload string `json:"-"`
}

// SentCorrResults returns every stored study result, newest study first.
func (s *Store) SentCorrResults(ctx context.Context) ([]SentCorrRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT feature, horizon, ts, obs, gated, payload FROM sentiment_corr
		ORDER BY feature, horizon`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []SentCorrRow
	for rows.Next() {
		var r SentCorrRow
		var gated int
		if err := rows.Scan(&r.Feature, &r.Horizon, &r.Ts, &r.Obs, &gated, &r.Payload); err != nil {
			return nil, err
		}
		r.Gated = gated == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// SymbolSentimentFeatures returns one symbol's recent aligned sentiment rows,
// newest first — the per-symbol view behind the UI panel.
func (s *Store) SymbolSentimentFeatures(ctx context.Context, symbolID int64, limit int) ([]SentimentFeature, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT day, n_polar, n_all, mean_score, pos, neg, hedged, ver
		FROM sentiment_features WHERE symbol_id=? ORDER BY day DESC LIMIT ?`, symbolID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []SentimentFeature
	for rows.Next() {
		var f SentimentFeature
		if err := rows.Scan(&f.Day, &f.NPolar, &f.NAll, &f.MeanScore, &f.Pos, &f.Neg, &f.Hedged, &f.Ver); err != nil {
			return nil, err
		}
		f.SymbolID = symbolID
		out = append(out, f)
	}
	return out, rows.Err()
}

// ActionableSession returns the calendar day (YYYY-MM-DD, US market timezone) of
// the first trading session on which a headline published at ts was already
// public and therefore tradeable.
//
// THIS IS THE NO-LOOKAHEAD RULE, and it is the reason this helper exists instead
// of a date() call in SQL. A headline is actionable on its own session only if it
// landed before that session's close; anything after the close — which is where
// most financial news is published — belongs to the NEXT session. Weekend and
// holiday articles roll forward to the next open session.
func ActionableSession(ts int64) string {
	t := time.Unix(ts, 0).In(marketcal.Loc())

	// A pre-market headline IS actionable that same session, so the test here is
	// "is this a session DAY and are we before its close" — deliberately not
	// marketcal.OpenForBars, which is an INTRADAY check (09:45 to the close) and
	// would push every 07:00 ET headline to the following day.
	if isSessionDay(t) && t.Before(sessionClose(t)) {
		return t.Format("2006-01-02")
	}
	// After the close, or on a weekend/holiday: the first session that can act on
	// it is the next open day. The bound is generous enough for the longest
	// stretch of consecutive market closures and prevents an unbounded loop on a
	// pathological timestamp.
	for i := 0; i < 10; i++ {
		t = t.AddDate(0, 0, 1)
		if isSessionDay(t) {
			break
		}
	}
	return t.Format("2006-01-02")
}

// isSessionDay reports whether t's ET calendar day is a trading day, ignoring
// the time of day.
func isSessionDay(t time.Time) bool {
	et := t.In(marketcal.Loc())
	if wd := et.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	return !marketcal.IsFullHoliday(et)
}

// sessionClose is t's ET calendar day's closing instant (13:00 on the half-days
// marketcal knows about, 16:00 otherwise).
func sessionClose(t time.Time) time.Time {
	et := t.In(marketcal.Loc())
	h := 16
	if marketcal.IsHalfDay(et) {
		h = 13
	}
	return time.Date(et.Year(), et.Month(), et.Day(), h, 0, 0, 0, marketcal.Loc())
}
