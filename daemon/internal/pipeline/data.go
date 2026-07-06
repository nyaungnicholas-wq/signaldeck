package pipeline

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/aiagents/sentiment"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/news"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sectors"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// envInt reads an integer env override, falling back to def when unset or
// malformed. Used for operator tunables that don't warrant config plumbing.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// defaultNewsTopRanked is how many of the latest ranking's top symbols get
// news coverage (env SIGNALDECK_NEWS_TOP_RANKED overrides).
const defaultNewsTopRanked = 25

// metaNewsScopeSkip gates the ONE-TIME backlog cleanup that relabels unrated
// headlines of out-of-scope symbols as sentiment='skipped'. Once the key is
// set the cleanup never runs again.
const metaNewsScopeSkip = "news_scope_skip_v1"

// NewsFetcher pulls per-symbol headlines from Alpaca into the DB (stocks only;
// Alpaca news doesn't cover crypto pairs).
//
// Fetching is SCOPED, not universe-wide: pulling news for all ~500 active
// symbols (incl. the broad daily-only universe) piled up a multi-thousand
// headline backlog the LLM daily cap can never tag. Only symbols worth the
// sentiment budget are fetched — see newsScope. On its first run after this
// policy landed it also performs a one-time, meta-gated cleanup marking the
// out-of-scope unrated backlog as 'skipped' (honest terminal label: not
// rated by choice, not pending).
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
	scope, err := w.newsScope(ctx, syms)
	if err != nil {
		return "", err
	}
	cleaned, err := w.skipOutOfScopeBacklog(ctx, scope)
	if err != nil {
		return "", fmt.Errorf("backlog cleanup: %w", err)
	}
	got, covered := 0, 0
	for _, s := range syms {
		if s.Market != md.Stocks || !scope[s.ID] {
			continue
		}
		n, err := w.Client.Ingest(ctx, w.St, s.ID, s.Symbol)
		if err != nil {
			return "", fmt.Errorf("%s: %w", s.Symbol, err)
		}
		got += n
		covered++
	}
	detail := fmt.Sprintf("ingested %d headlines across %d in-scope symbols", got, covered)
	if cleaned > 0 {
		detail += fmt.Sprintf(" (one-time cleanup: %d out-of-scope headlines marked skipped)", cleaned)
	}
	return detail, nil
}

// newsScope returns the set of symbol ids worth news-API calls and LLM
// sentiment budget: the streamed hot set (Stream flag or crypto market), the
// top-N of the latest cross-sectional ranking (N via
// SIGNALDECK_NEWS_TOP_RANKED, default 25), and every symbol on ANY user's
// watchlist. The rest of the broad daily-only universe is out of scope — its
// headlines would only grow an untaggable backlog against the LLM daily cap.
// Ranking and watchlist absence degrade gracefully (empty contribution).
func (w *NewsFetcher) newsScope(ctx context.Context, active []md.Symbol) (map[int64]bool, error) {
	scope := make(map[int64]bool)
	for _, s := range active {
		if s.Stream || s.Market == md.Crypto {
			scope[s.ID] = true
		}
	}

	// Top-N by latest ranking score (RankingPercentiles = newest snapshot,
	// symbol_id → score; empty map when no ranking has run yet).
	perc, err := w.St.RankingPercentiles(ctx)
	if err != nil {
		return nil, err
	}
	type rankedID struct {
		id    int64
		score float64
	}
	ranked := make([]rankedID, 0, len(perc))
	for id, sc := range perc {
		ranked = append(ranked, rankedID{id: id, score: sc})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id // deterministic tie-break
	})
	topN := envInt("SIGNALDECK_NEWS_TOP_RANKED", defaultNewsTopRanked)
	for i := 0; i < len(ranked) && i < topN; i++ {
		scope[ranked[i].id] = true
	}

	// Anything any user watches.
	watched, err := w.St.WatchedSymbolIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range watched {
		scope[id] = true
	}
	return scope, nil
}

// skipOutOfScopeBacklog is the ONE-TIME (meta-gated) migration for headlines
// fetched under the old fetch-everything policy: unrated rows for symbols now
// OUTSIDE the news scope are relabeled sentiment='skipped', so the pending
// queue reflects only work the tagger will actually do. Runs exactly once —
// the meta key records when and how many. Returns rows relabeled (0 when the
// gate is already set).
func (w *NewsFetcher) skipOutOfScopeBacklog(ctx context.Context, scope map[int64]bool) (int64, error) {
	done, err := w.St.GetMeta(ctx, metaNewsScopeSkip)
	if err != nil {
		return 0, err
	}
	if done != "" {
		return 0, nil
	}
	keep := make([]int64, 0, len(scope))
	for id := range scope {
		keep = append(keep, id)
	}
	n, err := w.St.SkipUnratedOutside(ctx, keep)
	if err != nil {
		return 0, err
	}
	return n, w.St.SetMeta(ctx, metaNewsScopeSkip,
		fmt.Sprintf("done ts=%d skipped=%d", time.Now().Unix(), n))
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
	// The provider limit is burst-shaped (past ~35 rapid calls → HTTP 429),
	// not throughput-shaped, so pace the calls and take a bigger batch:
	// 60 headlines at one call per 5s stays far below the burst limit while
	// draining a backlog ~6x faster than the old 12-per-pass burst. The LLM
	// daily cap still bounds total spend. Tunable via env for other providers.
	batch := envInt("SIGNALDECK_SENTIMENT_BATCH", 60)
	pace := time.Duration(envInt("SIGNALDECK_SENTIMENT_PACE_MS", 5000)) * time.Millisecond
	n, err := sentiment.RunOnce(ctx, w.LLM, w.St, batch, pace)
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
