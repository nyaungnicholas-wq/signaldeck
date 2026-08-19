// Package srchealth is the data-source freshness auditor's single source of
// truth: the registry of every EXTERNAL data source, its expected-max-staleness
// budget, and the pure classification of "is this source quietly dead?".
//
// It exists because a scraper can drift or die while its worker keeps returning
// ok — the run log stays green but the data stops. This package answers the one
// question that log can't: for each source, how old is the NEWEST row, and is
// that older than the source's real cadence allows RIGHT NOW.
//
// "Right now" matters: market-gated stock sources are legitimately quiet when
// the exchange is closed (weekend/holiday/overnight), so they are reported
// "market closed" — NOT stale — via internal/marketcal. Crypto and other 24/7
// sources are always checked. Both the source-audit pipeline worker (which
// records dq_events for stale sources) and GET /api/source-health call Evaluate,
// so the dashboard and the alerts can never disagree.
package srchealth

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
)

// Store is the read surface Evaluate needs (satisfied by *store.Store); kept as
// an interface so the registry can be exercised against a fixture DB in tests.
type Store interface {
	MaxSourceTs(ctx context.Context, query string) (int64, bool, error)
}

// spec describes one external source's freshness contract.
type spec struct {
	source string        // stable id, matches the table
	query  string        // SELECT one nullable unix-seconds int (the newest row)
	budget time.Duration // max tolerated age of the newest row (generous multiple of the worker cadence)
	gated  bool          // true = a stock-hours source: not stale while the market is legitimately closed
	note   string        // human-readable cadence/caveat
	// webhook marks the tv-webhook signal source: it is only judged when a
	// webhook secret is configured AND the market is open — the case that
	// catches a silently-dead tunnel/webhook while trading is live.
	webhook bool
}

// registry is the canonical source list. Budgets are deliberately generous
// multiples of each source's worker cadence so a single missed pass never
// alarms — only a source that has gone genuinely quiet does. Date-only tables
// (settlement_date/report_date/day) resolve to that day's UTC midnight, which
// only makes the reported age SMALLER (more conservative), never larger.
var registry = []spec{
	{source: "tv_quotes", query: `SELECT MAX(ts) FROM tv_quotes`,
		budget: 30 * time.Minute, gated: true,
		note: "TradingView quote tape (60s worker, market hours)"},
	{source: "tv_ratings", query: `SELECT MAX(ts) FROM tv_ratings`,
		budget: 6 * time.Hour, gated: true,
		note: "TradingView TA ratings (15m worker, market hours)"},
	{source: "short_volume", query: `SELECT CAST(strftime('%s', MAX(day)) AS INTEGER) FROM short_volume`,
		// 6d, gated. FINRA publishes this file EVERY session at 18:30 ET, so the
		// budget is set by the longest legitimate gap between consecutive trade
		// dates, not by a worker interval. Measured over the live table's full
		// history (49 trading days), that gap is 4 calendar days and occurs on
		// long holiday weekends (2026-05-26 Memorial Day, 2026-07-06 after the
		// July 4 observance). Gated to 9:45-16:00 ET caps the intraday term at
		// ~7h on top of it, so the worst honest age is ~4d21h; allowing one
		// missed publication on top of a holiday weekend puts the ceiling at
		// ~5d21h. 6d clears that and still catches a feed that has stopped:
		// short_interest cannot cover this failure for us, because ITS honest
		// age reaches ~28d and no budget that tolerates that can see a
		// week-old daily-feed outage (SD-H35).
		budget: 6 * 24 * time.Hour, gated: true,
		note: "FINRA Reg SHO daily short-sale volume (per-session file, published 18:30 ET)"},
	{source: "short_interest", query: `SELECT CAST(strftime('%s', MAX(settlement_date)) AS INTEGER) FROM short_interest`,
		// 35d, not 20d: settlement is bi-monthly (~15d apart) AND publication
		// lags ~9 business days (~13 calendar), so the freshest POSSIBLE cycle is
		// legitimately up to ~28d old just before the next one publishes. A 20d
		// budget false-flags near every cycle boundary — the auditor must not
		// cry wolf when the worker already holds the newest available file.
		budget: 35 * 24 * time.Hour, gated: false,
		note: "FINRA bi-monthly short interest (settlement-dated, ~2wk publication lag)"},
	{source: "crypto_perp", query: `SELECT MAX(ts) FROM crypto_perp`,
		budget: 2 * time.Hour, gated: false,
		note: "Hyperliquid perp funding/OI (15m worker, crypto 24/7)"},
	{source: "cot_reports", query: `SELECT CAST(strftime('%s', MAX(report_date)) AS INTEGER) FROM cot_reports`,
		budget: 10 * 24 * time.Hour, gated: false,
		note: "CFTC Commitments of Traders (weekly, Fri publish of Tue positions)"},
	{source: "stocktwits_sentiment", query: `SELECT MAX(ts) FROM stocktwits_sentiment`,
		budget: 6 * time.Hour, gated: true,
		note: "StockTwits page-snapshot sentiment (15m worker, market hours)"},
	{source: "wiki_views", query: `SELECT CAST(strftime('%s', MAX(day)) AS INTEGER) FROM wiki_views`,
		budget: 3 * 24 * time.Hour, gated: false,
		note: "Wikipedia daily page views (24h worker, attention proxy)"},
	{source: "cboe_pc", query: `SELECT CAST(strftime('%s', MAX(day)) AS INTEGER) FROM cboe_pc`,
		budget: 3 * 24 * time.Hour, gated: true,
		note: "CBOE market-wide put/call (6h worker, ~3 trading-day budget)"},
	{source: "news", query: `SELECT MAX(ts) FROM news`,
		budget: 12 * time.Hour, gated: true,
		note: "Alpaca news headlines (market hours)"},
	{source: "bars_1m_hot", query: `SELECT MAX(ts) FROM bars WHERE tf='1m' AND symbol_id IN (SELECT id FROM symbols WHERE stream=1)`,
		budget: 30 * time.Minute, gated: true,
		note: "streamed hot-set 1m bars (live ws, market hours)"},
	{source: "snapshots_1s", query: `SELECT MAX(ts) FROM snapshots_1s`,
		budget: 10 * time.Minute, gated: false,
		note: "1s microstructure snapshots (crypto 24/7)"},
	{source: "tv_signals", query: `SELECT MAX(ts) FROM tv_signals`,
		budget: 6 * time.Hour, gated: true, webhook: true,
		note: "TradingView inbound webhook alerts (only judged when a secret is set + market open — catches a silent tunnel failure)"},
}

// Report is one source's freshness verdict (the /api/source-health row shape).
type Report struct {
	Source          string `json:"source"`
	LastTs          int64  `json:"lastTs"`          // newest row's unix ts; 0 when the source has no rows
	AgeSecs         int64  `json:"ageSecs"`         // now − lastTs; -1 when the source has no rows
	StaleBudgetSecs int64  `json:"staleBudgetSecs"` // the tolerated max age
	Stale           bool   `json:"stale"`
	MarketGated     bool   `json:"marketGated"`
	Note            string `json:"note"`
}

// Evaluate probes every registered source and classifies its freshness at time
// now. webhookSecretSet gates the tv_signals source (a webhook with no secret
// configured is disabled by design, so its silence is honest, not stale). It
// returns one Report per source, registry order preserved. Any query error is
// returned (the caller decides whether to fail its run or the request).
func Evaluate(ctx context.Context, st Store, now time.Time, webhookSecretSet bool) ([]Report, error) {
	open := marketcal.OpenForBars(now)
	out := make([]Report, 0, len(registry))
	for _, s := range registry {
		maxTs, ok, err := st.MaxSourceTs(ctx, s.query)
		if err != nil {
			return nil, err
		}
		r := Report{
			Source:          s.source,
			StaleBudgetSecs: int64(s.budget / time.Second),
			MarketGated:     s.gated,
			Note:            s.note,
		}
		if ok {
			r.LastTs = maxTs
			r.AgeSecs = now.Unix() - maxTs
			// A real age of exactly noRowsAge would be indistinguishable from
			// the empty sentinel, so nudge it by a second. One second of
			// reported age is not a number anyone acts on; a source wrongly
			// labelled "no rows" is.
			if r.AgeSecs == noRowsAge {
				r.AgeSecs++
			}
		} else {
			r.AgeSecs = noRowsAge
		}

		switch {
		case s.webhook && !webhookSecretSet:
			// Webhook disabled (no secret) — its silence is by design.
			r.Note = "webhook disabled (no secret configured) — not checked"
		case s.gated && !open:
			// Market legitimately closed: an old newest row is expected, not stale.
			r.Note = "market closed — freshness check suppressed"
		case !ok:
			// No rows and the source SHOULD be producing now: quietly dead.
			r.Stale = true
			r.Note = s.note + " — no rows yet"
		case r.AgeSecs == noRowsAge:
			// Defensive: !ok above already covers the empty case, but if the
			// sentinel ever arrives another way it must not be read as "future".
			r.Stale = true
			r.Note = s.note + " — no rows yet"
		case r.AgeSecs < 0:
			// Newest row is in the future. Rows carry the provider's timestamp,
			// so this means our clock disagrees with theirs. Without this case
			// a negative age is never > the budget, and the source would read
			// as fresh forever no matter how long it had been dead. Reached
			// only when ok, so it cannot collide with the AgeSecs = -1 sentinel.
			r.Stale = true
			r.Note = s.note + " — newest row is in the future; check clock skew"
		case r.AgeSecs > r.StaleBudgetSecs:
			r.Stale = true
		}
		out = append(out, r)
	}
	return out, nil
}

// StaleCount is the number of stale sources in a report set.
func StaleCount(reports []Report) int {
	n := 0
	for _, r := range reports {
		if r.Stale {
			n++
		}
	}
	return n
}

// ageString renders a duration in seconds as a compact human string for dq
// detail lines ("no rows", "ahead of our clock", "42m", "3h", "5d").
//
// Negative used to mean only "no rows", but a row stamped AHEAD of our clock
// also yields a negative age, and the two are opposite problems. The live feed
// read "source=crypto_perp newest row NO ROWS old … newest row is in the
// future; check clock skew" — one sentence claiming the table is both empty
// and holding a future row, which sends the reader to look for a dead source
// when the actual issue is a timestamp. Only the exact sentinel means empty.
func ageString(secs int64) string {
	if secs == noRowsAge {
		return "no rows"
	}
	if secs < 0 {
		return "ahead of our clock by " + ageString(-secs)
	}
	d := time.Duration(secs) * time.Second
	switch {
	case d < time.Minute:
		// Sub-minute ages need their own branch. d.Round(time.Minute) collapses
		// anything under 30s to "0s", and trimming that suffix leaves the EMPTY
		// string — so the live feed printed "newest row ahead of our clock by
		// old (budget 10m)" every day for crypto_perp, snapshots_1s and
		// bars_1m_hot. Clock skew is measured in seconds, so the branch that
		// rendered it was the one branch that erased it, deleting the single
		// number the line exists to carry.
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	case d < time.Hour:
		return strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
	case d < 24*time.Hour:
		return strings.TrimSuffix(d.Round(time.Hour).String(), "0m0s")
	default:
		return strconv.Itoa(int(d.Round(24*time.Hour)/(24*time.Hour))) + "d"
	}
}

// noRowsAge is the AgeSecs sentinel for "the source has no rows at all". It is
// deliberately not -1: a row one second ahead of our clock produces an age of
// -1 too, and conflating an EMPTY source with a FUTURE-stamped one made the
// live feed contradict itself.
const noRowsAge int64 = -1 << 40

// DetailLine composes the dq_events detail string for a stale source (source=
// prefix first so the per-source-per-day dedup LIKE match is cheap).
func DetailLine(r Report) string {
	return "source=" + r.Source + " newest row " + ageString(r.AgeSecs) +
		" old (budget " + ageString(r.StaleBudgetSecs) + ") — " + r.Note
}
