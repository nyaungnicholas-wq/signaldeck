# 2026-10-02 cleanup ledger

This is the overnight cleanup run that Nicholas approved on 2026-10-02 around 01:00 PT. The plan is
in `~/.claude/plans/create-a-plan-to-steady-teapot.md`.

## Baseline
| What | Value |
|---|---|
| Trunk | public-launch = origin = running daemon = 6f8cd0b (worker_runs.revision) |
| CI | green |
| Web build | 0ed1bcc (no web/ change since) |

## Statuses and gate
- **Status values:** OPEN, FIXED (merged on cleanup-1002, not yet live), VERIFIED (live + evidence),
  BLOCKED (reason written), OWNER, WAIT, DECISION.
- **Stop-check gate:** it fails while any CODE row is OPEN or FIXED.

| ID | Cat | Status | Item | Done when | Evidence |
|---|---|---|---|---|---|
| HYG-BRANCHES | CODE | OPEN | Delete merged local+remote branches; archive-tag unmerged tips; close PR #1; drop stash | git branch -a / gh pr list / git stash list show only kept refs | |
| HYG-DIRS | CODE | OPEN | Stale worktree dirs, .next-prev-* (keep 20261001-224806), scratch -> quarantine/2026-10-02-cleanup | git worktree list; ls web/.next-prev-* | |
| GRADER-RED | CODE | OPEN | ops/ledger-revisions.txt stale (15 revisions) -> Check-Grader-Health red | check-grader-health.ps1 RESULT: ok | |
| SD51-REM | CODE | OPEN | Downsampler snapshot prunes, single-statement writer holds, wal_autocheckpoint lead; WAL 1.2 GB (8.5 GB 10-01) | store -race green; WAL ok after a deploy cycle | |
| TXLOCK | CODE | OPEN | main-writer DSN without _txlock=immediate | test proves reads don't take the write lock, or BLOCKED with reason | |
| SD30-LABEL | DECISION | BLOCKED | Prereg amendment as approved is unbuildable: 1m-bar-at-call coverage for the GRADED stocks 1d row is 22.3% (469/2104; all rows 54.2%) because the kept row is issued 00:00-02:00 UTC, after the forward session closed. Options (a) grade only pre-open rows (~37.7% pairs), (b) next-session close label, (c) keep caveat -> Nicholas | gate: coverage >= 80% | cl-c-sd30 step0 scripts (scratchpad sd30_step0*.py), mode=ro |
| SD54-FINRA | CODE | OPEN | /api/shorts + /api/short-interest operator-only (his yes 10-02) | member 403 tests; live anon/member refused | |
| JOURNAL | CODE | OPEN | ETF pickability in journal search; CI over independent days | journal tests | |
| DIGEST-TRIES | CODE | OPEN | member-digest try counts persisted | restart test keeps counts | |
| REGIMES-SIZE | CODE | OPEN | /api/regimes 3.6 MB on member pages | payload < 300 KB | |
| SLOW-OPS | CODE | OPEN | /api/postmortems, /api/research-loop stall 60-150 s under load | p95 < 2 s scratch daemon | |
| RV-COPY | CODE | OPEN | riskHeadline.ts:85 h=1 claim, meanDiff vs vsEwma, volforecast.go:28 caveat, RVLiveRecord start rule | tests | |
| SD31-NOTE | CODE | OPEN | model-health reasons say "lifetime" | test | |
| B6-CODE | CODE | OPEN | no localhost in og:image/sitemap when NEXT_PUBLIC_SITE_URL unset | built HTML grep | |
| CAL-N | CODE | OPEN | /calibration + postmortem.go:232 use deduped pairs, report independent n, distinct-days gate (PR #1 remainder) | test n = distinct (symbol, day) | |
| PREPUSH-COMMENT | CODE | OPEN | ops/githooks/pre-push comment says repo private (it is public) | diff | |
| WEBREL-PRUNE | CODE | OPEN | web-release.ps1 keeps newest 2 .next-prev-* | script test | |
| VACUOUS-SKIPS | CODE | OPEN | structregime/explain_test:71,129; pipeline/honestygaps_test:139; researchx/discover_test:171 | mutation check | |
| LINEAGE | CODE | OPEN | 5 unwired producers in lineage/doc.go:34-44 | wired + test, or BLOCKED | |
| AUDIT-H | CODE | OPEN | Fresh-eyes audit of 09-30..10-02 code (steps 3-10, rv-grader, anchor pinning, proof-honesty, copilot) | findings ledgered below | |
| GOVULN | CODE | OPEN | govulncheck ./... once | output clean or fixed | |
| RV-RERESOLVE | CODE | OPEN | rv-reresolve dry-run, then -apply (his yes 10-02) | counts compared to 10-01; CSV saved | |
| MAIN-PR | CODE | OPEN | PR public-launch -> main, merge on green CI | gh pr view merged | |
| LIVE-SWEEP | CODE | OPEN | Check logs for deployed-but-untriggered SD items; mark the fired ones | list | |
| PROOFDESK | CODE | OPEN | Owner queue + RV A1-A6 write-up on Proof Desk | cards posted | |
| ANCHORS-PUBLIC | OWNER | OWNER | Make anchors repo public; docs follow | | |
| ANCHOR-6H | OWNER | OWNER | Anchor-Publish trigger every 6 h (elevated) | | |
| TUNNEL-BOOT | OWNER | OWNER | Re-register Quick Tunnel with BootTrigger (elevated) | | |
| HOSTNAME | OWNER | OWNER | Stable hostname before 11-01 price rise; unblocks PUBLIC_URL, Google origin, B5, B6 | | |
| B5 | OWNER | OWNER | cloudflared tunnel login for the named tunnel | | |
| B6-ENV | OWNER | OWNER | NEXT_PUBLIC_SITE_URL once a hostname exists | | |
| B3 | OWNER | OWNER | Host off 38% of September | | |
| B7 | OWNER | OWNER | Old .db-journal backups, 655 MB .gz, daemon/.env.bak-* (delete by hand) | | |
| SD-27 | OWNER | OWNER | Turnstile keys | | |
| SD-52 | OWNER | OWNER | Optional Gmail app password rotation | | |
| STEP7-LIVE | OWNER | OWNER | Live execution NOT READY list | | |
| COUNSEL | OWNER | OWNER | Counsel on guardrails + Connect-Alpaca | | |
| STEP10 | OWNER | OWNER | Alpaca OAuth, IBKR account, CME licence | | |
| DEPENDABOT | OWNER | OWNER | Enable Dependabot alerts; optional main branch protection | | |
| STEP7-PAPER | DECISION | DECISION | First committed paper run | | |
| MIN-SIZE | DECISION | DECISION | Min order size vs 09-01 $10k override | | |
| MEMBER-COPILOT | DECISION | DECISION | SIGNALDECK_MEMBER_COPILOT on? | | |
| BEST-CALLS | DECISION | DECISION | Free vs paid; November signal-freeze lift; Alpaca email | | |
| CRYPTO-PROOF | DECISION | DECISION | Proof keeps crypto, member product drops it | | |
| VACUITY | DECISION | DECISION | Vacuity guard seeded-row fallback | | |
| AUTH-TRADEOFF | DECISION | DECISION | Sign-up timing oracle; verify-link login-CSRF shape | | |
| HOSTING | DECISION | DECISION | Paid hosting option B | | |
| RV-A1-A6 | DECISION | DECISION | Grader ambiguities: Proof Desk write-up | | |
| VOL-60D | WAIT | WAIT | 18/60 live days, verdict ~early Dec | | |
| SD30-WITHHOLD | CODE | OPEN | Withhold 1d/1w directional accuracy on every public/member surface with an SD-30 reason, one-line reversible flag, until the label decision | tests per surface + flag-reversal test | |
| DIR-WINDOW | WAIT | WAIT | 1d window (restarts with SD30-LABEL) | | |

## Findings added during the run
| ID | Cat | Status | Item | Done when | Evidence |
|---|---|---|---|---|---|
| F-SD30-KEPT-ROW | DECISION | DECISION | The graded 1d stocks row (latest per symbol per base, store/predict.go:409-424) is issued after the forward session closed: 93.8% of stocks / 78.6% crypto outcomes match the move visible at issue | Nicholas picks label design | cl-c-sd30 report |
| F-SD30-1W | DECISION | DECISION | Same leak material at 1w (stocks: agree 0.713 vs disagree 0.232 accuracy; outcome matches visible move 74.1%), pipeline/predict.go:1404-1425 | Nicholas picks label design | cl-c-sd30 report |
