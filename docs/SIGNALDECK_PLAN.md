# SignalDeck — Planning Doc (v0.1, 2026-07-01)

> A **data-first market intelligence app**: continuously gathers live market
> data (crypto now, any stock on demand), stores it in a queryable database,
> computes signals over it, and shows trends + a clear "which side is
> stronger" read per symbol. The database IS the product; the UI is a lens on
> it. Grows out of TickStream (which becomes the crypto ingestion engine).

---

## 1. What it does (one paragraph)

You type any symbol — `BTC/USD`, `AAPL`, `NVDA` — and SignalDeck starts
recording it: quotes, trades, bars, order-book signals. Everything lands in a
local time-series database with defined retention. On top of that data it
computes a signal stack (order-flow imbalance, trend, momentum, volatility,
volume) and rolls them into an honest per-symbol **Pressure Score** (buy side
vs sell side) with full "why" attribution — every score decomposes into the
signals that produced it, every signal links to the raw rows behind it. A
watchlist heatmap ranks everything you track; a screener answers "which one
looks best right now"; alerts fire when conditions you define trip.

**Not** an auto-trader, **not** financial advice — a measurement instrument.
(Same honesty brand as TickStream: every number traceable to raw data.)

---

## 2. Data sources (the core decision)

| Market | Source | Cost | Notes |
|---|---|---|---|
| Crypto L2 + trades | **TickStream engine** (Coinbase + Kraken, already built) | free | Reuse as a library — books, checksums, NBBO, imbalance already done |
| US stocks — real-time quotes/trades/bars | **Alpaca Market Data websocket** (IEX feed) | free tier | You ALREADY have an Alpaca account (stock-trader paper). ~30 symbols concurrent on free; on-demand subscribe/unsubscribe fits perfectly |
| US stocks — history backfill (bars) | Alpaca REST bars API | free | Backfill daily/minute bars on first subscribe so charts aren't empty |
| Fallback / EOD breadth | Stooq / Yahoo CSV endpoints | free | Nightly EOD sweep for the whole watchlist |
| Context (later) | ^VIX (already wired in stock-trader), Fed calendar, earnings dates | free | Regime layer |

Key constraint stated honestly in-app: free IEX real-time ≈ ~2-3% of US
volume (indicative, fine for signals; not SIP-consolidated). Paid upgrades
(Polygon, Alpaca Pro) slot in behind the same adapter interface later.

## 3. Architecture

```
                 ┌────────────────────────────────────────────┐
                 │              INGESTION DAEMON (Go)          │
                 │  crypto: TickStream engine (as a library)   │
                 │  stocks: Alpaca WS adapter (on-demand subs) │
                 │  normalize → Tick / Quote / Bar / Signal    │
                 └───────────────┬────────────────────────────┘
                                 ▼  batched writes (1s flush)
                 ┌────────────────────────────────────────────┐
                 │   DATABASE  (SQLite WAL → per-market files) │
                 │   ticks_raw (ring, 24-48h)                  │
                 │   snapshots_1s (7d) → bars_1m (2y) → 1d (∞) │
                 │   signals (computed, versioned)             │
                 │   symbols / subscriptions / alerts / scores │
                 └───────────────┬────────────────────────────┘
                                 ▼
                 ┌────────────────────────────────────────────┐
                 │  SIGNAL ENGINE (Go, runs on schedule + live)│
                 │  compute → score → rank → alert-evaluate    │
                 └───────────────┬────────────────────────────┘
                                 ▼  HTTP/JSON + SSE
                 ┌────────────────────────────────────────────┐
                 │  WEB APP (Next.js): watchlist heatmap,      │
                 │  symbol page, screener, trends, alerts, DQ  │
                 └────────────────────────────────────────────┘
```

- **One Go daemon** does ingest + store + compute + serve JSON (TickStream
  pattern, proven). Runs on login via launchd like Mission Control.
- **SQLite (WAL mode)** to start: zero-config, one file per market, easily
  synced/backed up; `bars` tables downsampled on a schedule. Escape hatch to
  QuestDB/Timescale later if write volume demands it (crypto ticks are the
  only heavy stream; stocks on IEX free tier are light).
- **Next.js frontend** (your standard stack — lotline/throughline patterns),
  dark terminal design language carried over from the TickStream dashboard.

## 4. The signal stack (v1)

**Order-flow / microstructure (crypto now, stocks where quote data allows):**
imbalance (signed + fraction), weighted-mid vs mid drift, spread state,
cross-venue divergence (crypto).

**Trend:** SMA/EMA (20/50/200) position + slope, higher-highs/lower-lows
structure, ADX-style trend strength, distance from VWAP.

**Momentum:** ROC (1h/1d/1w), RSI(14) with divergence flag, MACD state.

**Volatility/regime:** realized vol (rolling σ), ATR, vol-of-vol, VIX regime
tag for stocks (reuse stock-trader's ^VIX wiring), squeeze detection
(BB width percentile).

**Volume:** relative volume (RVOL), volume trend, up/down volume ratio.

**The Pressure Score** — the "which side is better" headline:
- Each signal normalizes to [-1, +1] (sell ↔ buy) with a confidence weight.
- Composite = weighted sum, shown as a gauge PLUS the full decomposition
  table (no black box — every score explains itself).
- Timeframe-aware: separate score at 1h / 1d / 1w horizons (a thing can be
  buy-the-dip on 1w and sell-pressure on 1h — show both, never blend
  silently).
- Backtest hook: every score is persisted, so "was the score any good?" is a
  query, not a hope. (Score-vs-forward-return scatter = the honesty page.)

## 5. Screens (v1)

1. **Watchlist / heatmap** — every tracked symbol as a tile: price, sparkline,
   pressure gauge, RVOL; sortable/rankable. The "what should I look at" page.
2. **Symbol page** — candlestick chart (lightweight-charts) with signal
   overlays, order-book/imbalance panel (crypto), signal decomposition table,
   trend read per horizon, raw-data drill-down.
3. **Screener** — filter across ALL tracked symbols ("RSI < 30 AND above
   200-SMA AND RVOL > 2"), saved screens.
4. **Trends** — market-wide view: breadth (% above 200-SMA), sector heat,
   regime banner (VIX tag), top movers by score change.
5. **Alerts** — condition builder ("AAPL pressure > +0.5 on 1d", "BTC crossed
   below VWAP"), fires to macOS notification + optional email (Resend, you
   have the pattern).
6. **Data quality** — the TickStream-honesty page: per-symbol ingest lag,
   gaps, dropped events, resyncs, coverage calendar, source disclosures.

## 6. On-demand symbols (the flow)

Search box → hits Alpaca/known-symbol list → `subscribe(SYMBOL)` →
(a) history backfill job (2y daily + 30d minute bars), (b) live WS
subscription added, (c) signal engine picks it up next cycle (≤1min), 
(d) tile appears on watchlist. Unsubscribe keeps history, stops live.
Subscription budget shown (free tier caps concurrent live symbols — manage
it visibly, evicting LRU with user consent, never silently).

## 7. What else to add (your question) — ranked

**Should be in v1:**
- **Score history + honesty backtest** (§4) — this is the differentiator.
- **Data-quality page** (§5.6) — trust is the product.
- **Watchlist persistence + CSV/Parquet export** — it's a database; let the
  data out (feeds your stock-trader research directly).

**v1.5 (high value, low risk):**
- **Alerts** (macOS notify first, email later).
- **Correlation matrix** across watchlist (what actually diversifies).
- **Earnings/econ calendar overlay** on charts (free sources).
- **Paper-trade hook**: one-click "log a paper position" against a score, so
  the app builds a track record of *your* reads (ties into Alpaca paper acct).

**v2 (park for now):**
- News/sentiment ingestion (RSS + LLM tagging), options flow (paid data),
  portfolio import, multi-device sync/deploy (Vercel front + small VPS for
  the daemon), micro-price offline fit from TickStream M6, mobile PWA wrap.

**Deliberately excluded:** auto-execution of trades (stock-trader already
owns live trading; SignalDeck is the instrument panel, not the pilot), paid
data dependencies in v1, any "AI predicts the price" claims.

## 8. Build phases (TickStream-style, each demoable)

| Phase | Deliverable | Demo |
|---|---|---|
| P0 | Repo + schema + migrations + config | `sqlite3` shows tables |
| P1 | Crypto persist: TickStream → DB (1s snapshots, 1m bars) | query BTC bars from SQL |
| P2 | Alpaca stocks adapter + on-demand subscribe + backfill | `AAPL` streams into DB |
| P3 | Signal engine + Pressure Score (persisted, versioned) | scores in DB, decomposed |
| P4 | API + Next.js: watchlist heatmap + symbol page | the app, live |
| P5 | Screener + trends + data-quality page | full v1 |
| P6 | Alerts + export + score-honesty backtest page | v1.5 |

Est: P0–P4 is one focused build session each; the whole v1 is roughly a
TickStream-sized effort ×2 (the frontend is the bulk).

## 9. Decisions (LOCKED 2026-07-01)

1. **Markets:** ✅ Crypto (TickStream engine) + US stocks on demand (Alpaca
   free IEX websocket, existing account).
2. **Stack:** ✅ Go ingestion/signal daemon + Next.js frontend.
3. **Storage:** ✅ SQLite-first (WAL, per-market files, downsample schedule);
   QuestDB is the named escape hatch if crypto tick volume demands it.
4. **Raw crypto ticks:** default to the 24-48h ring; Parquet archival is a
   flag, off by default (revisit when the honesty-backtest page wants deeper
   history).

**Next step when ready to build:** P0 — repo scaffold (`~/claude code/signaldeck`),
schema + migrations, config, launchd plan. Say "build P0" (or "build P0-P4"
for the whole core).
