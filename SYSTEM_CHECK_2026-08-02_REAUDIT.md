# SignalDeck — Independent Re-Audit, 2026-08-02

Second, independent pass over `AUDIT_SUPERPROMPT.md`, run without reference to the
conclusions in `SYSTEM_CHECK_2026-08-02.md`. Every number below was re-measured in this
session; the command that produced it is quoted. Where this pass disagrees with the prior
one, the disagreement is stated explicitly.

Auditor posture: falsify, don't confirm. Findings are ranked by severity, not by order found.

- Repo: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `audit/2026-07-27`
- HEAD at audit start: `c2c5310`. HEAD after the one commit this pass made: `58e42b0`
- Host: Windows 11. **This matters more than expected — see F-2 and F-3.**

---

## VERDICT

> **REFUTED, on the live forward record.** Over 11,216 independent symbol-days spanning 22
> distinct trading days, directional accuracy is **50.01%** against an "always-down" naive
> baseline of **55.76%** — the model is **5.75pp worse than guessing the majority direction**.
> Brier skill is **−0.138** (worse than the base rate), IC is **−0.020** with a day-clustered
> CI of **[−0.0404, +0.0032]**, and the live calibration curve **slopes downward** across
> n=10,000. There is no measured edge; the point estimate is mildly *inverted*, and it is not
> statistically distinguishable from zero.

Plain sentence: **as of 2026-08-02 SignalDeck has no measurable directional edge, its
calibrated probabilities run backwards on the live record, and it is not something to trade
real money on.**

This is not a new failure — it is the honest reading of numbers the system itself publishes.
The system's gates are, on the whole, working; what is broken is the *backup* path and the
*self-audit's* ability to notice a stably-useless model (F-1, F-4).

---

## SCOREBOARD

| Axis | State | The one number that decides it |
|---|---|---|
| Alive | ⚠️ degraded | daemon was **not running** at audit start; starts clean once the tree is clean. 1 stale source of 19. |
| Honest | ✅ mostly | gates verified enforced: alphax OOS lift −0.0072 → **gated off**, not blended. Ledger intact, ed25519-anchored. |
| Edge | ❌ refuted | win rate **50.01%** vs naive **55.76%** → **edgeVsNaive −5.75pp**; Brier skill **−0.138** |
| Backup | ❌ critical | newest "off-machine" backup holds **14 ledger rows vs 261,164**, and is on the **same physical disk** |

---

## BUILD GATE (Definition-of-Complete items 1–3)

| Check | Result | Evidence |
|---|---|---|
| `go build ./...` | **PASS** | exit 0, module root is `daemon/`, not repo root |
| `go vet ./...` | **PASS** | exit 0 |
| `go test ./...` | **PASS** | exit 0 — **112 packages ok, 0 FAIL** |
| Daemon from `git archive HEAD` | **PASS** | `/api/version` → `{"resolvable":true,"modified":false,"revision":"58e42b0…"}` |
| `/api/health`, `/api/ready` | **PASS** | 200 and 200 |

⚠️ **Correction to my own first attempt**: my initial `git archive` build produced
`revision_stamp=""` and the daemon refused to start. That was *my* error, not a defect —
`ops/signaldeck-ctl.sh:build_from_head` stamps the commit via
`-ldflags "-X …/internal/lineage.ldflagsRev=$rev"` precisely because `git archive` drops
`.git`. With the stamp, attribution resolves. **The daemon's refusal was correct both times.**

---

## FINDINGS

### F-1 — CRITICAL — The newest off-machine backup is structurally valid and substantively empty

The backup written 2026-08-01 20:07 passes `PRAGMA quick_check` but has lost almost
everything that matters:

| | Jul 31 backup | **Aug 1 backup** | live DB |
|---|---|---|---|
| `quick_check` | ok | **ok** | — |
| tables | 100 | **97** | — |
| `bars` rows | 13,379,790 | **3,361,790** | — |
| `prediction_ledger` rows | 261,164 | **14** | 263,345 |
| newest anchor `ledger_count` | 260,419 | **none** | — |
| file size | 2.2 GB | **420 MB** | 2.3 GB |

A restore from the newest backup would silently discard 99.995% of the accountability
record — and `quick_check` reports `ok`, so a naive validation passes it.

`ops/restore-rehearsal.sh` *does* guard against this in principle, but its `bars` floor is
`MIN_BARS_ROWS=1000` (line 55) and the bad backup has 3.36M bars — **the floor does not
catch it**. Only the ledger-anchor cross-check (lines 147–166) would, and see F-2 for why
that check is not running on this machine.

Compounding: `/api/quality.ops.lastBackupFile` still reports the *Jul 31* file, so the
Aug 1 run neither succeeded nor was recorded as failed. It failed silently.

### F-2 — CRITICAL — "Off-machine" backup is on the same physical disk, and the guards are macOS-only

`/api/quality.ops` reports `offsiteConfigured: true` with

```
offsiteDir: C:\Users\Nicholas_N\Library\Mobile Documents\com~apple~CloudDocs\SignalDeckBackups
```

That is a **macOS iCloud Drive path recreated literally as ordinary folders on Windows**.
There is no iCloud Drive on this host — `C:\Users\Nicholas_N\Library` exists only because the
backup script created it. The 2.6 GB of backups sit on the same physical disk as
`data/signaldeck.db`. `offsiteConfigured: true` is true as a config flag and false as a fact:
**one disk failure still takes the whole project to zero**, which is the exact condition
Phase 5 of the audit protocol exists to catch.

Root cause is broader than one path. The entire operational layer is macOS-only and inert
here: 13 `com.signaldeck.*.plist` launchd agents, and `.sh` scripts that call `launchctl`,
`caffeinate`, and `shasum`. On this Windows host **nothing supervises the daemon, nothing
runs the backup, and nothing runs `restore-rehearsal.sh`** — which is why F-1 went unnoticed.

### F-3 — HIGH — `tunnelConfigured()` is a constant, so the "safe by default" heuristic never varies

`daemon/internal/config/config.go`:

```go
var tunnelAgentPaths = []string{
    "ops/com.signaldeck.tunnel.plist",                                  // repo-relative
    os.ExpandEnv("$HOME/Library/LaunchAgents/com.signaldeck.tunnel.plist"),
}
```

The first entry is **repo-relative and committed to the repo**, so `os.Stat` succeeds in every
checkout on every machine forever. `tunnelConfigured()` is therefore permanently `true`,
`reachablePrivately()` permanently `false`, and `PublicReads` permanently defaults off —
including on Windows, where launchd cannot run and no tunnel can ever start.

Severity is HIGH rather than CRITICAL because it **fails closed**: the effect is that reads
require auth when they need not. But the code's own comment claims the presence check
distinguishes exposed from private deployments, and it does not distinguish anything. The
second, genuinely machine-specific path is unreachable dead code.

*Audit-environment note*: to read the audit endpoints I restarted the daemon with
`SIGNALDECK_PUBLIC_READS=true` — the operator override the code comment explicitly sanctions
— on a loopback bind with no tunnel process running. No file was modified and no honesty gate
was touched.

### F-4 — HIGH — The self-audit detects *drift*, never *level*, so a stably-useless model reads "ok"

Every one of the 11 findings at `/api/self-audit` has `status: "ok"`. Several should alarm:

- `calibration:1d` = **0.4982** = mean |cal_prob − realized|. For a binary outcome that is
  exactly what a useless constant p=0.5 scores. Marked `ok` because it moved only +0.0007,
  and the rule flags at Δ>0.020.
- `factor_ic:meanrev` = **−0.1616** over 1,315 samples — a large, persistent *inverse* signal.
  Marked `ok` because `prior IC −0.1616` is identical.
- `factor_ic:pressure` = **−0.0317** over 40,000 samples — persistently negative, marked `ok`.

The self-audit compares each metric only to its own prior value. A metric that has been wrong
since the day it was born never changes, therefore never flags. **The system cannot currently
tell itself that it is at chance** — it can only tell itself that it has recently got worse.

### F-5 — HIGH — The live calibration curve slopes downward (the sign is inverted)

`/api/calibration`, populated bins only, n=10,000:

```
pred 0.3587 -> actual 0.5212   n=3469
pred 0.4632 -> actual 0.4732   n=3548
pred 0.5201 -> actual 0.4525   n=2981
```

As the calibrated probability rises by +0.161, the realized frequency **falls** by −0.068.
The curve is monotonically decreasing across the entire populated range on n=10,000. This is
the same fact as the negative headline IC (−0.020), seen through the calibration surface.

`/api/track-record.reliability`, which spans a longer window, shows it more starkly — the
0.70–0.80 bucket predicts 0.743 and realizes **0.379** on n=1,666.

Honest bound: the day-clustered IC CI is **[−0.0404, +0.0032]**, which **includes zero**. So
the correct claim is *no measurable edge, point estimate inverted* — **not** "a reliable
inverse signal to trade backwards." Two of the three bins straddle the base rate.

### F-6 — MEDIUM — Provider silently rewrote history for many symbols

`/api/quality.events` is dominated by `dataset_revised` records, e.g.

```
GLW 1d: provider rewrote history inside 1546405200..1785297600 (was n=1903, now n=1905)
 — claims measured on it must be re-graded
```

The system correctly *detects and records* this. What it does not do is act on the
instruction it writes: nothing re-grades the affected claims. Every accuracy number above is
measured partly on bars that have since changed underneath it.

### F-7 — MEDIUM — Survivorship exposure is acknowledged but unquantified

`/api/track-record.survivorship` self-reports `exposed: true`, "delisted names absent —
long-side persistence and mean-reversion stats are inflated by an unmeasurable amount."
This is honest labelling, not a fix. **Not yet independently re-verified this pass** — the
prior session's claim of 21 → 716 delisted symbols is listed under "not re-derived" below.

### F-8 — LOW — `/api/accuracy` returns 404

19 of 20 probed endpoints return 200. `/api/accuracy` 404s (19 bytes). Cosmetic unless
something documents it as live.

---

## WHAT I FALSIFIED

1. **The handoff prompt says "all 19 phases of `AUDIT_SUPERPROMPT.md`."** That file has
   **5 phases**, not 19. Re-read it before planning around a phase count.
2. **`offsiteConfigured: true` does not mean off-machine.** It means a path string is set.
   The path resolves to the same disk (F-2).
3. **`PRAGMA quick_check: ok` does not mean a backup is usable.** The Aug 1 backup passes it
   and is empty of ledger history (F-1).
4. **`self-audit: all ok` does not mean healthy.** It means nothing changed (F-4).
5. **`reachablePrivately()` is not a heuristic.** It is a constant on every machine (F-3).

## WHAT GENUINELY HOLDS (verified this pass)

- Build/vet/test all green — **112 packages, 0 failures**, re-run from scratch.
- The dirty-build refusal works and is not bypassable by accident. It caught a genuinely
  unattributable build twice in this session before I supplied the correct `-ldflags` stamp.
- **The alphax gate works as designed**: OOS lift −0.0072, AUC 0.5002 → `gated: true`,
  `"never blended and never displayed as a signal"`. A losing model is stored, not served.
- **The prediction ledger is intact and anchored**: 263,345 rows, `intact: true`, 4 ed25519
  anchors, and the endpoint volunteers the caveat that intact ≠ written-when-claimed.
- **Cluster correction is real and conservative**: design effect **3.61** *measured* from
  between-day variance, effective N **3,104** from a raw 11,216, and the endpoint additionally
  publishes the harshest framing (day-as-unit: 22 days, 10 correct, 45.5%).
- **The sentiment study is a genuine published negative result** — IC −0.005 to −0.018 across
  three horizons, all CIs including zero, Bonferroni-corrected, and it retires sentiment
  rather than burying it.

## THE EDGE STANDARD, ELEMENT BY ELEMENT

| # | Element | Verdict | Number |
|---|---|---|---|
| 1 | Out-of-sample & forward | ✅ met | `/api/track-record`, live resolved, `live: true` |
| 2 | Independent N ≥ 30, days ≥ 20 | ✅ met | N=11,216; **22 distinct days** (floor 10) |
| 3 | Net of costs | ❌ fails | paper book **−2.09%** over 29 days, turnover 1.78, 22 fills |
| 4 | Beats SPY risk-adjusted | ⛔ **BLOCKED** | SPY over the identical 29-day window not pulled this pass |
| 5 | Survives multiple testing | ❌ fails | nothing to correct — the uncorrected result is already negative |
| 6 | Stable across time | ❌ fails | calibration slope is negative on n=10,000 (F-5) |

Element 4 is moot in practice: a strategy that is −2.09% absolute and below its own naive
baseline cannot beat SPY on any risk adjustment. It is marked BLOCKED rather than FAILED
because I did not measure it.

---

## RANKED FIX LIST

| # | Sev | Fix | Cost of ignoring |
|---|---|---|---|
| F-1 | CRITICAL | Make the backup verify *content*, not just structure: assert `prediction_ledger` count against the newest anchor's `ledger_count`, and fail the run loudly. Quarantine the Aug 1 file. | A restore silently loses the entire accountability record. |
| F-2 | CRITICAL | Point `offsiteDir` at genuinely separate media, and port backup + restore-rehearsal to run on Windows. | One disk failure = total loss, today. |
| F-4 | HIGH | Add absolute-level thresholds beside the drift thresholds (flag calibration ≥0.49, flag \|IC\| persistently negative). | The system cannot report its own uselessness. |
| F-3 | HIGH | Drop the repo-relative path; key the tunnel check on a machine-specific location or on `SIGNALDECK_ASSUME_TUNNEL` alone. | A safety heuristic that is really a constant will mislead the next auditor. |
| F-5 | HIGH | Do **not** flip the sign. Investigate why the map inverted; the CI includes zero. | Trading it either direction is trading noise. |
| F-6 | MEDIUM | Act on `dataset_revised`: re-grade affected claims, or mark them provisional. | Accuracy numbers rest on bars that changed. |
| F-8 | LOW | Remove or implement `/api/accuracy`. | Cosmetic. |

---

## WHAT I COULD NOT VERIFY

| Item | Why | When it can be settled |
|---|---|---|
| Live **equity** ticks parsed and landing in the DB | Audited Sunday 00:42 PDT; US equities closed | **Mon 2026-08-03, 06:30 PDT / 09:30 ET** |
| TickStream + `snapshots_1s` + crypto ticks end-to-end | Not yet exercised this pass | now — crypto is 24/7 |
| Socket reconnection with no duplicate subscriptions | Requires a live socket to kill | Monday, with equities up |
| Graceful shutdown on a real signal | Only `taskkill /F` used so far | now |
| `npm run build` + Playwright E2E | Not yet run this pass | now |
| SPY benchmark over the paper window | Not pulled | now |
| Prior session's survivorship claim (21 → 716 delisted) | Not re-derived | now |
| Prior session's cross-sectional IC (+0.0497, t=8.71) | Not re-derived — and it sits oddly beside a live IC of −0.020 | next pass |

**"The market was closed" is a real constraint here, not an excuse** — the loop can wait for
Monday's open, and that is the plan for the four rows above that need it.

---

## PHASE B — DEFINITION-OF-COMPLETE MATRIX (status at checkpoint)

| # | Criterion | Status | Evidence |
|---|---|---|---|
| 1 | `go build` / `go vet` / `go test` | **PASS** | all exit 0; **112 packages, 0 FAIL**, re-run after every change |
| 2 | `npm run build` | **PASS** | exit 0, full route manifest emitted |
| 2b | Playwright E2E | **OPEN** | not yet run |
| 3 | Daemon from `git archive HEAD`, attributable | **PASS** | `{"resolvable":true,"modified":false,"revision":"58e42b0…"}`; health 200, ready 200 |
| 4 | Live **equity** ticks parsed → DB | **BLOCKED — timing only** | audited Sunday; US equities open **Mon 2026-08-03 06:30 PDT** |
| 5 | TickStream up, `snapshots_1s` filling, crypto end-to-end | **FAIL** | `snapshots_1s` = **0 rows**; tickstreamd not running; daemon logs `crypto-live: tickstream unreachable for 80s` every ~2 min |
| 6 | Reconnection, no duplicate subscriptions | **OPEN** | needs a live socket (item 4 or 5 first) |
| 7 | Graceful shutdown on a real signal | **BLOCKED** | handler verified in code (`daemon/cmd/signaldeckd/main.go:67`, `signal.NotifyContext(…, os.Interrupt, syscall.SIGTERM)`) but Windows refuses non-forced `taskkill` on this process, so the signal cannot be delivered from this shell. **Inferred, not verified.** |
| 8 | Grader exits 0, README publishes live table | **PASS** | `ops/accuracy-registry.sh` exit 0; README `LIVE-ACCURACY` block regenerated `2026-08-02T01:10:52` |
| 9 | Zero CRITICAL / zero HIGH open | **PARTIAL** | F-1 CLOSED. F-2 partially closed. F-3/F-4/F-5 open. |
| 10 | Every matrix row PASS or evidenced | in progress | this table |

### Closed this session

- **F-1 CRITICAL — CLOSED.** `tools/verify_backup.py` + `tools/test_verify_backup.py`.
  Measured against the real files: Jul 31 backup passes, Aug 1 backup fails with
  `ledger_anchors table has no rows` and `prediction_ledger count 14 is too stale compared to
  live count 263431`. Bad file moved to `quarantine/backups-20260802/`. The backup script now
  runs the verifier after `VACUUM INTO` and quarantines + exits 1 before recording success.
- **F-2 CRITICAL — PARTIALLY CLOSED.** The hardcoded
  `SD="/Users/natalienyaung/claude code/signaldeck"` was fixed once in `accuracy-registry.sh`
  and left standing in **nine** other ops scripts; all nine now derive the root from
  `BASH_SOURCE`, and both absolute `exec` paths resolve relatively. `bash -n` clean across
  `ops/*.sh`, no live hardcoded path remains. **Still open**: `offsiteDir` is on the same
  physical disk, and the ops layer is still launchd-only so nothing runs it here.

### Closed later in the session

- **F-3 HIGH — CLOSED.** `tunnelAgentPaths[0]` no longer contains the repo-relative path;
  pinned by `TestTunnelAgentPathsAreAbsolute`, which failed before the change. **Verified
  live**: the daemon now serves `/api/ready` 200 with **no `SIGNALDECK_PUBLIC_READS`
  override**, proving the heuristic actually varies. Posture note: on this host
  `reachablePrivately()` is now true, so read-only endpoints answer without auth on the
  127.0.0.1 bind — the documented default. On the Mac, with the LaunchAgent installed, it
  still closes.
- **F-4 HIGH — CLOSED (test-verified, not yet observed live).** `calibration_level:<horizon>`
  now flags `at_chance` at reliability ≥ 0.49, additive to the drift check. Two new tests
  fail without it. **Not yet seen in `/api/self-audit`**: that worker runs on a 6-hour
  interval and last ran ~3h before this change shipped, so the endpoint still serves the
  pre-change findings. Expect `calibration_level:1d = at_chance` at the next run, since live
  reliability is 0.4982.
- **F-2 CRITICAL — honesty half CLOSED, physical half OPEN.** `/api/quality.ops` now
  publishes `offsiteSameVolume`, **verified live as `true`** — the dashboard states plainly
  that the "off-machine" copy is on the same disk. Destination is now
  `SIGNALDECK_OFFSITE_DIR`. **Still open**: nothing is yet written to separate hardware.

### Verified-correct refusals (NOT defects — do not "fix" these)

- **README accuracy rows read `WITHHELD (provenance unresolvable)` — correct.** Each row's
  `revision_gate` lists the builds that wrote the underlying rows, including `(unstamped)` and
  `…+dirty`. The grader drops the verdict rather than downgrading it. Independently, those
  rows have `distinct_days: 8` against a floor of 10. This heals only by accruing days from an
  attributable build — which is now what is running. **Not to be forced.**
- **The daemon's dirty-build refusal** fired twice in this session and was right both times.

### Root cause shared by items 4, 5, 7, and F-2

All four trace to the same thing: **the operational layer is macOS launchd and nothing
supervises anything on this Windows host.** The daemon was down at audit start, tickstreamd is
down, the backup runs ad-hoc, and provenance is unresolvable because rows were written by
whatever build someone happened to launch by hand. Porting supervision to this host is the
single highest-leverage fix remaining, and it closes four rows at once.

---

## DISAGREEMENTS WITH THE PRIOR AUDIT

1. Phase count: 5, not 19.
2. The prior pass did not report F-1 or F-2. The backup path is the single most dangerous
   thing in the system right now and it was measured as healthy because
   `offsiteConfigured: true` was taken at face value.
3. The prior pass's headline cross-sectional IC of **+0.0497 (t=+8.71)** is hard to reconcile
   with a live track-record IC of **−0.020** and a downward-sloping calibration curve. One of
   the two is measuring something other than what it claims. **Not resolved this pass** — it
   is the first thing the next pass should attack.
