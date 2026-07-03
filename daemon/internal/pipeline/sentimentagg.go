package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// SentimentAggregator rolls rated news headlines up into the permanent
// sentiment_daily archive (per symbol, per UTC day). Each pass recomputes
// today's AND yesterday's aggregates — a cheap idempotent upsert that
// absorbs late-tagged headlines and articles that land near midnight UTC.
// Older days never change (news rows are immutable once rated), so two days
// of recompute is complete coverage.
type SentimentAggregator struct {
	St *store.Store
}

func (w *SentimentAggregator) Name() string            { return "sentiment-aggregator" }
func (w *SentimentAggregator) Interval() time.Duration { return 30 * time.Minute }

func (w *SentimentAggregator) Run(ctx context.Context) (string, error) {
	now := time.Now().UTC()
	total := 0
	for _, day := range []string{
		now.AddDate(0, 0, -1).Format("2006-01-02"),
		now.Format("2006-01-02"),
	} {
		n, err := w.St.UpsertSentimentDaily(ctx, day)
		if err != nil {
			return "", fmt.Errorf("aggregate %s: %w", day, err)
		}
		total += n
	}
	return fmt.Sprintf("upserted %d symbol-day aggregate(s)", total), nil
}
