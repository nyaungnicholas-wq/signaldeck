"use client";

// FACTOR TILES — the 11 fixed-order legs behind one composite verdict.
// Each tile: verdict arrow (▲/■/▼), plain-English name in SIMPLE mode / the
// raw key in PRO, the API's evidence line VERBATIM, and a measured-skill chip
// ("hit 58% · IC .11 · n=44") when the leg has one. Gated tiles gray out and
// state their gateReason on the tile — a caveat is never a tooltip. regime +
// shortvol are CONTEXT tiles (always-neutral by design): dashed border and a
// CONTEXT tag instead of an arrow, so they never read as a vote.

import type { CompositeFactor } from "@/lib/api";
import { useViewMode, type ViewMode } from "@/components/Plain";
import { FACTOR_META, factorName } from "./compositeUi";

function skillChip(f: CompositeFactor): string | null {
  if (f.skillHitRate == null && f.skillIC == null) return null;
  const parts: string[] = [];
  if (f.skillHitRate != null) parts.push(`hit ${Math.round(f.skillHitRate * 100)}%`);
  if (f.skillIC != null) parts.push(`IC ${f.skillIC.toFixed(2).replace(/^(-?)0\./, "$1.")}`);
  if (f.skillN != null) parts.push(`n=${f.skillN}`);
  return parts.join(" · ");
}

function FactorTile({ f, mode }: { f: CompositeFactor; mode: ViewMode }) {
  const isContext = !!FACTOR_META[f.key]?.context;
  const arrow = f.verdict > 0 ? "▲" : f.verdict < 0 ? "▼" : "■";
  const word = f.verdict > 0 ? "bullish" : f.verdict < 0 ? "bearish" : "neutral";
  const arrowColor = f.gated
    ? "var(--faint)"
    : f.verdict > 0
      ? "var(--bid)"
      : f.verdict < 0
        ? "var(--ask)"
        : "var(--dim)";
  const skill = skillChip(f);

  return (
    <div
      className={`flex flex-col gap-1.5 rounded-lg border p-3 ${f.gated ? "opacity-60" : ""}`}
      style={{
        borderColor: "var(--border)",
        borderStyle: isContext ? "dashed" : "solid",
        background: "var(--panel2)",
      }}
    >
      <div className="flex items-center gap-2">
        {isContext ? (
          <span
            className="text-[0.75rem] font-bold tracking-[0.14em]"
            style={{ color: "var(--crossed)" }}
            title="context leg — informs the read but never votes a direction"
          >
            CONTEXT
          </span>
        ) : (
          <span role="img" aria-label={word} style={{ color: arrowColor }}>
            {arrow}
          </span>
        )}
        <span
          className="text-[0.75rem] font-bold tracking-wide"
          style={{ color: f.gated ? "var(--faint)" : "var(--text)" }}
        >
          {factorName(f.key, mode)}
        </span>
        {f.gated && (
          <span
            className="ml-auto text-[0.75rem] font-bold uppercase tracking-wider"
            style={{ color: "var(--warn)" }}
          >
            gated
          </span>
        )}
      </div>

      {/* the evidence line — verbatim from the API */}
      <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
        {f.evidence}
      </p>

      {skill && (
        <span
          className="chip tnum self-start"
          title="this leg's measured skill on resolved outcomes — absent chips mean unmeasured, never assumed"
        >
          {skill}
        </span>
      )}

      {f.gated && f.gateReason && (
        <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          excluded from the score — {f.gateReason}
        </p>
      )}
    </div>
  );
}

export default function FactorTiles({ factors }: { factors: CompositeFactor[] }) {
  const mode = useViewMode();
  const scored = factors.filter((f) => !f.gated).length;
  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        {mode === "simple" ? "WHAT WENT INTO IT — 11 FACTORS" : "FACTOR LEGS (11, FIXED ORDER)"}
        <span className="chip tnum ml-auto">
          {scored}/{factors.length} scored
        </span>
      </div>
      <div className="grid grid-cols-1 gap-2 px-4 py-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {factors.map((f) => (
          <FactorTile key={f.key} f={f} mode={mode} />
        ))}
      </div>
      <p
        className="px-4 pb-3 text-[0.75rem] leading-relaxed"
        style={{ color: "var(--faint)" }}
      >
        ▲ bullish · ■ neutral / no read · ▼ bearish. CONTEXT tiles (market regime, short-sale
        volume) inform the other legs but never vote a direction. Gated tiles are excluded from
        the score — the reason is on the tile. The hit/IC chip is that leg&apos;s measured skill
        over resolved outcomes; legs without one are unmeasured, not assumed skillful.
      </p>
    </section>
  );
}
