"use client";

// <Plain> — the render side of the translation layer (src/lib/plain.ts).
//
// SIMPLE mode (default): plain-English sentence + colored goodness dot, with
// the raw value small and dim underneath — jargon demoted, never deleted.
// PRO mode: raw value first, plain sentence as the subtitle.
//
// HONESTY: gated/null input renders "no read yet …" in BOTH modes — simple
// mode hides jargon, never caveats. The full technical detail() always rides
// on the title tooltip.

import { useEffect, useState } from "react";
import {
  goodnessColor,
  readMetric,
  type MetricKey,
  type PlainCtx,
} from "@/lib/plain";

export const VIEW_MODE_KEY = "sd-view-mode";
export const VIEW_MODE_EVENT = "sd-view-mode";
export type ViewMode = "simple" | "pro";

export function currentViewMode(): ViewMode {
  if (typeof window === "undefined") return "simple";
  return localStorage.getItem(VIEW_MODE_KEY) === "pro" ? "pro" : "simple";
}

/** Subscribe to the SIMPLE/PRO toggle (Shell header). SSR + first paint are
 *  always "simple" (the default), then localStorage takes over — the same
 *  hydration-safe pattern as the reading-mode toggle. */
export function useViewMode(): ViewMode {
  const [mode, setMode] = useState<ViewMode>("simple");
  useEffect(() => {
    const read = () => setMode(currentViewMode());
    read();
    window.addEventListener(VIEW_MODE_EVENT, read);
    return () => window.removeEventListener(VIEW_MODE_EVENT, read);
  }, []);
  return mode;
}

/** The colored good/bad dot. null goodness = hollow neutral dot. */
function GoodnessDot({ g }: { g: number | null }) {
  const c = goodnessColor(g);
  return (
    <span
      aria-hidden="true"
      className="inline-block h-2 w-2 shrink-0 rounded-full"
      style={
        g == null
          ? { border: `1px solid ${c}`, background: "transparent" }
          : { background: c }
      }
    />
  );
}

export default function Plain({
  metric,
  value,
  ctx,
  raw,
  compact = false,
  className = "",
}: {
  metric: MetricKey;
  value: number | string | null | undefined;
  ctx?: PlainCtx;
  /** Override the formatted raw string (e.g. to keep a page's exact digits). */
  raw?: string;
  /** Compact: one line only — SIMPLE shows just the sentence+dot, PRO shows
   *  nothing (the host already renders the raw number, e.g. a gauge dial). */
  compact?: boolean;
  className?: string;
}) {
  const mode = useViewMode();
  const r = readMetric(metric, value, ctx);
  const rawText = raw ?? r.raw;

  if (compact) {
    if (mode === "pro") return null;
    return (
      <span
        className={`inline-flex items-center gap-1.5 text-[0.75rem] leading-snug ${className}`}
        style={{ color: "var(--dim)" }}
        title={r.detail}
      >
        <GoodnessDot g={r.goodness} />
        <span>{r.plain}</span>
      </span>
    );
  }

  if (mode === "pro") {
    return (
      <span className={`inline-flex flex-col ${className}`} title={r.detail}>
        <span className="tnum font-bold" style={{ color: goodnessColor(r.goodness) === "var(--faint)" && rawText !== "—" ? "var(--text)" : goodnessColor(r.goodness) }}>
          {rawText}
        </span>
        <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
          {r.plain}
        </span>
      </span>
    );
  }

  return (
    <span className={`inline-flex flex-col ${className}`} title={r.detail}>
      <span className="inline-flex items-start gap-1.5 leading-snug">
        <span className="mt-[0.3em]">
          <GoodnessDot g={r.goodness} />
        </span>
        <span style={{ color: r.raw === "—" && rawText === "—" ? "var(--faint)" : "var(--text)" }}>{r.plain}</span>
      </span>
      {rawText !== "—" && (
        <span className="tnum pl-3.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {rawText}
        </span>
      )}
    </span>
  );
}
