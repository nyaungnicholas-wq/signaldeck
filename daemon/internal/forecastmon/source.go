package forecastmon

// Store-backed Source. Lives here rather than at each call site so the daemon
// worker and cmd/forecastmon cannot drift into measuring different things —
// which is exactly how a monitor ends up reporting on something other than what
// the operator is looking at.

import (
	"context"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// NewStoreSource adapts the live store to Source.
func NewStoreSource(st *store.Store) Source { return storeSource{st: st} }

type storeSource struct{ st *store.Store }

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
		out = append(out, Bucket{Label: r.Label, N: r.N, Said: r.Said, Actual: r.Actual})
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
