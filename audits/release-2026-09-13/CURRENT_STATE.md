# CURRENT_STATE — overnight release-candidate pass, 2026-09-13

Living checkpoint. Update after each substantial fix and before compaction.
Resume from here; do not restart the pass.

## Authoritative root

`C:\Users\Nicholas_N\Desktop\claude code\signaldeck` — confirmed authoritative:
`git rev-parse --show-toplevel` returns it, and `git worktree list` shows only
this checkout plus an unrelated detached coverage baseline in TEMP
(`sd-cov-baseline` @ 223f1aa). `.claude/worktrees/` exists but is EMPTY, so the
stale-worktree hazard in CLAUDE.md does not apply to this session.

## Instructions read

- `CLAUDE.md` (repo) — settled verdicts, pre-flight checks
- `web/AGENTS.md` — read installed Next docs before framework-sensitive changes
  (honoured: `node_modules/next/dist/docs` consulted for `distDir`,
  `deploymentId`, self-hosting/version-skew before touching `next.config.ts`)
- `DOCS_INDEX.md` — 18 governed, 30 exempt
- `ops/CHARTER.md` — action classes; DEPLOY needs a rollback plan, MONEY is
  human-owned only
- Root superprompts and `quarantine/` treated as evidence, NOT executed.

## Commits

| Point | Commit | Note |
| --- | --- | --- |
| Audit baseline (Codex, 2026-09-13 ~21:00 PT) | `334152d4` | branch `public-launch` |
| Actual HEAD when this pass started | `334152d4` | matched the baseline exactly |
| Moved under me at 21:25 by ANOTHER session | `cf321a23` | `e2e: the pre-release gate could not be run to completion` |
| This pass, fix 1 | `532e6a2` | web asset-integrity guard + negative control |

## Concurrency — resolved, stay alert

Another Claude session was running `npm run e2e` in this repo at 21:15 (PID
105168, its own `next start -p 8329` against the shared `web/.next`). I did NOT
build web while it held that directory. It finished, committed `cf321a23`, and
left a clean tree; no non-mine signaldeck processes remained at 21:27.

Its commit is useful input, not noise: it reports the first full e2e run as
**39 passed / 4 failed / 2 skipped**, and both failure classes were defects in
the GATE (an ambiguous `main details` selector, and five specs out-running the
daemon's burst-5 write limiter), not in the product.

**Rule for the rest of this pass:** re-check `git log -1` and for a foreign
`next start`/playwright before any `next build` or `git add`.

## Services live during this pass

| Port | Process | Started | Role |
| --- | --- | --- | --- |
| 8322 | `signaldeckd` pid 90272 | 2026-09-13 19:09 | daemon, revision `5483350` |
| 8323 | node pid 71408 | 2026-09-13 20:58 | web, "SignalDeck Web" task |
| 3000 | node pid 10856 -> **99148** | 2026-09-12 21:19 -> **2026-09-13 21:28** | web, "SignalDeck Local Workspace"; the 24h-old instance was the F01 defect and has been replaced |

Scheduled tasks: 19 `SignalDeck *`. Two are RED and not yet diagnosed —
`SignalDeck Check-Grader-Health` and `SignalDeck Check-Task-Health`, both
`LastTaskResult 1`. Open item.

**Do not restart the daemon** for a web-only HEAD move (CLAUDE.md standing
order). `LIVE_ARMED` untouched. Go research-loop cadence untouched.

## Protected / do-not-edit

- `tools/accuracy_registry.py` — sha256 pinned in the prereg chain; it refuses
  to run when its own bytes change. Guards ship BESIDE it.
- `PREREGISTRATION.md` — its digest is pinned by a `prereg-document` chain
  record; editing it makes every verdict unregistered.
- Sealed holdouts, settled verdicts in `CLAUDE.md`, historical audit records.

## Phase

1. Baseline + reproduce frontend — **DONE**
2. Publication fail-open repairs — **IN PROGRESS**
3. Build/lifecycle/public-mode + provenance parity — not started
4. Ledger write integrity + truthful proof presentation — not started
5. Release-contract split, copy/evidence accuracy — not started
6. Auth/data/privacy boundaries, journeys, public UX — not started
7. Clean build, full required tests, browser review, ops rehearsal — not started
8. Fresh final review, competition packet, owner actions — not started

## Completed

- F01 frontend asset incoherence — FIXED, verified, regression-tested (`532e6a2`)
- F02 weak web health check — FIXED, verified, negative control (`532e6a2`)

## Open

- F03/F04/F05 publication fail-open (both native and container wrappers)
- F-NEW-01 publication is not atomic (see FINDINGS)
- F06/F07 container parity, `NEXT_PUBLIC_SIGNALDECK_PUBLIC`, grader schedule
- F08/F09 ledger write integrity, `/proof` overclaim
- F11 docs-gate release-contract split
- F10/F12/F13 product honesty and evidence navigation
- Two red scheduled tasks (above)
- Full required test matrix, browser review, restore/rollback rehearsal
- Competition packet (target not yet confirmed by owner — CAC provisional)

## Failures / limitations so far

- None silently absorbed. One self-inflicted defect (guard restart race) was
  measured and fixed before commit; it is recorded in `532e6a2`'s message.

## Exact next command

```
cd "C:\Users\Nicholas_N\Desktop\claude code\signaldeck"
.venv/Scripts/python.exe tools/test_selection_honesty.py   # baseline before touching the publication chain
```
