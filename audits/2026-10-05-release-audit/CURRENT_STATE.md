# CURRENT STATE: release audit 2026-10-05

Resumable checkpoint. Update it when anything below changes.

## Roots and policies
- Canonical checkout: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `public-launch`. Remote `nyaungnicholas-wq/signaldeck` is **PUBLIC**: keep exploit detail for unrepaired issues and banned figures out of committed files.
- Repair worktree: `C:\Users\Nicholas_N\Desktop\claude code\sd-audit-1005`, branch `audit-1005`. It has junctions to the live `web/node_modules` and `.venv`. Remove them as links (`[IO.Directory]::Delete(path,$false)`) BEFORE `git worktree remove`.
- Execution layer: `C:\Users\Nicholas_N\Desktop\claude code\execution` (main d9cf131, remote private). Not modified. No live capability is armed.
- Policies followed: repo `CLAUDE.md` (pre-flight checks), `ops/CLOUDFLARE_TUNNEL.md`, PREREGISTRATION.md section 15 (the SD-30 withhold lifts only at 10 credible days AND the owner's yes), deploy windows (avoid 23:00–03:00 and 06:15–13:45 PT).

## Identity
- Audit start (02:39Z): HEAD 1249c436 = origin/public-launch; daemon 41a817a9 and web 6a1a327 are runtime-identical to HEAD; PR #26 merged 02:53Z, after which main and public-launch trees match.
- Repair commits on audit-1005, fast-forwarded into public-launch: f07075f6 (round 1), 35012ab3 + 952e6e85 + 53afe5d5 (round 2 and its review fixes), 81c09af0 (round 3, web only).
- LIVE: daemon 53afe5d5 (ctl deploy VERIFIED) and web 81c09af0 (build tMgxjN2aJ8Bbnl96Dz8x2). Daemon code is identical between 53afe5d5 and 81c09af0.

## Protected boundaries (untouched)
Sealed holdouts, pinned graders (`tools/accuracy_registry.py`), the prereg chain, research history, ledger anchors and keys, `LIVE_ARMED`, broker accounts, DNS and nameservers, repository visibility, backups (nothing deleted), personal authorship statements.

## Completed
- Discovery and inventory: inventory.md.
- Browser user journeys: public anonymous, plus new-user, member and attacker journeys on an isolated copy (verification.md).
- Source security review, evidence-contract trace, ops review and privacy/legal/copy review, in four parallel read-only reviews. Full reports are kept locally, outside the public repo.
- Repairs AUD-01, 02, 03, 06, 07, 10 (round 1); AUD-11, 14, 16, 22, 24, 25, 28, 29, and HSTS from AUD-23 (round 2); AUD-37, 38 (round 3). Each has a regression test that fails on the old code where a test is meaningful. Fresh-context reviews of rounds 1 and 2 found 2 must-fix and 8 should-fix items; all were applied before deploy.

## Open (see findings.md)
Engineering: AUD-12, 13, 18, 19, 21, 23 (CSP inline), 27, 30, 32–35, 39.
Owner: AUD-04 (stable host and domain), AUD-05 (privacy notice, terms, contact, deletion policy), AUD-08 (ngrok scope), AUD-15 (BootTrigger, elevated), AUD-17 (Kraken derived works), AUD-26 (`.env.bak` copies), AUD-31 (AGPL deps), AUD-36 (Turnstile keys).

## Failed approaches (do not repeat)
- omni workers for long documents: the worker's output stops at 25,000 characters. Assemble long artifacts from parts.
- A `findstr /C:"^OK" /C:"Ran 4 tests"` verify passes on failure. Assert the success token alone.
- Fixed-epoch file mtimes in tests turn into future dates as the calendar moves (the cleanup age guard skipped them). Anchor fixtures to `time.time()`.
- The Bash tool collapses `\\` in heredocs: a Go `"\x00"` became a literal NUL byte. Write edit scripts with the Write tool.

## Next action
Sync main via a PR from public-launch (CI must pass; merge pinned with `--match-head-commit`). Then, engineering: AUD-12 (retired rows absent from /api/accuracy), AUD-39 (post-restart ledger warm-up and its misleading ceiling log), AUD-13 (readiness semantics and the hang-blind daemon-guard), AUD-19 (write-lock holds). Owner: release_verdict.md "Exact next actions".
