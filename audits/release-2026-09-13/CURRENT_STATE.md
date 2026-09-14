# CURRENT_STATE — overnight release-candidate pass, 2026-09-13/14

Checkpoint. Resume from here; do not restart the pass.

## Authoritative root

`C:\Users\Nicholas_N\Desktop\claude code\signaldeck` — confirmed:
`git rev-parse --show-toplevel` returns it, and `git worktree list` shows only
this checkout plus an unrelated detached coverage baseline in TEMP
(`sd-cov-baseline` @ 223f1aa). `.claude/worktrees/` exists but is **empty**, so
the stale-worktree hazard in CLAUDE.md does not apply to this session.

## Instructions read

`CLAUDE.md` (settled verdicts, pre-flight checks) · `web/AGENTS.md` (read the
installed Next docs before framework-sensitive changes — honoured for `distDir`,
`deploymentId` and self-hosting/version-skew before touching `next.config.ts`) ·
`DOCS_INDEX.md` (18 governed, 30 exempt) · `ops/CHARTER.md` (action classes;
DEPLOY needs a rollback plan, MONEY is human-owned). Root superprompts and
`quarantine/` treated as evidence, **not executed**.

## Candidate

| | |
| --- | --- |
| Branch | `public-launch` |
| Baseline audit HEAD | `334152d4` — matched exactly when this pass started |
| Moved under me at 21:25 by ANOTHER session | `cf321a23` |
| **Final candidate** | **`f1d9236`** |
| Web `BUILD_ID` | `AG83roTnCLnAWt5dtSGj0` |
| Daemon **running** | revision `5483350` — daemon changes in this candidate are **source-only, not live** |
| URL browsed | `http://127.0.0.1:8323` |

## Concurrency — resolved, but stay alert

Another Claude session was running `npm run e2e` here at 21:15 (PID 105168, its
own `next start -p 8329` against the shared `web/.next`). I did **not** build web
while it held that directory. It finished, committed `cf321a23`, and left a clean
tree.

Its commit is useful input: the first full e2e run gave **39 passed / 4 failed /
2 skipped**, and both failure classes were defects in the GATE (an ambiguous
`main details` selector; five specs out-running the daemon's burst-5 write
limiter), not in the product.

**Rule for any future session:** re-check `git log -1` and look for a foreign
`next start` / playwright before any `next build` or `git add`.

## Services

| Port | Process | Role |
| --- | --- | --- |
| 8322 | `signaldeckd` pid 90272 | daemon, revision `5483350` |
| 8323 | node | "SignalDeck Web" task |
| 3000 | node (replaced 21:28) | "SignalDeck Local Workspace"; the 24 h-old instance **was** the F01 defect |

`LIVE_ARMED` untouched. Go research-loop cadence untouched. Daemon **not**
restarted.

## Protected / do-not-edit

- `tools/accuracy_registry.py` — sha256 pinned in the prereg chain; refuses to
  run when its own bytes change. Guards ship **beside** it. Not edited.
- `PREREGISTRATION.md` — digest pinned by a `prereg-document` chain record.
- Sealed holdouts, settled verdicts in `CLAUDE.md`, historical audit records.

## Phase — final state of this pass

1. Baseline + reproduce frontend — **DONE**
2. Publication fail-open repairs + atomic snapshot — **DONE**
3. Build/lifecycle/public-mode config — **DONE**; provenance parity (F06) **DONE**
4. Ledger truthful proof presentation — **DONE**; write integrity (F08) **DONE**
5. Release-contract split, copy/evidence accuracy — **DONE**
6. Auth/data boundary tests — **DONE**; full private journeys **PARTIAL**
7. Clean build + required tests + browser review — **DONE**; ops/restore/rollback rehearsal **NOT RUN**
8. Final review + competition packet + owner actions — **DONE**

## Commits from this pass

| Commit | What |
| --- | --- |
| `532e6a2` | web asset-integrity guard + negative control (F01, F02) |
| `67d69e2` | publication fail-open ×3 + atomic publication + release contracts (F03, F04, F05, F-NEW-01, F11) |
| `e9c0900` | product honesty + `/proof` provenance + registration chain (F09, F10, F12, F13) |
| `2901422` | `NEXT_PUBLIC_SIGNALDECK_PUBLIC` wired through the image (F07) |
| `6cd06a8` | behaviour-not-formatting test fix, nav relabel, audit records |
| `de8742b` | cross-user tenant isolation tests |
| `413b5ce` | the durable audit record |
| `cff7012` | grader-health can tell a powered-off machine from a broken grader (red tasks) |
| `0000059` | container build provenance bound by content hash (F06) |
| `03d3b4a` | attestation and eligibility are one transaction (F08) |
| `902f777` | repaired the self-test my own manifest guard broke |
| `d656701` | one pre-existing red ops self-test, reported not hidden |

## Open, and why

**No finding is left in CONFIRMED -> OPEN.** What remains is unexercised
verification, not unfixed defects.

| Item | Reason |
| --- | --- |
| Playwright e2e of my own | Suite drives the LIVE daemon and real paper books; isolated fixture not built |
| Docker image build | docker daemon not running on this host, so the in-image `seal` step is unexercised |
| Restore / clean-clone / load / dependency scan | Not run |
| `ops/test-reference-transaction-hook.sh` 11/1 | **Pre-existing** (fails at `cf321a23` too), not diagnosed, deliberately not blind-fixed. See FINDINGS F-NEW-06 |
| Daemon deploy | The daemon changes in this candidate are source-only until someone deploys them |

Closed in the continuation: **F06** (container parity), **F08** (unattested
forecasts), both **red scheduled tasks**, and the **degraded-worker diagnosis**.

## Failures during the pass, recorded rather than smoothed

- **One self-inflicted defect.** My first web-guard restart wait polled `/login`
  and asset-checked once, catching the dying process and filing a false
  INCOMPLETE for a healthy server. Measured, fixed, recorded in `532e6a2`.
- **One delegation failure.** `tools/publication_gate.py` was delegated to an
  OmniRoute worker; sample 1 returned truncated mid-statement (SyntaxError at
  line 408) and correctly failed its own `-Verify`. The chain was still retrying
  through model timeouts, so I stopped it and wrote the file myself. The verify
  gate did its job.
- **One broken test, mine.** Reflowing the `hb_mode` case block broke a regex
  that pinned single-line formatting. Fixed structurally, not by deleting.

## Failures in the continuation, same rule

- **I broke `ops/test-docker-build.sh` twice** — once with the F07 audience
  guard, once with the F06 manifest guard — and neither commit ran it. A
  concurrent session caught the first from CI (`bea128c`); I caught the second
  only during the final diff review, by noticing a file in the diff I had not
  edited. Both are the same failure: a guard that changes a contract must update
  the file that guards it.
- **I nearly reported a nonsense number.** The first ledger-coverage query said
  255,055 rows were unattested. `prediction_outcomes` also holds the `#pm`
  benchmark rows, which are never attested and correctly absent from the chain.
  The real figure is 31. Caught because 255,055 could not possibly exceed the 30
  the health check reported.

## Exact next command

```bash
ops/docker-build.sh
```

On a machine with a running docker daemon. It is the one leg of F06 that has
never executed: the in-image `seal` step. If it fails, the build fails, which is
the intended direction.
