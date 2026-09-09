# SignalDeck release ledger — 2026-09-08 pass

Repo `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `public-launch`. Baseline HEAD
`cbec4177b072cb4c3f16c0190ffb211bdbdaf264` (the daemon was running `60b7afa`; the intervening
commits were backup scripts, docs and a test — no daemon fix was undeployed). Release commits:
`8d8e6ce` (daemon, tools, ops), `cdd89db` (web), `b15975d` (regenerated UX score); the daemon was
deployed through `ops/signaldeck-ctl.sh deploy` and verified running
`b15975d54e27a51b7897e45e0f9d91c83b75a09f` at 2026-09-08 00:00 local, with worker rows stamped.
The documentation, tests and heartbeat-timeout changes made after that deploy are in the commit
that carries this ledger; none of them touches the daemon binary.

Status vocabulary: **CONFIRMED** (reproduced, not yet repaired) · **IMPLEMENTED — NOT YET
VERIFIED** · **VERIFIED** (repaired and observed at runtime) · **EXPECTED LIMITATION** (intended
behaviour or evidence that must accrue) · **BLOCKED** (needs the owner) · **NOT REPRODUCED**.

Independent audit re-checked: `C:\Users\Nicholas_N\Documents\Codex\2026-09-07\ca\outputs\SignalDeck_submission_audit.md`
(its findings 1–5 map to F1–F5 below; its operational notes map to F6, F9–F13).

## Summary

| id | area | severity | impact | evidence (measured) | status |
|---|---|---|---|---|---|
| **F1** | publication | high | Landing page printed the refused figures under a "publication refused" banner | Browser, 2026-09-07 22:20: / showed the red banner, a "retired by its own rule" card reading −10.3pp / 44.7% / 55.0% / 2,892 forecasts / 28 days, and a four-row table. On disk, data/accuracy_registry.json (mtime 19:30... | fixed |
| **F2** | statistics | high | Grader's "significantly worse" sentence shown while the honesty block says WITHHELD (null-interval overlap) | The directional-ensemble (1d) row carries honesty.resolvability.supported=false with the reason that accuracy 0.4474 [0.3517, 0.5473] and null 0.5500 [0.3870, 0.7029] overlap; the grader's sentence compares the accura... | fixed |
| **F3** | access | medium | "See the grades" led to a sign-in wall rendered as a refusal; `/api/accuracy` 401 anonymously | Anonymous GET /api/accuracy → 401; /accuracy rendered a REFUSED banner reading "not public on this deployment — sign in"; "Open workspace" bounced to /login. - Root cause. /api/accuracy was missing from the daemon's a... | fixed |
| **F4** | docs | medium | README declared the freeze lifted and active; refusal block quoted numeric grader output; SHIP_READINESS reprinted the withheld table | README line 5 "freeze lifted", line 27 "FROZEN — REMEDIATION IN PROGRESS", line 49 "lifted"; the refusal block embedded the grader's stderr including calibration bins with percentages and counts; partials/live_accurac... | fixed |
| **F5** | wording | medium | `/proof` and the dashboard called deduplicated symbol-days "independent" | /proof and the dashboard strip say "deduplicated symbol-day observations over N distinct trading days (observations on one day share a market move, so they are not independent)"; the stat tile is "SYMBOL-DAY OBS."; th... (web release commit cdd89db) | fixed |
| **F6** | ops | medium | `/api/ready` 503 for weeks because deliberately abstaining workers counted as failures; then again on 09-08 for an `orphaned` row left by a restart | /api/ready 503 with reasons naming congress-poller, expectancy-trainer, forecast-monitor and gbm-trainer — all degraded, three of them by design (benched models, expected coverage abstention). - Repair. The ready hand... | fixed |
| **F7** | ops | medium | A publication refusal was filed as a grader failure heartbeat; the health task was red every day of the window | tools/grader_heartbeat.py --refused writes success=1 with error REFUSED: <reason>; ops/accuracy-registry.sh chooses --refused unless the grader itself exited or the liveness check failed. ops/check-grader-health.ps1 n... | fixed |
| **F8** | web | medium | `/volatility` showed "not readable" to cold visitors (25 s build vs 15 s bound) | /api/vol-forecast/record 25.2 s uncached; the page's server fetch is bounded at 15 s; the sweep and the browser both showed "The live record is not readable right now". - Repair. Body-level stale-while-revalidate cach... | fixed |
| **F9** | performance | high | Heavy reads and writes collapse under worker load (symbol page >200 s, sign-in 30 s timeouts) | (authenticated curl). Under nightly trainer load, 2026-09-07 22:30: /api/paper 116.7 s (timed out), /api/symbol?symbol=SPY 48 s, /api/screener 31 s, /api/movers 19 s, /api/ledger/verify 503 after its 30 s cap. Quiet, ... | accepted-risk |
| **F10** | forecasts | low | Forecast-monitor "coverage starved" on 10/12 days | - Status. NOT REPRODUCED as a defect: every measured directional leg ranks backwards and is | refuted |
| **F11** | evidence | low | Volatility 1/60 days; accuracy window refused; congress poller awaiting its next run | - Volatility record 1 of 60 distinct days; accuracy window refused over 23 collapsed | accepted-risk |
| **F12** | deploy | low | Worker rows stamped `60b7afa` while HEAD was `cbec417` | - Status. VERIFIED as resolved: ops/signaldeck-ctl.sh deploy reported "deploy VERIFIED: | fixed |
| **F13** | provenance | medium | Docs call the anchors repo public; GitHub says PRIVATE | - gh repo list reports nyaungnicholas-wq/signaldeck-anchors PRIVATE while anchor-publish.sh | open |
| **F14** | web | low | `/accuracy` titled "Dashboard"; `/volatility` title suffix doubled | - /accuracy had no metadata export (inherited "Dashboard — SignalDeck"); /volatility carried (web release commit cdd89db) | fixed |
| **F15** | UI | medium | Overlapping onboarding surfaces, 6,000 px symbol page, eight header chips, reason wall on the dashboard | - Three overlapping onboarding surfaces on /dashboard (setup checklist, goal banner, "New | fixed |
| **F16** | disclosure | medium | No development-AI disclosure in the README | - README "Credits and AI assistance"; docs/COMPETITION.md carries the submission disclosure (docs commit 8859834) | fixed |
| **F17** | ops | low | Scheduled tasks ending with result 1 (Accuracy, Check-Grader-Health, Market-Close, Web) | - Last result 1 for SignalDeck Accuracy (the refusal path exits non-zero), Check-Grader-Health | fixed |
| **F18** | ops | low | Heartbeat and anchor jobs lose writes to "database is locked" | - grader_heartbeat.py at 23:58 and anchor-publish.sh at 19:30 both hit the lock. The | fixed |
| **F19** | ops | medium | The SignalDeck Web task (8323) dies with 0xC000013A when a console control event reaches it; the principal fix needs elevation | At 2026-09-08 00:50 port 8323 refused connections; the task "SignalDeck Web" showed last result 0xC000013A (console control exit) at 23:53 while the loopback workspace on 3000 stayed up. ops/check-task-health.ps1 has ... | open |
| **F20** | security | low | Public-surface mode had never been exercised end to end | bin/signaldeckd.exe (commit b15975d) started on a scratch database with SIGNALDECK_PUBLIC_SURFACE=1, SIGNALDECK_PUBLIC_READS=false, SIGNALDECK_OPEN_SIGNUP=false, a throwaway token, and the notify/LLM/offsite keys over... | fixed |
| **F21** | ops | low | No keepalive for the web tasks after a console-control kill | - ops/web-guard.ps1 probes 8323 and 3000 every five minutes and restarts the matching task when one stops answering; registered unelevated as "SignalDeck Web Keepalive" (first run 18:38: "8323 ok, 3000 ok"). Mitigates... | fixed |

## Findings

### F1 — Landing page republished the refused figures
- **Evidence.** Browser, 2026-09-07 22:20: `/` showed the red banner, a "retired by its own rule" card
  reading −10.3pp / 44.7% / 55.0% / 2,892 forecasts / 28 days, and a four-row table. On disk,
  `data/accuracy_registry.json` (mtime 19:30:49) had 18 rows and no `status` field, although the
  14:05 grading job had written a REFUSED envelope.
- **Root cause.** `ops/anchor-publish.sh` (19:30 daily) re-ran `tools/accuracy_registry.py --json`
  raw — no publication gate (`cmd/collapsecheck`), no envelope, no honesty blocks — and
  `web/src/app/page.tsx` read the file directly, deciding "refused" from the missing status while
  rendering the rows anyway. Two private interpretations of one decision.
- **Repair.** `anchor-publish.sh` no longer regrades; it publishes the registry as the 14:05 job
  left it. `tools/render_track_record.py` renders a REFUSED envelope as a refusal notice with no
  figures and exits 0 instead of aborting the whole anchor publish. The landing page fetches
  `GET /api/accuracy` and renders one of four states (ok, refused, private, unreachable); it
  prints no figure unless the daemon says OK, and never the grader's stored sentence.
- **Regression check.** `tools/test_live_accuracy.py` (`test_refused_registry_renders_a_refusal_and_no_figures`,
  `test_refused_registry_with_no_stale_grade_still_renders_a_refusal`), `tools/test_render_track_record.py`
  plus a refusal-envelope fixture check, `web/e2e/accuracy-refusal.spec.ts` (no percentage on a
  refused page, `data-status` REFUSED, `data-tone` bad).
- **Runtime verification.** After the deploy and web restart, and again after the 23:58 grading
  run: `/` shows "PUBLICATION REFUSED · REFUSED", the plain-English summary ("23 of the 80 graded
  day-horizons … collapsed cross-section between 2026-07-17 and 2026-08-06"), the full reason
  behind a details element, and the "what is still true" list. No accuracy, n, baseline or skill
  number appears anywhere on the page (page text captured 2026-09-08 00:00 and 00:30).
- **Remaining limitation.** While refusal lasts, the retirement is stated as a dated fact with a
  link to `proofs/P2_LIVE_RECORD_RECONCILIATION.md`, without figures.

### F2 — Conflicting verdicts
- **Evidence.** The directional-ensemble (1d) row carries `honesty.resolvability.supported=false`
  with the reason that accuracy 0.4474 [0.3517, 0.5473] and null 0.5500 [0.3870, 0.7029] overlap;
  the grader's sentence compares the accuracy interval to the null's point estimate only.
- **Root cause.** Pages consumed `row.verdict` (the grader's sentence) and neither the daemon's
  publication verdict (`publication.BuildVerdict`) nor the honesty block; the 19:30 regrade also
  stripped the honesty blocks from the file.
- **Repair.** `/accuracy` rows lead with the daemon's `publication_status` and its reasons,
  quote the grader's sentence labelled "Grader's sentence", and add "Not resolved by this sample:
  <reason>" when the honesty block says so. The landing page never prints the sentence.
- **Regression check.** Type-checked; no automated fixture carries an honesty block yet.
- **Runtime verification.** Under refusal the page shows no verdicts at all (by design); the row
  rendering runs only when publication is OK, so it was verified by type-check and code review,
  not in the browser.
- **Remaining limitation.** `publication.BuildVerdict` still condemns a row whose accuracy
  interval sits below the null's point estimate — not a paired, dependence-aware test of the
  difference. The sha256-pinned grader was not edited (protocol rule). Proposal on record: a
  day-blocked paired-difference test as the verdict rule, introduced through grader
  re-registration (see memory `project-signaldeck-grader-reregistration`), never by editing the
  pinned file. Historical retirement (2026-07-24) is distinct from this and stays.

### F3 — Public access mismatch
- **Evidence.** Anonymous `GET /api/accuracy` → 401; `/accuracy` rendered a REFUSED banner reading
  "not public on this deployment — sign in"; "Open workspace" bounced to `/login`.
- **Root cause.** `/api/accuracy` was missing from the daemon's always-open set (track-record,
  ledger/verify and vol-forecast/record were in it); the page mapped 401 onto REFUSED.
- **Repair.** `daemon/internal/api/security.go` adds `/api/accuracy` to the exemption (aggregates
  only; fails closed with 503 on refusal); `security_test.go` extended. `/accuracy` renders a
  distinct "Sign-in required" state on 401/403. The public header carries Grades / Risk estimates /
  Receipts / Glossary and a Sign in link (`web/src/components/PublicNav.tsx`).
- **Runtime verification.** Anonymous `curl 127.0.0.1:8322/api/accuracy` → HTTP 503, status
  REFUSED, reason present, 3 ms. Browser: `/accuracy` renders the refusal with the title
  "Accuracy registry — SignalDeck".

### F4 — Contradictory documentation
- **Evidence.** README line 5 "freeze lifted", line 27 "FROZEN — REMEDIATION IN PROGRESS", line 49
  "lifted"; the refusal block embedded the grader's stderr including calibration bins with
  percentages and counts; `partials/live_accuracy.md` printed the withheld table under a STALE
  label into six documents.
- **Repair.** README rewritten (v1.1, ACTIVE in `ops/docs-registry.json`, dated status table,
  freeze history as one dated line, limitations, credits, AI disclosure); the refusal block no
  longer embeds stderr; `tools/live_accuracy.py` renders a refusal-only block; `DOCS_INDEX.md`
  regenerated.
- **Runtime verification.** `tools/docs_gate.py check` → `docs-gate: clean`; the 23:58 grading run
  regenerated README, `partials/live_accuracy.md` and the six included documents with the new
  block (no figures); `tools/live_accuracy.py --scan` over the new documents finds no superseded
  figure.

### F5 — Sample-size wording
- **Repair.** `/proof` and the dashboard strip say "deduplicated symbol-day observations over N
  distinct trading days (observations on one day share a market move, so they are not
  independent)"; the stat tile is "SYMBOL-DAY OBS."; the footer names the measured design effect
  with the day as the unit of resampling. The collapse reason is summarized by `RefusalNotice`.
- **Runtime verification.** `/proof` text, 2026-09-08 00:00: "86,556 raw resolutions → 3,644
  deduplicated symbol-day observations over 32 distinct trading days".

### F6 — Readiness counted intended abstention as failure
- **Evidence.** `/api/ready` 503 with reasons naming congress-poller, expectancy-trainer,
  forecast-monitor and gbm-trainer — all `degraded`, three of them by design (benched models,
  expected coverage abstention).
- **Repair.** The ready handler lists degraded workers under `degraded` for an authenticated
  caller and fails only on error/timeout/orphaned; `daemon/internal/api/ready_degraded_test.go`.
- **Runtime verification.** Anonymous `/api/ready` → HTTP 200 after the deploy.

### F7 — Refusal filed as a grader failure
- **Repair.** `tools/grader_heartbeat.py --refused` writes success=1 with error `REFUSED: <reason>`;
  `ops/accuracy-registry.sh` chooses `--refused` unless the grader itself exited or the liveness
  check failed. `ops/check-grader-health.ps1` needs no change: it is red only for success=0.
- **Verification so far.** All three modes checked against a temporary database. The 23:58 run
  printed "recorded ok (publication REFUSED)" but its write failed with "database is locked" (see
  F18), so no row landed; the newest heartbeat is still the 14:05 failure row. The 2026-09-08
  14:05 run is the runtime verification.

### F8 — Volatility page unreadable when cold
- **Evidence.** `/api/vol-forecast/record` 25.2 s uncached; the page's server fetch is bounded at
  15 s; the sweep and the browser both showed "The live record is not readable right now".
- **Repair.** Body-level stale-while-revalidate cache (10-minute TTL) on the route and a
  cache-warmer entry.
- **Runtime verification.** Anonymous curl 0.07 s after the deploy; the browser shows both horizon
  cards (1 of 60 days, 301 resolved, 306 ungradable; 0 of 60, 297 ungradable).

### F9 — Heavy reads and writes collapse under worker load
- **Evidence** (authenticated curl). Under nightly trainer load, 2026-09-07 22:30: `/api/paper`
  116.7 s (timed out), `/api/symbol?symbol=SPY` 48 s, `/api/screener` 31 s, `/api/movers` 19 s,
  `/api/ledger/verify` 503 after its 30 s cap. Quiet, 2026-09-08 00:03: `/api/paper` 7.5 s,
  `/api/ledger/verify` 7.9 s. Right after the deploy (every worker's first pass at once, 62 rows
  in `running`, 28.7 CPU-seconds per 10 s): `/api/symbol` >200 s (timed out), `/api/screener`
  49.5 s, `POST /api/auth/login` 500 after 30,456 ms (the daemon log shows the session write
  timing out behind worker transactions: "worker journal: write failed, retrying … context
  deadline exceeded").
- **Root cause.** Not fully isolated. Every individual SQL behind the symbol page runs in ≤0.18 s
  read-only from Python (bars covering index, scores primary key, expectancy index, snapshots
  primary key; `insights` is a table scan at 0.18 s). The API has its own four-connection read
  pool (`apiReadConns`), but one SQLite file, one writer and ~100 workers mean the slow path is
  inside the daemon under concurrency.
- **Repair.** Not done in this pass. Proposal: body-cache and warm `/api/symbol`, `/api/screener`
  and `/api/paper` the way `/api/honesty` is; add an `insights(symbol_id, ts)` index; stagger the
  post-boot first passes; measure with worker load present before claiming a fix.
- **Consequence for the demo.** Wait 45 minutes after any daemon restart before recording; the
  first load of a symbol page can still take tens of seconds during the nightly trainers.

### F10 — Coverage starvation
- **Status.** NOT REPRODUCED as a defect: every measured directional leg ranks backwards and is
  dropped, never down-weighted (memory notes 2026-08-17 and 2026-08-27); the monitor files the
  condition as degraded with its expected marker. Its effect on readiness is handled by F6.

### F11 — Evidence that must accrue
- Volatility record 1 of 60 distinct days; accuracy window refused over 23 collapsed
  cross-sections of 80 day-horizons (2026-07-17 to 2026-08-06); congress-poller last degraded
  2026-09-07 13:00, before the Kadoa fallback's first scheduled 09:00 ET run. No threshold, grader,
  holdout or pre-registration was touched.

### F12 — Revision drift
- **Status.** VERIFIED as resolved: `ops/signaldeck-ctl.sh deploy` reported "deploy VERIFIED:
  daemon is running commit b15975d… (resolvable); 3 worker run(s) already stamped with it".

### F13 — The anchors repository is private
- `gh repo list` reports `nyaungnicholas-wq/signaldeck-anchors` PRIVATE while `anchor-publish.sh`
  and the runbooks call it the public anchors repo. README now says so;
  `docs/PUBLIC_RELEASE_PLAN.md` lists making it public as an approval the owner must give.
  OpenTimestamps proofs remain the external timestamp.

### F14 — Page titles
- `/accuracy` had no metadata export (inherited "Dashboard — SignalDeck"); `/volatility` carried
  the suffix twice. Both fixed and confirmed in the browser.

### F15 — Workspace UI density
- Three overlapping onboarding surfaces on `/dashboard` (setup checklist, goal banner, "New
  here?" popover), a 6,000-pixel symbol page, eight header chips, and the collapse reason dumped
  on the dashboard strip. The strip now summarizes the reason (F5); the rest is recorded as
  "Repair needed" in `docs/PRODUCT_SPEC.md` and `docs/DESIGN_DIRECTIONS.md` and was not changed.

### F16 — AI disclosure
- README "Credits and AI assistance"; `docs/COMPETITION.md` carries the submission disclosure
  draft with `[NICHOLAS TO CONFIRM]` placeholders for personal contribution, inspiration and
  learning. Nothing personal was invented.

### F17 — Scheduled task results
- Last result 1 for SignalDeck Accuracy (the refusal path exits non-zero), Check-Grader-Health
  (F7), Market-Close (2026-09-04, not investigated) and SignalDeck Web (launcher exit). Only F7
  addressed.

### F18 — Lost writes to "database is locked"
- `grader_heartbeat.py` at 23:58 and `anchor-publish.sh` at 19:30 both hit the lock. The
  heartbeat connect timeout was raised from 30 s to 120 s; anchor-publish is unchanged. Verified
  only by the next scheduled runs.

## Verification commands

| Command | Result | Note |
|---|---|---|
| `cd daemon && go build ./... && go vet ./internal/api/...` | OK | before and after the edits |
| `go test ./internal/api/ ./internal/publication/ ./internal/store/ -count=1` | ok 25.9 s / 0.4 s / 47.7 s | includes `ready_degraded_test.go` |
| `go test ./...` (inside `ops/signaldeck-ctl.sh deploy`) | passed | deploy VERIFIED at b15975d |
| `.venv/Scripts/python.exe -m pytest tools/test_live_accuracy.py tools/test_render_track_record.py tools/test_docs_gate.py -q` | 89 passed, 24 subtests | refusal contract tests replace the old fallback tests |
| `.venv/Scripts/python.exe tools/docs_gate.py check` | docs-gate: clean | after the README rewrite |
| `cd web && npx tsc --noEmit --incremental false` | exit 0 | |
| `cd web && npm run lint` | clean | |
| `cd web && npx next build` | exit 0 | served on 8323 and 3000 after task restart |
| `cd web && npx playwright test` | 29 passed, 2 skipped, 11 failed (first run) | 7 navigation tests asserted "no header nav" and met the new public-record nav — contract updated to "no Primary nav, no workspace links" (9 pass on rerun); the refusal test asserted a Tailwind `border-red-` class — now checks `data-tone="bad"` (needs the rebuilt bundle); `smoke:303` passed on rerun; `paper-manual` and `personalization:88` failed on sign-in — 30 s write timeouts during the post-deploy storm, then HTTP 429 from the daemon's write-tier limiter after repeated reruns (F9, harness) — final rerun after the rebuild and a limiter cool-down: accuracy-refusal (3), paper-manual (2), personalization:88 → 5 passed, 2 skipped (14 s) |
| `node web/scripts/screens.mjs --out …` | 54 PNGs (27 routes × 1440/390) | captured before the changes; public pages re-inspected in the in-app browser after |
| `bash ops/pre-publish-scan.sh` | no secret-shaped strings in tracked files or history; manifest tier 2 fails only on untracked new files until they are committed | |
| Anonymous curl after deploy | `/api/accuracy` 503 REFUSED with reason; `/api/ready` 200; `/api/health` 200 degraded=true; `/api/vol-forecast/record` 200 in 0.07 s; `/api/version` 401 (by design) | |

## Open items needing the owner
- A hosted public surface and the payment method for it (`docs/PUBLIC_RELEASE_PLAN.md`, Option B).
- Making `signaldeck-anchors` public, and creating a separate reviewed public source repository.
- The personal-contribution, inspiration and learning statements in `docs/COMPETITION.md`.
- Whether to body-cache the heavy endpoints (F9) before recording the demo video.
- Observe the 2026-09-08 14:05 grading run (F7, F18) and the 09:00 ET congress poll (F11).

### F19 — The SignalDeck Web task is killed by console control events
- **Evidence.** At 2026-09-08 00:50 port 8323 refused connections; the task "SignalDeck Web" showed last result 0xC000013A (console control exit) at 23:53 while the loopback workspace on 3000 stayed up. `ops/check-task-health.ps1` has warned since 2026-09-01 that two tasks still run with an Interactive principal reachable by console control events; the Playwright web server shutdown at the end of an e2e run is the likely sender.
- **Repair.** Not done: `ops/fix-task-principals.ps1` must run elevated. Restarting the task unelevated brings 8323 back. Status CONFIRMED · BLOCKED (elevation).

### F20 — Public-surface mode verified on a throwaway instance
- **Method (2026-09-08 18:41).** `bin/signaldeckd.exe` (commit b15975d) started on a scratch database with `SIGNALDECK_PUBLIC_SURFACE=1`, `SIGNALDECK_PUBLIC_READS=false`, `SIGNALDECK_OPEN_SIGNUP=false`, a throwaway token, and the notify/LLM/offsite keys overridden with dummies, listening on 127.0.0.1:8398; killed after the probes.
- **Result.** Anonymous: `/api/health` 200, `/api/version` 200, `/api/track-record`, `/api/honesty`, `/api/ledger/verify`, `/api/vol-forecast/record`, `/api/evidence` 200; `/api/accuracy` 503 (no registry on a fresh database, fails closed); `/api/ready` 503 (fresh database, nothing ingested yet); `/api/dashboard`, `/api/bars`, `/api/paper`, `/api/hud`, `/api/portfolio`, `/api/watchlist`, `/api/screener`, `/api/symbol`, `/api/agents`, `/api/ai/status`, `/api/export/bars.csv` all 401; `POST /api/auth/register` 403; a foreign Host header 403. This is the contract `docs/PUBLIC_RELEASE_PLAN.md` Option B relies on. Status VERIFIED.

### F21 — Web keepalive
- `ops/web-guard.ps1` probes 8323 and 3000 every five minutes and restarts the matching task when one stops answering; registered unelevated as "SignalDeck Web Keepalive" (first run 18:38: "8323 ok, 3000 ok"). Mitigates F19 without elevation; the principal fix still needs `ops/fix-task-principals.ps1` elevated.

### F9 — post-deploy measurements (daemon f0466d0, 2026-09-08 19:0x, during the post-restart storm: 70 rows running)
- `/api/screener` 2 ms and `/api/paper?strategy=flagship-1d` 1 ms, both served warm by the cache-warmer.
- `/api/symbol?symbol=SPY` answered 503 in 5.0 s twice: a cold miss waits at most 5 s for one of the two cold-build slots, and the warmer held both during the storm. That is the cache's admission control working (`Retry-After: 5`), replacing the >120 s hang; the message now says the daemon is busy after a restart and the view loads on retry. Once the storm settles a miss builds inline in seconds and repeat loads are served from the 60 s body cache.
- Sign-in during the storm still waits on the single writer (unchanged).

## Final state (2026-09-08 19:15 local)
- HEAD and the running daemon are both `26e198391dc9e7ba8b339d876bb3f818fc76d984` (deploy VERIFIED three times today: b15975d, f0466d0, 26e1983); both web instances (8323, 3000) serve the build from `46ccc20`+ (no web change since). `/api/ready` 200 with `finra-shorts` reported under `degraded`; `/api/accuracy` 503 REFUSED with the envelope reason; grader health ok; web keepalive logging every five minutes.
- Playwright after the rebuild: accuracy-refusal, paper-manual, personalization, header-tools and the smoke suite all pass once the two login helpers retry on the daemon's write-tier 429 (27 passed in the mixed run before that fix, 8 of 8 after it, 3 of 3 header-tools).
