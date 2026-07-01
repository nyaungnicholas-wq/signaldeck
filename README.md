# SignalDeck

**Data-first market intelligence.** SignalDeck continuously records live
market data — crypto (via [TickStream](../tickstream)) and **any US stock on
demand** (Alpaca IEX) — into a growing SQLite database, computes a
fully-decomposable **Pressure Score** per symbol and horizon, measures **what
usually happened next** in comparable historical states, writes **readable
plain-English insights**, and — the differentiator — **grades its own scores
against realized returns** on the Honesty page.

Not an auto-trader. Not financial advice. A measurement instrument.

```
tickstreamd (:8321) ──┐                       ┌── web app (Next.js, :8323)
   crypto L2/NBBO     │   signaldeckd (:8322) │   watchlist · symbol · screener
                      ├─▶  12 in-app agents  ─┤   trends · insights · honesty
Alpaca IEX ws/REST ───┤   SQLite (data/*.db)  │   quality · agents · PUSH-20 HUD
Kraken OHLC REST ─────┤                       │
trader-hud (:8787) ───┘                       └── CSV exports / raw SQL
```

## Run it

```sh
# 1. crypto feed (separate process, optional but recommended)
cd ../tickstream && make run                      # :8321

# 2. the data daemon (agents + API); Alpaca keys are read from
#    stock-trader/.env automatically (ALPACA_KEY / ALPACA_SECRET)
cd daemon && go run ./cmd/signaldeckd             # :8322

# 3. the app
cd web && npm run dev -- -p 8323                  # http://localhost:8323

# optional: the PUSH-20 trader HUD source
cd ../stock-trader && .venv/bin/python dashboard/server.py   # :8787
```

First boot seeds BTC/USD + SPY, QQQ, AAPL, NVDA, TSLA and backfills ~2 years
of daily bars (plus 60 days of minutes for stocks). Add any US ticker or
Kraken pair from the watchlist header — validation, backfill, live streaming,
scoring, and tendency tables all happen automatically within seconds.

## The in-app agents (visible at /agents)

| Agent | Cadence | Job |
|---|---|---|
| crypto-live | 1 Hz stream | TickStream consolidated book → 1s microstructure snapshots |
| stock-streamer | stream | Alpaca IEX live minute bars for active stocks |
| backfiller | on demand | 2y daily + 60d minute history for new symbols, then primes rollups + tendencies |
| crypto-bars | 15 m | Kraken OHLC refresh (2y/1d, 30d/1h, 12h/1m) — canonical crypto bars |
| stock-bars | 6 h | official daily-bar top-up / gap healing |
| downsampler | 5 m | 1m→1h rollups + retention (90d minutes, 7d snapshots) |
| signal-runner | 1 m | Pressure Scores for every active symbol × horizon |
| expectancy-runner | 1 h | rebuilds conditional forward-return tables |
| outcome-resolver | 10 m | grades past scores against realized returns (Honesty) |
| insight-writer | 15 m | plain-English symbol reads + market brief (deduplicated) |
| hud-sync | 1 m | syncs the PUSH-20 trader HUD summary |
| dq-auditor | 5 m | flags stale/gapped data as incidents |

Every run is persisted (status + detail) — the Agents page renders exactly
what ran, including failures.

## Honesty by construction

- **Scores decompose.** Every Pressure Score is stored with its component
  table (value, normalization, weight, contribution, plain-English note) —
  there is no black box to trust.
- **"Prediction" = measured tendency.** The expectancy engine reports
  *"in this state, the next day resolved higher 46% of the time (n=122,
  median −0.4%)"* — sample sizes always attached, "not a forecast" always
  stated.
- **The system grades itself.** Every score seeds an outcome row at write
  time; the resolver later records what the market actually did; the Honesty
  page shows the score↔forward-return correlation (IC) and quintile table.
  Nothing is backfilled or cherry-picked — a wrong call stays wrong in the
  record.
- **Data quality is a page, not a footnote.** Coverage spans, freshness, and
  every incident (stale feed, gap, failed backfill) are visible at /quality.

## Storage

SQLite (WAL) at `data/signaldeck.db`. Retention: 1s crypto snapshots 7 days,
1m bars 90 days, 1h/1d bars forever — the database grows deliberately.
Exports: per-symbol bars/scores CSV and the full honesty outcomes CSV from
the UI, or query the .db directly with any SQLite client.

## Repo layout

```
daemon/   Go — store (SQLite), ingest (cryptolive/alpaca/cryptohist),
          signals, expectancy, insights, pipeline workers, maintain,
          hud sync, JSON API (:8322)
web/      Next.js 16 — 9 pages, typed API client, terminal design system
```

## Boundaries (stated, not hidden)

Free IEX feed covers ~2–3% of US equity volume (fine for signals; not
SIP-consolidated). Crypto bars come from Kraken only. Expectancy tables are
per-symbol tendencies, not cross-sectional models. The insight writer is
deterministic templates (no LLM). Execution stays in stock-trader — this is
the instrument panel, not the pilot.
