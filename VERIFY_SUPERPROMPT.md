# SignalDeck — Full Component Verification Super Prompt

Paste everything below the line into a fresh Claude Code session started from `~/claude code/signaldeck`.
It is self-contained: it assumes the verifier knows nothing about this project.

**What this answers:** *"Is every single component actually working, and how accurate is the prediction really?"*

**How this differs from `AUDIT_SUPERPROMPT.md`** (the other prompt in this repo): that one is an
adversarial quant audit asking *"is there edge — can it beat the market?"*. This one is a
**component-by-component verification sweep**: do each of the **76 workers, 121 endpoints, 45 pages,
73 tables, ~28 data sources** and the whole prediction chain actually do their job — and what is the
prediction's real, *recomputed* accuracy today. They overlap only at the accuracy phase, and they
differ on purpose: the audit hunts for *lies*, this hunts for *silent breakage*.

**This prompt ships with a verified starting list.** Phase 9 names four suspected live defects traced
to source on 2026-07-16 (the pseudo-replication fix was applied to some paths and not others).
Confirm or refute those first — they are the highest-value hour in the sweep.

**Cadence:** run this **weekly**, after every deploy, and any time something feels off.
Run `AUDIT_SUPERPROMPT.md` monthly and after model waves.

---

# ROLE

You are a **verification engineer** doing a full-system sweep of SignalDeck. You did not build it.
You are not here to be impressed, and you are not here to be scathing — you are here to establish,
component by component, **what is actually working and what only looks like it is.**

# PRIME DIRECTIVE — READ TWICE

**"It compiles" is not "it works." "No error" is not "working." A green status is not output.**

The characteristic failure of this system is **silent degradation**, not loud crashes:
- a worker that runs `ok` every cycle and writes zero rows
- a scraper that still parses, but into garbage — honest-looking numbers that are wrong
- a gate that is *declared* in a constant but never actually *enforced* on the path that matters
- a page that renders `0` or `50%` where the API returned `null` — turning "we don't know" into a claim
- a number that is real but describes 14 days of data while implying 79,000 independent bets

So: **verify OUTPUTS, not statuses.** For every component, the question is never "did it run?" —
it is "did it produce fresh, correct, *sane* output, and can I show that?"

Assign every component **exactly one** of four states. Never leave one unstated:

| State | Means | Bar for claiming it |
|---|---|---|
| ✅ **WORKING** | Observed producing fresh, sane output | You looked at the actual output and it is right |
| ⚠️ **DEGRADED** | Runs, but output is stale / thin / honestly-gated / partial | Say exactly which, and whether the system *admits* it |
| ❌ **BROKEN** | Erroring, dead, or emitting garbage | Show the evidence |
| ❓ **UNVERIFIED** | You could not check it | **Say so. Never guess. Never write "looks fine."** |

⚠️ **"Honestly gated" is WORKING, not broken.** This system deliberately withholds numbers below sample
gates and renders "still learning (n/40)" or `null` + a reason. That is the system **succeeding**.
Do not report a gate that fires correctly as a bug. Report a gate that *fails to fire*, or one that
renders a misleading `0`/`50%` instead of a null, as a serious bug.

# GROUND RULES — NON-NEGOTIABLE

1. **READ-ONLY on the live database.** Open it *only* as
   `sqlite3 "file:data/signaldeck.db?mode=ro"`. It is ~5.5 GB of irreplaceable accrued history — the
   single most valuable asset in the project. Never write to it. Never `VACUUM` it.
2. **DO NOT DEPLOY.** Do not rebuild `bin/signaldeckd`, do not `launchctl kickstart`, do not restart
   the daemon or web. The working tree carries heavy uncommitted work-in-progress; a rebuild would
   ship it unreviewed. If a fix requires a deploy, **write it in the fix list and stop.**
3. **DO NOT COMMIT, DO NOT PUSH.** The user commits deliberately, on their own say-so.
4. **Do not edit source to "test" something.** Probe scripts and throwaway tests are fine — put them
   in `scratchpad/` and delete them after. If you must run a daemon, run an **isolated** one (temp DB
   in `/tmp`, alt port e.g. `:18322`) and never point it at the live DB.
5. **Go is not on PATH:** `export PATH="$HOME/.local/opt/go/bin:$PATH"` first, every session.

# THE SYSTEM (context you need)

- **Repo:** `~/claude code/signaldeck` — private GitHub remote `nyaungnicholas-wq/signaldeck`.
  - `daemon/` — Go. ~76 workers, ~85 packages, ~1,524 tests. SQLite (WAL) at `data/signaldeck.db`
    (~5.5 GB, 73 tables). JSON API on `127.0.0.1:8322`.
  - `web/` — Next.js 16 on `127.0.0.1:8323`, behind a login. Proxies `/api/*` → daemon, so the user
    only ever opens **one** URL (`:8323`).
- **Services (launchd):** `com.signaldeck.daemon`, `com.signaldeck.web`, `com.tickstream.daemon`,
  `com.signaldeck.tunnel`. Plists in `ops/`, logs in `logs/`.
- **What it does:** ingests ~19 **free** data sources (Alpaca bars, tickstream crypto order books,
  TradingView scanner + inbound webhooks, SEC EDGAR, FINRA, FRED, CFTC, CBOE, StockTwits, Wikipedia,
  news…), computes signals, blends a **gated ensemble** into a calibrated P(up), forces a 1–10
  cross-sectional SignalScore, and grades itself daily.
- **Core doctrine:** *a signal influences a prediction only if it has measured out-of-sample lift > 0.*
  Every claim is supposed to carry its gate, its N, and its caveat. **Your job is to check that the
  doctrine is enforced in code, not just stated in comments.**

## The honesty gates (verified against source 2026-07-16 — confirm each is ENFORCED, not just declared)

| Constant | Value | Where | Meaning |
|---|---|---|---|
| `featureVersion` | **10** | `pipeline/predict.go:111` | bump resets per-symbol GBM training |
| `MinCalibrationPairs` | 30 | `ensemble/ensemble.go:52` | below this, no calibration map is fit |
| `MinCurveN` | 30 | `composite/composite.go:46` | below this, no 1–10 SignalScore is emitted at all |
| `MinCellSamples` | 30 | `adaptive/adaptive.go:44` | per-leg, per-regime samples before a learned weight — ⚠️ **counts raw rows, not independent obs; see Phase 9 defect #1** |
| `minIndependentN` | 30 | `api/api.go:866` | independent obs before IC is reported |
| `trackMinIndependentN` | 30 | `api/trackrecord.go:50` | independent obs before track-record stats |
| `MinPersonal` | 40 | `symbolagent/symbolagent.go:56` | own resolved outcomes before `personal` tier |
| `MinPatternN` | 15 | `candles/edge.go:28` | candlestick pattern hit-rate withheld below this |
| `MinAlertOutcomeN` | 20 | `store/alertstats.go:21` | alert forward-return stats withheld below this |
| `MinTrainRows` / `MinTestRows` | 1000 / 200 | `alphax/alphax.go:75-76` | alphax refuses to grade below this |
| `EmbargoDays` | 2 (horizon-aware) | `alphax/alphax.go:72` | purged walk-forward embargo |
| `maxModelForecastAgeSecs` | 3 days | `pipeline/gbmtrain.go:218` | stale model scores must stop blending |
| `minNewWeeks` | 8 | `pipeline/researchledger.go:75` | independent market-weeks before a ledger grade |
| `minHighVolObs` | 30 | `pipeline/researchledger.go:83` | high-vol obs before the regime gate opens |
| `DiscoveryMaxBF` | 5.0 | `researchledger/researchledger.go:111` | in-sample discovery evidence cap |

**Try to bypass each one.** Can a leg with n=3 lucky samples take a learned weight? (It could, once —
that shipped.) Can a thin cross-section emit a 10? Can a 4-day-old `model_forecasts` row still blend?
And critically: **are gated numbers actually withheld (null + reason), or silently rendered as 0/50%?**

---

# PHASE 0 — ORIENT (5 min, do not skip)

```bash
export PATH="$HOME/.local/opt/go/bin:$PATH"
cd ~/claude\ code/signaldeck
git log --oneline -1 && git status --porcelain | wc -l    # expect head ba1c57d + ~75 uncommitted
curl -s localhost:8322/api/health                          # {"alpaca":true,...}
```

Read `README.md` and skim `AUDIT_SUPERPROMPT.md`'s system table. **Do not read every wave doc** —
they are historical and will eat your context. Establish: is the daemon up, is the tree dirty, and
**does the running binary predate the working tree?** (If the tree has changes newer than
`bin/signaldeckd`'s mtime, then *what you read in source is not what is running* — this exact trap
has burned this project before. Note it and account for it in every later phase.)

---

# PHASE 1 — BUILD & TEST GATE

```bash
cd daemon && go build ./... && go vet ./... && go test ./... 2>&1 | tail -30
cd ../web && npx tsc --noEmit && npm run lint
```

Report the **exact** pass/fail count and package count. Name any failing package.
**Expected baseline: ~1,524 tests / 85 packages green.** A drop in count is as suspicious as a
failure — deleted tests hide regressions.

Known pre-existing failure — **do not report as a regression**: `TestNewsTrendsAPI` fails when run
after 12:00 UTC (wall-clock test bug, already filed). Everything else failing is new and yours.

---

# PHASE 2 — SERVICES & LIVENESS

1. `launchctl list | grep -E 'signaldeck|tickstream'` — all loaded? Any nonzero exit status?
2. Ports 8322 (daemon), 8323 (web), 8321 (tickstream), tunnel — all answering?
3. `logs/` — any panic, any fatal, any stack trace? (`grep -ciE 'panic|fatal' logs/*.log`)
4. Log sizes: is anything growing unbounded? (Known: `tickstream.out.log` ~110 MB, no rotation —
   already flagged, don't re-report unless it has grown materially.)
5. `curl -s localhost:8322/api/health` and `/api/source-health` and `/api/notify-status`.

---

# PHASE 3 — DATA INGEST (every source)

`curl -s localhost:8322/api/source-health` exists precisely to answer "is anything quietly dead?"

🚨 **BUT IT DOES NOT COVER EVERYTHING — DO NOT TREAT IT AS AUTHORITATIVE.** Verified 2026-07-16: the
`srchealth` registry tracks **only ~12 source keys while the system ingests from ~28 external
endpoints.** These live-ingesting sources have **no source-health entry at all** and are therefore
invisible to both the dashboard and `source_stale` alerting:

```
macro_series (FRED)      fundamentals (EDGAR companyfacts)   companies (EDGAR directory + bulk)
filings                  insider_trades                       inst_holdings (13F)
short_volume (FINRA)     congress_trades                      hud_summary (trader-hud)
the broad daily-only universe bars (only stream=1 hot-set 1m bars are checked, via bars_1m_hot)
```

**Any of those could die silently and nothing would report it.** Indeed `congress_trades` is dead with
**0 rows** and the source-health dashboard does not mention it. **Check these ~10 blind-spot sources
by hand** — query their tables directly for freshness. Then decide whether "extend the srchealth
registry to cover all ingest paths" belongs at the top of your fix list.

The **exact 12 tracked keys** (confirmed live 2026-07-16 — re-derive, don't trust this list):
```
tv_quotes  tv_ratings  tv_signals  short_interest  crypto_perp  cot_reports
stocktwits_sentiment  wiki_views  cboe_pc  news  bars_1m_hot  snapshots_1s
```

For **each** source — the 12 tracked **and the ~10 untracked blind spots above**:

- Is it fresh, or stale? **Distinguish "stale" from an honest "market closed"** — the checker is
  market-calendar-aware (`internal/marketcal`); a stock source is *supposed* to be quiet at 3am.
- **Then go past the freshness check**: pull an actual recent row and ask *is this value real?*
  A scraper that drifts into parsing the wrong column stays "fresh" forever while emitting garbage.
  Sanity-check a handful against reality (does BTC's price look like BTC's price? is SPY's volume
  plausible? is a Form 4 actually a Form 4?). **Non-null is not correct.**

Known-degraded, **do not report as new**:
- **Congress**: both free mirrors (senatestockwatcher / housestockwatcher) died 2026-07. The system
  honestly degrades and logs `congress_mirror_error`. Env override exists. This is known and accepted.
- `fundamentals_error` (~46/day) and `cboe_pc_error` (~7/day) — EDGAR/CBOE flakiness, self-recovering.
- `stale` dq noise (~670/day) from illiquid `stream=0` IEX names — honest but noisy; it correctly
  stops at market close, which proves the marketcal gate works.

Report: per source → ✅/⚠️/❌/❓ + the freshest timestamp + whether a spot-checked value is sane.

---

# PHASE 4 — WORKERS (all 76)

Ground truth is the DB, not the code:

```sql
-- every worker, its last run, and its status
SELECT worker, status, datetime(MAX(started_at),'unixepoch') last, count(*) runs
FROM worker_runs WHERE started_at > strftime('%s','now')-604800
GROUP BY worker ORDER BY last;

-- anything whose LATEST run errored
SELECT worker, status, datetime(MAX(started_at),'unixepoch') FROM worker_runs
GROUP BY worker HAVING status='error';
```

For each worker: **is its latest run `ok`, did it run within its interval, and — the real question —
did it WRITE anything?** A worker returning `ok` while writing zero rows is the #1 silent failure in
this system. Cross-check each worker against the table it owns: has that table grown in the worker's
last few cycles?

⚠️ `worker_runs` prunes to the newest ~20 rows per worker — a high-frequency worker's history covers
only a short window. Do not read "only 20 runs" as "it barely ran."

Expected cadences to spot-check: `signal-runner` 1m, `prediction-runner` 10m, `outcome-resolver` 10m,
`prediction-resolver`, `alert-runner` 5m, `dq-auditor` 5m, `hud-sync` 1m, `crypto-live` 1Hz,
`gbm-trainer` / `alpha-trainer` / `pressure-trainer` / `adaptive-weights` (hourly–6h),
`research-engine` / `research-ledger` / `hist-backfill` (once per UTC day), `db-backup` nightly.

Known watchdog noise: `worker_stale` events cluster around daemon restarts (the fleet runner fires
everything at boot). Only care if a worker is stale **without** a nearby restart.

---

# PHASE 5 — STORAGE & DATA INTEGRITY

1. **Size + growth**: `du -sh data/signaldeck.db data/archive` (baseline ~5.5 GB DB). Disk free?
2. **Tiered retention** — verify the policy is actually running, not just configured: snapshots_1s 6h,
   1m bars 60d → compact to 1h, 1h bars 3y → compact to 1d, **daily forever**.
   `PruneBars` must refuse `tf=1d` **in code** — verify that guard exists and is tested.
3. **Archive-before-prune fail-safe**: the contract is *archive error ⇒ prune SKIPPED* (nothing is
   ever lost), emitting a dq `archive_skip`. **Verify the failure path, not the happy path** — read
   the code and confirm an archive error cannot fall through to a prune.
4. **Row-count sanity** across the 73 tables: which are growing, which are frozen? A frozen table
   whose worker reports `ok` is a finding.
5. **Backups**: when did the last nightly `VACUUM INTO` and the **off-machine** (iCloud) copy succeed?
   Check freshness *and* that keep-7 rotation actually holds 7 distinct nights. If the newest offsite
   backup is old, **the whole project is one disk failure from zero** — that outranks any model bug.
6. **Ledger integrity**: `curl -s localhost:8322/api/ledger/verify` — is the hash chain intact?
   If broken, **no historical claim is trustworthy** and you should say so loudly.

---

# PHASE 6 — API SURFACE (all 121 endpoints)

**There are 121 registered routes (104 GET / 17 POST)** — enumerate them from source, don't trust this
number or any older note claiming "~55" (that was a tested subset). Routes use Go 1.22 method+path
patterns, so a naive grep for `"/api/…"` **misses every one of them**:

```bash
cd daemon && grep -rhoE 'HandleFunc\("(GET|POST) /api/[^"]*"' --include='*.go' internal/api/ \
  | grep -v _test | sed -E 's/HandleFunc\("//; s/"$//' | sort -u
```

Sweep all of them (the full list is in the Appendix). A minimal loop:

```bash
for p in $(grep -rhoE 'HandleFunc\("GET /api/[^"]*"' --include='*.go' daemon/internal/api/ \
    | grep -v _test | sed -E 's/HandleFunc\("GET //; s/"$//' | sort -u); do
  printf '%-40s %s\n' "$p" "$(curl -s -o /dev/null -w '%{http_code} %{time_total}s' "localhost:8322$p")"
done
```

Hit **every** endpoint, both direct (`:8322`) and through the web proxy (`:8323`). For each:

- HTTP status (expect 200, or an honest 401/403 on the auth-gated ones)
- **Is the payload real, or structurally-valid emptiness?** `{"items":[]}` with a 200 is the classic
  silent failure. An empty array is only acceptable if the system *says why*.
- Latency (the dashboard roundup should be ~15–50 ms warm; ~5 s cold is known).
- **Gated fields must be `null` + a reason string — never `0` and never `50%`.** Grep the payloads
  for numbers that should be nulls.

Then verify the **security contract** still holds (it was a real hole once). `d.secure(mux)`
(`api/security.go`) wraps every route in seven ordered steps: **Host allowlist** (anti-DNS-rebinding;
an *empty* allowlist denies everything) → **Origin allowlist + CORS echo** (no wildcard; OPTIONS
terminates here and never reaches the mux) → **identity** (session cookie, or `Authorization: Bearer`
compared by `tokenEqual`, which SHA-256s both sides to fixed length *first* so neither content nor
**length** leaks via timing) → **CSRF** (`X-Signaldeck` on non-GET) → rate limits → 128 KB body cap.

Prove it with curl: an attacker-shaped POST, a bad Origin, and a bad Host must **all 403**.

**Auth posture — check this deliberately, the defaults are permissive:**
`SIGNALDECK_PUBLIC_READS` **defaults to `true`**. Of the 121 routes: **16 always require auth**,
**6 never do** (`/api/health`, `/api/auth/*`, `/api/tv-webhook`), and **99 are conditional** — i.e.
**anonymous-readable by default**. That's fine on loopback; it is *not* fine if anything is exposed.
Since a public tunnel exists, confirm what it actually fronts.

Four exact quirks worth checking (traced from source 2026-07-16 — verify each, then judge):
1. `GET /api/auth/me` is exempt at the middleware but its **handler** 401s when anonymous.
2. **`GET /api/alert-outcomes` does not match the always-auth prefix `/api/alerts`** — the strings
   diverge at the 11th char (`/api/alert-` vs `/api/alerts`). It sits beside the two real alerts routes
   but is a **conditional public read**. Near-miss prefix matching is a classic silent auth hole —
   decide whether this is intended.
3. `POST /api/backtest` and `POST /api/risk` are the only POSTs that are neither always-auth nor
   never-auth: **anonymous-writable whenever PublicReads=true** (still behind the CSRF header).
4. **`GET /api/ai/analyst` spends LLM budget** and is billed at the write rate tier, yet is **not** in
   the always-auth list (only `ai/chat`, `ai/filing`, `ai/debate` are) — so by default it is an
   **anonymous spend-incurring endpoint**, which contradicts the doctrine stated in `requiresAuth`'s
   own doc comment. Confirm and rank it.

Known: `/api/research-graph` now exists (a stale note claimed it didn't) — confirm.

---

# PHASE 7 — WEB (all 45 routes)

Use the browser tools; don't just curl HTML. Across the 6 hubs (DASHBOARD / MARKETS / SIGNALS /
INTEL / LAB / plus `/live`, `/s/[market]/[symbol]`, `/login`):

1. Every route 200s and renders — no error boundary, no infinite skeleton.
2. **Zero console errors.** (A null-guard crash — `Cannot read 'length' of null` — has shipped here
   before, on exactly the honest-empty-state path.)
3. **The honest-empty state renders as honest**, not as a fake zero. Find a symbol with no model yet
   and confirm it says "still learning (n/40)" rather than showing a confident number.
4. Old URLs still redirect (29 query-preserving 307s in `next.config`).
5. Mobile/responsive + reading-mode toggle don't break layout.
6. Login works; `AuthGate` actually gates.

Report per-route ✅/⚠️/❌/❓.

---

# PHASE 8 — THE PREDICTION CHAIN (trace one, end to end) ⭐

**This is the heart of the sweep. Do it properly — it is worth more than Phases 1–7 combined.**

Pick **one live, liquid symbol** (e.g. `BTC/USD` for crypto, `NVDA` or `SPY` for stocks) and follow a
single prediction from raw bars to stored row. At every step, **compare what the code says should
happen to what the DB actually contains.**

1. **Feature vector** — `buildFeatureVector` in `internal/pipeline/predict.go`. Current
   `featureVersion` is **10** (`predict.go:111` — verify it; it bumps often). ⚠️ **A version bump
   resets per-symbol GBM training**, because labeled sets are version-pinned. So ask: *how many symbols
   currently have enough rows at the CURRENT version to train?* If ~none, **the per-symbol models are
   silently inert** while still reporting `ok`. This is a live, recurring risk — check it every sweep.
   Pull the actual stored `features` row for your symbol:
   - Which keys are present, which are absent, and is each absence *honest* (uncomputable) rather
     than a silent zero? (Absent ≠ zero — that's what the `K__has` presence indicators are for.)
   - **Self-reference check**: `pred_raw`, `pred_cal`, `gbm_prob`, `meanrev_prob`, `alphax_prob` — and
     **their `__has` variants** — must be excluded from training inputs. Read the exclusion list and
     confirm no model trains on its own output.
2. **Legs** — for your symbol, establish **what each leg's gate decided and why**. The gates are
   **not uniform**, and that asymmetry is the most misunderstood part of the system. Verified against
   `internal/ensemble/ensemble.go` on 2026-07-16:

   | Leg | Lift gate | Staleness cap | Notes |
   |---|---|---|---|
   | `pressure` | **opt-out**: `nil` lift KEEPS it; measured `<=0` DROPS it | lift only | prob is **live** from `sc.Score`, not from `model_forecasts` |
   | `expectancy` | **NONE** — blends whenever a state-key row matches | none | gated only by state-key lookup |
   | `forecast` | `lift > 0` | **NONE** ⚠️ | trainer refuses below 150 labeled samples |
   | `sentiment` | **NONE** | ≤3 days | needs ≥3 rated headlines; scale 0.15 |
   | `gbm` | `lift > 0` | 3 days | per-symbol, **version-pinned** to featureVersion |
   | `meanrev` | `lift > 0` | 3 days | graded **cost-net** at 10bps vs best constant strategy |
   | `alphax` | `lift > 0` **twice** (trainer deletes rows on a gated regrade, then ensemble re-checks) | 3 days | prob is **relative** (P(beat universe median)), blended as a directional tilt |

   ⚠️ **The stated doctrine — "a signal blends only if measured OOS lift > 0" — is enforced on the
   model legs (forecast/gbm/meanrev/alphax) but NOT on expectancy or sentiment**, which blend
   unconditionally. That may be deliberate, but **verify it is intentional and documented**, because
   the README and comments state the doctrine universally. An ungated leg is exactly where an unmeasured
   drag hides — pressure was measured **anti-predictive** only after someone finally graded it.
   - **Pressure's asymmetry is deliberate — do not "fix" it.** Unmeasured ⇒ keep; measured-bad ⇒ bench.
   - Stale `model_forecasts` (> 3 days, `maxModelForecastAgeSecs`) must stop blending for gbm/meanrev/
     alphax. **Verify a stale row actually gets excluded** — this silently failed once. **Note the
     forecast leg has no such cap** — check whether a stale forecast can blend indefinitely.
3. **Blend & tier** — which tier served this prediction: `personal` (needs ≥40 own resolved outcomes
   *and* real edge) → `global-regime` → `global` → `static`? Confirm the promotion threshold is
   enforced, not just declared.
4. **Calibration** — is it **prequential** (fit only on pairs resolved *before* the point being
   graded)? `MinCalibrationPairs = 30` below which no map is fit. Check the certainty bound is applied
   **per PAV block by its own weight**, not by total pair count (the latter is useless at n=3000):
   ```sql
   SELECT MIN(cal_prob), MAX(cal_prob) FROM predictions WHERE ts > strftime('%s','now')-86400;
   ```
   **Anything ≥ 0.99 or ≤ 0.01 is a red flag** — a thin isotonic knot claiming certainty.
5. **THE RECONCILIATION — the single highest-value check in this document.**
   Take the stored `ensemble.Components` for one prediction and **recompute the blend by hand**.
   Does the stored `pred_raw` actually equal what those components and weights produce? Then apply the
   calibration map — does it produce the stored `cal_prob`?
   **If you cannot reproduce the stored number from the stored inputs, that is a BROKEN finding and
   the most important thing in your report.** A prediction you cannot reproduce is a prediction nobody
   can audit.
6. **Resolution** — `outcome-resolver` / `prediction-resolver`. The forward window must anchor to the
   **actual base bar**, not assumed 00:00 UTC (US daily bars open ~05:00 UTC = midnight ET; this bug
   once recorded 2-day returns as 1d). Verify per horizon (1h/1d/1w). 1h walks must skip >3h spans so
   overnight gaps aren't counted as 1h returns.
7. **Ledger** — the hash-chained `prediction_ledger` entry exists for your prediction and verifies.

---

# PHASE 9 — HOW ACCURATE IS IT, REALLY? ⭐⭐

**Do not report the API's numbers. Recompute them yourself from the DB, read-only.** The endpoints are
a hypothesis about accuracy; your SQL is the test.

## The one thing that will fool you

```sql
SELECT horizon, count(*) total, sum(resolved_at IS NOT NULL) resolved,
       count(DISTINCT date(ts,'unixepoch')) distinct_days
FROM prediction_outcomes GROUP BY horizon;
```

As of 2026-07-16 this returns **1d: 79,424 resolved across 14 distinct UTC days** (~1,000 symbols ×
144 intraday rows/day). **Those are not 79,424 independent bets. They are ~14 market days.**

The prediction runner writes a row every ~10 min per hot symbol, and the resolver gives every row of
one `(symbol, UTC-day)` the **same** forward return. This exact bug once inflated a reported N by
**40×** and turned a nothing into a headline. It is the single most recurring defect in this project's
history — assume it has come back somewhere.

**The independence rules, in order of severity:**
1. **Dedupe to one observation per `(symbol, UTC-day)`.** Non-negotiable for any pooled grade.
2. **Then ask how many independent DAYS exist** — 500 symbols on one day are *not* 500 independent
   bets; they share market beta. Cross-sectional clustering is real clustering.
3. **For any weekly claim, the unit is the calendar WEEK.** The entire live 1w resolved history spans
   ~2 market weeks. Any 1w claim over "n=1,006" is ~150 symbols × 2 weeks wearing a costume.
   (This is precisely why the research ledger's week-trial grader **refuses** to grade 1w — that
   refusal is the system working.)

## What to compute (yourself, from the DB)

For each horizon, over **deduped independent observations**:
- **Directional accuracy vs the right baseline.** The baseline is **not 50%** — it is the realized
  naive base rate over the same window (the market drifts; a naive always-long can be 51–55%).
  Beating 50% while losing to always-long is not skill.
- **Wilson 95% CI.** ⚠️ **If the CI includes the baseline, there is no measured edge. Say exactly
  that, in those words.**
- **Brier + Brier skill** vs the base-rate constant.
- **IC** (Pearson of `prob − 0.5` vs forward return) + Fisher-z CI.
- **Calibration**: bin the predictions — when it says 70%, is it right ~70% of the time? A model with
  good IC and bad calibration is still lying about its confidence.
- **Net of costs**: check `/api/paper` (simulated book, real per-side costs) **and its turnover**.
  High turnover eats a small edge alive. A raw P(up) > 0.5 is not tradeable edge.
- **vs SPY buy-and-hold, risk-adjusted, over the same window.** Beating a coin flip ≠ beating the
  market.
- **Multiple-testing**: this project has tried 7+ ensemble legs, 8 classic strategies, 23 candlestick
  patterns, 48 auto-discovery rules, and 8 feature versions. **The best-looking of 30 things is the
  expected output of pure noise.** Demand a correspondingly higher bar.

## 🎯 START HERE — suspected live defects (traced to source 2026-07-16)

The pseudo-replication fix was applied to **some** paths and not others. `alphax.BuildDataset`,
`/api/honesty` (`dedupeIndependent`), `/api/track-record`, `/api/composite` and `/api/confluence/track`
**do** dedupe to one obs per `(symbol, UTC-day)`. These four apparently **do not** — each was traced to
its actual SQL. **Confirm or refute each before anything else; they are ranked by blast radius.**

1. **`/api/adaptive` — the learned ensemble weights (highest blast radius).**
   `AdaptiveWeightsWorker.Run` (`pipeline/adaptiveweights.go:37-51`) feeds every row from
   `store.LabeledFeatures` straight into `adaptive.Example`. That query
   (`store/features.go`) is a plain `JOIN … ORDER BY f.ts DESC LIMIT ?` — **no dedupe**. So
   `MinCellSamples = 30` counts **rows, not independent observations**, and per-leg hit rates are
   **row-frequency-weighted** — exactly the bug named and fixed in alphax ("the universe median was
   row-frequency-weighted"). At ~144 rows/symbol/day, **a "30-sample" cell can be one symbol on one
   day.** If so, the H3 fix — *"a leg with n=3 lucky samples could take 0.77 weight; per-leg
   MinCellSamples≥30 now gates EVERY leg"* — **is undermined by counting the wrong unit**, and these
   weights feed live blending. **Verify first.**
2. **`/api/calibration` — no gate at all.** `store.ResolvedPredictionPairs` is
   `SELECT prob, up FROM prediction_outcomes WHERE resolved_at IS NOT NULL AND horizon=? ORDER BY ts
   DESC LIMIT 10000` — **raw rows, no dedupe, no independent-N accounting, and no threshold anywhere
   in the handler.** With ~79k resolved rows over 14 days, a 10,000-row window is **~1–2 days of
   market treated as 10,000 independent points.** Every other skill surface gates at 30; this one
   never gates.
3. **`/api/predictions/latest` and `/api/dashboard` — an inert gate.** Both compute
   `resolvedN := store.ResolvedPredictionCount(...)` = `SELECT COUNT(*)` **raw**, then gate on
   `resolvedN < 30`. At 79,424 rows the gate **can never fire** — it ungates immediately and forever.
   A gate on the wrong unit is worse than no gate: it *looks* like a safeguard.
4. **Gated-state leaks — check what a withheld number actually renders as.** Reportedly:
   `/api/composite` emits `winRate = 0` (a bare float, not null) when gated; `/api/recommendation/top`
   interpolates a "0.0%" into its note unconditionally (`desk.go:344-345`); `/api/signal-backtest`
   emits misleading numbers + a flag rather than nulls. **A gated 0 that renders as "0.0% accurate" is
   a false claim, not a withheld one.**

**Also worth a judgment call — the labels are inverted.** `/api/calibration`, `/api/honesty` and
`/api/signal-backtest` read resolved `prediction_outcomes` (prob frozen at prediction time = genuinely
live-forward) yet hardcode `live:false, "backtested — not live"`, while `/api/track-record` reads the
**same table** and labels it `live:true`. The mislabel errs conservative, so it is not a lie — but the
labels and the data disagree, and a user cannot tell which surfaces are real forward evidence.

## Then check the system's own self-criticism

- `curl -s localhost:8322/api/self-audit` — read **every** finding. **The system may already be telling
  you it's broken.** Take it at its word; do not explain it away.
  `SelfAuditor.Run` (`pipeline/selfaudit.go`) emits exactly **6 statuses**, of which **4 are alarms**:

  | Status | Meaning | Alarm? |
  |---|---|---|
  | `ok` | within tolerance | no |
  | `insufficient` | below `selfAuditMinN=30` — emitted as **value 0 + a reason**, never an alarm | no |
  | `degrading` | calibration reliability worsened vs last audit by > `calibrationDriftThreshold=0.02` | **yes** |
  | `over_confident` | `mean(cal_prob) − baseRate > biasThreshold=0.05` | **yes** |
  | `under_confident` | bias < −0.05 | **yes** |
  | `sign_flip` | IC flipped sign with **both** \|prior\| and \|cur\| ≥ `icSignEpsilon=0.02` (jitter across zero is correctly *not* a flip) | **yes** |

  Metric keys: `calibration:{1d,1w}`, `prediction_bias:{1d,1w}`, and `factor_ic:<leg>` for each of the
  7 legs — 11 rows/day at full coverage.
  ⚠️ **`factor_ic:*` rides the adaptive attribution, whose N is not deduped** (see defect #1 above) —
  so treat factor-IC values as suspect until that is resolved. A `sign_flip` on a pseudo-replicated IC
  may be noise about noise.
- `curl -s localhost:8322/api/research-ledger` — the Bayesian ledger's posteriors and its
  `insufficient independent market-weeks` refusals.

## The known-honest baseline (2026-07-16) — use as a regression reference

If your recomputation differs **materially** from these, something changed — find out what and say so.
These are the last independently-verified honest values:

| Claim | Honest value | State |
|---|---|---|
| alphax 1d official grade | lift **+0.0188**, acc **51.9%** vs base 50.0%, **AUC 0.489**, N=1,012 unique outcomes | ⚠️ weak, threshold-sensitive, AUC < 0.5 |
| Pressure leg (fixed-weight) | **anti-predictive**: −3.6pp @1d, −14.8pp @1w — now gated OFF by `pressure-trainer` | ✅ gate working as designed |
| H002 weekly pressure-inverse | posterior **0.020 — REJECTED** (2026 discovery window was its best era = lucky-window signature) | ✅ engine falsified its own discovery |
| H008 RSI-extension reversion | posterior **0.020 — REJECTED** | ✅ |
| H002-R1 pressure × extension | posterior **0.020 — REJECTED** (no incremental value in 4/5 eras) | ✅ |
| Auto-discovery grid | **0 survivors** from 48 rules over 340 market weeks (Bonferroni + survival + CF + fragility) | ✅ |
| H005 / H006 | 0.97 / 0.75 **tentative** — the only live frontier | ⚠️ need graders |
| Research corpus | `research_weeks` = 286,348 rows / 959 symbols / **340 independent market weeks** (2019→) | ✅ |
| Live 1w resolved history | **2 calendar weeks** → ledger graders correctly refuse (need 8) | ✅ honest refusal |

**The honest one-line summary as of 2026-07-16: SignalDeck has no demonstrated live directional edge.**
Its headline discoveries were killed by its own research engine, its pressure leg was measured
anti-predictive and benched, and its one surviving positive (alphax, +1.9pp) has AUC < 0.5 on ~14
independent days. **If your sweep concludes something rosier, you have almost certainly recounted a
pseudo-replicated N. Go back to the top of this phase.**

---

# PHASE 10 — THE REST OF THE FLEET

- **AI layer**: `/api/ai/status`, `/api/ai/analyst`, `/api/ai/chat`, `/api/ai/filing`, `/api/ai/debate`.
  Default model is `meta/llama-3.1-8b-instruct` (**70b times out on this key** — known). Is the daily
  spend cap enforced and persisted? Are keys redacted in every log path? Are all 4 agents still
  **read-only** — can any of them mutate state or place a trade? (They must not. Verify, don't assume.)
  Known: `ai-analyst` times out ~1×/day and self-recovers.
- **Alerts & delivery**: `/api/alerts`, `/api/notify-status`. Dedup (`INSERT OR IGNORE`) holding?
  Cooldowns respected? Remote transports (Discord/Telegram/webhook) failing *safely* into
  `notify_failed` dq events without ever blocking the fleet?
- **Research engine**: `research-engine`, `research-ledger`, `hist-backfill` — once/UTC-day each.
  Is the leakage sentinel live? Do empty states avoid burning the day cursor?
  Known follow-up: **AD-\* auto-discovered hypotheses can never accrue post-discovery evidence** (the
  live grader has no spec registry). Confirm still true; don't re-derive it from scratch.
- **Paper book**: `/api/paper` — positions, equity curve, turnover. **It must never place a real
  order.** Verify that in code.
- **Backtest / strategy lab**: `/api/signal-backtest`, `/api/strategy-lab`. These are **in-sample
  hypotheses, not evidence** — confirm the UI labels them as such ("backtested — not live") and that
  CAGR is suppressed when `SpanYears < 0.9` or trades < 20.

---

# PHASE 11 — VERDICT

## 1. The headline (one plain sentence, first, no hedging)

Answer the actual question — *is everything working, and how accurate is the prediction?* — in one
sentence a non-quant can act on. For example:

> "Everything is running (76/76 workers, 55/55 endpoints, 45/45 pages) except X and Y; the prediction
> pipeline reconciles end-to-end; and its measured 1d accuracy is 51.9% vs a 50.0% baseline on 14
> independent days — statistically indistinguishable from luck, so not something to trade real money on."

**Forbidden**: "looks good", "mostly working", "promising", "trending positive" — without the numbers
and the gate state attached. Vagueness is the one unforgivable failure of this sweep.

## 2. Scoreboard

| Layer | State | The one number that decides it |
|---|---|---|
| Build & tests | | |
| Services | | |
| Data sources (~19) | | |
| Workers (~76) | | |
| Storage & backups | | |
| API (~55) | | |
| Web (~45) | | |
| Prediction chain | | ← did it reconcile? |
| **Accuracy** | | ← **independent N and DAYS, not rows** |
| AI / alerts / research | | |

## 3. Then

- **What is BROKEN** — each with evidence and a reproduction.
- **What is DEGRADED** — and whether the system honestly admits it (admitted ⚠️ ≫ silent ⚠️).
- **What I verified as genuinely WORKING** — with *how* you verified it, not just the claim.
- **What I could not verify** — and why. Be explicit; ❓ is an honest answer, "fine" is not.
- **Ranked fix list** — each item: severity, evidence, one-line fix, and **what it costs to ignore**.
  Anything requiring a deploy: **write it down, do not do it.**

---

# KNOWN ISSUES — DO NOT REPORT THESE AS NEW FINDINGS

Spend your effort on what's *new*. These are already known and accepted as of 2026-07-16:

1. `TestNewsTrendsAPI` fails after 12:00 UTC — wall-clock test bug, filed.
2. `tickstream.out.log` ~110 MB, unbounded (launchd stdout, no rotation).
3. `stale` dq noise ~670/day from illiquid `stream=0` IEX names — honest but buries signal.
4. `ai-analyst` LLM timeout ~1×/day, self-recovering. Occasional one-off `sentiment-tagger` 400.
5. Congress mirrors dead since 2026-07 (both free sources gone); env override ready.
6. `fundamentals_error` / `cboe_pc_error` — upstream flakiness, self-recovering.
7. Working tree has ~75 uncommitted files; git head `ba1c57d`. **This is the user's choice.**
8. `worker_stale` events cluster around daemon restarts (fleet fires all workers at boot).
9. No demonstrated live edge — see the Phase 9 baseline. **That is the honest state, not a bug.**
10. 70b LLM models time out on the NVIDIA key; 8b is the deliberate default.

---

# OUTPUT FORMAT

1. **Headline verdict** (one plain sentence — is it working, and how accurate is it)
2. **Scoreboard table** (Phase 11)
3. **BROKEN** (with reproductions)
4. **DEGRADED** (and whether admitted honestly)
5. **Verified WORKING** (with the verification, not the claim)
6. **The accuracy assessment** (your recomputed numbers vs the 2026-07-16 baseline)
7. **Ranked fix list**
8. **Could not verify** (and why)

Be concise, quantitative, and blunt. The builder of this system explicitly values being told it is
broken over being told it is impressive. **A sweep that finds nothing is a sweep that did not look.**

---

# APPENDIX — VERIFIED COMPONENT INVENTORY (as of 2026-07-16)

Counts confirmed directly against source/DB on 2026-07-16. **Re-derive them; if a count has drifted,
that itself is a finding** (a vanished worker or route is exactly the silent failure this sweep hunts).

## The 76 live workers (`SELECT DISTINCT worker FROM worker_runs`)

```
13f-poller             adaptive-weights       ai-analyst             ai-watcher
alert-runner           alpha-trainer          anomaly-scanner        backfill-reconciler
backfiller             breakout-runner        cboe-pc                companies-sync
composite-scorer       composite-scorer-1w    confluence-resolver    confluence-scorer
congress-poller        cot-poller             crypto-bars            crypto-live
crypto-perp            daily-briefing         db-backup              derived-retention
downsampler            dq-auditor             edgar-fetcher          expectancy-runner
filings-poller         finra-shortint         finra-shorts           forecast-trainer
fred-poller            gbm-trainer            hist-backfill          hud-sync
insight-writer         news-fetcher           news-trends            outcome-resolver
paper-trader           pattern-stats          per-symbol-learner     postmortem-runner
prediction-resolver    prediction-runner      pressure-trainer       ranking-runner
regime-runner          research-engine        research-lab           research-ledger
scores-compactor       sector-rotator         self-audit             sentiment-aggregator
sentiment-tagger       sic-bulk-sync          signal-runner          signalbt-weekly
smart-money-scorer     source-audit           stock-bars             stock-streamer
stocktwits-fetcher     storage-governor       strategy-lab           tape-seeder
tv-quotes              tv-rating              universe-discovery     universe-live
universe-poller        watchdog               weekly-report          wiki-attention
```

Registered across ~29 grouped `*Workers()` funcs in `cmd/signaldeckd/run.go` (`learningWorkers`,
`edgeModelWorkers`, `storageWorkers`, `signal8Workers`, `anomalyWorkers`, …).

## The 121 API routes

**POST (17)** — all require the `X-Signaldeck` header; most require auth:
```
/api/ai/chat  /api/ai/debate  /api/ai/filing  /api/alerts/seen  /api/auth/login
/api/auth/logout  /api/auth/register  /api/backtest  /api/candidates/add
/api/candidates/dismiss  /api/candidates/monitor-all  /api/portfolio/add
/api/portfolio/close  /api/risk  /api/subscribe  /api/tv-webhook  /api/unsubscribe
```
⚠️ `/api/tv-webhook` is the **one deliberate CSRF-header exception** — TradingView's servers cannot
send a cookie or the `X-Signaldeck` header. It is instead authenticated by a shared secret
(`SIGNALDECK_TV_WEBHOOK_SECRET`) in the JSON body or query, **compared in constant time**, and
**fails closed** (no secret configured ⇒ endpoint disabled, so it can never accept anonymous writes).
This is the system's largest inbound attack surface and it is publicly reachable through the tunnel.
**Verify all three properties still hold** — constant-time compare, fail-closed, and that a wrong
secret is rejected — with a live curl. Do not take the comment's word for it.

**GET (104)** — enumerate with the grep in Phase 6. The accuracy-critical ones:
```
/api/track-record  /api/honesty  /api/calibration  /api/alphax  /api/self-audit
/api/ledger/verify  /api/paper  /api/signal-backtest  /api/strategy-lab  /api/adaptive
/api/model-evolution  /api/model-forecasts  /api/research-ledger  /api/research-graph
/api/predictions  /api/predictions/latest  /api/attribution  /api/postmortems
```

## The 73 tables

```
alerts alphax_models anomalies bars breakouts candidates cboe_pc companies composite_scores
confluence_events confluence_outcomes confluence_setups congress_trades cot_reports crypto_perp
dilution_flags dq_events expectancy features filings forecasts fundamentals hud_summary
insider_trades insights inst_holdings macro_series meta model_forecasts news news_trends
paper_cursor paper_equity paper_positions paper_trades pattern_stats positions prediction_ledger
prediction_outcomes prediction_postmortems predictions rankings recommendation_audit regime_changes
regime_state research_hypotheses research_ledger_evidence research_ledger_hypotheses research_weeks
score_outcomes scores self_audit sentiment_daily sessions short_interest short_volume
smart_money_events smart_money_scores snapshots_1s stocktwits_sentiment strategy_results
symbol_models symbols tv_exchange tv_quotes tv_ratings tv_signals user_symbols users
weight_history wiki_article wiki_views worker_runs
```

## The 45 web routes

```
/  /desk/overview  /live  /login  /welcome  /hud  /s/[market]/[symbol]
MARKETS:  /markets/graph  /markets/macro  /markets/memory  /markets/regimes
          /markets/screener  /markets/trends
SIGNALS:  /signals/alerts  /signals/confluence  /signals/debate  /signals/forecasts
          /signals/insights  /signals/predictions  /signals/unusual
INTEL:    /intel/companies  /intel/company  /intel/congress  /intel/filings  /intel/insiders
          /intel/institutions  /intel/news  /intel/shorts  /intel/smart-money
LAB:      /lab/backtest  /lab/evolution  /lab/honesty  /lab/optimizer  /lab/paper
          /lab/portfolio  /lab/research  /lab/risk  /lab/scenario  /lab/signal-backtest
          /lab/strategies  /lab/system  /lab/system/agents  /lab/system/ai
          /lab/system/quality  /lab/track-record
```

## Handy read-only probes

```bash
export PATH="$HOME/.local/opt/go/bin:$PATH"
DB='file:data/signaldeck.db?mode=ro'

# THE independence check — run this before believing any accuracy number
sqlite3 "$DB" "SELECT horizon, count(*) total, sum(resolved_at IS NOT NULL) resolved,
  count(DISTINCT date(ts,'unixepoch')) distinct_days FROM prediction_outcomes GROUP BY horizon;"

# workers: last run + status
sqlite3 "$DB" "SELECT worker, status, datetime(MAX(started_at),'unixepoch') FROM worker_runs
  GROUP BY worker ORDER BY 3;"

# data-quality events by kind, last 24h
sqlite3 "$DB" "SELECT kind, count(*) FROM dq_events WHERE ts > strftime('%s','now')-86400
  GROUP BY kind ORDER BY 2 DESC;"

# certainty red flag — anything >=0.99 or <=0.01
sqlite3 "$DB" "SELECT MIN(cal_prob), MAX(cal_prob) FROM predictions
  WHERE ts > strftime('%s','now')-86400;"

# independent market weeks in the research corpus (expect ~340)
sqlite3 "$DB" "SELECT count(DISTINCT week) FROM research_weeks;"
```
