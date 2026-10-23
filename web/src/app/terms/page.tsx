import type { Metadata } from "next";
import Link from "next/link";
import { OPERATOR_NAME, CONTACT_EMAIL, LEGAL_UPDATED } from "@/lib/legal";

export const metadata: Metadata = { title: "Terms of Use" };

export default function TermsPage() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">
        Terms of Use
      </h1>

      <div
        role="note"
        className="panel px-4 py-3 text-[0.8rem]"
        style={{ color: "var(--dim)" }}
      >
        Draft, pending legal review. Last updated {LEGAL_UPDATED}. Sections on
        liability, governing law and disputes will be added after review.
        Questions:{' '}
        <a
          href={`mailto:${CONTACT_EMAIL}`}
          style={{ color: "var(--dim)", textDecoration: "underline" }}
        >
          {CONTACT_EMAIL}
        </a>
        .
      </div>

      <section className="panel px-5 py-4" aria-label="agreement">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Agreement
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          These Terms are an agreement between {OPERATOR_NAME} and each person
          who holds an account on SignalDeck (signaldeck.nicholasnyaung.com).
          They apply to account holders, who accept them when they create an
          account. Our{' '}
          <Link href="/privacy" style={{ color: "var(--dim)", textDecoration: "underline" }}>
            Privacy Notice
          </Link>{' '}
          explains how we handle personal information.
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          If you browse without an account, you are not asked to accept
          anything. The public pages are provided as-is, for information.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="what signaldeck is and is not">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          What SignalDeck is and is not
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          SignalDeck makes statistical market forecasts and grades them against
          rules registered before the results were known. The rules and a
          hash-chained public record of every published directional forecast
          stay public. Since 23 October 2026 the forecasts, grades and other
          figures themselves are withheld from everyone except the operator,
          for the reason given under Data sources below.
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          SignalDeck is not financial, investment, tax or legal advice.
        </p>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>
            The forecasts are the same for every reader, published on a regular
            schedule, and not tailored to anyone&rsquo;s finances or objectives.
          </li>
          <li>
            Using SignalDeck does not make us your adviser.
          </li>
          <li>
            We are not a broker, and SignalDeck does not place or route trades.
          </li>
        </ul>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          About the accuracy figures:
        </p>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>
            Backtested and simulated results are hypothetical, made with
            hindsight, and limited for that reason.
          </li>
          <li>
            Live graded results are real, but they cover a limited period and do
            not predict future results.
          </li>
          <li>
            A forecast can be retired by its published rule. Published accuracy
            figures can be withheld if we find a flaw in how they were measured.
          </li>
        </ul>
      </section>

      <section className="panel px-5 py-4" aria-label="accounts">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Accounts
        </h2>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>
            You may hold one free account per Gmail address. You are responsible
            for activity on your account.
          </li>
          <li>
            If you sign up with email and password, keep the password safe.
          </li>
          <li>
            We may close an account that breaks these Terms. You can download
            your data and delete your account at any time from the Account page.
          </li>
        </ul>
      </section>

      <section className="panel px-5 py-4" aria-label="your content">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Your content
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          Your watchlist, calls and notes are yours. They are private to your
          account and never shown to other users. You give us only the
          permission we need to store, grade and show them back to you. If
          &ldquo;Ask the data&rdquo; is turned on and you use it, that includes
          sending your question and a summary of your journal to our AI provider
          to answer you (see the Privacy Notice).
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="acceptable use">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Acceptable use
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          Do not:
        </p>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>
            scrape or load the service in a way that burdens it;
          </li>
          <li>
            try to get around rate limits, human checks or security controls;
          </li>
          <li>
            copy, resell or redistribute SignalDeck&rsquo;s content, forecasts
            or derived figures, except as allowed under Intellectual property.
          </li>
        </ul>
      </section>

      <section className="panel px-5 py-4" aria-label="ai answers">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          AI answers
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          SignalDeck&rsquo;s AI features, including &ldquo;Ask the data&rdquo;,
          ran on a model hosted by NVIDIA. As of the 23 October 2026 release
          they are switched off for everyone, and so is the AI tagger that
          marked news headlines as positive or negative: headlines are no
          longer sent to NVIDIA. If an AI feature is ever switched on again, it
          gives general information about the data on the site, not
          personalised advice. Its answers are generated automatically and may
          be wrong.
        </p>
      </section>

      <section id="data-sources" className="panel scroll-mt-20 px-5 py-4" aria-label="data sources and third-party services">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Data sources and third-party services
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          SignalDeck&rsquo;s figures are computed from third-party market and
          public data: Alpaca Markets (US stock market data and news), SEC
          EDGAR (filings, insider Form 4 reports, 13F holdings and company
          facts), the CFTC (Commitments of Traders), Wikimedia (Wikipedia
          page-view counts, CC0) and congressional trade disclosures.
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          What is shown, as of 23 October 2026. Alpaca licenses its market data
          and news for personal, non-commercial use and does not allow them, or
          information derived from them, to be displayed publicly or passed on
          without its written permission. SignalDeck&rsquo;s prices, forecasts,
          accuracy figures and other statistics are derived from that data, so
          they are withheld from everyone except the operator until Alpaca
          gives written permission. Signing in does not change this: member
          accounts see the same notice as visitors. The method, the
          pre-registered rules and the hash-chained record of claims stay
          public. Public-domain data, such as SEC EDGAR filings, is not covered
          by Alpaca&rsquo;s licence.
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          Congressional trades are disclosures made public under the STOCK
          Act. SignalDeck obtained them through third-party datasets:
          Kadoa&rsquo;s congress-trades file, and for older rows the House and
          Senate Stock Watcher datasets. Kadoa&rsquo;s terms do not permit
          automated collection, or making its compilation available to others,
          without written permission. As of 23 October 2026 collection from
          Kadoa has stopped, and the rows already stored are shown only to the
          operator. SignalDeck is free and non-commercial: no ads, no fees and
          no sale of data.
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          TradingView: member symbol pages embed TradingView&rsquo;s chart
          widget, which your browser loads from TradingView. Alerts the
          operator sends from his own TradingView account to SignalDeck by
          webhook are stored for the record but are no longer used as a model
          input since 22 October 2026 (licence review).
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          No longer collected or used for new forecasts:
        </p>
        <ul className="list-disc pl-5 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <li>the TradingView scanner and StockTwits (retired 21&ndash;22 October 2026);</li>
          <li>Kadoa&rsquo;s congress-trades file (collection stopped 23 October 2026; the stored rows are withheld from everyone except the operator);</li>
          <li>the NVIDIA-hosted AI features and the AI news-sentiment tagger (switched off as of the 23 October 2026 release; headlines are no longer sent to NVIDIA);</li>
          <li>crypto market data from Kraken, Coinbase and Hyperliquid (crypto paused since 22 October 2026; crypto history stays on the public record; open crypto direction forecasts were voided, never deleted; open crypto regime calls are marked ungradable when they fall due; nothing was deleted);</li>
          <li>FINRA short-sale data, Cboe put/call statistics and FRED economic data (retired 22 October 2026 after a data-licence review). Rows already stored from FINRA short-sale data and FRED still feed two pre-registered research tests until about 27 October 2026, when those tests&rsquo; 5-day freshness gates lapse; after that they are not used at all.</li>
        </ul>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          Forecasts made before these dates used data from these sources.
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          Embedded services (TradingView charts and, when switched on, Google
          Sign-In and Telegram) are provided under their own terms.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="availability">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Availability
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          The service runs on a single machine and may be unavailable at times.
          We may change or stop any part of it.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="intellectual property">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Intellectual property
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          SignalDeck and its original content belong to {OPERATOR_NAME}. You may
          view and use it for your own personal, non-commercial purposes. Any
          other copying, redistribution or derivative work needs our permission.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="disclaimers">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Disclaimers
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          The service is provided &ldquo;as is&rdquo; and &ldquo;as
          available&rdquo;, without warranties of any kind, express or implied,
          including merchantability, fitness for a particular purpose and
          non-infringement. We do not warrant that it will be uninterrupted,
          error-free or secure.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="ending">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Ending
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          You can stop using SignalDeck and delete your account at any time. We
          may close an account that breaks these Terms.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="changes">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Changes
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          We will post changes here and update the date at the top. For a
          material change, we will tell members by email before it takes effect.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="contact">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Contact
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          <a
            href={`mailto:${CONTACT_EMAIL}`}
            style={{ color: "var(--dim)", textDecoration: "underline" }}
          >
            {CONTACT_EMAIL}
          </a>
        </p>
      </section>
    </div>
  );
}
