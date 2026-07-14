"use client";

// Click-a-candle detail panel. Appears below the chart when a candle carrying
// one or more patterns is clicked; lists each pattern in plain English with a
// bias arrow, its description, and — when the daemon measured it on THIS
// symbol — the honest edge ("54% up over next 5 bars, n=38"). When the sample
// is too thin (measured null) it says so instead of inventing a number. The
// daemon's `note` renders verbatim, under an explicit weak/context-only caption.

import type { CandlePattern } from "@/lib/api";
import { fmtPct, fmtTs } from "@/lib/format";

function BiasArrow({ bias }: { bias: -1 | 0 | 1 }) {
  const color = bias > 0 ? "var(--bid)" : bias < 0 ? "var(--ask)" : "var(--dim)";
  const label = bias > 0 ? "bullish" : bias < 0 ? "bearish" : "neutral";
  const glyph = bias > 0 ? "▲" : bias < 0 ? "▼" : "◆";
  return (
    <span className="chip tnum shrink-0" style={{ color, borderColor: color }} title={label}>
      {glyph} {label}
    </span>
  );
}

function measuredLine(p: CandlePattern): string {
  const m = p.measured;
  if (!m || m.n < 15) {
    const n = m?.n ?? 0;
    return `not enough history to measure (n=${n} < 15)`;
  }
  const pct = (m.hitRate * 100).toFixed(0);
  const dir = m.meanFwd >= 0 ? "up" : "down";
  return `${pct}% ${dir} over next ${m.horizon} bars on this symbol (n=${m.n}, avg ${fmtPct(m.meanFwd * 100)})`;
}

export default function PatternPopup({
  ts,
  patterns,
  note,
  onClose,
}: {
  ts: number;
  patterns: CandlePattern[];
  note?: string;
  onClose: () => void;
}) {
  if (!patterns.length) return null;
  return (
    <div
      className="pop-in mt-2 rounded-lg border p-3 text-[0.8125rem]"
      style={{ background: "var(--panel3)", borderColor: "var(--border-strong)", boxShadow: "var(--shadow-1)" }}
      role="region"
      aria-label="candle pattern detail"
    >
      <div className="mb-2 flex items-center gap-2">
        <span className="text-[0.6875rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
          Pattern · {fmtTs(ts)}
        </span>
        <button
          type="button"
          onClick={onClose}
          aria-label="dismiss pattern detail"
          className="ml-auto flex h-7 w-7 cursor-pointer items-center justify-center rounded-md transition-colors hover:bg-[var(--panel2)]"
          style={{ color: "var(--dim)" }}
        >
          <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
            <path d="M3 3l8 8M11 3l-8 8" />
          </svg>
        </button>
      </div>

      <ul className="flex flex-col gap-3">
        {patterns.map((p, i) => (
          <li key={`${p.name}-${i}`} className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-semibold" style={{ color: "var(--text)" }}>
                {p.name}
              </span>
              <BiasArrow bias={p.bias} />
            </div>
            {p.desc && (
              <p className="leading-snug" style={{ color: "var(--dim)" }}>
                {p.desc}
              </p>
            )}
            <p
              className="tnum text-[0.75rem] leading-snug"
              style={{ color: p.measured && p.measured.n >= 15 ? "var(--text)" : "var(--faint)" }}
            >
              {measuredLine(p)}
            </p>
          </li>
        ))}
      </ul>

      <p className="mt-3 border-t pt-2 text-[0.75rem] leading-relaxed" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
        {note && note.trim()
          ? note
          : "Candlestick patterns are weak, context-only signals — read them alongside the rest of the page, never on their own."}
      </p>
    </div>
  );
}
