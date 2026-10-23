import type { Metadata } from "next";
import Link from "next/link";
import { OPERATOR_NAME, CONTACT_EMAIL, LEGAL_UPDATED } from "@/lib/legal";

export const metadata: Metadata = { title: "Privacy Notice" };

export default function PrivacyPage() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">
        Privacy Notice
      </h1>
      <div role="note" className="panel px-4 py-3 text-[0.8rem]" style={{ color: "var(--dim)" }}>
        Draft, pending legal review. Last updated {LEGAL_UPDATED}. Questions: <a href={`mailto:${CONTACT_EMAIL}`} style={{ color: "var(--dim)", textDecoration: "underline" }}>{CONTACT_EMAIL}</a>.
      </div>
      <section className="panel px-5 py-4" aria-label="Who we are">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Who we are</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          SignalDeck (signaldeck.nicholasnyaung.com) is run by {OPERATOR_NAME}. Contact: <a href={`mailto:${CONTACT_EMAIL}`} style={{ color: "var(--dim)", textDecoration: "underline" }}>{CONTACT_EMAIL}</a>.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="What we collect and why">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">What we collect and why</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          You can browse SignalDeck without an account. An account needs a username, a Gmail address and a password; everything else is optional.
        </p>
        <div className="overflow-x-auto">
          <table className="w-full text-[0.8rem] border-collapse" style={{ minWidth: 0, tableLayout: "fixed" }}>
            <thead>
              <tr>
                <th className="border-t px-2 py-1.5 align-top text-left font-semibold" style={{ borderColor: "var(--border)" }}>
                  Data
                </th>
                <th className="border-t px-2 py-1.5 align-top text-left font-semibold" style={{ borderColor: "var(--border)" }}>
                  Why
                </th>
                <th className="border-t px-2 py-1.5 align-top text-left font-semibold" style={{ borderColor: "var(--border)" }}>
                  How long we keep it
                </th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Username
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To sign you in
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until you delete your account
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Gmail address, normalised (lower-cased; googlemail.com becomes gmail.com; dots and +tags removed)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Account confirmation, password reset, and the daily digest if you turn it on (paused for member accounts since 23 October 2026: it goes only to the operator)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until you delete your account
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Password, stored only as a bcrypt hash
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To check your sign-in
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until you delete your account
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Account creation time and email-confirmed flag
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To manage your account
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until you delete your account
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Sign-in sessions, stored only as a digest of the session token
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To keep you signed in
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Up to 30 days; ended by signing out, resetting your password, deleting your account, or when an account passes 20 sessions (oldest first)
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Emailed link tokens, stored only as hashes: confirmation (valid 24 hours), password reset (1 hour), digest unsubscribe (60 days)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To make the emailed links work
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Confirmation and reset records stay, marked used or expired, until you delete your account; unsubscribe records are deleted after they expire
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Watchlist
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Your list of symbols
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until you remove a symbol or delete your account
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Your calls and their notes (up to 280 characters each)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Your private journal, graded (withheld from member accounts since 23 October 2026, because the grades are derived from Alpaca market data; stored calls are kept)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until you delete your account. A call cannot be edited once made; an open call can be withdrawn and stays marked &ldquo;withdrawn&rdquo;
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Alert settings: digest on or off, the date of your last digest, today&rsquo;s delivery attempts, and, if you link Telegram, your Telegram chat id and a 15-minute link code
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To send the daily digest you asked for (paused for member accounts since 23 October 2026; the setting is kept and nothing is sent)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until you delete your account
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Waitlist email (the landing-page form, no account needed), trimmed and lower-cased, with the time it was added
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To send one email when the volatility record can be judged (nothing sends it yet)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Up to 12 months, or sooner if you ask us to remove it (email the contact address)
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Failed sign-in record: a short digest of the name typed and its length, never the name itself
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To spot a name under attack
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Until its log file is deleted; the limit is 30 days after that file&rsquo;s last entry (see the note on logs below and the retention limits under Backups and deletion)
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Request log: method, path without query string, status, account number, duration. No IP address, no user agent
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To run and fix the service
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  As for failed sign-in records
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Deletion record: the account number and one one-way (SHA-256) hash of a deleted account&rsquo;s email address, username and creation time, and nothing else
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  So that restoring an older backup cannot bring a deleted account back (with one exception, described under Backups and deletion)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Indefinitely, in a separate file outside the database; an encrypted copy is uploaded with each off-site backup
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          Information we use but do not store:
          <br />
          - Your IP address. Held in memory only, to rate-limit requests. Our server also sends it to Cloudflare to verify the human check on the sign-up and forgot-password pages. It is not written to our logs or database.
          <br />
          - The name you type at sign-in. Held in memory for up to 24 hours to lock a name that is being guessed at.
          <br />
          - Your browser&rsquo;s user agent. Read to block AI crawlers; not stored.
        </p>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          Logs rotate at 20 MB with 3 old files kept, and a daily job compresses and rotates any log over 10 MB, keeping 3. The limit for a log file is 30 days after its last entry (see the status of these limits under Backups and deletion). Because logs rotate by size, not by date, one log file on a quiet site can cover several months, so an entry can stay in the logs for more than 30 days. Retention limits are listed under Backups and deletion.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Stored in your browser">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Stored in your browser</h2>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>Cookies: the two described under Cookies.</li>
          <li>Local storage: display preferences, and on-device usability counters (for example, repeated clicks on something that does not respond). These never leave your browser.</li>
        </ul>
      </section>
      <section className="panel px-5 py-4" aria-label="What we do not do">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">What we do not do</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          We do not run analytics. We do not show ads or use advertising trackers. We do not sell personal information or share it for anyone&rsquo;s marketing. We do not track you across other websites ourselves, so a Do Not Track signal changes nothing about what we do. Pages that embed another company&rsquo;s content (see Who else handles your data) let that company receive your browser&rsquo;s request, and possibly track you, under its own policy.
        </p>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          We make no automated decisions about you with legal or similarly significant effects. Grading your calls is automatic, but it only scores your own journal.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Cookies">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Cookies</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          We set two cookies. Both are needed for features you ask for, and neither tracks you.
        </p>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>
            <code className="mono">signaldeck_session</code> (HttpOnly, SameSite=Lax, Secure over HTTPS) keeps you signed in for up to 30 days.
          </li>
          <li>
            <code className="mono">signaldeck_device</code> (HttpOnly, SameSite=Lax, Secure over HTTPS, limited to sign-in requests, 180 days) holds a keyed hash of your username and is renewed at each password sign-in. It lets a browser that signed in before keep signing in while failed guesses have locked the name, with its own rate-limited allowance. It never grants access by itself.
          </li>
        </ul>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          Embedded third-party content may set its own cookies.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Who else handles your data">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Who else handles your data</h2>
        <div className="overflow-x-auto">
          <table className="w-full text-[0.8rem] border-collapse" style={{ minWidth: 0, tableLayout: "fixed" }}>
            <thead>
              <tr>
                <th className="border-t px-2 py-1.5 align-top text-left font-semibold" style={{ borderColor: "var(--border)" }}>
                  Party
                </th>
                <th className="border-t px-2 py-1.5 align-top text-left font-semibold" style={{ borderColor: "var(--border)" }}>
                  What they receive
                </th>
                <th className="border-t px-2 py-1.5 align-top text-left font-semibold" style={{ borderColor: "var(--border)" }}>
                  Why
                </th>
                <th className="border-t px-2 py-1.5 align-top text-left font-semibold" style={{ borderColor: "var(--border)" }}>
                  When
                </th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Google (Gmail)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Your email address and the mail we send you; that mail can include your username, and the digest lists forecasts for the symbols you watch
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To send account mail and the digest (the digest is paused for member accounts)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Always
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Cloudflare
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  All traffic to the site, including your IP address; encrypted connections end at Cloudflare
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  To put the site on the internet, and the human check on sign-up and forgot-password
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Always
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  GitHub (a private repository)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  A compressed copy of the whole database, made each weekday after the US market close, including every database row above, password hashes and the waitlist (not the logs). Since 22 October 2026 each copy is encrypted (AES-256-GCM) on the operator&rsquo;s computer before upload, with a key that is kept on the operator&rsquo;s computer, separate from the database, and is never uploaded with the backups. Copies made before that date were not encrypted; they leave the rotation as newer copies replace them. An encrypted copy of the deletion record (see Backups and deletion) is uploaded with each copy
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Off-site backup; the 7 newest are kept (about a week and a half)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Always
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  TradingView
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Your browser loads its chart widget; TradingView receives your IP address, browser details and the symbol you view, and may set its own cookies. Separately, the operator sends alerts from his own TradingView account to SignalDeck by webhook; nothing about you is sent to TradingView for this
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Price charts, so SignalDeck shows no price data itself; the operator&rsquo;s alerts are stored for the record but are no longer used as a model input since 22 October 2026 (licence review)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Chart widget: member symbol pages only
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Telegram
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Your chat id and the digest text
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Delivers the digest, if you link Telegram (paused for member accounts)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Not switched on today
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Google Sign-In
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Loads on the sign-in and sign-up pages; Google sees the visit and, if you use it, your Google account
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Sign in with Google
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Not switched on today
                </td>
              </tr>
              <tr>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  NVIDIA (AI model API)
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  The question you type into &ldquo;Ask the data&rdquo;, and the summary of your own journal used to answer it
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  AI answers
                </td>
                <td className="border-t px-2 py-1.5 align-top" style={{ borderColor: "var(--border)" }}>
                  Switched off for everyone as of the 23 October 2026 release, together with the AI news-sentiment tagger, so no question and no headline is sent to NVIDIA
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
      <section className="panel px-5 py-4" aria-label="Data sources">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Data sources</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          As of 23 October 2026: SignalDeck&rsquo;s prices, forecasts and statistics are derived from Alpaca market data, which is licensed for personal use only, so they are withheld from everyone except the operator, signed-in members included, until Alpaca gives written permission. Collection of the congressional trading rows compiled by Kadoa has stopped, and the stored rows are shown only to the operator. The NVIDIA-hosted AI features and the AI news-sentiment tagger are switched off as of this release, so headlines are no longer sent to NVIDIA. None of this changes what we hold about you. The{" "}
          <Link href="/terms#data-sources" style={{ color: "var(--dim)", textDecoration: "underline" }}>
            Terms of Use
          </Link>{" "}
          give the detail.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Backups and deletion">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Backups and deletion</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          On the operator&rsquo;s computer, the newest backup is kept uncompressed (at most two), and the five newest compressed backups are kept; older compressed backups are deleted. Uncompressed copies beyond the two newest move to a local holding folder, where the limit is 30 days. Local backup copies are not encrypted.
        </p>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          Off-site, one compressed copy of the database is uploaded each weekday to a private GitHub repository, and the 7 newest copies are kept (about a week and a half). Since 22 October 2026 each copy is encrypted (AES-256-GCM) on the operator&rsquo;s computer before upload, with a key that is kept on the operator&rsquo;s computer, separate from the database, and is never uploaded with the backups. Copies made before 22 October 2026 were not encrypted; they leave the rotation as newer copies replace them.
        </p>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          Retention limits, set on 22 October 2026: a log file 30 days after its last entry; the local holding folder 30 days; waitlist entries 12 months, or sooner if you ask. A daily automated job checks these limits. Until automatic deletion is switched on, it lists anything past its limit and the operator removes it by hand, so logs and backup copies older than these limits may still exist for a short time. Older full copies of the database, made before these limits were set, also still exist on the operator&rsquo;s computer (in the local holding folder, in an inactive local cloud-sync folder, and a few one-off copies made during maintenance); the operator is deleting them by hand.
        </p>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          When you delete your account, its data is erased from the live database at once. It can remain in logs until the log file holding it is deleted (limit: 30 days after that file&rsquo;s last entry), and in backups until they rotate out (off-site about a week and a half; local holding folder limit 30 days), subject to the note on retention limits above.
        </p>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          When an account is deleted, SignalDeck records the account number and one one-way (SHA-256) hash of the account&rsquo;s email address, username and creation time, and nothing else, in a separate file outside the database, so that restoring an older backup cannot bring the account back and a later sign-up is never mistaken for it. This record is kept indefinitely. An encrypted copy of it is uploaded with each off-site backup. Because the record itself is backed up with each off-site copy, an account deleted after the newest off-site copy was made could come back if the operator&rsquo;s computer were lost before the next weekday backup.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Your choices">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Your choices</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          On your Account page you can:
        </p>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>Download your data: a JSON file of your account record, watchlist, calls, alert settings and digest delivery records, without the password hash or any token. The waitlist and server logs are not included.</li>
          <li>Delete your account, confirmed with your password. Your tokens, sessions, watchlist, calls, alert settings and account are erased in one step. If you signed up with Google, first set a password with &ldquo;Forgot password&rdquo;. This does not remove a waitlist entry for the same address; ask us to.</li>
          <li>Turn the digest off, or use the unsubscribe link in any digest.</li>
        </ul>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          To correct your email address or username, remove a waitlist entry, or ask anything else about your data, email <a href={`mailto:${CONTACT_EMAIL}`} style={{ color: "var(--dim)", textDecoration: "underline" }}>{CONTACT_EMAIL}</a>.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Security">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Security</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          Passwords and tokens are stored only as one-way hashes. The site is served over HTTPS. Off-site backups made since 22 October 2026 are encrypted (AES-256-GCM) before upload; older off-site copies and the local backup copies are not encrypted. Encryption can reduce breach risk and may affect notification obligations; each incident requires assessment under applicable law. If a breach affects your personal information, we will notify you as the law requires.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Children">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Children</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          SignalDeck is not intended for children. If we learn an account belongs to a child under 13, we will delete it. Tell us at <a href={`mailto:${CONTACT_EMAIL}`} style={{ color: "var(--dim)", textDecoration: "underline" }}>{CONTACT_EMAIL}</a>.
        </p>
      </section>
      <section className="panel px-5 py-4" aria-label="Changes">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Changes</h2>
        <p className="text-[0.85rem] leading-relaxed max-w-prose" style={{ color: "var(--dim)" }}>
          We will post changes here and update the date at the top.
        </p>
      </section>
    </div>
  );
}
