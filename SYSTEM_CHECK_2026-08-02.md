# Signal Deck System Check Report

## 1. Executive Summary

**Overall status: NOT READY** (for the stated goal of live auto-trading).
**As a measurement instrument: READY WITH WARNINGS.**

SignalDeck builds cleanly, starts, ingests real provider data, computes real
signals from it, and serves them over an authenticated API. That chain is
verified end to end with data ingested during this audit. The system is
substantially more functional than the "known concerns" list assumed: the Go
toolchain is fine, the provider is configured and reachable, streaming works,
and reconnection logic is real and tested.

It is **not ready for live execution**, for reasons that are about data
integrity rather than plumbing:

**Major blockers**
- **B2** Survivorship bias substantially unresolved — 21 delistings in 1,077
  symbols over 7.5 years, roughly 2% total against a real rate of several
  percent per year.
- **B3** `universe_membership` is empty, so there is no point-in-time universe
  and every cross-sectional feature would be computed against today's
  membership applied to historical dates.
- **B1** The accuracy grader has been refusing to publish since 2026-07-27.
  There is currently no verified accuracy number for anything.
- **F1** The documented startup command in README.md cannot work.

**Critical risks**
- **F2** Machine clock is ~77s slow, verified against two independent sources.
  Fixed in code this session; the clock itself still needs `w32tm /resync`.
- No execution layer exists at all — no order placement, no kill switch, no
  position caps. Live trading is not partially built; it is absent.

**Verified capabilities**
Backend and frontend builds, full Go test suite, daemon startup, schema
creation from scratch (97 tables), REST backfill of 822,481 bars across 417
symbols, signal computation from that data, authenticated API serving it, live
crypto WebSocket ticks, provider authentication, loopback-only binding.

**Unverified capabilities**
Live equity ticks (market closed), WebSocket-to-database persistence, runtime
reconnection behaviour, graceful shutdown, frontend E2E tests, and every
downstream claim that depends on B1/B2/B3.

**Recommended next action:** resolve B2 and B3 — they need a survivorship-free
historical vendor and gate everything downstream. Then B1. Execution last.

---

## 2. Audit Scope

| Field | Value |
|---|---|
| Repository | `C:\Users\Nicholas_N\Desktop\claude code\signaldeck` |
| Branch | `audit/2026-07-27` |
| Commit | `d25ceda0bba596fc60bbc878377c7f5d8a561aea` |
| Uncommitted | `ops/selfimprove-loop.ps1` (pre-existing), plus this session's fixes |
| Date | 2026-08-02, ~02:00–03:00 UTC (Sunday — US equity market closed) |
| Machine | Windows 11 Pro 10.0.26200, Go 1.26.5, Node 24.18.0, Python 3.12.10 |

**Components inspected:** Go daemon (`daemon/`, ~90 internal packages), Next.js
web app (`web/`), Python research tools (`tools/`), SQLite store (2.51 GB),
Alpaca provider integration, TickStream integration point.

**Limitations, stated up front:**
- US equity market closed for the entire audit window. Live equity ticks could
  not be observed and are reported BLOCKED, not FAIL.
- The daemon was started against a **temporary database**, not the live 2.5 GB
  one, to avoid agents writing into production data mid-audit. Production DB
  was opened read-only throughout.
- TickStream (`:8321`) and trader-hud (`:8787`) were not started, so paths
  depending on them are untested.
- Graceful shutdown could not be exercised: Windows would not deliver a signal
  to a `go run` child without `/F`.
- Playwright E2E suite not run.

---

## 3. System Architecture

Discovered, not assumed:

```
Alpaca REST      ──┐                                  ┌── Next.js web (:8323)
Alpaca WS (IEX)  ──┤                                  │   30+ routes
Kraken OHLC REST ──┼──▶ signaldeckd (:8322) ──────────┤
TickStream(:8321)──┤    ~90 pkgs, SQLite              │── /api/* (authenticated)
trader-hud(:8787)──┘    workers fleet + agents        └── CSV / raw SQL
```

| Link | Status | Evidence |
|---|---|---|
| Alpaca REST → parser | **VERIFIED** | 822,481 bars backfilled on first boot |
| Alpaca REST → store | **VERIFIED** | `bars` table populated, 417 symbols |
| Alpaca WS → connection | **VERIFIED** | dialled 232ms, authenticated, subscribed |
| Alpaca WS (crypto) → live msgs | **VERIFIED** | 5 real BTC/USD trades received |
| Alpaca WS (equity) → live msgs | **BLOCKED** | market closed, 0 msgs in 20s |
| WS → parser → store | **UNKNOWN** | never observed with data flowing |
| store → signal logic | **VERIFIED** | expectancy computed, n=299, hitRate=0.538 |
| signal logic → API | **VERIFIED** | `/api/symbol?symbol=AAPL` served it |
| API → frontend | **PARTIALLY VERIFIED** | frontend builds; not run against live API |
| TickStream → store | **FAILED/UNTESTED** | `snapshots_1s` empty, service down |
| Execution layer | **ABSENT** | no order-placement code exists |

---

## 4. Runtime and Dependency Results

| Component | Required | Installed | Status | Evidence |
|---|---|---|---|---|
| Go | `go 1.25.0` (go.mod) | 1.26.5 | **PASS** | newer toolchain builds older module |
| Node.js | unpinned | 24.18.0 | **PASS** | `npm run build` exit 0 |
| npm | unpinned | 11.16.0 | PASS | — |
| Next.js | 16.2.10 | 16.2.10 | PASS | `web/package.json` |
| React | 19.2.4 | 19.2.4 | PASS | — |
| Python | unpinned | 3.12.10 | PASS | tools run |
| numpy | required by `tools/alpha` | **absent from system python** | **WARN** | resolved via `uv run --with numpy` |
| SQLite | embedded | — | PASS | 97 tables created from scratch |
| TickStream | optional | **not running** | WARN | `:8321` refused |
| trader-hud | optional | **not running** | WARN | `:8787` refused, worker failed |

**No Go version mismatch exists.** Concern #1 and #2 from the brief are
disproven: `go build ./...`, `go vet ./...`, and `go test ./...` all pass.

---

## 5. Configuration Results

All values redacted; only presence and shape were checked.

| Variable | Present | Used by | Required | Status |
|---|---|---|---|---|
| `ALPACA_KEY` | Yes (`stock-trader/.env`) | Provider client | Yes | Present, `[REDACTED]`, auth verified |
| `ALPACA_SECRET` | Yes (`stock-trader/.env`) | Provider client | Yes | Present, `[REDACTED]`, auth verified |
| `SIGNALDECK_DB` | No (defaults) | Store | No | Default path resolves |
| `SIGNALDECK_API_TOKEN` | No | API auth | No | Auth still enforced via session |
| `SIGNALDECK_ALPACA_FEED` | No | Streamer | No | Defaults to IEX |
| `SIGNALDECK_FRED_KEY` | No | Macro ingest | No | Macro features degrade |
| `SIGNALDECK_GEMINI_KEY` | No | AI layer | No | AI layer used a different provider |
| `SIGNALDECK_DISCORD_WEBHOOK` | No | Alerts | No | Logged: alerts stay local |
| `SIGNALDECK_TELEGRAM_*` | No | Alerts | No | Same |
| `SIGNALDECK_ALLOW_DIRTY_BUILD` | Set by auditor | Build gate | No | **Required to start from source** |

31 `SIGNALDECK_*` variables are read in total; the remainder are tuning knobs
with working defaults. No `.env` exists under `signaldeck/` — Alpaca keys are
read from the sibling `stock-trader/.env`, which is an undocumented coupling.

---

## 6. Build and Test Results

| Command | Component | Result | Exit | Duration | Notes |
|---|---|---|---|---|---|
| `go build ./...` | Backend | **PASS** | 0 | 21.4s | No warnings |
| `go vet ./...` | Backend | **PASS** | 0 | — | Clean |
| `go test ./...` | Backend | **PASS** | 0 | ~4min | No failures |
| `gofmt -l` | Backend | **PASS** | 0 | — | Clean after edits |
| `npm run build` | Frontend | **PASS** | 0 | 20.1s | 30+ routes emitted |
| `npm run lint` | Frontend | **NOT RUN** | — | — | — |
| `npx playwright test` | E2E | **NOT RUN** | — | — | Requires running stack |
| `python test_labels.py` | New labeler | **PASS** | 0 | <1s | 12/12 asserts |
| `python smoke_real.py` | New labeler | **PASS** | 0 | 5.4s | 377,000 real events |

Concern #8 — "may build but fail at launch" — is **half true**: it builds, and
it *does* fail at launch under the documented command (see F1), though for an
integrity reason rather than a defect.

---

## 7. Startup and Service Results

**Command used:** `SIGNALDECK_ALLOW_DIRTY_BUILD=1 SIGNALDECK_DB=<temp> go run ./cmd/signaldeckd`

**Without the override the daemon refuses to start:**
```
level=ERROR msg="refusing to start: build is unattributable —
  rows it writes cannot be graded" revision_stamp="" vcs_modified=false
```
This is deliberate and correct — the daemon will not write rows it cannot
attribute to a revision. But it means README.md's `go run ./cmd/signaldeckd` is
wrong (F1).

**Startup behaviour (15 log lines, 3 warnings, 0 errors after override):**
- `api listening url=http://127.0.0.1:8322` — **loopback only**, not `0.0.0.0`
- First boot created schema: **97 tables**
- Seeded watchlist: BTC/USD + SPY QQQ AAPL NVDA TSLA
- Seeded broad daily universe: 400 symbols, **196,875 bars backfilled**
- AI layer enabled (llama-3.1-8b / nemotron-49b), daily cap 2000
- Initial admin user created, password printed **to stderr only**
- Warnings: `hud-sync` failed (`:8787` down), `congress-poller` degraded
  (external mirrors unavailable, DQ event recorded), dirty-build warning

**Health:** `GET /api/health` → `{"alpaca":true,"uptimeS":24,"version":"0.1.0-dev"}`
**Version:** `GET /api/version` → `{"revision":"","resolvable":false}` — honest
about its own unattributability.
**Readiness:** no `/ready` or `/api/ready` endpoint exists. `/health` and
`/ready` both 404 (the real path is `/api/health`).
**Shutdown:** code implements `signal.NotifyContext(SIGINT, SIGTERM)` +
`srv.Shutdown(shutCtx)`. **Not runtime-verified** — Windows refused `taskkill`
without `/F`, so no signal could be delivered; the process was force-killed.

---

## 8. Market-Data Provider Results

**Provider:** Alpaca. **Endpoints:** `paper-api.alpaca.markets` (REST),
`stream.data.alpaca.markets/v2/iex` (equities, the daemon's `DefaultStreamURL`),
`stream.data.alpaca.markets/v1beta3/crypto/us` (crypto).

| Check | Result | Evidence |
|---|---|---|
| Config present | PASS | keys load, `[REDACTED]` |
| REST reachable | PASS | HTTP 200 |
| REST auth | PASS | `status=ACTIVE`, `crypto_status=ACTIVE` |
| Market clock | — | `is_open=false`, next open 2026-08-03 09:30 ET |
| Crypto WS dial | PASS | 226ms |
| Crypto WS auth | PASS | `authenticated: true` |
| Crypto subscription | PASS | confirmed `[BTC/USD ETH/USD]` |
| **Crypto live ticks** | **PASS** | **5 real trades received** |
| Equity WS dial | PASS | 232ms |
| Equity WS auth | PASS | `authenticated: true` |
| Equity subscription | PASS | confirmed `[AAPL SPY]` |
| **Equity live ticks** | **BLOCKED** | 0 in 20s — market closed |
| REST bar backfill | PASS | 822,481 bars stored |

**Live tick evidence (crypto, redacted of nothing sensitive):**
```
T=t sym=BTC/USD px=63100    sz=0.01429967  event_ts=2026-08-02T02:06:53.186Z
T=t sym=BTC/USD px=63126.84 sz=1.7425e-05  event_ts=2026-08-02T02:06:55.281Z
T=t sym=BTC/USD px=63186.61 sz=0.000161    event_ts=2026-08-02T02:07:11.343Z
T=t sym=BTC/USD px=63193.62 sz=8.1179e-05  event_ts=2026-08-02T02:07:15.155Z
T=t sym=BTC/USD px=63253.33 sz=1.7548e-05  event_ts=2026-08-02T02:07:15.918Z
```
Monotonic timestamps, varying sub-satoshi sizes, price drifting 63,100 → 63,253
over ~23s. **This is real tape, not fixture data.**

**Latency could not be measured** — every reading came back at `-1m16.9s`
because of F2 (local clock 77s slow). Once the clock is corrected these become
meaningful.

**Concern #4 is disproven** (provider is properly configured), **#5 is
disproven** (WS connections succeed), **#3 is partially open**: WS ingestion
into the database was never observed with data actually flowing.

---

## 9. End-to-End Data-Flow Results

Traced for **AAPL**, using data ingested during this audit:

| Stage | Status | Evidence |
|---|---|---|
| 1. Provider data fetched | **VERIFIED** | Alpaca REST, first-boot backfill |
| 2. Parsed and validated | **VERIFIED** | 1,905 daily / 656 1h / 34,829 1m bars |
| 3. Internal state updated | **VERIFIED** | `bars` table, 417 symbols |
| 4. Signal logic invoked | **VERIFIED** | expectancy leg: `n=299`, `hitRate=0.5385`, `meanFwd=0.000616`, `stdev=0.0183` |
| 5. Result stored | **VERIFIED** | `scores` = 20 rows |
| 6. Served to consumer | **VERIFIED** | `GET /api/symbol?symbol=AAPL&market=stocks` returned coverage + expectancy |
| 7. User-visible change | **PARTIALLY VERIFIED** | frontend builds and has the routes; not driven against a live API |

**This is a genuine end-to-end path from real provider data to a served
signal.** It is the strongest positive result in this audit.

**What it is not:** this path is REST-backfill-driven, not stream-driven. The
WebSocket → parse → store → signal path was never exercised with live data,
because equities were closed and TickStream was down. **Concern #7 remains
open** for the streaming path specifically.

Note `hitRate = 0.5385` on `rsi:mid` over n=299 — a plausible, unexciting,
honest number. Nothing here looks inflated.

---

## 10. Reconnection and Error-Handling Results

| Scenario | Tested | Result |
|---|---|---|
| Dependency down (trader-hud) | **Yes, live** | Worker failed, logged clearly, daemon kept running |
| External source degraded (congress) | **Yes, live** | Marked degraded, DQ event recorded, stored history still served |
| Provider disconnect | No | Code-verified only |
| Reconnect backoff | No | Code + unit tests only |
| Duplicate subscription on resync | No | Code-verified (`diffSymbols`) |
| Stale feed detection | **Yes, unit** | New tests added this session |
| Clock skew | **Yes, unit** | New tests added this session |
| Malformed / empty message | No | Not exercised |
| Backend restart, DB unavailable | No | Not exercised |
| Shutdown during active stream | No | Could not deliver signal |

**Code evidence for reconnection** (`internal/workers/workers.go:200-260`):
jittered, capped exponential backoff, `streamBackoffCap = 2 * time.Minute`,
attempt counter resets after a healthy run. A comment records a **measured**
regression on 2026-07-31 — "~60 restarts in 68 seconds" with a dependency down
— which was diagnosed and fixed. `backoff_test.go` and `degraded_test.go` cover
it.

`internal/ingest/alpaca/streamer.go` has `resync()` and `diffSymbols()` so a
reconnect re-subscribes by difference rather than blindly re-adding, and
`isExpectedDisconnect()` distinguishes clean closes from faults.

**Verdict: reconnection is well-built and unit-tested, but has not been
observed recovering a real stream.** Concern #6 is largely answered by code,
not by demonstration.

---

## 11. Database and Storage Results

**Technology:** SQLite. **Production file:** 2,510,667,776 bytes (2.51 GB).

**Production content (read-only inspection):**

| Table | Rows |
|---|---|
| `bars` | 13,379,860 |
| `score_outcomes` | 1,557,150 |
| `scores` | 1,552,006 |
| `tv_ratings` | 543,182 |
| `predictions` | 265,556 |
| `prediction_ledger` | 261,225 |

**Bar coverage:**

| tf | rows | symbols | span |
|---|---|---|---|
| `1m` | 10,904,475 | 1,061 | 2026-06-02 → 2026-08-01 |
| `1h` | 834,255 | 1,063 | 2025-06-23 → 2026-08-01 |
| `1d` | 1,641,130 | 1,077 | 2019-01-02 → 2026-08-01 |

**Empty tables (6):** `congress_trades`, `regime_postmortems`,
`research_ledger_runs`, `snapshots_1s`, `symbol_bars_epoch`,
**`universe_membership`**.

**Migrations: PASS** — a fresh boot created all 97 tables and immediately
populated them. That is a strong migration test.

**Reads/writes: PASS** — 822,481 rows written to the temp DB during startup.

**Retention/indexes:** not audited in depth.

**Findings:** `universe_membership` empty (B3) and `snapshots_1s` empty
(TickStream never ran) are the two that matter.

---

## 12. Security Findings

No secret values appear anywhere in this report.

| ID | Severity | Finding | Location | Status |
|---|---|---|---|---|
| S1 | **INFO** | Initial admin password printed to stderr on first boot | `cmd/signaldeckd/run.go:588` | **Not a leak** — `fmt.Fprintf(os.Stderr, ...)`, bypasses the structured logger; `grep -ci password` on the log file returns 0. A supervisor capturing stderr could still persist it. |
| S2 | **PASS** | API binds loopback only | `api listening url=http://127.0.0.1:8322` | Correct |
| S3 | **PASS** | API requires auth | `/api/watchlist` → `{"error":"authentication required"}` | Correct |
| S4 | **PASS** | No secrets in source control | `.gitignore` covers `.env`/`.env.*`; only `.env.example` tracked | Correct |
| S5 | **PASS** | No hardcoded credentials | Regex sweep for `AK*`/`sk-*`/`PK*` across Go + TS: clean | Correct |
| S6 | **PASS** | CORS is explicit | `internal/api/security.go:54-57`, echoes an allowlisted origin, tested in `api_test.go:327-336` | Correct |
| S7 | **LOW** | No rate limiting observed on API | — | `SIGNALDECK_RATE_RPS`/`_BURST` exist; not verified active |
| S8 | **INFO** | No `panic()` in non-test daemon code | 0 occurrences | Good |

**Security posture is notably good.** Nothing here blocks anything. The audit
brief's concern about exposed secrets is not borne out.

---

## 13. Performance and Observability

**Measured**
- Go build: 21.4s. Frontend build: 20.1s. Full Go test suite: ~4min.
- Daemon cold start to serving: <24s (health reported `uptimeS:24` on first hit).
- First-boot backfill: 822,481 bars in ~2min (~6,800 rows/s).
- WS dial: 226ms (crypto), 232ms (equity).
- Triple-barrier labeler: 377,000 events over 200 symbols in 5.4s.

**Not measured** — tick-processing latency, provider-to-application latency,
throughput under sustained streaming, CPU/memory, goroutine count, queue depth,
dropped-message count, reconnect frequency, DB latency, frontend update rate.
All require a live stream that was unavailable.

**Provider latency is currently unmeasurable** because of F2: every reading was
`-1m16.9s`.

**Observability present:** structured logging via `log/slog` (14 files), DQ
event recording (32 files call `InsertDQ`), `/api/health`, `/api/fleet-health`,
`/api/model-health`, `/api/feature-health`, per-source freshness via
`internal/srchealth`, worker degradation states.

**Observability missing:** no Prometheus/OpenMetrics endpoint, no tracing, no
correlation IDs, no `/ready` distinct from `/health`.

---

## 14. Documentation Findings

| ID | Severity | Finding |
|---|---|---|
| D1 | **HIGH** | **README's startup command cannot work.** It documents `cd daemon && go run ./cmd/signaldeckd`; the daemon refuses to start from an unattributable build. The correct path (`ops/signaldeck-ctl.sh deploy`) appears only in the error message. |
| D2 | MEDIUM | The Alpaca-keys-from-`stock-trader/.env` coupling is mentioned in README but is a fragile cross-project dependency and is not in any setup checklist. |
| D3 | MEDIUM | No documented prerequisite list (Go/Node/Python versions). None are pinned anywhere except `go.mod`. |
| D4 | LOW | `tools/` has no documented Python environment; `numpy` is absent from system Python. |
| D5 | LOW | No `/ready` endpoint but health-check conventions are undocumented. |
| D6 | INFO | 27 markdown files at repo root, many superprompts and plans. Genuinely useful reference docs (`REPRODUCE.md`, `STORAGE.md`, `PREDICTION_PROCESS.md`) are hard to find among them. |

The README's live-accuracy block is auto-generated and currently prints a
refusal rather than numbers — which is correct behaviour, not a doc defect.

---

## 15. Acceptance-Criteria Matrix

| Criterion | Status | Evidence | Blocking? |
|---|---|---|---|
| A. Repository located | **PASS** | branch `audit/2026-07-27` @ `d25ceda` | No |
| B. Runtimes documented | **FAIL** | no pinned prerequisites; startup command wrong (D1) | No |
| C. Runtimes compatible | **PASS** | Go 1.26.5 ⊇ 1.25.0; build+vet+test green | No |
| D. Backend builds | **PASS** | `go build ./...` exit 0 | No |
| E. Frontend builds | **PASS** | `npm run build` exit 0, 30+ routes | No |
| F. Tests pass | **PASS** | full Go suite exit 0; E2E NOT RUN | No |
| G. Application starts | **PASS** (with override) | `api listening :8322` | No |
| H. Health checks pass | **PARTIAL** | `/api/health` 200; no `/ready` | No |
| I. Provider reachable | **PASS** | REST 200, WS dial 226/232ms | No |
| J. Authentication succeeds | **PASS** | `ACTIVE`; WS `authenticated: true` | No |
| K. Real-time stream established | **PASS** | both endpoints subscribed | No |
| L. Live ticks received | **PASS** (crypto) / **BLOCKED** (equity) | 5 BTC/USD trades; 0 equity, market closed | No |
| M. Ticks parsed and validated | **PARTIAL** | REST bars yes; WS→store never observed | **Yes** |
| N. Ticks reach downstream | **PARTIAL** | REST→expectancy→API verified; stream path not | **Yes** |
| O. Signals update from live data | **PARTIAL** | computed from backfill, not from a live stream | **Yes** |
| P. Disconnects handled | **PARTIAL** | 2 real dependency failures handled well; provider disconnect untested | No |
| Q. Reconnection works | **UNVERIFIED** | strong code + unit tests, no runtime demo | **Yes** |
| R. No secrets/security issues | **PASS** | see §12 | No |
| S. Docs sufficient to reproduce | **FAIL** | documented startup fails (D1) | **Yes** |

---

## 16. Findings by Severity

### CRITICAL

**F-C1 — Survivorship bias substantially unresolved**
Location: `bars` table / `tools/backfill_delistings.py`.
Evidence: 1,077 symbols with daily bars 2019→2026; only **21** stopped trading
>14d before the newest bar (~2% total vs. several %/year real).
Impact: models never see a company go to zero; every long-horizon backtest and
profit figure is inflated by an unknown, probably large margin.
Action: acquire survivorship-free history including delisted tickers.
Verification: delisted count consistent with historical base rates.

**F-C2 — `universe_membership` is empty**
Location: `universe_membership` (schema `day, symbol_id, source`), 0 rows.
Evidence: direct query.
Impact: no point-in-time universe. Every cross-sectional Z-score or rank is
today's membership applied to historical dates — lookahead bias in the
denominator of the feature the strategy depends on most.
Action: populate PIT daily membership.
Verification: `SELECT COUNT(DISTINCT day)` spans 2019→today.

**F-C3 — Accuracy grader refusing to publish since 2026-07-27**
Location: README live-accuracy block, `tools/accuracy_registry.py`.
Evidence: `GRADING REFUSED`; 4 `worker_runs` narrated 48-rule grid searches
with 0 `research_loop_judgments` rows for their UTC days.
Impact: **no verified accuracy number exists for any component.**
Action: make searches write judgment rows, or make narration honest.
Verification: README prints a table.

### HIGH

**F-H1 — Documented startup command cannot work**
Location: `README.md` "Run it"; `cmd/signaldeckd` build-attribution gate.
Evidence: `refusing to start: build is unattributable`.
Impact: a new developer cannot start the system by following the docs.
Action: document `ops/signaldeck-ctl.sh deploy` as the startup path.

**F-H2 — Machine clock ~77s slow**
Location: host OS.
Evidence: local `02:06:32` vs Alpaca `02:07:49` and Google `02:07:50`.
Impact: latency unmeasurable; timestamps misaligned against provider time;
would corrupt bar bucketing and any time-based risk gate.
Action: `w32tm /resync /force` (elevated). **Code hardened this session.**

**F-H3 — WebSocket→store path never observed with data**
Location: `internal/ingest/alpaca/streamer.go`, `internal/ingest/cryptolive`.
Evidence: `snapshots_1s` empty; equity WS produced 0 messages (market closed).
Impact: the live-ingestion path — the thing the system is *for* — is unproven.
Action: re-run during market hours with TickStream up.

**F-H4 — 1-minute history is 2 months deep**
Evidence: `1m` spans 2026-06-02 → 2026-08-01, ~40 trading days.
Impact: after purging and embargoing, any 1m-tier edge is likely noise.
Action: treat daily as the workhorse; let 1m accumulate.

### MEDIUM

**F-M1 — IEX feed is ~2–3% of consolidated volume.** Order-book imbalance and
VPIN computed from it describe one venue. Not fixable in code; needs a
consolidated-tape vendor. Crypto (TickStream L2) is the only valid
microstructure source today.

**F-M2 — Fails-open staleness guard.** `cryptolive.go:109` used `age > 10s`
only; negative age (skew) is never `> 10s`, so a dead feed reads as fresh.
**Fixed this session** + 6 new tests. Same class of bug latent wherever an
external timestamp is compared with `time.Since`. `internal/backup/backup.go:140`
already guarded correctly and was used as the precedent.

**F-M3 — Receipt time used as snapshot timestamp.** `cryptolive.go:117` stamped
`time.Now()` while `dto.PublishUnixNanos` was in scope. **Fixed this session.**

**F-M4 — No readiness endpoint** distinct from `/api/health`.

**F-M5 — Cross-project credential coupling** — Alpaca keys read from
`stock-trader/.env`. Fragile and undocumented as a setup step.

### LOW

**F-L1** — No metrics endpoint, no tracing, no correlation IDs.
**F-L2** — Rate limiting configured but unverified.
**F-L3** — `numpy` absent from system Python; `tools/` has no documented env.
**F-L4** — 6 TODO/FIXME markers in daemon (low for a codebase this size).
**F-L5** — 159 `_ = ` discarded returns; sampled ones were deliberate
`defer Close()` patterns, not swallowed errors.

### INFORMATIONAL

**F-I1** — Graceful shutdown implemented but not runtime-verified on Windows.
**F-I2** — Playwright E2E suite exists but was not run.
**F-I3** — Build-attribution gate and the grader's refusal behaviour are
genuinely good engineering; they should be preserved, not worked around.

---

## 17. Remediation Plan

**1. Immediate blockers**
| Action | Component | Complexity | Depends on | Validation |
|---|---|---|---|---|
| Acquire survivorship-free history | data vendor | High | budget | delisted count plausible |
| Populate `universe_membership` | store/universe | Medium | vendor | `COUNT(DISTINCT day)` spans 2019→now |
| Repair research-loop liveness | `tools/accuracy_registry.py` | Medium | — | README prints a table |

**2. Configuration**
| Fix README startup path | docs | Low | — | new dev can start it |
| Document prerequisites + pin versions | docs | Low | — | — |

**3. Runtime**
| `w32tm /resync /force` | host | Low | admin | probe lag becomes positive |

**4. Provider and streaming**
| Re-run provider check in market hours | — | Low | Mon 09:30 ET | equity ticks observed |
| Start TickStream, confirm `snapshots_1s` fills | tickstream | Low | — | row count > 0 |
| Runtime reconnection test (kill the socket) | streamer | Medium | above | stream recovers, no dupes |

**5. Data flow**
| Verify WS→store→signal with live data | ingest | Medium | above | trace one tick through |

**6. Reliability**
| Audit remaining `time.Since` on external timestamps | various | Low | — | no fails-open guards |
| Add `/ready` | api | Low | — | 200 when workers healthy |

**7. Security** — nothing blocking. Optionally confirm rate limiting is active.

**8. Testing** — run Playwright E2E; add a WS-ingestion integration test.

**9. Documentation** — D1–D6 above.

**10. Optional** — metrics endpoint, tracing, correlation IDs.

---

## 18. Exact Commands Used

| Command | Purpose | Result |
|---|---|---|
| `git rev-parse HEAD` / `--abbrev-ref HEAD` | identify commit/branch | `d25ceda`, `audit/2026-07-27` |
| `go version` / `head -5 daemon/go.mod` | version compare | 1.26.5 vs 1.25.0 — compatible |
| `go build ./...` | backend build | exit 0, 21.4s |
| `go vet ./...` | static check | exit 0 |
| `go test ./...` | test suite | exit 0 |
| `gofmt -l internal/...` | format check | clean |
| `npm run build` | frontend build | exit 0, 20.1s |
| `python dbscan.py` / `bars.py` | read-only DB inventory | 100 tables, coverage tables above |
| `go run ./probe` (scratchpad) | provider probe | REST 200, crypto 5 live ticks, equity 0 |
| `curl -sI https://...` ×2 | clock verification | local 77s slow |
| `SIGNALDECK_ALLOW_DIRTY_BUILD=1 ... go run ./cmd/signaldeckd` | startup | `api listening :8322` |
| `curl /api/health`, `/api/version`, `/api/symbol` | health + E2E | see §7, §9 |
| `uv run --with numpy python test_labels.py` | labeler check | 12/12 |
| `uv run --with numpy python smoke_real.py` | labeler on real data | 377,000 events |

No command that would reveal a secret is listed, and none was run against
production data in write mode.

---

## 19. Final Verdict

**Is SignalDeck operational?** Yes, as a measurement instrument. It builds,
starts, ingests real market data, computes real signals from it, and serves
them over an authenticated API. That was demonstrated end to end.

**Is it receiving real-time provider data?** Partially proven. Provider
authentication, WebSocket connection, and subscription all succeed on both
equity and crypto endpoints, and **live crypto ticks were received and
displayed**. Live *equity* ticks could not be observed because the market was
closed, and the WebSocket→database path was never exercised with data flowing.

**Does downstream functionality consume that data?** Yes — for the REST path,
verified concretely (AAPL: 1,905 bars → expectancy `n=299, hitRate=0.5385` →
served by `/api/symbol`). For the streaming path, unproven.

**Is it production-ready?** **No** — and specifically not for the live
auto-trading mandate. There is no execution layer at all: no order placement,
no kill switch, no position caps. Beyond that, the survivorship and
point-in-time universe gaps mean any profitability estimate produced today
would be unreliable in a direction that flatters the system, and the grader is
currently unable to publish an accuracy figure at all.

**What remains unverified:** live equity ticks, WebSocket persistence, runtime
reconnection, graceful shutdown, frontend E2E, sustained-load performance, and
every downstream number gated on F-C1/F-C2/F-C3.

**Conditions to change the verdict:**
1. Survivorship-free history with delisted names, and `universe_membership`
   populated point-in-time.
2. Grader publishing a real accuracy table.
3. A market-hours run showing live equity ticks landing in the database and
   moving a signal.
4. A runtime reconnection demonstration.
5. An execution layer with a kill switch, position caps, and a paper→live
   promotion gate — none of which exists yet.

---

## Top five actions

1. **Acquire survivorship-free historical data** including delisted tickers,
   and populate `universe_membership` point-in-time. Everything downstream is
   gated on this, and it is the only item requiring outside spend.
2. **Repair the research-loop liveness failure** so the grader publishes again.
   Until it does, SignalDeck has no accuracy number to build on.
3. **Fix the README startup command** to `ops/signaldeck-ctl.sh deploy`, and
   run `w32tm /resync /force`.
4. **Re-run the provider check during market hours with TickStream up**, and
   confirm a live tick reaches the database and moves a signal.
5. **Do not begin the execution layer until 1–4 are done.** Live order
   placement on top of unverified accuracy and survivorship-biased history is
   the single highest-risk sequencing error available here.

---

# ADDENDUM — Remediation executed 2026-08-02

## What was fixed

| Finding | Status | Evidence |
|---|---|---|
| F-C1 survivorship | **LARGELY CLOSED** | delisted symbols 21 → **716**; 40 collapse-grade (<20% of peak) |
| F-C2 point-in-time universe | **CLOSED** (different root cause) | `TradableAt` 0 → 1,325 / 985 / 1,009 for 2021 / 2023 / 2025 |
| F-H1 README startup | **FIXED** | documents `ops/signaldeck-ctl.sh deploy` + dev override |
| F-M2 fails-open stale guard | **FIXED** | bidirectional age check + 6 tests |
| F-M3 receipt-time stamping | **FIXED** | uses `dto.PublishUnixNanos` |
| F-M4 no readiness endpoint | **FIXED** | `/api/ready`, 503 when degraded, 4 tests |
| D3 prerequisites undocumented | **FIXED** | README Prerequisites table |
| F-C3 grader refusing | **NOT DONE** | still `GRADING REFUSED` |
| F-H2 machine clock 77s slow | **CODE HARDENED, CLOCK NOT SET** | needs `w32tm /resync /force` |

## The finding that mattered most

**`universe_membership` was a red herring.** It has zero references anywhere in
the repo — nothing reads or writes it. The real point-in-time mechanism is
`store.TradableAt(ts)`, which filters `added_at <= ts`.

And `added_at` was being set to `time.Now()` on insert — the date the daemon
first SAW a symbol, not the date it started trading. Measured on 2026-08-02,
**`TradableAt` returned 0 symbols for every historical date tested**. The
survivorship accessor built on 2026-07-24 had never worked: it did not return a
biased universe, it returned an empty one, silently, and nothing detected that.

`RepairAddedAtFromBars` rewrites `added_at` to each symbol's first daily bar.
1,727 symbols repaired. The universe is now more populous in 2021 than 2023,
which is the correct shape once dead companies are visible.

## Data recovered

Two independent free sources, because the first has a hard ceiling:

1. **Alpaca inactive assets** — 19,205 inactive securities, 2,099 exchange-listed
   with alpha tickers → 650 usable delistings, 169,296 bars.
   *Ceiling:* the asset API is not a complete enumeration of what the bars API
   knows. FRC, SIVB, TWTR, ATVI and SBNY appear in **neither** asset list, yet
   their full history is served by the bars endpoint.

2. **Wikipedia S&P 500 removals** — 383 removal events, 372 tickers → 81 further
   delistings, 61,501 bars, and these are the large-cap failures the first pass
   missed: First Republic (-98%), SVB (-86%), Rite Aid (-98%), Big Lots (-99%),
   Mallinckrodt (-98%).
   *Guard:* index removal is not delisting. 160 tickers were left alone because
   they still trade (Conagra left the index in June 2026 and is alive).

**SEC Form 25 is a dead end** and was abandoned after testing three routes:
`company_tickers.json` drops delisted names (70% unresolved), per-CIK
submissions return the post-delisting OTC ticker, and the filing document
carries no symbol at all. EDGAR records that a delisting happened but not which
ticker it happened to.

## Known data-quality caveats

* **SBNY** carries a delisting date of 2025-03-21, but Signature Bank was seized
  in March 2023. Its ticker was almost certainly reassigned; the bars after 2023
  are probably a different entity. Treat SBNY's tail with suspicion.
* IEX history begins 2020-07-27, so recovered delistings span ~6 years, not the
  7.5 the survivor universe covers.
* The recovered population still skews to 2021-2022. Cohort tags
  (`collapse` / `ordinary` / `spac_shell` / `spac_named`) exist in staging so a
  cohort can be held out; production carries no cohort column, but the
  classification is reproducible from bars via `tools/alpha/tag_delisted.py`.
* 214 tickers were refused as reassignments (COHR, CZR, ECHO all trade today
  under symbols a previous company was delisted from). Zero live symbols were
  altered by either import.

## Verification

```
go build ./...   OK        go vet ./...   OK        go test ./...   OK
symbols 1,080 → 1,780      delisted 21 → 716        1d bars 1,641,130 → 1,851,057
incoherent rows (added_at > delisted_at): 0
active symbols carrying delisted_at:      0
backup: data/signaldeck.db.bak-preimport-20260802 (2,510,667,776 bytes)
```
