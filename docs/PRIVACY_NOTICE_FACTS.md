# Privacy notice: verified facts for the drafter (2026-10-05)

**This is not the privacy notice.** It is the factual basis for one, taken from the code and configuration on 2026-10-05 (audit AUD-05). Counsel writes the notice and the terms. Items marked **OPEN** are owner or counsel decisions. Do not publish this file as policy.

**Update 2026-10-22:** the retention, removal, backup-encryption and data-source items are now **DECIDED** (marked inline below) and published on /privacy and /terms, dated 22 October 2026.

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
| Notification-list email (the landing page form, no account) | `waitlist` | One email when the volatility record can be judged | **DECIDED 2026-10-22**: up to 12 months, or sooner on request to the contact address |
| Failed sign-in | daemon log: a 12-character digest of the typed name and its length, no name and no address | Spotting a name under attack | Log rotation (20 MB × 3 files, plus a daily rotation over 10 MB); **DECIDED 2026-10-22**: logs kept up to 30 days |
| Requests | daemon log: method, path, status, user id, duration; no IP address, no user agent | Operating the service | Up to 30 days, as above |

Unverified sign-ups older than 24 hours are deleted the next time anyone signs up. There is no analytics, no advertising tracker and no tracking cookie.

## Your controls (in the product)

- **Download:** Account → "Download my data": a JSON file of the rows above, without the password hash or any token.
- **Delete:** Account → "Delete my account", confirmed with your password. Every row above that is keyed to the account is erased in one transaction. Since 2026-10-22 a deletion also records the account number and a one-way (SHA-256) hash of the email address, nothing else, in a separate file outside the database, kept indefinitely, so restoring an older backup cannot bring the account back.
- **Waitlist removal:** **DECIDED 2026-10-22**: on request to privacy@nicholasnyaung.com (published 2026-10-11), and entries are kept up to 12 months. An unsubscribe link is still needed in the one email when that send is built (nothing sends it yet). Deleting an account does not remove a notification-list row for the same address: the list stores the address as typed, the account stores it folded.

## Who else handles the data

| Party | What they see | Why |
|---|---|---|
| Google (Gmail SMTP) | Your address and the mail sent to it | Sends account mail and the digest |
| Cloudflare (tunnel) | All traffic to the site; TLS terminates at Cloudflare | Puts the site on the internet |
| GitHub (a private repository) | A compressed copy of the whole database each weekday, including the rows above; since 2026-10-22 **encrypted (AES-256-GCM)** on the operator's computer before upload, key kept offline; copies made before then are unencrypted and leave the rotation as newer ones replace them | Off-site backup; the newest 7 are kept (about a week and a half) |
| TradingView | Your browser loads its chart widget on member symbol pages | Price charts, so SignalDeck redistributes no price data. Separately, the operator's own TradingView alerts arrive by webhook as one model input |
| Cloudflare Turnstile and Google Sign-In | Turnstile is live on sign-up and forgot-password (since 2026-10-11); Google Sign-In is not configured | Bot check; Google sign-in |
| NVIDIA (AI model API) | The "Ask the data" question and a journal summary; the feature is switched off for member accounts. Also receives Alpaca/Benzinga news headlines (no personal data) for sentiment tagging | AI answers; sentiment tags |

## Backups

- On this machine: the local backup folder keeps compressed copies within a disk budget, plus the 2 newest uncompressed ones. The cleanup moves older ones to a local holding folder; **DECIDED 2026-10-22**: kept there up to 30 days. Local copies are not encrypted.
- Off-site: the newest 7 GitHub releases (about a week and a half).
- A deleted account therefore remains in logs up to 30 days and in backups until they rotate out (off-site about a week and a half; local holding folder up to 30 days). **DECIDED 2026-10-22**: off-site copies are encrypted from that date.
- A daily automated job checks the log, holding-folder and waitlist limits. Until automatic deletion is switched on, it lists anything past its limit and the operator removes it by hand.

## Open decisions for the owner and counsel

1. ~~A contact address to publish~~ **DECIDED**: privacy@nicholasnyaung.com, published 2026-10-11.
2. The jurisdiction and governing law; any age requirement (no age gate exists).
3. ~~Whether to encrypt off-site backups, and the backup retention to state~~ **DECIDED 2026-10-22** (see Backups).
4. ~~Waitlist retention and a removal path~~ **DECIDED 2026-10-22**: 12 months, removal on request.
5. Consent for third-party scripts (Google Sign-In, Turnstile) for EU and UK visitors once they are enabled.
6. The terms of use, including "descriptive market analysis, not financial advice", data-source attributions, and the Kraken derived-works question (crypto-derived grades are public). *2026-10-22:* the Kraken question is moot (crypto paused); the source list below is on /terms; the remaining licence questions are in counsel packet version 3.

## Data sources (2026-10-22)

- **In use:** Alpaca Markets (US stock market data and news), SEC EDGAR (filings, Form 4, 13F, company facts), the CFTC (Commitments of Traders), Wikimedia (page-view counts, CC0), congressional STOCK Act disclosures via third-party datasets (Kadoa's public congress-trades file; older rows from House/Senate Stock Watcher), and TradingView (chart widget on member symbol pages; the operator's own alerts by webhook).
- **Retired:** TradingView scanner and StockTwits (2026-10-21/22); crypto data from Kraken, Coinbase and Hyperliquid (crypto paused 2026-10-22; history kept on the public record, open crypto forecasts voided, never deleted); FINRA, Cboe and FRED (retired 2026-10-22: licence review; forecasts made before then used FRED data).
