# PIPELINE — the dataflow, and which of the 93 workers occupy each stage

The daemon runs ONE fleet (`cmd/signaldeckd/run.go`, `runner.Add`). "Pipeline"
is not a scheduler construct — there is no DAG engine, no topological ordering,
no barrier. Every worker is an independent loop over the same SQLite store, and
the store IS the message bus. This document records the dataflow that the
cadences are chosen to approximate, so a future cadence change can be checked
against intent instead of guessed at.

## The intended flow

```
External release / market calendar event
        │
        ▼
Ingestion workers ─────────────► commit rows + advance the source's watermark
        │
        ▼
Feature invalidation / build ──► changed symbol/time ranges
        │
        ▼
Scoring + regime analysis ─────► new score version
        │
        ▼
Ranking / expectancy / risk ───► actionable candidates
        │
        ▼
Paper trader
        │
        ▼
Outcome resolution + postmortem

Independent control plane (does NOT sit in the flow):
scheduler · integrity checks · model health · alerts · reporting
```

## Stage occupancy

**Ingest** — `stock-streamer`, `crypto-live`, `backfiller` (long-running, no
interval); `tv-quotes`, `universe-live` (1m); `crypto-bars`, `crypto-perp`,
`stocktwits-fetcher`, `tv-rating` (15m); `news-fetcher`, `backfill-reconciler`
(20m); `news-backfiller` (30m); `filings-poller` (2h); `stock-bars`,
`hist-backfill`, `fred-poller`, `cboe-pc`, `universe-discovery`,
`universe-poller`, `tape-seeder` (6h); `sic-bulk-sync` (12h); `companies-sync`,
`edgar-fetcher`, `delisting-detector` (24h); and the CALENDAR workers below.

**Feature build** — `signal-runner` (1m); `sentiment-tagger` (10m);
`sentiment-lex-scorer`, `breakout-runner` (15m); `sentiment-aggregator`,
`news-trends` (30m); `per-symbol-learner`, `pressure-trainer` (1h);
`alpha-trainer`, `adaptive-weights` (6h); `metalabel-runner` (12h);
`pattern-stats`, `wiki-attention` (24h).

**Scoring + regime** — `composite-scorer` (10m); `confluence-resolver` (15m);
`confluence-scorer`, `regime-runner` (30m); `smart-money-scorer` (1h);
`vol-regime-runner`, `regime-outcome-runner` (6h).

**Ranking / expectancy / risk** — `ranking-runner`, `sector-rotator`,
`expectancy-runner` (1h); `return-distribution-runner` (2h).

**Paper trader** — `paper-trader` (1h).

**Outcome resolution + postmortem** — `outcome-resolver`,
`prediction-resolver` (10m); `postmortem-runner`, `model-health` (1h).

**Control plane** (deliberately outside the flow) — `cache-warmer` (60s);
`hud-sync` (1m); `alert-runner`, `anomaly-scanner`, `downsampler`, `dq-auditor`
(5m); `watchdog` (10m); `source-audit`, `derived-retention`,
`scores-compactor`, `storage-governor`, `ai-analyst`, `canary-runner`,
`weekly-digest` (1h); the four `honesty-gap` tiers (1h/2h/6h/24h);
`feature-health`, `self-audit`, `split-repair`, `dataset-version-runner` (6h);
`price-validator`, `evidence-staleness-runner`, `feature-redundancy-runner`,
`ops-heartbeat` (24h); the research loop (`research-engine`, `research-lab`,
`research-ledger` 6h, `research-loop`, `strategy-lab` 24h, `prereg-registrar`
12h); `ai-watcher`, `insight-writer` (15m); `daily-briefing` (6h).

## Calendar workers (`workers.ScheduledWorker`)

These do NOT have a cadence — they have a publication schedule, and the runner
sleeps to the exact instant (`internal/workers/schedule.go`). Interval() is
retained only to size the per-run timeout.

| Worker | Fires | Upstream reality |
|---|---|---|
| `finra-shorts` | trading days 18:30 ET | daily Reg SHO file, published by ~18:00 ET |
| `finra-shortint` | trading days 18:45 ET | semi-monthly, ~8 business days after settlement |
| `cot-poller` | Saturday 09:00 ET | CFTC COT, Friday 15:30 ET, Tuesday's positions |
| `13f-poller` | daily 20:00 ET | quarterly filings, manager list rotated by cursor |
| `congress-poller` | daily 09:00 ET, backing off to 7d while mirrors are dead | STOCK Act allows 45 days to file |
| `weekly-report` | Sunday 17:00 ET | its own week |
| `signalbt-weekly` | Sunday 18:00 ET | its own week, one hour after the report |

Each keeps its internal date/week-key gate. The gate is what makes a catch-up
run idempotent; the schedule only stops the fleet from consulting it hundreds of
times per useful answer.

## Where reality diverges from the diagram — on purpose

1. **There are no stage barriers.** A worker consumes whatever its upstream has
   already committed. This is deliberate: a barrier would make one slow ingest
   stall scoring for every symbol, and the store's rows are timestamped, so a
   consumer can always tell how stale its input is.

2. **`signal-runner` (1m) is not the head of the scoring chain.** It is a
   SAMPLER: every insert also seeds the score's outcome row, so its cadence sets
   the density of the forward honesty record. It looks like a cadence inversion
   against `composite-scorer` (10m) and `ranking-runner` (1h), but those are
   independent producers, not its consumers. Slowing it thins the research
   corpus; do not change it to "fix" an inversion that is not there.

3. **The three sentiment workers are queue drains, not a redundant chain.**
   `sentiment-tagger` is LLM-paced (batch size and inter-call delay tuned to a
   burst-shaped provider limit); `sentiment-lex-scorer` pulls `UnscoredNews`,
   i.e. it is already incremental and does nothing when the queue is empty;
   `sentiment-aggregator` recomputes exactly two UTC days of aggregates because
   headlines land late and near midnight. Merging them into one event-triggered
   pass would couple LLM rate limiting to news arrival for no measured gain.

4. **`cache-warmer` (60s) matches the API cache TTL by construction.** It is a
   keep-warm loop, not a poll: the caches expire on a timer regardless, so an
   event-driven rebuild would still need the timer, plus a hook in every write
   path.

5. **The ~10 integrity workers stay separate on purpose.** Each writes its own
   `worker_runs` row, which is what makes per-check liveness visible on the
   Agents page and what gives each check its own run timeout. Folding them into
   one orchestrator would trade 10 independently monitorable checks for 1 row
   and one shared deadline.

## The quiesce cost

`storage-governor` holds the WHOLE fleet still so the WAL can be
TRUNCATE-checkpointed. That pause is now measured on every window
(`Runner.LastQuiesce`): drain duration, how many runs were in flight, fleet
size, and whether the drain timed out. A drain over 5s, or any drain timeout,
writes a `quiesce_stall` dq event. Check that number before adding worker #94 —
the cost of the pause scales with the fleet, and it was previously invisible.
