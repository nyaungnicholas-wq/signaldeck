# Privacy notice: verified facts for the drafter (2026-10-05)

**This is not the privacy notice.** It is the factual basis for one, taken from the code and configuration on 2026-10-05 (audit AUD-05). Counsel writes the notice and the terms. Items marked **OPEN** are owner or counsel decisions. Do not publish this file as policy.

**Update 2026-10-22:** the retention, removal, backup-encryption and data-source items are now **DECIDED** (marked inline below) and published on /privacy and /terms, dated 22 October 2026.

**Update 2026-10-23 (licence gate):** on a published deployment everything derived from Alpaca market data or Kadoa's congress rows (prices, forecasts, grades, statistics, the member digest's reads, the journal and its grades, crypto history) is served only to the operator; everyone else, members included, gets an HTTP 451 "withheld" answer. The member daily digest is therefore sent to the operator's account only: a member can still store the digest switch and a Telegram link, but nothing is sent to them. Kadoa collection stopped on 2026-10-23.

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
| Alert settings (digest on/off, optional Telegram chat id) | `member_alert_prefs`, `member_digest_tries` | The opt-in daily digest (sent to the operator's account only since 2026-10-23; a member's switch is stored but nothing is sent) | Until the account is deleted |
| Notification-list email (the landing page form, no account) | `waitlist` | One email when the volatility record can be judged | **DECIDED 2026-10-22**: up to 12 months, or sooner on request to the contact address |
| Failed sign-in | daemon log: a 12-character digest of the typed name and its length, no name and no address | Spotting a name under attack | Log rotation by size (20 MB × 3 files, plus a daily rotation over 10 MB); **DECIDED 2026-10-22**: the limit is 30 days after a log file's last entry (see the interim note under Backups). Rotation is by size, not date, so on a quiet site one file can cover months and an entry can be kept longer than 30 days |
| Requests | daemon log: method, path, status, user id, duration; no IP address, no user agent | Operating the service | As above |

Unverified sign-ups older than 24 hours are deleted the next time anyone signs up. There is no analytics, no advertising tracker and no tracking cookie.

## Your controls (in the product)

- **Download:** Account → "Download my data": a JSON file of the rows above, without the password hash or any token.
- **Delete:** Account → "Delete my account", confirmed with your password. Every row above that is keyed to the account is erased in one transaction. Since 2026-10-22 a deletion also records the account number and one one-way (SHA-256) hash of the account's email address, username and creation time, and nothing else, in a separate file outside the database, kept indefinitely, so restoring an older backup cannot bring the account back and a later re-registration is never mistaken for it. A sealed (encrypted) copy of this record is uploaded with each off-site backup. Because the record itself is backed up with each off-site copy, an account deleted after the newest off-site copy was made could come back if the operator's computer were lost before the next weekday backup.
- **Waitlist removal:** **DECIDED 2026-10-22**: on request to privacy@nicholasnyaung.com (published 2026-10-11), and entries are kept up to 12 months. An unsubscribe link is still needed in the one email when that send is built (nothing sends it yet). Deleting an account does not remove a notification-list row for the same address: the list stores the address as typed, the account stores it folded.

## Who else handles the data

| Party | What they see | Why |
|---|---|---|
| Google (Gmail SMTP) | Your address and the mail sent to it | Sends account mail and the digest |
| Cloudflare (tunnel) | All traffic to the site; TLS terminates at Cloudflare | Puts the site on the internet |
| GitHub (a private repository) | A compressed copy of the whole database each weekday, including the rows above; since 2026-10-22 **encrypted (AES-256-GCM)** on the operator's computer before upload, with a key that is kept on the operator's computer, separate from the database, and is never uploaded with the backups; copies made before then are unencrypted and leave the rotation as newer ones replace them. A sealed copy of the deletion record goes up with each backup | Off-site backup; the newest 7 are kept (about a week and a half) |
| TradingView | Your browser loads its chart widget on member symbol pages | Price charts, so SignalDeck redistributes no price data. Separately, the operator's own TradingView alerts arrive by webhook and are stored for the record, but are no longer used as a model input since 2026-10-22 (licence review) |
| Cloudflare Turnstile and Google Sign-In | Turnstile is live on sign-up and forgot-password (since 2026-10-11); Google Sign-In is not configured | Bot check; Google sign-in |
| NVIDIA (AI model API) | **Nothing since 2026-10-23.** Before then: the "Ask the data" question and a journal summary (switched off for member accounts), and Alpaca/Benzinga news headlines (no personal data) for sentiment tagging. Retired because the hosted API was used under NVIDIA's trial terms, which do not permit production use; the daemon now refuses NVIDIA hosts and no AI answers are produced | Was: AI answers; sentiment tags |

## Backups

- On this machine: the local backup folder keeps the 5 newest compressed copies, plus up to 2 uncompressed ones (the newest). The cleanup moves older ones to a local holding folder; **DECIDED 2026-10-22**: kept there up to 30 days. Local copies are not encrypted.
- Off-site: the newest 7 GitHub releases (about a week and a half).
- A deleted account therefore remains in logs until the log file holding it is deleted (30 days after that file's last entry) and in backups until they rotate out (off-site about a week and a half; local holding folder up to 30 days). **DECIDED 2026-10-22**: off-site copies are encrypted from that date.
- The limits were set on 2026-10-22. A daily automated job checks the log, holding-folder and waitlist limits. Until automatic deletion is switched on, it lists anything past its limit and the operator removes it by hand, so logs and backup copies older than these limits may still exist for a short time. Older full copies of the database made before the limits were set also still exist on the operator's computer (in the local holding folder, in an inactive local cloud-sync folder, and a few one-off copies made during maintenance); the operator is deleting them by hand. No completion date is promised.

## Open decisions for the owner and counsel

1. ~~A contact address to publish~~ **DECIDED**: privacy@nicholasnyaung.com, published 2026-10-11.
2. The jurisdiction and governing law; any age requirement (no age gate exists).
3. ~~Whether to encrypt off-site backups, and the backup retention to state~~ **DECIDED 2026-10-22** (see Backups).
4. ~~Waitlist retention and a removal path~~ **DECIDED 2026-10-22**: 12 months, removal on request.
5. Consent for third-party scripts for EU and UK visitors: Turnstile (live since 2026-10-11) and Google Sign-In (if it is ever configured).
6. The terms of use, including "descriptive market analysis, not financial advice", data-source attributions, and the Kraken derived-works question (crypto-derived grades are public). *2026-10-22:* the Kraken question is moot (crypto paused); the source list below is on /terms; the remaining licence questions are in counsel packet version 3.

## Data sources (2026-10-22)

- **In use:** Alpaca Markets (US stock market data and news; licensed for personal use only, so everything derived from it is shown only to the operator since 2026-10-23), SEC EDGAR (filings, Form 4, 13F, company facts), the CFTC (Commitments of Traders), Wikimedia (page-view counts, CC0), and TradingView (chart widget on member symbol pages; the operator's own alerts by webhook).
- **Retired:** Kadoa's congress-trades file (2026-10-23: its terms bar automated access and sharing its rows; no stored row records its source, Kadoa was the fallback from 2026-09-07 and the Stock Watcher mirrors answer 403, so every stored row is treated as Kadoa's and shown only to the operator); TradingView scanner and StockTwits (2026-10-21/22); crypto data from Kraken, Coinbase and Hyperliquid (crypto paused 2026-10-22; history kept but shown only to the operator since 2026-10-23, open crypto forecasts voided, never deleted); FINRA, Cboe and FRED (retired 2026-10-22: licence review); NVIDIA's hosted model API, which tagged headline sentiment (retired 2026-10-23: trial terms do not permit production use). Forecasts made before these dates used data from these sources.
