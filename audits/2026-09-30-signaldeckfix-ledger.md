# /signaldeckfix 2026-09-30 — task ledger

Repo: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck`, branch `public-launch`.
Start: HEAD 349eff6, clean tree, in sync with origin; deployed daemon d63f47b (HEAD
differed only in docs/partials). Verification ran in a clean `git worktree` in the
session scratchpad, never the live tree. Nothing was deployed or pushed (see BLOCKED).

Statuses: FIXED = changed + verified locally (tests, mutation-checked where noted) but
NOT deployed; VERIFIED = also confirmed against live behaviour; BLOCKED = needs the
owner; NOT APPLICABLE = by design, with evidence.

## Ledger

| ID | Component | Finding (evidence) | Fix / outcome | Sev | Status |
|----|-----------|--------------------|---------------|-----|--------|
| SD-01 | web deps | CI `web` red: next 16.3.4 has critical RCE GHSA-vcvr-r3jv-pc5j in next/og ImageResponse; `src/app/opengraph-image.tsx` uses it; site is public | next + eslint-config-next 16.3.8 (lock: 12 entries, version only). `npm audit --omit=dev`: 0. lint/tsc/42 tests/build green | critical | FIXED f0bfd07 |
| SD-02 | daemon lint | CI `daemon` red since 09-29: S1012/QF1008 prioritygate.go, unused smtpBody | fixed; golangci-lint 0 issues | med | FIXED f0bfd07 |
| SD-03 | process | 3 pushes deployed+pushed red; deploy gate ran only `go test` | `signaldeck-ctl.sh deploy` now runs go vet + golangci-lint first (refuses on failure) | med | FIXED f0bfd07 |
| SD-04 | auth / storage governor | LIVE: `POST /api/auth/login` 500 after 24.2s at 19:56 and 20:03 PT. Lock holder: governor's size-keyed "emergency" VACUUM of the 7.2 GB file outside 2-6am ET (freelist crossed 5%); etilqs temp 6.4 GB; each deploy killed it before `storage_last_vacuum` was stamped, so every boot restarted it (runs 18:32 orphaned 2093s, 19:10/19:31 stopped) | VACUUM only in the 2-6am ET window, read via injectable clock. Test fails on old code ("vacuumed=true" at 21:00 ET). The live VACUUM was left to finish (20:33:57, vacuumed=true) — not restarted; 4 sign-ins failed during it (19:56, 20:03, 20:17, 20:32); last SQLITE_BUSY 20:32:13, none after | high | FIXED fea1a19 |
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
| SD-24 | storage budget | gap-fill paused 7+ days: DB 7164 MB vs 6144 MB budget (gate opens at 5632). Budget is a tripwire nothing shrinks toward; floor above budget since 09-27; features table not yet at its 180d retention. 35 streamed symbols carry session gaps; gap-fill window is 30 days, so 09-14 gaps become unrepairable ~10-14 | Owner decision: raise SIGNALDECK_BUDGET_DB_MB (agent estimate 10240) in daemon/.env + restart; or shorten features retention (destructive to hot rows) | high | BLOCKED (owner config + restart) |
| SD-25 | deploy | fixes committed locally; deployed revision was d63f47b | Nicholas authorized "deploy it and push after all of it are done" (2026-09-30 ~21:22 PT): ctl deploy + web npm ci/build/restart + push after the final ladder | — | see Verification |
| SD-26 | Quick Tunnel principal | task runs InteractiveToken (dies at logoff/console events) | `ops\fix-task-principals.ps1 -Match 'Quick Tunnel' -NoRestart` in an ELEVATED shell, then re-export | med | BLOCKED (admin) |
| SD-27 | Turnstile | no keys; sign-up/reset guarded only by honeypot + limiters (SMTP reputation risk) | owner adds SIGNALDECK_TURNSTILE_SITE_KEY/SECRET | med | BLOCKED (owner credentials) |
| SD-28 | member tier | members reach only publicRoutes + GET /api/watchlist; /api/subscribe is operator-only, so a member watchlist can never be filled; tour links and /lab/live 403 or render "vundefined" for members | product decision: what members get | med | BLOCKED (owner product call) |
| SD-29 | grading protocol | 1d gap guard counts calendar seconds: every pre-holiday Friday 1d row is never graded (selection in the 1d record) | owner/prereg decision (guard in sessions?) | med | BLOCKED (protocol) |
| SD-30 | point-in-time | settled-base rule (09-07): label window starts at the last settled close, BEFORE the prediction; minute bars / crypto micro snapshots are untrimmed and sit inside it (554/554 post-09-07 crypto 1d rows resolve on a bar stamped <= ts). No leakage signature found, not excluded | research question for the owner | med | BLOCKED (research) |
| SD-31 | model health | DirectionalRecord(...,0) grades the LIFETIME ledger (1d 12,439 @ 47.6% vs graded 687 @ 50.5%); StructuralRecords no survivorship epoch | By design: lifetime record retires, clean post-epoch shadow readmits — pinned by TestModelHealthWorkerPersistsReadmission (an epoch floor broke it; reverted). Operator-only surface. Note: reason strings don't say "lifetime" | low | NOT APPLICABLE |
| SD-32 | expectancy-trainer / forecast-monitor | degraded: anti-predictive benched; coverage starved, self-labelled EXPECTED (1d retired) | by design (memory: starvation-is-honest, book-is-shut) | — | NOT APPLICABLE |
| SD-33 | cleanup task | exits 0 while "17,759 MB NOT RECLAIMED" | by design: backups moved to recoverable ~/.Trash (18 GB), message is honest; emptying it is a permanent delete for the owner | — | NOT APPLICABLE |
| SD-34 | research-liveness | 4 unverifiable claims | July history, quarantined + acknowledged; current runs persist 48 judgments | — | NOT APPLICABLE |
| SD-35 | /proof | ledger/verify 503 at 30s | occurred during the VACUUM; now 200 in 3.9s | — | NOT APPLICABLE (SD-04 cause) |
| SD-36 | README | trend63 "first grade 2026-09-25" while resolver gate opens ~10-17 | generated by the hash-pinned grader; structural-liveness already flags it | low | NOT APPLICABLE (pinned) |
| SD-37 | web lint | 2 exhaustive-deps warnings | callers pass stable setState/router | — | NOT APPLICABLE |
| SD-38 | market-close | StartWhenAvailable catch-up at boot runs `ctl stop` (09-30: ~4.5 min public outage) | owner's schedule call | low | BLOCKED (owner) |
| SD-39 | WAL | after the VACUUM the WAL reached ~6.9 GB (TRUNCATE BUSY, 267 attempts) | the 20:50 governor pass checkpointed 72,934 frames: DB file 7.5 -> 6.44 GB, WAL 683 MB; not forced | low | NOT APPLICABLE (self-resolved) |
| SD-40 | hygiene | stale worktree %TEMP%\sd-cov-baseline @223f1aa (only an untracked coverage file) | removed at session end with this session's worktree | low | see end |
| SD-41 | review: llm | 400 reclassified transient after ANY retryable attempt, incl. a 401/403 key rotation (masks a real max_tokens-style 400) | only after timeout/network/5xx/429 (keyRejected type) | med | FIXED 287cd1d |
| SD-42 | review: research-lab | a stop returned before the look counter + day key: next boot re-surveyed the same shadows (double streak step), stopped looks never reached the divisor | closeDay(looks) on every exit after a grading | med | FIXED 287cd1d |
| SD-43 | review: lockout | 24h memory made sweep a full-map scan per sign-in; name-spraying could grow it | sweep <= 1/min; past 50k entries memory falls back to 15 min | med | FIXED 287cd1d |
| SD-44 | review: resolver | keyset paging re-scanned the unresolved index per page | one whole-queue read (LIMIT -1); net 15 lines vs the original | low | FIXED 287cd1d |
| SD-45 | review: proxy | redirect host from client-passable X-Forwarded-Host; no Cache-Control | Host only + no-store | low | FIXED 287cd1d |
| SD-46 | review: config | OpenSignup/PublicReads defaults keyed on bind+allowlist only (A9 third shape) | also closed by TUNNEL_LOG / PUBLIC_URL; live .env pins both | low | FIXED 287cd1d |
| SD-47 | review: verify | redeem-then-SetEmailVerified could spend the link unverified | Store.VerifyEmail one transaction; dead ConsumeAuthToken/SetEmailVerified removed | low | FIXED 287cd1d |
| SD-48 | review: fleetSkillWindow -1 | whole graded window loads every SWR rebuild; grows ~10k rows/day (1w) | accepted for correctness now; upgrade path = per-(symbol, settle day) collapse in SQL | low | NOT APPLICABLE (watch) |
| SD-49 | review: session trim | DELETE scans sessions (no user_id index) | table bounded by the cap itself (20/account) | low | NOT APPLICABLE |
| SD-50 | concurrency | another session ("Signal Deck readiness") committed 7d0c058 (long write-lock hold logging) on this branch at 21:13; my cross-session note expired unapproved | its files untouched; my commits stage explicit paths only | — | NOTED |

### Deferred with reason (not fixed this run)
- `ResolvedPredictionCount` (confidence gate, operator-only) is a raw COUNT(*) (208k) not the
  independent graded N (687); the 30-obs gate passes either way today. Fixing needs re-anchoring
  a pre-epoch fixture. Low.
- Main writer DSN without `_txlock=immediate` (read-then-write tx vs the new aw writer could
  SQLITE_BUSY_SNAPSHOT): 0 occurrences in logs; changes lock timing of every main-writer tx. Low.
- Sign-up timing distinguishes new vs taken email (sync insert vs async mail); verify link signs
  the clicker into the creator's account (login-CSRF shape). Both are design trade-offs. Low.
- ai-analyst sees ~50 of 329 symbols (alphabetical, 24k-char cap); cancelled runs filed `ok`;
  sentiment-tagger files ErrTransient as error; mcp audit write errors unrecorded; signal-report
  (operator-only) labels a whole-ledger hit rate "LIVE forward record". Low.

## Verification (final HEAD)
(filled in below by the final ladder run)
