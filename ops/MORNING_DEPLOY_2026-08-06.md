# Morning runbook — 2026-08-06

State as of 23:29 local, verified not assumed.

| | |
|---|---|
| running daemon | `0499416` · `resolvable: true` · `modified: false` |
| `origin/main` | `fa4999d` — **behind the daemon** |
| PR #8 head | `a78ed92` (3 commits main lacks) |
| live DB | migration APPLIED — `superseded_by` present, 2,817 of 29,595 rows superseded |
| health | `{"ok":true,"staleWorkers":[]}`, zero ERROR lines since the 22:59:42 restart |

## Read this before running anything

**Do NOT deploy from `main` first.** The schema migration is already live: the
daemon runs `0499416` and `regime_outcomes` already carries `superseded_by`.
`main` does not have that commit. Deploying from `main` as it stands rolls the
CODE backward while the SCHEMA stays forward — a daemon with no `superseded_by`
handling against a table that has the column. That is a downgrade into a
mismatch, not a deploy.

Merging PR #8 is what makes `main` correct. Only then is the deploy safe.

**Correction (post-merge).** This is no longer a no-op relabel. That framing was
written when `main` lacked only `0499416` and `a78ed92`. PR #8 merged all 11
commits, and the daemon is still on `0499416` — so this deploy ships the
cross-section gate, breadth reporting, the fleet-veto evidence floor and the
cost disclaimer into production for real. Treat it as a behaviour change, not a
provenance tidy-up.

**Known failing test.** `TestStorageGovernorCheckpointLadder` in
`internal/maintain` fails on Windows, and `TestTunnelAgentPathsAreAbsolute` in
`internal/config` fails because a macOS `/Library/LaunchAgents` path is not
absolute there. Both were confirmed failing at baseline `46a2375` before any of
this work, so neither is introduced by it. If you deploy with either still red,
record that in the deploy notes rather than letting a green-looking run imply
they passed.

**`go` on PATH.** `ops/signaldeck-ctl.sh` runs under Git Bash, where `go` is not
on PATH by default — the test step then fails on a missing binary rather than on
real results. Export `C:\Program Files\Go\bin` before running it. A green that
came from a missing binary is worse than a red.

## Order

1. **Review and merge PR #8** — "Fold the last day-fold holdout without deleting
   a frozen row". Was `OPEN MERGEABLE UNSTABLE` (checks still running) at the
   time of writing; require all six green first. `daemon` takes ~35 min.

2. **Confirm `main` now contains the running revision.** This is the whole point
   of step 1; if it fails, stop.

       git fetch origin && git merge-base --is-ancestor 0499416 origin/main && echo OK

3. **Pause the Eighty Loop.** It writes `research/eighty/hNNNN.py` every ~10 min
   and the deploy takes longer than that, so it WILL dirty the tree mid-run and
   the provenance gate will refuse. It beat two deploy attempts on 2026-08-05.

       Stop-ScheduledTask -TaskName "SignalDeck Eighty Loop"

4. **Take `main` clean.**

       git checkout main && git pull --ff-only && git status --porcelain

   Must print nothing. If a `daemon/`/`tools/` file is modified, a session is
   mid-edit — stop and find out whose it is before stashing anything. Do not
   stash a live edit to satisfy the gate.

5. **Deploy** — the only sanctioned source-to-running path. It refuses a dirty
   tree, runs the daemon suite, and builds from `git archive HEAD` so the binary
   IS the commit rather than whatever the worktree held.

       bash ops/signaldeck-ctl.sh deploy

6. **Verify — and do not accept `deploy VERIFIED` as proof.** That line proves a
   binary started, not that anything is correct.

       curl -sf -H 'X-Signaldeck: 1' http://127.0.0.1:8322/api/version
       # expect revision == the merge commit, resolvable:true, modified:false

       sqlite3 -readonly data/signaldeck.db "PRAGMA table_info(regime_outcomes);" | grep superseded
       # expect the column present (it already is — this confirms no regression)

       cat data/health.json          # ok:true, staleWorkers:[]

7. **Restart the Eighty Loop.**

       Start-ScheduledTask -TaskName "SignalDeck Eighty Loop"

## Not done, and why

The crash-safety rehearsal against a database copy was skipped deliberately.
The migration has ALREADY run against the live database and succeeded — column
present, 2,817 rows superseded, their 98.2%-agreement claim reproduced exactly
on 2,817 pairs, zero errors since restart. Rehearsing on a copy would spend
~5 GB and an hour asking whether something that already succeeded might succeed.
The live outcome is strictly better evidence than a rehearsal of it.

If the intent was to validate the ROLLBACK path rather than the forward
migration, that is a different and still-open question — the commit claims a
mid-flight kill leaves the old shape intact, and that claim has not been
exercised. It needs a copy, and it is worth doing before the NEXT schema change,
not before this deploy.
