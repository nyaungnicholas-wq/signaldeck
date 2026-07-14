// Store methods for alert forward-outcome stats (SIGNALS-hub overhaul: "the
// honesty differentiator — feeds that show their receipts"). Kept in this
// file — separate from alerts.go — so the stats read never touches the alert
// write paths.
//
// HONESTY: these are FORWARD RETURNS AFTER ALERTS, measured against the
// symbol's own stored daily bars — descriptive receipts, not advice, and
// never a claim of causation. Kinds with fewer than MinAlertOutcomeN
// resolvable forward returns get NULL stats with the count shown — a median
// over a handful of alerts is noise dressed as evidence.
package store

import (
	"context"
	"sort"
)

// MinAlertOutcomeN is the floor of resolvable forward returns below which a
// kind's stats are withheld (returned as NULL with n shown). Mirrors the
// independent-N gates in api/trackrecord.go and adaptive.MinCellSamples.
const MinAlertOutcomeN = 20

// AlertKindOutcome is one alert kind's forward-outcome stats. N counts
// DISTINCT fired events (symbol+ts, deduped across users — the same event
// fans out to every watching user); N1d/N5d count events whose forward
// return was resolvable from stored daily bars. Gated stats are nil.
type AlertKindOutcome struct {
	Kind string `json:"kind"`
	N    int    `json:"n"`
	N1d  int    `json:"n1d"`
	N5d  int    `json:"n5d"`
	// 1-trading-day forward stats (nil when N1d < MinAlertOutcomeN).
	Mean1d    *float64 `json:"mean1d"`
	Median1d  *float64 `json:"median1d"`
	HitRate1d *float64 `json:"hitRate1d"` // share of fwd 1d returns > 0
	Gated1d   bool     `json:"gated1d"`
	// 5-trading-day forward stats (nil when N5d < MinAlertOutcomeN).
	Mean5d   *float64 `json:"mean5d"`
	Median5d *float64 `json:"median5d"`
	Gated5d  bool     `json:"gated5d"`
}

// AlertKindOutcomes measures, per alerts.kind, what the symbol's price
// actually did after the alert fired: median + mean forward return over the
// next 1 and 5 daily bars, and the 1d hit rate (fwd > 0). The base price is
// the close of the last daily bar at/before the alert; "1d"/"5d" are the 1st
// and 5th stored daily bars after it (trading days, not calendar days).
// Alerts without a symbol (e.g. correlation breaks) are skipped — there is
// no price series to grade them against. Kinds are returned alphabetically.
func (s *Store) AlertKindOutcomes(ctx context.Context, sinceTs int64) ([]AlertKindOutcome, error) {
	// Distinct fired events: the same breakout alert inserted for N users is
	// ONE event, not N observations.
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT kind, symbol_id, ts FROM alerts
		WHERE symbol_id IS NOT NULL AND ts >= ?`, sinceTs)
	if err != nil {
		return nil, err
	}
	type fired struct {
		kind     string
		symbolID int64
		ts       int64
	}
	var events []fired
	symbolSet := map[int64]bool{}
	for rows.Next() {
		var f fired
		if err := rows.Scan(&f.kind, &f.symbolID, &f.ts); err != nil {
			rows.Close() //nolint:errcheck
			return nil, err
		}
		events = append(events, f)
		symbolSet[f.symbolID] = true
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Daily close series per involved symbol, ascending — one query per
	// symbol, loaded once regardless of how many alerts it fired.
	type series struct {
		ts     []int64
		closes []float64
	}
	bars := map[int64]*series{}
	for id := range symbolSet {
		r, err := s.db.QueryContext(ctx, `
			SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts`, id)
		if err != nil {
			return nil, err
		}
		sr := &series{}
		for r.Next() {
			var ts int64
			var c float64
			if err := r.Scan(&ts, &c); err != nil {
				r.Close() //nolint:errcheck
				return nil, err
			}
			sr.ts = append(sr.ts, ts)
			sr.closes = append(sr.closes, c)
		}
		r.Close() //nolint:errcheck
		if err := r.Err(); err != nil {
			return nil, err
		}
		bars[id] = sr
	}

	// Grade each event: base = last daily bar at/before the alert ts; fwd
	// returns come from the 1st/5th bars after it, when they exist.
	type agg struct {
		n          int
		fwd1, fwd5 []float64
		hits1      int
	}
	byKind := map[string]*agg{}
	for _, ev := range events {
		a := byKind[ev.kind]
		if a == nil {
			a = &agg{}
			byKind[ev.kind] = a
		}
		a.n++
		sr := bars[ev.symbolID]
		if sr == nil || len(sr.ts) == 0 {
			continue
		}
		// idx of the last bar with ts <= alert ts.
		idx := sort.Search(len(sr.ts), func(i int) bool { return sr.ts[i] > ev.ts }) - 1
		if idx < 0 || sr.closes[idx] <= 0 {
			continue
		}
		base := sr.closes[idx]
		if idx+1 < len(sr.ts) {
			f1 := sr.closes[idx+1]/base - 1
			a.fwd1 = append(a.fwd1, f1)
			if f1 > 0 {
				a.hits1++
			}
		}
		if idx+5 < len(sr.ts) {
			a.fwd5 = append(a.fwd5, sr.closes[idx+5]/base-1)
		}
	}

	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	out := make([]AlertKindOutcome, 0, len(kinds))
	for _, k := range kinds {
		a := byKind[k]
		o := AlertKindOutcome{Kind: k, N: a.n, N1d: len(a.fwd1), N5d: len(a.fwd5)}
		// Gate: below MinAlertOutcomeN resolvable returns the stats are
		// withheld as NULL — the count is shown so the gate renders as a
		// reason, never silently.
		if o.N1d >= MinAlertOutcomeN {
			m, med := meanMedian(a.fwd1)
			hr := float64(a.hits1) / float64(o.N1d)
			o.Mean1d, o.Median1d, o.HitRate1d = &m, &med, &hr
		} else {
			o.Gated1d = true
		}
		if o.N5d >= MinAlertOutcomeN {
			m, med := meanMedian(a.fwd5)
			o.Mean5d, o.Median5d = &m, &med
		} else {
			o.Gated5d = true
		}
		out = append(out, o)
	}
	return out, nil
}

// meanMedian returns the mean and median of vals (len >= 1).
func meanMedian(vals []float64) (mean, median float64) {
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	mean = sum / float64(len(sorted))
	if n := len(sorted); n%2 == 1 {
		median = sorted[n/2]
	} else {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return mean, median
}
