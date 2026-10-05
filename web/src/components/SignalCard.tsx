"use client";

// <SignalCard> — the uniform insight card. Every signal reads the same way:
// what changed → why it matters → how much to trust it. The verb chip carries
// the app's action language (monitor / investigate / compare) and the footer
// meta row keeps confidence, freshness, and evidence together so a claim is
// never separated from its receipts.

import Link from "next/link";
import { ago } from "@/lib/format";

export type SignalVerb = "monitor" | "investigate" | "compare";

const VERB_COLOR: Record<SignalVerb, string> = {
  monitor: "var(--dim)",
  investigate: "var(--accent)",
  compare: "var(--crossed)",
};

/** ago() expects unix seconds; freshness timestamps sometimes arrive as ms. */
function toSeconds(ts: number): number {
  return ts > 1e12 ? Math.floor(ts / 1000) : ts;
}

export default function SignalCard({
  title,
  whatChanged,
  whyItMatters,
  confidence,
  freshnessTs,
  evidenceHref,
  verb,
  children,
}: {
  title: string;
  whatChanged: string;
  whyItMatters: string;
  confidence?: { label: string; n?: number };
  freshnessTs?: number;
  evidenceHref?: string;
  verb?: SignalVerb;
  children?: React.ReactNode;
}) {
  // Keyed by identity, not index — which chips exist can change between
  // renders (e.g. freshness arriving late), and index keys would let React
  // mismatch DOM nodes across those renders.
  const meta: { key: string; node: React.ReactNode }[] = [];
  if (confidence) {
    meta.push({
      key: "confidence",
      node: (
        <span>
          {confidence.label}
          {confidence.n != null ? <span className="tnum"> (n={confidence.n})</span> : ""}
        </span>
      ),
    });
  }
  if (freshnessTs) {
    meta.push({
      key: "freshness",
      node: <span>updated <span className="tnum">{ago(toSeconds(freshnessTs))}</span></span>,
    });
  }
  if (evidenceHref) {
    meta.push({
      key: "evidence",
      node: (
        <Link
          href={evidenceHref}
          className="underline decoration-dotted underline-offset-2 transition-colors duration-150 hover:text-[var(--text)]"
          style={{ color: "var(--accent)" }}
        >
          see the evidence
        </Link>
      ),
    });
  }

  return (
    <article className="panel flex flex-col gap-2 px-4 py-3">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h3 className="m-0 min-w-0 text-[0.85rem] font-bold tracking-wide">{title}</h3>
        {verb && (
          <span
            className="chip ml-auto shrink-0 font-bold uppercase tracking-[0.14em]"
            style={{ color: VERB_COLOR[verb] }}
          >
            {verb}
          </span>
        )}
      </header>
      <p className="m-0 text-[0.75rem] leading-relaxed">
        <span
          className="mr-2 text-[0.75rem] font-bold uppercase tracking-[0.12em]"
          style={{ color: "var(--dim)" }}
        >
          what changed
        </span>
        {whatChanged}
      </p>
      <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
        <span className="mr-2 text-[0.75rem] font-bold uppercase tracking-[0.12em]">
          why it matters
        </span>
        {whyItMatters}
      </p>
      {children}
      {meta.length > 0 && (
        <footer
          className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 border-t pt-2 text-[0.75rem]"
          style={{ borderColor: "var(--border)", color: "var(--dim)" }}
        >
          {meta.map(({ key, node }, i) => (
            <span key={key} className="inline-flex items-center gap-x-2">
              {i > 0 && <span aria-hidden="true">·</span>}
              {node}
            </span>
          ))}
        </footer>
      )}
    </article>
  );
}
