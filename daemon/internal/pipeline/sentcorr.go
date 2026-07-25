// Sentiment-correlation wave — the three workers that turn a headline feed into
// a measured answer about whether news text predicts returns.
//
//	news-backfiller       (30m) — reaches BACKWARD through Alpaca's news archive
//	                              so the study has years instead of weeks
//	sentiment-lex-scorer  (15m) — deterministic lexicon score for every headline,
//	                              then the as-of-aligned daily features
//	sentiment-corr-runner (12h) — the study itself, gates and all
//
// The split is deliberate: fetching is rate-limited and slow, scoring is pure
// CPU over whatever has arrived, and the study is a single read-mostly pass. A
// single worker doing all three would stall the fast parts behind the slow one
// and make a failure in any stage look like a failure of the measurement.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/news"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/newssent"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sentcorr"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── news-backfiller ──────────────────────────────────────────────────────

// metaNewsBackfillCursor stores how far back the backfill has reached, as a unix
// timestamp. The sweep walks BACKWARD from today, so the cursor decreases.
const metaNewsBackfillCursor = "news_backfill_floor_ts"

// metaNewsBackfillBatch stores which SYMBOL BATCH of the current window the
// sweep reached.
//
// WHY IT EXISTS (bug found on the first live run): the universe is ~1,000 stocks
// = ~22 batches of 50, and one pass is capped at maxPagesPerPass requests. A
// dense month costs more than that, so a pass would run out of budget, correctly
// decline to advance the window cursor, and the NEXT pass would restart the same
// window from batch 0 — re-fetching the same headlines forever and never
// reaching 2019. Remembering the batch index makes each pass resume where the
// last one stopped, so the window completes across several passes and only then
// moves backward.
const metaNewsBackfillBatch = "news_backfill_batch_idx"

// newsBackfillFloor is where the backfill stops. Alpaca's news archive is
// Benzinga-sourced and thins out badly before ~2016; the daily bars in this
// database start 2019, and an observation needs both, so reaching further would
// fetch headlines no study can use.
var newsBackfillFloor = time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

// backfillWindow is how much history one pass claims. A month per pass at a 30m
// cadence walks ~7 years of archive in about a week of uptime, which is well
// inside the free rate limit and needs no burst.
const backfillWindow = 30 * 24 * time.Hour

// maxPagesPerPass bounds one pass's request count so a dense window cannot make
// a single run monopolise the rate limit for hours.
const maxPagesPerPass = 60

// NewsBackfiller walks Alpaca's news archive backwards, storing every headline
// for the tracked stock universe together with the full multi-ticker mapping.
type NewsBackfiller struct {
	St     *store.Store
	Client *news.Client
	Now    func() time.Time
}

func (w *NewsBackfiller) Name() string            { return "news-backfiller" }
func (w *NewsBackfiller) Interval() time.Duration { return 30 * time.Minute }

func (w *NewsBackfiller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *NewsBackfiller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "no news client (no Alpaca key) — skipped", nil
	}
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	// symbol -> id for attributing an article to every tracked ticker it tags.
	idOf := map[string]int64{}
	var tickers []string
	for _, s := range syms {
		if s.Market != md.Stocks {
			continue // Alpaca's news endpoint covers equities
		}
		idOf[strings.ToUpper(s.Symbol)] = s.ID
		tickers = append(tickers, s.Symbol)
	}
	if len(tickers) == 0 {
		return "no stock symbols to backfill", nil
	}

	// Resume from the recorded floor; first run starts at now.
	end := w.now().UTC()
	if raw, _ := w.St.GetMeta(ctx, metaNewsBackfillCursor); raw != "" {
		if ts, err := strconv.ParseInt(raw, 10, 64); err == nil && ts > 0 {
			end = time.Unix(ts, 0).UTC()
		}
	}
	if !end.After(newsBackfillFloor) {
		return fmt.Sprintf("archive complete back to %s — nothing left to fetch",
			newsBackfillFloor.Format("2006-01-02")), nil
	}
	start := end.Add(-backfillWindow)
	if start.Before(newsBackfillFloor) {
		start = newsBackfillFloor
	}

	// Resume at the batch this window reached, so a budget-limited pass continues
	// instead of restarting.
	batch := 0
	if raw, _ := w.St.GetMeta(ctx, metaNewsBackfillBatch); raw != "" {
		if b, err := strconv.Atoi(raw); err == nil && b > 0 {
			batch = b
		}
	}
	totalBatches := (len(tickers) + news.MaxRangeSymbols - 1) / news.MaxRangeSymbols
	if batch >= totalBatches {
		batch = 0 // universe shrank since the cursor was written
	}

	inserted, seen, pages := 0, 0, 0
	// Resuming mid-batch would need the provider's opaque page token to survive a
	// restart, which it does not. So a batch is only marked done when its
	// pagination FINISHED; a batch cut off by the budget is retried whole next
	// pass, and INSERT OR IGNORE makes the repeat free. Advancing past a
	// half-paginated batch would silently skip the headlines it never reached.
	for batch < totalBatches && pages < maxPagesPerPass {
		i := batch * news.MaxRangeSymbols
		j := min(i+news.MaxRangeSymbols, len(tickers))
		token := ""
		complete := false
		for pages < maxPagesPerPass {
			page, err := w.Client.FetchRange(ctx, tickers[i:j], start, end, token)
			if err != nil {
				// A failed batch must not roll the cursor back over the window:
				// report and stop this pass, and the next one retries the same
				// window. Advancing on failure would leave a permanent hole.
				return "", fmt.Errorf("range fetch [%s..%s): %w",
					start.Format("2006-01-02"), end.Format("2006-01-02"), err)
			}
			pages++
			for k, it := range page.Items {
				seen++
				// Attribute to every TRACKED ticker the article tags.
				var ids []int64
				for _, sym := range page.Symbols[k] {
					if id, ok := idOf[strings.ToUpper(sym)]; ok {
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					continue
				}
				it.SymbolID = ids[0]
				if err := w.St.InsertNews(ctx, it); err != nil {
					return "", fmt.Errorf("insert %s: %w", it.ID, err)
				}
				if err := w.St.InsertNewsSymbols(ctx, it.ID, ids); err != nil {
					return "", fmt.Errorf("map %s: %w", it.ID, err)
				}
				inserted++
			}
			if page.NextToken == "" {
				complete = true
				break
			}
			token = page.NextToken
		}
		if !complete {
			break // budget exhausted mid-batch: retry this whole batch next pass
		}
		batch++
	}

	// The window is finished only when every symbol batch in it was walked. Then
	// the window moves backward and the batch index resets; otherwise the batch
	// index is saved so the next pass picks up mid-window.
	done := batch >= totalBatches
	if done {
		if err := w.St.SetMeta(ctx, metaNewsBackfillCursor, strconv.FormatInt(start.Unix(), 10)); err != nil {
			return "", err
		}
		if err := w.St.SetMeta(ctx, metaNewsBackfillBatch, "0"); err != nil {
			return "", err
		}
		return fmt.Sprintf("window %s..%s COMPLETE: %d headlines seen, %d stored (%d pages); archive now reaches back to %s",
			start.Format("2006-01-02"), end.Format("2006-01-02"), seen, inserted, pages,
			start.Format("2006-01-02")), nil
	}
	if err := w.St.SetMeta(ctx, metaNewsBackfillBatch, strconv.Itoa(batch)); err != nil {
		return "", err
	}
	return fmt.Sprintf("window %s..%s: batch %d/%d, %d headlines seen, %d stored (%d pages, budget reached — resumes next pass)",
		start.Format("2006-01-02"), end.Format("2006-01-02"), batch, totalBatches, seen, inserted, pages), nil
}

// ── sentiment-lex-scorer ─────────────────────────────────────────────────

// lexBatch is how many headlines one pass scores. Scoring is pure CPU and
// microseconds per headline; the bound exists to keep the write transaction
// short, not because the arithmetic is expensive.
const lexBatch = 20000

// featureRebuildDays is how far back each pass rebuilds the daily features.
// Rebuilding a trailing window (rather than only today) means a late-arriving
// backfilled headline is folded into its correct session instead of being lost,
// and the upsert makes the repeat work idempotent.
const featureRebuildDays = 400

// ── the feature backfill ─────────────────────────────────────────────────
//
// The trailing rebuild above is bounded, and for a long time that bound WAS the
// study's history: the news backfiller walked the archive back past 2015, but no
// feature row was ever created for any headline older than featureRebuildDays,
// so the correlation study stayed pinned at ~14 months no matter how much
// archive arrived. The backfill's entire purpose — re-judging the 5-session lead
// on a longer sample — was blocked by this constant, silently, because nothing
// reported the gap between "headlines stored" and "headlines aligned".
//
// So a second, backwards sweep walks the rest of the archive on its own cursor,
// mirroring the news backfiller's discipline: one bounded window per pass, the
// cursor advancing only after the window is written.

// MetaFeatureBackfillCursor stores how far back feature-building has reached,
// as a unix timestamp. Like the news cursor, it DECREASES. Exported because the
// API reports catch-up progress from the cursor itself: the aligned-vs-archive
// DATES cannot answer it, since a session key legitimately rolls past the
// headline's own day and would leave a permanent phantom gap.
const MetaFeatureBackfillCursor = "sentiment_feature_floor_ts"

// featureBackfillWindow is how much history one pass aligns. Aligning is a
// grouped in-memory pass over already-scored rows — cheap next to fetching them
// — so the window is much wider than the news backfiller's month.
const featureBackfillWindow = 180 * 24 * time.Hour

// featureBackfillOverlap is how far past its window's newer edge a pass reads.
//
// It exists because a session key is not a calendar day: store.ActionableSession
// rolls a headline forward to the first session on which it was already public,
// so an article published just below a window boundary belongs to a day the
// NEWER window already wrote. Without the overlap that day would be rewritten
// from this window's rows alone and silently lose the newer half of its
// headlines. Seven days clears the longest weekend-plus-holiday roll.
const featureBackfillOverlap = 7 * 24 * time.Hour

// SentimentLexScorer scores unscored headlines with the deterministic lexicon
// and rebuilds the as-of-aligned daily sentiment features.
type SentimentLexScorer struct {
	St  *store.Store
	Now func() time.Time
}

func (w *SentimentLexScorer) Name() string            { return "sentiment-lex-scorer" }
func (w *SentimentLexScorer) Interval() time.Duration { return 15 * time.Minute }

func (w *SentimentLexScorer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *SentimentLexScorer) Run(ctx context.Context) (string, error) {
	pending, err := w.St.UnscoredNews(ctx, newssent.Version, lexBatch)
	if err != nil {
		return "", err
	}
	scores := make([]store.LexScore, 0, len(pending))
	polar := 0
	for _, p := range pending {
		s := newssent.Rate(p.Headline)
		if s.Polar {
			polar++
		}
		scores = append(scores, store.LexScore{
			ID: p.ID, Score: s.Value, Polar: s.Polar, Hedged: s.Hedged,
		})
	}
	if err := w.St.SetLexScores(ctx, newssent.Version, scores); err != nil {
		return "", fmt.Errorf("store scores: %w", err)
	}

	mapped, err := w.St.ReconcileNewsSymbols(ctx)
	if err != nil {
		return "", fmt.Errorf("reconcile mapping: %w", err)
	}

	feats, err := w.rebuildFeatures(ctx)
	if err != nil {
		return "", err
	}

	backfilled, err := w.backfillFeatures(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scored %d headlines (%d expressed polarity), mapped %d new article-symbol pairs, rebuilt %d daily features; %s",
		len(scores), polar, mapped, feats, backfilled), nil
}

// backfillFeatures aligns ONE window of older archive per pass, walking
// backwards from where the trailing rebuild stops until it reaches the oldest
// scored headline.
//
// The cursor advances only after the window's features are written, so a crash
// mid-window costs one repeated pass rather than a permanent hole — the same
// discipline (and for the same reason) as the news backfiller's cursor.
func (w *SentimentLexScorer) backfillFeatures(ctx context.Context) (string, error) {
	end := w.now().AddDate(0, 0, -featureRebuildDays)
	if raw, _ := w.St.GetMeta(ctx, MetaFeatureBackfillCursor); raw != "" {
		if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
			end = time.Unix(ts, 0).UTC()
		}
	}

	oldest, err := w.St.OldestScoredNewsTs(ctx, newssent.Version)
	if err != nil {
		return "", fmt.Errorf("oldest scored: %w", err)
	}
	if oldest == 0 || end.Unix() <= oldest {
		// Nothing scored yet, or the walk has reached the start of the archive.
		// Either way there is no older window to align.
		return "feature backfill complete: every scored headline is aligned", nil
	}

	start := end.Add(-featureBackfillWindow)
	if start.Unix() < oldest {
		start = time.Unix(oldest, 0).UTC()
	}
	// Read past the newer edge: see featureBackfillOverlap. Days that straddle
	// the boundary are recomputed here from BOTH sides rather than half of one.
	n, err := w.buildFeatureWindow(ctx, start.Unix(), end.Add(featureBackfillOverlap).Unix())
	if err != nil {
		return "", err
	}
	if err := w.St.SetMeta(ctx, MetaFeatureBackfillCursor, strconv.FormatInt(start.Unix(), 10)); err != nil {
		return "", err
	}
	return fmt.Sprintf("feature backfill %s..%s: %d daily features aligned, archive now aligned back to %s",
		start.Format("2006-01-02"), end.Format("2006-01-02"), n, start.Format("2006-01-02")), nil
}

// rebuildFeatures groups scored headlines into per-(symbol, ACTIONABLE SESSION)
// aggregates.
//
// Two things here are load-bearing:
//
//   - The session key comes from store.ActionableSession, not from the article's
//     own calendar day. Most financial headlines publish outside market hours;
//     keying them to their own day would let the study act on information hours
//     before it existed.
//   - mean_score averages ONLY the headlines that expressed polarity. Averaging
//     in the factual ones as zeros would shrink every real opinion toward
//     nothing while inflating the apparent sample.
func (w *SentimentLexScorer) rebuildFeatures(ctx context.Context) (int, error) {
	since := w.now().AddDate(0, 0, -featureRebuildDays).Unix()
	rows, err := w.St.ScoredNewsSince(ctx, newssent.Version, since)
	if err != nil {
		return 0, err
	}
	return w.aggregateAndUpsert(ctx, rows)
}

// buildFeatureWindow aligns one half-open [fromTs, toTs) slice of the archive.
// It shares aggregateAndUpsert with the trailing rebuild deliberately: two code
// paths writing the same table by slightly different rules is how a study ends
// up with a discontinuity at the seam between them.
func (w *SentimentLexScorer) buildFeatureWindow(ctx context.Context, fromTs, toTs int64) (int, error) {
	rows, err := w.St.ScoredNewsBetween(ctx, newssent.Version, fromTs, toTs)
	if err != nil {
		return 0, err
	}
	return w.aggregateAndUpsert(ctx, rows)
}

func (w *SentimentLexScorer) aggregateAndUpsert(ctx context.Context, rows []store.LexNewsRow) (int, error) {
	type agg struct {
		nPolar, nAll, pos, neg, hedged int
		sum                            float64
	}
	byKey := map[int64]map[string]*agg{}
	for _, r := range rows {
		day := store.ActionableSession(r.Ts)
		if byKey[r.SymbolID] == nil {
			byKey[r.SymbolID] = map[string]*agg{}
		}
		a := byKey[r.SymbolID][day]
		if a == nil {
			a = &agg{}
			byKey[r.SymbolID][day] = a
		}
		a.nAll++
		if !r.Polar {
			continue
		}
		a.nPolar++
		a.sum += r.Score
		if r.Hedged {
			a.hedged++
		}
		if r.Score > 0 {
			a.pos++
		} else if r.Score < 0 {
			a.neg++
		}
	}

	out := make([]store.SentimentFeature, 0, 4096)
	for symID, days := range byKey {
		for day, a := range days {
			mean := 0.0
			if a.nPolar > 0 {
				mean = a.sum / float64(a.nPolar)
			}
			out = append(out, store.SentimentFeature{
				SymbolID: symID, Day: day,
				NPolar: a.nPolar, NAll: a.nAll, MeanScore: mean,
				Pos: a.pos, Neg: a.neg, Hedged: a.hedged, Ver: newssent.Version,
			})
		}
	}
	if err := w.St.UpsertSentimentFeatures(ctx, out); err != nil {
		return 0, fmt.Errorf("upsert features: %w", err)
	}
	return len(out), nil
}

// ── sentiment-corr-runner ────────────────────────────────────────────────

// SentCorrFeature names the sentiment feature under study. Only the daily mean
// polarity is measured today; the name is stored so a second feature can be
// added later without the results table becoming ambiguous.
const SentCorrFeature = "mean_score"

// sentCorrHorizons are the forward windows studied, in trading sessions: a day,
// a week, and a month. Short horizons are where news effects are claimed to
// live, and the monthly one is the control — a real information effect should
// decay, and one that grows with horizon is usually a factor exposure.
var sentCorrHorizons = []int{1, 5, 21}

// SentCorrHorizons returns the studied forward windows, for the API to render
// without duplicating the list.
func SentCorrHorizons() []int { return append([]int(nil), sentCorrHorizons...) }

// SentCorrRunner runs the sentiment-vs-forward-return study and stores each
// horizon's verdict.
type SentCorrRunner struct {
	St  *store.Store
	Now func() time.Time
}

func (w *SentCorrRunner) Name() string            { return "sentiment-corr-runner" }
func (w *SentCorrRunner) Interval() time.Duration { return 12 * time.Hour }

func (w *SentCorrRunner) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *SentCorrRunner) Run(ctx context.Context) (string, error) {
	ts := w.now().Unix()
	var parts []string
	for _, h := range sentCorrHorizons {
		cfg := sentcorr.DefaultConfig(h)
		// All three horizons are tested in the same pass, so each one's interval
		// is Bonferroni-widened over the family. Without this, "the 5-day horizon
		// came back significant" is a one-in-seven coin flip reported as a
		// finding.
		cfg.FamilySize = len(sentCorrHorizons)
		rows, err := w.St.SentCorrObservations(ctx, h, cfg.MinHeadlines)
		if err != nil {
			return "", fmt.Errorf("observations h=%d: %w", h, err)
		}
		obs := make([]sentcorr.Obs, 0, len(rows))
		for _, r := range rows {
			obs = append(obs, sentcorr.Obs{
				SymbolID: r.SymbolID, Day: r.Day, SessionIdx: r.SessionIdx,
				Sent: r.MeanScore, NHeadlines: r.NPolar,
				Ret0: r.Ret0, RetTrail: r.RetTrail, Fwd: r.Fwd,
			})
		}
		res := sentcorr.Study(obs, cfg)
		payload, err := json.Marshal(res)
		if err != nil {
			return "", err
		}
		if err := w.St.UpsertSentCorrResult(ctx, SentCorrFeature, h, ts, res.Obs, res.Gated, string(payload)); err != nil {
			return "", fmt.Errorf("store result h=%d: %w", h, err)
		}
		if res.Gated {
			parts = append(parts, fmt.Sprintf("%dd: no verdict (%d obs)", h, res.Obs))
		} else {
			parts = append(parts, fmt.Sprintf("%dd: partial IC %+.4f over %d obs", h, deref(res.PartialIC), res.Obs))
		}
		slog.Info("sentcorr study", "horizon", h, "obs", res.Obs, "gated", res.Gated, "verdict", res.Verdict)
	}
	return strings.Join(parts, "; "), nil
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
