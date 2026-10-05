# Findings ledger: release audit 2026-10-05

Revision audited: `public-launch` 1249c436 (runtime code identical to the live daemon 41a817a9 and web build 6a1a327). Repairs are on branch `audit-1005`.

Kinds: **D** = confirmed defect, **R** = unverified risk, **L** = expected limitation or owner/legal decision.

Status values: `FIXED` (code changed and verified, see verification.md), `OPEN` (not repaired in this pass), `OWNER` (needs the owner's account, money, policy or legal decision), `BLOCKED`.

The repository is public. Unrepaired security items are described here without exploit steps; full reproductions are kept off the public record.

## Critical
None found.

## High

| ID | Kind | Area | Finding | Impact | Status |
|---|---|---|---|---|---|
| AUD-01 | D | Sign-up | The public site took sign-ups with no server-side bot check (no Turnstile secret). Only a honeypot, per-network limits and the Gmail-only rule applied. Every sign-up, and every "account already exists" reply, sends mail from the operator's own mailbox. | A distributed bot could spend that mailbox's daily sending quota and sender reputation, and mail arbitrary Gmail users. | FIXED: a published daemon with no Turnstile secret now refuses sign-up (503, with the reason). `/api/health` reports `openSignup:false, signupPaused:true`, and `/signup` shows a "sign-ups paused" notice. Setting the Turnstile keys reopens it. |
| AUD-02 | D | Evidence | `/accuracy` printed the retired flagship's 1d/1w directional figures from a hardcoded constant. A comment exempted that constant from the banned-figure scanner. The SD-30 withhold covers every such figure, and the same page's refusal path says they are "not published here". | Public figures measured on the leaked pre-SD-30 label, contradicting the stated publication contract. | FIXED: the block keeps the retirement date and the FAILED verdict, and says why the figures are withheld. The self-contradicting "rows below" sentence and the stale "re-registered 2026-09-20" footer are corrected. |
| AUD-03 | D | Backups | On 2026-10-04 14:05 the cleanup kept a **0-byte** `signaldeck-20261003-200108.db`, left by a db-backup run cut off by a shutdown, as one of its "2 newest". It moved the newest sha256-recorded, offsite-verified backup (10-02) to `~/.Trash`. The worker had written straight to the final name, so an interrupted copy looked like a generation. | The newest verified local backup was retired. A torn copy could also have been kept as the restore point. | FIXED: the worker writes `<name>.partial` and renames only after the integrity and content checks pass. The cleanup retires 0-byte files without counting them and never retires the newest backup that has a `.sha256`. The trashed 10-02 file is still in `~/.Trash` and in GitHub release `backup-20261002-131010`. |
| AUD-04 | L | Availability | The site is served from this PC through a Cloudflare quick tunnel. The PC was off 29.8% of the last 14 days (9 manual shutdowns), and the public URL changed 5 times in 6.6 days. Each change signs everyone out and breaks emailed links. Nothing outside the PC watches the public URL. | A real user cannot rely on the address or the uptime. | OWNER: a named tunnel on `signaldeck.nicholasnyaung.com` (zone move to Cloudflare) plus an always-on host or acceptance of PC hours. See release_verdict.md. |
| AUD-05 | L | Privacy | No privacy notice, terms or public contact (`/privacy` and `/terms` are 404). There is no account deletion, data export or email change. Accounts, watchlists, journal notes and waitlist entries are kept indefinitely. Nightly unencrypted database copies, including emails and bcrypt hashes, go to a **private** GitHub repository and are kept 7 days. | Real-user readiness blocker. The policy statements and the rights process are not in place. | OWNER: counsel and the owner decide the policy text and the contact address. Deletion and export are engineering work once the policy defines them. The false on-page privacy promises were corrected (AUD-10). |

## Medium

| ID | Kind | Area | Finding | Status |
|---|---|---|---|---|
| AUD-06 | D | Auth | The sign-in lockout was keyed on the username alone. Anyone could keep any account locked, including the admin, which has no emailed reset, with about 96 requests a day. | FIXED for the owner: sign-ins typed on the machine itself (loopback, no `X-Forwarded-For`) count on their own ladder, and both tunnels always add `X-Forwarded-For`. Residual: members can still be slowed through the internet (AUD-35). |
| AUD-07 | D | Abuse | The account rate-limit table was wiped whenever it held 50,000 keys. That reset every exhausted budget, including a victim address's mail limit. | FIXED: past 50,000 keys only the keys with budget left are shed. The table is dropped only past 200,000. |
| AUD-08 | R | Exposure | The weekday ngrok webhook tunnel serves the daemon port directly, bypassing the web tier, so the operator API token is accepted over the internet while it runs. | OWNER: restrict the tunnel to the webhook path or retire it. |
| AUD-09 | R | Exposure | The public web instance runs with the local-proxy key, so every public request reaches the daemon marked local. Only the `published()` evidence (today the tunnel log) keeps operator authority and raw data closed. This is already documented in ops/CLOUDFLARE_TUNNEL.md section 3. | OPEN: set the public posture explicitly (`SIGNALDECK_PUBLIC_URL` or `SIGNALDECK_PUBLIC_SURFACE`), or run the public web tier without the local key. This belongs to the domain cutover. |
| AUD-10 | D | Copy | False or misleading statements: waitlist "stored on this server only … contact the operator" (backups go off-site, and no contact exists); sign-up CTA "never send anything but the record"; login "private workspace … dashboard"; receipts "Open the full workspace" (operator-only link); landing "paper book under Lab" (not reachable), "No figure without an interval" (above point estimates), "Recompute the whole chain yourself" (operator-only full walk); `/today` "accuracy it has actually measured" (backtests); unknown symbol shown as "on our side" with Retry. | FIXED (web). |
| AUD-11 | D | Evidence | Public `/api/track-record` `regimes` block publishes structural intervals the registry withholds (2 of 10 blocks), and disagrees with the registry on n and days. | FIXED (952e6e85): `regimes` is operator-only on /api/track-record |
| AUD-12 | D | Evidence | `/api/accuracy` builds rows only from the registry. The four sticky RETIRED rows in the database never reach it, so the landing table shows no retirement. | OPEN |
| AUD-13 | R | Ops | `/api/health` always answers 200, and `/api/ready` ignores degraded workers. Neither checks data freshness, grader heartbeat, backup age or WAL size. `daemon-guard` checks only that the process exists. | OPEN |
| AUD-14 | D | Ops docs | DR_RUNBOOK states RPO ≤ ~24 h, but scheduled backups run weekdays only. The offsite copy was 54 h old at audit time, up to ~3 days. | FIXED (35012ab3): runbook states up to ~3 days across a weekend |
| AUD-15 | L | Ops | The live "SignalDeck Quick Tunnel" task still lacks the BootTrigger added to the repo XML on 10-01, so the fleet health check reports UNHEALTHY every day. | OWNER (elevated shell; block in ops/CLOUDFLARE_TUNNEL.md section 4) |
| AUD-16 | D | Evidence | The paper P&L of the retired 1d model is served publicly and shown to members on `/today`. | FIXED (952e6e85): `paper` is operator-only; the member strip drops the block |
| AUD-17 | L | Data rights | Crypto-derived grades are public, while DATA_SOURCES.md records the Kraken terms as restricting derived works. | OWNER / counsel |
| AUD-18 | R | DR | The second-machine DR drill has never run. The ledger anchor signing key is in no backup by design, and whether an offline copy exists is unverified. The weekly data-level restore rehearsal passes, latest 2026-10-04. | OPEN |
| AUD-19 | R | DB | Write locks up to 48 s (166 long-hold warnings on 10-04), WAL peaks of 8.5 GB and 1.4 GB, and `/api/accuracy` returned 503 ten times in 24 h. | OPEN |
| AUD-20 | R | Backups | The offsite prune fixed on 10-03 first runs Mon 10-05 13:10 and will delete 5 of 12 releases. A read-only check is already scheduled for 14:30 PT. | WATCH |

## Low

| ID | Finding | Status |
|---|---|---|
| AUD-21 | Sign-up reveals taken usernames (409, and a faster answer). Moot while sign-up is paused. | OPEN |
| AUD-22 | 15 handlers return raw store errors in 500 bodies, including anonymous `/api/prereg`. | FIXED (35012ab3): all 15 go through httpInternal; TestNoRawErrorsIn5xxBodies scans the package |
| AUD-23 | No HSTS header, and the CSP allows inline scripts. | PARTLY FIXED (35012ab3): HSTS added; the CSP still allows inline scripts (Next inline bootstrap) |
| AUD-24 | The `/verify` page redeems the link on load, so a mail scanner that runs JavaScript gets the session. | FIXED (35012ab3): the token is redeemed only on "Confirm my email" |
| AUD-25 | The failed-login log line records whatever was typed as the username, which can be an address. | FIXED (35012ab3): logs a 12-hex digest and the length |
| AUD-26 | `daemon/.env.bak-*` files keep plaintext copies of the secrets on disk. They are untracked and local. | OWNER (delete or move) |
| AUD-27 | The daemon resolves `data/accuracy_registry.json` relative to its working directory. Started elsewhere, it refuses publication as "registry unavailable". | OPEN |
| AUD-28 | A deep link (e.g. `/watchlist`) is not restored after sign-in; every member lands on `/today`. | FIXED (35012ab3): `?next=` through lib/safenext (same-origin paths only) |
| AUD-29 | `/signup` and `/account` titles read "… — SignalDeck — SignalDeck". | FIXED (35012ab3) |
| AUD-30 | `/sitemap.xml` is empty until `NEXT_PUBLIC_SITE_URL` is set at build time. | OPEN (domain cutover) |
| AUD-31 | `requirements-quant.txt` lists unused `openbb` (AGPL) and `vectorbt` (Commons Clause) in a public repository. | OWNER |
| AUD-32 | The anonymous waitlist stores rows without a bound. | OPEN |
| AUD-33 | MCP `get_track_record` carries a hardcoded backtest block and says "none has resolved". MCP is disabled in production. | OPEN |
| AUD-34 | A db-backup run cut off by shutdown was recorded `ok … stopped (shutdown)`. | OPEN (AUD-03 removes its debris) |
| AUD-35 | Residual of AUD-06: a member account can still be locked through the internet for up to 15 min per lock. | OPEN |
| AUD-36 | Per-network limits treat an IPv6 /64 as one client, so one /48 holds 65,536 budgets. Turnstile is the control. | OWNER (Turnstile keys) |
| AUD-37 | Backtest-only regime reads carry "VALIDATED" badges on member pages (TodaysRead, ValidatedSignalsPanel). The `/today` header was corrected; the badges were not. | FIXED (81c09af0): member badges read "BACKTEST-VALIDATED" |
| AUD-38 | Status words (NO_BASELINE, INSUFFICIENT, WITHHELD …) are missing from the glossary, while three pages say every term is in it. | FIXED (81c09af0): glossary "Status words" section, from the banner's own map |
| AUD-39 | After a daemon restart, `/api/ledger/verify` answers 503 "warming" for 15–30+ min (10-01: about 16 min; 10-05: still warming 15 min after the 20:42 PT restart) while `/proof` reads "still preparing". Rebuilds log "hit the detached-build ceiling" within 4 min of boot, so that warning reports a different deadline than the 10-minute ceiling it names. The intermittent 500/503 on this route predates this audit. | OPEN (measured 10-05: warm-up took about 15 min after the 20:42 PT restart) |

## Regression leads from the brief, re-checked

| Lead | Result |
|---|---|
| `/proof` hydration | Renders and hydrates (public tunnel, 2026-10-05 02:47Z). No console errors besides the expected anonymous 401 on `/api/auth/me`. |
| Stale registry fallback | `/accuracy` shows refusal text and no figures when the registry is unreadable (isolated copy, reproduced). The hardcoded flagship block was the one stale-number path (AUD-02). |
| Proxy limits | Body limits apply before buffering; the web proxy forwards every `/api/*` path and the daemon is the only gate (AUD-09). |
| Staging races / container provenance | Not re-tested this pass. Native deploy only; no container deploy is in use. |
| Contradictory readiness documents | DEPLOY.md and CLOUDFLARE_TUNNEL.md disagree with the code on task installation and on the DR runbook's platform (logged in the ops review). Not repaired. |
