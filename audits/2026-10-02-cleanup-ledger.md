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
| HYG-BRANCHES | CODE | VERIFIED | Delete merged local+remote branches; archive-tag unmerged tips; close PR #1; drop stash | git branch -a / gh pr list / git stash list show only kept refs | local: 34 deleted (only public-launch, main, keep/613bd2e + active cl-*); GitHub branches now: keep/613bd2e-provenance, main, public-launch; PR #1 closed with comment; stash dropped (tag archive/stash-2026-08-04 local); archive tags pushed only for the 2 already-public tips; ledger-provenance --check OK after |
| HYG-DIRS | CODE | OPEN | Stale worktree dirs, .next-prev-* (keep 20261001-224806), scratch -> quarantine/2026-10-02-cleanup | git worktree list; ls web/.next-prev-* | |
| GRADER-RED | CODE | FIXED | ops/ledger-revisions.txt stale (15 revisions) -> Check-Grader-Health red | check-grader-health.ps1 RESULT: ok | 4457fd4: ledger-provenance --diff recorded=149 unrecorded=0 (was 17 stale) |
| SD51-REM | CODE | OPEN | Downsampler snapshot prunes, single-statement writer holds, wal_autocheckpoint lead; WAL 1.2 GB (8.5 GB 10-01) | store -race green; WAL ok after a deploy cycle | |
| TXLOCK | CODE | OPEN | main-writer DSN without _txlock=immediate | test proves reads don't take the write lock, or BLOCKED with reason | |
| SD30-LABEL | DECISION | BLOCKED | Prereg amendment as approved is unbuildable: 1m-bar-at-call coverage for the GRADED stocks 1d row is 22.3% (469/2104; all rows 54.2%) because the kept row is issued 00:00-02:00 UTC, after the forward session closed. Options (a) grade only pre-open rows (~37.7% pairs), (b) next-session close label, (c) keep caveat -> Nicholas | gate: coverage >= 80% | cl-c-sd30 step0 scripts (scratchpad sd30_step0*.py), mode=ro |
| SD54-FINRA | CODE | FIXED | /api/shorts + /api/short-interest operator-only (his yes 10-02) | member 403 tests; live anon/member refused | 760ad29: member 403 at security.go 6b; SIGNALDECK_MEMBER_FINRA=1 reopens; TestMemberFINRAOperatorOnlyByDefault (public + proxied posture); browser: no short fetches flag off |
| JOURNAL | CODE | FIXED | ETF pickability in journal search; CI over independent days | journal tests | 914167a+3a68a02+85e8c9a: 56 ETF/BRK.B symbols now pickable via /api/journal/symbols, same predicate on POST; Wilson CI + 30 floor on distinct call days; mutations red |
| DIGEST-TRIES | CODE | FIXED | member-digest try counts persisted | restart test keeps counts | 7bcf7ce: member_digest_tries table, saved before send / delivery after; restart tests red under mutation |
| REGIMES-SIZE | CODE | OPEN | /api/regimes 3.6 MB on member pages | payload < 300 KB | |
| SLOW-OPS | CODE | OPEN | /api/postmortems, /api/research-loop stall 60-150 s under load | p95 < 2 s scratch daemon | |
| RV-COPY | CODE | OPEN | riskHeadline.ts:85 h=1 claim, meanDiff vs vsEwma, volforecast.go:28 caveat, RVLiveRecord start rule | tests | |
| SD31-NOTE | CODE | OPEN | model-health reasons say "lifetime" | test | |
| B6-CODE | CODE | OPEN | no localhost in og:image/sitemap when NEXT_PUBLIC_SITE_URL unset | built HTML grep | |
| CAL-N | CODE | OPEN | /calibration + postmortem.go:232 use deduped pairs, report independent n, distinct-days gate (PR #1 remainder) | test n = distinct (symbol, day) | |
| PREPUSH-COMMENT | CODE | OPEN | ops/githooks/pre-push comment says repo private (it is public) | diff | |
| WEBREL-PRUNE | CODE | OPEN | web-release.ps1 keeps newest 2 .next-prev-* | script test | |
| VACUOUS-SKIPS | CODE | FIXED | structregime/explain_test:71,129; pipeline/honestygaps_test:139; researchx/discover_test:171 | mutation check | 486a1d2 (merged 075538f): skips->Fatal in structregime x2, honestygaps, researchx, maintain:842; mutations red (orig PASSED/SKIPPED) |
| LINEAGE | CODE | FIXED | 5 unwired producers in lineage/doc.go:34-44 | wired + test, or BLOCKED | 8912e56+6d206bb: research-ledger writers (AST guard test) + evidence claim->feature edges wired; BLOCKED with reasons in doc.go: papertrade (no trade IDs), grader feature keys (closures), researchlab (no ID scheme), datasetver (nothing records version) |
| AUDIT-H | CODE | VERIFIED | Fresh-eyes audit of 09-30..10-02 code (steps 3-10, rv-grader, anchor pinning, proof-honesty, copilot) | findings ledgered below | read-only audit done 10-02 02:05; 12 findings + 4 plausible ledgered below (H-1 HIGH live) |
| GOVULN | CODE | FIXED | govulncheck ./... once | output clean or fixed | 8be0099: toolchain go1.26.6; govulncheck 'affected by 0 vulnerabilities' (was 6 stdlib) |
| RV-RERESOLVE | CODE | OPEN | rv-reresolve dry-run, then -apply (his yes 10-02) | counts compared to 10-01; CSV saved | |
| MAIN-PR | CODE | OPEN | PR public-launch -> main, merge on green CI | gh pr view merged | |
| LIVE-SWEEP | CODE | VERIFIED | Check logs for deployed-but-untriggered SD items; mark the fired ones | list | sweep 10-02 01:35: VERIFIED-LIVE SD-17,21,23,44,63,64 (+SD-02 per CI); SD-62 REGRESSED partial (1m36s hold); SD-03 not verifiable (ctl deploy writes no log); 19 NOT-YET-TRIGGERED -> WAIT row |
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
| H1-1H-GAP | CODE | FIXED | HIGH live: SD-55 holiday exemption (maintain.go:581-584) applies to 1h; 3,372/10,754 stock 1h outcomes since 10-01 graded across >3h gaps (max 14.7h) -> /api/honesty?horizon=1h | 1h 14h-gap row VOID test; 1d holiday still graded | 2325249 (merged 342e671): exemption 'tf == md.TF1d'; resolver_1h_gap_test 14.7h->VOID; mutations red (no guard / TF1m) |
| H1-REVOID | CODE | FIXED | One-off re-void tool for the wrongly graded 1h rows (rv-reresolve pattern, CSV of old values); dry-run live | dry-run count; apply only on Nicholas's yes (Proof Desk) | 84a3d49+7306676 cmd/score-1h-revoid: fixture dry-run 1 flagged, apply 1, re-apply 0; snapshot 3,423 bad of 7,421 (first 11h; control 0/23,442). Live dry-run after deploy; apply = Nicholas's yes |
| H2-COPILOT-TRACK | CODE | OPEN | copilot directional_track_record shows 1d#pm twins as model horizons, no gates, member tier | operator-only + horizon filter test | cl-j-copilot |
| H3-COPILOT-VOL | CODE | OPEN | copilot vol_forecast_record skill-shaped below 60-day floor, member tier | operator-only | cl-j-copilot |
| H5-CITATIONS | CODE | OPEN | CheckCitations exemption lets an uncited claim through | adversarial string test | cl-j-copilot |
| H6-ANCHOR-FALLBACK | CODE | OPEN | anchor-publish.sh SQLite fallback timestamps unverified anchors | fallback verifies head+sig or fails | cl-j-copilot |
| H9-ASK-CAP | CODE | OPEN | member ask pool cap check-then-increment race | concurrency test | cl-j-copilot |
| H12-ROUTEKEY | CODE | OPEN | datalicense_routes_test keys by path, POST bodies unscanned | mutation red | cl-j-copilot |
| VERIFY-VACUOUS | CODE | OPEN | verify_public_record.py prints PASS for skipped checks | test | cl-j-copilot |
| PROOFSTRIP | CODE | OPEN | /today ProofStrip ledger chip may ignore failing anchors (plausible) | verify then fix or N/A | cl-j-copilot |
| H4-SKILLBARS | CODE | OPEN | member SymbolAgentPanel shows unfloored per-signal hit rates green >=58% | hidden for members | cl-f-honesty |
| H8-BADGE | CODE | OPEN | /volatility badge green on any sufficient verdict | green only BEATS | cl-f-honesty |
| H10-VOLCACHE | CODE | OPEN | vol-forecast latest caches a warming 200 / stale verdict 10 min | per-request gate test | cl-f-honesty |
| H11-RISKCOPY | CODE | OPEN | riskHeadline.ts:111 'most predictable forecast' while INSUFFICIENT | copy test | cl-f-honesty |
| H7-WARM | CODE | OPEN | warm.go treats run deadline as shutdown, files ok | errors.Join ctx.Err test | cl-e (pending) |
| ACCURACY-503 | CODE | OPEN | /api/accuracy 503 at ~6000 ms x15 01:00-01:31 under contention | cheap query/cache or contention-only proof | cl-b-store |
| SD62-REGRESS | CODE | OPEN | PruneScoresKeepDailyLast held 1m36.8s (22:45), PruneComposite 35.4s | batched, hold log | cl-b-store |
| DOWNSAMPLER-TIMEOUT | CODE | OPEN | downsampler 15-min timeout every run since 10-01 20:28 (rollup SHMD); nothing pruned since 00:02 | runs complete | cl-b-store |
| SD60-CURSOR | CODE | OPEN | analyst coverage cursor save fails ~6/12 runs (busy writer) | cursor advances | cl-b-store |
| TASKHEALTH-TUNNEL | CODE | VERIFIED | Check-Task-Health lists 'CREATE SignalDeck Cloudflare Tunnel' drift despite SD-18 .pending honoured | explained or fixed | installer report 10-02 01:50: 'PENDING SignalDeck Cloudflare Tunnel staged in ops\tasks\.pending' (SD-18 works; old log lines predate it). Remaining drift 'REFUSE SignalDeck Quick Tunnel differs but is RUNNING' = owner TUNNEL-BOOT |
| LIVE-TRIGGER-WAIT | WAIT | WAIT | Not yet triggered: SD-06,08,10,12,14,15,16,19,20,22,29(11-25),41,42,43,46,47,57(10-02 13:10),59(10-05) | | sweep 10-02 |
| D-NULLQS | CODE | OPEN | Every member symbol page logs 'Uncaught TypeError: Cannot read properties of null (reading querySelector)' (flag on or off) | console clean in browser | web/src/app/s/[market]/[symbol] |
| D-DELISTED | CODE | OPEN | journal picker predicate ignores delisted_at (delisted ticker with old bars pickable; void after 10 sessions) | picker excludes symbols with no recent daily bar | store/journalsymbols.go:21 |
