"use client";

// VISUAL KIT (Stage 3) — <Heatmap/>: the universe day-%change grid. Pure
// CSS/flex tiles (no canvas dep). Cell COLOR is a red→neutral→green diverging
// ramp clamped at ±3% (alpha-scaled so magnitude reads as saturation); the
// %change is ALSO printed in every cell so color is never the only signal
// (colorblind safety per ui-ux-pro-max). Cell SIZE ∝ sqrt(mcap) when SEC
// EDGAR covers the symbol; cells WITHOUT mcap render at the uniform base size
// and are labeled "sized: mcap unavailable" (honesty rule — never a guessed
// size). Every tile is a real <Link> → /s/{market}/{symbol}: keyboard
// focusable, focus ring from globals.css, hover tooltip via title.

import type { Market } from "@/lib/api";
import { usePeek } from "@/components/CompanyPeek";

export interface HeatmapItem {
  symbol: string;
  changePct: number;
  mcap?: number | null;
  name?: string;
  market?: Market;
}

// Clamp for the color ramp: ±3% daily move = fully saturated.
const CLAMP_PCT = 3;
// Tile side bounds (px). Base = the uniform no-mcap size; also min touch-ish.
const SIDE_MIN = 48;
const SIDE_MAX = 112;

function cellBackground(chg: number): string {
  const t = Math.max(-1, Math.min(1, chg / CLAMP_PCT));
  if (t === 0) return "var(--panel2)";
  const alpha = 0.1 + 0.42 * Math.abs(t);
  return t > 0
    ? `rgba(52, 211, 153, ${alpha.toFixed(3)})` // --bid ramp
    : `rgba(248, 113, 113, ${alpha.toFixed(3)})`; // --ask ramp
}

function fmtChg(v: number): string {
  return `${v >= 0 ? "+" : ""}${v.toFixed(2)}%`;
}

export default function Heatmap({ items }: { items: HeatmapItem[] }) {
  const peek = usePeek();
  if (!items || items.length === 0) {
    return (
      <p className="px-3 py-4 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        No fresh universe bars yet — the universe poller fills daily closes on
        its own cadence; tiles appear as bars arrive (nothing is faked to fill
        the grid).
      </p>
    );
  }

  // sqrt(mcap) sizing over the covered subset; unknown-mcap = uniform base.
  const known = items.filter((i) => typeof i.mcap === "number" && (i.mcap as number) > 0);
  const maxSqrt = known.length
    ? Math.max(...known.map((i) => Math.sqrt(i.mcap as number)))
    : 0;
  const side = (it: HeatmapItem): number => {
    if (!(typeof it.mcap === "number" && it.mcap > 0) || maxSqrt === 0) return SIDE_MIN;
    const s = Math.sqrt(it.mcap) / maxSqrt; // 0..1
    return Math.round(SIDE_MIN + s * (SIDE_MAX - SIDE_MIN));
  };

  // Big known names first (treemap feel), then unknown-mcap uniformly, both
  // alphabetical within their band so the layout is stable across refreshes.
  const sorted = [...items].sort((a, b) => {
    const am = typeof a.mcap === "number" && a.mcap > 0 ? a.mcap : -1;
    const bm = typeof b.mcap === "number" && b.mcap > 0 ? b.mcap : -1;
    if (am !== bm) return bm - am;
    return a.symbol.localeCompare(b.symbol);
  });

  const anyUnknown = sorted.some((i) => !(typeof i.mcap === "number" && i.mcap > 0));

  return (
    <div>
      <ul
        className="m-0 flex list-none flex-wrap content-start gap-1 p-0"
        aria-label={`market heatmap, ${items.length} symbols, color = day % change`}
      >
        {sorted.map((it) => {
          const s = side(it);
          const hasMcap = typeof it.mcap === "number" && it.mcap > 0;
          const tip =
            `${it.symbol}${it.name ? ` — ${it.name}` : ""} · ${fmtChg(it.changePct)} today` +
            (hasMcap ? "" : " · sized: mcap unavailable (EDGAR gap)");
          return (
            <li key={it.symbol} className="m-0 p-0">
              {/* 2026-07-18: tiles open the CompanyPeek slide-over (fast
                  browsing); the full symbol page is one click inside it. */}
              <button
                type="button"
                onClick={() => peek.open(it.symbol, (it.market ?? "stocks") as Market)}
                title={tip}
                aria-label={tip}
                className="flex cursor-pointer flex-col items-center justify-center overflow-hidden rounded-lg border transition-colors duration-150 hover:border-[var(--accent)]"
                style={{
                  width: s,
                  height: s,
                  background: cellBackground(it.changePct),
                  borderColor: "var(--border)",
                  textDecorationLine: "none",
                }}
              >
                <span
                  className="max-w-full truncate px-1 font-bold tracking-wide"
                  style={{ fontSize: s >= 72 ? "0.72rem" : "0.6rem", color: "var(--text)" }}
                >
                  {it.symbol}
                </span>
                <span
                  className="tnum"
                  style={{
                    fontSize: s >= 72 ? "0.72rem" : "0.6rem",
                    color: it.changePct > 0 ? "var(--bid)" : it.changePct < 0 ? "var(--ask)" : "var(--dim)",
                  }}
                >
                  {fmtChg(it.changePct)}
                </span>
                {!hasMcap && s >= 64 && (
                  <span style={{ fontSize: "0.5rem", color: "var(--faint)" }}>mcap n/a</span>
                )}
              </button>
            </li>
          );
        })}
      </ul>

      {/* legend — color scale + the sizing honesty note */}
      <div
        className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[0.75rem]"
        style={{ color: "var(--faint)" }}
      >
        <span className="flex items-center gap-1" aria-hidden="true">
          <span className="inline-block h-2.5 w-2.5 rounded-[2px]" style={{ background: cellBackground(-CLAMP_PCT) }} />
          −{CLAMP_PCT}%
          <span className="inline-block h-2.5 w-2.5 rounded-[2px]" style={{ background: "var(--panel2)", border: "1px solid var(--border)" }} />
          0
          <span className="inline-block h-2.5 w-2.5 rounded-[2px]" style={{ background: cellBackground(CLAMP_PCT) }} />
          +{CLAMP_PCT}%
        </span>
        <span>area ∝ √mcap (SEC EDGAR, best-effort)</span>
        {anyUnknown && <span>uniform cells: sized: mcap unavailable — never guessed</span>}
      </div>
    </div>
  );
}
