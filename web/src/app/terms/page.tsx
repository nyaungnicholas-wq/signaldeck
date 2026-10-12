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
          These Terms are an agreement between you and {OPERATOR_NAME} about your
          use of SignalDeck (signaldeck.nicholasnyaung.com). Our{' '}
          <Link href="/privacy" style={{ color: "var(--dim)", textDecoration: "underline" }}>
            Privacy Notice
          </Link>{' '}
          explains how we handle personal information. You accept these Terms
          when you create an account.
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
          SignalDeck publishes statistical market forecasts and grades them in
          public against rules registered before the results were known. We keep
          a hash-chained public record of every published directional forecast.
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
            The watchlist, journal grading and &ldquo;Ask the data&rdquo; work
            only on what you enter yourself; they are not advice about your
            finances.
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
          Answers from &ldquo;Ask the data&rdquo; (when available) are generated
          automatically. They may be wrong and are not advice.
        </p>
      </section>

      <section className="panel px-5 py-4" aria-label="data sources and third-party services">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">
          Data sources and third-party services
        </h2>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          SignalDeck&rsquo;s figures are computed from third-party market and
          public data, including Alpaca Markets, Kraken, Coinbase, Hyperliquid,
          SEC EDGAR, FINRA, the CFTC, Cboe, STOCK Act disclosures, Wikimedia
          and the Federal Reserve Bank of St. Louis (FRED).
        </p>
        <p
          className="text-[0.85rem] leading-relaxed max-w-prose"
          style={{ color: "var(--dim)" }}
        >
          This product uses the FRED&reg; API but is not endorsed or certified
          by the Federal Reserve Bank of St. Louis.{' '}
          <a
            href="https://fred.stlouisfed.org/docs/api/terms_of_use.html"
            rel="noopener noreferrer"
            target="_blank"
            style={{ color: "var(--dim)", textDecoration: "underline" }}
          >
            FRED&reg; API Terms of Use
          </a>
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
