"use client";

/**
 * GLOSSARY LIST — the definitions, with a filter.
 *
 * Split out of the /glossary server component solely so the filter can hold
 * state. The definitions still come from PLAIN via `read(null).detail`, the
 * same dictionary that feeds every tooltip on the site, so this list cannot
 * drift from them.
 *
 * The filter is the point. A glossary is the one page a person arrives at
 * already knowing what they want, and scrolling a wall of terms to find it is
 * the worst possible way to answer a question you can already name.
 */

import type { ReactElement } from "react";
import { useMemo, useState } from "react";
import { PLAIN, type MetricKey } from "@/lib/plain";

export default function GlossaryList({
  order,
}: {
  order: readonly MetricKey[];
}): ReactElement {
  const [query, setQuery] = useState("");

  // Precompute the searchable text once. Matching covers both labels and the
  // definition body: people search for the words they read in the definition
  // at least as often as for the term itself.
  const entries = useMemo(
    () =>
      order.map((key) => {
        const def = PLAIN[key];
        const detail = def.read(null).detail;
        return {
          key,
          simple: def.label.simple,
          pro: def.label.pro,
          detail,
          haystack: `${def.label.simple} ${def.label.pro} ${detail}`.toLowerCase(),
        };
      }),
    [order],
  );

  const needle = query.trim().toLowerCase();
  const shown = needle === "" ? entries : entries.filter((e) => e.haystack.includes(needle));

  return (
    <>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <label className="flex min-h-[40px] flex-1 items-center gap-2 text-[0.8rem]">
          <span style={{ color: "var(--faint)" }}>Find a term</span>
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="volatility, breadth, hit rate…"
            className="chip min-h-[40px] min-w-0 flex-1 px-3"
            style={{ color: "var(--text)", background: "transparent" }}
          />
        </label>
        {/* Announced, because the list changing under you is exactly the kind
            of update a screen reader is otherwise never told about. */}
        <span className="tnum text-[0.75rem]" role="status" style={{ color: "var(--faint)" }}>
          {needle === ""
            ? `${entries.length} terms`
            : `${shown.length} of ${entries.length} terms`}
        </span>
      </div>

      {shown.length === 0 ? (
        <div data-empty="" className="panel px-4 py-6 text-[0.8rem]" style={{ color: "var(--dim)" }}>
          Nothing here matches &ldquo;{query.trim()}&rdquo;.
          <div className="mt-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            This glossary only covers terms SignalDeck actually shows you. If you saw the word on a
            page here, it should be in this list &mdash; tell us if it is not.
          </div>
          <button
            type="button"
            onClick={() => setQuery("")}
            className="mt-3 inline-flex min-h-[40px] cursor-pointer items-center px-3"
            style={{ color: "var(--accent)" }}
          >
            Show all terms
          </button>
        </div>
      ) : (
        <dl className="panel flex flex-col">
          {shown.map((e, i) => {
            const showProLabel = e.simple !== e.pro;
            return (
              <div
                key={e.key}
                className={`px-4 py-3 sm:px-5 ${i > 0 ? "border-t" : ""}`}
                style={{ borderColor: "var(--border)" }}
              >
                <dt
                  className="flex items-baseline gap-2 text-[0.85rem] font-bold"
                  style={{ color: "var(--text)" }}
                >
                  <span>{e.simple}</span>
                  {showProLabel && (
                    <span className="chip mono text-[0.75rem]" style={{ color: "var(--faint)" }}>
                      {e.pro}
                    </span>
                  )}
                </dt>
                <dd className="m-0 text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                  {e.detail}
                </dd>
              </div>
            );
          })}
        </dl>
      )}
    </>
  );
}
