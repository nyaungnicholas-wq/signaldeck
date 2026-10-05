# Release verdict: 2026-10-05

Overall task state: **BLOCKED — NOT COMPLETE**. Engineering repairs are deployed and verified. The real-user scope depends on owner decisions and accounts (below), and engineering items AUD-11 … AUD-39 remain open.

## 1. Public demo: READY FOR STATED SCOPE

**Scope.** Anonymous, read-only visitors to the live record (`/`, `/accuracy`, `/proof`, `/volatility`, `/glossary`) at the current quick-tunnel address, while the owner's PC is on. Excluded: sign-up (paused), the email waitlist form (see limitation 3), member features, uptime guarantees and a stable address.

Evidence:
- Every public page renders after hydration on the live tunnel. The only console errors are the expected anonymous `401` on `/api/auth/me`. No horizontal overflow at 375 px.
- The evidence contract holds on every page checked. No SD-30-withheld 1d/1w figure is printed (AUD-02 fixed and live). Refused, insufficient and no-baseline states are shown as such.
- False or overclaiming copy was corrected (AUD-10, claims.md).
- Sign-up says plainly that it is paused (AUD-01).
- Runtime identity: daemon and web both serve the deployed commit (see Deployment).

Material limitations:
1. Availability (AUD-04). The site is up only while the PC is on (off 29.8% of the last 14 days), and the address changes on every tunnel restart.
2. After a daemon restart, the ledger-check panel on `/proof` reads "still preparing" for 15–30+ minutes (AUD-39). The page says so honestly; it is not an error.
3. The waitlist form collects email addresses. Its on-page text is now accurate, but no privacy notice or removal contact exists (AUD-05). Treat it as outside the demo scope until the owner decides to keep it under a notice or to remove it.

## 2. App for real users: NOT READY

Blocking (any one is enough):
- No new users can join. Sign-up is paused by design until Turnstile is configured (AUD-01/AUD-36: owner creates the widget and sets two keys).
- No privacy notice, terms or public contact, and no account deletion or data export (AUD-05: owner and counsel).
- Availability and identity. A rotating trycloudflare address signs every member out and breaks emailed links on each restart, and nothing monitors the site from outside (AUD-04: stable hostname and host).
- Member email digest is inert: it needs `SIGNALDECK_PUBLIC_URL`, which arrives with the stable hostname.
- Data rights questions are open for public crypto-derived grades (AUD-17).

Verified for members (isolated copy): sign-in, sign-out, password reset, email verification, watchlist, journal, account isolation, CSRF and origin guards, member/operator separation.

## 3. Trading functionality: NOT APPLICABLE (SignalDeck); separate execution layer NOT READY and DISARMED

- SignalDeck offers users no trading. The paper book is operator-only and simulated, and no order path exists from the web or daemon to a broker. SignalDeck and the execution layer do not call each other.
- The execution layer (`execution/`, main d9cf131) has no live capability armed: no `LIVE_ARMED`, no live authorisation file, account id unset, `CAPITAL_RUNG` 0, no live keys, no scheduled task, no listener. Its own readiness record says live is not ready, with owner steps outstanding. This audit did not exercise it and has no authorisation to.
- The only scheduled order flow on the host is the stock-trader daily rotation, against the Alpaca **paper** endpoint.

## Deployment

| Step | Result |
|---|---|
| Round 1 commit | f07075f6 (audit-1005, fast-forwarded into public-launch) |
| Daemon deploy | `ops/signaldeck-ctl.sh deploy` built from `git archive` f07075f6 after vet, lint, tests and the manifest check. It printed "UNVERIFIED: /api/version did not answer" during a slow boot. Checked directly afterwards: `/api/version` revision f07075f6 (`modified:false`, `resolvable:true`), and new `worker_runs` rows stamped f07075f6. Rollback: `daemon/signaldeckd.exe.prev`. |
| Web release | `ops/web-release.ps1` built f07075f6 (BUILD_ID YhbA8McAcxOcoTFcdUUhQ). The asset gate passed, and the browser gate passed 7/7. Promoted 20:50 PT. Rollback: `web/.next-prev-20261004-204951`. |
| Post-deploy (public tunnel) | `/signup` shows the pause; `/api/health` reports `openSignup:false, signupPaused:true`; `/accuracy` shows no withheld figures and the corrected footer; landing copy is corrected. Anonymous `/api/watchlist` and CSV export answer 401. |
| Round 2 daemon | 35012ab3 + 952e6e85 + 53afe5d5: `ctl deploy` printed "deploy VERIFIED: daemon is running commit 53afe5d5 (resolvable); 3 worker run(s) already stamped with it". |
| Round 2–3 web | 81c09af0 (web-only on top of 53afe5d5) released by `ops/web-release.ps1`: build tMgxjN2aJ8Bbnl96Dz8x2, browser gate passed, promoted 21:11 PT. Rollback: `web/.next-prev-20261004-211120`. |
| Post-deploy round 2–3 (public tunnel) | The glossary has its status words. `/watchlist` signed out goes to `/login?next=%2Fwatchlist`. `/verify` waits for "Confirm my email". HSTS is present. Anonymous `/api/track-record` has no `regimes` and no `paper`. |
| Push | `public-launch` pushed by refspec after each verified deploy; main is synced by PR (see CURRENT_STATE.md). |

## Exact next actions

Owner:
1. **Turnstile** (re-opens sign-up). In the Cloudflare dashboard, create a free Turnstile widget for the site hostname. Put its two values in `daemon/.env` as `SIGNALDECK_TURNSTILE_SITE_KEY` and `SIGNALDECK_TURNSTILE_SECRET` through your usual secret handling, never in chat. Restart with `bash ops/signaldeck-ctl.sh deploy`. Check that `/api/health` shows `openSignup:true` and `signupPaused:false`. Turnstile hostnames cannot follow a rotating quick-tunnel URL, so do this after step 2 or together with it.
2. **Stable hostname** (`signaldeck.nicholasnyaung.com`). Finish the zone move to a free Cloudflare account. Send the two assigned nameservers so the zone can be checked before you switch GoDaddy's nameservers. Then run `cloudflared tunnel login`. The cutover itself is ops/CLOUDFLARE_TUNNEL.md section 3, which also closes AUD-09.
3. **Privacy notice, terms, contact address**, and a decision on account deletion and export (AUD-05). Counsel drafts. A fact sheet for the notice is in the privacy section of findings.md (AUD-05).
4. **Elevated shell, once.** Re-register the Quick Tunnel task with its BootTrigger (ops/CLOUDFLARE_TUNNEL.md section 4) (AUD-15).
5. Decide the weekday ngrok tunnel: restrict it to the webhook path, or retire it (AUD-08).
6. Delete or move `daemon/.env.bak-*` (AUD-26). Decide the unused AGPL dependencies (AUD-31) and the Kraken derived-works question (AUD-17).

Engineering (no owner input needed): AUD-12, 13, 19, 39 first (evidence and reliability), then AUD-18, 21, 27, 30, 32–35.
