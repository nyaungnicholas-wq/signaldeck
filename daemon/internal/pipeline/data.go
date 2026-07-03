package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/aiagents/sentiment"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/news"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sectors"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// NewsFetcher pulls per-symbol headlines from Alpaca into the DB (stocks only;
// Alpaca news doesn't cover crypto pairs).
type NewsFetcher struct {
	St     *store.Store
	Client *news.Client // nil when no Alpaca key
}

func (w *NewsFetcher) Name() string            { return "news-fetcher" }
func (w *NewsFetcher) Interval() time.Duration { return 20 * time.Minute }

func (w *NewsFetcher) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no Alpaca key", nil
	}
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	got := 0
	for _, s := range syms {
		if s.Market != md.Stocks {
			continue
		}
		n, err := w.Client.Ingest(ctx, w.St, s.ID, s.Symbol)
		if err != nil {
			return "", fmt.Errorf("%s: %w", s.Symbol, err)
		}
		got += n
	}
	return fmt.Sprintf("ingested %d headlines", got), nil
}

// SentimentTagger LLM-tags any unrated headlines. Degrades to no-op without a
// key (headlines stay "unrated").
type SentimentTagger struct {
	St  *store.Store
	LLM llm.Client
}

func (w *SentimentTagger) Name() string            { return "sentiment-tagger" }
func (w *SentimentTagger) Interval() time.Duration { return 10 * time.Minute }

func (w *SentimentTagger) Run(ctx context.Context) (string, error) {
	if !w.LLM.Enabled() {
		return "skipped: no LLM key", nil
	}
	// Small batch every 10 min: gentle on the free-tier rate limit (bursts
	// past ~35 rapid calls get HTTP 429), and it steadily drains the backlog.
	n, err := sentiment.RunOnce(ctx, w.LLM, w.St, 12)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("tagged %d headlines", n), nil
}

// SectorRotator aggregates the latest ranking into sector strength + rotation.
// It stores the result via meta (small, single blob) so the API can serve it
// without a dedicated table.
type SectorRotator struct {
	St *store.Store
}

func (w *SectorRotator) Name() string            { return "sector-rotator" }
func (w *SectorRotator) Interval() time.Duration { return time.Hour }

func (w *SectorRotator) Run(ctx context.Context) (string, error) {
	ranked, err := w.St.LatestRanking(ctx)
	if err != nil {
		return "", err
	}
	if len(ranked) == 0 {
		return "no ranking yet", nil
	}
	inputs := make([]sectors.Input, 0, len(ranked))
	for _, r := range ranked {
		inputs = append(inputs, sectors.Input{Symbol: r.Symbol, Score: r.Score, Ret1M: r.Ret1M})
	}
	agg := sectors.Aggregate(inputs)
	if err := w.St.SetJSON(ctx, "sector_agg", agg); err != nil {
		return "", err
	}
	return fmt.Sprintf("aggregated %d sectors", len(agg)), nil
}
