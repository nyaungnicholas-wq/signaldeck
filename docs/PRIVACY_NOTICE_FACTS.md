# Privacy notice: verified facts for the drafter (2026-10-05)

**This is not the privacy notice.** It is the factual basis for one, taken from the code and configuration on 2026-10-05 (audit AUD-05). Counsel writes the notice and the terms. Items marked **OPEN** are owner or counsel decisions. Do not publish this file as policy.

## What is collected, and why

| Data | Where | Why | Kept |
|---|---|---|---|
| Username | `users.username` | Sign-in | Until the account is deleted |
| Gmail address (folded to one spelling: dots and `+tags` removed) | `users.email` | Confirmation and password-reset mail; the opt-in digest | Until the account is deleted |
| Password, as a bcrypt hash (cost 10) | `users.pass_hash` | Sign-in | Until the account is deleted |
| Account created time, email-confirmed flag | `users` | Account state | Until the account is deleted |
| Session token | `sessions`, cookie `signaldeck_session` (HttpOnly, SameSite=Lax, Secure over HTTPS) | Keeping you signed in | 30 days, or until sign-out, password reset or deletion |
| Known-browser marker | cookie `signaldeck_device` (HttpOnly, path `/api/auth`, 180 days); an HMAC of the username, no personal data stored server-side | Lets a browser that signed in before keep signing in while someone else's failed guesses lock the name. It is per name, so it outlives a password reset; it grants a separate rate-limited ladder, never access | 180 days in the browser |
| Emailed link tokens, stored only as hashes | `auth_tokens` | Confirmation (24 h) and reset (1 h) links | Until used or expired; deleted with the account |
| Watchlist | `member_symbols` | Your list | Until you remove a symbol or delete the account |
| Your calls and their notes | `member_calls` | Your journal, graded | Until the account is deleted; a call cannot be edited once made |
| Alert settings (digest on/off, optional Telegram chat id) | `member_alert_prefs`, `member_digest_tries` | The opt-in daily digest | Until the account is deleted |
| Notification-list email (the landing page form, no account) | `waitlist` | One email when the volatility record can be judged | **OPEN**: no removal path or retention period exists yet |
| Failed sign-in | daemon log: a 12-character digest of the typed name and its length, no name and no address | Spotting a name under attack | Log rotation (20 MB × 3 files, plus a daily rotation over 10 MB) |
| Requests | daemon log: method, path, status, user id, duration; no IP address, no user agent | Operating the service | Log rotation, as above |

Unverified sign-ups older than 24 hours are deleted the next time anyone signs up. There is no analytics, no advertising tracker and no tracking cookie.

## Your controls (in the product)

- **Download:** Account → "Download my data": a JSON file of the rows above, without the password hash or any token.
- **Delete:** Account → "Delete my account", confirmed with your password. Every row above that is keyed to the account is erased in one transaction.
- **Waitlist removal:** **OPEN**. Needs a contact address now, and an unsubscribe link in the one email when that send is built (nothing sends it yet). Deleting an account does not remove a notification-list row for the same address: the list stores the address as typed, the account stores it folded.

## Who else handles the data

| Party | What they see | Why |
|---|---|---|
| Google (Gmail SMTP) | Your address and the mail sent to it | Sends account mail and the digest |
| Cloudflare (tunnel) | All traffic to the site; TLS terminates at Cloudflare | Puts the site on the internet |
| GitHub (a private repository) | A nightly, **unencrypted** copy of the whole database, including the rows above | Off-site backup; the newest 7 are kept |
| TradingView | Your browser loads its chart widget on member symbol pages | Price charts, so SignalDeck redistributes no price data |
| Cloudflare Turnstile and Google Sign-In | Load on the sign-up and sign-in pages, **only once configured** (neither is configured today) | Bot check; Google sign-in |

## Backups

- On this machine: the local backup folder keeps compressed copies within a disk budget, plus the 2 newest uncompressed ones. The cleanup moves older ones to a local holding folder that is emptied by hand.
- Off-site: the newest 7 GitHub releases.
- A deleted account therefore remains in backups until they age out. **OPEN**: whether to encrypt the off-site copy, and the period to state.

## Open decisions for the owner and counsel

1. A contact address to publish, for privacy requests and waitlist removal.
2. The jurisdiction and governing law; any age requirement (no age gate exists).
3. Whether to encrypt off-site backups, and the backup retention to state.
4. Waitlist retention and a removal path.
5. Consent for third-party scripts (Google Sign-In, Turnstile) for EU and UK visitors once they are enabled.
6. The terms of use, including "descriptive market analysis, not financial advice", data-source attributions, and the Kraken derived-works question (crypto-derived grades are public).
