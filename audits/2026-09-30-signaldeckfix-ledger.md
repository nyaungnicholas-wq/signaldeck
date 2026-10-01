# /signaldeckfix 2026-09-30 — task ledger

Repo: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `public-launch`.
Start: HEAD 349eff6, clean tree, in sync with origin; deployed daemon d63f47b (HEAD
differed only in docs/partials). Verification ran in a clean `git worktree` in the
session scratchpad, never the live tree. Round 1 (09-30) was deployed and pushed on the
owner's go-ahead; round 2 (10-01, "fix them all") re-registered the directional graded
window on the owner's choice and closed the rows marked 10-01 below.

Statuses: FIXED = changed + verified locally (tests, mutation-checked where noted) but
NOT deployed; VERIFIED = also confirmed against live behaviour; BLOCKED = needs the
owner; NOT APPLICABLE = by design, with evidence.

## Ledger

| ID | Component | Finding (evidence) | Fix / outcome | Sev | Status |
|----|-----------|--------------------|---------------|-----|--------|
| SD-01 | web deps | CI `web` red: next 16.3.4 has critical RCE GHSA-vcvr-r3jv-pc5j in next/og ImageResponse; `src/app/opengraph-image.tsx` uses it; site is public | next + eslint-config-next 16.3.8 (lock: 12 entries, version only). `npm audit --omit=dev`: 0. lint/tsc/42 tests/build green | critical | FIXED f0bfd07 |
| SD-02 | daemon lint | CI `daemon` red since 09-29: S1012/QF1008 prioritygate.go, unused smtpBody | fixed; golangci-lint 0 issues | med | FIXED f0bfd07 |
| SD-03 | process | 3 pushes deployed+pushed red; deploy gate ran only `go test` | `signaldeck-ctl.sh deploy` now runs go vet + golangci-lint first (refuses on failure) | med | FIXED f0bfd07 |
| SD-04 | auth / storage governor | LIVE: `POST /api/auth/login` 500 after 24.2s at 19:56 and 20:03 PT. Lock holder: governor's size-keyed "emergency" VACUUM of the 7.2 GB file outside 2-6am ET (freelist crossed 5%); etilqs temp 6.4 GB; each deploy killed it before `storage_last_vacuum` was stamped, so every boot restarted it (runs 18:32 orphaned 2093s, 19:10/19:31 stopped) | VACUUM only in the 2-6am ET window, read via injectable clock. Test fails on old code ("vacuumed=true" at 21:00 ET). The live VACUUM was left to finish (20:33:57, vacuumed=true) — not restarted; 4 sign-ins failed during it (19:56, 20:03, 20:17, 20:32). CORRECTION (post-deploy review): a 5th failed at 21:08:48 with no VACUUM running — see SD-51 | high | FIXED fea1a19 (VACUUM cause only) |
| SD-05 | auth lockout | lockout key lowercased but lookup exact-case + counter cleared before verified check: register "OWNER", sign in, resets "owner"'s counter every 4 guesses; sweep forgot a capped name the instant its lock ended; unbounded username = map key + log line; IPv6 /64 = unlimited buckets | exact-name key; clear only when a session is issued; capped names remembered 24h; >32-byte names refused; limiter folds IPv6 /64 | high | FIXED fea1a19 (3 tests mutation-checked) |
| SD-06 | accounts | `go test -race`: DATA RACE in TestSignupRateLimited (mail goroutines outlive the request; test swaps globals) — masked in CI by the lint step | one tracked `mailInBackground` helper; test cleanup waits | med | FIXED fea1a19 |
| SD-07 | public site | `/api/waitlist` 401 for every visitor (open only in publicRoutes; live daemon published without PublicSurface) | legacy branch opens it (POST-only route); test covers every public-page route in that posture | high | FIXED 7d377e6 |
| SD-08 | public site | sign-up impossible: no Turnstile site key -> widget returned "" -> submit disabled forever | widget hands back a sentinel; daemon skips the check without a secret and now logs that once | high | FIXED 7d377e6 |
| SD-09 | landing honesty | hash-chain "every link matches" printed from the animation, never from `chainVerified`; retired panel showed 1d +0.7pp in red under "worse than guessing"; "14 predictors graded" counted benchmarks + an n=0 row (real: 7); "claims" (95 of 129 are bookkeeping) | check line states the daemon's verdict + break point; negative-skill retired row, sign-aware sentence; honest count; "records" | high | FIXED 7d377e6 |
| SD-10 | web forms | /forgot nested `<a>` (React #418); /reset re-enabled during success redirect (2nd click spends token); account logout unhandled rejection | fixed | low | FIXED 7d377e6 |
| SD-11 | resolver | 1d resolution wedged since ~09-10: single oldest-first batch of 1500, all permanently skipped by the 3-horizon gap guard (pre-holiday Fridays: Fri->Tue 3d6h). 28/30 runs "resolved 0"; 1d resolved 2-166 rows/day, 44,162 owed. 1w unaffected | keyset paging through the whole queue; labels/guards unchanged. Test: 1501 stuck rows ahead of 1 resolvable — old code "resolved 0 predictions" | high | FIXED 5c9940b |
| SD-12 | password reset | redeem then SetPassword (3 autocommits): a timeout spent the link and left old password + sessions | `Store.ResetPassword`: one transaction (redeem, hash, void links, end sessions, verify); bcrypt before priority; fresh budget for sign-in | med | FIXED 412c3aa |
| SD-13 | edge | quick tunnel serves http:// as-is (verified 200) -> session cookie without Secure | Next proxy 308s edge http -> https (loopback untouched). Verified on a local build: http edge 308 w/ path+query, https 200, loopback 200, POST 308 | med | FIXED 412c3aa |
| SD-14 | ai-analyst | `llm: provider error: HTTP 400` with the body discarded (cause unknowable); a 400 after a timed-out attempt filed as permanent `error` | body carried (sanitized); 400 after a retryable failure -> ErrTransient; first-attempt 400 still permanent (existing guard test passes) | med | FIXED 412c3aa |
| SD-15 | research-lab | loops never read ctx: 5 of 6 forced shutdowns (8-20 min past grace) orphaned the fleet; Bonferroni look-counter SetMeta discarded on cancel | stops on cancel; counter written on a non-cancelled ctx and its error returned. No unit test (no seam to cancel mid-loop) | med | FIXED 412c3aa |
| SD-16 | scheduler | weekly gate used midnight+N hours: 2027-03-14 17:00 EDT fire refused, week lost (weekly-report, digest, signalbt share it) | wall-clock hour; test fails on old code | low | FIXED 412c3aa |
| SD-17 | published honesty | selection_honesty calls_up read the whole table (1d 47% UP) vs graded window 6% UP: reason said "calling DOWN on 52.9%" of a book that called DOWN on 94% | graded window only; epoch pinned to the grader by test (grader not imported: hash-pinned) | med | FIXED 412c3aa |
| SD-18 | ops | Check-Task-Health exit 1 daily since 09-21: installer ignored ops/tasks/.pending (staged Cloudflare tunnel read as CREATE; -Install would register a credential-less tunnel); Quick Tunnel (public URL) had no xml; drift probe ignored exit code; export banner "EXPORTED  tasks" | .pending honoured (PENDING, skipped by -Install); Quick Tunnel xml exported; exit code checked; @().Count. Report: 22 correct, 1 staged; PS 5.1 parse clean | med | FIXED 917535d |
| SD-19 | security (latent) | rawDataRefused, licence gate, MCP anon-loopback keyed on ReachablePrivately(); public tunnel arrives via the KEYED loopback web proxy; held only because a stale ngrok host sits in ALLOWED_HOSTS (docs advise removing it) | keyed on published() (the 0dfa718 member-tier fix, siblings missed). Test: old code serves raw bars 200, new 451. No change on the live box today | high | FIXED 4f0a16f |
| SD-20 | scheduler | LastWorkerRunAt counted orphaned / running / stopped-(shutdown) runs: slots pushed a whole period (finra-shorts +33h, congress-poller +19h) | interrupted runs excluded; errors still count | med | FIXED 85c5e84 |
| SD-21 | news-fetcher | pass aborts at first 429 and always restarted alphabetically -> symbols past the rate-limit point never fetched | next pass resumes after the failing symbol | low | FIXED 85c5e84 |
| SD-22 | public track record | 120k row cap binding again (1w graded 128,834): oldest graded days silently dropped, ~10k/day | whole graded window (epoch bounds it); test with 20,001 rows (old: 20,000) | med | FIXED a103c34 |
| SD-23 | accounts | sessions grow without bound under a login loop (user 5: 276 live) | 20 newest per account, trimmed in the insert transaction | low | FIXED d67c440 |
| SD-24 | storage budget | gap-fill paused 7+ days: DB 7164 MB vs 6144 MB budget (gate opens at 5632). Budget is a tripwire nothing shrinks toward; floor above budget since 09-27; features table not yet at its 180d retention. 35 streamed symbols carry session gaps; gap-fill window is 30 days, so 09-14 gaps become unrepairable ~10-14 | 10-01: one Go default `pipeline.DefaultBudgetDBMB = 10240` (gap-fill gate, sdmaint flag) and refresh.sh's default; backups tripwire 21504 -> 35840 (its derive_budget_mb invariant). daemon/.env carries no SIGNALDECK_BUDGET_DB_MB pin (checked by count) | high | FIXED 8a1cc9f |
| SD-25 | deploy | fixes committed locally; deployed revision was d63f47b | Nicholas authorized "deploy it and push after all of it are done" (2026-09-30 ~21:22 PT): ctl deploy + web npm ci/build/restart + push after the final ladder | — | see Verification |
| SD-26 | Quick Tunnel principal | task runs InteractiveToken (dies at logoff/console events) | `ops\fix-task-principals.ps1 -Match 'Quick Tunnel' -NoRestart` in an ELEVATED shell, then re-export. 10-01: the task's principal is already S4U on the live box (read-only Get-ScheduledTask: LogonType S4U, RunLevel Limited; xml re-exported in f26bc69), and web-guard now restarts a dead quick tunnel within 5 min (34489d0, 8b12ddc) | med | VERIFIED (S4U live) |
| SD-27 | Turnstile | no keys; sign-up/reset guarded only by honeypot + limiters (SMTP reputation risk) | owner adds SIGNALDECK_TURNSTILE_SITE_KEY/SECRET | med | BLOCKED (owner credentials) |
| SD-28 | member tier | members reach only publicRoutes + GET /api/watchlist; /api/subscribe is operator-only, so a member watchlist can never be filled; tour links and /lab/live 403 or render "vundefined" for members | 10-01: the concurrent session shipped a member product (181ee74, 6c4addb, f0aeb97, live at 4e0bf95): members get /api/watch + /api/unwatch on their OWN list (no global ingestion), symbol pages and public-domain data. Not verified live by this run: it would need a member account, which this run does not create | med | FIXED (other session; not verified here) |
| SD-29 | grading protocol | 1d gap guard counts calendar seconds: every pre-holiday Friday 1d row is never graded (selection in the 1d record) | 10-01: for stocks the NYSE calendar decides (no whole session closed between target and fwd = next session); crypto keeps the 3-horizon rule. PREREGISTRATION.md does not pin the gap rule and the grader already admits Fri->Tue, so only the resolver was refusing. Test across Labor Day + crypto twin, mutation-checked both ways | med | FIXED d6ab489, 9a42ed5 |
| SD-30 | point-in-time | settled-base rule (09-07): label window starts at the last settled close, BEFORE the prediction; minute bars / crypto micro snapshots are untrimmed and sit inside it (554/554 post-09-07 crypto 1d rows resolve on a bar stamped <= ts). No leakage signature found, not excluded | research question for the owner | med | BLOCKED (research) |
| SD-31 | model health | DirectionalRecord(...,0) grades the LIFETIME ledger (1d 12,439 @ 47.6% vs graded 687 @ 50.5%); StructuralRecords no survivorship epoch | By design: lifetime record retires, clean post-epoch shadow readmits — pinned by TestModelHealthWorkerPersistsReadmission (an epoch floor broke it; reverted). Operator-only surface. Note: reason strings don't say "lifetime" | low | NOT APPLICABLE |
| SD-32 | expectancy-trainer / forecast-monitor | degraded: anti-predictive benched; coverage starved, self-labelled EXPECTED (1d retired) | by design (memory: starvation-is-honest, book-is-shut) | — | NOT APPLICABLE |
| SD-33 | cleanup task | exits 0 while "17,759 MB NOT RECLAIMED" | by design: backups moved to recoverable ~/.Trash (18 GB), message is honest; emptying it is a permanent delete for the owner | — | NOT APPLICABLE |
| SD-34 | research-liveness | 4 unverifiable claims | July history, quarantined + acknowledged; current runs persist 48 judgments | — | NOT APPLICABLE |
| SD-35 | /proof | ledger/verify 503 at 30s | occurred during the VACUUM; now 200 in 3.9s | — | NOT APPLICABLE (SD-04 cause) |
| SD-36 | README | trend63 "first grade 2026-09-25" while resolver gate opens ~10-17 | generated by the hash-pinned grader; structural-liveness already flags it | low | NOT APPLICABLE (pinned) |
| SD-37 | web lint | 2 exhaustive-deps warnings | callers pass stable setState/router | — | NOT APPLICABLE |
| SD-38 | market-close | StartWhenAvailable catch-up at boot runs `ctl stop` (09-30: ~4.5 min public outage) | 10-01: market-close stops only the ngrok tunnel and the daemon, so the public web stays up (test-market-close-keeps-web.sh, now in CI). Residual: catch-up runs still stop the daemon for the whole offline backup incl. the offsite upload (~7.7 min on 09-30) | low | FIXED 34489d0 (web); daemon residual = SD-57 |
| SD-39 | WAL | after the VACUUM the WAL reached ~6.9 GB (TRUNCATE BUSY, 267 attempts) | the 20:50 governor pass checkpointed 72,934 frames: DB file 7.5 -> 6.44 GB, WAL 683 MB; not forced | low | NOT APPLICABLE (self-resolved) |
| SD-40 | hygiene | stale worktree %TEMP%\sd-cov-baseline @223f1aa (only an untracked coverage file) | removed at session end with this session's worktree | low | see end |
| SD-41 | review: llm | 400 reclassified transient after ANY retryable attempt, incl. a 401/403 key rotation (masks a real max_tokens-style 400) | only after timeout/network/5xx/429 (keyRejected type) | med | FIXED 287cd1d |
| SD-42 | review: research-lab | a stop returned before the look counter + day key: next boot re-surveyed the same shadows (double streak step), stopped looks never reached the divisor | closeDay(looks) on every exit after a grading | med | FIXED 287cd1d |
| SD-43 | review: lockout | 24h memory made sweep a full-map scan per sign-in; name-spraying could grow it | sweep <= 1/min; past 50k entries memory falls back to 15 min | med | FIXED 287cd1d |
| SD-44 | review: resolver | keyset paging re-scanned the unresolved index per page | one whole-queue read (LIMIT -1); net 15 lines vs the original | low | FIXED 287cd1d |
| SD-45 | review: proxy | redirect host from client-passable X-Forwarded-Host; no Cache-Control | Host only + no-store | low | FIXED 287cd1d |
| SD-46 | review: config | OpenSignup/PublicReads defaults keyed on bind+allowlist only (A9 third shape) | also closed by TUNNEL_LOG / PUBLIC_URL; live .env pins both | low | FIXED 287cd1d |
| SD-47 | review: verify | redeem-then-SetEmailVerified could spend the link unverified | Store.VerifyEmail one transaction; dead ConsumeAuthToken/SetEmailVerified removed | low | FIXED 287cd1d |
| SD-48 | review: fleetSkillWindow -1 | whole graded window loads every SWR rebuild; grows ~10k rows/day (1w) | 10-01: store.IndependentPredictionOutcomes collapses per (symbol, settle day) in SQL; payload byte-identical on a fixture; 1w reads 9,120 rows instead of 128,834. Root cause of the 153-223 s builds not profiled | low | FIXED 599de96 |
| SD-49 | review: session trim | DELETE scans sessions (no user_id index) | table bounded by the cap itself (20/account) | low | NOT APPLICABLE |
| SD-50 | concurrency | another session ("Signal Deck readiness") committed 7d0c058 (long write-lock hold logging) on this branch at 21:13; my cross-session note expired unapproved | its files untouched; my commits stage explicit paths only | — | NOTED |
| SD-51 | sign-in vs long writes | post-deploy review: POST /api/auth/login 500 at 21:08:48 (SQLITE_BUSY, 24.2s) 35 min after the VACUUM; the long-hold log (7d0c058) shows non-VACUUM writer holds over sign-in's 2x12s budget since deploy: DeleteScoreOutcomesBefore 142.9s, UpsertBars (alpaca backfill) 107.9s, UpsertPredictionAttested 92.5s, StartWorkerRun 50s (CPU-starved host inflates them) | 10-01: the other session's chunked UpsertBars (1000/tx) and reader-side batched DeleteScoreOutcomesBefore are kept; this run adds idx_outcomes_ts (the prune's key read full-scanned 8.86M rows), batches the six other retention prunes, and tests that an account write lands mid-prune / mid-upsert (mutation-checked). Unchanged by design: UpsertPredictionAttested (3 point writes; its long times include a passive auto-checkpoint) | high | FIXED 70c5823 |
| SD-52 | secret hygiene | the token recipe in this run's verification brief (`set -a; . daemon/.env`) fails on unquoted values at .env lines 53/60; bash printed a 4-char fragment of SIGNALDECK_SMTP_PASS into local agent transcripts | owner: quote both values in daemon/.env; consider rotating the Gmail app password; extract the token with grep, never source the file | med | BLOCKED (owner secret) |
| SD-53 | public tunnel | Quick Tunnel cloudflared exited 22:25:58 ('signal terminated', task 0xC000013A = SD-26 failure mode); public URL 530 for ~12 min | restarted the "SignalDeck Quick Tunnel" task 22:37:59; new trycloudflare URL registered 22:39 (old links dead); permanent fix is SD-26 | high | RESTORED; permanent fix in place (SD-26) |
| SD-54 | concurrency | while this run verified, another session deployed 1fa2fe5 (21:49), 73652be (22:19) and 44acb40 (22:30), merged members-and-proof into public-launch locally (unpushed; diverged from origin), and its 181ee74 widened memberRoutes to /api/shorts, /api/short-interest (the code comment cites FINRA terms) | not mine to change; flagged to the owner | med | NOTED |
| SD-55 | resolver (score outcomes) | sd29 review: maintain.go's score-outcome resolver used the same calendar-seconds 3-horizon rule and VOIDED (final) every pre-holiday Friday 1d stock outcome | same NYSE-calendar rule as SD-29; Labor Day test, mutation-checked both ways | med | FIXED 36625a2 |
| SD-56 | resolver (benchmark) | after the re-registration grade, the prequential-majority (1d) row was missing from the registry: 39,232 1d#pm rows in the window, 0 resolved. Cause: the #pm pass ran after the whole ensemble queue at ~13 rows/s, and five restarts in two hours (10-01 05:19-07:34Z) killed every pass inside the 1d drain | the twin resolves in the same step as its ensemble row via store.ResolveOpenPrediction (writes only while resolved_at IS NULL); the #pm pass still sweeps; mutation-checked | high | FIXED 4de23b9 |
| SD-57 | backup / market close | residual of SD-38: on catch-up runs the daemon (API, sign-in) stays down through the offsite upload | restart the daemon after the local VACUUM INTO + verify, before upload; the script's later meta/dq writes assume a stopped daemon and must move to busy_timeout | low | OPEN (follow-up) |
| SD-58 | accuracy publication | /api/accuracy 503 REFUSED since SD-11 graded the collapsed 1d day 2026-09-14 | owner chose "re-register after the fix": prereg seq 130 grading-window-reregistration-2 (window from 2026-09-25, filed with knowledge of the outcome, stated in the record), deploy e311610 re-pinned the grader (seq 131 grading-protocol, 132 prereg-document); regrade 10-01 00:34 PT: HTTP 200 status OK, directional 1d INSUFFICIENT DAYS 4/10 as the amendment predicted | high | VERIFIED e311610 |
| SD-59 | tests | fixtures time-bombed on the old epoch: selfaudit tests stamped now-10d (fell before the new window until 10-05; one passed vacuously), resolver/collapse/track-record fixtures hard-coded 2026-08 dates | re-anchored on store.GradingEpoch itself (incl. the other session's new collapse-cache test); resolver fixtures moved to exchange-midnight bars past the 09-08 settled-base cutover; mutation-checked | med | FIXED ae60967, e311610 |
| SD-60 | review: analyst | the new coverage-cursor write ran on the request context through the single writer from GET /api/ai/analyst (unbounded) | 3 s budget; losing it repeats the window | low | FIXED 9a42ed5 |
| SD-61 | ops: quick-tunnel guard | reported by a concurrent session's review: web-guard counted an UNREADABLE cloudflared as the quick tunnel; from its S4U/Limited token every SYSTEM process reads empty, so the named-tunnel service (once installed) would have masked a dead quick tunnel forever | a Running task is the tunnel (its action waits on cloudflared); otherwise only a cloudflared naming the quick tunnel counts. 2 cases, mutation-checked; live run 04:05:44 PT 'quicktunnel ok' | med | FIXED 8098341 |

### Deferred with reason (not fixed this run)
- FIXED 10-01 (377ed9a): `ResolvedPredictionCount` now returns the grader's independent N.
- NOT APPLICABLE (critic 10-01): "ai-analyst cancelled runs filed ok" is by design: workers.go files a
  shutdown as ok with detail 'stopped (shutdown)', and SD-20 keeps those runs out of scheduling.
- FIXED 10-01 (a70dbb7, 06525d7): mcp sink failures log once per streak; stage5 reads the graded
  record once; OpenSignup doc corrected; the per-symbol graded read is tested in its production form.
- OPEN (SD-51 remainder, critic 10-01): Downsampler bar/snapshot prunes (up to 50k rows per DELETE),
  Rollup (46 s holds), and single-statement holders InsertNews/UpsertVolForecast/InsertMacro (likely
  auto-checkpoint inflation, unmeasured); `wal_autocheckpoint=0` on the account writer is an
  unmeasured lead. The long-hold log counts a post-commit auto-checkpoint as hold time.
- OPEN (SD-48 remainder): the cause of the 153-223 s 1w track-record builds needs a CPU profile on the
  live host.
- Main writer DSN without `_txlock=immediate` (read-then-write tx vs the new aw writer could
  SQLITE_BUSY_SNAPSHOT): 0 occurrences in logs; changes lock timing of every main-writer tx. Low.
- Sign-up timing distinguishes new vs taken email (sync insert vs async mail); verify link signs
  the clicker into the creator's account (login-CSRF shape). Both are design trade-offs. Low.
- FIXED 10-01 (377ed9a, 255cb67, 9a42ed5): ai-analyst rotates a cursor over all symbols;
  sentiment-tagger files ErrTransient as degraded; mcp audit write/close errors recorded;
  signal-report reads the graded, deduplicated window; undelivered responses log status 499.

## Verification (final HEAD)

- Final local ladder at 792f6f8 (scratch worktree): daemon build/vet/linux cross-build/archive build/
  manifest/golangci-lint 0/tree clean/coverage 70.7% (store 70.3, forecast 93.8, canary 81.2); web
  npm ci/lint/tsc/42 tests/build/prod audit 0 vulns; tools unittest + script-style + includes +
  scan md + scan code + controls + deck + repro; pre-publish scan; ledger provenance; docs gates.
  Two load artefacts, both re-run clean: TestPublicReadsFalseGatesTrends timed out at 43s while
  After Effects held ~16 of 32 cores (5/5 isolated passes; full internal/api -race re-run ok 705s);
  cold-clone check segfaulted in Git Bash under load (standalone at 792f6f8: COLD-CLONE OK).
- Missed by the local ladder, caught by CI: STRATEGY_DECK/partials controls table (maintain 44 -> 45
  tests) -> 86dfc68. CI on 86dfc68: ALL SIX JOBS GREEN (daemon incl. Linux race + coverage floors,
  web, tools, docs-gate, pre-publish-scan, ledger-provenance) — first fully green run since 09-25.
- Deploy: the concurrent session's ctl deploy put 1fa2fe5 live at 21:49 (all fixes), later
  73652be / 44acb40. Web: npm ci 22:01 (next 16.3.8 runtime live, RCE closed); web-release
  candidate .next-release-1fa2fe5 (built pre-merge, asset gate PASSED 22:02; browser gate FAILED
  22:03 on /accuracy while the daemon was CPU-starved — the old build failed identically) re-gated
  22:29 PASSED 7/7, promoted 22:31 with the script's swap + launcher restart (build
  IKKXuka3kFg3ZpPVUSVM5, 8323 asset check 28/28; :3000 healed by web-guard 22:37, 28/28). Not
  rebuilt from the live tree because it held another session's unpushed /proof copy saying the
  anchors repo is public (gh api: it is private).
- Live through the restored tunnel: / 200 with the new copy; http:// -> 308 https (path+query kept,
  no-store); GPTBot 403; POST /api/waitlist bad body -> 400 (was 401); register bad body -> 400;
  forged Origin -> 403; anonymous raw export -> 401, operator -> 451.
- Independent post-deploy workflow (4 read-only lenses + completeness critic): LIVE-VERIFIED SD-01,
  05 (overlong name refused pre-log; exact-name lockout 429 on the 6th), 07, 09, 11, 13, 18, 45;
  TEST-ONLY (no live trigger tonight) SD-02/03/06/08/10/12/14/15/16/19/20/21/22/23/41-44/46/47;
  NOT-YET-LIVE SD-17 (next grader run); CONTRADICTED SD-04 -> SD-51.
- Consequence of SD-11 (owner decision): unblocking 1d resolution graded 1,217 rows from 2026-09-14,
  a collapsed cross-section (28 distinct probabilities / 324 symbols), so /api/accuracy now answers
  503 REFUSED for the whole window. That is the pre-registered gate doing its job on rows the wedge
  hid; it clears only by re-registering the window. Not reverted.

## Verification, round 2 (2026-10-01)

- Re-registration: prereg seq 130 (dry run first; chain INTACT before and after), deploy e311610
  VERIFIED, registrar appended seq 131/132; /api/accuracy 503 REFUSED -> 200 OK.
- Integration fixes-1001 (7 workflow branches cherry-picked + review fixes + SD-55/56) merged with
  another session's 4 commits, deployed cf9cdad: `deploy VERIFIED`, /api/version unmodified.
- Live after deploy: idx_outcomes_ts present; backfill-reconciler "gap-filled 6 streamed" (was
  "gap-fill paused" for 7+ days); prediction-resolver reports "(+N benchmark twins)" (0 until the next
  session settles); 1d and 1d#pm in step (33,850 resolved of 39,376 each); 0 voided 1d score outcomes
  stamped 09-04..09-08 (watch as the outcome-resolver head passes 09-04).
- Every new test mutation-checked (removing its fix fails it). Local ladder load artefacts, each
  re-run clean in isolation: TestStressRunAuthAndLimits (register timed out under -race + load; 3.6 s
  alone), internal/store -race over 30 min under load (1685 s alone), golangci-lint 21 errcheck from a
  stale shared cache of a deleted worktree (0 issues on a fresh cache), cold-clone Git Bash segfault.
- Regrade 02:22 PT after the #pm drain: prequential-majority (1d) restored; repro re-cut (--verify
  identical, --verify-complete seq 131), frozen verdicts re-read (15 rows = live registry).
- CI: 28461e2 / 70ee393 / 3b9b99c / cf9cdad red on tools (frozen verdicts, then the controls table
  45 -> 47); 208ee55 ALL SIX JOBS GREEN (daemon incl. Linux race + coverage floors).
