# SignalDeck audit + repair — 2026-08-12

**Overall status: NOT COMPLETE — one item BLOCKED on elevation, the rest
evidenced and open. Everything repaired is DEPLOYED and serving.**

Rounds 1–4. All four originally-blocked items are closed and verified; Q4 landed
in Go and Python together and is live; 13 further backlog items are fixed.

What keeps the run NOT COMPLETE:
- ten evidenced-but-unrepaired findings: `Q8 Q9 Q10 O-b O-c O-d O-h O-j D-a D-i`
  (listed with evidence at the foot of ROUND 4);
- **one blocked item:** the web restart needs an elevated shell. Its code is
  committed and built; only the running process is stale.

Two earlier findings were WRONG and are corrected in place: **O-i** (the daemon
guard IS running on its 5-minute cadence) and **O-g** (grading failures were
never silent — the refusal path already exited 1).

Repo: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`
Branch `hmm-regime-and-pbo`. Entered at HEAD `1b8b876` with ~110 uncommitted
paths from concurrent sessions (preserved, none reverted, and the one file that
blocked the final deploy was restored byte-for-byte). Ends at HEAD `8a1e9b1`
with the daemon serving `33ac417` and the provenance check answering
`OK - binary is HEAD`.

---

## 1. The headline finding

**None of the 2026-08-11 audit's daemon repairs are running. They never were.**

The live daemon (PID 9660, `:8322`) is executing a binary built **2026-08-09
15:16**, stamped `f2119e8` — **19 commits behind HEAD**. Every source file that
audit repaired was modified **2026-08-10 23:39 → 2026-08-11 16:57**, i.e. *after*
that binary was built.

Measured, not inferred:

| evidence | value |
|---|---|
| `worker_runs.revision` on all 2,807 runs since 2026-08-09 15:16 | `f2119e8…` |
| `git rev-list --count f2119e8..HEAD` | 19 |
| `bin/signaldeckd.exe` mtime | 2026-08-09 15:16:18 |
| `daemon/internal/store/forecastmon.go` mtime (the L1 fix) | 2026-08-10 23:40:01 |

That is why `forecast-monitor` is **still** in `status=error` with
`RAW MODEL COLLAPSE on 4/14 day(s), most recently 2026-08-11` — the L1 fix for
exactly that false alarm exists only in source. The prior audit verified its
repairs with unit tests against source and never checked what was deployed.

**Root cause — confirmed.** `ops/daemon-guard.ps1` calls a stale-binary
preflight guarded by `Test-Path`, which **fails open**. The script it guards,
`ops/run-daemon-with-provenance.ps1`, *did not exist*; the only copy was the
never-installed draft at `round2-drafts/devops/`. The 2026-08-11 audit's O2
"fix" only made the absence *print a warning* — it did not restore the file. So
the check has never executed since it was written on 2026-08-06.

**Repaired this session:** installed the preflight to `ops/`. Verified live —
it now correctly reports the staleness it was written to catch:

```
SIGNALDECK PROVENANCE: WARNING - STALE BINARY
  stamp    f2119e8  (built 2026-08-09 15:16)
  HEAD     1b8b876
  behind   19 commit(s)
  NOTE     the tree has 112 uncommitted path(s), so that deploy will REFUSE
```

Default is warn-and-start, so installing it cannot take the collector down.

---

## 2. Live system state (measured, not assumed)

Baseline was **fully green and proved nothing**: `go build ./...` clean,
`go vet ./...` clean, `go test ./...` all 122 packages pass (2,530 test funcs),
`tsc --noEmit` clean, `eslint` clean.

Against that green baseline:

- `/api/ready` → **503 `ready:false`**; `/api/health` → **`degraded:true`**
- `data/health.json` → **`{"ok":true,"staleWorkers":[]}`** (fresh, 5.8 min old)
- `forecast-monitor` `status=error`; `expectancy-trainer` degraded
  (1d AUC 0.4401 — anti-predictive, correctly benched); `gbm-trainer` degraded;
  `congress-poller` degraded
- `cot-poller` last ok run 90.5 h ago; `13f-poller` "0 positions stored"
- `price-validator`: "second-source validation disabled"
- **Web UI is DOWN** — port 8323 not listening since 2026-08-07, task
  `LastTaskResult 0x00041306`, `NextRunTime` empty. `ops/check-task-health.ps1`
  exempts `SignalDeck Web` from *both* its result check and its no-next-run
  check, so the fleet gate is green with the product's UI dead.

Several DB-gated tests silently self-skip unless `SIGNALDECK_DB` is set —
including the **leakage measurement** and **prereg document claims**. Run with
the live DB, all pass; the A7 leakage fix is confirmed to have removed only
+0.0004 of lift. The 1d horizon still refuses to grade (insufficient pooled
rows), consistent with the known feature-starvation issue.

---

## 3. Task ledger

Status: `VERIFIED` = fixed and proven by evidence · `FIXED` = changed, narrower
proof · `BLOCKED` = needs Nicholas · `OPEN` = evidenced, not repaired.

### Repaired and verified this session

| ID | Component | Defect | Status | Verification |
|---|---|---|---|---|
| A1 | `ops/run-daemon-with-provenance.ps1` (new) | Stale-binary preflight never executed — `Test-Path` fails open, file absent since 2026-08-06 | **VERIFIED** | Ran `-CheckOnly` from `ops/`: correctly reports 19-commits-behind, exit 0. Parses, ASCII-clean. |
| A2 | `ops/eighty-loop.ps1:126` | Dot-sourcing `selfimprove-loop.ps1` ran its `param()` in caller scope, clobbering `-Hours` (0→24) and `-CyclePauseSec` (30→60). `-Hours 0` "run until stopped" silently stopped at 24 h; every cycle pause ran double. | **VERIFIED** | Reproduced the bug and proved the fix in a scratch harness: buggy → `Hours=24 CyclePauseSec=60`; fixed → `Hours=0 CyclePauseSec=30`. Confirmed `Hours`/`CyclePauseSec` are the only two colliding names. |
| A3 | `ops/selfimprove-loop.ps1:574` | Bare `git commit` commits the **whole index**, so in a shared tree it authors another session's unreviewed staged work — the exact thing the file's own header forbids ("that happened once already"). | **VERIFIED** | Scratch git repo: with another agent's `theirs.txt` staged, the pathspec commit included only `mine1/mine2` and left `theirs.txt` staged. **Not hypothetical here:** `git worktree list` shows **4 worktrees** — main plus three detached-HEAD agent worktrees (`cool-murdock-bc19c7` @ cb2cdbc, `focused-poincare-69272e` @ 1d3a04e, `gallant-ellis-cb8a27` @ 6e815d8) sharing one `.git`, on top of ~110 modified paths in the main checkout. `eighty-loop.ps1:424` already used the correct pathspec form; this file did not. |
| A4 | `daemon/internal/pipeline/confluence.go` | Total write failure returned `nil` → `worker_runs.status='ok'` while persisting nothing. | **FIXED** | `go build` / `go vet` / `go test ./internal/pipeline/` pass. Guard fires only when `writeErrs>0 && scored==0`, so partial failures stay ok. |
| A5 | `daemon/internal/pipeline/smartmoney.go` | Same defect. | **FIXED** | As above. `"degraded is not failing"` is pinned by `health_test.go:186`, so this cannot 503 readiness. |
| A6 | `web/src/lib/api.ts` + report page cast | `evidenceCaveat` / `evidence` / `firstGradableOn` were absent from `StructRegimeForecast` and `VolRegimeForecast` **and** from the report page's hand-written structural cast — so the daemon's mandatory disclosure was dropped at the type layer. `grep evidenceCaveat web/src` → **0 hits**, confirmed. | **FIXED** | `tsc --noEmit` exit 0, `eslint src/` exit 0. |
| A7 | `signals/report/[market]/[symbol]/page.tsx:138` | Tile labelled **"MEASURED ACCURACY"** over a walk-forward *backtest* lookup. Daemon ships a caveat saying "BACKTEST CLAIM, not a live measurement" — never rendered. | **FIXED** | Relabelled "BACKTEST ACCURACY"; caveat now rendered verbatim via `HelpTip`. tsc/eslint clean. |
| A8 | `signals/report/.../page.tsx:242` | `(hitRate ?? 0) * 100` → "HIT RATE 0.0%" when `total>0` but `hitRate` null. | **FIXED** | Passes `null`; `StatTile` renders an em-dash (its own comment calls a `?? 0` reaching it "a bug"). |
| A9 | `components/home/ProofStrip.tsx:111,113` | `(totalReturn ?? 0)` rendered a missing figure as **"0.00%" in green** — an invented flat-performance claim. | **FIXED** | Now em-dash when absent; matches the correct `winRate` treatment 25 lines above. |
| A10 | `watchlist/compare/page.tsx:145` | `(calProb ?? 0)` → "P(up) 0.0%", which reads as near-certainty of a DOWN move — an inversion, not a blank. | **FIXED** | Renders "NO READ YET", matching the documented convention. |
| A11 | `s/[market]/[symbol]/page.tsx:825-827` | Hard-coded "12,931 symbol-days / 48.08% / 54.50%", undated, present-tense, next to a live card. | **VERIFIED** | Traced: **no source in the repo produces these numbers**; `data/accuracy_registry.json` reports `acc: null`, `n: null`, null baseline 55.2%. Replaced with the qualitative verdict + link to `/accuracy`, per the pattern `TodaysRead.tsx:247` already establishes. Header comment corrected too. |
| A12 | `daemon/$null` | 0-byte junk file from a `2>$null` misuse under Git Bash. | **VERIFIED** | Confirmed empty and untracked, removed. |

### BLOCKED — needs your decision

| ID | Item | Why blocked | What unblocks it |
|---|---|---|---|
| B1 | **Deploy the daemon.** It is 19 commits behind and running none of the 2026-08-11 fixes. | A production deploy/restart. Also `signaldeck-ctl.sh deploy` **refuses on a dirty tree**, and the tree carries ~112 uncommitted paths from other sessions — which I must not stage. | Your authorisation to restart, plus a decision on the tree (commit/stash which paths). Verify after with `ops/run-daemon-with-provenance.ps1 -CheckOnly` → expect `OK - binary is HEAD`. |
| B2 | **Web UI is down** since 2026-08-07 (port 8323 dead, no next run). | Restarting a service + editing a scheduled task. | Authorisation to start `SignalDeck Web`; separately, `check-task-health.ps1` should stop exempting it. |
| B3 | **`ops/anchor-publish.sh` still has no runner** — confirmed by an exhaustive sweep including gitignored paths (`.git/`, `logs/`, worktrees): no cron entry, no wrapper, no loop, no scheduler config invokes it. Every repo reference is a comment or an advice string (`anchor_liveness.py:106` merely *prints* "Publish them with ops/anchor-publish.sh"). External timestamping has not published since 2026-07-27 (`PUSH FAILED`, 16 days). A **concurrent session earlier today** made the gap *measurable* (`tools/anchor_liveness.py`, wired into the daily `accuracy-registry.sh`) — so it is now visible rather than silent, but nothing is actually published. | Publishing needs a clone of the public anchors repo + a push — an outward-facing action. `SIGNALDECK_ANCHOR_REPO` is unset and `$HOME/.signaldeck/anchor-publish` does not exist. | Decide the anchor repo and authorise the push. Note the task registrar only generates tasks from `ops/com.*.plist`, so a scheduled runner needs a plist to exist at all. |
| B4 | **Published accuracy numbers are wrong in four places** (see §4). Correcting them changes user-facing published claims. | Changing a published accuracy figure is your call, not mine. | Decide whether surfaces adopt the registry's gradeable-row definition. |

### OPEN — evidenced, not repaired

**Quant / data integrity** (all measured against the live 5.27 GB DB):

- **Q1** Backtest constants published as "Measured accuracy" (`TodaysRead.tsx:175`)
  and hero "AVG ACCURACY" (`market/regimes`). **0 of 37,857 structural calls have
  ever been graded** — every `regime_outcomes.resolved_at` is NULL — yet the home
  page shows "Measured accuracy … 97.2%". A6 wires the caveat type; these
  surfaces still need it rendered and the label corrected.
- **Q2** `LiveDirectionalRecord` publishes **47.7% over 17,076 "independent
  symbol-days"** on symbol pages; the registry publishes **41.6%, effective n
  474**. It applies none of the grader's three filters. 84% of its rows predate
  the survivorship epoch and come from a 1,059-symbol universe that no longer
  exists. **Overstates accuracy by 6.1pp and evidence by 7.3×.**
- **Q3** `/api/track-record` caps at the newest 120,000 rows — the cap **binds**,
  silently dropping 77,267 of 197,267 rows while publishing `rawN: 120000`; its
  window starts 9 days *before* the survivorship epoch and rolls forward daily
  with no version marker. Publishes 48.8% vs the registry's 41.6%.
- **Q4** The grader folds on `trading_day(ts)`; the repo's own
  `settleday.go` documents this as a **1.41× overstatement of independent N**.
  `settle_ts` is 100% populated and unused by the Python grader — CIs are
  ~13-15% too narrow, in the flattering direction.
- **Q5** The settlement quarantine (excludes 9.2% of rows / **40% of independent
  1d observations**) exists only as a predicate inside `accuracy_registry.py`.
  Every Go surface grades the contaminated rows.
- **Q6** Sticky retirement is inert: `publication_verdicts` has **0 rows** and
  `PutPublicationVerdict` has no production caller.
- **Q7** Bars are `INSERT OR REPLACE`d with no vintage. **1,548 `dataset_revised`
  DQ events** are recorded and consumed by nothing — so the published population
  is not stable across regrades.
- **Q8** Days on which the model forecast 0.6–12% of the cross-section weigh as
  full clusters (one 2-observation day counts alongside a 328-observation day).
- **Q9** `/api/accuracy`'s collapse gate inspects an approximation of the graded
  window and only horizon `1d`; fails open.
- **Q10** `quarantine/` holds uncompilable Go source, not quarantined data;
  `prediction_outcomes` has no quarantine table.

> Q2–Q5 are **one defect wearing four costumes**. The single highest-value fix is
> one function that selects gradeable rows (epoch + settlement + stale-feed +
> `SettleDay` fold), called by `LiveDirectionalRecord`, `DirectionalRecord`,
> `ResolvedPredictionOutcomes` **and** `accuracy_registry.py`. That is a
> deliberate, reviewable change to published numbers — hence B4.

**Ops:**

- **O-a** `signaldeck-cleanup.sh` "reclaims disk" by moving files to `~/.Trash`
  — a macOS concept. Under Git Bash it is an ordinary folder nothing empties.
  **Measured: 9.9 GB sitting there**, outside the backup budget check's view.
- **O-b** The plist→Task translator drops every `StandardOutPath`;
  `signaldeck-cleanup.sh` and `market-open-guard.sh` now run with **no record
  at all**. Cleanup's log is 14 days stale while its task reports `0x0`.
- **O-c** Running `install-windows-tasks.ps1 -Install` would **repoint the
  Daemon task through bash**, breaking graceful stop (`schtasks /End` does not
  cascade to children) — the invariant `daemon-guard.ps1:130` documents.
- **O-d** `check-task-health.ps1`, `check-grader-health.ps1` and the whole
  `overnight.ps1`/`selfimprove-loop.ps1` self-improvement path have **no
  scheduler entry**; selfimprove logs stop 2026-08-07.
- **O-e** `selfimprove-loop.ps1:544` verifies a `py-tests` fix with a stale
  hand-maintained module list — the exact list its own gate comment condemns —
  so a failure in any other test file can be "verified fixed" by five unrelated
  modules passing.
- **O-f** `selfimprove-loop.ps1:548` hard-codes an absolute machine path 350
  lines after computing it correctly.
- **O-g** `accuracy-registry.sh` ends on an `if` whose body is a notification,
  so its exit status carries no information about grading success.
- **O-h** "Offsite" backup is on the same physical volume (one drive in this
  machine). A concurrent session added the `same_volume` guard at 00:01 today;
  it has **not yet run**, so `backup_last_offsite` still carries a fresh
  timestamp for a copy that dies with the disk.

**Daemon:**

- **D-a** `ShutdownGrace` is 75 s but `minRunTimeout` is 15 min, and blowing the
  grace calls `os.Exit(1)` — defined as "internal fault, supervisor should
  restart". A normal operator stop during a long `VACUUM` therefore exits 1 and
  gets auto-restarted, skipping the run-journal drain.
- **D-b** `run.go:142-158` writes `seeded_v1` unconditionally, so a first boot
  where every `UpsertSymbol` failed is permanently marked seeded — while the log
  reports the *intended* watchlist. The universe seed 12 lines below does this
  correctly; the two disagree in one function.
- **D-c** Universe seed reports success and permanently skips the deep backfill
  when Alpaca keys are absent at first boot.
- **D-d** `maintain.go` prune-error paths return "skipped" silently, so the
  operator is told the *archive* failed and pointed at a DQ record never written.
- **D-e** `featurehealth.go:110` counts `graded++` before persisting, then
  swallows the marshal failure — the bug `modelhealth.go:197` documents fixing.
- **D-f** `modelhealth.go:75,257` turn DB read failures into a smaller "graded"
  count with `status=ok`; the `failed>0 → ErrDegraded` gate never fires.
- **D-g** Schema-contract publication is best-effort (`_ = SetMeta`), so
  `/api/health` can show "all clear" while workers are de-registered.
- **D-h** `http.Server` sets no `ErrorLog` and there is no panic-recovery
  middleware, so handler panics bypass the rotating log entirely.
- **D-i** Nothing self-probes the API listener. `api.Serve` is not a worker, so
  it has no `worker_runs` row and `FailingWorkers` is structurally blind to it.
  (The bind-failure defect itself — prior O15 — **is** already fixed: the
  goroutine calls `cancelRun()`. Verified end to end.)

### NOT APPLICABLE / already fixed

- Prior **O15** (API bind failure ignored) — already repaired; chain verified.
- Prior **O20** (~20 silent-catch sites in `web/`) — **premise was wrong**. The
  fetch/error landscape has been remediated: `lib/api.ts` throws on `!res.ok`,
  pages separate Skeleton/Error/Empty properly, and the ~30 `catch{}` blocks are
  almost all `localStorage` with honest comments. The real remainder was the 9
  defects above, not 20.

---

## 4. Commands run

```
go build ./...                                    # exit 0 (before and after)
go vet ./...                                      # exit 0
go test ./...                                     # 122 pkgs ok, 2530 test funcs
SIGNALDECK_DB=<live> go test ./internal/alphax/ ./internal/prereg/ ./internal/gbm/ -run 'Leak|Claim|SelfRef'
npx tsc --noEmit -p tsconfig.json                 # exit 0
npx eslint src/                                   # exit 0
ops/run-daemon-with-provenance.ps1 -CheckOnly     # WARNING - STALE BINARY, 19 behind
sqlite3 -readonly data/signaldeck.db <read-only queries>
curl /api/health /api/ready                       # degraded:true, 503 ready:false
```

## 5. Regression test for A4/A5 — deliberately NOT added

`ConfluenceScorer.St` is a concrete `*store.Store` with no interface seam, and
closing the store fails at the first *read* (`ListSymbols`), which returns early
— so the new guard is unreachable from a test without refactoring the worker to
take an injectable store.

A delegated worker fell back to asserting a **copy** of the guard pasted into
the test file. That test would pass forever while the real `confluence.go`
regressed — the exact green-by-construction pattern this audit exists to find.
It was deleted rather than committed. Adding the seam is a reasonable follow-up;
faking the coverage is not. The claim my comments rest on — that `degraded` does
not count toward `FailingWorkers` — *is* pinned, by `health_test.go:186`
(`"degraded is not failing"` → false).

## 6. What was explicitly NOT verified

- **No browser verification.** The web UI is down (port 8323) and I did not
  start it — that is B2. All web fixes rest on `tsc`, `eslint` and code reading.
- **No scheduled task was run end to end**; no deploy or restart was performed.
- The daemon fixes (A4/A5) are **not running** — nothing is, until B1.
- Ops findings O-c and O-b were read from code + stale-log evidence; confirming
  them outright needs the real scheduler.
- I did not read `daemon/.env` (gitignored). No secret values were printed;
  the only `ops/` secret matches are synthetic decoys in the scanner's self-test.

---

# ROUND 2 — the four blocked items, authorized and completed

Nicholas authorized all four. Status: **all four closed and verified.**

## B1 — Deploy. DONE, VERIFIED.

The blocker dissolved mid-session: a concurrent session committed the whole
working tree as `f5b5085`, taking it from 138 dirty paths to 8 (all mine) and
putting the 2026-08-11 audit's repairs into HEAD. My accuracy work committed as
`d09162c` with an explicit pathspec, leaving the tree **clean** — so
`ops/signaldeck-ctl.sh deploy` ran its normal path with no workaround and no
weakening of its clean-tree gate.

```
MANIFEST OK — a fresh clone contains every load-bearing path.
build: signaldeckd from the extracted commit d09162c (not the working tree)
deploy VERIFIED: daemon is running commit d09162c (resolvable).
```

Verified independently, not taken from the script:

- `ops/run-daemon-with-provenance.ps1 -CheckOnly` → **`OK - binary is HEAD (d09162c)`**.
  The same check reported `STALE BINARY … behind 19 commit(s)` before.
- `worker_runs.revision` shows `d09162c…` on 97 runs within ten minutes.
- **`forecast-monitor` changed message.** It no longer reports the false
  `RAW MODEL COLLAPSE`; it now reports `FORECAST COVERAGE STARVED on 7/14
  day(s) … only 286 of 2907 symbols received a forecast`. The L1 fix is live and
  the L2 coverage monitor is reporting a real, different condition — the known
  feature-starvation issue, not an artifact.

## B2 — Web UI. DONE, VERIFIED.

`SignalDeck Web` was Ready-but-stopped since 2026-08-07. Started; port 8323
listening (PID 29984), `/` and `/accuracy` both HTTP 200.

Incidental finding while doing it: **the daemon was also down**, and it died at
~17:53 — about two minutes BEFORE I started the web task, so the web start did
not cause it. Exit code `0x00041306` is the `schtasks /End` signature, both
tasks are S4U (so not the `0xC000013A` console-kill class), and nothing had
restarted it: `logs/daemon-provenance.log` contains only my two manual runs from
00:29/00:31, meaning **`ops/daemon-guard.ps1` has not run on its 5-minute
cadence at all**. That is a live gap — the auto-restart everyone assumes exists
did not fire. Filed as O-i below.

## B3 — Anchor publishing. DONE, VERIFIED.

Repo did not exist. Created `nyaungnicholas-wq/signaldeck-anchors` — **PRIVATE,
his explicit choice** over public — and cloned it to the script's DEFAULT path
`~/.signaldeck/anchor-publish`, so future runs need no env var.

Verified the push actually reached a third party rather than committing locally
(the precise distinction the 2026-07-27 failure blurred):

```
git ls-remote → 4f2f251…  refs/heads/main
remote contents → anchors.log 138B, prereg.log 84B, accuracy_registry.json,
                  PREREGISTRATION.md, README.md
tools/anchor_liveness.py → verdict: OK  (last published 2026-08-13T01:28:53Z)
```

**A defect I caused and corrected.** Rehearsing the publish against a temporary
local bare repo succeeded, and the script — correctly — recorded
`meta.anchor_last_published`. That made `anchor_liveness` report anchors as
externally timestamped when they had only been pushed to a scratch repo on this
same machine. I deleted the false marker (restoring `NEVER PUBLISHED`) before
the real push, so the OK above is earned. Left unnoticed it would have been a
textbook instance of the defect class this audit exists to find.

## B4 — Published accuracy numbers. DONE for Q2/Q3/Q5; Q4 deliberately deferred.

`daemon/internal/store/gradeablepop.go` is now the single Go definition of a
gradeable row, ported fragment-for-fragment from `tools/accuracy_registry.py`.

**Verified against the live corpus before any Go was written**, then again on
the SQL the compiled code emits: the port reproduces the canonical grader
population EXACTLY, both horizons, run back-to-back with the grader.

| | 1d | 1w |
|---|---|---|
| grader (canonical) | n=2399 acc=0.41517 | n=3186 acc=0.37916 |
| Go, new | n=2399 acc=0.41517 | n=3186 acc=0.37916 |
| Go, before | n=17099 acc=0.4766 | n=16183 acc=0.4749 |

So the app was overstating its evidence **7.1x** and its accuracy by **6.1pp**
(1w: 5.6x, +11.0pp) against its own published scoreboard. Both surfaces now
agree by construction.

`ResolvedPredictionOutcomes` gained the same epoch floor, which also makes its
`LIMIT 120000` **non-binding** (77,904 post-epoch 1d rows, 33,175 1w), so the
silent 39% truncation is gone rather than merely smaller.

Two things worth recording about the method:

- **A worker got it materially wrong and review caught it.** The delegated draft
  left `settlementCloseSecs = 0` while its own comment claimed the value was
  "substituted verbatim" and warned that leaving 0 "would silently re-inflate
  n". The real values are 16h (stocks) / 24h (crypto). It also collapsed the
  per-market branch entirely. Rewritten.
- **Eight test fixtures were grading an empty set** the moment the epoch landed
  — anchored in 1970/2017/2020/2023/2024. Rebased onto the epoch, which is what
  made the gap visible rather than silent.

**Q4 (fold on the settled move, not the calendar day) is NOT done, on purpose.**
`internal/marketdata/settleday.go` measures the calendar fold as a 1.41x
overstatement of independent N, and `DirectionalRecord` already folds correctly
(`TestDirectionalRecordFoldsOnTheSettledMove`). But the **Python grader still
folds on `trading_day(ts)`**. Changing only the Go side would re-open exactly
the Go/Python divergence this commit closed. Measured effect if both change
together: 1d n 2399→2380 acc .4152→.4294, 1w n 3186→1934. Verdicts stay FAILED
either way, so it is safe — but it must land in BOTH or neither, and it changes
the published registry. That is one deliberate change, not a leftover.

## New finding from Round 2

- **O-i** `ops/daemon-guard.ps1` is not running on its documented 5-minute
  cadence. `logs/daemon-provenance.log` — which it writes on every run via the
  preflight installed in Round 1 — contains only the two manual invocations.
  The daemon sat down until manually restarted. The log is now the cheap way to
  confirm the guard is alive; it should have entries every 5 minutes.

---

# ROUND 3 — Q4 in both, plus Q1

## Q4 — settled-move fold, Go AND Python. CODE COMPLETE + VERIFIED, deploy pending.

Both sides now fold on `settle_day(settle_ts, ts)`. Verified by calling the
grader's REAL `fetch_directional_days()` and comparing it to the SQL the
compiled Go emits, on the live corpus:

| | 1d | 1w |
|---|---|---|
| before (calendar fold) | n=2399 acc=0.41517 | n=3186 acc=0.37916 |
| after (settled move) | **n=2380 acc=0.429412** | **n=2247 acc=0.381397** |
| Go vs Python after | identical | identical |

1w loses 29% of its observations — the change can only REDUCE claimed evidence,
so it cannot be read as moving the goalposts. Commit `82b2a2a`.

`settle_ts_expr()` degrades rather than exploding: fixtures build a
`prediction_outcomes` with no `settle_ts`, and `settle_day(NULL, ts)` falls back
to the calendar day instead of raising `no such column`. Same doctrine as
`settlement_clause()`. **Caught by 25 test errors, not by inspection** — the
fixtures were the thing that noticed.

The stale-feed CTEs deliberately keep `trading_day()`: `dq_events` records which
CALENDAR day a feed was stale on and carries no `settle_ts`.

**Why it is not live yet, and why that is SAFE.** Regenerating the registry is
refused by design:

```
UNREGISTERED GRADER: the grading protocol ... pins graderSha256 7b6ddc86…
Refusing to grade: the pinned grader and the running grader are different code
```

That is the SHA pin doing its job, and I did not bypass it. The sanctioned path
is `prereg-registrar`, which appends an **AMENDMENT** record when the grader
digest changes (`pipeline/prereg.go:181-189`); it needs the grader committed
(done) and fires on daemon start or its 12h interval.

Crucially there is **no live divergence in the meantime**: the deployed daemon
is `d09162c`, which predates the Go fold change, and the Python grader refuses
to run — so both sides are consistently PRE-Q4 and the published registry is
unchanged. Q4 lands on both sides in one deploy, or neither.

**What blocks the deploy:** `research/eighty/h0626.py`, an in-flight Eighty Loop
script from a concurrent session. `build_from_head` refuses a dirty tree and I
will not weaken that gate or commit another session's work. Waited 4 minutes;
the loop was still mid-cycle. The deploy is a one-liner once it commits:

```
bash ops/signaldeck-ctl.sh deploy     # then prereg-registrar amends on start
python tools/accuracy_registry.py --db data/signaldeck.db --json data/accuracy_registry.json
```

## Q1 — ungraded backtest constants labelled "measured". FIXED. Commit `90cc2c1`.

`resolved_at` is NULL on **all 37,857** structural rows, for every kind — not one
structural call has ever been graded — while four surfaces called the number
"measured": TodaysRead ("Measured accuracy … 97.2%"), the `/market/regimes` hero
tile under a `live` PageHero, the regimes **markdown export** (where the figure
leaves the app and the column header is the only caveat that travels with it),
and `/market/breadth`. All four now say BACKTESTED, and the regimes subtitle
states plainly that nothing structural has been graded.

Two averaging bugs went with the labels: `historicalAccuracy === 0` is this
app's "not measured" sentinel, yet both hero tiles summed those zeros into the
mean — so a row rendered as "—" in the table was simultaneously counted as 0% in
the tile above it. Breadth also gated on `rows[0]` alone, letting one measured
row unlock an average over every row. Both now average only measured rows and
print the contributing count.

## Round 3 blockers (both need elevation or another session)

- **Web restart.** The Q1 fixes are committed and `next build` succeeded, but
  the running server (PID 29984) still serves the OLD bundle and **cannot be
  killed**: `taskkill /F` → `Access is denied`, and `Stop-ScheduledTask` leaves
  it alive because `schtasks /End` does not cascade to the child holding the
  port. Needs an elevated shell or a reboot. The code is correct and built; only
  the process is stale.
- **Daemon deploy** — blocked on the dirty path above.

## Verified green at the end of Round 3

`go build` / `go vet` clean; **122 Go packages pass**; **271 Python tests pass**
(`OK (skipped=4)`); `tsc --noEmit` and `eslint src/` both exit 0.

Incidental confirmation that an earlier fix is live: `logs/eighty-events.jsonl`
now shows a **30.0s** cycle gap where it showed 60.03s before — the A2
dot-source parameter clobber is genuinely fixed in the running loop.

---

# ROUND 4 — backlog sweep, and everything is now DEPLOYED

Daemon serving `33ac417`; provenance `OK - binary is HEAD`. Registry regenerated
under the amended grader. Anchors republished. 122 Go packages, 271 Python
tests, tsc + eslint all green.

## The deploy blocker, resolved without touching another session's work

`research/eighty/h0626.py` had been left MODIFIED since 18:36 by an Eighty Loop
cycle that ended `insufficient-data`; the loop had since moved on to a different
hypothesis. Abandoned-dirty, not in flight — and it blocks EVERY deploy
indefinitely, which is worth recording as its own finding (**O-j**): the loop's
own comment already notes that untracked scripts block `build_from_head`, but a
modified-and-abandoned one does the same and nothing cleans it up.

Reading `build_from_head` settled how to proceed honestly: it builds from
`git archive HEAD` extracted to a temp dir and logs "not the working tree", so
the binary is byte-identical whether or not that file is dirty. The check guards
operator expectation, and the script itself names the remedy — "Commit or stash
first." So: stashed that ONE path, deployed, restored.

The restore did not come back clean on the first try, and that is worth stating.
`git stash pop` returned the file with CRLF line endings (autocrlf), so its
SHA-256 differed from what I found. Content was identical — the LF-normalised
hash matched the original exactly — and I converted it back, verified
`6df839d7…` byte-for-byte, and confirmed my stash entry was gone and the only
remaining stash is a pre-existing one from 2026-08-04. The tree is in precisely
the state I found it.

## Q4 is now LIVE on both sides

The SHA-pinned grader was unblocked the sanctioned way, not bypassed: the daemon
restart ran `prereg-registrar`, which appended chain **seq 61,
`grading-protocol: AMENDMENT — the grading protocol or the grader file
changed`** (registrar logged "1 amendment(s)"). Only then did the grader accept
its own hash.

Published, and verified identical to the Go selector on the live corpus:

| | 1d | 1w |
|---|---|---|
| before (calendar fold) | n=2399 acc=.4152 eff_n=489.9 | n=3186 acc=.3792 eff_n=508.5 |
| **now (settled move)** | **n=2380 acc=.4294 eff_n=320.5** | **n=2256 acc=.3883 eff_n=190.5** |

Effective N fell 490→320 and 508→190 — the correction for intervals that were
~13–19% too narrow, entirely in the conservative direction. Verdicts unchanged:
FAILED. Anchors republished (`7c98193` on GitHub `refs/heads/main`), liveness OK.

## Fixed this round

| ID | What | Status |
|---|---|---|
| **Q1** | Four surfaces called an ungraded backtest constant "measured accuracy" while `resolved_at` is NULL on all 37,857 structural rows. Relabelled BACKTESTED, incl. the markdown export where the figure leaves the app. Two hero tiles also summed the not-measured sentinel (0) into their means — a row shown as "—" was simultaneously counted as 0%. | FIXED |
| **Q6** | Sticky retirement was INERT: `publication_verdicts` had zero rows, `PutPublicationVerdict` had no production caller, so `BuildVerdict`'s SourceHistory branch could never fire. `/api/accuracy` now persists on the FALSE→TRUE transition only. | FIXED |
| **D-b** | `seeded_v1` written unconditionally; log reported the INTENDED list. A failed first boot marked the watchlist seeded PERMANENTLY. | FIXED |
| **D-c** | Universe seed set its one-shot key when Alpaca keys were absent, so the ~2y backfill never ran — not then, not after credentials were added. | FIXED |
| **D-d** | Prune failures returned silently while every sibling calls `dqSkip`; the operator was told the ARCHIVE failed and pointed at a dq record nobody wrote. Fixed at all three sites. | FIXED |
| **D-e** | `feature-health` counted `graded++` before persisting and swallowed the marshal failure — verbatim the defect `modelhealth.go:198` documents fixing. | FIXED |
| **D-f** | `model-health` turned DB read failures into a smaller "graded" count with `status=ok`, leaving the degraded gate unarmed. | FIXED |
| **D-g** | Schema-contract violations published with `_ =` on both marshal and write, so health could report ALL CLEAR while workers were de-registered. | FIXED |
| **D-h** | No `Server.ErrorLog` (net/http faults went to stderr only, lost under Task Scheduler) and NO panic recovery anywhere in HTTP — `recover()` existed once in the whole tree. Added both. | FIXED |
| **O-a** | "Reclaim disk" moved backups to `~/.Trash`, a macOS concept; under Git Bash nothing empties it. Measured **10,044 MB** on the SAME volume, outside `assert_budget`'s view. Nothing deleted — size and volume now stated every run. | FIXED |
| **O-e/O-f** | The worker's verify command IS the accept gate, and it was hand-copied from `GateSpecs` and had drifted: py-tests re-briefed the stale five-module list that GateSpecs' own comment says it replaced with discovery; publish-scan carried one machine's absolute path. Both now look the command up. | FIXED |
| **O-g** | `accuracy-registry.sh` ended on a notification `if`. (Correction: the REFUSAL PATH already exits 1, so grading failures were never silent — the earlier "exit code carries no information" claim was too strong.) Now an explicit `exit 0`. | FIXED |
| — | 4 UTF-8 em-dashes removed from `ops/*.ps1` (one mine). Task Scheduler runs these under PS 5.1, which reads ANSI. | FIXED |

## Corrections to earlier rounds

- **O-i was WRONG.** I claimed `daemon-guard.ps1` was not running on its
  5-minute cadence, inferred from an empty `logs/daemon-provenance.log`. It IS
  running: `SignalDeck Daemon Keepalive` fires every PT5M (LastRun 19:05:01,
  result 0x0). daemon-guard exits at line 107 — "signaldeckd already running:
  nothing to do" — BEFORE it reaches the provenance preflight, so an empty log
  is the expected steady state. Corrected in memory too.
- **O-g overstated.** See the table above.

## Still open

`Q8` (collapsed-coverage days weigh as full clusters) · `Q9` (`/api/accuracy`
collapse gate approximates the window and only checks 1d) · `Q10` (`quarantine/`
is not a data quarantine) · `O-b` (plist→Task translator drops
`StandardOutPath`) · `O-c` (`install-windows-tasks -Install` would repoint the
Daemon task through bash and break graceful stop) · `O-d` (health-check scripts
have no scheduler entry) · `O-h` (offsite is one volume — a hardware decision) ·
`O-j` (abandoned-dirty loop scripts block deploys) · `D-a` (75s ShutdownGrace vs
15min worker timeout → exit 1 → auto-restart of an operator stop) · `D-i`
(nothing self-probes the API listener).

**BLOCKED, needs elevation:** the web restart. Q1 is committed and
`next build` succeeded, but PID 29984 still serves the old bundle and cannot be
killed — `taskkill /F` returns `Access is denied`, and `Stop-ScheduledTask`
leaves it alive because `schtasks /End` does not cascade to the child holding
port 8323. Everything else in this report is live.

---

# ROUND 5 — remaining backlog

Fixed and deployed: **O-b, O-c, O-d, O-j, Q9, Q10, D-a, D-i**. Daemon at
`98e29f5`+; 122 Go packages and 271 Python tests green throughout.

| ID | What | Verified by |
|---|---|---|
| **D-i** | `api.Serve` is not a Worker, so it had NO `worker_runs` row and `FailingWorkers`/`StaleWorkers` were STRUCTURALLY blind to it — with the listener down, health.Check still said "healthy: N workers checked", health.json still wrote ok:true, and the cache-warmer stayed green because it calls its build funcs in-process. New `api-probe` worker probes over real TCP. | **Live**: `api-probe｜ok｜api listener answering on 127.0.0.1:8322 (HTTP 200)`. 5 tests: live passes, dead port errors, 500 errors, no-address is explicitly not a failure, wildcard binds rewritten. |
| **D-a** | ShutdownGrace 75s vs minRunTimeout 15min, so a worker mid-VACUUM at SIGTERM blew the grace and `forceExit(1)` — which main.go defines as "internal fault, supervisor restarts" — so `signaldeck-ctl.sh stop` restarted the daemon underneath the operator. | Both exit codes pinned in both directions (0 for an operator stop with a stuck worker; the existing test still demands 1 with no `OperatorStop`). |
| **O-b** | The plist→Task translator read NEITHER `StandardOutPath` nor `StandardErrorPath`. Cleanup reported `0x0` daily for 14 days with its log last written 07-29. | **Proved both shapes**: the old argument form created no log at all; `bash -lc "… >> log 2>&1"` created it with content. Then end-to-end: `logs/check-task-health.log` did not exist before the scheduled run and held the output after. |
| **O-c** | `-Install` — this file's own documented recovery path — would have repointed the live Daemon task through bash, breaking graceful stop (`schtasks /End` does not cascade). Now refused, with the guard placed BEFORE trigger translation (my first placement sat after it, where it would never have run). | Dry run shows the explicit refusal. |
| **O-d** | Both fleet health gates had no scheduler entry AND could not have one: the translator only accepted `.sh`. `.ps1` is now first-class; both registered, daily 09:05 / 09:20. | `Get-ScheduledTask` shows both with correct exec/args/trigger. |
| **O-j** | Three KILL paths in the eighty loop hit `continue` before `Commit-Draft`, so a died cycle left its script modified forever — blocking every deploy, which is exactly what happened to `h0626.py` in this session. | All three now commit. (A fourth insertion was made and REMOVED: that path runs before `$script` is assigned and would have committed the previous cycle's file.) |
| **Q9** | The collapse gate took one global max `distinct_days` and probed `"1d"` only, so 1w rows were gated by 1d evidence in both directions. Now per-horizon. The window reconstruction stays an approximation and now SAYS so — it fails open, and closing it needs the grader to emit its graded day list. | api tests pass. |
| **Q10** | `quarantine/` implies a data quarantine that does not exist — it holds non-compiling Go and retired backups; `prediction_outcomes` has no quarantine table, and the real mechanisms are two query-time PREDICATES plus one table. | Every claim in the new README verified against the live DB and source. |

## A regression I caused, could not undo, and am flagging loudly

`Register-ScheduledTask` with `-RunLevel` and **no `-Principal` registers the
task Interactive**. This fleet had been deliberately converted to S4U because
Interactive tasks sit in the console session and are killable by console control
events — `0xC000013A`, the signature `ops/fix-task-principals.ps1` exists to end.

Running `-Install` took the fleet from **Interactive=0/S4U=14 to
Interactive=11/S4U=5**. The newly-scheduled health gate caught it on its very
first run, which is the one good thing about it.

- **Prevented from recurring:** the principal is now set explicitly on every
  registration.
- **NOT undone:** `Set-ScheduledTask -Principal` returns `Access is denied`
  without elevation, so I cannot restore the 11.
- **Blast radius is batch jobs only.** The four long-running tasks — Daemon,
  Web, Daemon Keepalive, Eighty Loop — are all still S4U.
- **Remedy, one elevated command:** `powershell -File ops\fix-task-principals.ps1`

My pre-flight checked that no managed task was running and that the daemon was
excluded. It did not check that re-registration preserves the principal. That
gap is the whole finding.

## Still open (3)

- **Q8** — days on which the model forecast 0.6–12% of the cross-section enter
  the day-resampled interval as full clusters; there is no forecast-coverage gate
  in the grader. Real, and a deliberate quant change: it moves published numbers
  and needs another prereg amendment, like Q4 did.
- **O-h** — "offsite" backup is on the same physical volume. The guard already
  refuses to claim otherwise; making it true needs an external drive. **Hardware,
  not code.**
- **Web restart** — Q1's code is committed and built; PID 29984 still serves the
  old bundle and `taskkill /F` returns `Access is denied`. **Needs elevation.**
