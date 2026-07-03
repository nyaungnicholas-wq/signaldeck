// Package discovery implements universe auto-discovery: a worker that sweeps
// Alpaca's stock screener endpoints (most-actives + movers) for candidate
// symbols, records them in the candidates table, and — while under the
// symbol budget — auto-adds the best persistently-interesting ones through
// the same server-side path /api/subscribe uses.
//
// Honesty rules: a candidate is only auto-added after it has shown up in at
// least MinSweeps separate sweeps (persistent interest, not one flashy
// spike), at most DailyAutoAddLimit auto-adds happen per day, and every
// auto-add writes an insight explaining exactly why.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Budget rules.
const (
	// DefaultSymbolCap is the default max ACTIVE symbols (env-tunable via
	// SIGNALDECK_SYMBOL_CAP).
	DefaultSymbolCap = 30
	// MinSweeps is how many separate sweeps must have seen a candidate
	// before it qualifies for auto-add.
	MinSweeps = 2
	// DailyAutoAddLimit caps auto-adds per UTC day.
	DailyAutoAddLimit = 2
)

// BudgetMu serializes every check-then-subscribe sequence against the symbol
// cap (the manual /api/candidates/add handler AND the worker's autoAdd).
// Without it two concurrent adds can both pass the cap check before either
// activates a symbol, overshooting the budget (TOCTOU). A process-wide mutex
// is sufficient: all writes already funnel through this one daemon.
var BudgetMu sync.Mutex

// SymbolCap returns the active-symbol budget: SIGNALDECK_SYMBOL_CAP when set
// to a positive integer, else DefaultSymbolCap.
func SymbolCap() int {
	if v := os.Getenv("SIGNALDECK_SYMBOL_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultSymbolCap
}

// ── Alpaca screener client ──────────────────────────────────────────────

// defaultBase is the market-data host WITHOUT a version prefix: the screener
// endpoints live under /v1beta1 while latest-trade lives under /v2 (the bars
// client's BaseData embeds /v2, so it can't be reused verbatim here).
const defaultBase = "https://data.alpaca.markets"

// Client is a minimal Alpaca screener client. Same auth headers as the bars
// client; Base/HTTP are overridable so tests can point at httptest servers.
type Client struct {
	Key    string       // APCA-API-KEY-ID
	Secret string       // APCA-API-SECRET-KEY
	Base   string       // host, no version prefix (default data.alpaca.markets)
	HTTP   *http.Client // defaults to a 30s-timeout client
}

// NewClient returns a Client wired to Alpaca's production data host.
func NewClient(key, secret string) *Client {
	return &Client{Key: key, Secret: secret, Base: defaultBase,
		HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimSuffix(c.Base, "/")
	}
	return defaultBase
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// getJSON GETs path (already query-encoded) with Alpaca auth headers.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("APCA-API-KEY-ID", c.Key)
	req.Header.Set("APCA-API-SECRET-KEY", c.Secret)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("alpaca screener: GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ActiveRow is one entry of the most-actives screener.
type ActiveRow struct {
	Symbol     string  `json:"symbol"`
	Volume     float64 `json:"volume"`
	TradeCount float64 `json:"trade_count"`
}

// MostActives fetches GET /v1beta1/screener/stocks/most-actives?by=volume&top=N.
func (c *Client) MostActives(ctx context.Context, top int) ([]ActiveRow, error) {
	var out struct {
		MostActives []ActiveRow `json:"most_actives"`
	}
	path := fmt.Sprintf("/v1beta1/screener/stocks/most-actives?by=volume&top=%d", top)
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.MostActives, nil
}

// MoverRow is one gainer/loser of the movers screener.
type MoverRow struct {
	Symbol        string  `json:"symbol"`
	PercentChange float64 `json:"percent_change"`
	Change        float64 `json:"change"`
	Price         float64 `json:"price"`
}

// Movers fetches GET /v1beta1/screener/stocks/movers?top=N (top N gainers AND
// top N losers).
func (c *Client) Movers(ctx context.Context, top int) (gainers, losers []MoverRow, err error) {
	var out struct {
		Gainers []MoverRow `json:"gainers"`
		Losers  []MoverRow `json:"losers"`
	}
	path := fmt.Sprintf("/v1beta1/screener/stocks/movers?top=%d", top)
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, nil, err
	}
	return out.Gainers, out.Losers, nil
}

// LatestPrices fetches GET /v2/stocks/trades/latest?symbols=… and returns
// symbol → last trade price (used to turn most-actives share volume into
// dollar volume). Missing symbols are simply absent from the map.
func (c *Client) LatestPrices(ctx context.Context, symbols []string) (map[string]float64, error) {
	if len(symbols) == 0 {
		return map[string]float64{}, nil
	}
	var out struct {
		Trades map[string]struct {
			P float64 `json:"p"`
		} `json:"trades"`
	}
	path := "/v2/stocks/trades/latest?symbols=" + url.QueryEscape(strings.Join(symbols, ","))
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	prices := make(map[string]float64, len(out.Trades))
	for sym, tr := range out.Trades {
		prices[sym] = tr.P
	}
	return prices, nil
}

// ── budget math (pure, unit-tested) ─────────────────────────────────────

// AutoAddBudget returns how many auto-adds may happen right now given the
// active-symbol cap and the per-day limit. Never negative.
func AutoAddBudget(activeCount, symbolCap, addsToday, dailyLimit int) int {
	byCap := symbolCap - activeCount
	byDay := dailyLimit - addsToday
	n := byCap
	if byDay < n {
		n = byDay
	}
	if n < 0 {
		return 0
	}
	return n
}

// PickAutoAdds selects which candidates to auto-add: status "new", seen in at
// least minSweeps sweeps (persistent interest), ranked by dollar volume
// (highest first), capped at budget. Pure function — all the honesty rules
// live here so they are directly testable.
func PickAutoAdds(cands []store.Candidate, budget, minSweeps int) []store.Candidate {
	if budget <= 0 {
		return nil
	}
	eligible := make([]store.Candidate, 0, len(cands))
	for _, c := range cands {
		if c.Status == "new" && c.SeenCount >= minSweeps {
			eligible = append(eligible, c)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return eligible[i].DollarVol > eligible[j].DollarVol
	})
	if len(eligible) > budget {
		eligible = eligible[:budget]
	}
	return eligible
}

// FmtDollarVol renders a dollar volume like "$1.2B" / "$340M" / "$9.5K".
func FmtDollarVol(v float64) string {
	switch {
	case v >= 1e12:
		return fmt.Sprintf("$%.1fT", v/1e12)
	case v >= 1e9:
		return fmt.Sprintf("$%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("$%.0fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("$%.1fK", v/1e3)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}

// ── daily auto-add counter (meta-backed) ────────────────────────────────

func autoAddKey(day time.Time) string {
	return "discovery_autoadds_" + day.UTC().Format("2006-01-02")
}

// AutoAddsToday reads the persisted auto-add counter for now's UTC day
// (0 when absent/malformed).
func AutoAddsToday(ctx context.Context, st *store.Store, now time.Time) int {
	v, err := st.GetMeta(ctx, autoAddKey(now))
	if err != nil || v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func bumpAutoAdds(ctx context.Context, st *store.Store, now time.Time) error {
	n := AutoAddsToday(ctx, st, now)
	return st.SetMeta(ctx, autoAddKey(now), strconv.Itoa(n+1))
}

// ── worker ──────────────────────────────────────────────────────────────

// SubscribeFunc is the server-side subscribe path (validate + upsert +
// activate + backfill) — the same function /api/subscribe calls.
type SubscribeFunc func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error)

// Worker is the universe-discovery agent.
type Worker struct {
	St        *store.Store
	Client    *Client       // nil when no Alpaca keys → worker degrades to a skip
	Subscribe SubscribeFunc // required for auto-add (nil disables auto-add)
	NowFn     func() time.Time
	// tunables (zero → defaults)
	Cap        int // active-symbol budget; 0 → SymbolCap()
	DailyLimit int // auto-adds per day; 0 → DailyAutoAddLimit
}

func (w *Worker) Name() string            { return "universe-discovery" }
func (w *Worker) Interval() time.Duration { return 6 * time.Hour }

func (w *Worker) now() time.Time {
	if w.NowFn != nil {
		return w.NowFn()
	}
	return time.Now()
}

func (w *Worker) cap() int {
	if w.Cap > 0 {
		return w.Cap
	}
	return SymbolCap()
}

func (w *Worker) dailyLimit() int {
	if w.DailyLimit > 0 {
		return w.DailyLimit
	}
	return DailyAutoAddLimit
}

// Run performs one sweep: fetch screeners, upsert candidates, then auto-add
// under budget. Degrades like NewsFetcher when no Alpaca keys are configured.
func (w *Worker) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no Alpaca keys", nil
	}
	swept, upserted, err := w.sweep(ctx)
	if err != nil {
		return "", err
	}
	added, err := w.autoAdd(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("swept %d candidates (%d recorded), auto-added %d", swept, upserted, added), nil
}

// sweep fetches both screener endpoints (each may independently fail — e.g. a
// 404 on a moved path — as long as ONE succeeds), merges rows per symbol, and
// upserts candidates that are not already actively ingested.
func (w *Worker) sweep(ctx context.Context) (swept, upserted int, err error) {
	type obs struct {
		volume    float64
		price     float64
		pctChange float64
	}
	seen := map[string]*obs{}
	order := []string{} // deterministic upsert order

	note := func(sym string) *obs {
		sym = strings.ToUpper(strings.TrimSpace(sym))
		if sym == "" {
			return nil
		}
		o, ok := seen[sym]
		if !ok {
			o = &obs{}
			seen[sym] = o
			order = append(order, sym)
		}
		return o
	}

	actives, errA := w.Client.MostActives(ctx, 20)
	if errA != nil {
		slog.Warn("universe-discovery: most-actives fetch failed — falling back to movers only", "err", errA)
	}
	for _, a := range actives {
		if o := note(a.Symbol); o != nil {
			o.volume = a.Volume
		}
	}
	gainers, losers, errM := w.Client.Movers(ctx, 10)
	if errM != nil {
		slog.Warn("universe-discovery: movers fetch failed — falling back to most-actives only", "err", errM)
	}
	for _, m := range append(gainers, losers...) {
		if o := note(m.Symbol); o != nil {
			o.pctChange = m.PercentChange
			o.price = m.Price
		}
	}
	if errA != nil && errM != nil {
		return 0, 0, fmt.Errorf("both screener endpoints failed: most-actives: %v; movers: %v", errA, errM)
	}

	// Enrich most-actives share volume into dollar volume with one bulk
	// latest-trade call. Best-effort: on failure those rows keep dollar_vol
	// from movers price (if any) or 0 — logged, never fatal.
	var need []string
	for sym, o := range seen {
		if o.volume > 0 && o.price == 0 {
			need = append(need, sym)
		}
	}
	sort.Strings(need)
	if len(need) > 0 {
		prices, err := w.Client.LatestPrices(ctx, need)
		if err != nil {
			slog.Warn("universe-discovery: latest-price fetch failed — dollar volume partial this sweep", "err", err)
		} else {
			for sym, p := range prices {
				if o, ok := seen[sym]; ok {
					o.price = p
				}
			}
		}
	}

	now := w.now().Unix()
	for _, sym := range order {
		o := seen[sym]
		// Already actively ingested → not a candidate.
		if s, err := w.St.GetSymbol(ctx, sym, md.Stocks); err == nil && s.Active {
			continue
		}
		if err := w.St.UpsertCandidate(ctx, store.Candidate{
			Symbol:     sym,
			Market:     md.Stocks,
			LastSeenTs: now,
			DollarVol:  o.volume * o.price,
			PctChange:  o.pctChange,
		}); err != nil {
			return len(seen), upserted, fmt.Errorf("upsert candidate %s: %w", sym, err)
		}
		upserted++
	}
	return len(seen), upserted, nil
}

// autoAdd promotes the best qualifying candidates while under budget. Every
// add goes through the same server-side subscribe path as /api/subscribe,
// lands on the ADMIN user's watchlist, and writes an insight saying why.
func (w *Worker) autoAdd(ctx context.Context) (int, error) {
	if w.Subscribe == nil {
		return 0, nil
	}
	// Hold the budget lock across the whole check→subscribe sequence so a
	// concurrent manual add can't race the cap (see BudgetMu).
	BudgetMu.Lock()
	defer BudgetMu.Unlock()
	now := w.now()
	active, err := w.St.ActiveSymbolCount(ctx)
	if err != nil {
		return 0, err
	}
	budget := AutoAddBudget(active, w.cap(), AutoAddsToday(ctx, w.St, now), w.dailyLimit())
	cands, err := w.St.Candidates(ctx, "new")
	if err != nil {
		return 0, err
	}
	picks := PickAutoAdds(cands, budget, MinSweeps)
	added := 0
	for _, c := range picks {
		sym, err := w.Subscribe(ctx, c.Symbol, c.Market)
		if err != nil {
			// Not fatal for the sweep: e.g. the symbol failed Alpaca asset
			// validation. Leave it 'new' so the operator can still see it.
			slog.Warn("universe-discovery: auto-add subscribe failed", "symbol", c.Symbol, "err", err)
			continue
		}
		if adminID, err := w.St.AdminUserID(ctx); err == nil && adminID != 0 {
			if err := w.St.AddUserSymbol(ctx, adminID, sym.ID); err != nil {
				return added, err
			}
		}
		if err := w.St.SetCandidateStatus(ctx, c.Symbol, c.Market, "added"); err != nil {
			return added, err
		}
		if err := bumpAutoAdds(ctx, w.St, now); err != nil {
			return added, err
		}
		body := fmt.Sprintf(
			"universe-discovery added %s: %s daily dollar volume, seen %d sweeps (budget %d/%d active, %d/%d auto-adds today)",
			c.Symbol, FmtDollarVol(c.DollarVol), c.SeenCount,
			active+added+1, w.cap(), AutoAddsToday(ctx, w.St, now), w.dailyLimit())
		evidence, _ := json.Marshal(map[string]any{
			"kind": "universe_discovery_autoadd", "symbol": c.Symbol,
			"dollarVol": c.DollarVol, "pctChange": c.PctChange, "seenCount": c.SeenCount,
		})
		if err := w.St.InsertInsight(ctx, md.Insight{
			Scope: "symbol", SymbolID: &sym.ID, Ts: now.Unix(),
			Headline: fmt.Sprintf("Auto-added %s to the universe", c.Symbol),
			Body:     body, Data: string(evidence),
		}); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}
