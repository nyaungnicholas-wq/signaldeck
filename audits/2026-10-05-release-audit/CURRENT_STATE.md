# CURRENT STATE: release audit 2026-10-05

Resumable checkpoint. Update it when anything below changes.

## Roots and policies
- Canonical checkout: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `public-launch`. Remote `nyaungnicholas-wq/signaldeck` is **PUBLIC**: keep exploit detail for unrepaired issues and banned figures out of committed files.
- Repair worktrees: rounds 1-3 used `C:\Users\Nicholas_N\Desktop\claude code\sd-audit-1005`, branch `audit-1005`; rounds 4-8 used `C:\Users\Nicholas_N\Desktop\claude code\sd-audit-1005b`, branch `audit-1005b`. Both have junctions to the live `web/node_modules` and `.venv`. Remove them as links (`[IO.Directory]::Delete(path,$false)`) BEFORE `git worktree remove`.
- Execution layer: `C:\Users\Nicholas_N\Desktop\claude code\execution` (main d9cf131, remote private). Not modified. No live capability is armed.
- Policies followed: repo `CLAUDE.md` (pre-flight checks), `ops/CLOUDFLARE_TUNNEL.md`, PREREGISTRATION.md section 15 (the SD-30 withhold lifts only at 10 credible days AND the owner's yes), deploy windows (avoid 23:00–03:00 and 06:15–13:45 PT).

## Identity
- Audit start (02:39Z): HEAD 1249c436 = origin/public-launch; daemon 41a817a9 and web 6a1a327 are runtime-identical to HEAD; PR #26 merged 02:53Z, after which main and public-launch trees match.
- Repair commits on audit-1005 (rounds 1-3), fast-forwarded into public-launch: f07075f6 (round 1), 35012ab3 + 952e6e85 + 53afe5d5 (round 2 and its review fixes), 81c09af0 (round 3, web only).
- Repair commits on audit-1005b (rounds 4-8), fast-forwarded into public-launch: e35fe93d (round 4), 3d2f3fee (proxy Content-Disposition), 35eebd88 (round 5), 199b9898 (round 6), c29b4d60 (round 7), a80bed03 (round 8).
- LIVE: daemon a80bed03 ("deploy VERIFIED: daemon is running commit a80bed03 (resolvable); 8 worker run(s) already stamped with it") and web build QmXlyf43Yl6bD-y6IHNTR (asset gate passed, browser gate 7/7, promoted 03:05:37). public-launch pushed to a80bed03 by refspec. Main synced by PR #28 (merged 2026-10-05 10:45Z, 3678573b) and PR #29 (test-only flake fix, merged 11:19Z, 741b3fb0); public-launch at f0e82e64 plus this docs commit.

## Protected boundaries (untouched)
Sealed holdouts, pinned graders (`tools/accuracy_registry.py`), the prereg chain, research history, ledger anchors and keys, `LIVE_ARMED`, broker accounts, DNS and nameservers, repository visibility, backups (nothing deleted), personal authorship statements.

## Completed
- Discovery and inventory: inventory.md.
- Browser user journeys: public anonymous, plus new-user, member and attacker journeys on an isolated copy (verification.md).
- Source security review, evidence-contract trace, ops review and privacy/legal/copy review, in four parallel read-only reviews. Full reports are kept locally, outside the public repo.
- Repairs AUD-01, 02, 03, 06, 07, 10 (round 1); AUD-11, 14, 16, 22, 24, 25, 28, 29, and HSTS from AUD-23 (round 2); AUD-37, 38 (round 3). Each has a regression test that fails on the old code where a test is meaningful. Fresh-context reviews of rounds 1 and 2 found 2 must-fix and 8 should-fix items; all were applied before deploy.
- Repairs AUD-05 (export/delete, partly: owner text remains), 08, 09 (mitigated), 12, 13, 19, 21, 22 (extended), 27, 31, 32, 33, 35, 39 (log), 40 (new: web proxy dropped Content-Disposition). Each round 5-8 applied a fresh-context review of the round before; the final pre-deploy review said DEPLOY: GO. Isolated browser journeys J29-J33 (account export and delete), an isolated load test (web 554 req/s 0 errors; daemon 3,580 req/s 0 errors, 85% shed by the limiter) and the hour-sliced prune measured on a backup copy (128k rows, 0 write-lock holds over 3 s). Post-deploy on the public tunnel: 4 sticky retirements back on /api/accuracy (no figures, SD-30 withheld text), landing "Retired by its own rule" panel shows, /account signed out goes to /login?next=%2Faccount, anonymous /api/account/export 401, daemon-guard logged "running and answering (HTTP 200)".

## Open (see findings.md)
Engineering: none open; by design or accepted: AUD-23 (CSP inline, measured), AUD-34, AUD-39 warm-up. AUD-20 verified 10-05 (first real prune kept 7 backups). The load-sensitive test flake (TestPersistedBody_ServedAcrossBuildsOfOneFormatOnly) is fixed (verification.md round 9). Stable hostname https://signaldeck.nicholasnyaung.com live 10-06 (verification.md round 11). Owner: AUD-04 (uptime: PC hours; retire the quick tunnel when chosen), AUD-05 (notice, terms, contact; facts in docs/PRIVACY_NOTICE_FACTS.md), AUD-17 (Kraken derived works, counsel), AUD-18 (second machine), AUD-26 (daemon/.env.bak copies), AUD-36 (Turnstile keys).

## Failed approaches (do not repeat)
- omni workers for long documents: the worker's output stops at 25,000 characters. Assemble long artifacts from parts.
- A `findstr /C:"^OK" /C:"Ran 4 tests"` verify passes on failure. Assert the success token alone.
- Fixed-epoch file mtimes in tests turn into future dates as the calendar moves (the cleanup age guard skipped them). Anchor fixtures to `time.time()`.
- The Bash tool collapses `\\` in heredocs: a Go `"\x00"` became a literal NUL byte. Write edit scripts with the Write tool.
- Under Git Bash, TZ=America/Los_Angeles silently gives UTC; a deploy-window waiter written with it exited at once; use the local clock.
- A load generator that opens one TCP connection per request exhausts Windows ephemeral ports in about a minute (the daemon's own probe then fails); keep connections alive.
- findstr in an omni -Verify treats forward-slash paths as switches; pass Windows backslash paths.
- A posture refusal at startup on every port blocked the e2e test and would have blocked ctl deploy; it is scoped to the web tier's port 8322.

## Next action
Engineering is done: main is synced (PR #28 merged 10:45Z as 3678573b, PR #29 the flake fix merged 11:19Z as 741b3fb0) and the repair worktrees and branches are removed. Owner items in release_verdict.md "Exact next actions". AUD-08 and AUD-20 were verified in production on 10-05 (verification.md round 10).
