package forecastmon

// Store-backed Source. Lives here rather than at each call site so the daemon
// worker and cmd/forecastmon cannot drift into measuring different things —
// which is exactly how a monitor ends up reporting on something other than what
// the operator is looking at.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// NewStoreSource adapts the live store to Source.
func NewStoreSource(st *store.Store) Source { return storeSource{st: st} }

type storeSource struct{ st *store.Store }

// ModelEmitting reads the directional model's published state out of meta,
// where the model-health worker writes it.
//
// EVERY failure path returns emitting=true on purpose. This value only ever
// DOWNGRADES a starvation report from error to degraded, so guessing "retired"
// when the state is unreadable would silence the coverage failure the monitor
// exists to catch. Unknown must therefore mean "assume it is live and shout".
func (s storeSource) ModelEmitting(ctx context.Context, horizon string) (bool, string, error) {
	var raw string
	err := s.st.DB().QueryRowContext(ctx, `SELECT v FROM meta WHERE k = ?`,
		"model_health:directional-ensemble-"+horizon).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true, "ungraded", nil // never graded: not a licence to go quiet
	case err != nil:
		return true, "unreadable", err
	}
	var mh struct {
		Emitting bool   `json:"emitting"`
		Verdict  string `json:"verdict"`
	}
	if json.Unmarshal([]byte(raw), &mh) != nil {
		return true, "unparseable", nil
	}
	return mh.Emitting, mh.Verdict, nil
}

func (s storeSource) DayStats(ctx context.Context, horizon string, since time.Time) ([]DayStat, error) {
	rows, err := s.st.ForecastDayStats(ctx, horizon, since)
	if err != nil {
		return nil, err
	}
	out := make([]DayStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, DayStat{Day: r.Day, Symbols: r.Symbols, DistinctProbs: r.DistinctProbs})
	}
	return out, nil
}

func (s storeSource) Buckets(ctx context.Context, horizon string, since time.Time) ([]Bucket, float64, int, error) {
	rows, base, days, err := s.st.ForecastBuckets(ctx, horizon, since)
	if err != nil {
		return nil, 0, 0, err
	}
	out := make([]Bucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, Bucket{Label: r.Label, N: r.N, Days: r.Days,
			Said: r.Said, Actual: r.Actual, DayRates: r.DayRates})
	}
	return out, base, days, nil
}

func (s storeSource) RawDayStats(ctx context.Context, horizon string, since time.Time) ([]DayStat, error) {
	rows, err := s.st.ForecastDayStatsRaw(ctx, horizon, since)
	if err != nil {
		return nil, err
	}
	out := make([]DayStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, DayStat{
			Day: r.Day, Symbols: r.Symbols, DistinctProbs: r.DistinctProbs, Withheld: r.Withheld,
		})
	}
	return out, nil
}
