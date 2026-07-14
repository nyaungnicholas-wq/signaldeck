// NEWS-TRENDS wave — store layer.
//
// Turns the news headline archive into two DESCRIPTIVE trend reads:
//   - per-symbol daily headline VOLUME and its z-score vs the symbol's OWN
//     trailing-30-day baseline (honesty gate: >=10 prior days with any news
//     and a non-zero baseline spread — otherwise ok=false, never a fake 0);
//   - fleet-wide trending TOKENS extracted deterministically from recent
//     headline titles (lowercase, non-alpha split, stopwords + short tokens +
//     tracked tickers dropped — no LLM anywhere).
//
// HONESTY: a headline-frequency trend — descriptive attention, not a
// forecast. Every consumer carries that caveat verbatim.
package store

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strings"
	"time"
)

// NewsVolumeDay is one day's headline count for a symbol.
type NewsVolumeDay struct {
	Day string `json:"day"` // YYYY-MM-DD (UTC)
	N   int    `json:"n"`
}

// NewsVolumeByDay returns a symbol's daily headline counts over the last
// `days` UTC days (today inclusive), oldest first. Days with zero headlines
// are simply absent from the result (the news table has no row to count).
func (s *Store) NewsVolumeByDay(ctx context.Context, symbolID int64, days int) ([]NewsVolumeDay, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
		SELECT date(ts,'unixepoch') AS day, COUNT(*)
		FROM news WHERE symbol_id=? AND date(ts,'unixepoch')>=?
		GROUP BY day ORDER BY day ASC`, symbolID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []NewsVolumeDay
	for rows.Next() {
		var d NewsVolumeDay
		if err := rows.Scan(&d.Day, &d.N); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// newsVolMinBaselineDays is the honesty gate for the volume z-score: the
// trailing baseline must contain at least this many PRIOR days that had any
// news at all, else the z is withheld (ok=false, reason says why).
const newsVolMinBaselineDays = 10

// NewsVolumeZ standardizes TODAY's (UTC) headline count for a symbol against
// the symbol's own trailing-30-day baseline (the 30 days before today,
// zero-news days counted as 0). ok=false — with a stated reason — when fewer
// than newsVolMinBaselineDays prior days had any news, or when the baseline
// has zero variance (a z over a flat baseline is not a statistic).
func (s *Store) NewsVolumeZ(ctx context.Context, symbolID int64) (day string, n int, z float64, ok bool, reason string, err error) {
	today := time.Now().UTC().Format("2006-01-02")
	series, err := s.NewsVolumeByDay(ctx, symbolID, 31)
	if err != nil {
		return "", 0, 0, false, "", err
	}
	counts := make(map[string]int, len(series))
	activePrior := 0
	for _, d := range series {
		counts[d.Day] = d.N
		if d.Day != today && d.N > 0 {
			activePrior++
		}
	}
	n = counts[today]
	if activePrior < newsVolMinBaselineDays {
		return today, n, 0, false, "insufficient baseline: fewer than 10 prior days with any news in the trailing 30d", nil
	}
	// Baseline over the 30 prior calendar days, zero-news days included as 0.
	var sum, sumSq float64
	for i := 1; i <= 30; i++ {
		c := float64(counts[time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")])
		sum += c
		sumSq += c * c
	}
	mean := sum / 30
	variance := sumSq/30 - mean*mean
	if variance <= 0 {
		return today, n, 0, false, "zero-variance baseline: trailing 30d counts are flat", nil
	}
	z = (float64(n) - mean) / math.Sqrt(variance)
	return today, n, z, true, "", nil
}

// UpsertNewsTrend writes one (symbol, day) news-volume trend row. z==nil
// records an honest NULL (baseline gate not met), never a fabricated 0.
func (s *Store) UpsertNewsTrend(ctx context.Context, symbolID int64, day string, n int, z *float64) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO news_trends (symbol_id, day, n, z) VALUES (?,?,?,?)
		ON CONFLICT(symbol_id, day) DO UPDATE SET n=excluded.n, z=excluded.z`,
		symbolID, day, n, z)
	return err
}

// LatestNewsTrendZ returns the newest stored news-volume z for a symbol that
// is at most maxAgeDays old (UTC days). ok=false when there is no fresh row
// or the fresh row's z is NULL (gate not met) — an absent z stays absent.
func (s *Store) LatestNewsTrendZ(ctx context.Context, symbolID int64, maxAgeDays int) (z float64, ok bool, err error) {
	if maxAgeDays < 0 {
		maxAgeDays = 0
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -maxAgeDays).Format("2006-01-02")
	var nz sql.NullFloat64
	row := s.db.QueryRowContext(ctx, `
		SELECT z FROM news_trends WHERE symbol_id=? AND day>=?
		ORDER BY day DESC LIMIT 1`, symbolID, cutoff)
	if err := row.Scan(&nz); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !nz.Valid {
		return 0, false, nil
	}
	return nz.Float64, true, nil
}

// SymbolsWithNewsSince returns the distinct symbol ids that have at least one
// headline with ts >= sinceTs (the news-trends worker's per-pass scope —
// symbols with no fresh news get no new row).
func (s *Store) SymbolsWithNewsSince(ctx context.Context, sinceTs int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT symbol_id FROM news WHERE ts>=? ORDER BY symbol_id`, sinceTs)
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

// TrendingToken is one fleet-wide headline token with its frequency.
type TrendingToken struct {
	Token   string `json:"token"`
	Count   int    `json:"count"`   // headline occurrences (once per headline)
	Symbols int    `json:"symbols"` // distinct symbols whose headlines used it
}

// newsStopwords is the embedded english + generic-news stopword list for the
// deterministic token extractor. Generic glue words only — finance-signal
// words (earnings, guidance, downgrade, tariffs, ...) are deliberately KEPT:
// they are exactly the attention signal this feature reports.
var newsStopwords = map[string]struct{}{}

func init() {
	for _, w := range strings.Fields(`
		the a an and or but if then else when while for nor so yet as at by
		into onto from of off on in out over under above below to up down
		with within without about against between through during before
		after again further once here there all any both each few more most
		other some such only own same than too very can will just should
		could would may might must shall not no dont doesnt didnt isnt arent
		wasnt werent hasnt havent hadnt wont cant its his her their our your
		my this that these those what which who whom whose why how where is
		are was were be been being have has had do does did says said say
		new now amid among per via still also even ever never gets get got
		make makes made take takes took see sees seen goes going gone come
		comes came its lets heres theres whats you they them him she he it
		we us i one two three first second next last year years week weeks
		day days month months today yesterday tomorrow ago near could us
		more less many much lot big small top best worst latest breaking
		update updated news report reports reported reportedly according
		announce announces announced statement inc corp corporation company
		companies co ltd plc group holdings shares stock stocks percent`) {
		newsStopwords[w] = struct{}{}
	}
}

// FleetTrendingTokens extracts the top-N trending tokens from every headline
// with ts >= sinceTs, across all symbols. Deterministic keyword extraction —
// lowercase, split on non-alpha runs, drop stopwords, drop tokens shorter
// than 3 chars, drop tickers we track (they would dominate their own
// headlines) — counted once per headline, ranked by count then token
// (stable). No LLM anywhere.
func (s *Store) FleetTrendingTokens(ctx context.Context, sinceTs int64, topN int) ([]TrendingToken, error) {
	if topN <= 0 {
		topN = 10
	}
	// Tracked tickers (both halves of pairs like BTC/USD) are excluded.
	tickers := map[string]struct{}{}
	trows, err := s.db.QueryContext(ctx, `SELECT symbol FROM symbols`)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var sym string
		if err := trows.Scan(&sym); err != nil {
			trows.Close() //nolint:errcheck
			return nil, err
		}
		for _, part := range splitAlpha(sym) {
			tickers[part] = struct{}{}
		}
	}
	trows.Close() //nolint:errcheck
	if err := trows.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, headline FROM news WHERE ts>=?`, sinceTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	counts := map[string]int{}
	syms := map[string]map[int64]struct{}{}
	for rows.Next() {
		var symbolID int64
		var headline string
		if err := rows.Scan(&symbolID, &headline); err != nil {
			return nil, err
		}
		seen := map[string]struct{}{} // count each token once per headline
		for _, tok := range splitAlpha(headline) {
			if len(tok) < 3 {
				continue
			}
			if _, stop := newsStopwords[tok]; stop {
				continue
			}
			if _, tick := tickers[tok]; tick {
				continue
			}
			if _, dup := seen[tok]; dup {
				continue
			}
			seen[tok] = struct{}{}
			counts[tok]++
			if syms[tok] == nil {
				syms[tok] = map[int64]struct{}{}
			}
			syms[tok][symbolID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TrendingToken, 0, len(counts))
	for tok, c := range counts {
		out = append(out, TrendingToken{Token: tok, Count: c, Symbols: len(syms[tok])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Token < out[j].Token
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out, nil
}

// splitAlpha lowercases s and splits it into maximal ascii-letter runs.
func splitAlpha(s string) []string {
	s = strings.ToLower(s)
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		alpha := c >= 'a' && c <= 'z'
		if alpha && start < 0 {
			start = i
		}
		if !alpha && start >= 0 {
			out = append(out, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
