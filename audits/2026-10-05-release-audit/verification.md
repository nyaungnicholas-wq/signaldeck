# Verification record: release audit 2026-10-05

## Environments

| Name | What | Identity |
|---|---|---|
| Production | `signaldeckd` on 127.0.0.1:8322, Next on 127.0.0.1:8323, Cloudflare quick tunnel `aim-logo-honest-hometown.trycloudflare.com` | Before: daemon 41a817a9, web build 6a1a327 (BUILD_ID Ixyi5rajLjTKFATt-Mhx2). After: see "Deployment" below. |
| Isolated copy | Daemon built from the repo on 127.0.0.1:18322 (image name `sdaudit*.exe`, so ctl's `taskkill /IM signaldeckd.exe` cannot hit it). It ran on a copy of `data/backups/signaldeck-20261003-200839.db`, rooted in a scratch directory (no `.env` reachable), with outbound HTTP pointed at a dead proxy and SMTP at a local capture sink. Next production build (`next build`, `next start`) on 127.0.0.1:13023 with the same local-proxy topology as production (`SIGNALDECK_LOCAL_ONLY_PROXY=1`, its own key). | Round 1: 1249c436. Round 2: the audit-1005 working tree. |
| Header probe | A throwaway quick tunnel to a header-echo server (no SignalDeck data), used once to observe what Cloudflare's edge sends an origin, then stopped. | n/a |

No active security test, failure injection or load was sent to production. Production saw ordinary browsing and one anonymous GET per route.

## Test suites (audit-1005 tree)

| Suite | Command | Result |
|---|---|---|
| Daemon, all packages | `go test ./... -count=1` | pass (no FAIL lines) |
| Daemon api + backup, after review fixes | `go test ./internal/api/ ./internal/backup/ -count=1` | ok (api 39.9 s, backup 8.5 s) |
| Lint | `golangci-lint run ./internal/api/... ./internal/backup/...` | 0 issues |
| Vet | `go vet ./internal/api/ ./internal/backup/` | clean |
| Web typecheck | `npx tsc --noEmit -p .` | exit 0 |
| Web lint (changed files) | `npx eslint <10 files>` | exit 0 |
| Web unit | `npm test` | 141 pass, 0 fail |
| Cleanup retention | `python -m unittest tools.test_cleanup_retention` | 4/4 OK |
| Banned-figure scan | `python tools/live_accuracy.py --scan web/src/app/accuracy/page.tsx web/src/app/page.tsx` | exit 0 |
| Controls table | `controls_evidence.py --inject STRATEGY_DECK.md` / `--write partials/controls_evidence.md` | no diff |
| Deploy gates | `bash ops/signaldeck-ctl.sh deploy` | see "Deployment" |

## Negative controls (each new test fails on the pre-fix code)

| Test | Mutation | Result on mutation |
|---|---|---|
| TestInterruptedBackupNeverLooksLikeAGeneration | original backup.go from HEAD | FAIL: verify ran on `signaldeck-….db`; unverified copy visible as a generation |
| tools/test_cleanup_retention.py | original cleanup.sh from HEAD | 3 of 4 FAIL (incident case reproduces the 10-04 log line for line) |
| TestPublishedSignupPausedWithoutBotCheck | `signupOpen` forced true | FAIL: 200 verify-sent |
| TestTunnelFailuresCannotLockTheConsole | `lockoutKey` returns the bare name | FAIL: console sign-in 429 |
| TestWindowLimiterSprayKeepsExhaustedBudgets | shedding disabled, old 50k drop restored | FAIL: exhausted budget reset |

## User journeys

Role, start state, steps, expected and actual. P = public tunnel, I = isolated copy.

| # | Env | Role | Journey | Expected | Actual | Disposition |
|---|---|---|---|---|---|---|
| J1 | P | anonymous | Landing → read record, receipts, constraints; console and network | Content hydrates; only the anonymous `/api/auth/me` 401 | As expected | PASS |
| J2 | P | anonymous | `/accuracy`, `/proof`, `/volatility`, `/glossary` | Render honest states | Render. `/accuracy` showed SD-30-withheld flagship figures (AUD-02) | FAIL → FIXED |
| J3 | P | anonymous | 375 px width on `/`, `/accuracy`, `/proof`, `/volatility` | No horizontal scroll | `scrollWidth == 375` on all four | PASS |
| J4 | P | anonymous | `/privacy`, `/terms`, `/about`, `/help` | Policy pages exist | 404 | FAIL (AUD-05, owner) |
| J5 | P | anonymous | Member and operator URLs (`/today`, `/watchlist`, `/journal`, `/dashboard`, `/lab/*`, `/s/stocks/AAPL`) | Sign-in required | Redirect to `/login` after hydration | PASS (deep link not restored, AUD-28) |
| J6 | I | new user | Sign up → mail captured → verify link → signed in | Account active, lands on `/today` | As expected; Gmail canonicalised; verify token single-use | PASS |
| J7 | I | member | Wrong password, then right password | Generic error, then session | As expected | PASS |
| J8 | I | member | Log out, reuse the old cookie | 401 | 401 | PASS |
| J9 | I | anonymous | Forgot (existing and unknown address) | Same answer and timing; mail only to the existing one | 200/200, 8.4 ms vs 8.8 ms; one reset mail | PASS |
| J10 | I | member | Reset with token, reuse token, old session | New password works; reuse 400; old session 401 | As expected | PASS |
| J11 | I | member | Watchlist add AAPL, reload, sign out and in | Persists | Persists; unwatch leaves the global symbol active | PASS |
| J12 | I | member B | Read the watchlist | Only B's (empty) | `[]` | PASS |
| J13 | I | member | Journal call MSFT up 1 week, double-click confirm, HTML in note | One call; note shown as text | One call (9 left, 1 open); 0 injected elements | PASS |
| J14 | I | member | Operator routes (`/api/notify-status`, `/api/workers`, `/api/admin/users`, CSV export) | 403 | 403 | PASS |
| J15 | I | attacker | POST without `X-Signaldeck`; cross-origin; `text/plain`; forged `x-signaldeck-local` | Refused | 403/403/403/401 | PASS |
| J16 | I | bot | 12 sign-ups from one client; honeypot filled | Limit, then silence | 3 × 200 then 429; honeypot 200 with no account and no mail | PASS |
| J17 | I + header probe | bot | Rotate `X-Forwarded-For` / `CF-Connecting-IP` | Limit holds in production | Bypasses on the isolated copy (no edge). Through Cloudflare the edge appends the real client as the last hop and rejects client CF-Connecting-IP (error 1000), and the daemon keys on the last hop | PASS in production; residual IPv6 /64 (AUD-36) |
| J18 | I | member | Unknown symbol `/s/stocks/ZZZZQ` | Says the symbol is not tracked | Before: "on our side" + Retry. After: "does not track this symbol", no Retry | FAIL → FIXED |
| J19 | I (round 2) | anonymous | `/signup` with no Turnstile keys | Paused notice, no form | "SIGN-UPS PAUSED" with links to the public record; API 503 with the reason; health `openSignup:false, signupPaused:true` | PASS |
| J20 | I (round 2) | member | Nav | Account reachable | ACCOUNT link present | PASS |
| J21 | I (round 2) | anonymous | `/accuracy`, `/`, `/proof` copy | Corrected text present, withheld figures absent | Rendered HTML checked string by string (see claims.md) | PASS |

## Round 2 and 3 (35012ab3, 952e6e85, 53afe5d5, 81c09af0)

| Check | Result |
|---|---|
| `go test ./internal/api/ -count=1` | ok (39–42 s) after each round |
| `golangci-lint run ./internal/api/...` | 0 issues |
| `npm test` (web) | 145 pass, 0 fail |
| `tsc --noEmit`, eslint on changed files | exit 0 |
| TestNoRawErrorsIn5xxBodies | FAILS when prereg.go is reverted (names prereg.go:29 and :34) |
| TestTrackRecordRegimesAndPaperAreOperatorOnly | FAILS when the public strip is reverted (regimes and paper both present) |
| TestAIErrorsAreNotEchoed | passes; the old aiErr returned err.Error() |
| `node --test src/lib/safenext.test.mjs` | 4/4, including dot-segment and backslash bypasses |
| Fresh-context review of 35012ab3 | 2 must-fix (e2e smoke and test-public-profile expected the bare /login redirect), 3 should-fix (safeNext dot segments, aiErr echo, Google ?next=). All fixed in 53afe5d5 |

| # | Env | Role | Journey | Result |
|---|---|---|---|---|
| J22 | I | new user | Open emailed verify link | Page shows "CONFIRM MY EMAIL". The account stays unverified (login 403) until the click; after the click the user is signed in on /today. PASS |
| J23 | I | member | Signed out, open /watchlist, sign in | Redirected to /login?next=%2Fwatchlist and resumed on /watchlist after sign-in. PASS |
| J24 | I | attacker | /login?next=%2F%2Fevil.example then sign in | Lands on /today (same origin). PASS |
| J25 | I | attacker | Failed sign-in typing an address | Log shows `username_digest=… username_len=17`, no address. PASS |
| J26 | I | anonymous | `curl -I /` | `Strict-Transport-Security: max-age=31536000`. PASS |
| J27 | I + P | anonymous | `/api/track-record?horizon=1d` | No `regimes`, no `paper` (isolated copy and production after deploy). PASS |
| J28 | I | member | /today | Paper P&L block absent, no error boundary, no failing requests. PASS |

## Round 4 (e35fe93d plus the proxy fix)

| Check | Result |
|---|---|
| `go test ./internal/api/ -count=1` | ok (43.7 s). One earlier run failed TestPersistedBody_ServedAcrossBuildsOfOneFormatOnly at its 30 s wait while a Next build and two isolated boots shared the CPU; it then passed 3/3 alone and in the full run. Recorded as load-sensitive, not fixed |
| store, pipeline, mcp packages; `go vet` | ok |
| `npm test` (web) | 146 pass. The new Content-Disposition test FAILS when the header is removed from the allowlist |
| `ops/daemon-guard.ps1 __selfcheck`; PowerShell 5.1 parse | SELFCHECK OK; 0 parse errors |
| `bash ops/test-publication-posture.sh` | 7 cases OK |

| # | Env | Role | Journey | Result |
|---|---|---|---|---|
| J29 | I | member | Account page | YOUR DATA panel renders under ALERTS. PASS |
| J30 | I | member | Download my data | 200, JSON with account, member_symbols, member_calls, member_alert_prefs, member_digest_tries; no hash, token or session text. Through the proxy the attachment header was missing; fixed (proxy allowlist) and re-checked: `attachment; filename="signaldeck-my-data.json"`. PASS |
| J31 | I | member | Delete with a wrong password | "that password is not right"; account intact. PASS |
| J32 | I | member | Delete with the password | Lands on /; /api/auth/me 401; sign-in with the same credentials 401; export 401; DB: user row and sessions gone, admin row intact. PASS |
| J33 | I | anonymous | `/api/accuracy` on the 10-03 backup | 503 REFUSED_STALE (last grade 26h49m old, max 26h): fails closed. The retired rows are checked in production after deploy |

## Round 5 (the fresh-context review of round 4)

The review (scratch report, not committed) found 2 must-fix and 4 should-fix. Each fix has a test that fails on the old code (mutation-checked).

| Review item | Fix | Proof |
|---|---|---|
| M1 delete fails under worker load (read-then-write on a deferred transaction: SQLITE_BUSY with no wait) | Write first; the delete also goes ahead of the fleet (Priority) | TestDeleteAccountWaitsForTheWriteLock: old order fails `database is locked (5)` |
| M2 deploy posture refusal came after the daemon was stopped | Check runs in build_from_head before the stop (deploy and launch) | Reviewed in ctl; self-test below |
| S1 guard counted HTTP 403/429 as hang strikes (PS 5.1 throws on non-2xx) | curl.exe status code; any HTTP status = alive | Selfcheck fails on the old 2xx-only rule (3 FAIL lines) |
| S2 a locked name blocked the member's own delete | Delete keeps a session-only ladder | Delete through an internet hop with the name locked: 200; old key: 429 |
| S3 device key replaced on a read error; created under a global lock on the busy writer | Read-only on error; insert-if-absent on the account writer; per-store memo | TestDeviceKeyIsReadNotReplaced fails on a global memo |
| S4 posture grep disagreed with the daemon's .env parser | The binary answers (`-publication-posture`); the guard asks it too | TestPublishedReadsDotEnvLikeTheDaemon (8 cases incl. quoted-empty, `1x`, later blank, tunnel host); shell self-test 6 cases, fails on an always-allow mutant; live: the new binary reads production as `published` |
| Notes | Runbook ngrok check; landing "over 0 forecasts"; raw read errors in /api/accuracy refusals; registry candidates reaching the live file from a package directory; MCP claim; returning-member sign-up mail; copilot quota on a reused id | TestAccuracyRefusalCarriesNoReadError (old code leaks a full path), TestSignupDoesNotRevealTakenUsernames (old order fails), TestRegistryCandidatesAnchorOnTheBinary, delete test checks the quota keys |

| Check | Result |
|---|---|
| Hour-sliced prune on a copy of the 10-03 backup (30-day window) | Backlog: 127,904 scores + 2,663 composite rows in 17.0 s + 3.1 s, **0** write-lock holds over 3 s (10-04 live: 125 holds of 3-48 s). Steady state: 3.0 s + 3.4 s against 3.5 s + 2.8 s for day slices |
| Load, isolated copy only (never the tunnel): web tier, 50 simulated clients, 20 concurrent, 60 s | 33,254 requests, 554/s, 0 errors, p95 83 ms, max 697 ms. /api/accuracy 503 throughout = the stale refusal on the old backup |
| Load, daemon directly, 50 clients, 20 concurrent, 30 s | 107,412 requests, 3,580/s, 0 errors; the rate limiter answered 85% with 429; served requests p95 under 65 ms; /api/health 200 in 15 ms afterwards |

The first load run opened a new connection per request and exhausted the machine's ephemeral ports (the daemon's own probe logged it); the tool now keeps connections alive and counts every status. That run is discarded.

## Round 6 (the fresh-context review of round 5)

The review confirmed the remaining round-4 items closed (it measured the delete waiting 1.84 s for the lock, and the guard's exit-3 refusal coming through the PowerShell pipeline). Two items were only partly closed and became this round's fixes: AUD-09 coverage (an overclaim) and a masking path in the guard's probe.

| Review item | Fix | Proof |
|---|---|---|
| M1 AUD-09 claimed every automatic start was checked; market-close, refresh, collect and restart-on-failure start the Daemon task directly | The daemon on the web tier's port (8322) refuses at startup (exit 3) when unpublished while cloudflared runs; copies on other ports start normally | TestUnpublishedDaemonRefusesBehindATunnel (6 cases). Real binary on this host with the tunnel up, configured for 8322 against a scratch root: exits 3 with the reason (live daemon unaffected). The e2e test's scratch daemon (another port) starts; it failed while the first draft refused on every port |
| S1 a 429 from the shared loopback bucket answered the guard's probe, so a wedged daemon under traffic was never restarted | A direct loopback GET of /api/health with no forwarded hop and no proxy key skips the limiter | TestLocalHealthProbeSkipsTheLimiter: with the bucket empty the direct probe gets 200, a proxy-keyed request still 429; the old rule fails it (429) |
| Notes | Priority released by defer; stale lockout comment; a concurrent second delete answers 401, not 500; device-key creation goes ahead of the fleet; curl `--noproxy '*'`; launch names the real refusal; a warning when no process tool exists; runbook names the 503 "webhook disabled" answer; AUD-22 states what the collapse-gate reason can carry | The guard edits: selfcheck OK, PS 5.1 parse 0 errors, its exact curl call returns 200 from the live daemon. The 401, the device-key priority and the defer were code-reviewed in round 6; round 7 adds tests for the delete paths |

## Round 7 (the fresh-context review of round 6)

No must-fix. Two should-fix and the notes, each with a test that fails on the old code:

| Review item | Fix | Proof |
|---|---|---|
| S1 the posture check ran only at start: a tunnel that came up after the daemon (logon order, tunnel restart, first go-live) was never caught | A watcher re-checks every minute and stops an unpublished daemon on 8322 gracefully with exit 3 | TestWatchPostureStopsWhenATunnelAppears (fails when the watcher does not stop) |
| S2 `tasklist` is a WMI client with no timeout on the startup path; "cannot tell" was silent | Toolhelp snapshot on Windows (no WMI, ms instead of 0.8 s), `pgrep` with a 10 s timeout elsewhere; "cannot tell" is logged | TestUnpublishedDaemonRefusesBehindATunnel requires the process list to be readable on the test host and the unknown case to be reported |
| Notes | Port compared as a number (`:08322`); the check runs before the database opens; delete matches the verified username as well as the id (a reused id is never erased); `prioritized` helper for both priority holds; the limiter comment says why web requests always carry the hop; runbook's Host-refusal answer is a 403 `forbidden host`; AUD-09 and AUD-13 list their residuals | Leading-zero case fails on a string compare; the store test fails (`delete under another name: <nil>`) without the username match; real binary on 8322: exit 3, no database or data directory created, live daemon unaffected |

## Not verified

- Email delivery through the production SMTP account: not sent (no mail to real people).
- Google sign-in: no client ID is configured anywhere.
- Turnstile with real keys: none exist; the server path is covered only by the stubbed-siteverify tests.
- Keyboard-only and screen-reader passes: only spot checks (skip link present, labelled form fields, focus moved to the error box on sign-up). No full assistive-technology run.
- Load against production: deliberately not run (the brief forbids stressing the public tunnel). The isolated copy was loaded (Round 5 above); production capacity behind Cloudflare is not measured.
- Restore drill: the weekly data-level rehearsal is recorded passing (latest 2026-10-04 07:18). This audit did not run the full drill. Commands are in the ops review.
- The production member view of `/today` (it needs a member account on production).
