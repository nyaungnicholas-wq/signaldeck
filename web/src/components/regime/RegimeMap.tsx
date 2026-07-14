"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import type { RegimeState } from "@/lib/api";
import { ago } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import { useViewMode } from "@/components/Plain";
import { regimeSentence } from "@/lib/plain";
import { regimeColor, regimeKind, regimeSortRank, strengthPct } from "./regime";

// Stage 4 (tables→charts): the map leads with a heatmap-style COLORED TILE
// GRID (tile tint = regime color, so the whole universe's mood reads in one
// glance), with the detailed per-symbol cards one toggle away. Default view
// follows the SIMPLE/PRO toggle — grid in simple, detailed cards in pro.
// Both views render the SAME classified states; unclassified symbols simply
// aren't in the payload (honest absence, nothing invented to fill the grid).
type MapView = "grid" | "detailed";

/** Compact heatmap-style tile: symbol on a regime-tinted background. */
function CompactTile({ s }: { s: RegimeState }) {
  const color = regimeColor(s.label);
  const pct = strengthPct(s.strength);
  return (
    <Link
      href={`/s/${s.market}/${encodeURIComponent(s.symbol)}`}
      className="flex cursor-pointer flex-col items-center gap-0.5 rounded-lg px-2 py-2 text-center transition-[filter] duration-150 hover:brightness-125"
      style={{
        background: `color-mix(in srgb, ${color} 16%, var(--panel2))`,
        border: `1px solid color-mix(in srgb, ${color} 45%, var(--border))`,
      }}
      title={`${s.symbol} (${s.market}) — ${regimeSentence(s.label)} · strength ${pct.toFixed(0)}% · ${ago(s.ts)} — descriptive from stored bars, no lookahead`}
    >
      <span className="mono text-[0.75rem] font-bold leading-tight" style={{ color: "var(--text)" }}>
        {s.symbol}
      </span>
      <span className="text-[0.75rem] uppercase tracking-wider" style={{ color }}>
        {s.label || "—"}
      </span>
    </Link>
  );
}

function RegimeTile({ s }: { s: RegimeState }) {
  const color = regimeColor(s.label);
  const kind = regimeKind(s.label);
  const pct = strengthPct(s.strength);
  return (
    <div
      className="flex flex-col gap-2 rounded-lg border bg-[var(--panel2)] p-3 transition-colors duration-150 hover:bg-[var(--panel)]"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="flex items-center justify-between gap-2">
        <Link
          href={`/s/${s.market}/${encodeURIComponent(s.symbol)}`}
          className="mono cursor-pointer text-sm font-bold transition-colors duration-150 hover:text-[var(--accent)]"
        >
          {s.symbol}
          <span className="ml-1.5 text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
            {s.market}
          </span>
        </Link>
        <span
          className="chip shrink-0"
          style={{ color, borderColor: color }}
          aria-label={`regime: ${kind}`}
        >
          {s.label || "—"}
        </span>
      </div>

      {/* strength bar */}
      <div
        role="meter"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(pct)}
        aria-label={`${s.symbol} regime strength`}
        className="h-1.5 w-full overflow-hidden rounded-full"
        style={{ background: "var(--panel)" }}
        title={`strength ${pct.toFixed(0)}%`}
      >
        <div
          className="h-full transition-[width] duration-300"
          style={{ width: `${pct}%`, background: color }}
        />
      </div>

      <div className="flex items-center justify-between gap-2 text-[0.75rem]">
        <span className="min-w-0 flex-1 truncate" style={{ color: "var(--dim)" }} title={s.note}>
          {s.note || "no note"}
        </span>
        <span className="tnum shrink-0" style={{ color: "var(--faint)" }}>
          {ago(s.ts)}
        </span>
      </div>
    </div>
  );
}

/** REGIME MAP — every tracked symbol as a tile, grouped by regime. */
export default function RegimeMap({ states }: { states: RegimeState[] }) {
  const mode = useViewMode();
  // null = follow the SIMPLE/PRO default (grid in simple, detailed in pro).
  const [view, setView] = useState<MapView | null>(null);
  const effView: MapView = view ?? (mode === "simple" ? "grid" : "detailed");

  const sorted = useMemo(() => {
    return [...states].sort((a, b) => {
      const r = regimeSortRank(a.label) - regimeSortRank(b.label);
      if (r !== 0) return r;
      const sa = Number.isFinite(a.strength) ? a.strength : -Infinity;
      const sb = Number.isFinite(b.strength) ? b.strength : -Infinity;
      if (sb !== sa) return sb - sa;
      return a.symbol.localeCompare(b.symbol);
    });
  }, [states]);

  // Legend: which regimes actually appear (never a legend entry for a regime
  // that isn't in the data).
  const legend = useMemo(() => {
    const seen = new Map<string, string>(); // kind → representative label
    for (const s of sorted) {
      const k = regimeKind(s.label);
      if (!seen.has(k)) seen.set(k, s.label || k);
    }
    return [...seen.entries()];
  }, [sorted]);

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        REGIME MAP
        {/* Stage 4: grid ↔ detailed toggle — same classified states. */}
        <span className="flex items-center gap-1" role="tablist" aria-label="regime map view">
          {(["grid", "detailed"] as MapView[]).map((v) => (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={effView === v}
              onClick={() => setView(v)}
              className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:brightness-125"
              style={{
                color: effView === v ? "var(--accent)" : "var(--dim)",
                borderColor: effView === v ? "var(--accent)" : "var(--border)",
              }}
            >
              {v}
            </button>
          ))}
        </span>
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          {effView === "grid"
            ? "tile color = detected regime (descriptive, no lookahead)"
            : "what state each symbol is in right now"}
        </span>
      </div>
      {sorted.length === 0 ? (
        <EmptyState
          className="border-0"
          message="No regimes classified yet"
          detail="States appear once enough bars are stored per symbol."
        />
      ) : effView === "grid" ? (
        <div className="p-3">
          <div
            className="grid gap-1.5"
            style={{ gridTemplateColumns: "repeat(auto-fill, minmax(86px, 1fr))" }}
          >
            {sorted.map((s) => (
              <CompactTile key={`${s.market}:${s.symbol}`} s={s} />
            ))}
          </div>
          {legend.length > 0 && (
            <div className="mt-2 flex flex-wrap items-center gap-2 text-[0.75rem]">
              {legend.map(([kind, label]) => (
                <span key={kind} className="inline-flex items-center gap-1" style={{ color: "var(--faint)" }}>
                  <span
                    aria-hidden="true"
                    className="inline-block h-2.5 w-2.5 rounded-sm"
                    style={{ background: `color-mix(in srgb, ${regimeColor(label)} 45%, var(--panel2))` }}
                  />
                  {kind}
                </span>
              ))}
              <span style={{ color: "var(--faint)" }}>
                · each tile links to its symbol page; switch to “detailed” for strength bars and
                plain-English notes
              </span>
            </div>
          )}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-2 p-3 sm:grid-cols-2 lg:grid-cols-3">
          {sorted.map((s) => (
            <RegimeTile key={`${s.market}:${s.symbol}`} s={s} />
          ))}
        </div>
      )}
    </section>
  );
}
