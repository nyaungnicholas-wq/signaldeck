# SignalDeck — Final Completion Report
**Date:** 2026-08-06 · **Branch:** `hmm-regime-and-pbo` · **HEAD:** `0b1f73b` · **Running daemon:** `0499416`

---

## 1. Executive summary

This run imported the prior audit (170 findings), adversarially re-verified the Critical/High tier,
and implemented **six evidence-backed repairs**, each with a runnable check. Every repair is
source-only and reversible. **No database was written, no service restarted, no binary rebuilt, no
git state changed, nothing deleted.**

The central fact has not moved and no repair could move it: **SignalDeck's flagship predictor has
negative measured skill, and the system's own grader says so** (`directional-ensemble (1d)`:
44.27% vs a 59.73% null, skill −0.155, verdict FAILED, `retired: true`). I reproduced no-edge
independently over 295,292 resolved outcomes. That is a research result, not a defect to repair.

The second fact is that **what is running is not what has been fixed.** The live daemon is 22
commits behind HEAD. The most consequential single item in this entire engagement —
`SETTLEMENT GUARD`, which stops 1d labels being frozen against unfinished forward bars — exists at
HEAD and is **absent from the running revision** (verified: `git show 0499416:…predict.go | grep -c` → 0;
HEAD → 1). HEAD's own comment measures the damage: *"37.8% were frozen before their forward bar's
16:00 ET close… about 3.7% of the whole 1d record labeled against a price that had not happened
yet."* The running process is still producing contaminated labels. **A deploy, not a repair, is the
highest-value action available, and it needs your approval.**

**Sharpened during this run:** the daemon *restarted* mid-session (pid 22220 → **33844**, started
13:35:02) and still reports revision `0499416`, because `bin/signaldeckd.exe` is untouched since
2026-08-05 22:58. **Restarting does not close this gap — only a rebuild does.** Anyone who "fixes"
this by bouncing the service will believe it is resolved and be wrong.

Three findings self-resolved mid-run: the accuracy task recovered on its 14:05 cycle (exit 0), the
26-hour staleness deadline was averted, and README's softened verdict was regenerated to
`FAILED — significantly worse than the naive baseline`. Two audit findings were **REFUTED** on
re-verification, both by the system's own logs.

---

## 2. Imported prior results

| Source | Date | Content | Reconciled |
|---|---|---|---|
| Prior audit (this session, 14 agents) | 2026-08-06 | 170 findings: 14C / 52H / 66M / 27L / 11I | Yes — deduplicated into the task graph |
| `data/accuracy_registry.json` | 2026-08-05 23:00, then 2026-08-06 21:05 | 13 graded rows; flagship RETIRED | Yes — re-read post-regeneration |
| `grader_heartbeats` (live DB) | ids 7–10 | 1 failure (21:05 08-05), successes either side | Yes — refuted a finding |
| Windows Scheduled Tasks (13) | live | 12 result=0; Accuracy recovered to 0 | Yes |
| `logs/` | live | grader log, daemon restart logs | Partially — sampled, not exhaustive |
| Existing repo audits (`audits/`, `SYSTEM_CHECK_*`, `REMEDIATION_*`) | 2026-07/08 | prior lineage | Partially — indexed, not re-litigated |

---

## 3. Repairs implemented — all VERIFIED

| ID | Finding | Files | Evidence |
|---|---|---|---|
| **R2** | `TradingDayAtET` could never match an evening hour → 2 FINRA feeds dead since 2026-08-02 | `marketcal.go`, `workers/schedule.go` (+test) | new `IsTradingDay`; test pins exact fire instants; `go test ./internal/workers/ -run TradingDayAtET -v` PASS |
| **R3** | Watchdog derived its alarm threshold from the schedule it polices (~30-day budget) | `cmd/signaldeckd/run.go` (+test) | `maxDerivedCadence = 8d` clamp + `slog.Warn`; 7/7 PASS; observed `derived=240h clampedTo=192h` |
| **R5** | Positions in de-activated symbols could be entered but never exited | `pipeline/paper.go` (+test) | exit-only re-admission; **negative control: test FAILS with fix disabled**, passes with it |
| **SD-H35** | `short_volume` absent from source-health registry → dead feed invisible | `srchealth/*` (agent lane) | registered with a 6-day market-gated budget; test fails before, passes after |
| **SD-012** | Docs-gate anchoring never executed (colon-form regex matched zero files) | `tools/docs_gate.py`, `tools/test_docs_gate_anchor.py` (agent lane) | 3 tests OK; forged-marker exploit still rejected |
| **NEW-1** | MCP security test failed **open** under load (~1 run in 256) | `internal/mcp/attack_test.go` | root-caused to `tampered := good[:len(good)-2]+"00"` being a no-op when the minted key already ends in `00`; guarded |

**NEW-1 deserves a note.** During the post-repair suite, an MCP *security* test failed with a
tampered key signature returning `200` and a full tool list — a textbook fail-open. It passed 3/3 in
isolation and was clean under `-race -count=2` and `-count=5 -cpu=1,2`. Rather than filing it as
"flaky", I read the fixture: the tamper overwrites the last two characters with the literal `"00"`,
which is a **no-op whenever the freshly minted key already ends in `00`** — about 1 run in 256. On
those runs the "tampered" key *was* the valid key and the server was behaving correctly. **The auth
path was never broken; the test was.** Now guaranteed to differ by construction.

---

## 4. Adversarial re-verification

All 9 fleet agents complete (1,210,642 subagent tokens, 477 tool uses, 0 errors).

**Two of my own Critical findings were downgraded by re-verification, and the corrections matter
more than the confirmations:**

- **SD-012 (docs gate) — impact INVERTED, severity Critical → Medium.** An agent instrumented the
  gate rather than reading the regex: it monkeypatched `_region_is_anchored` with a spy and found it
  is called **zero** times as shipped. But with no region recognised, `wellformed_regions` is empty,
  so region contents receive **more** scrutiny, not less. It then built a forged-marker copy of
  `EDGE_PLAN.md` containing *"Our live accuracy is 91%"* and ran the as-shipped gate — **it flagged
  it.** The exploit the code comment worried about is closed, by accident. What is genuinely lost is
  narrower: no drift check between an injected region and its partial, and no check that a region
  names a declared partial. My audit overstated this one.
- **SD-010 (IEX/SIP tape split) — data CONFIRMED, impact REFUTED, Critical → Medium.** The ~10×
  instrument gap is real and independently re-measured (stream=0 summed 1m volume = 97.0% of the
  daily bar; stream=1 = 9.9%). But the agent enumerated every `TF1m` consumer and found **no
  cross-sectional ranking or comparison on 1m/1h volume** — all consumers self-normalise per symbol,
  which is immune to a constant tape scale. It is a latent trap, not live contamination.

**REFUTED (both by the system's own logs, matching my independent findings):**
1. *"Accuracy task failed and no grading has run since 2026-08-05"* — true when written, self-cleared
   on the next cycle: `Last=8/6/2026 2:05:01 PM, Result=0`, heartbeat id=10 success.
2. *"The 2026-08-05T21:05Z grading refusal was reverted"* — it was **superseded 2h21m later by a
   legitimate successful grade**, not reverted.

**CONFIRMED (selected):** build drift (22 commits, deploy-gated); watchdog self-concealment; web app
not running and not schedulable; rank-preserving calibration publishing the whole cross-section on
one side of 0.5 and grading it as thousands of independent forecasts; train/serve skew (live
features on a *partial* daily bar vs a model fit on completed bars).

**Security re-check materially improved the picture:** the claim *"the daemon is reachable
off-host"* was **REFUTED** — it is loopback-only. The exposure claim was **partly confirmed with a
corrected mechanism**: `SIGNALDECK_PUBLIC_READS` and `SIGNALDECK_OPEN_SIGNUP` *do* default open on
this Windows host, but the guard is not macOS-only — only its tunnel-detection leg is.

---

## 5. Verification matrix

| Check | Command | Result | Proves | Does NOT prove |
|---|---|---|---|---|
| Build | `go build ./...` | **exit 0** | daemon module compiles at HEAD+repairs | nothing about the running binary |
| Vet | `go vet ./...` | **exit 0**, 1341 ms | no vet-class defects module-wide | not a linter; no logic guarantee |
| Unit (baseline) | `go test -short ./...` | exit 0 · 119 ok · 0 FAIL · 10 no-test | pre-repair health | `-short` **skips e2e by definition** |
| Unit (post-repair) | `go test -short ./...` | 118 ok · 1 FAIL (NEW-1, root-caused) | caught a real test defect | — |
| **Unit (final)** | `go build && go vet && go test -short -count=1 ./...` | **0 / 0 / 0 — 119 ok · 0 FAIL · 10 no-test** | all repairs green, baseline package count restored, `-count=1` defeats caching | still `-short`: **e2e never ran** |
| Race | `go test -race` on workers/store/api/ensemble/pipeline | clean | no races **on paths that executed this run** | ThreadSanitizer only sees executed interleavings |
| gofmt | `gofmt -l .` | 387 listed → **335 CRLF artifacts, 52 genuine drift** | byte-level state | CI never runs gofmt; nothing gates |
| DB integrity | 7 bounded read-only queries | 4 clean, 3 defects | logical constraints | **`PRAGMA integrity_check` NOT run** — page/B-tree corruption uncovered |
| Docs gate | `python tools/docs_gate.py check` | **exit 1, 2 violations** | the gate now actually works | see §7 |

---

## 6. Newly surfaced work (created by a repair working correctly)

Fixing SD-012 turned a gate that was silently protecting **nothing** into one that finds two real
violations: `STRATEGY_DECK.md:126` (`controls_evidence`) and `:199` (`deck_facts`) open generated
regions naming partials this repo does not produce — `partials/` contains only `live_accuracy.md`.
**CI will go red until this is resolved.**

I attempted the obvious fix — declaring both in `ops/docs-registry.json` `external_partials` — and
**reverted it**, because it only changed the failure message (the gate then demanded the partial
*files*, which do not exist) without fixing anything, and leaving a half-measure in a governance
file that grants accuracy-check exemptions is worse than leaving it clean. Resolving it properly
means running the two generators (which rewrite `STRATEGY_DECK.md`, a governed ACTIVE document) or
removing the markers. **That is your decision, not mine.**

---

## 7. Blocked items — require your approval

| ID | Action | Why it matters | Why blocked |
|---|---|---|---|
| **BLOCKED-1** | Rebuild `bin/signaldeckd.exe` and restart the daemon on HEAD | **Highest-value action available.** Deploys the settlement guard (stopping live look-ahead label contamination), the collapsed-cross-section refusal, ranking-based leg admission, the fleet-veto gate, plus R2/R3/R5. | Live-state change; 22 commits at once. Your rule 13. |
| **BLOCKED-2** | Quarantine the contaminated 1d label population and the paper equity curve | ~3.7% of the 1d record is labeled against prices that had not happened | **Database write.** Not exactly reconstructable → quarantine, never backfill. |
| **BLOCKED-3** | Web app: create a Windows task or formally retire the surface | The entire user-visible product is down and unschedulable | System/external change. |
| **BLOCKED-4** | Resolve the two `STRATEGY_DECK.md` generated regions (§6) | CI red until done | Rewrites a governed ACTIVE document. |
| **BLOCKED-5** | Configure a real alert transport | Nobody is told when anything breaks | External service + credentials. |

---

## 8. Remaining risks

1. **No edge.** Repairs fix correctness, not profitability. Directional accuracy is below base rate
   on both horizons and confidence carries no information (48.4→47.1→46.7→48.7→**50.0%** across
   confidence buckets). Nothing here changes that.
2. **~120 Medium/Low findings remain UNVERIFIED**; 2 of 9 verify lanes had not returned at writing.
3. **`PRAGMA integrity_check` was never run** on the 5.28 GB database — physical corruption is
   entirely uncovered by this run.
4. **Every repair is inert until deployed** (BLOCKED-1).
5. The working tree is being written by two autonomous scheduled tasks; `README.md` and
   `research/eighty/**` were treated as off-limits throughout.

---

## 9. Rollback

```bash
git checkout -- daemon/internal/marketcal/marketcal.go daemon/internal/workers/schedule.go \
  daemon/internal/workers/schedule_check_test.go daemon/cmd/signaldeckd/run.go \
  daemon/cmd/signaldeckd/staleness_test.go daemon/internal/pipeline/paper.go \
  daemon/internal/pipeline/paper_test.go daemon/internal/mcp/attack_test.go \
  daemon/internal/srchealth/ tools/docs_gate.py
rm -f tools/test_docs_gate_anchor.py   # new file
```
The running daemon is unaffected by everything in this run — which is also why none of it is live.

## 10. Reproduction

```bash
cd daemon && go build ./... && go vet ./... && go test -short -count=1 ./...
go test ./internal/workers/ -run TradingDayAtET -v
go test ./cmd/signaldeckd/ -run TestStalenessInterval -v
go test ./internal/pipeline/ -run "ExitsPositionInDeactivatedSymbol|DeactivatedSymbolNeverEntered" -v
python tools/test_docs_gate_anchor.py
```

---

---

## ROUND 3 — completeness critic, corrections, and one new live finding

The original 38-agent audit completed after delivery (5.4 M subagent tokens, 109 min) and its
completeness critic raised gaps. Acting on them produced three corrections and one new finding.

### NEW-2 — the live universe is 9× its intended size, right now · **CONFIRMED, HIGH**

`SELECT COUNT(*), SUM(active) FROM symbols` → **2950 / 2950**. Every symbol is active. Normal is
~328 (`logs/refresh.log`, 2026-08-05: *"pruned back to 328 active"*).

Cause: `SignalDeck Daily-Refresh` ran 2026-08-06 13:15 and was **terminated mid-sweep** —
`LastTaskResult 3221225786` = `0xC000013A` (STATUS_CONTROL_C_EXIT). `logs/refresh.log` ends at
`13:15:01 reactivated full universe: 2950 active` with **no prune line**. The sweep reactivates the
full universe first and prunes second; it died in between.

Live consequence, confirmed from `/api/agents` right now: `dq-auditor: checked 2950 active symbols`
(329 an hour earlier), `downsampler: rolled up 2950 symbols`. **Every worker reports `status: ok`.**
Nothing anywhere flags that the universe is nine times its intended size — the same silent-degradation
class as the FINRA outage. `signal-runner` is unaffected (still scoped to 43 hot symbols).

**Remediation requires a DB write or a task re-run → added to the blocked register.**

### Correction 1 — BLOCKED-1 as previously written was incomplete · **my error**

`git show HEAD:daemon/internal/marketcal/marketcal.go | grep -c "func IsTradingDay"` → **0**;
working tree → **1**. **All six repairs are uncommitted.** "Deploy HEAD" would therefore ship the 22
commits *without* R2, R3, R5, SD-H35, SD-012 or NEW-1. The skew is three-way — running binary
(`0499416`) vs HEAD (`0b1f73b`) vs working tree — not two-way. **BLOCKED-1 must be: commit the
repairs, then rebuild, then restart.** Stated wrongly in the round-1 report; corrected here.

### Correction 2 — the critic's "5 dead workers" is wrong; it is 2 · **critic REFUTED**

The critic escalated `cot-poller`, `signalbt-weekly` and `weekly-report` to the FINRA bug class
because all last ran at the same instant. That instant is a **daemon restart**, not a schedule
defect. `COTPoller.NextFire` (`pipeline/cot.go:85`) uses `WeeklyAtET(now, time.Saturday, 9, 0)` —
**not** `TradingDayAtET`, so R2's bug cannot reach it. 2026-08-02 was a **Sunday**; the next Saturday
is **2026-08-08**; today is Thursday 2026-08-06. **No Saturday has elapsed.** cot-poller is correct.
The genuine dead-worker count stays **2** (`finra-shorts`, `finra-shortint`).

### Correction 3 — the installer does not fail silently

`ops/install-windows-tasks.ps1` dry-run prints `SKIP  SignalDeck Web  no shell script in
ProgramArguments` and `6 skipped`. Root cause is that `ops/com.signaldeck.web.plist` still points at
a **macOS path** (`/Users/natalienyaung/…`). The failure was reported and unread, not silent. Not
patched: verifying a change requires creating a scheduled task (BLOCKED-3), and an unverifiable
repair is not a repair.

### Storage gate — ALREADY FIXED at HEAD, not a new repair

`ops/signaldeck-refresh.sh:132-143` already carries the corrected `bin/sdmaint$SDMAINT_EXE` lookup
plus a comment describing this exact defect. The `sdmaint not built` log lines are from 08-03…08-05,
before the fix. **NOT_APPLICABLE** as a repair target.

### Phase 11 — adversarial review of the six repairs (9 agents, 1.48 M tokens)

**All five reviews returned `REPAIR_INCOMPLETE`. None returned `REPAIR_WRONG` or `REPAIR_HARMFUL`** —
the repairs are correct but not sufficient.

- **R2 survived every attack.** The reviewer independently recomputed marketcal's holiday algorithm
  in Python and diffed it against the Go table (2026/2027 match real NYSE exactly), scanned every
  day 2022–2040 for the worst consecutive-closure run (**max 3**, so the 10-iteration bound has 2.5×
  headroom), scanned DST 2026–2035 for all six real fire times (**zero** non-existent/ambiguous
  hits), proved strict forward progress on every path, and confirmed the diff is purely additive so
  none of `OpenForBars`' 7 callers regressed.
  **But R2 does not recover the incident.** `short_volume` is stuck at 2026-07-31 with 08-03/04/05
  missing (all trading days) plus an interior hole at 07-29. `NextFire` discards `last`, `Run`
  fetches exactly one day, and the one-time backfill is already marked done — so the first post-fix
  fire jumps the cursor to the current day and **those four days are never fetched and never
  dq-recorded**. Worse, srchealth's `SELECT MAX(day)` (srchealth.go:58) is a *recency* probe blind
  to interior holes, so it flips green on the first success and the gap becomes permanently
  invisible. **The schedule is repaired; the data it lost is not, and its detector self-heals.**
- **R3's primary attack failed.** The reviewer enumerated all seven ScheduledWorkers and measured
  real derived cadence across 8,784 hourly boot instants in 2026: longest legitimate gap **169 h**
  (weekly + DST fall-back) against the **192 h** ceiling — **zero clamp hits on any live schedule**,
  confirming my own semi-monthly check. Residual: `StaleWorkers`' boot grace caps any threshold at
  daemon uptime, so the clamp's benefit is smaller than intended.
- **R5 survived every attack it was designed against** — no double admission, no cross-strategy entry
  leak, no wrong `ExecInputs.Market`, no ordering-dependent allocation, no `asof` deadlock — and the
  drawdown-flatten rung provably now reaches a held-inactive symbol end to end.

### My own test was vacuous — found by the review, fixed and re-proven

The reviewer found `TestPaperTrader_DeactivatedSymbolNeverEntered` **passed with the `!s.Active`
guard deleted**. Confirmed by deleting the guard and re-running: `ok`. Root cause: the fixture used
an inactive **unheld** symbol, which is never re-admitted, so the loop never saw it.

The guard is nonetheless load-bearing, via a path the test missed: `syms` is built once and shared
across strategies while `PaperPositions` is per-strategy, so a symbol held by `flagship-1d` reaches
`flagship-1w`'s **entry** path with `hasPos == false`. Rewritten to exercise exactly that, and
re-verified both ways — passes with the guard, **FAILS without it**:
`flagship-1w opened a NEW position in a symbol the universe had already dropped`.

This is the same defect class I criticised in `TestTradingDayAtETSkipsWeekend`, committed by me, in
the risk path. It is the strongest argument in this report for why the adversarial phase is not
optional.

### Medium/Low verification — 85 verified, 10 escalated to High

69 CONFIRMED · 10 PLAUSIBLE · 6 REFUTED · **10 escalated from Medium/Low to High**, including:
`bars` written `INSERT OR REPLACE` so a provider revision silently rewrites the price basis;
`.env.example` instructing the admin bearer token into a `NEXT_PUBLIC_*` variable; local group
`CodexSandboxUsers` holding read access to `daemon/.env` and `signaldeck.db`; two health surfaces
disagreeing at the same instant; `/api/companies` taking 19 s.

New High findings from failure-injection: a stream disconnect gap is **never healed** (the
reconciler's 1 m gate is a total row count that can never fire for an established symbol — measured
SPY 1 m bars/day of 5, 39, 49 against 388–397 on healthy days); single-symbol Alpaca fetch has **no
429 handling** with a confirmed live retry storm; the only restart/recovery e2e test **skips
entirely on Windows**; off-machine backup silently dead for 4 days.

### Gaps the critic raised that remain open

| Gap | Status |
|---|---|
| Frontend entirely unaudited (57 pages, 6 Playwright specs) | **BLOCKED** — app not running (BLOCKED-3) |
| `research/`: 257 `.py`, **0 test files** | **UNVERIFIED** |
| `dq_events`: **3,825 `stale`/day** unexamined | **UNVERIFIED** |
| `.env.bak-*` carries the same `CodexSandboxUsers` ACL; 8 worktree repo copies | **UNVERIFIED** |
| plists vs Windows tasks never reconciled (14 vs 13) | **UNVERIFIED** |

---

# NOT COMPLETE — required work remains

**Why, precisely:**
- 5 items are **BLOCKED on your approval** (§7), including the deploy that would make all six repairs
  live and stop ongoing label contamination.
- 2 of 9 verification agents had not returned; **~120 Medium/Low findings remain UNVERIFIED**.
- `PRAGMA integrity_check` was never run — a whole class of database defect is unexamined.
- The docs gate is **FAILING** by design after being repaired (§6), pending your decision.
- The completion gate requires every discovered task to be VERIFIED / FIXED / BLOCKED / NOT
  APPLICABLE. It is not, so the status is NOT COMPLETE. Declaring otherwise would be the exact
  failure this engagement was commissioned to prevent.

**Trust verdict, unchanged: SignalDeck cannot currently be trusted for live operation** — now for
one fewer reason than this morning, and with the single highest-value corrective action sitting one
approved deploy away.
