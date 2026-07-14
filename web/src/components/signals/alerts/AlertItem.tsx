"use client";

// One alert row. The kind chip is a NAMED-RULE chip — its color/label come
// from the published rulebook (rules.ts) and its hover states the exact rule
// that fired it; the full thresholds live in the METHODOLOGY panel above the
// feed. Unknown kinds render generically (never hidden, never guessed at).

import Link from "next/link";
import { ago } from "@/lib/format";
import type { AlertRow } from "@/lib/api";
import { ruleFor } from "./rules";

export default function AlertItem({ a }: { a: AlertRow }) {
  const r = ruleFor(a.kind);
  return (
    <div
      className="panel flex min-h-[48px] flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-[0.75rem]"
      style={a.seen ? undefined : { borderColor: r.color }}
    >
      {!a.seen && (
        <span
          aria-label="unread"
          className="inline-block h-2 w-2 shrink-0 rounded-full"
          style={{ background: r.color }}
        />
      )}
      <span
        className="chip shrink-0 px-2 py-[2px] text-[0.75rem] tracking-wider"
        style={{ color: r.color, borderColor: r.color }}
        title={`rule: ${r.rule}`}
      >
        {r.label}
      </span>
      {a.symbol && a.market ? (
        <Link
          href={`/s/${a.market}/${encodeURIComponent(a.symbol)}`}
          className="mono flex min-h-[40px] cursor-pointer items-center font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
        >
          {a.symbol}
        </Link>
      ) : null}
      {a.horizon && <span className="chip px-2 py-[2px] text-[0.75rem]">{a.horizon}</span>}
      <span className="min-w-0 flex-1" style={{ color: "var(--dim)" }}>
        {a.detail}
      </span>
      <span className="tnum shrink-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {ago(a.ts)}
      </span>
    </div>
  );
}
