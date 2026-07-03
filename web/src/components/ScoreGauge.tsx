"use client";

import { fmtScore, verdict } from "@/lib/format";

/** Horizontal −1…+1 pressure gauge with needle; the app's signature read. */
export default function ScoreGauge({
  score,
  label,
  compact = false,
}: {
  score: number;
  label?: string;
  compact?: boolean;
}) {
  const pct = Math.max(0, Math.min(100, 50 + score * 50));
  return (
    <div>
      {!compact && (
        <div className="mb-1 flex items-baseline justify-between text-[0.78rem]">
          <span style={{ color: "var(--dim)" }}>{label ?? "pressure"}</span>
          <span className="tnum" style={{ color: "var(--text)" }}>
            {fmtScore(score)} · {verdict(score)}
          </span>
        </div>
      )}
      <div
        role="meter"
        aria-valuemin={-1}
        aria-valuemax={1}
        aria-valuenow={Number(score.toFixed(3))}
        aria-label={label ?? "pressure score"}
        className="relative rounded-full border"
        style={{
          height: compact ? 8 : 12,
          borderColor: "var(--border)",
          background: "linear-gradient(90deg, var(--ask-dim), var(--panel2) 50%, var(--bid-dim))",
        }}
      >
        <div
          className="absolute top-[-3px] bottom-[-3px] w-px"
          style={{ left: "50%", background: "var(--faint)" }}
        />
        <div
          className="absolute top-[-2px] bottom-[-2px] w-1 rounded-sm transition-[left] duration-200"
          style={{
            left: `calc(${pct}% - 2px)`,
            background: "var(--accent)",
            boxShadow: "0 0 8px rgba(251,191,36,.6)",
          }}
        />
      </div>
    </div>
  );
}
