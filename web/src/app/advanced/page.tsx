// Advanced hub intro page. SignalDeck has 55 pages, and the INTEL + LAB hubs
// (35 surfaces) were hidden behind a single nav entry. New users should not
// meet that much raw surface on day one, so this labelled door routes them to
// the right section without pretending anything behind it is exclusive or
// dangerous. Server component: static JSX + links only.

import Link from "next/link";
import type { Metadata } from "next";

// Bare page name — the root layout appends " — SignalDeck" via its template.
export const metadata: Metadata = {
  title: "Advanced tools",
};

export default function AdvancedPage() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">
        Advanced tools
      </h1>

      <p
        className="max-w-prose text-[0.85rem] leading-relaxed"
        style={{ color: "var(--dim)" }}
      >
        Research tools, backtests, and the experimental model lab. Everything
        here is labelled with how well it actually works — some of it is good,
        some of it is not good yet, and we would rather show you which is which
        than hide the difference.
      </p>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Link href="/lab/track-record" className="block">
          <div className="panel flex flex-col gap-1 p-4 transition-colors duration-150 hover:border-[var(--border-strong)]">
            <span
              className="text-[0.7rem] font-bold tracking-[0.14em]"
              style={{ color: "var(--accent)" }}
            >
              START HERE
            </span>
            <span className="font-bold">Our scorecard</span>
            <span
              className="text-[0.85rem] leading-relaxed"
              style={{ color: "var(--dim)" }}
            >
              How right have we actually been? Every call, graded against what
              happened.
            </span>
          </div>
        </Link>

        <Link href="/lab/backtest" className="block">
          <div className="panel flex flex-col gap-1 p-4 transition-colors duration-150 hover:border-[var(--border-strong)]">
            <span
              className="text-[0.7rem] font-bold tracking-[0.14em]"
              style={{ color: "var(--accent)" }}
            >
              TEST AN IDEA
            </span>
            <span className="font-bold">Backtests</span>
            <span
              className="text-[0.85rem] leading-relaxed"
              style={{ color: "var(--dim)" }}
            >
              Run a strategy against real history and see what it would have
              done.
            </span>
          </div>
        </Link>

        <Link href="/lab/portfolio" className="block">
          <div className="panel flex flex-col gap-1 p-4 transition-colors duration-150 hover:border-[var(--border-strong)]">
            <span
              className="text-[0.7rem] font-bold tracking-[0.14em]"
              style={{ color: "var(--accent)" }}
            >
              YOUR MONEY
            </span>
            <span className="font-bold">Portfolio tools</span>
            <span
              className="text-[0.85rem] leading-relaxed"
              style={{ color: "var(--dim)" }}
            >
              Risk, suggested weights, paper trading, and what-if scenarios.
            </span>
          </div>
        </Link>

        <Link href="/intel/news" className="block">
          <div className="panel flex flex-col gap-1 p-4 transition-colors duration-150 hover:border-[var(--border-strong)]">
            <span
              className="text-[0.7rem] font-bold tracking-[0.14em]"
              style={{ color: "var(--accent)" }}
            >
              COMPANY DIGGING
            </span>
            <span className="font-bold">Company news and filings</span>
            <span
              className="text-[0.85rem] leading-relaxed"
              style={{ color: "var(--dim)" }}
            >
              News, SEC filings, insider buying, and what the big funds are
              holding.
            </span>
          </div>
        </Link>
      </div>

      <p
        className="text-[0.85rem] leading-relaxed"
        style={{ color: "var(--dim)" }}
      >
        Looking for something specific?{" "}
        <Link
          href="/lab/research"
          className="hover:underline"
          style={{ color: "var(--accent)" }}
        >
          Research lab
        </Link>
        {" · "}
        <Link
          href="/lab/system"
          className="hover:underline"
          style={{ color: "var(--accent)" }}
        >
          System health
        </Link>
        {" · "}
        <Link
          href="/lab/system/quality"
          className="hover:underline"
          style={{ color: "var(--accent)" }}
        >
          Data quality
        </Link>
      </p>

      <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        Nothing here is financial advice. Experimental surfaces say so on the
        page itself.
      </p>
    </div>
  );
}