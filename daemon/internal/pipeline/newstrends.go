// NEWS-TRENDS wave — worker: news-trends (30m tick).
//
// Turns the news archive into two DESCRIPTIVE trend reads, deterministically
// (no LLM anywhere):
//
//   - Per-symbol NEWS-VOLUME Z: for every symbol with fresh news (a headline
//     in the last 48h) it recomputes today's UTC headline count and its
//     z-score vs the symbol's OWN trailing-30-day baseline, upserting one
//     news_trends(symbol_id, day, n, z) row per pass (idempotent — late
//     headlines simply correct the row). The honesty gate (>=10 prior days
//     with any news, non-flat baseline) stores z as NULL when unmet — an
//     absent z is information, never a fabricated 0. The stored z also joins
//     the prediction FEATURE VECTOR (news_vol_z, featureVersion 4) so the
//     GBM leg can LEARN whether headline attention carries edge — the GBM's
//     OOS-lift gate keeps that honest automatically.
//
//   - Fleet TRENDING TOKENS: once per 6h (meta news_trends_insight_ts) it
//     extracts the top-10 tokens from the last 24h of headline titles
//     (lowercase, non-alpha split, embedded stopword list, <3-char and
//     tracked-ticker tokens dropped) and writes ONE market-scope insight
//     (kind news_trends) listing them with counts + distinct-symbol counts.
//
// HONESTY (carried verbatim by the insight and the API): headline-frequency
// trend — descriptive attention, not a forecast.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// newsTrendsNote ships verbatim with the insight and every API payload.
	newsTrendsNote = "headline-frequency trend — descriptive attention, not a forecast"
	// newsTrendsInsightKey holds the unix seconds of the last fleet insight.
	newsTrendsInsightKey = "news_trends_insight_ts"
	// newsTrendsInsightEvery is the fleet-insight cadence.
	newsTrendsInsightEvery = 6 * time.Hour
	// newsTrendsFreshWindow scopes a pass to symbols with recent headlines.
	newsTrendsFreshWindow = 48 * time.Hour
	// newsTrendsTokenWindow is the trending-token extraction lookback.
	newsTrendsTokenWindow = 24 * time.Hour
)

// NewsTrends is the news-trends worker.
type NewsTrends struct {
	St *store.Store
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *NewsTrends) Name() string            { return "news-trends" }
func (w *NewsTrends) Interval() time.Duration { return 30 * time.Minute }

func (w *NewsTrends) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *NewsTrends) Run(ctx context.Context) (string, error) {
	now := w.now()
	ids, err := w.St.SymbolsWithNewsSince(ctx, now.Add(-newsTrendsFreshWindow).Unix())
	if err != nil {
		return "", err
	}
	stored, gated := 0, 0
	for _, id := range ids {
		day, n, z, ok, _, err := w.St.NewsVolumeZ(ctx, id)
		if err != nil {
			return "", err
		}
		var zp *float64
		if ok {
			zp = &z
		} else {
			gated++
		}
		if err := w.St.UpsertNewsTrend(ctx, id, day, n, zp); err != nil {
			return "", err
		}
		stored++
	}

	// Fleet trending-token insight, once per 6h.
	insight := "insight not due"
	if raw, _ := w.St.GetMeta(ctx, newsTrendsInsightKey); dueSince(raw, now, newsTrendsInsightEvery) {
		toks, err := w.St.FleetTrendingTokens(ctx, now.Add(-newsTrendsTokenWindow).Unix(), 10)
		if err != nil {
			return "", err
		}
		if len(toks) == 0 {
			insight = "insight skipped: no tokens in the last 24h of headlines"
		} else {
			if err := w.St.InsertInsight(ctx, newsTrendsInsight(now, toks)); err != nil {
				return "", err
			}
			if err := w.St.SetMeta(ctx, newsTrendsInsightKey, strconv.FormatInt(now.Unix(), 10)); err != nil {
				return "", err
			}
			insight = fmt.Sprintf("insight written (%d tokens)", len(toks))
		}
	}
	return fmt.Sprintf("news-volume rows for %d symbol(s) (%d below the z baseline gate — stored without z); %s", stored, gated, insight), nil
}

// dueSince reports whether `every` has elapsed since the unix-seconds cursor
// in raw (empty/garbage reads as 0 — immediately due).
func dueSince(raw string, now time.Time, every time.Duration) bool {
	last, _ := strconv.ParseInt(raw, 10, 64)
	return now.Unix()-last >= int64(every/time.Second)
}

// newsTrendsInsight composes the one market-scope insight (kind news_trends)
// listing the fleet's trending headline tokens.
func newsTrendsInsight(now time.Time, toks []store.TrendingToken) md.Insight {
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		parts = append(parts, fmt.Sprintf("%s(%d across %d symbols)", t.Token, t.Count, t.Symbols))
	}
	data, _ := json.Marshal(map[string]any{
		"kind": "news_trends", "tokens": toks, "note": newsTrendsNote,
	})
	return md.Insight{
		Scope:    "market",
		Ts:       now.Unix(),
		Headline: fmt.Sprintf("News trends: %s leads headline chatter", toks[0].Token),
		Body: "trending in headlines: " + strings.Join(parts, ", ") +
			". Token counts are deterministic keyword frequencies over the last 24h of stored headline titles (stopwords, short tokens and tracked tickers dropped). " + newsTrendsNote + ".",
		Data: string(data),
	}
}
