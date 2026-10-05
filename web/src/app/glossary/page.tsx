import type { Metadata } from "next";
import Link from "next/link";
import { type MetricKey } from "@/lib/plain";
import GlossaryList from "@/components/GlossaryList";
import { STATUS_GLOSS } from "@/components/accuracy/AccuracyStatusBanner";

// Bare page name — the root layout appends " — SignalDeck" via its template.
export const metadata: Metadata = { title: "Glossary" };

// The glossary is rendered from the same PLAIN dictionary that feeds every
// tooltip on the site.  This page cannot drift from those definitions because
// both are built from PLAIN[key].read(null) at render time.
const ORDER: readonly MetricKey[] = [
  "cal_prob",
  "regime",
  "vol_regime",
  "pressure",
  "vix",
  "zscore",
  "win_rate",
  "base_rate",
  "lift",
  "ic",
  "brier",
  "reliability",
  "quintile_spread",
  "sample_gate",
];

export default function GlossaryPage() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">
        Glossary
      </h1>
      <p
        className="text-[0.85rem] leading-relaxed max-w-prose"
        style={{ color: "var(--dim)" }}
      >
        Every number SignalDeck shows you, in plain English first and the
        precise definition underneath. If a term on any page is unclear, it is
        defined here.
      </p>

      {/* The list holds filter state, so it is a client component. The
          definitions still come from PLAIN, so they cannot drift from the
          tooltips. */}
      <GlossaryList order={ORDER} />

      {/* Status words (2026-10-05 audit): three pages said every term was here,
          and none of the verdict words were. Registry statuses come from the
          same map the banner uses; the last three are the words other pages
          print beside a figure. */}
      <section className="panel px-5 py-4" aria-label="status words">
        <h2 className="mb-2 text-[0.8rem] font-extrabold tracking-[0.14em]">Status words</h2>
        <dl className="grid gap-x-4 gap-y-1 text-[0.8rem] sm:grid-cols-[max-content_1fr]">
          {[
            ...Object.entries(STATUS_GLOSS),
            ["WITHHELD", "a figure exists but is deliberately not shown; the page says why (for example the SD-30 label leak on 1d and 1w direction calls)"],
            ["PENDING", "a backtested claim whose live record has not begun; it becomes evidence on its first-grade date, never before"],
            ["ACCRUING", "a live record still collecting toward its pre-registered minimum; no verdict either way yet"],
          ].map(([word, meaning]) => (
            <div key={word} className="contents">
              <dt className="mono font-semibold">{word.replace(/_/g, " ")}</dt>
              <dd className="m-0" style={{ color: "var(--dim)" }}>{meaning}</dd>
            </div>
          ))}
        </dl>
      </section>

      <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        These definitions are generated from the same dictionary the tooltips
        use, so they cannot drift apart. Still stuck on something?{" "}
        <Link href="/health" style={{ color: "var(--dim)", textDecoration: "underline" }}>
          Tell us &mdash; we grade how understandable this is.
        </Link>
      </p>
    </div>
  );
}