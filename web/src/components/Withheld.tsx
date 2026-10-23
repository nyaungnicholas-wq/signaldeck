import Link from "next/link";
import type { ReactNode } from "react";
import { WITHHELD_HREF } from "@/lib/withheld";

/**
 * The licence withholding (HTTP 451, lib/withheld), in place of the data it
 * withholds. One component for every page, so the explanation cannot drift.
 *
 * It is a status, not an alert: nothing failed, the daemon answered, and the
 * answer will be the same on every retry. So no Retry button, no "on our side",
 * no outage wording. It says what is withheld and why (the daemon's notice),
 * that the system keeps running privately, and where the full reason lives.
 * Server- and client-safe: no hooks, no browser APIs.
 */
export default function Withheld({
  notice,
  source = "unknown",
  title,
  compact = false,
  className = "",
  children,
}: {
  notice: string;
  source?: string;
  /** What this panel would have shown, e.g. "Live track record". */
  title?: string;
  /** A card inside a page that already explains itself. */
  compact?: boolean;
  className?: string;
  children?: ReactNode;
}) {
  return (
    <section
      role="status"
      aria-label={title ? `${title}: withheld` : "Withheld under a data licence"}
      data-testid="withheld"
      data-source={source}
      className={`flex min-w-0 flex-col gap-2 rounded-lg border ${compact ? "px-3 py-2" : "px-4 py-3"} ${className}`}
      style={{
        borderColor: "color-mix(in srgb, var(--warn) 55%, transparent)",
        backgroundColor: "color-mix(in srgb, var(--warn) 8%, transparent)",
        color: "var(--text)",
      }}
    >
      <div className="mono text-[0.7rem] uppercase tracking-[0.15em]" style={{ color: "var(--warn)" }}>
        {title ? `${title} · withheld` : "Withheld"} · data licence
      </div>
      <p className={`m-0 max-w-[70ch] break-words leading-relaxed ${compact ? "text-[0.8rem]" : "text-[0.9rem]"}`}>
        {notice}
      </p>
      {!compact && (
        <p className="m-0 max-w-[70ch] text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          This is a licensing decision, not an outage, so retrying will not change it. SignalDeck keeps
          running privately: the operator&rsquo;s copy still forecasts and grades itself, and the
          hash-chained receipts stay public.
        </p>
      )}
      <Link
        href={WITHHELD_HREF}
        className="w-fit text-[0.78rem] underline underline-offset-2"
        style={{ color: "var(--accent)" }}
      >
        Why this is withheld
      </Link>
      {children}
    </section>
  );
}
