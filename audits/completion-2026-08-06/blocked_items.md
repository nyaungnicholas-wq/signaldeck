# Blocked Items Register — SignalDeck Completion 2026-08-06

Every item here is blocked on **explicit user approval** or on external state. None can be cleared
by more agents, more analysis, or more time. Per the engagement's own rule 13, these categories
require approval: database changes, migrations, destructive actions, live-trading changes,
credential changes, external-system changes.

Approval has **not** been given for any of these. It was requested at the end of the audit round and
again at the end of completion round 1, and has not been received. Automated background-task
notifications are not approval and were not treated as such.

---

## BLOCKED-1 — Rebuild and redeploy the daemon on HEAD  ·  **HIGHEST VALUE**

**Category:** live-state change.
**What:** rebuild `bin/signaldeckd.exe` from HEAD and restart the `SignalDeck Daemon` task.

**Why it dominates everything else:** the running process is revision `0499416`, **22 commits behind
HEAD**. Verified live: `curl localhost:8322/api/version` → `049941691d84…`, unchanged even after the
service restarted mid-session (pid 22220 → 33844) — because `bin/signaldeckd.exe` has not been
rebuilt since 2026-08-05 22:58. **Restarting does not close this gap. Only a rebuild does.**

What a deploy would make live:
- `SETTLEMENT GUARD` — verified absent from the running revision (`grep -c` → 0) and present at HEAD
  (→ 1). HEAD's own comment measures the cost of its absence: *"37.8% were frozen before their
  forward bar's 16:00 ET close… about 3.7% of the whole 1d record labeled against a price that had
  not happened yet."* **The live process is producing look-ahead-contaminated labels right now.**
- `Refuse to publish a collapsed cross-section`, `Admit ensemble legs on measured ranking`,
  `Withhold a fleet veto until enough symbols carry a graded row`, `Say the directional signal is
  gross of costs, always`.
- All six repairs from this engagement (R2, R3, R5, SD-H35, SD-012, NEW-1).

**Risk:** 22 commits deploy at once. `bin/signaldeckd.exe.bak` and several dated rollback binaries
already exist. **Recommended, but it is your call.**

---

## BLOCKED-2 — Quarantine the contaminated 1d labels and the paper equity curve

**Category:** database write.
**What:** mark the affected `prediction_outcomes` rows and `paper_equity` range invalid.

Approximately **3.7% of the 1d record** is labeled against prices that had not yet occurred, and the
paper equity curve was written one mark per pass while fills carry timestamps back-dated up to 29
days. Neither is **exactly reconstructable** from stored source data — bars have been revised, and
the curve was never rebuilt. Per rules 8 and 9: **quarantine, never backfill.** The schema already
contains the intended mechanism — `basis_epoch` / `symbol_bars_epoch` — which currently has zero
code behind it and is 100% NULL.

**Consequence of quarantining:** existing published grades over the affected window become invalid.
That is the honest outcome, and it is a decision only you should make.

---

## BLOCKED-3 — Web application: schedule it, or formally retire it

**Category:** external/system change.
Nothing listens on :8323, there is no `SignalDeck Web` scheduled task, and
`ops/install-windows-tasks.ps1` structurally skips any plist it cannot map — so the web surface
*cannot* be scheduled on this host as things stand. The macOS `.plist` heritage did not survive the
Windows migration. The entire user-visible product is therefore down. Keeping a dead surface
documented as live is itself a defect.

---

## BLOCKED-4 — Resolve the two generated regions in `STRATEGY_DECK.md`

**Category:** rewrites a governed ACTIVE document.
Repairing SD-012 turned a gate that was protecting **nothing** into one that finds two real
violations (`STRATEGY_DECK.md:126` `controls_evidence`, `:199` `deck_facts`). `partials/` contains
only `live_accuracy.md`. **CI is red until this is resolved.**

I attempted the obvious fix — declaring both in `ops/docs-registry.json` `external_partials` — and
**reverted it**: it only changed the failure message (the gate then demanded the partial *files*,
which do not exist) without fixing anything, and a half-measure in a file that grants
accuracy-check exemptions is worse than none. Proper resolution is to run the two generators
(rewriting a governed ACTIVE document) or remove the markers.

---

## BLOCKED-5 — Configure a real alert transport

**Category:** external service + credentials.
Zero remote transports are configured; the only channel is a best-effort Windows toast with
untracked delivery, and part of the notify path is macOS-only (`osascript`). **Nobody is told when
anything breaks** — which is precisely how two FINRA feeds stayed dead for four days.

---

## Non-approval blockers (technical)

| Item | Status | What would clear it |
|---|---|---|
| e2e suite | **BLOCKED** | binds a real port / touches a real DB while the live daemon runs; needs an isolated host or a maintenance window |
| Live FINRA ingest verification after R2 | **BLOCKED** | gated on BLOCKED-1 — the fix must be running to be observed |
| `npx tsc --noEmit` / any web verification | **BLOCKED** | gated on BLOCKED-3 |
| Production-like dry run | **BLOCKED** | gated on BLOCKED-1 |
| ~~Whether any position is *currently* stranded in the live book~~ | **ANSWERED — NOT BLOCKED** | Resolved by a read-only query rather than deferred. `SELECT … FROM paper_positions p JOIN symbols s ON s.id=p.symbol_id WHERE s.active=0` returns **zero rows**; the book holds **2 open positions total, both in active symbols**. **R5 therefore fixed a real but latent defect that had not yet caused live damage.** No remediation needed, so BLOCKED-2 does not extend to positions. |

---

## BLOCKED-6 — Restore the active universe to its intended size (added round 3)

**Category:** database write / scheduled-task re-run.

`SELECT COUNT(*), SUM(active) FROM symbols` → **2950 / 2950**. Intended is ~328.
`SignalDeck Daily-Refresh` was terminated mid-sweep on 2026-08-06 13:15
(`LastTaskResult 3221225786` = `0xC000013A`, STATUS_CONTROL_C_EXIT), after it had reactivated the
full universe and before it pruned. Confirmed live: `dq-auditor: checked 2950 active symbols`,
`downsampler: rolled up 2950 symbols`, every worker `status: ok`.

**Remedy options (your choice):** re-run `ops/signaldeck-refresh.sh` to completion, or prune
directly. Both mutate the database. Also worth deciding: the sweep reactivates-then-prunes, so any
interruption leaves the universe maximally open — a reversed order (or a transaction) would fail safe.

**Why blocked:** rule 13 — database change.
