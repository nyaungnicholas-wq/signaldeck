"use client";

import Link from "next/link";

/**
 * The app-level error boundary.
 *
 * The copy here is for a VISITOR, not an operator. The shared ErrorState and
 * several pages used to tell whoever hit them to "start signaldeckd (:8322)",
 * which is a useful instruction to exactly one person on earth and reads as a
 * leak to everyone else. The operator detail stays in the daemon's JSON error
 * and in the logs; the rendered surface says what a reader can act on.
 */
export default function Error({ reset }: { error: Error; reset: () => void }) {
  return (
    <div className="flex flex-col gap-4 pt-10">
      <div
        className="mono text-[0.7rem] uppercase tracking-[0.2em]"
        style={{ color: "var(--bad)" }}
      >
        Something broke
      </div>
      <h1 className="m-0 text-[1.6rem] font-bold">This page could not be loaded.</h1>
      <p className="m-0 max-w-[56ch] text-sm leading-relaxed" style={{ color: "var(--dim)" }}>
        The problem is on our side, not yours. Nothing you did caused it and nothing
        was lost.
      </p>
      <div className="flex flex-wrap gap-3 pt-1">
        <button
          type="button"
          onClick={reset}
          className="chip cursor-pointer"
          style={{ borderColor: "var(--accent)", color: "var(--accent)", padding: "0.55rem 0.9rem" }}
        >
          Try again
        </button>
        <Link href="/" className="chip" style={{ padding: "0.55rem 0.9rem" }}>
          Front page
        </Link>
      </div>
    </div>
  );
}
