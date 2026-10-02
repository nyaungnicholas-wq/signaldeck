// Package copilot is the "ask the data" layer (plan step 10): a question in
// plain English, answered ONLY from SignalDeck's own tables, with every claim
// citing the row it came from.
//
// The model never writes SQL. It picks up to MaxQueries entries from a fixed
// catalog of named, parameterised, read-only queries; the server validates the
// names and every parameter, runs them on a query-only connection with a
// timeout, hands the rows back, and then checks that every citation in the
// answer names a row it actually returned. Anything else is refused.
//
// Two tiers. The operator may use every entry. A member may use only entries
// marked TierMember, and those serve derived or public-domain data only
// (datalicense.go D1): no vendor price, OHLC, volume, quote, headline,
// StockTwits text or per-row realised return, and no crypto row.
package copilot

import (
	"fmt"
	"regexp"
	"strings"
)

// Tier says who may run a catalog entry.
type Tier string

const (
	TierOperator Tier = "operator" // the operator only
	TierMember   Tier = "member"   // the operator and members
)

// ParamKind is how a parameter is validated.
type ParamKind string

const (
	KindSymbol ParamKind = "symbol" // a US stock/ETF ticker, upper-cased
	KindEnum   ParamKind = "enum"   // one of Enum
	KindInt    ParamKind = "int"    // an integer in [Min, Max]
	KindSlug   ParamKind = "slug"   // lower-case word: letters, digits, '-', '_'
)

// Param is one typed, validated query parameter. It is bound by name (:Name)
// and never spliced into SQL text.
type Param struct {
	Name     string    `json:"name"`
	Desc     string    `json:"description"`
	Kind     ParamKind `json:"kind"`
	Enum     []string  `json:"enum,omitempty"`
	Min      int       `json:"min,omitempty"`
	Max      int       `json:"max,omitempty"`
	Required bool      `json:"required,omitempty"`
	// Default is bound when the param is absent (nil = SQL NULL, which every
	// optional filter treats as "no filter").
	Default any `json:"default,omitempty"`
}

// Query is one catalog entry.
type Query struct {
	Name    string   `json:"name"`
	Desc    string   `json:"description"`
	Params  []Param  `json:"params"`
	Tier    Tier     `json:"tier"`
	MaxRows int      `json:"-"`
	Columns []string `json:"-"`
	SQL     string   `json:"-"`
	// Scoped queries read only the CALLER's rows: the server binds :uid from
	// the session. It is never a parameter, so no plan can name another user.
	Scoped bool `json:"-"`
	// PerUserReason says why a Scoped entry answers members differently; every
	// other member entry gives every member the same rows (impersonal).
	PerUserReason string `json:"-"`
}

// MaxQueries is the most entries one plan may run.
const MaxQueries = 3

var (
	symbolRe = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,9}$`)
	slugRe   = regexp.MustCompile(`^[a-z0-9_\-]{1,40}$`)
)

// kinds are the stock regime-forecast kinds (internal/structregime); the
// -crypto kinds are not part of the member product and stocks-only joins drop
// them anyway.
var kinds = []string{"trend21", "trend63", "liquidity21", "vol21", "gapfill5"}

var (
	pSymbol = Param{Name: "symbol", Desc: "US stock or ETF ticker, e.g. AAPL", Kind: KindSymbol, Required: true}
	pSymOpt = Param{Name: "symbol", Desc: "optional ticker filter, e.g. MSFT", Kind: KindSymbol}
	pDays   = func(def, max int) Param {
		return Param{Name: "days", Desc: fmt.Sprintf("lookback in days (1-%d, default %d)", max, def),
			Kind: KindInt, Min: 1, Max: max, Default: def}
	}
)

// Catalog is every query the copilot can run. Every SQL statement is a single
// SELECT (or WITH ... SELECT) with named parameters; catalog_test.go holds the
// shape and runs each one against a fresh schema.
var Catalog = []Query{
	{
		Name: "regime_forecasts_for_symbol",
		Desc: "The latest market-structure regime call for one stock, one row per kind (trend21, trend63, " +
			"liquidity21, vol21, gapfill5): the regime called, conviction 0-1, and the walk-forward " +
			"BACKTEST accuracy measured at that conviction tier (a backtest figure, not a live record).",
		Params: []Param{pSymbol}, Tier: TierMember, MaxRows: 10,
		Columns: []string{"symbol", "kind", "horizon_days", "regime", "conviction", "backtest_accuracy", "tier", "n", "computed_ts"},
		SQL: `SELECT s.symbol, f.kind, f.horizon_days, f.regime, ROUND(f.conviction, 3) AS conviction,
       ROUND(f.historical_accuracy, 3) AS backtest_accuracy, f.tier, f.n, f.ts AS computed_ts
FROM regime_forecasts f JOIN symbols s ON s.id = f.symbol_id
WHERE s.symbol = :symbol AND s.market = 'stocks'
ORDER BY f.kind`,
	},
	{
		Name: "strongest_regime_calls",
		Desc: "Stocks with the highest-conviction current call for one regime kind, optionally only those " +
			"calling one regime label (e.g. uptrend). Conviction 0-1 and backtest accuracy per row.",
		Params: []Param{
			{Name: "kind", Desc: "regime kind", Kind: KindEnum, Enum: kinds, Required: true},
			{Name: "regime", Desc: "optional regime label filter, e.g. uptrend, downtrend, elevated, calm", Kind: KindSlug},
		},
		Tier: TierMember, MaxRows: 20,
		Columns: []string{"symbol", "kind", "regime", "conviction", "backtest_accuracy", "tier", "computed_ts"},
		SQL: `SELECT s.symbol, f.kind, f.regime, ROUND(f.conviction, 3) AS conviction,
       ROUND(f.historical_accuracy, 3) AS backtest_accuracy, f.tier, f.ts AS computed_ts
FROM regime_forecasts f JOIN symbols s ON s.id = f.symbol_id
WHERE f.kind = :kind AND s.market = 'stocks' AND (:regime IS NULL OR f.regime = :regime)
ORDER BY f.conviction DESC, s.symbol`,
	},
	{
		Name: "vol_regime_for_symbol",
		Desc: "The latest volatility-regime call (elevated or calm) for one stock, with conviction and its " +
			"walk-forward BACKTEST accuracy at that tier.",
		Params: []Param{pSymbol}, Tier: TierMember, MaxRows: 1,
		Columns: []string{"symbol", "regime", "conviction", "backtest_accuracy", "tier", "n", "computed_ts"},
		SQL: `SELECT s.symbol, v.regime, ROUND(v.conviction, 3) AS conviction,
       ROUND(v.historical_accuracy, 3) AS backtest_accuracy, v.tier, v.n, v.ts AS computed_ts
FROM vol_forecasts v JOIN symbols s ON s.id = v.symbol_id
WHERE s.symbol = :symbol AND s.market = 'stocks'`,
	},
	{
		Name: "regime_live_grades_by_kind",
		Desc: "Live grading of frozen regime calls, per kind: how many calls have been graded, how many were " +
			"right, the hit rate, the mean accuracy CLAIMED at call time, and the naive 'nothing changes' " +
			"baseline's hits on the same rows. A raw tally: the published accuracy figure applies further " +
			"gates (grading epoch, quarantine) and lives on the Record page.",
		Tier: TierMember, MaxRows: 10,
		Columns: []string{"kind", "graded", "correct", "hit_rate", "mean_claimed_accuracy", "naive_graded", "naive_correct"},
		SQL: `SELECT o.kind, COUNT(*) AS graded, SUM(o.correct) AS correct, ROUND(AVG(o.correct), 3) AS hit_rate,
       ROUND(AVG(o.historical_accuracy), 3) AS mean_claimed_accuracy,
       SUM(o.naive_label IS NOT NULL) AS naive_graded,
       SUM(CASE WHEN o.naive_label IS NOT NULL AND o.naive_label = o.actual THEN 1 ELSE 0 END) AS naive_correct
FROM regime_outcomes o JOIN symbols s ON s.id = o.symbol_id
WHERE o.correct IS NOT NULL AND o.superseded_by IS NULL AND o.ungradable IS NULL AND s.market = 'stocks'
GROUP BY o.kind ORDER BY o.kind`,
	},
	{
		Name: "directional_track_record",
		Desc: "The directional (up/down) prediction track record per horizon over a lookback: resolved " +
			"predictions, how many called the direction right (probability above 0.5 = up), the hit rate, " +
			"and the share of outcomes that went up (the base rate to beat). Counts and rates only.",
		Params: []Param{pDays(90, 365)}, Tier: TierMember, MaxRows: 10,
		Columns: []string{"horizon", "resolved", "directional_hits", "hit_rate", "base_rate_up", "first_ts", "last_ts"},
		SQL: `SELECT o.horizon, COUNT(*) AS resolved,
       SUM(CASE WHEN (o.prob > 0.5) = (o.up = 1) THEN 1 ELSE 0 END) AS directional_hits,
       ROUND(AVG(CASE WHEN (o.prob > 0.5) = (o.up = 1) THEN 1.0 ELSE 0.0 END), 4) AS hit_rate,
       ROUND(AVG(o.up), 4) AS base_rate_up, MIN(o.ts) AS first_ts, MAX(o.ts) AS last_ts
FROM prediction_outcomes o JOIN symbols s ON s.id = o.symbol_id
WHERE o.resolved_at IS NOT NULL AND o.up IS NOT NULL AND s.market = 'stocks'
  AND o.ts >= CAST(strftime('%s', 'now') AS INTEGER) - :days * 86400
GROUP BY o.horizon ORDER BY o.horizon`,
	},
	{
		Name:   "recent_regime_flips",
		Desc:   "Recent changes in a stock's trend regime label (from -> to), newest first, optionally for one ticker.",
		Params: []Param{pSymOpt, pDays(7, 90)}, Tier: TierMember, MaxRows: 50,
		Columns: []string{"symbol", "changed_ts", "from_regime", "to_regime"},
		SQL: `SELECT s.symbol, c.ts AS changed_ts, c.from_lbl AS from_regime, c.to_lbl AS to_regime
FROM regime_changes c JOIN symbols s ON s.id = c.symbol_id
WHERE s.market = 'stocks' AND (:symbol IS NULL OR s.symbol = :symbol)
  AND c.ts >= CAST(strftime('%s', 'now') AS INTEGER) - :days * 86400
ORDER BY c.ts DESC, s.symbol`,
	},
	{
		Name: "vol_forecast_record",
		Desc: "The realised-volatility forecast record per horizon (days): calls frozen, calls resolved, calls " +
			"that can never be graded (with a stated reason), and how many resolved calls landed closer to the " +
			"realised volatility than the random-walk null and than the EWMA null. Counts only.",
		Tier: TierMember, MaxRows: 10,
		Columns: []string{"horizon", "calls", "resolved", "ungradable", "beat_random_walk", "beat_ewma"},
		SQL: `SELECT f.horizon, COUNT(*) AS calls, SUM(f.actual IS NOT NULL) AS resolved,
       SUM(f.ungradable IS NOT NULL) AS ungradable,
       SUM(CASE WHEN f.actual IS NOT NULL AND ABS(f.rv_hat - f.actual) < ABS(f.null_rw - f.actual) THEN 1 ELSE 0 END) AS beat_random_walk,
       SUM(CASE WHEN f.actual IS NOT NULL AND ABS(f.rv_hat - f.actual) < ABS(f.null_ewma - f.actual) THEN 1 ELSE 0 END) AS beat_ewma
FROM rv_forecasts f JOIN symbols s ON s.id = f.symbol_id
WHERE s.market = 'stocks'
GROUP BY f.horizon ORDER BY f.horizon`,
	},
	{
		Name: "prereg_chain",
		Desc: "Pre-registration records, newest first: each study's kind, when it was registered, the hash of " +
			"its frozen spec and the hash-chain link. Optionally one kind only.",
		Params: []Param{{Name: "kind", Desc: "optional pre-registration kind filter", Kind: KindSlug}},
		Tier:   TierMember, MaxRows: 20,
		Columns: []string{"seq", "registered_ts", "kind", "spec_hash", "entry_hash", "note"},
		SQL: `SELECT seq, ts AS registered_ts, kind, spec_hash, entry_hash, note
FROM prereg_records WHERE (:kind IS NULL OR kind = :kind)
ORDER BY seq DESC`,
	},
	{
		Name: "my_journal_stats",
		Desc: "The asking member's OWN call journal, per horizon in sessions (1, 5, 21): calls made, open, " +
			"hits, misses, void and withdrawn. Never anyone else's.",
		Tier: TierMember, MaxRows: 3, Scoped: true,
		PerUserReason: "the caller's own journal calls (member_calls WHERE user_id = the session's id); never a SignalDeck forecast",
		Columns:       []string{"horizon", "calls", "open", "hits", "misses", "void", "withdrawn"},
		SQL: `SELECT horizon, COUNT(*) AS calls, SUM(status = 'open') AS open,
       SUM(outcome = 'hit') AS hits, SUM(outcome = 'miss') AS misses,
       SUM(status = 'void') AS void, SUM(status = 'withdrawn') AS withdrawn
FROM member_calls WHERE user_id = :uid
GROUP BY horizon ORDER BY horizon`,
	},
	{
		Name: "postmortem_clusters",
		Desc: "Why directional predictions failed, grouped by primary reason over a lookback: misses per " +
			"reason, mean conviction and the latest one.",
		Params: []Param{pDays(30, 365)}, Tier: TierOperator, MaxRows: 25,
		Columns: []string{"primary_reason", "misses", "mean_conviction", "latest_ts"},
		SQL: `SELECT primary_reason, COUNT(*) AS misses, ROUND(AVG(conviction), 3) AS mean_conviction,
       MAX(ts) AS latest_ts
FROM prediction_postmortems
WHERE created_at >= CAST(strftime('%s', 'now') AS INTEGER) - :days * 86400
GROUP BY primary_reason ORDER BY misses DESC, primary_reason`,
	},
	{
		Name: "paper_book_summary",
		Desc: "Each simulated paper-trading book: cash, latest marked equity, open positions, trades filled, " +
			"when it started and the last bar it acted on.",
		Tier: TierOperator, MaxRows: 20,
		Columns: []string{"strategy", "cash", "equity", "open_positions", "trades", "started_ts", "last_bar_ts"},
		SQL: `SELECT c.strategy, ROUND(c.cash, 2) AS cash,
       (SELECT ROUND(e.equity, 2) FROM paper_equity e WHERE e.strategy = c.strategy ORDER BY e.ts DESC LIMIT 1) AS equity,
       (SELECT COUNT(*) FROM paper_positions p WHERE p.strategy = c.strategy) AS open_positions,
       (SELECT COUNT(*) FROM paper_trades t WHERE t.strategy = c.strategy) AS trades,
       c.started_ts, c.last_bar_ts
FROM paper_cursor c ORDER BY c.strategy`,
	},
	{
		Name: "worker_health",
		Desc: "Each background worker's latest run: status (ok, degraded, error, running, orphaned), start " +
			"and finish time and the start of its detail text. Not-ok first; status=not_ok shows only those.",
		Params: []Param{{Name: "status", Desc: "all (default) or not_ok", Kind: KindEnum,
			Enum: []string{"all", "not_ok"}, Default: "all"}},
		Tier: TierOperator, MaxRows: 50,
		Columns: []string{"worker", "status", "started_ts", "finished_ts", "detail"},
		SQL: `SELECT w.worker, w.status, w.started_at AS started_ts, w.finished_at AS finished_ts,
       substr(w.detail, 1, 200) AS detail
FROM worker_runs w
JOIN (SELECT worker, MAX(started_at) AS m FROM worker_runs GROUP BY worker) l
  ON l.worker = w.worker AND l.m = w.started_at
WHERE (:status = 'all' OR w.status <> 'ok')
ORDER BY (w.status = 'ok'), w.worker`,
	},
}

// Find returns the catalog entry with that name.
func Find(name string) (Query, bool) {
	for _, q := range Catalog {
		if q.Name == name {
			return q, true
		}
	}
	return Query{}, false
}

// Allowed reports whether a caller of tier may run q.
func (q Query) Allowed(caller Tier) bool {
	return caller == TierOperator || q.Tier == TierMember
}

// For is the catalog a caller of tier may use.
func For(caller Tier) []Query {
	var out []Query
	for _, q := range Catalog {
		if q.Allowed(caller) {
			out = append(out, q)
		}
	}
	return out
}

// Call is one validated query invocation.
type Call struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

// Validate checks one model-proposed invocation against the catalog and the
// caller's tier, returning the params to bind (defaults filled in). Any name,
// key or value the catalog does not declare is an error: nothing the model
// writes reaches SQL except a validated value bound by name.
func Validate(caller Tier, name string, raw map[string]any) (Query, map[string]any, error) {
	q, ok := Find(name)
	if !ok {
		return Query{}, nil, fmt.Errorf("unknown query %q", name)
	}
	if !q.Allowed(caller) {
		return Query{}, nil, fmt.Errorf("query %q is not available to this account", name)
	}
	declared := map[string]Param{}
	for _, p := range q.Params {
		declared[p.Name] = p
	}
	for k := range raw {
		if _, ok := declared[k]; !ok {
			return Query{}, nil, fmt.Errorf("query %q takes no parameter %q", name, k)
		}
	}
	out := map[string]any{}
	for _, p := range q.Params {
		v, present := raw[p.Name]
		if !present || v == nil {
			if p.Required {
				return Query{}, nil, fmt.Errorf("query %q needs %q", name, p.Name)
			}
			out[p.Name] = p.Default
			continue
		}
		val, err := p.check(v)
		if err != nil {
			return Query{}, nil, fmt.Errorf("query %q parameter %q: %w", name, p.Name, err)
		}
		out[p.Name] = val
	}
	return q, out, nil
}

func (p Param) check(v any) (any, error) {
	switch p.Kind {
	case KindSymbol:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("must be a ticker string")
		}
		s = strings.ToUpper(strings.TrimSpace(s))
		if !symbolRe.MatchString(s) {
			return nil, fmt.Errorf("%q is not a ticker", s)
		}
		return s, nil
	case KindSlug:
		s, ok := v.(string)
		if !ok || !slugRe.MatchString(s) {
			return nil, fmt.Errorf("must be a lower-case word")
		}
		return s, nil
	case KindEnum:
		s, ok := v.(string)
		if ok {
			for _, e := range p.Enum {
				if s == e {
					return s, nil
				}
			}
		}
		return nil, fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
	case KindInt:
		f, ok := v.(float64) // encoding/json decodes every number as float64
		if !ok || f != float64(int64(f)) || f < float64(p.Min) || f > float64(p.Max) {
			return nil, fmt.Errorf("must be a whole number from %d to %d", p.Min, p.Max)
		}
		return int64(f), nil
	}
	return nil, fmt.Errorf("undeclared kind %q", p.Kind)
}
