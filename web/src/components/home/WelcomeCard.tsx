"use client";

// First-run welcome card — shows only when localStorage `sd-onboarded` is
// missing AND the caller has no tracked symbols yet. Dismiss (or visiting
// /welcome) is remembered. Hydration-safe: SSR/first paint render nothing,
// then the localStorage read decides — same pattern as PagePurpose.

import Link from "next/link";
import { useState, useSyncExternalStore } from "react";

const ONBOARDED_KEY = "sd-onboarded";

// localStorage flag read through useSyncExternalStore: the server snapshot
// says "onboarded" (card hidden), the client snapshot reads the real flag —
// hydration-safe without a setState-in-effect. The flag never changes from
// outside this card, so subscribe is a no-op.
const subscribeNoop = () => () => {};
function getOnboarded(): boolean {
  try {
    return window.localStorage.getItem(ONBOARDED_KEY) !== null;
  } catch {
    // storage unavailable → stay hidden (never block the dashboard)
    return true;
  }
}
const getOnboardedServer = () => true;

export default function WelcomeCard({ watchlistEmpty }: { watchlistEmpty: boolean }) {
  const onboarded = useSyncExternalStore(subscribeNoop, getOnboarded, getOnboardedServer);
  const [dismissed, setDismissed] = useState(false);

  if (onboarded || dismissed || !watchlistEmpty) return null;

  const dismiss = () => {
    try {
      window.localStorage.setItem(ONBOARDED_KEY, "1");
    } catch {
      // remembered for this render only
    }
    setDismissed(true);
  };

  return (
    <section
      className="panel"
      aria-label="welcome to SignalDeck"
      style={{ borderColor: "var(--accent)" }}
    >
      <div className="panel-h">
        <span style={{ color: "var(--accent)" }}>WELCOME TO SIGNALDECK</span>
        <button
          type="button"
          onClick={dismiss}
          aria-label="dismiss welcome card"
          className="ml-auto inline-flex min-h-[40px] min-w-[40px] cursor-pointer items-center justify-center text-[var(--faint)] transition-colors duration-150 hover:text-[var(--text)]"
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 14 14"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            aria-hidden="true"
          >
            <path d="M3 3l8 8M11 3l-8 8" />
          </svg>
        </button>
      </div>
      <div className="flex flex-col gap-2 px-4 py-3 sm:px-5">
        <p className="m-0 text-[0.85rem] leading-relaxed">
          SignalDeck records real market data, makes gated predictions, and grades itself in
          public. No hype: when there isn&apos;t enough evidence, it says &ldquo;no read
          yet&rdquo; — and nothing here is financial advice.
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Link
            href="/welcome"
            className="inline-flex min-h-[44px] cursor-pointer items-center gap-2 rounded-lg border px-4 text-sm font-bold tracking-wide transition-colors duration-150 hover:bg-[var(--accent-dim)]"
            style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
          >
            See how it works →
          </Link>
          <span className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
            then Monitor your first symbol from the watchlist below
          </span>
        </div>
      </div>
    </section>
  );
}
