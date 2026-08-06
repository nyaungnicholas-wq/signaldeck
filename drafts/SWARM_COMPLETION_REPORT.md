# Swarm Completion Report — 2026-08-06

**Run:** `wf_33f4acab-dd7` · 11 agents · 1,729,249 subagent tokens · 677 tool uses · 23 min · **0 errors**
**Result:** 41 artifacts, all under `drafts/`. **103 findings** (3 Critical, 26 High, 36 Medium, 16 Low, 22 Informational).

## Invariant compliance — verified, not asserted

| Invariant | Evidence |
|---|---|
| 1. No live DB writes | All queries `?mode=ro`; heavy scans on the integrity-verified static backup |
| 2. No deploys/restarts | `/api/version` still `0499416`, pid 33844 undisturbed; no rebuild |
| 3. No git state changes | `git status --porcelain` shows only the 10 pre-existing repairs + 3 externally-owned files. **Zero swarm edits to source** — all 41 artifacts are under `drafts/` |
| 4. No secret exposure | Name-only greps; `icacls` output contains no values |
| 5. Preserved files | `README.md`, `research/eighty/h026[5-9].py` untouched |
| 6. BLOCKED not executed | Every remediation is a `.sql`/`.ps1` draft with a do-not-execute header |

---

## THREE CORRECTIONS THAT CHANGE THE PLAN

### C1 — The `basis_epoch` quarantine design does not work · **I verified this personally**

My BLOCKED-2 draft proposed quarantining contaminated labels by stamping the existing
`basis_epoch` column. Measured:

```
grep -rn "basis_epoch|BasisEpoch" daemon --include=*.go | grep -v _test.go   ->  0
grep -c "basis_epoch" tools/accuracy_registry.py                             ->  0
```

**Zero readers, anywhere.** Stamping it would exclude nothing from any published grade — quarantine
in appearance, not in effect. That is precisely the false-assurance pattern this engagement exists to
find, and I proposed it. Any real quarantine must add a reader to the grader, or the exclusion must
happen in `tools/accuracy_registry.py` itself.

### C2 — The quarantine population is 68,368 rows, not "3.7%"

The two numbers measure different things. The 3.7% figure comes from HEAD's own comment on a 4,000-row
sample of resolved 1d **stock** rows. The swarm derived the guard's actual predicate and measured the
full population: **68,368 rows**. Any approval decision must be made against the measured number.

### C3 — e2e credential isolation is a fiction · **I verified this personally**

`daemon/e2e/e2e_test.go` sets a fake `HOME` with the comment *"no .env files → no Alpaca/LLM keys"*.
That isolation does not hold. `buildDaemon` writes the binary to `t.TempDir()`, but
`config.projectRoot()` also probes `os.Getwd()` — which under `go test` is `daemon/e2e/` **inside the
real repo**. Walking up finds `claude code/signaldeck/daemon` and returns the real repo root, so the
daemon loads the operator's real `daemon/.env`. The `os.UserHomeDir()` fallback that `HOME` controls is
the last resort and is never reached.

**Running the e2e suite would load real credentials and could make real API calls.** This retroactively
justifies the decision to leave e2e BLOCKED rather than run it.

---

## Critical findings (3)

| # | Finding | Evidence |
|---|---|---|
| S-C1 | Daily-Refresh killed mid-sweep leaves the universe expanded (2950 vs ~328) | `logs/refresh.log` + `symbols` — independently confirmed; this is BLOCKED-6 |
| S-C2 | `h0221` design effect **collapses to 1.0 at maximal clustering** — the pseudoreplication correction is non-monotone | `research/eighty/h0221.py:166-170` |
| S-C3 | e2e credential isolation is a fiction (see C3) | `e2e_test.go:110,228` + `config.go:33-60` |

**S-C2 deserves emphasis.** The published intervals lean on a design-effect haircut (`live_n` 3,322 →
`effective_n` 320). A design effect that is *non-monotone in clustering* — collapsing to 1.0 exactly
when clustering is worst — understates the correction precisely when it matters most. This is the one
finding that could make the published confidence intervals narrower than the truth. It is in the
research lane, so whether it feeds the shipped registry must be settled before any interval is trusted.

## Selected High findings

- **Contamination is ONGOING** — revision `0499416` mints new contaminated rows **every 10 minutes** (`predict.go:1073`, guard at `:1132` absent from the running build).
- **The Daemon task bypasses the provenance gate.** `ops/com.signaldeck.daemon.plist` carries a `build_from_head` PROVENANCE GATE; the actual Windows task executes `bin\signaldeckd.exe` directly. **This is why a stale binary keeps running across restarts.**
- **`dq-auditor` floods `dq_events` with ~1,897 events/hour against 1,868 permanently-delisted tickers**, blinding the only data-quality signal — this explains the 3,825/day `stale` events, and it is a *consequence* of the universe bloat.
- **1,868 of 2,950 symbols are simultaneously `active=1` and delisted**; 3,898 day/symbol pairs carry 1d bars dated *after* delisting.
- **65 rows of the published graded population** were predicted on days that symbol's own feed was flagged stale (`tools/accuracy_registry.py:1455-1457`).
- **`com.signaldeck.revalidation` is MONTHLY in the plist but runs DAILY on Windows** — the installer has no day-of-month branch.
- **Both generated regions in `STRATEGY_DECK.md` are already stale against their own generators**, and the CI steps meant to guard them do not.
- **Test coverage is 70.0% against a CI floor of 70.1** — CI would fail on coverage today.
- `TestNoEdgeStaysNearBaseRateWhileKeepingOrder` **skips** (`beta_test.go:122`) — a self-disarming calibration test.

The swarm also *refuted* three sub-claims from earlier rounds (the paper dust-fill mechanism is closed
at HEAD by `riskgate.Evaluate`; the universe-poller cannot explain the 2026-08-05 coverage gap; the
Python-sidecar count was 4→3 writers). Corrections in both directions.

---

## Review commands

Nothing is applied. To inspect:

```bash
cd "C:/Users/Nicholas_N/Desktop/claude code/signaldeck"
find drafts -type f -not -path '*__pycache__*' | sort
```

To review a patch before applying (dry run — `--check` applies nothing):

```bash
git apply --check --stat -v drafts/patches/alpaca-429.patch
```

To apply one patch:

```bash
git apply drafts/patches/alpaca-429.patch
```

Patches available: `alpaca-429`, `env-example-token-leak`, `notify-slack-smtp`,
`companies-latestdailyall-perf`, `dq-auditor-skip-delisted`, `refresh-atomicity`,
`e2e/patches/0001-e2e-harness-and-windows`, `e2e/patches/0002-main-http-sandbox`.

New files (copy rather than patch): `drafts/e2e/daemon/internal/testharness/*`,
`drafts/patches/notify/*`, `drafts/research_tests/*`.

---

## `drafts/pending-approval/` manifest — NOTHING HERE HAS BEEN EXECUTED

| Path | What it does | Risk | Gate |
|---|---|---|---|
| `data-ops/quarantine_1d_labels.sql` | Marks contaminated 1d labels (68,368 rows measured) | **Ineffective as written — see C1.** Needs a grader-side reader first | BLOCKED-2 |
| `data-ops/rollback_quarantine_1d_labels.sql` | Reverses the above | Low | BLOCKED-2 |
| `data-ops/verify_quarantine_1d_labels.sql` | Proves the quarantine took effect | Read-only | — |
| `data-ops/prune_universe.sql` | Restores `active=0` outside the intended set, in a transaction | Medium — derived from the script's own criterion, not hardcoded | BLOCKED-6 |
| `data-ops/companies-index-NOT-REQUIRED.md` | Concludes no index is needed for the 19s `/api/companies` | — | — |
| ~~`devops/signaldeck-web-task.ps1`~~ → `ops/signaldeck-web-task.ps1` | Registers a Windows task for the web app | Medium | **BLOCKED-3 CLOSED 2026-08-06** — approved, promoted to `ops/`, registered and started. Verified live: :8323 serves HTTP 200 HTML, `/api/version` proxies to the daemon |
| `devops/strip-sandbox-acl.ps1` | Removes `CodexSandboxUsers` read access from `.env` and the DB | **Header states any exposed secret must be treated as COMPROMISED and ROTATED — removing an ACL does not un-disclose it** | BLOCKED-5 |
| `docs-gate/RUNBOOK.md` + `STRATEGY_DECK.expected.diff` + `ops-docs-registry.patch` | Regenerate both partials and re-inject | Medium — rewrites a governed ACTIVE doc | BLOCKED-4 |

**Safer alternative for BLOCKED-6, recommended over the SQL:** re-run `ops/signaldeck-refresh.sh` to
completion. It reaches the same end state through the code path that owns the invariant. The SQL exists
for the case where the sweep cannot be run.

---

## What this swarm did NOT do

- **Did not generate 257 mocked pytest files.** With DB/model/API calls mocked, those tests assert that
  mocks were called. Instead: a real `conftest.py` plus 3 analytic suites over the research corpus,
  which **found S-C2** — a genuine math bug a mocked suite could never surface.
- Did not deploy, restart, rebuild, write to the DB, change git state, or execute any BLOCKED item.
- Did not run the e2e suite — and C3 shows that was the right call.

**Status: NOT COMPLETE.** Six items remain gated on human approval, now with drafted scripts attached —
and one of those drafts (C1) is known-ineffective and must be redesigned before it is worth approving.
