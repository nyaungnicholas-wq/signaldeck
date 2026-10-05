"use client";

// METHODOLOGY — the collapsible published rulebook for the alert kinds
// actually present in the feed. Every kind cites its NAMED rule with the
// exact daemon thresholds (rules.ts mirrors the Go constants verbatim), so
// "BREAKOUT" is never an unexplained colored word. Starts collapsed in both
// view modes (it's reference material); unknown kinds get the honest
// "unpublished rule" entry — listed, never hidden.

import { useState } from "react";
import Link from "next/link";
import { ruleFor, sortKinds } from "./rules";

export default function MethodologyPanel({ kinds }: { kinds: string[] }) {
  const [open, setOpen] = useState(false);
  const sorted = sortKinds(kinds);

  return (
    <section className="panel">
      <div className="panel-h">
        METHODOLOGY — THE NAMED RULES
        <span className="chip tnum px-2 py-[2px] text-[0.75rem]">
          {sorted.length} kind{sorted.length === 1 ? "" : "s"} in this feed
        </span>
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
          className="chip ml-auto min-h-[36px] cursor-pointer px-3 text-[0.75rem] transition-colors duration-150 hover:bg-[var(--panel3)] hover:text-[var(--text)]"
          style={open ? undefined : { color: "var(--accent)", borderColor: "var(--accent)" }}
        >
          {open ? "hide the rules" : "show the exact rules ↓"}
        </button>
      </div>

      {open ? (
        <div className="flex flex-col gap-3 px-4 py-3">
          <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            Every alert kind fires on a published rule with fixed thresholds — nothing
            discretionary. Prediction thresholds are configurable via SIGNALDECK_ALERT_HI /
            SIGNALDECK_ALERT_LO (defaults 0.65 / 0.35); anomaly z via SIGNALDECK_ANOM_Z
            (default 2.5). The values below are the running defaults.
          </p>
          {sorted.map((kind) => {
            const r = ruleFor(kind);
            return (
              <div key={kind} className="flex flex-col gap-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    className="chip shrink-0 px-2 py-[2px] text-[0.75rem] tracking-wider"
                    style={{ color: r.color, borderColor: r.color }}
                  >
                    {r.label}
                  </span>
                  <span className="text-[0.75rem]" style={{ color: "var(--text)" }}>
                    {r.rule}
                  </span>
                  {!r.known && (
                    <span className="chip px-2 py-[2px] text-[0.75rem]" style={{ color: "var(--warn)", borderColor: "var(--warn)" }}>
                      unpublished rule
                    </span>
                  )}
                  {r.href && (
                    <Link
                      href={r.href}
                      className="cursor-pointer text-[0.75rem] underline decoration-dotted underline-offset-2 transition-colors duration-150 hover:decoration-solid"
                      style={{ color: "var(--accent)" }}
                    >
                      {r.hrefLabel ?? r.href}
                    </Link>
                  )}
                </div>
                <ul className="m-0 flex list-none flex-col gap-0.5 pl-3">
                  {r.thresholds.map((t, i) => (
                    <li
                      key={i}
                      className="text-[0.75rem] leading-relaxed"
                      style={{ color: "var(--dim)" }}
                    >
                      · {t}
                    </li>
                  ))}
                </ul>
              </div>
            );
          })}
        </div>
      ) : (
        <p className="m-0 px-4 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          each alert kind fires on a published rule with exact thresholds — folded, never hidden.
        </p>
      )}
    </section>
  );
}
