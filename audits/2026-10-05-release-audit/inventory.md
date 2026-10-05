# SignalDeck capability inventory (2026-10-05)

Inventoried the daemon (repo revision 1249c436, live daemon 41a817a9) and web (live build 6a1a327) by reading source, parsing route registrations, tracing page→endpoint imports, and sending one anonymous GET per route through 127.0.0.1:8323 (no POSTs on production). Verification results come from an isolated copy (daemon 127.0.0.1:18322 on a copy of the 2026-10-03 backup, web 127.0.0.1:13023, outbound network disabled, local mail sink) and the public Cloudflare quick tunnel.

## 1. Roles

| Role | Rule | Code |
|---|---|---|
| anonymous | `userID(r)==0`. No valid session cookie `signaldeck_session` and no bearer token matching `SIGNALDECK_API_TOKEN`. | auth.go:47-52, resolveUser auth.go:57-72 |
| published (deployment flag) | `PublicSurface \|\| PublicURL!="" \|\| TunnelLog!="" \|\| !ReachablePrivately()`. `ReachablePrivately` means loopback bind, no non-loopback host in `ALLOWED_HOSTS`, unless `SIGNALDECK_ASSUME_TUNNEL` overrides. **Live: true** (TUNNEL_LOG set). | accounts.go:207-214; config.go:506-545 |
| operator | `uid!=0 && (!published() \|\| isAdminUID(uid))`. On a private box every signed-in account is the operator. On a published box only the admin uid is. | isOperator accounts.go:220-226 |
| admin uid | `SELECT id FROM users WHERE is_admin=1 ORDER BY id LIMIT 1`. Only the **lowest** `is_admin` row counts. Cached 1 min; fails closed on DB error. The bearer API token resolves to this uid. | store/users.go:76-84; isAdminUID accounts.go:192-203; auth.go:63-70 |
| member | `uid!=0 && !isOperator(r)`. The gate refuses anything outside `memberMay` with 403 "not available to member accounts". | isMember accounts.go:97-99; gate security.go:134-137; memberMay/memberAllowed accounts.go:238-252 |
| member route set | alwaysOpen + **publicRoutes** + memberRoutes + `/api/evidence/*`. The FINRA pair also needs `SIGNALDECK_MEMBER_FINRA`. | publicRoutes security.go:267-327; memberRoutes accounts.go:60-90; memberFINRARoutes accounts.go:247 |

**Admin vs operator:** On this published deployment, admin and operator are the same account. `requireAdmin` (auth.go:430-446) is called **only by `/api/hud`** (api.go:1589). `askAccess` (ask.go:61-77) also uses `isAdminUID` rather than `isOperator`, so on a private box a non-admin account gets the member copilot tier. There is no admin console, user-management route, role-assignment route, or ban route. In `/api/auth/me`, `isAdmin` is the raw DB flag; the daemon's own verdict is the separate `member` field (auth.go:396).

**Member sees more than anonymous.** In the live posture, a member (any verified Gmail sign-up) reaches routes that anonymous visitors cannot (inventory_raw.md §3): `/api/version`, `/api/quality`, `/api/honesty`, `/api/calibration`, `/api/model-health`, `/api/canary`, `/api/postmortems`, `/api/regime-postmortems`, `/api/self-audit`, `/api/lineage`, `/api/dataset-versions`, `/api/evidence[/{id}]`, `/api/research-loop`, `/api/research-ledger`, `/api/ledger`, `/api/ledger/anchors`.

**Web side:** `hooks/useMe` `useIsMember()` drives `Shell.tsx:413-473` (member vs operator nav, members bounced from off-limits pages). `HubTabs.tsx:65-68` filters tabs. `AuthGate.tsx` gates every non-public page on `/api/auth/me`. Login, verify, and reset send members to `/today` and operators to `/dashboard` (login/page.tsx:59,81; verify/page.tsx:38; reset/page.tsx:42).

**Account bootstrap:** On a published box, sign-up refuses to create the first (admin) account: "create it on the server itself" (accounts.go:444-448, google.go:396-401). Only the private `registerPrivate` path makes the admin (accounts.go:528-556). Accounts made privately before publishing have no email, are exempt from email verification (auth.go:312-323), and become **members** once the box is published, unless they are the lowest `is_admin` row.

## 2. Pages

| route | audience | linked from | what it shows/fetches | anonymous result |
|---|---|---|---|---|
| `/` | public | logo (Shell.tsx:504) | Landing: accuracy summary, prereg chain, waitlist form. Fetches `/api/accuracy`, `/api/prereg`, `/api/waitlist`. | 200 (renders after hydration, no console errors except expected 401 on `/api/auth/me`; no horizontal overflow at 375px) |
| `/account` | member | **NOT LINKED ANYWHERE** (only in memberPages.ts:11) | Member settings: email digest toggle, Telegram link/unlink, logout. Fetches `/api/alert-prefs`, `/api/alert-prefs/telegram-link`, `/api/alert-prefs/telegram-unlink`, `/api/auth/logout`. | 200 (shell served, redirects to `/login` after hydration) |
| `/accuracy` | public | PublicNav, landing, member nav RECORD | Accuracy registry page. Fetches `/api/accuracy`. | 200 (renders after hydration, no console errors; no horizontal overflow at 375px) |
| `/advanced` | operator | operator nav (SIMPLE mode door), HelpPanel, CommandPalette | SIMPLE mode door page. No daemon fetches (static/server-only). | 200 (shell served, redirects to `/login` after hydration) |
| `/ask` | member (flag-gated) | operator nav; member nav only when copilot open | Copilot "Ask the data" page. Fetches `/api/ask`. | 200 (shell served, redirects to `/login` after hydration) |
| `/dashboard` | operator | operator nav HOME | Operator dashboard. Fetches `/api/alerts`, `/api/bars`, `/api/chart-overlays`, `/api/companies`, `/api/dashboard`, `/api/earnings-est`, `/api/movers`, `/api/predictions/latest`, `/api/regimes`, `/api/screener`, `/api/subscribe`, `/api/tape`, `/api/track-record`, `/api/unsubscribe`, `/api/vol-regime`. | 200 (shell served, redirects to `/login` after hydration) |
| `/forgot` | public | `/login` page | Forgot password form. Fetches `/api/auth/forgot`, `/api/health`. | 200 (renders after hydration) |
| `/glossary` | public | PublicNav | Glossary page. No daemon fetches (static/server-only). | 200 (renders after hydration) |
| `/health` | public | footer (Shell.tsx:660), HelpPanel, `/glossary` | Health page. No daemon fetches (static/server-only; reads ux-score.json and localStorage). | 200 (renders after hydration) |
| `/hud` | operator | CommandPalette ONLY (deliberately not in nav, Shell.tsx:87) | Admin HUD mirror. Fetches `/api/hud`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/companies` | operator | intel hub tab (intel/layout.tsx) | Companies intel. Fetches `/api/candidates/add`, `/api/companies`, `/api/dashboard`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/company` | operator | intel hub tab (intel/layout.tsx) | Company profile. Fetches `/api/company/profile`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/congress` | operator | (no static link found) | Congress trades. Fetches `/api/congress`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/filings` | operator | intel hub tab (intel/layout.tsx) | SEC filings feed. Fetches `/api/filings`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/insiders` | operator | intel hub tab (intel/layout.tsx) | Insider trades. Fetches `/api/insiders`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/institutions` | operator | intel hub tab (intel/layout.tsx) | Institutional holdings. Fetches `/api/institutions`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/news` | operator | intel hub tab (intel/layout.tsx); operator nav INTEL (PRO mode) | News feed. Fetches `/api/dashboard`, `/api/news`, `/api/screener`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/shorts` | operator | intel hub tab (intel/layout.tsx) | Short interest/volume. Fetches `/api/shorts`. | 200 (shell served, redirects to `/login` after hydration) |
| `/intel/smart-money` | operator | intel hub tab (intel/layout.tsx) | Smart money flows. Fetches `/api/smart-money`, `/api/smart-money/top`. | 200 (shell served, redirects to `/login` after hydration) |
| `/journal` | member | member nav MY CALLS | Member call journal. Fetches `/api/journal`, `/api/journal/symbols`, `/api/journal/withdraw`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/backtest` | operator | lab tab (lab/sections.ts) | Backtest lab. Fetches `/api/backtest`, `/api/dashboard`, `/api/screener`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/confluence` | operator | lab tab (lab/sections.ts) | Confluence lab. Fetches `/api/confluence`, `/api/confluence/top`, `/api/confluence/track`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/debate` | operator | lab tab (lab/sections.ts) | AI debate lab. Fetches `/api/ai/debate`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/desk` | operator | lab tab (lab/sections.ts) | Desk lab. Fetches `/api/recommendation`, `/api/recommendation/top`, `/api/world-model`, `/api/world-model/propagate`, `/api/world-model/shocks`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/evolution` | operator | lab tab (lab/sections.ts) | Model evolution lab. Fetches `/api/model-evolution`, `/api/self-audit`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/forecasts` | operator | lab tab (lab/sections.ts) | Forecasts lab. Fetches `/api/dashboard`, `/api/forecast`, `/api/model-forecasts`, `/api/scores/history`, `/api/screener`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/graph` | operator | lab tab (lab/sections.ts) | Knowledge graph lab. Fetches `/api/graph`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/honesty` | operator | lab tab (lab/sections.ts) | Honesty lab. Fetches `/api/export`, `/api/honesty`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/insights` | operator | lab tab (lab/sections.ts) | Insights lab. Fetches `/api/dashboard`, `/api/insights`, `/api/news-trends`, `/api/screener`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/live` | operator | lab tab (lab/sections.ts) | Live lab. Fetches `/api/agents`, `/api/datastats`, `/api/health`, `/api/tv-signals`, `/api/tv-status`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/memory` | operator | lab tab (lab/sections.ts) | Market memory lab. Fetches `/api/market-memory`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/optimizer` | operator | lab tab (lab/sections.ts) | Portfolio optimizer lab. Fetches `/api/portfolio/optimize`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/options` | operator | lab tab (lab/sections.ts) | Options lab. Fetches `/api/options/price`, `/api/options/vol-edge`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/pairs` | operator | lab tab (lab/sections.ts) | Pairs study lab. Fetches `/api/pairs-study`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/paper` | operator | lab tab (lab/sections.ts) | Paper trading lab. Fetches `/api/paper`, `/api/paper/order`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/portfolio` | operator | lab tab (lab/sections.ts) | Portfolio lab. Fetches `/api/correlation`, `/api/dashboard`, `/api/portfolio`, `/api/portfolio/add`, `/api/portfolio/close`, `/api/screener`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/research` | operator | lab tab (lab/sections.ts) | Research lab. Fetches `/api/research-graph`, `/api/research-ledger`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/risk` | operator | lab tab (lab/sections.ts) | Risk lab. Fetches `/api/dashboard`, `/api/risk`, `/api/screener`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/scenario` | operator | lab tab (lab/sections.ts) | Scenario lab. Fetches `/api/scenario`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/sentiment` | operator | lab tab (lab/sections.ts) | Sentiment lab. Fetches `/api/sentiment-correlation`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/signal-backtest` | operator | lab tab (lab/sections.ts) | Signal backtest lab. Fetches `/api/signal-backtest`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/strategies` | operator | lab tab (lab/sections.ts) | Strategies lab. Fetches `/api/dashboard`, `/api/screener`, `/api/strategy-lab`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/system` | operator | lab tab (lab/sections.ts) | System lab. Fetches `/api/agents`, `/api/ai/analyst`, `/api/ai/chat`, `/api/ai/filing`, `/api/ai/status`, `/api/datastats`, `/api/quality`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/system/agents` | operator | lab tab (lab/sections.ts) | Agents sub-page. Fetches `/api/agents`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/system/ai` | operator | lab tab (lab/sections.ts) | AI sub-page. Fetches `/api/ai/analyst`, `/api/ai/chat`, `/api/ai/filing`, `/api/ai/status`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/system/quality` | operator | lab tab (lab/sections.ts) | Quality sub-page. Fetches `/api/datastats`, `/api/quality`. | 200 (shell served, redirects to `/login` after hydration) |
| `/lab/track-record` | operator | lab tab (lab/sections.ts) | Track record lab. Fetches `/api/regime-postmortems`, `/api/regimes`, `/api/track-record`. | 200 (shell served, redirects to `/login` after hydration) |
| `/login` | public | PublicNav, landing, Shell sign-in chip | Login page. Fetches `/api/auth/google`, `/api/auth/login`, `/api/auth/register`, `/api/health`. | 200 (renders after hydration, no console errors except expected 401 on `/api/auth/me`) |
| `/market/activity` | operator | market hub tab (market/layout.tsx) | Alerts activity. Fetches `/api/alerts`, `/api/alerts/seen`, `/api/notify-status`. | 200 (shell served, redirects to `/login` after hydration) |
| `/market/breadth` | member | market hub tab (market/layout.tsx) | Market breadth. Fetches `/api/market-regimes`. | 200 (shell served, redirects to `/login` after hydration) |
| `/market/macro` | operator | market hub tab (market/layout.tsx) | Macro page. Fetches `/api/calendar`, `/api/earnings-est`, `/api/macro`, `/api/ranking`, `/api/regime`, `/api/sectors`. | 200 (shell served, redirects to `/login` after hydration) |
| `/market/overview` | operator | market hub tab (market/layout.tsx); operator nav MARKET | Market overview. Fetches `/api/candidates`, `/api/candidates/add`, `/api/candidates/dismiss`, `/api/candidates/monitor-all`, `/api/dashboard`, `/api/export/bars.csv`, `/api/export/scores.csv`, `/api/movers`, `/api/ranking`, `/api/regime`, `/api/screener`. | 200 (shell served, redirects to `/login` after hydration) |
| `/market/regimes` | member | market hub tab (market/layout.tsx); member nav REGIMES | Regimes page. Fetches `/api/regimes`, `/api/vol-regime`. Renders `/signals/report/...` links that bounce members (no member check in page.tsx:67,108,166). | 200 (shell served, redirects to `/login` after hydration) |
| `/market/signals` | operator | market hub tab (market/layout.tsx) | Signals page. Fetches `/api/alphax`, `/api/calibration`, `/api/composite`, `/api/composite/top`, `/api/dashboard`, `/api/screener`, `/api/tv-rating`. | 200 (shell served, redirects to `/login` after hydration) |
| `/market/trends` | operator | market hub tab (market/layout.tsx) | Trends page. Fetches `/api/candle-patterns`, `/api/trends`. | 200 (shell served, redirects to `/login` after hydration) |
| `/market/unusual` | operator | market hub tab (market/layout.tsx) | Unusual activity. Fetches `/api/anomalies`. | 200 (shell served, redirects to `/login` after hydration) |
| `/og-image` | public (route handler) | metadata image (layout.tsx / lib/site.ts) | OG image generator. No daemon fetches. | 200 |
| `/proof` | public | PublicNav, landing | Receipts/ledger verify page. Fetches `/api/health`, `/api/ledger/verify`, `/api/prereg`, `/api/track-record`. | 200 (renders after hydration, no console errors; no horizontal overflow at 375px) |
| `/reset` | public | emailed link only (accounts.go:707) | Password reset form. Fetches `/api/auth/reset`. | 200 (renders after hydration) |
| `/s/[market]/[symbol]` | member | dynamic links from watchlist, intel, movers, regimes, etc. | Symbol page. Fetches `/api/anomalies`, `/api/attribution`, `/api/bars`, `/api/breakouts`, `/api/candle-patterns`, `/api/chart-overlays`, `/api/company/profile`, `/api/congress`, `/api/dilution`, `/api/explain`, `/api/export`, `/api/filings`, `/api/fundamentals`, `/api/insiders`, `/api/institutions`, `/api/news`, `/api/predictions`, `/api/regimes`, `/api/short-interest`, `/api/shorts`, `/api/signal-report`, `/api/snaps`, `/api/stocktwits`, `/api/stream/snaps`, `/api/symbol`, `/api/symbol-agent`, `/api/trend`, `/api/unwatch`, `/api/vol-regime`, `/api/watch`, `/api/watchlist`, `/api/wiki-attention`. | 200 (shell served, redirects to `/login` after hydration) |
| `/signals/report/[market]/[symbol]` | operator | dynamic links from `/market/regimes`, `/s` page, report page | Signal report page. Fetches `/api/bars`, `/api/chart-overlays`, `/api/signal-report`. | 200 (shell served, redirects to `/login` after hydration) |
| `/signup` | public | PublicNav, landing CTA | Sign-up page. Fetches `/api/auth/google`, `/api/auth/register`, `/api/auth/resend`, `/api/health`. | 200 (renders after hydration, no console errors except expected 401 on `/api/auth/me`; no horizontal overflow at 375px) |
| `/today` | member | member nav TODAY; post-login landing for members | Today page. Fetches `/api/regimes`, `/api/track-record`, `/api/vol-forecast/latest`, `/api/vol-forecast/record`, `/api/vol-regime`, `/api/watchlist`. | 200 (shell served, redirects to `/login` after hydration) |
| `/verify` | public | emailed link only (accounts.go:565) | Email verification page. Fetches `/api/auth/verify`. | 200 (renders after hydration) |
| `/volatility` | public | PublicNav, landing | Volatility record page. Fetches `/api/vol-forecast/record`. | 200 (renders after hydration, no console errors; no horizontal overflow at 375px) |
| `/watchlist` | member + operator | operator + member nav | Watchlist page. Fetches `/api/companies`, `/api/dashboard`, `/api/regimes`, `/api/screener`, `/api/subscribe`, `/api/unsubscribe`, `/api/unwatch`, `/api/vol-regime`, `/api/watch`, `/api/watchlist`. | 200 (shell served, redirects to `/login` after hydration) |
| `/watchlist/compare` | operator | watchlist hub tab | Compare page. Fetches `/api/signal-report`. | 200 (shell served, redirects to `/login` after hydration) |
| `/welcome` | operator | HelpPanel, FirstRunTour, NextStep, empty states, CommandPalette | Welcome/onboarding. Fetches `/api/dashboard`, `/api/screener`, `/api/subscribe`. | 200 (shell served, redirects to `/login` after hydration) |

**Notes on public pages:** `/privacy`, `/terms`, `/about`, `/help` return 404 on the public tunnel (no policy pages exist). Verified via VERIFIED JOURNEYS.

## 3. Capability matrix

| capability | anonymous | member | operator | data accessed | action | current exposure | verification result |
|---|---|---|---|---|---|---|---|
| landing/live record (`/`, `/api/accuracy`, `/api/prereg`, `/api/waitlist`) | read | read | read | accuracy registry, prereg chain, waitlist emails | GET (accuracy may WRITE publication_verdicts on retirement transition accuracy.go:331) | public | PASS (public tunnel: renders, no console errors, no overflow at 375px) |
| accuracy registry (`/accuracy`, `/api/accuracy`) | read | read | read | accuracy registry | GET (may WRITE publication_verdicts) | public | PASS (public tunnel: renders, no console errors, no overflow at 375px) |
| receipts/ledger verify (`/proof`, `/api/ledger/verify`, `/api/ledger/range`, `/api/track-record`, `/api/prereg`) | read | read | read (+ ?full=1) | ledger chain, anchors, track record, prereg | GET (ledger/verify may append signed anchor on cadence ledger.go:434) | public | PASS (public tunnel: renders, no console errors, no overflow at 375px) |
| prereg chain (`/api/prereg`) | read | read | read | prereg chain (~260 KB) | GET | public | source + anonymous probe only |
| track record (`/api/track-record`, `/lab/track-record`, `/proof`) | read | read | read (+ operator-only detail sections trackrecord.go:105,126) | track record, regime postmortems | GET | public (anon), member/operator (detail) | source + anonymous probe only |
| volatility record (`/volatility`, `/api/vol-forecast/record`, `/today`) | read | read | read | volatility forecast record | GET | public | PASS (public tunnel: renders, no console errors, no overflow at 375px) |
| sign-up (`/signup`, `POST /api/auth/register`, `POST /api/auth/resend`) | write | write | write | users table, verification tokens, honeypot | POST (Gmail-only, email verify, honeypot, 5/h limit, no Turnstile) | public (OPEN_SIGNUP=true) | PASS (isolated: sign-up → confirmation mail captured locally → verify link → signed in on `/today`; Gmail canonicalised; verify token single-use PASS) |
| email verification (`/verify`, `POST /api/auth/verify`) | write | write | write | verification tokens, sessions | POST (redeems emailed token, signs in) | public | PASS (isolated: verify token single-use PASS; second use 400) |
| resend verification (`/signup`, `POST /api/auth/resend`) | write | write | write | verification tokens | POST (same answer whether or not account exists) | public | PASS (isolated: same answer and timing) |
| login (`/login`, `POST /api/auth/login`) | write | write | write | sessions, per-username lockout | POST (unverified email → 403) | public | PASS (isolated: wrong password → generic "invalid username or password"; correct → lands on `/today`; deep link not restored) |
| logout (`POST /api/auth/logout`) | write | write | write | sessions table | POST (deletes session row; 500 if delete fails auth.go:369) | public (requires session) | PASS (isolated: session invalidated server-side, old cookie 401; cookie HttpOnly, path /, 30-day expiry) |
| forgot/reset (`/forgot`, `/reset`, `POST /api/auth/forgot`, `POST /api/auth/reset`) | write | write | write | reset tokens, passwords, sessions | POST (forgot: same answer/timing for existing/unknown; mail only for existing; reset: token single-use, ends all prior sessions) | public | PASS (isolated: forgot same answer/timing; reset token single-use PASS; pre-reset sessions invalidated PASS; new password works PASS) |
| Google sign-in (`/login`, `/signup`, `POST /api/auth/google`) | write | write | write | Google OAuth, users table | POST | **404 live** (GOOGLE_CLIENT_ID unset; google.go:297) | not verified (disabled) |
| waitlist subscribe/unsubscribe (`/`, `POST /api/waitlist`) | write | write | write | waitlist emails | POST (anonymous write only, email only, CSRF header required) | public | source + anonymous probe only |
| watchlist add/remove (`POST /api/watch`, `POST /api/unwatch`, `GET /api/watchlist`) | none | read/write own | read/write own + global subscribe/unsubscribe | watchlist, companies, regimes, screener | POST/GET (member: own list only, crypto refused api.go:1720; operator: global feed management) | member+operator | PASS (isolated: add AAPL persists across reload and sign-out/sign-in; unwatch does not deactivate global symbol; second member sees empty watchlist) |
| journal calls (`GET/POST /api/journal`, `GET /api/journal/symbols`, `POST /api/journal/withdraw`) | none | read/write own | read/write own | journal entries, symbols | POST/GET (member call journal journal.go:128) | member+operator | PASS (isolated: double-click on confirm creates exactly one call; HTML/script payload in note renders as plain text) |
| member digest/alert prefs (`GET/POST /api/alert-prefs`, `POST /api/alert-prefs/telegram-link`, `POST /api/alert-prefs/telegram-unlink`) | none | read/write | read/write | alert prefs, telegram link | POST/GET (digest stored but **never sent** without PUBLIC_URL run.go:1861; telegram-link 503 live, bot token unset) | member+operator (digest inert; telegram 503) | source + anonymous probe only |
| account page (`/account`) | none | read/write | read/write | alert prefs, telegram, session | GET/POST | member+operator (page **unlinked** from UI) | not verified (unreachable via nav) |
| regimes/breadth (`/market/regimes`, `/market/breadth`, `/api/regimes`, `/api/vol-regime`, `/api/market-regimes`) | none | read (crypto dropped/sliced) | read (full) | structural regimes, vol regime, market regimes | GET | member+operator | source + anonymous probe only |

Note (2026-10-05): the account page row above predates the repair; branch audit-1005 adds an ACCOUNT link to the member nav.

## 4. All daemon routes

Copied from the route census (one anonymous GET per route through 127.0.0.1:8323; POST routes were not sent).

| # | Method | Path | Handler (file:line) | Live anon class | Anon probe via 8323 | Member | Anon if PUBLIC_SURFACE=1 | State change | Licence 451 | Web consumers | Notes |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | GET | `/api/accuracy` | daemon/internal/api/accuracy.go:145 (accuracy; reg accuracy.go:51) | anon | 200 | yes | yes | yes |  | /, /accuracy | GET may WRITE publication_verdicts on retirement transition (accuracy.go:331); 503 REFUSED/REFUSED_STALE fail-closed |
| 2 | GET | `/api/adaptive` | daemon/internal/api/adaptive.go:15 (adaptiveWeights; reg api.go:172) | session | 401 | no | no |  |  | **none** |  |
| 3 | GET | `/api/agents` | daemon/internal/api/api.go:1566 (agents; reg api.go:137) | session | 401 | no | no |  |  | /lab/live, /lab/system, /lab/system/agents |  |
| 4 | GET | `/api/ai/analyst` | daemon/internal/api/ai.go:43 (aiAnalyst; reg ai.go:142) | session | 401 | no | no | LLM spend |  | /lab/system, /lab/system/ai | GET that SPENDS LLM budget |
| 5 | POST | `/api/ai/chat` | daemon/internal/api/ai.go:56 (aiChat; reg ai.go:143) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/system, /lab/system/ai | LLM spend |
| 6 | POST | `/api/ai/debate` | daemon/internal/api/ai.go:97 (aiDebate; reg ai.go:145) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/debate | LLM spend |
| 7 | POST | `/api/ai/filing` | daemon/internal/api/ai.go:76 (aiFiling; reg ai.go:144) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/system, /lab/system/ai | LLM spend |
| 8 | GET | `/api/ai/status` | daemon/internal/api/ai.go:15 (aiStatus; reg ai.go:141) | session | 401 | no | no |  |  | /lab/system, /lab/system/ai | reports LLM budget; requiresAuth exempts it but PublicReads=false still closes it |
| 9 | GET | `/api/alert-outcomes` | daemon/internal/api/alerts.go:62 (alertOutcomes; reg alerts.go:46) | session | 401 | no | no |  |  | **none** | NOT under the /api/alerts prefix -> governed by PublicReads |
| 10 | GET | `/api/alert-prefs` | daemon/internal/api/alertprefs.go:64 (alertPrefsGet; reg alertprefs.go:178) | session | 401 | yes | no |  |  | /account | STATE(POST): email digest toggle; digest never SENT without SIGNALDECK_PUBLIC_URL (run.go:1861) |
| 11 | POST | `/api/alert-prefs` | daemon/internal/api/alertprefs.go:66 (alertPrefsSet; reg alertprefs.go:179) | session | (GET row only) | yes | no | yes |  | /account | STATE(POST): email digest toggle; digest never SENT without SIGNALDECK_PUBLIC_URL (run.go:1861) |
| 12 | POST | `/api/alert-prefs/telegram-link` | daemon/internal/api/alertprefs.go:91 (alertPrefsTelegramLink; reg alertprefs.go:180) | session | 401 (GET; gate refused) | yes | no | yes |  | /account | 503 unless SIGNALDECK_TELEGRAM_BOT_TOKEN (alertprefs.go:92) -- UNSET in prod |
| 13 | POST | `/api/alert-prefs/telegram-unlink` | daemon/internal/api/alertprefs.go:116 (alertPrefsTelegramUnlink; reg alertprefs.go:181) | session | 401 (GET; gate refused) | yes | no | yes |  | /account | STATE |
| 14 | GET | `/api/alerts` | daemon/internal/api/alerts.go:18 (alertsList; reg alerts.go:44) | session | 401 | no | no |  |  | (chrome), /dashboard, /market/activity | per-user list |
| 15 | POST | `/api/alerts/seen` | daemon/internal/api/alerts.go:34 (alertsSeen; reg alerts.go:45) | session | 401 (GET; gate refused) | no | no | yes |  | /market/activity | STATE: mark seen |
| 16 | GET | `/api/alerts/unsubscribe` | daemon/internal/api/alertprefs.go:139 (alertsUnsubscribeConfirm; reg alertprefs.go:182) | anon | 400 | yes | yes |  |  | **none** | GET = confirm page (no change); POST = token redeem, CSRF-exempt (security.go:333) |
| 17 | POST | `/api/alerts/unsubscribe` | daemon/internal/api/alertprefs.go:158 (alertsUnsubscribe; reg alertprefs.go:183) | anon | (GET row only) | yes | yes | yes |  | **none** | GET = confirm page (no change); POST = token redeem, CSRF-exempt (security.go:333) |
| 18 | GET | `/api/alphax` | daemon/internal/api/alphax.go:37 (alphaX; reg alphax.go:87) | session | 401 | no | no |  |  | /market/signals |  |
| 19 | GET | `/api/anomalies` | daemon/internal/api/anomalies.go:31 (anomaliesList; reg anomalies.go:80) | session | 401 | no | no |  |  | /market/unusual, /s/[market]/[symbol] |  |
| 20 | GET | `/api/ask` | daemon/internal/api/ask.go:88 (askStatus; reg ask.go:211) | session | 401 | flag:MEMBER_COPILOT | no |  |  | (chrome), /ask | GET = status only; POST spends LLM; 403 for members unless SIGNALDECK_MEMBER_COPILOT (ask.go:68) -- UNSET in prod; per-member daily cap |
| 21 | POST | `/api/ask` | daemon/internal/api/ask.go:105 (ask; reg ask.go:212) | session | (GET row only) | flag:MEMBER_COPILOT | no | yes |  | (chrome), /ask | GET = status only; POST spends LLM; 403 for members unless SIGNALDECK_MEMBER_COPILOT (ask.go:68) -- UNSET in prod; per-member daily cap |
| 22 | GET | `/api/attribution` | daemon/internal/api/attribution.go:173 (attribution; reg attribution.go:364) | session | 401 | no | no |  |  | /s/[market]/[symbol] |  |
| 23 | GET | `/api/attribution/prediction` | daemon/internal/api/predattribution.go:33 (predAttribution; reg predattribution.go:27) | session | 401 | no | no |  |  | **none** |  |
| 24 | POST | `/api/auth/forgot` | daemon/internal/api/accounts.go:677 (authForgot; reg accounts.go:771) | anon | 405 (GET; gate passed) | yes | yes | yes |  | /forgot | Turnstile if secret set; reset mail to verified OR unverified account (accounts.go:695) |
| 25 | POST | `/api/auth/google` | daemon/internal/api/google.go:296 (authGoogle; reg accounts.go:773) | anon | 405 (GET; gate passed) | yes | yes | yes |  | /login, /signup | 404 unless SIGNALDECK_GOOGLE_CLIENT_ID (google.go:297) -- UNSET in prod; sign-up path also OPEN_SIGNUP-gated (google.go:385) |
| 26 | POST | `/api/auth/login` | daemon/internal/api/auth.go:262 (authLogin; reg auth.go:402) | anon | 405 (GET; gate passed) | yes | yes | yes |  | /login | per-username lockout (auth.go:157); unverified email -> 403 (auth.go:317) |
| 27 | POST | `/api/auth/logout` | daemon/internal/api/auth.go:356 (authLogout; reg auth.go:403) | anon | 405 (GET; gate passed) | yes | yes | yes |  | (chrome), /account | deletes session row; 500 if delete fails (auth.go:369) |
| 28 | GET | `/api/auth/me` | daemon/internal/api/auth.go:380 (authMe; reg auth.go:404) | anon | 401 | yes | yes |  |  | (chrome), /account, /intel/companies, /intel/company +47 | 401 anon; returns member + memberFinra flags (auth.go:396) |
| 29 | POST | `/api/auth/register` | daemon/internal/api/accounts.go:404 (authRegister; reg auth.go:401) | anon | 405 (GET; gate passed) | yes | yes | yes |  | /login, /signup | OPEN_SIGNUP gate (accounts.go:405); published: Gmail-only, verify email, honeypot+5/h limiter, Turnstile if secret set; never creates admin (accounts.go:444) |
| 30 | POST | `/api/auth/resend` | daemon/internal/api/accounts.go:651 (authResend; reg accounts.go:770) | anon | 405 (GET; gate passed) | yes | yes | yes |  | /signup | same answer whether or not account exists |
| 31 | POST | `/api/auth/reset` | daemon/internal/api/accounts.go:720 (authReset; reg accounts.go:772) | anon | 405 (GET; gate passed) | yes | yes | yes |  | /reset | token + new password, ends all sessions |
| 32 | POST | `/api/auth/verify` | daemon/internal/api/accounts.go:602 (authVerify; reg accounts.go:769) | anon | 405 (GET; gate passed) | yes | yes | yes |  | /verify | redeems emailed token, signs in |
| 33 | POST | `/api/backtest` | daemon/internal/api/quant.go:63 (backtestRun; reg quant.go:399) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/backtest | compute only, no persistence |
| 34 | GET | `/api/bars` | daemon/internal/api/api.go:1057 (bars; reg api.go:128) | session | 401 | no | no |  | yes | (chrome), /dashboard, /s/[market]/[symbol], /signals/report/[market]/[symbol] | rawDataRefused (api.go:1089) |
| 35 | GET | `/api/breakouts` | daemon/internal/api/predict.go:189 (breakouts; reg predict.go:210) | session | 401 | no | no |  |  | /s/[market]/[symbol] |  |
| 36 | GET | `/api/calendar` | daemon/internal/api/signal8home.go:224 (calendar; reg signal8home.go:304) | session | 401 | no | no |  |  | /market/macro |  |
| 37 | GET | `/api/calibration` | daemon/internal/api/predict.go:201 ((inline); reg predict.go:201) | session | 401 | yes | yes |  |  | /market/signals |  |
| 38 | GET | `/api/canary` | daemon/internal/api/honestygaps.go:95 (canaryTrials; reg honestygaps.go:31) | session | 401 | yes | yes |  |  | **none** |  |
| 39 | GET | `/api/candidates` | daemon/internal/api/discovery.go:30 (candidatesList; reg discovery.go:276) | session | 401 | no | no |  |  | /market/overview |  |
| 40 | POST | `/api/candidates/add` | daemon/internal/api/discovery.go:103 (candidateAdd; reg discovery.go:277) | session | 401 (GET; gate refused) | no | no | yes |  | /intel/companies, /market/overview | STATE: registers symbol into polled universe + backfill (global) |
| 41 | POST | `/api/candidates/dismiss` | daemon/internal/api/discovery.go:262 (candidateDismiss; reg discovery.go:279) | session | 401 (GET; gate refused) | no | no | yes |  | /market/overview | STATE: candidate status |
| 42 | POST | `/api/candidates/monitor-all` | daemon/internal/api/discovery.go:185 (candidateMonitorAll; reg discovery.go:278) | session | 401 (GET; gate refused) | no | no | yes |  | /market/overview | STATE: promotes all candidates into universe (global) |
| 43 | GET | `/api/candle-patterns` | daemon/internal/api/candlepatterns.go:52 (candlePatterns; reg candlepatterns.go:119) | session | 401 | no | no |  |  | /market/trends, /s/[market]/[symbol] |  |
| 44 | GET | `/api/cboe-pc` | daemon/internal/api/dataexpansion.go:231 (cboePC; reg dataexpansion.go:294) | session | 401 | no | no |  |  | **none** |  |
| 45 | GET | `/api/chart-overlays` | daemon/internal/api/chartoverlays.go:42 (chartOverlays; reg chartoverlays.go:149) | session | 401 | no | no |  |  | /dashboard, /s/[market]/[symbol], /signals/report/[market]/[symbol] |  |
| 46 | GET | `/api/companies` | daemon/internal/api/companies.go:69 (companies; reg companies.go:338) | session | 401 | yes | no |  |  | (chrome), /dashboard, /intel/companies, /watchlist | member branch drops price/volume/mcap (companies.go:98) |
| 47 | GET | `/api/company/profile` | daemon/internal/api/capstones.go:579 (companyProfile; reg capstones.go:33) | session | 401 | yes | no | LLM spend |  | /intel/company, /s/[market]/[symbol] | operator ?summary=1 runs LLM (capstones.go:680); never for member |
| 48 | GET | `/api/composite` | daemon/internal/api/composite.go:126 (compositeDetail; reg composite.go:517) | session | 401 | no | no |  |  | /market/signals |  |
| 49 | GET | `/api/composite/top` | daemon/internal/api/composite.go:518 ((inline); reg composite.go:518) | session | 401 | no | no |  |  | /market/signals |  |
| 50 | GET | `/api/confidence` | daemon/internal/api/confidence.go:37 (confidenceRead; reg confidence.go:214) | session | 401 | no | no |  |  | **none** |  |
| 51 | GET | `/api/confluence` | daemon/internal/api/confluence.go:66 (confluence; reg confluence.go:471) | session | 401 | no | no |  |  | /lab/confluence |  |
| 52 | GET | `/api/confluence/top` | daemon/internal/api/confluence.go:129 (confluenceTop; reg confluence.go:472) | session | 401 | no | no |  |  | /lab/confluence |  |
| 53 | GET | `/api/confluence/track` | daemon/internal/api/confluence.go:170 (confluenceTrack; reg confluence.go:473) | session | 401 | no | no |  |  | /lab/confluence |  |
| 54 | GET | `/api/congress` | daemon/internal/api/congress.go:31 (congressTrades; reg congress.go:75) | session | 401 | yes | no |  |  | /intel/congress, /s/[market]/[symbol] |  |
| 55 | GET | `/api/correlation` | daemon/internal/api/quant.go:220 (correlation; reg quant.go:401) | session | 401 | no | no |  |  | /lab/portfolio |  |
| 56 | GET | `/api/cot` | daemon/internal/api/dataexpansion.go:109 (cot; reg dataexpansion.go:291) | session | 401 | no | no |  |  | **none** |  |
| 57 | GET | `/api/crypto-perp` | daemon/internal/api/dataexpansion.go:73 (cryptoPerp; reg dataexpansion.go:290) | session | 401 | no | no |  | yes | **none** |  |
| 58 | GET | `/api/dashboard` | daemon/internal/api/dashboard.go:253 (dashboardHandler; reg dashboard.go:248) | session | 401 | no | no |  |  | /dashboard, /intel/companies, /intel/news, /lab/backtest +9 |  |
| 59 | GET | `/api/dataset-versions` | daemon/internal/api/honestygaps.go:143 (datasetVersions; reg honestygaps.go:32) | session | 401 | yes | yes |  |  | **none** |  |
| 60 | GET | `/api/datastats` | daemon/internal/api/api.go:166 ((inline); reg api.go:166) | session | 401 | no | no |  |  | /lab/live, /lab/system, /lab/system/quality | slow (>30s measured), SWR cached |
| 61 | GET | `/api/digest` | daemon/internal/api/digest.go:18 (digest; reg digest.go:49) | session | 401 | no | no |  |  | **none** |  |
| 62 | GET | `/api/dilution` | daemon/internal/api/signal8.go:241 (dilution; reg signal8.go:295) | session | 401 | yes | no |  |  | /s/[market]/[symbol] |  |
| 63 | GET | `/api/earnings-est` | daemon/internal/api/companies.go:279 (earningsEstimates; reg companies.go:339) | session | 401 | no | no |  |  | /dashboard, /market/macro |  |
| 64 | GET | `/api/earnings-window` | daemon/internal/api/earningswindow.go:41 (earningsWindow; reg earningswindow.go:111) | session | 401 | no | no |  |  | **none** |  |
| 65 | GET | `/api/ev/decisions` | daemon/internal/api/evdecisions.go:29 (evDecisions; reg evdecisions.go:19) | session | 401 | no | no |  |  | **none** |  |
| 66 | GET | `/api/evidence` | daemon/internal/api/evidence.go:33 (evidenceList; reg evidence.go:22) | session | 401 | yes | yes |  |  | **none** |  |
| 67 | GET | `/api/evidence/{id}` | daemon/internal/api/evidence.go:53 (evidenceOne; reg evidence.go:23) | session | 401 | yes | yes |  |  | **none** | prefix-matched public route (security.go:372) |
| 68 | GET | `/api/explain` | daemon/internal/api/explain.go:26 (explain; reg explain.go:110) | session | 401 | no | no |  |  | /s/[market]/[symbol] |  |
| 69 | GET | `/api/export/bars.csv` | daemon/internal/api/api.go:1794 (exportBars; reg api.go:161) | session | skipped | no | no |  | yes | /market/overview | bulk CSV export (not probed); rawDataRefused (api.go:1800) |
| 70 | GET | `/api/export/outcomes.csv` | daemon/internal/api/api.go:1851 (exportOutcomes; reg api.go:163) | session | skipped | no | no |  | yes | **none** | bulk CSV export (not probed); no web consumer |
| 71 | GET | `/api/export/scores.csv` | daemon/internal/api/api.go:1823 (exportScores; reg api.go:162) | session | skipped | no | no |  | yes | /market/overview | bulk CSV export (not probed); rawDataRefused |
| 72 | GET | `/api/feature-health` | daemon/internal/api/featurehealth.go:28 (featureHealth; reg featurehealth.go:87) | session | 401 | no | no |  |  | **none** |  |
| 73 | GET | `/api/feature-redundancy` | daemon/internal/api/honestygaps.go:73 (featureRedundancy; reg honestygaps.go:30) | session | 401 | no | no |  |  | **none** |  |
| 74 | GET | `/api/filings` | daemon/internal/api/signal8.go:87 (filingsFeed; reg signal8.go:292) | session | 401 | yes | no |  |  | /intel/filings, /s/[market]/[symbol] |  |
| 75 | GET | `/api/fleet-health` | daemon/internal/api/fleethealth.go:56 (fleetHealth; reg fleethealth.go:479) | session | 401 | no | no |  |  | **none** |  |
| 76 | GET | `/api/forecast` | daemon/internal/api/quant.go:47 (forecast; reg quant.go:398) | session | 401 | no | no |  |  | /lab/forecasts |  |
| 77 | GET | `/api/fundamentals` | daemon/internal/api/freedata.go:67 (fundamentals; reg freedata.go:111) | session | 401 | yes | no |  |  | /s/[market]/[symbol] |  |
| 78 | GET | `/api/graph` | daemon/internal/api/capstones.go:470 (knowledgeGraph; reg capstones.go:31) | session | 401 | no | no |  |  | /lab/graph |  |
| 79 | GET | `/api/health` | daemon/internal/api/api.go:548 (health; reg api.go:114) | anon | 200 | yes | yes |  |  | (chrome), /forgot, /lab/live, /login +2 | anon gets summary only {degraded,openSignup,turnstileSiteKey,googleClientId} (api.go:594); operator gets revision+workers |
| 80 | GET | `/api/honesty` | daemon/internal/api/honestycache.go:140 ((inline); reg honestycache.go:140) | session | 401 | yes | yes |  |  | /lab/honesty |  |
| 81 | GET | `/api/hud` | daemon/internal/api/api.go:1588 (hud; reg api.go:138) | session | 401 | no | no |  |  | /hud | requireAdmin (api.go:1589): personal Alpaca PUSH-20 HUD mirrored from trader-hud :8787; the ONLY admin-vs-operator distinction |
| 82 | GET | `/api/insiders` | daemon/internal/api/signal8.go:114 (insiders; reg signal8.go:293) | session | 401 | yes | no |  |  | /intel/insiders, /s/[market]/[symbol] |  |
| 83 | GET | `/api/insights` | daemon/internal/api/api.go:1606 (insights; reg api.go:139) | session | 401 | no | no |  |  | /lab/insights |  |
| 84 | GET | `/api/institutions` | daemon/internal/api/signal8.go:154 (institutions; reg signal8.go:294) | session | 401 | yes | no |  |  | /intel/institutions, /s/[market]/[symbol] |  |
| 85 | GET | `/api/journal` | daemon/internal/api/journal.go:82 (journalGet; reg journal.go:206) | session | 401 | yes | no |  |  | /journal | STATE(POST): member call journal (journal.go:128) |
| 86 | POST | `/api/journal` | daemon/internal/api/journal.go:84 (journalCreate; reg journal.go:208) | session | (GET row only) | yes | no | yes |  | /journal | STATE(POST): member call journal (journal.go:128) |
| 87 | GET | `/api/journal/symbols` | daemon/internal/api/journal.go:188 (journalSymbols; reg journal.go:207) | session | 401 | yes | no |  |  | /journal |  |
| 88 | POST | `/api/journal/withdraw` | daemon/internal/api/journal.go:146 (journalWithdraw; reg journal.go:209) | session | 401 (GET; gate refused) | yes | no | yes |  | /journal | STATE |
| 89 | GET | `/api/ledger` | daemon/internal/api/ledger.go:778 (ledger; reg ledger.go:880) | session | 401 | yes | yes |  |  | **none** |  |
| 90 | GET | `/api/ledger/anchors` | daemon/internal/api/ledger.go:725 (ledgerAnchors; reg ledger.go:878) | session | 401 | yes | yes |  |  | **none** | ?recompute=1 operator only (ledger.go:739) |
| 91 | GET | `/api/ledger/range` | daemon/internal/api/ledger.go:836 (ledgerRange; reg ledger.go:879) | anon | 200 | yes | yes |  |  | **none** | paged chain, limit<=5000 (ledger.go:94) |
| 92 | GET | `/api/ledger/verify` | daemon/internal/api/ledger.go:616 (ledgerVerify; reg ledger.go:877) | anon | 200 | yes | yes | yes |  | /proof | public read with bounded WRITE: may append signed anchor on cadence (ledger.go:434); ?full=1 operator only (ledger.go:621) |
| 93 | GET | `/api/lineage` | daemon/internal/api/lineage.go:25 (lineageTrace; reg lineage.go:17) | session | 401 | yes | yes |  |  | **none** |  |
| 94 | GET | `/api/macro` | daemon/internal/api/data.go:193 ((inline); reg data.go:193) | session | 401 | no | no |  |  | /market/macro |  |
| 95 | GET | `/api/macro-series` | daemon/internal/api/freedata.go:20 (macroSeries; reg freedata.go:110) | session | 401 | no | no |  |  | **none** |  |
| 96 | GET | `/api/market-memory` | daemon/internal/api/capstones.go:847 (marketMemory; reg capstones.go:32) | session | 401 | no | no |  |  | /lab/memory |  |
| 97 | GET | `/api/market-regimes` | daemon/internal/api/marketregimes.go:48 (marketRegimes; reg marketregimes.go:182) | session | 401 | yes | no |  |  | /market/breadth |  |
| 98 | GET | `/api/metalabel` | daemon/internal/api/metalabel.go:27 (metaLabel; reg metalabel.go:22) | session | 401 | no | no |  |  | **none** |  |
| 99 | GET | `/api/model-evolution` | daemon/internal/api/modelevolution.go:22 (modelEvolution; reg modelevolution.go:45) | session | 401 | no | no |  |  | /lab/evolution |  |
| 100 | GET | `/api/model-forecasts` | daemon/internal/api/quant.go:414 (modelForecasts; reg quant.go:406) | session | 401 | no | no |  |  | /lab/forecasts |  |
| 101 | GET | `/api/model-health` | daemon/internal/api/modelhealth.go:30 (modelHealth; reg modelhealth.go:158) | session | 401 | yes | yes |  |  | **none** |  |
| 102 | GET | `/api/movers` | daemon/internal/api/signal8home.go:301 ((inline); reg signal8home.go:301) | session | 401 | no | no |  |  | /dashboard, /market/overview |  |
| 103 | GET | `/api/news` | daemon/internal/api/data.go:15 (news; reg data.go:190) | session | 401 | no | no |  | yes | /intel/news, /s/[market]/[symbol] |  |
| 104 | GET | `/api/news-trends` | daemon/internal/api/newstrends.go:24 (newsTrends; reg newstrends.go:64) | session | 401 | no | no |  |  | /lab/insights |  |
| 105 | GET | `/api/notify-status` | daemon/internal/api/notifystatus.go:67 (notifyStatus; reg notifystatus.go:139) | session | 401 | no | no |  |  | /market/activity |  |
| 106 | POST | `/api/notify/test` | daemon/internal/api/notifystatus.go:98 (notifyTest; reg notifystatus.go:140) | session | 401 (GET; gate refused) | no | no | yes |  | **none** | SIDE EFFECT: sends test message to configured Discord/Slack/Telegram/SMTP (security.go:510) |
| 107 | GET | `/api/options/price` | daemon/internal/api/options.go:61 (optionsPrice; reg options.go:39) | session | 401 | no | no |  |  | /lab/options |  |
| 108 | GET | `/api/options/vol-edge` | daemon/internal/api/options.go:118 (optionsVolEdge; reg options.go:40) | session | 401 | no | no |  |  | /lab/options |  |
| 109 | GET | `/api/pairs-study` | daemon/internal/api/pairsstudy.go:23 (pairsStudy; reg pairsstudy.go:51) | session | 401 | no | no |  |  | /lab/pairs |  |
| 110 | GET | `/api/paper` | daemon/internal/api/paper.go:306 ((inline); reg paper.go:306) | session | 401 | no | no |  |  | /lab/paper | ?strategy=manual per-user (paper.go:47) |
| 111 | POST | `/api/paper/order` | daemon/internal/api/paperorder.go:47 (paperOrder; reg api.go:129) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/paper | STATE: per-user simulated manual book (paperorder.go:47) |
| 112 | GET | `/api/portfolio` | daemon/internal/api/quant.go:271 (portfolioGet; reg quant.go:402) | session | 401 | no | no |  |  | /lab/portfolio |  |
| 113 | POST | `/api/portfolio/add` | daemon/internal/api/quant.go:324 (portfolioAdd; reg quant.go:403) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/portfolio | STATE: per-user simulated position (quant.go:353) |
| 114 | POST | `/api/portfolio/close` | daemon/internal/api/quant.go:364 (portfolioClose; reg quant.go:404) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/portfolio | STATE: per-user (quant.go:384) |
| 115 | GET | `/api/portfolio/optimize` | daemon/internal/api/capstones.go:332 (portfolioOptimize; reg capstones.go:30) | session | 401 | no | no |  |  | /lab/optimizer |  |
| 116 | GET | `/api/portfolio/rebalance` | daemon/internal/api/capstones.go:44 (portfolioRebalance; reg capstones.go:34) | session | 401 | no | no |  |  | **none** |  |
| 117 | GET | `/api/postmortems` | daemon/internal/api/postmortem.go:22 (postmortems; reg api.go:173) | session | 401 | yes | yes |  |  | **none** | operator-only field (postmortem.go:78) |
| 118 | GET | `/api/predictions` | daemon/internal/api/predict.go:14 (predictions; reg predict.go:200) | session | 401 | no | no |  |  | /s/[market]/[symbol] |  |
| 119 | GET | `/api/predictions/latest` | daemon/internal/api/stage5.go:28 (predictionsLatestCached; reg stage5.go:123) | session | 401 | no | no |  |  | /dashboard |  |
| 120 | GET | `/api/prereg` | daemon/internal/api/prereg.go:24 (prereg; reg prereg.go:144) | anon | 200 | yes | yes |  |  | /, /proof | ~260 KB anonymous body (measured) |
| 121 | GET | `/api/price-validation` | daemon/internal/api/honestygaps.go:165 (priceValidation; reg honestygaps.go:33) | session | 401 | no | no |  |  | **none** |  |
| 122 | GET | `/api/quality` | daemon/internal/api/api.go:1441 (quality; reg api.go:136) | session | 401 | yes | yes |  |  | /lab/system, /lab/system/quality | raw DQ error text operator-only (api.go:1473) |
| 123 | GET | `/api/ranking` | daemon/internal/api/predict.go:179 (ranking; reg predict.go:209) | session | 401 | no | no |  |  | /market/macro, /market/overview |  |
| 124 | GET | `/api/ready` | daemon/internal/api/api.go:733 (ready; reg api.go:115) | anon | 200 | yes | yes |  |  | **none** | anon summary only; operator detail (api.go:817,828) |
| 125 | GET | `/api/recommendation` | daemon/internal/api/desk.go:101 (deskRecommendation; reg desk.go:399) | session | 401 | no | no | yes |  | /lab/desk | GET APPENDS hash-chained audit row (desk.go:295) |
| 126 | GET | `/api/recommendation/top` | daemon/internal/api/desk.go:320 (deskTop; reg desk.go:400) | session | 401 | no | no |  |  | /lab/desk |  |
| 127 | GET | `/api/regime` | daemon/internal/api/predict.go:164 (regimes; reg predict.go:208) | session | 401 | no | no |  |  | /market/macro, /market/overview |  |
| 128 | GET | `/api/regime-conditioned` | daemon/internal/api/data.go:56 (regimeConditioned; reg data.go:192) | session | 401 | no | no |  |  | **none** |  |
| 129 | GET | `/api/regime-postmortems` | daemon/internal/api/regimepostmortems.go:14 (regimePostmortems; reg regimepostmortems.go:29) | session | 401 | yes | yes |  |  | /lab/track-record |  |
| 130 | GET | `/api/regimes` | daemon/internal/api/regimes.go:51 (structuralRegimesCached; reg api.go:190) | session | 401 | yes | no |  |  | /dashboard, /lab/track-record, /market/regimes, /s/[market]/[symbol] +2 | member: crypto refused, sliced (regimes.go:57) |
| 131 | GET | `/api/research` | daemon/internal/api/researchlab.go:16 (research; reg api.go:174) | session | 401 | no | no |  |  | **none** |  |
| 132 | GET | `/api/research-graph` | daemon/internal/api/researchgraph.go:13 (researchGraph; reg api.go:250) | session | 401 | no | no |  |  | /lab/research |  |
| 133 | GET | `/api/research-ledger` | daemon/internal/api/api.go:185 ((inline); reg api.go:185) | session | 401 | yes | yes |  |  | /lab/research | slow, SWR cached |
| 134 | GET | `/api/research-loop` | daemon/internal/api/researchloop.go:28 (researchLoop; reg api.go:188) | session | 401 | yes | yes |  |  | **none** |  |
| 135 | GET | `/api/return-forecast` | daemon/internal/api/honestygaps.go:38 (returnForecast; reg honestygaps.go:29) | session | 401 | no | no |  |  | **none** |  |
| 136 | POST | `/api/risk` | daemon/internal/api/quant.go:140 (risk; reg quant.go:400) | session | 401 (GET; gate refused) | no | no | yes |  | /lab/risk | compute only |
| 137 | GET | `/api/scenario` | daemon/internal/api/capstones.go:228 (scenario; reg capstones.go:29) | session | 401 | no | no |  |  | /lab/scenario |  |
| 138 | GET | `/api/scores/history` | daemon/internal/api/api.go:1143 (scoreHistory; reg api.go:130) | session | 401 | no | no |  |  | /lab/forecasts |  |
| 139 | GET | `/api/screener` | daemon/internal/api/api.go:131 ((inline); reg api.go:131) | session | 401 | no | no |  |  | (chrome), /dashboard, /intel/news, /lab/backtest +9 | SWR cached |
| 140 | GET | `/api/sectors` | daemon/internal/api/data.go:39 (sectors; reg data.go:191) | session | 401 | no | no |  |  | /market/macro |  |
| 141 | GET | `/api/self-audit` | daemon/internal/api/selfaudit.go:22 (selfAudit; reg selfaudit.go:58) | session | 401 | yes | yes |  |  | /lab/evolution |  |
| 142 | GET | `/api/sentiment-correlation` | daemon/internal/api/sentcorr.go:33 (sentimentCorrelation; reg sentcorr.go:188) | session | 401 | no | no |  |  | /lab/sentiment |  |
| 143 | GET | `/api/short-interest` | daemon/internal/api/dataexpansion.go:36 (shortInterest; reg dataexpansion.go:289) | session | 401 | flag:MEMBER_FINRA | no |  |  | /s/[market]/[symbol] |  |
| 144 | GET | `/api/shorts` | daemon/internal/api/shorts.go:69 (shorts; reg shorts.go:161) | session | 401 | flag:MEMBER_FINRA | no |  |  | /intel/shorts, /s/[market]/[symbol] |  |
| 145 | GET | `/api/signal-backtest` | daemon/internal/api/signalbt.go:56 (signalBacktest; reg signalbt.go:319) | session | 401 | no | no |  |  | /lab/signal-backtest |  |
| 146 | GET | `/api/signal-report` | daemon/internal/api/signalreport.go:26 (signalReport; reg api.go:191) | session | 401 | no | no |  |  | (chrome), /s/[market]/[symbol], /signals/report/[market]/[symbol], /watchlist/compare |  |
| 147 | GET | `/api/smart-money` | daemon/internal/api/smartmoney.go:46 (smartMoney; reg smartmoney.go:170) | session | 401 | no | no |  |  | /intel/smart-money |  |
| 148 | GET | `/api/smart-money/top` | daemon/internal/api/smartmoney.go:115 (smartMoneyTop; reg smartmoney.go:171) | session | 401 | no | no |  |  | /intel/smart-money |  |
| 149 | GET | `/api/snaps` | daemon/internal/api/api.go:1174 (snaps; reg api.go:140) | session | 401 | no | no |  | yes | /s/[market]/[symbol] |  |
| 150 | GET | `/api/source-health` | daemon/internal/api/sourcehealth.go:23 (sourceHealth; reg api.go:233) | session | 401 | no | no |  |  | **none** |  |
| 151 | GET | `/api/stocktwits` | daemon/internal/api/dataexpansion.go:143 (stocktwitsSentiment; reg dataexpansion.go:292) | session | 401 | no | no |  | yes | /s/[market]/[symbol] |  |
| 152 | GET | `/api/strategy-lab` | daemon/internal/api/stratlab.go:24 (strategyLab; reg stratlab.go:56) | session | 401 | no | no |  |  | /lab/strategies |  |
| 153 | GET | `/api/stream/snaps` | daemon/internal/api/stream.go:47 (streamSnaps; reg stream.go:18) | session | skipped | no | no |  | yes | /s/[market]/[symbol] | SSE (not probed) |
| 154 | POST | `/api/stress/run` | daemon/internal/api/stress.go:76 (stressRun; reg stress.go:64) | session | 401 (GET; gate refused) | no | no | yes |  | **none** | compute (capped), userID required (stress.go:77) |
| 155 | GET | `/api/stress/scenarios` | daemon/internal/api/stress.go:67 (stressScenarios; reg stress.go:63) | session | 401 | no | no |  |  | **none** |  |
| 156 | POST | `/api/subscribe` | daemon/internal/api/api.go:1640 (subscribe; reg api.go:141) | session | 401 (GET; gate refused) | no | no | yes |  | /dashboard, /watchlist, /welcome | STATE: adds symbol to STREAMED hot set + backfill (global ingestion) |
| 157 | GET | `/api/symbol` | daemon/internal/api/api.go:125 ((inline); reg api.go:125) | session | 401 | no | no |  |  | /s/[market]/[symbol] | SWR cached |
| 158 | GET | `/api/symbol-agent` | daemon/internal/api/symbolagent.go:44 (symbolAgent; reg api.go:193) | session | 401 | yes | no |  |  | /s/[market]/[symbol] | member crypto refused (symbolagent.go:116) |
| 159 | GET | `/api/tape` | daemon/internal/api/signal8home.go:80 (tape; reg signal8home.go:300) | session | 401 | no | no |  |  | /dashboard |  |
| 160 | GET | `/api/track-record` | daemon/internal/api/trackrecord.go:116 (trackRecordCached; reg trackrecord.go:855) | anon | 200 | yes | yes |  |  | /dashboard, /lab/track-record, /proof, /today | operator-only detail sections (trackrecord.go:105,126) |
| 161 | GET | `/api/trend` | daemon/internal/api/trendapi.go:29 (trendRead; reg trendapi.go:80) | session | 401 | no | no |  |  | /s/[market]/[symbol] |  |
| 162 | GET | `/api/trends` | daemon/internal/api/api.go:1192 (trends; reg api.go:134) | session | 401 | no | no |  |  | /market/trends |  |
| 163 | GET | `/api/tv-quote` | daemon/internal/api/dataexpansion.go:260 (tvQuote; reg dataexpansion.go:295) | session | 401 | no | no |  | yes | **none** |  |
| 164 | GET | `/api/tv-rating` | daemon/internal/api/tvrating.go:20 (tvRating; reg tvrating.go:55) | session | 401 | no | no |  | yes | /market/signals |  |
| 165 | GET | `/api/tv-signals` | daemon/internal/api/tvwebhook.go:155 (tvSignalsList; reg tvwebhook.go:171) | session | 401 | no | no |  | yes | /lab/live |  |
| 166 | GET | `/api/tv-status` | daemon/internal/api/tvstatus.go:57 (tvStatus; reg tvstatus.go:219) | session | 401 | no | no |  |  | /lab/live |  |
| 167 | POST | `/api/tv-webhook` | daemon/internal/api/tvwebhook.go:62 (tvWebhook; reg tvwebhook.go:170) | anon | 405 (GET; gate passed) | yes | yes | yes |  | **none** | STATE: inserts tv_signals (tvwebhook.go:131); shared secret in body (tvwebhook.go:62); CSRF-exempt |
| 168 | GET | `/api/universe` | daemon/internal/api/universe.go:16 (universe; reg api.go:192) | session | 401 | no | no |  |  | **none** |  |
| 169 | POST | `/api/unsubscribe` | daemon/internal/api/api.go:1671 (unsubscribe; reg api.go:153) | session | 401 (GET; gate refused) | no | no | yes |  | /dashboard, /watchlist | STATE: removes from user list; deactivates feed when last watcher leaves |
| 170 | POST | `/api/unwatch` | daemon/internal/api/api.go:1738 (unwatch; reg api.go:155) | session | 401 (GET; gate refused) | yes | no | yes |  | /s/[market]/[symbol], /watchlist | STATE: own watchlist only |
| 171 | GET | `/api/version` | daemon/internal/api/version.go:29 (version; reg api.go:116) | session | 401 | yes | yes |  |  | **none** | build revision -- in publicRoutes, so members (and anon under PUBLIC_SURFACE) get the revision /api/health withholds |
| 172 | GET | `/api/vol-forecast/latest` | daemon/internal/api/volforecast.go:97 (serveVolLatest; reg api.go:152) | session | 401 | yes | no |  |  | /today |  |
| 173 | GET | `/api/vol-forecast/record` | daemon/internal/api/volforecast.go:75 (serveVolRecord; reg api.go:151) | anon | 200 | yes | yes |  |  | /today, /volatility |  |
| 174 | GET | `/api/vol-regime` | daemon/internal/api/volregime.go:14 (volRegime; reg api.go:189) | session | 401 | yes | no |  |  | /dashboard, /market/regimes, /s/[market]/[symbol], /today +1 | member: crypto dropped (volregime.go:20) |
| 175 | POST | `/api/waitlist` | daemon/internal/api/waitlist.go:73 (waitlistAdd; reg api.go:144) | anon | 405 (GET; gate passed) | yes | yes | yes |  | / | STATE: only anonymous write (email only), CSRF header required |
| 176 | POST | `/api/watch` | daemon/internal/api/api.go:1711 (watch; reg api.go:154) | session | 401 (GET; gate refused) | yes | no | yes |  | /s/[market]/[symbol], /watchlist | STATE: own watchlist only (member-safe); member crypto refused (api.go:1720) |
| 177 | GET | `/api/watchlist` | daemon/internal/api/api.go:870 (watchlist; reg api.go:121) | session | 401 | yes | no |  |  | /s/[market]/[symbol], /today, /watchlist | member branch strips closes/sparks (api.go:871) |
| 178 | GET | `/api/wiki-attention` | daemon/internal/api/dataexpansion.go:180 (wikiAttention; reg dataexpansion.go:293) | session | 401 | no | no |  |  | /s/[market]/[symbol] |  |
| 179 | GET | `/api/world-model` | daemon/internal/api/desk.go:35 (deskWorldModel; reg desk.go:396) | session | 401 | no | no |  |  | /lab/desk |  |
| 180 | GET | `/api/world-model/propagate` | daemon/internal/api/desk.go:51 (deskPropagate; reg desk.go:398) | session | 401 | no | no |  |  | /lab/desk |  |
| 181 | GET | `/api/world-model/shocks` | daemon/internal/api/desk.go:47 (deskShocks; reg desk.go:397) | session | 401 | no | no |  |  | /lab/desk |  |
| 182 | GET | `/api/xs-factor` | daemon/internal/api/xsfactor.go:234 ((inline); reg xsfactor.go:234) | session | 401 | no | no |  |  | **none** |  |
| 183 | POST/GET/DELETE | `/mcp` | daemon/internal/api/mcpmount.go:37 (mcp.Server.Handler; reg mcpmount.go:61-63) | n/a (unmounted) | 404 (Next, not proxied) | n/a | own key auth | yes |  | **none** | NOT MOUNTED: SIGNALDECK_MCP_ENABLED unset; never proxied by web (proxy handles /api/* only); 8323/mcp = Next 404 |

## 5. Feature flags and env toggles that change exposure

Daemon (`daemon/internal/config/config.go` unless noted). Load order: real env, then `daemon/.env` (exported into the process, config.go:222-226), then the default. "private" means a loopback bind, no non-loopback ALLOWED_HOSTS entry, and no PUBLIC_URL or TUNNEL_LOG (config.go:260-261).

| Variable | Effect on exposure | Default | Live |
|---|---|---|---|
| SIGNALDECK_PUBLIC_SURFACE | Switches anonymous access from the legacy denylist to the `publicRoutes` allowlist. Also forces Secure cookies (auth.go:104). | false | unset |
| SIGNALDECK_PUBLIC_READS | Legacy branch: every non-listed read is anonymous when true (security.go:513). | `private` | set; behaves false |
| SIGNALDECK_OPEN_SIGNUP | Enables register and Google sign-up (accounts.go:405, google.go:385). Reported in anonymous /api/health. | `private` | **true** |
| SIGNALDECK_ALLOWED_HOSTS | Host allowlist. Any non-loopback entry makes the box "published". | loopback set (`defaultAllowedHosts`) | set |
| SIGNALDECK_ASSUME_TUNNEL | Forces the tunnel or no-tunnel verdict (config.go:507). | unset | unset |
| SIGNALDECK_PUBLIC_URL | Makes the box published. Origin for email links. **Required for the member digest to send.** | "" | unset |
| SIGNALDECK_TUNNEL_LOG | Makes the box published. The quick-tunnel URL is read from the log for email links and CORS. | "" | set |
| SIGNALDECK_WEB_ORIGINS | CORS/CSRF origin allowlist. | 4 localhost origins | default |
| SIGNALDECK_API_TOKEN | Bearer token that maps to the admin. Usable only directly on 8322, since the proxy drops `Authorization`. | "" | set |
| SIGNALDECK_TRUST_PROXY | Uses the last X-Forwarded-For hop for rate limits. Honours X-Forwarded-Proto for Secure cookies. | false | set (value unknown) |
| SIGNALDECK_LOCAL_PROXY_KEY | Keyed "this request is local" assertion, honoured only when !published (api.go:1136). | "" | set |
| SIGNALDECK_ALLOW_RAW_EXPORT | Lifts the 451 on licensed vendor routes and on bars/exports. | false | unset |
| SIGNALDECK_MEMBER_COPILOT | Lets members use `POST /api/ask`. | false | unset |
| SIGNALDECK_MEMBER_FINRA | Lets members use `/api/shorts` and `/api/short-interest`. | false | unset |
| SIGNALDECK_MCP_ENABLED (+ _SECRET, _AUDIT, _DAILY_CALLS, _REVOKED) | Mounts `/mcp`. Anonymous MCP only on a private box. | false | unset |
| SIGNALDECK_TURNSTILE_SECRET / _SITE_KEY | Bot check on sign-up and reset. With no secret, the check is skipped and a warning logged. | "" | unset |
| SIGNALDECK_GOOGLE_CLIENT_ID | Enables Google sign-in. | "" | unset |
| SIGNALDECK_TV_WEBHOOK_SECRET | Enables `/api/tv-webhook`. Empty means the route fails closed. | "" | set |
| SIGNALDECK_RATE_RPS / _RATE_BURST | Rate-limit tiers. | 10/30 reads | unset |
| SIGNALDECK_LLM_DAILY_CAP; _NVIDIA_KEY(S) / _LLM_KEY; _LLM_BASE_URL; _LLM_MODEL* | LLM spend guard and provider. No key means the AI routes are no-ops. | 2000/day; NVIDIA | key set |
| SIGNALDECK_HTTP | Daemon bind address. | 127.0.0.1:8322 | default |
| SIGNALDECK_HALT / SIGNALDECK_KILL_SWITCH | Kill switch (internal/killswitch/killswitch.go:41-44). | unset | unset |
| SIGNALDECK_DISCORD_WEBHOOK / _SLACK_WEBHOOK / _WEBHOOK_URL / _TELEGRAM_BOT_TOKEN+_CHAT_ID / _SMTP_* | Outbound alert transports. SMTP host+from is also the gate for account mail (notify/slack_smtp.go:191-193). Telegram is the gate for member Telegram linking. | unset | Discord + SMTP set |
| SIGNALDECK_PRICE_VALIDATION_URL | Opt-in second-source price check (outbound fetch). | unset | unset |
| SIGNALDECK_GEMINI_KEY | Read into config (config.go:281) and **used nowhere**. Dead knob. | "" | unset |
| SIGNALDECK_DAEMON (web, server) | Where the proxy and server components send requests. | http://127.0.0.1:8322 | default |
| SIGNALDECK_LOCAL_ONLY_PROXY (web, server) | When "1", the proxy injects `x-signaldeck-local`. | unset | **"1" on the live 8323 instance** |
| SIGNALDECK_DIST_DIR (web) | Build output directory (release isolation). | .next | default |
| NEXT_PUBLIC_SIGNALDECK_PUBLIC (web, build-time) | AuthGate sends anonymous visitors to `/` instead of `/login`. Switches `/proof` copy (proof/page.tsx:41). | unset | unset in build |
| NEXT_PUBLIC_SIGNALDECK_API (web) | API base for the browser. | "" (same-origin) | unset |
| NEXT_PUBLIC_SITE_URL (web, build-time) | Canonical, OG, sitemap and robots URLs. | unset (empty sitemap) | unset |

Documented plan vs live:
- `docs/PUBLIC_RELEASE_PLAN.md:45` prescribes `SIGNALDECK_PUBLIC_SURFACE=1`, `SIGNALDECK_OPEN_SIGNUP=0` and `SIGNALDECK_TRUST_PROXY=1`. Live is PUBLIC_SURFACE off and OPEN_SIGNUP on.
- `ops/GO-LIVE.md:44` says `TRUST_PROXY=false`.

## 6. External integrations

| Integration | Where called | Notes |
|---|---|---|
| **Alpaca** market data (REST bars, IEX websocket, news) | `internal/ingest/alpaca/client.go:28-29`, `streamer.go:24` (wss://stream.data.alpaca.markets/v2/iex), `internal/ingest/news/news.go:21` (v1beta1 news), `internal/discovery/discovery.go:79` | Keys `ALPACA_KEY/ALPACA_SECRET`, falling back to `stock-trader/.env` (config.go:325-336). `paper-api.alpaca.markets` is used only for asset validation (client.go:61). Licensed: bars and news are 451 on a published box. |
| Kraken (crypto history) | `internal/ingest/cryptohist/kraken.go:132` | Restricted terms; members get no crypto. |
| tickstream (local :8321) | `cmd/signaldeckd/run.go:263` (`cryptolive`) | Feeds `/api/snaps` and `/api/stream/snaps` (451 published). |
| trader-hud (local :8787, personal Alpaca PUSH-20) | `cmd/signaldeckd/run.go:274` (`hud.New`) | Served at `/api/hud`, admin only. |
| SEC EDGAR | `internal/ingest/edgar/edgar.go:37`, `bulk.go:41` | Filings, Form 4, 13F, XBRL. UA via `SIGNALDECK_EDGAR_UA` / `SIGNALDECK_CONTACT_EMAIL`. |
| FRED | `internal/ingest/fred/fred.go:49-52` | `SIGNALDECK_FRED_KEY`. |
| FINRA | `internal/ingest/finra/finra.go:47` | Short volume and short interest. Members only with the flag. |
| CFTC COT | `internal/ingest/cftc/cftc.go:35` | |
| CBOE put/call | `internal/ingest/cboe/cboe.go:38` | |
| Hyperliquid | `internal/ingest/hyperliquid/hyperliquid.go:37` | `/api/crypto-perp` is 451 published. |
| Stocktwits | `internal/ingest/stocktwits/stocktwits.go:38` | Restricted; 451 published. |
| Wikimedia pageviews | `internal/ingest/wikimedia/wikimedia.go:40` | |
| Congress trades (S3 mirrors, Kadoa) | `internal/ingest/congress/congress.go:42-43`, `kadoa.go:13` | URL overrides via `SIGNALDECK_HOUSE/SENATE_TRADES_URL` and `SIGNALDECK_CONGRESS_KADOA_URL`. |
| TradingView scanner (data) | `internal/ingest/tvscanner/tvscanner.go:41-42` | `/api/tv-rating`, `/api/tv-quote`, `/api/tv-signals` are 451 published. |
| TradingView **embed** (browser) | `web/src/components/TradingViewChart.tsx`, `lib/tradingviewEmbed.ts`; CSP in `next.config.ts:103-119` | The member price chart, so SignalDeck never redistributes bars. |
| TradingView **webhook** inbound | `internal/api/tvwebhook.go:62-170` | Shared secret. ngrok task `SignalDeck Tunnel` → 8322 exists but is not running. |
| LLM: NVIDIA NIM (OpenAI-compatible) | `internal/llm/llm.go:206-208` (nemotron models); base URL config.go:265 | Consumers: `aiagents/{analyst,chat,debate,filingmind,sentiment,watcher}`, `briefing/{briefing,weekly}.go`, `copilot/ask.go`, `pipeline/{ai,data}.go`, `api/capstones.go:680`. Daily cap 2000. |
| Email: SMTP | `internal/notify/slack_smtp.go:169-216` | Sign-up verify and reset (accounts.go:131-133), and the member digest (inert without PUBLIC_URL). |
| Discord / Slack / generic webhook / Telegram alerts | `internal/notify/notify.go:4-10,134`; Telegram API `internal/memberdigest/memberdigest.go:241-243` | Discord is configured live. `/api/notify-status` reports the transports. |
| Cloudflare Turnstile | daemon `accounts.go:135,344-380`; web `components/auth/Turnstile.tsx:29` | **Not configured live.** |
| Google Sign-In | daemon `google.go:48,193,296`; web `components/auth/GoogleButton.tsx` | **Not configured live.** |
| Cloudflare quick tunnel | task `SignalDeck Quick Tunnel` (cloudflared → 127.0.0.1:8323); URL scraped from the log (accounts.go:263-322) | The public site. The URL changes on every restart, and so do email links. |
| ngrok | task `SignalDeck Tunnel` (`ngrok http 8322 --domain=spearfish-dwindle-module.ngrok-free.dev`) | Direct daemon exposure when started. Currently not running. |
| GitHub public anchors repo | `ops/anchor-publish.sh` (anchors.log, prereg.log, registry, README), `ops/lib-offsite-gh.sh` (`SIGNALDECK_OFFSITE_GH_REPO`, offsite backups); signing key `SIGNALDECK_LEDGER_ANCHOR_KEY` (`internal/ledgeranchor/ledgeranchor.go:65`) | Scheduled task `SignalDeck Anchor-Publish`. |
| Broker / execution layer | `C:\Users\Nicholas_N\Desktop\claude code\execution\execute.py:104-107` (Alpaca paper/live orders), `book.py` | **Not connected to SignalDeck.** book.py:16-22 states it is deliberately not driven by SignalDeck (prereg seq 87). No daemon route places orders. `/api/paper*` and `/api/portfolio*` are internal simulations. |
| Google Fonts | `web/src/app/layout.tsx:2` (`next/font/google`) | Downloaded at build time and self-hosted. |

## 7. Dead, partial or misleading features

- **No anonymous 404 or 5xx** among the 178 daemon paths. Only 401, 405, 400 and 200 were observed. `8323/mcp` is a Next 404 because MCP is unmounted and never proxied.
- **Licensed routes are dead for everyone, operator included.** On this published posture, step 7 of `secureWith` returns 451 with no role exemption (security.go:146-152; datalicense.go:187-220). Affected: `/api/bars`, `/api/snaps`, `/api/news`, `/api/stocktwits`, `/api/crypto-perp`, `/api/stream/snaps`, `/api/tv-quote`, `/api/tv-rating`, `/api/tv-signals` and all 3 CSV exports. Operator pages that fetch them therefore cannot show that data: /dashboard candles, CompanyPeek, /intel/news, /market/signals tv-rating, /lab/live tv-signals, the /s/ operator view (bars, snaps, stream, stocktwits, news), and the /market/overview CSV export. Inferred from code: anonymous probes stop at 401 before step 7.
- **Google sign-in:** the route returns 404 and the button is hidden, because GOOGLE_CLIENT_ID is unset.
- **Turnstile:** the widget code ships but there is no key. Sign-up and reset rely only on the honeypot, the limiters and Gmail-only.
- **Member email digest:** prefs are stored, but the worker needs `SIGNALDECK_PUBLIC_URL` (run.go:1861), and PUBLIC_URL is unset. The `/account` page that controls it is unlinked.
- **Member Telegram linking:** 503, because there is no bot token.
- **Member "Ask the data":** 403, because MEMBER_COPILOT is off. The Shell hides the nav item.
- **MCP server:** fully built (internal/mcp, cmd/signaldeck-mcp) but not mounted.
- **`/api/digest`:** reports `available:false` until the weekly worker fires. It has no web consumer.
- **The seven "machine-readable receipts"** that security.go:273-295 calls the curl-able published record (/api/model-health, /api/canary, /api/postmortems, /api/lineage, /api/dataset-versions, /api/evidence, /api/research-loop) **401 anonymously live**. That contradicts their stated purpose, because PUBLIC_SURFACE is off.
- **`SIGNALDECK_GEMINI_KEY`:** dead config.
- **Stale docs:** next.config.ts:5-8 and web/.env.example say the proxy attaches the API token. It does not.
- **Proxy methods:** PUT and PATCH are exported by the proxy and always 405.
- **No TODO, FIXME or placeholder handlers** in daemon/internal/api or web/src (grep).
- **No page references a removed API.** Every `/api/...` path the web uses exists in the daemon. The only unmatched string is the generic `exportUrl` builder `/api/export/${kind}.csv`.
