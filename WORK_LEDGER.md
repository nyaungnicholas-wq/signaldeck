# SignalDeck Work Ledger

Controller-owned. One row per work item. `PASS` requires evidence (command + exit
code). `BLOCKED` requires an external prerequisite and the exact resume action.
No secrets in this file.

Opened: 2026-08-02 17:33 PDT (= 2026-08-02 20:33 America/New_York)
Branch: `audit/2026-07-27` @ `796eaa2`
Repo: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`

## Environment ground truth (verified, not assumed)

| Fact | Value | Evidence |
|---|---|---|
| Go required | `go 1.25.0` | `daemon/go.mod` |
| Go installed | `go1.26.5 windows/amd64` | `go version` — satisfies the directive, no toolchain defect |
| Physical disks | **exactly one**: `KINGSTON OM8TAP42048K1-A00`, SSD, 1907.7 GB, DeviceId 0 | `Get-PhysicalDisk` |
| Partitions | all on DiskNumber 0; `C:` = disk 0 partition 3 | `Get-Partition` |
| Database | `data/signaldeck.db`, 2,957,795,328 bytes, on `C:` | `ls -la data/*.db` |
| Market session at open | Sunday 20:33 ET — **equity market closed** | system clock |

## Status

| # | Item | Status | Evidence |
|---|---|---|---|
| 1 | Offsite backup on separate hardware | **BLOCKED** | Only one physical disk exists (above). Any backup target is necessarily the same physical volume. |
| 2 | Digest helper hashes file contents | **PASS (was already green)** | `cd tools && python -m unittest test_accuracy_registry` → exit 0, `Ran 109 tests ... OK (skipped=4)`. Verified genuinely satisfied, not weakened — see note. |
| 3 | eslint scratch files | **PARTIAL — BLOCKED on concurrency** | Kit.tsx defect fixed and verified. Remaining errors are in another session's live working set. |
| 4 | Pre-publish scan / untracked paths | **NOT STARTED (gated by 3)** | Depends on a stable `web/` tree. |
| 5 | Live equity ticks land in DB | **BLOCKED (time)** | Requires Mon–Fri 09:30–16:00 ET. Next window: Mon 2026-08-03 09:30 ET. |
| 6 | Forced reconnect, no duplicate subs | **BLOCKED (time)** | Same window as Item 5; must run in a live session. |
| 7 | Reverify revision resolvability | **PASS** | 5 new tests pass; `go vet` + `go build ./...` clean; committed `6c1e2b1` |
| 7b | Clean *attributable* daemon build | **BLOCKED on concurrency** | Binary stamps `vcs.modified=true` — see below |
| 8 | Supervise `tickstreamd` + backups | NOT STARTED | Reboot-survival test needs owner authorization. |
| 4.1–4.5 | Go daemon refactor (Phases 4) | NOT STARTED | Requires baseline benchmark first. |
| 5.A–5.E | Quant/data audit (Phase 5) | NOT STARTED | |
| 6 | Research direction (Phase 6) | NOT STARTED | Treat prior ensemble path as closed. |

---

## Item 1 — BLOCKED: separate physical backup device required

Not a software defect. `Get-PhysicalDisk` returns exactly one device (DeviceId 0,
KINGSTON OM8TAP42048K1-A00, 1907.7 GB SSD). Every partition — including `C:`,
which holds `data/signaldeck.db` — is on DiskNumber 0. There is no second
physical device attached, so no path on this machine can satisfy
`offsiteSameVolume: false`. Running `ops/signaldeck-backup-offline.sh` against
any local folder would produce a same-volume copy and a false green.

**Not faked, not worked around.** Resume when an external drive is attached:

```bash
SIGNALDECK_OFFSITE_DIR=<external-drive-path> bash ops/signaldeck-backup-offline.sh
```

then confirm `/api/quality.ops` reports `offsiteSameVolume: false`.

## Item 2 — PASS, and the guard is intact

The prompt's snapshot said this test fails. It does not; it passes on the
current tree. Confirmed it is genuinely satisfied rather than weakened:

- The test computes `want` with Python `hashlib.sha256(open(target,'rb').read())`,
  sources the real helper block out of `ops/accuracy-registry.sh`, runs
  `sha256_of` against `PREREGISTRATION.md`, and `assertEqual(want, got)`.
  The assertion is present and strict.
- `ops/accuracy-registry.sh:42-45` implements `sha256_of` as shasum → sha256sum →
  a Python `hashlib.sha256(open(...,'rb').read())` fallback. It hashes file bytes.
- It is not one of the 4 skips: `python -m unittest
  test_accuracy_registry.ProtocolDocumentGateTest -v` runs it and reports `OK`.

Nothing to change. No threshold, digest pin, or assertion was touched.

## Item 3 — one real defect fixed; the rest is another session's live tree

Reproduced with the named verifier, `cd web && npx eslint . --max-warnings 0`.
The prompt's description was incomplete on both ends:

- It named two scratch files. There were **three** (`scratch_check_kit.js`,
  `scratch_check_motion.js`, `scratch_check_page.js`).
- It did not mention a **real source defect**: `web/src/components/ui/Kit.tsx:27`,
  rule `react-hooks/set-state-in-effect`.

**Fixed (verified).** `Kit.tsx` `AnimatedNumber` called `setDisplay(NaN)`
synchronously inside a `useEffect` purely so the render guard — which read the
`display` state mirror — would show an em-dash. The prop `value` is the actual
source of truth for "is this a number at all", so the guard reads `value` and the
`setState` disappears. Two lines, root cause, no rule disabled and no comment
suppression:

- `daemon`-free change, `web/src/components/ui/Kit.tsx:27` — `return setDisplay(NaN)` → `return`
- `web/src/components/ui/Kit.tsx:47` — guard now tests `value`, not `display`

Kit.tsx no longer appears in eslint output. `node scratch_check_kit.mjs` → exit 0.

**Scratch files: converted, not deleted.** Established by search that they are
referenced nowhere in build scripts, CI, `package.json`, or code — the only hits
repo-wide are `REPAIR_LOOP_SUPERPROMPT.md` and one self-referential usage comment.
They are nonetheless part of another session's in-flight untracked design work
(`DESIGN_BRIEF.md`, `src/app/motion.css`, `src/components/ui/`), so they were
converted to ESM rather than deleted — converting is reversible and loses nothing,
deletion is neither. `package.json` has no `"type"`, so ESM here means `.mjs`.
Created `scratch_check_kit.mjs`, `scratch_check_motion.mjs`, `scratch_check_page.mjs`.

**BLOCKER — concurrent writer.** Another session is actively editing `web/`:

| Time | Observation |
|---|---|
| 17:33 | 6 modified web files; 3 `scratch_check_*.js` |
| 17:43 | `scratch_check_page.js` **recreated** after I deleted it |
| 17:44 | `scratch_check_types.js` **created** (new, never seen at 17:33) |
| 17:44 | 13 modified web files; eslint 5 errors/14 warnings → **17 errors/22 warnings** |

A `PostToolUse` hook also reported "Another chat's dev server is running in this
folder." The new errors are in files that did not have them an hour ago
(`intel/companies/page.tsx`, `watchlist/page.tsx`, `market/unusual/page.tsx`).

Continuing to drive `web/` to green would (a) destroy another agent's in-flight
work — which the operating rules forbid — and (b) produce a green that is stale
within a minute. Stopped contending. **Resume when `web/` is quiescent**, then:

```bash
cd web && npx eslint . --max-warnings 0
```

Remaining known errors at 17:44, all in the other session's files: 3×
`no-require-imports` (`scratch_check_page.js`, `scratch_check_types.js`), 3×
`react-hooks/static-components` (`intel/companies/page.tsx` — `SortArrow`
declared inside render), 2× `react/no-unescaped-entities`
(`market/unusual/page.tsx:219`), 7× `no-explicit-any` + 2×
`set-state-in-effect` (`watchlist/page.tsx:44,140`).

## Item 7 — root cause located, fix not yet written

`daemon/internal/api/version.go:27`:

```go
"resolvable": lineage.BuildRevision() != "" && !lineage.BuildModified(),
```

Both operands are **build-time** facts. The endpoint reports `resolvable: true`
whenever a revision string was stamped into a clean build — it never re-checks
that the stamped revision still resolves in the authoritative source. A revision
that was rebased away, force-pushed over, or lives only on a deleted branch still
reports `true`. That is precisely the "trusts a build-time `resolvable: true`"
defect.

**FIXED — commit `6c1e2b1`.** `lineage.RevisionResolvable(ctx)` now asks git, per
request, whether the stamp still names a commit the repository contains. It fails
closed exactly like the Python precedent it mirrors
(`tools/accuracy_registry.py:518 revision_resolvable()`): empty stamp, dirty
build, non-40-hex stamp, absent git, directory that is not a checkout, cancelled
context, or a 2s timeout all report **false**, never true.

Deliberately **not cached**: a cached `true` is precisely the stale claim the
field exists to rule out, and `/api/version` is a diagnostic, not a hot path.
The prompt permitted a freshness-bounded cache; declining it is the stricter and
simpler option.

Files: `daemon/internal/lineage/lineage.go` (+`RevisionResolvable`, testable core
`revisionResolvable`, `revisionResolveTimeout`), `daemon/internal/api/version.go`
(endpoint now calls it with the request context),
`daemon/internal/lineage/resolvable_test.go` (new).

Verification:

```
cd daemon && go test ./internal/lineage/... -run TestRevisionResolvable -v   -> exit 0, 5/5 PASS
cd daemon && go test ./internal/lineage/...                                  -> ok  1.814s
cd daemon && go test ./internal/api/...                                      -> ok 19.635s
cd daemon && go vet ./internal/lineage/... ./internal/api/...                -> exit 0
cd daemon && go build ./...                                                  -> exit 0
```

The acceptance criterion is `TestRevisionResolvableRejectsAStampThatNoLongerResolves`:
a clean, well-formed 40-hex stamp that the repository does not contain. Under the
old build-time form that input returned `true`; it now returns `false`.
Dirty-build and empty-stamp refusals are preserved and covered by
`TestRevisionResolvableRejectsDirtyAndEmptyStamps`.

## Item 7b — BLOCKED: cannot produce a clean attributable binary

The post-commit rebuild runs and succeeds, but the binary is **not attributable**:

```
cd daemon && go build -ldflags "-X ...lineage.ldflagsRev=$(git rev-parse HEAD)" \
  -o ../bin/signaldeckd.exe ./cmd/signaldeckd      -> exit 0
go version -m ../bin/signaldeckd.exe
  build vcs.revision=6c1e2b1e39405370506da1d783a5b225f70d0eaf
  build vcs.modified=true        <-- dirty
```

`vcs.modified` reflects the **whole working tree**, and the concurrent session's
uncommitted `web/` changes keep it dirty. So `RevisionStamp()` yields
`6c1e2b1e...+dirty` and `RevisionResolvable()` correctly reports **false**.

Forcing this green would mean committing another session's in-flight work, which
is not mine to commit. Resume once `web/` is quiescent and its owner has
committed: re-run the build above and confirm `vcs.modified=false`.

## Environment — race detector RESOLVED

Was blocked: `-race` requires cgo and no C compiler was on PATH. Installed
`BrechtSanders.WinLibs.POSIX.UCRT` (gcc 16.1.0) via winget. **winget does not shim
`gcc` onto PATH** — it lands in the package directory and must be added manually:

```bash
export PATH="$PATH:/c/Users/Nicholas_N/AppData/Local/Microsoft/WinGet/Packages/BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe/mingw64/bin"
CGO_ENABLED=1 go test -race ./internal/...
```

Verified working: `internal/lineage` → ok 12.428s, `internal/workers` → ok 20.645s.

## Phase 4 — the snapshot is stale; Steps 1 and 2 are essentially done

### Step 1 (ScheduledWorker + NextFire scheduler) — ALREADY IMPLEMENTED

`daemon/internal/workers/schedule.go` (130 lines) already defines exactly what
Step 1 asks for, and the runner already drives it:

- `ScheduledWorker` interface embedding `Worker`, with
  `NextFire(last, now time.Time) time.Time`. The signature takes **`last` as well
  as `now`** — richer than the prompt's `NextFire(now)`, and necessary: `last` is
  seeded from `worker_runs` so a restart cannot re-fire a weekly job.
- `scheduledLoop` (workers.go:312) computes the next real instant, sleeps to it,
  runs, repeats — no ticker, no stagger.
- `Interval()` retained as the fallback; returning the zero time from `NextFire`
  declines and falls back, which is treated as a valid answer rather than an error.
- Guards already present: `minScheduledGap` (1m) floors a `NextFire` that returns
  the past forever so a buggy worker cannot hot-loop the fleet; `maxScheduledGap`
  (24h) caps one sleep hop so a 7-day backoff still re-evaluates daily against
  clock/config change.
- Periodic path keeps a measured anti-stampede stagger (rationale dated
  2026-07-16: phase-locked tickers put 10 heavy workers on a 5.5GB DB at once and
  produced a 61s `/api/honesty`).

The `WHY` block is dated **2026-08-02 — today**, so this landed in a session
before or alongside this one. No Step 1 work is needed.

### Step 2 (precise schedules) — ALL SIX NAMED TARGETS ALREADY MIGRATED

`cmd/signaldeckd/scheduled_assert.go` is a compile-time tripwire proving each one
still implements `ScheduledWorker` (it exists because the interface is optional
and discovered by type assertion, so a dropped `NextFire` would otherwise fail
silently). Verified implementations:

| Worker | NextFire at |
|---|---|
| finra-shorts | `internal/pipeline/shorts.go:78` |
| finra-shortint | `internal/pipeline/shortinterest.go:68` |
| cot-poller | `internal/pipeline/cot.go:85` |
| 13f-poller | `internal/pipeline/signal8.go:475` |
| signalbt-weekly | `internal/briefing/signalbtpin.go:71` |
| weekly-report | `internal/briefing/weekly.go:336` |

### congress-poller — deletion NOT performed; needs the owner's decision

Step 2 says delete it once the upstream mirrors are proven dead. The mirrors *are*
dead — `internal/pipeline/congress.go:75` records both free mirrors
(senatestockwatcher.com, housestockwatcher.com) going down in 2026-07, DNS gone
and S3 403. **But the waste the deletion was meant to remove is already gone.**
It was deliberately rebuilt as a circuit breaker: healthy → once daily 09:00 ET;
failing → exponential backoff from 12h out to `congressBackoffMax` = 7 days, with
the dead-run counter held in-process only so a restart re-probes once ("a restart
is exactly when a human may have fixed the source").

Deleting it now would discard a documented, measured decision and remove the
auto-recovery path, to save roughly one request per week. The prompt's own
precondition — stop paying for a known-403 twice a day — is already satisfied by
different means. **Left in place pending an explicit decision.** Nothing was
removed; this is reversible either way.

### Fleet size — the claimed 93 is NOT confirmable by static analysis

The fleet is assembled conditionally in `cmd/signaldeckd/run.go`: a 32-entry
literal plus 46 `fleet = append(fleet, ...)` sites, several gated on configuration
(Alpaca credentials present, backup configured, LLM client available). Crude
name-literal counts land at 79/96/102 depending on the pattern used, and none is
93. **The count is configuration-dependent, so no single static number is
honest.** To confirm it, enumerate at runtime from the Agents page / worker
registry on a configured instance rather than trusting any grep — including these.

## Phase 4 baseline (reproducible, required before edits)

Static extraction pairing each type's `Name()` literal with its `Interval()`:

```
workers with Name()+Interval(): 91   (+4 whose Interval is not a literal)
TOTAL periodic wakeups/day:     10,941
```

So the claimed fleet of **93 is approximately right** (91 parsed + 4 unparsed =
95 candidates, some conditional on configuration). Top consumers:

| Worker | Interval | wakeups/day |
|---|---|---:|
| cache-warmer | 60s | 1,440 |
| hud-sync | 1m | 1,440 |
| signal-runner | 1m | 1,440 |
| tv-quotes | 1m | 1,440 |
| universe-live | 1m | 1,440 |
| alert-runner / anomaly-scanner / downsampler / dq-auditor | 5m | 288 each |

Five workers at 1m account for **7,200/day — 66% of all fleet wakeups.**

## Step 3 — inventory done; consolidation MEASURED AS NET-NEGATIVE, not performed

The prompt's list is stale and internally inconsistent, as it warned it might be.
`honesty-gap` is **not a worker**: `cmd/signaldeckd/run.go:1592 honestyGapWorkers()`
is a factory returning six workers, and `FeatureHealthGrader` (`feature-health`)
is **already one of them** — yet the prompt lists `feature-health` separately as an
addition to "the honesty-gap tiers". The true candidate set is ten workers, not
the eleven the prompt implies:

| Worker | Interval | wakeups/day |
|---|---|---:|
| dq-auditor | 5m | 288 |
| canary | 1h | 24 |
| source-audit | 1h | 24 |
| model-health | 1h | 24 |
| return-distribution | 2h | 12 |
| feature-health | 6h | 4 |
| dataset-version | 6h | 4 |
| self-audit | 6h | 4 |
| feature-redundancy | 24h | 1 |
| price-validator | 24h | 1 |
| **total** | | **386** |

An `IntegrityOrchestrator` whose `NextFire` returns the earliest matured sub-check
must still wake at the tightest sub-cadence — dq-auditor's 5m — so it wakes
**288/day**. The saving is **386 → 288 = 98 wakeups/day, 0.9% of the fleet's
10,941.**

Against that 0.9%, the orchestrator would have to **reimplement inside one worker**
everything the runner currently provides per worker for free: panic isolation,
per-worker `runTimeoutFor` deadlines, independent `worker_runs` history (which the
Agents page and the watchdog read), independent failure reporting and metrics. The
prompt itself requires all of these be preserved. The failure mode of getting it
wrong is *an integrity check silently stopping* — precisely the risk
`cmd/signaldeckd/scheduled_assert.go` was written to guard against, and precisely
what this codebase's honesty discipline exists to prevent.

**Verdict: not implemented.** 0.9% wakeup reduction does not justify concentrating
ten independently-isolated integrity checks behind one point of silent failure.
This is the prompt's own rule applied — "reject changes that merely move cost or
weaken semantics."

**The measurement points at a different target.** `dq-auditor` alone is 288/day —
75% of the whole integrity family — so its cadence is worth more than the entire
consolidation. And Step 5's `cache-warmer` removal is **1,440/day, 13% of all fleet
wakeups: 14× the entire Step 3 payoff**, with a detectable failure mode (stale
cache) rather than a silent one. Recommended order: Step 5 first, then the 1m
cohort, then revisit Step 3 only if profiling shows the wakeups themselves matter.

### Step 4 — NOT STARTED

### Step 5 — INVENTORIED; INAPPLICABLE. Warmer NOT removed.

**Correction to this ledger's earlier recommendation.** It ranked Step 5 as the
highest-value Phase 4 item at "14× the Step 3 payoff" on the strength of
`cache-warmer`'s 1,440 wakeups/day. That ranking used wakeup count as a proxy for
cost, and the inventory Step 5 itself demands shows the proxy fails precisely
here. The wakeups are nearly free; what they prevent is not.

`cache-warmer` is **not a row cache and has no write path.** It is a
read-through/SWR precompute warmer for expensive *analytical* API builds
(`internal/api/warm.go`). Measured costs recorded in that file:

| Endpoint | Cold build |
|---|---|
| `/api/predictions/latest` | ~45s (latest-per-symbol self-join over 240k rows) |
| `/api/track-record` | ~22–44s per horizon |
| `/api/composite/top` | ~40s |
| `/api/datastats` | >30s |
| `/api/honesty` | ~22.7s |
| `/api/calibration` | ~22.6s |
| `/api/macro` | ~5.9s |
| `/api/regimes` | ~1.5–4.6s |
| `/api/xs-factor` | full cross-section from ~300 trailing bars per active symbol |

**Write-through is not implementable against these.** They are whole-database
aggregates: one ingested bar changes track-record, composite/top, xs-factor and
predictions/latest at once. There is no bounded mutation → cache-entry mapping to
write through, and no sane write path performs a 45-second self-join per row.

**Removing the warmer reintroduces a measured regression.** `warm.go:3` records
the original defect (2026-07-18): the first `/api/dashboard` and `/api/movers`
after a restart took **30–55s**, then 3ms once hot, because the caches were only
ever filled *by a request* — so the first visitor always paid. The warmer exists
to make that never happen.

**The 60s cadence is correct, not arbitrary:** `dashboardTTL = 60s` and
`respCacheTTL = 60s`, so 60s is the *minimum* cadence that keeps the shortest-TTL
caches continuously hot. Waking slower lets them lapse onto a visitor. On a hot
cache a pass "costs two map lookups" (warm.go:39).

There is also **no external cache to write through to** — no Redis; every `redis`
grep hit is the substring in `redistribution`/`rediscover`. All caches are
in-process Go maps with TTL/SWR semantics, and several already key on
`d.St.CacheKey()`, a store-generation token — i.e. generation-based invalidation
is already present.

**Step 5's own precondition forbids the removal:** "Remove the 60-second
cache-warmer *only after* all write paths and recovery behavior are covered."
There are no such write paths to cover, so the gate can never be satisfied.
**No change made.** Implementing Step 5 as written would trade ~13% of scheduler
wakeups — which cost two map lookups each when hot — for 22–45s user-facing page
loads.

The Item 7 change adds no new shared mutable state — `readBuild()` already
serialises through `sync.Once`, and `revisionResolvable` is a pure function plus
an `exec` call — so no race claim is being made or needed here.

---

## STOP CONDITION — the concurrent session moved into `daemon/`

At 17:33 the other session was confined to `web/`. It has since committed that
work (`d0e39b7` "Web v4 Cinematic Terminal redesign") and moved into `daemon/`.
Working tree at close: **58 changed paths**, including

- `daemon/internal/workers/workers.go` — the scheduler itself
- `daemon/internal/workers/workers_test.go`, `degraded_test.go`
- `daemon/cmd/signaldeckd/run.go` — the worker registry
- `daemon/internal/store/store.go`
- deletions under `quarantine/incomplete-2026-07-27/` (3 files)

`workers.go` and `run.go` are exactly the files Phase 4 Steps 4–5 must edit.
Editing them concurrently would conflict and risk clobbering that session's work,
which the operating rules forbid. **Phase 4 Steps 4–5 are therefore blocked on
coordination, not on difficulty.**

Verified intact at close: `daemon/internal/lineage/` and
`daemon/internal/api/version.go` are unmodified by anyone else and
`RevisionResolvable` is present in both. Item 7 survived.

Worth a look by the owner: the `quarantine/incomplete-2026-07-27/` deletions.
These appear to be quarantined *source* files, not the null/narration quarantine
*records* the integrity rules protect — but a deletion inside a quarantine
directory is worth confirming was intended. Flagged as an observation, not as a
confirmed violation, and not reverted.

## Where this stands

| Item | State |
|---|---|
| 1 offsite backup | BLOCKED — one physical disk |
| 2 digest helper | PASS — was already green, verified genuine |
| 3 eslint | Kit.tsx defect fixed; remainder was the other session's, now committed |
| 4 pre-publish scan | NOT DONE — needs a quiescent tree |
| 5, 6 live session | BLOCKED — next window Mon 2026-08-03 09:30 ET |
| 7 revision resolvability | **PASS — `6c1e2b1`** |
| 7b attributable build | BLOCKED — tree dirty from the other session |
| 8 service supervision | NOT STARTED |
| P4 Step 1 scheduler | Already implemented before this session |
| P4 Step 2 schedules | Already implemented (6/6); congress-poller kept by decision |
| P4 Step 3 integrity | Inventoried + measured; **net-negative, not implemented** |
| P4 Steps 4–5 | BLOCKED — concurrent editor in `workers.go` / `run.go` |
| Phase 5 audit | NOT STARTED |
| Phase 6 research | NOT STARTED |

Recommended next action when the tree is quiescent: **Step 5 (remove
`cache-warmer`)** — 1,440 wakeups/day, 13% of the fleet, 14× the entire Step 3
payoff, with a detectable rather than silent failure mode.

## Integrity statement

No check, threshold, assertion, refusal, quarantine, or provenance rule was
weakened, suppressed, re-pinned, or bypassed. No lint rule was disabled and no
suppression comment was added. No `SIGNALDECK_ALLOW_DIRTY_BUILD` was set. The
alphax gate, null/narration quarantine, and README `WITHHELD` rows were not
touched. Nothing was committed.

---

# Session 2026-08-02 22:00–22:20 PDT — "fix them all"

## The headline: F-1 was never actually fixed *on this machine*

The prior entry recorded F-1 (backup verifies structure, not content) as closed
and Item 1 as blocked on hardware. Both readings were wrong in the same way, and
the error hid a live CRITICAL.

Chain of facts, each verified:

| Fact | Evidence |
|---|---|
| The content check lives ONLY in `ops/signaldeck-backup-offline.sh` | `grep verify_backup.py` — one call site |
| That script last ran **2026-07-29**, on the Mac | `logs/backup-offline.log` tail — paths are `/Users/natalienyaung/...` |
| It cannot run here: `caffeinate` and `sqlite3` are both absent | `command -v` — MISSING; line 90 is `caffeinate -i sqlite3 "$DB" "VACUUM INTO ..."` |
| So on Windows the in-daemon Go worker is the ONLY backup path | Jul-31 and Aug-2 `.db` files exist with no matching log lines |
| And that worker verified **structure only** | `backup.go` `verifyBackup()` = `PRAGMA quick_check`, nothing else |

`quick_check` is exactly what the 2026-08-01 backup passed while carrying 14
`prediction_ledger` rows against a live 261,164. The guard raised to catch that
was sitting in a script that has not executed in four days, on an OS that cannot
execute it. Every nightly backup since the move to Windows has been recorded as
good without anything ever checking it still contained the audit trail.

This is not a hardware problem and never needed an external disk.

## FIXED — content verification now runs in the path that actually runs

`daemon/internal/backup/content.go` (new) — `verifyContent(ctx, backupPath,
liveLedgerCount)`, porting `tools/verify_backup.py`'s checks into Go:
`prediction_ledger` present and populated, `ledger_anchors` present with rows,
ledger not behind its own anchor, and not stale against the live count.
All problems are collected into one verdict rather than returning at the first.

`daemon/internal/backup/backup.go` — called after `quick_check` and, critically,
**before** `prune()` and before `backup_last_ts` advances, preserving the H8
ordering: a gutted copy can never rotate away a good generation nor move the
recorded-good pointer. Failure **quarantines** rather than deletes (the failed
copy is the only evidence of why it failed), records `dq_events(backup_gutted)`,
and pages.

### One semantics correction, deliberately not a weakening
The Python original asserts `prediction_ledger >= 1` absolutely. Ported
literally, that refused to back up a legitimately empty database — it failed 7
existing package tests whose fixtures are empty-but-correct stores. Emptiness is
only a fault *relative to the live database*, so the empty-ledger and
missing-anchor-rows checks now fire only when `liveLedgerCount > 0`. The Aug-1
shape is still caught, by two independent checks (see evidence below). An
unreadable live count reads as 0 and skips the staleness comparison — not
knowing how many rows there should be is not evidence the copy is short.

### Evidence
```
go vet ./internal/backup/                       -> exit 0
go test ./internal/backup/ -count=1             -> ok (8 new + 7 pre-existing)
go build ./...                                  -> exit 0
```
Against the REAL database, not fixtures:
```
LIVE          prediction_ledger=275097  newest_anchor=263345
BACKUP Aug2   prediction_ledger=270913  newest_anchor=263345  -> ACCEPTED (no false positive on 2.9 GB)
Aug-1 shape (14 rows, no anchors) vs live 275097 -> REJECTED:
  "missing ledger_anchors table (anchor); backup prediction_ledger count 14 is stale against live count 275097"
```

## Findings re-checked and found ALREADY fixed (snapshot was stale)
- **F-3** tunnelConfigured constant — repo-relative path is gone; `config` tests pass.
- **F-4** drift-without-level — `calibrationAtChanceThreshold = 0.49` and an
  `at_chance` level status exist in `pipeline/selfaudit.go`; tests pass.
- **F-8** `/api/accuracy` 404 — closed by inspection, not by code: the path
  appears nowhere outside the audit documents. Nothing advertises it, so there
  is no defect to fix.

## Still open, with the honest reason
| Item | Why not done |
|---|---|
| F-2 offsite on separate media | Still exactly one physical disk (`Get-PhysicalDisk`, DeviceId 0). Genuinely hardware. |
| `ops/*.sh` macOS-only | `caffeinate`/`osascript`/`stat -f`/iCloud paths. Now downgraded from CRITICAL to cleanup: the Go path carries the guard. |
| F-6 act on `dataset_revised` | Needs the grading surface in `store.go`, held by the concurrent session. Nothing consumes the event today. |
| Items 5, 6 live ticks | Market shut. Next window Mon 2026-08-03 06:30 PDT / 09:30 ET. |
| Item 8 supervision | Installing a service/scheduled task is a system-settings change — owner's call. |
| P4 Steps 4–5 | `workers.go` / `run.go` still the concurrent session's working set. |

## Concurrency note — my files were swept into someone else's commit
Commit `86aea84` ("Web v4 cleanup, store/worker fixes, and self-improve loop
hardening") was made by the concurrent session with a repo-wide add and captured
both of my new files plus the `backup.go` edit. The content committed is the
final verified version (`git diff HEAD -- daemon/internal/backup/` is empty and
the suite passes at HEAD), so nothing is lost — but this change is **not**
described by that commit message. Recorded here because the commit log alone
will not tell anyone the backup guard landed.

## Integrity statement
No check, threshold, assertion, refusal, quarantine, or provenance rule was
weakened, suppressed, re-pinned, or bypassed. The one semantics change is argued
above and makes the check strictly more accurate, not more permissive; the
failure it exists to catch is still caught, twice. No lint rule was disabled, no
suppression comment added, no `SIGNALDECK_ALLOW_DIRTY_BUILD` set.

---

# Session 2026-08-02 23:00–23:30 PDT — "fix the rest"

The concurrent session committed `web/` and `daemon/` and moved into `ops/`,
which freed the surfaces below. It is running its own remediation pass
(`REMEDIATION_2026-08-03.md`) and is presently building
`ops/install-windows-tasks.ps1` + `ops/signaldeck-ctl.sh` — i.e. it is doing
Item 8 and the macOS-only script port. Those are deliberately left to it.

## Closed this session

| # | What | Evidence |
|---|---|---|
| **Item 3** | eslint gate | `cd web && npx eslint . --max-warnings 0` → **exit 0** |
| **C-2** | the two live-record surfaces now name each other | `6082e17`, 2 tests |
| **A-2** | `/api/honesty` day-declusters its IC interval | `ddde1f7`, 3 tests |
| **C-1** | calibration states the floor below which a bin is not evidence | `11afe1a` |
| **F-8** | closed by inspection — no route ever existed, nothing references it | agrees with the other session's finding 6 |

### A-2 — the correction existed; it was never called here
`/api/honesty` published an IC over 880 independent symbol-days spanning **five**
market days with no distinct-day count and no interval. Dedup to one row per
(symbol, UTC-day) kills intraday pseudo-replication and leaves the real problem
untouched: on one day every symbol shares one move.

The IC is a *correlation*, so `clusterstat.DesignEffect` — defined on
proportions — would be the wrong instrument, and shipping one anyway is exactly
the "looks corrected" failure that package warns about. `BootstrapStat`
resamples **whole days** and recomputes the statistic; its own doc names
correlations as the case it exists for.

**A real bug the test caught:** `BootstrapStat` accepts any `numDays >= 2` — the
`MinDistinctDays` refusal lives in `BootstrapDays`, not in it. My first version
inherited no floor and produced a `[1.0, 1.0]` interval off five market moves.
The floor is now enforced explicitly at the call site, with a comment saying
why. Corrected: the interval. Unchanged: the point estimate.

### C-2 — both numbers were right; neither payload admitted the other existed
Paired `scopeNote` constants defined side by side (the failure mode is the two
surfaces drifting apart in what they claim about each other). No figure moved.
Both notes end on the verdict the two scopes agree about — no measured
probabilistic skill on either reading — because that agreement is what makes the
difference safe to publish.

### C-1 — a reporting gap, not a gate breach
A bin of N=2 shipped in the same array, same shape, as one of N=3,469. Every bin
already carried its `N`; nothing said what `N` is too thin to read. Publishes
`binMinN=30` — at n=30 the binomial SE on a proportion is ~9.1pp, already wider
than the miscalibration the curve exists to show — and a note built *from* the
constant so the two cannot drift. Explicitly **not** a `MinCalibrationPairs`
breach: that gate governs fitting and is holding. This gates reading.

Worth recording: `web/.../ReliabilityDiagram.tsx` already sizes each dot by `N`,
so the primary consumer was never treating the bins as equal. The gap was the
API contract, which is what a *new* consumer reads.

## Still open, with the honest reason

| Item | Why | Resume |
|---|---|---|
| **F-2** offsite media | One physical disk. `Get-PhysicalDisk` → DeviceId 0 only. | attach an external drive |
| **Items 5, 6** live ticks | Market shut. | Mon 2026-08-03 06:30 PDT |
| **Item 7b** attributable build | Verified again this session: `vcs.modified=true` from the other session's 15 uncommitted paths. | after it commits |
| **Item 4** pre-publish scan | Fails on exactly one line — `ops/` holds files absent from HEAD (`lib-portable.sh`, `test-lib-portable.sh`, …). Everything else in the scan passes. | after it commits |
| **F-6** `dataset_revised` | Needs the grading surface in `store.go`/`pipeline`, which the other session is editing now. Nothing consumes the event today. | after it lands |
| **Item 8** supervision | The other session is writing `ops/install-windows-tasks.ps1`. | its call, not mine |
| 3 symbols lack a naive baseline | Real in-universe data gap, not a code defect. | — |

## Integrity statement
No check, threshold, assertion, refusal, quarantine, or provenance rule was
weakened, suppressed, re-pinned, or bypassed. A-2 and C-1 both ADD refusals.
No lint rule was disabled, no suppression comment added, no
`SIGNALDECK_ALLOW_DIRTY_BUILD` set by me — note the running daemon was found
under it, as the other session records. `gofmt` reports ~48 pre-existing
unformatted files in `internal/api` (a CRLF artifact present at HEAD); I
formatted only the files I added and did not reformat the package, which would
have produced a large diff against an active editor.
