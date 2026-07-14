// DATA-EXPANSION wave — the two ATTENTION workers and their shared scope:
//
//   - stocktwits-fetcher (15m): page-snapshot tallies of StockTwits' free
//     public symbol streams (bullish/bearish/untagged counts over the ~30
//     newest messages), paced ≤1 req/2s;
//   - wiki-attention (24h): daily Wikipedia page views (official Wikimedia
//     REST API, agent=user) with a heuristic company-name → article
//     resolution that CACHES failures so unresolved names are never
//     re-hammered.
//
// SCOPE (news-fetcher-style, see data.go's newsScope): the streamed hot set
// + every symbol on ANY user's watchlist — stocks only. The broad daily-only
// universe is out of scope: hundreds of unauthenticated requests per pass for
// symbols nobody watches would be discourteous and useless.
//
// HONESTY (carried verbatim by the API/UI): StockTwits is retail message
// sentiment from a self-selected crowd, counted per PAGE SNAPSHOT — not a
// census. Wikipedia views are a public ATTENTION proxy — not a trading
// signal, and the article resolution heuristic can pick the wrong page.
// Both are descriptive context only, never scored factors.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/stocktwits"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/wikimedia"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// attentionScope returns the stocks worth external-attention requests: the
// streamed hot set + anything any user watches, sorted by symbol for stable
// pacing. Watchlist absence degrades gracefully (empty contribution).
func attentionScope(ctx context.Context, st *store.Store) ([]md.Symbol, error) {
	active, err := st.ListSymbols(ctx, true)
	if err != nil {
		return nil, err
	}
	watched, err := st.WatchedSymbolIDs(ctx)
	if err != nil {
		return nil, err
	}
	watchedSet := make(map[int64]bool, len(watched))
	for _, id := range watched {
		watchedSet[id] = true
	}
	var out []md.Symbol
	for _, s := range active {
		if s.Market != md.Stocks {
			continue
		}
		if s.Stream || watchedSet[s.ID] {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}

// ── stocktwits-fetcher ────────────────────────────────────────────────────

// stocktwitsUnknownPrefix caches symbols StockTwits 404s (meta key per
// symbol) so they aren't re-requested every pass.
const stocktwitsUnknownPrefix = "stocktwits_unknown:"

// StocktwitsFetcher is the stocktwits-fetcher worker.
type StocktwitsFetcher struct {
	St     *store.Store
	Client *stocktwits.Client
	// MaxPerRun bounds symbols fetched per pass (0 ⇒ 40; at the client's 2s
	// pacing a full run stays under ~80s).
	MaxPerRun int
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *StocktwitsFetcher) Name() string            { return "stocktwits-fetcher" }
func (w *StocktwitsFetcher) Interval() time.Duration { return 15 * time.Minute }

func (w *StocktwitsFetcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *StocktwitsFetcher) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		// Shouldn't happen — StockTwits needs no key — but degrade honestly.
		return "skipped: no stocktwits client", nil
	}
	scope, err := attentionScope(ctx, w.St)
	if err != nil {
		return "", err
	}
	if len(scope) == 0 {
		return "skipped: no watchlist/hot-set stocks in scope", nil
	}
	max := w.MaxPerRun
	if max <= 0 {
		max = 40
	}
	if len(scope) > max {
		scope = scope[:max]
	}
	ts := w.now().Unix()
	stored, unknown, failed := 0, 0, 0
	var firstErr error
	for _, s := range scope {
		if v, _ := w.St.GetMeta(ctx, stocktwitsUnknownPrefix+s.Symbol); v != "" {
			unknown++
			continue
		}
		snap, ferr := w.Client.FetchSymbol(ctx, s.Symbol)
		if errors.Is(ferr, stocktwits.ErrUnknown) {
			// Cache the miss — an unlisted symbol stays unlisted; delete the
			// meta key to force a retry.
			_ = w.St.SetMeta(ctx, stocktwitsUnknownPrefix+s.Symbol, w.now().Format(time.RFC3339))
			unknown++
			continue
		}
		if ferr != nil {
			failed++
			if firstErr == nil {
				firstErr = ferr
			}
			id := s.ID
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				SymbolID: &id, Ts: ts, Kind: "stocktwits_error",
				Detail: fmt.Sprintf("%s: %v", s.Symbol, ferr),
			})
			continue
		}
		if err := w.St.InsertStocktwits(ctx, store.StocktwitsRow{
			SymbolID: s.ID, Ts: ts,
			Bullish: snap.Bullish, Bearish: snap.Bearish,
			Untagged: snap.Untagged, Total: snap.Total,
		}); err != nil {
			return "", err
		}
		stored++
	}
	if stored == 0 && failed > 0 {
		// Nothing landed and the upstream errored throughout — surface it as a
		// worker error (recorded, retried next tick) instead of a quiet "0".
		return "", fmt.Errorf("all %d fetch(es) failed (first: %w)", failed, firstErr)
	}
	detail := fmt.Sprintf("stored %d page snapshot(s) across %d in-scope symbol(s)", stored, len(scope))
	if unknown > 0 {
		detail += fmt.Sprintf("; %d not on StockTwits (cached)", unknown)
	}
	if failed > 0 {
		detail += fmt.Sprintf("; %d fetch failure(s) (dq recorded)", failed)
	}
	return detail, nil
}

// ── wiki-attention ────────────────────────────────────────────────────────

// WikiAttention is the wiki-attention worker.
type WikiAttention struct {
	St     *store.Store
	Client *wikimedia.Client
	// MaxPerRun bounds symbols processed per pass (0 ⇒ 30); each symbol costs
	// 1 request when resolved, ≤2 during first-time resolution.
	MaxPerRun int
	// FetchDays is the trailing view window per fetch (0 ⇒ 30; one request
	// covers the whole range, and REPLACE upserts heal gaps for free).
	FetchDays int
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *WikiAttention) Name() string            { return "wiki-attention" }
func (w *WikiAttention) Interval() time.Duration { return 24 * time.Hour }

func (w *WikiAttention) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *WikiAttention) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		// Shouldn't happen — Wikimedia needs no key — but degrade honestly.
		return "skipped: no wikimedia client", nil
	}
	scope, err := attentionScope(ctx, w.St)
	if err != nil {
		return "", err
	}
	if len(scope) == 0 {
		return "skipped: no watchlist/hot-set stocks in scope", nil
	}
	max := w.MaxPerRun
	if max <= 0 {
		max = 30
	}
	if len(scope) > max {
		scope = scope[:max]
	}
	fetchDays := w.FetchDays
	if fetchDays <= 0 {
		fetchDays = 30
	}
	now := w.now()
	// Views for today aren't final; fetch through yesterday (UTC).
	to := now.UTC().AddDate(0, 0, -1)
	from := to.AddDate(0, 0, -(fetchDays - 1))

	// Company names for first-time resolution: ONE batched directory query.
	tickers := make([]string, 0, len(scope))
	for _, s := range scope {
		tickers = append(tickers, strings.ToUpper(s.Symbol))
	}
	names, err := w.St.CompanyNamesByTickers(ctx, tickers)
	if err != nil {
		return "", err
	}

	fetched, daysStored, resolvedNow, unresolved, unnamed, failed := 0, 0, 0, 0, 0, 0
	for _, s := range scope {
		wa, found, gerr := w.St.GetWikiArticle(ctx, s.ID)
		if gerr != nil {
			return "", gerr
		}
		article := ""
		switch {
		case found && wa.OK:
			article = wa.Article
		case found && !wa.OK:
			// Cached resolution failure — never re-hammered.
			unresolved++
			continue
		default:
			name := names[strings.ToUpper(s.Symbol)]
			if name == "" {
				// No directory name yet (companies-sync may not have run) —
				// skip WITHOUT caching so a later run can resolve it.
				unnamed++
				continue
			}
			a, views, rerr := w.resolve(ctx, name, from, to)
			if rerr != nil {
				failed++
				id := s.ID
				_ = w.St.InsertDQ(ctx, md.DQEvent{
					SymbolID: &id, Ts: now.Unix(), Kind: "wiki_error",
					Detail: fmt.Sprintf("%s: %v", s.Symbol, rerr),
				})
				continue
			}
			if a == "" {
				// All candidates 404'd — cache the failure honestly.
				_ = w.St.SetWikiArticle(ctx, store.WikiArticle{
					SymbolID: s.ID, Article: "", OK: false, ResolvedAt: now.Unix(),
				})
				unresolved++
				continue
			}
			_ = w.St.SetWikiArticle(ctx, store.WikiArticle{
				SymbolID: s.ID, Article: a, OK: true, ResolvedAt: now.Unix(),
			})
			resolvedNow++
			if err := w.storeViews(ctx, s.ID, views); err != nil {
				return "", err
			}
			fetched++
			daysStored += len(views)
			continue
		}

		views, ferr := w.Client.FetchDaily(ctx, article, from, to)
		if errors.Is(ferr, wikimedia.ErrNotFound) {
			// Article vanished (rename) — drop the cache so a later run
			// re-resolves instead of 404ing forever.
			_ = w.St.SetWikiArticle(ctx, store.WikiArticle{
				SymbolID: s.ID, Article: "", OK: false, ResolvedAt: now.Unix(),
			})
			unresolved++
			continue
		}
		if ferr != nil {
			failed++
			id := s.ID
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				SymbolID: &id, Ts: now.Unix(), Kind: "wiki_error",
				Detail: fmt.Sprintf("%s (%s): %v", s.Symbol, article, ferr),
			})
			continue
		}
		if err := w.storeViews(ctx, s.ID, views); err != nil {
			return "", err
		}
		fetched++
		daysStored += len(views)
	}
	detail := fmt.Sprintf("fetched views for %d symbol(s) (%d day rows) of %d in scope",
		fetched, daysStored, len(scope))
	if resolvedNow > 0 {
		detail += fmt.Sprintf("; %d newly resolved", resolvedNow)
	}
	if unresolved > 0 {
		detail += fmt.Sprintf("; %d unresolved (cached, not re-hammered)", unresolved)
	}
	if unnamed > 0 {
		detail += fmt.Sprintf("; %d without a directory name yet", unnamed)
	}
	if failed > 0 {
		detail += fmt.Sprintf("; %d fetch failure(s) (dq recorded)", failed)
	}
	return detail, nil
}

// resolve tries the heuristic article candidates in order, returning the
// first that has pageview data (with its views, saving a second fetch).
// ("", nil, nil) means every candidate 404'd; a non-404 error aborts so a
// Wikimedia outage isn't mistaken for "article doesn't exist" and cached.
func (w *WikiAttention) resolve(ctx context.Context, name string, from, to time.Time) (string, []wikimedia.DayViews, error) {
	for _, cand := range wikimedia.ArticleCandidates(name) {
		views, err := w.Client.FetchDaily(ctx, cand, from, to)
		if errors.Is(err, wikimedia.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return cand, views, nil
	}
	return "", nil, nil
}

func (w *WikiAttention) storeViews(ctx context.Context, symbolID int64, views []wikimedia.DayViews) error {
	rows := make([]store.WikiViewRow, 0, len(views))
	for _, v := range views {
		rows = append(rows, store.WikiViewRow{SymbolID: symbolID, Day: v.Day, Views: v.Views})
	}
	return w.St.UpsertWikiViews(ctx, rows)
}
