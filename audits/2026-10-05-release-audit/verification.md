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

## Not verified

- Email delivery through the production SMTP account: not sent (no mail to real people).
- Google sign-in: no client ID is configured anywhere.
- Turnstile with real keys: none exist; the server path is covered only by the stubbed-siteverify tests.
- Keyboard-only and screen-reader passes: only spot checks (skip link present, labelled form fields, focus moved to the error box on sign-up). No full assistive-technology run.
- Load: no concurrency test was run against any environment this pass.
- Restore drill: the weekly data-level rehearsal is recorded passing (latest 2026-10-04 07:18). This audit did not run the full drill. Commands are in the ops review.
- The production member view of `/today` (it needs a member account on production).
