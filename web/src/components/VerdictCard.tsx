"use client";

// VERDICT CARD (Stage 2 of the "numbers with meaning" pass) — THE one way the
// product says "leans up / leans down / no read" everywhere: dashboard
// watchlist rows, the screener VERDICT column, the symbol-page hero, and the
// signals predictions table.
//
// Anatomy: colored arrow + "LEANS UP — 62%" (from lib/plain.ts probVerdict,
// which only ever speaks from the REAL calibrated probability) + a
// 0.5-centered confidence meter + the honest evidence-tier badge ("still
// learning 12/40 — using global model" / "own model") + optional sparkline.
//
// HONESTY DOCTRINE (non-negotiable):
//   · gated/null prob → "NO READ YET — still collecting evidence", never a
//     fabricated lean;
//   · the tier badge is ALWAYS visible — simple mode hides jargon, never
//     caveats;
//   · the backtested-not-live caveat rides on the card (footer on lg,
//     tooltip on sm);
//   · low-confidence leans render muted so a 52% never shouts like a 75%.
//
// size="sm" is row-embeddable (tables/sidebars); size="lg" is the hero card.

import { useViewMode } from "@/components/Plain";
import Sparkline from "@/components/viz/Sparkline";
import { probVerdict, tierBadge } from "@/lib/plain";

/** March toward the personal-model gate ("12/40"). */
export interface TierProgress {
  nSamples: number;
  threshold: number;
}

const NO_READ_TEXT = "NO READ YET — still collecting evidence";
const BACKTEST_NOTE = "backtested calibration — not a live track record";

/** 0.5-centered horizontal confidence meter: the bar grows from the 50%
 *  coin-flip tick toward the calibrated P(up). null = empty track (no read). */
function ConfidenceMeter({
  pct,
  color,
  h = 4,
}: {
  pct: number | null;
  color: string;
  h?: number;
}) {
  const left = pct === null ? 50 : Math.min(pct, 50);
  const width = pct === null ? 0 : Math.abs(pct - 50);
  return (
    <div
      className="relative w-full overflow-hidden rounded-full"
      style={{ height: h, background: "color-mix(in srgb, var(--faint) 22%, transparent)" }}
      role="img"
      aria-label={
        pct === null
          ? "no read yet — confidence meter empty"
          : `calibrated ${pct}% chance of an up move (50% would be a coin flip)`
      }
    >
      <div
        className="absolute top-0 h-full"
        style={{ left: `${left}%`, width: `${width}%`, background: color }}
      />
      {/* the coin-flip tick — always visible so "distance from 50" reads at a glance */}
      <div
        aria-hidden="true"
        className="absolute top-0 h-full"
        style={{ left: "50%", width: 1, background: "var(--dim)" }}
      />
    </div>
  );
}

export default function VerdictCard({
  symbol,
  market,
  calProb,
  nUsed = 0,
  tier,
  tierProgress,
  sparkCloses,
  size = "sm",
  horizon = "1d",
  gated = false,
  className = "",
}: {
  symbol: string;
  market?: string;
  /** The REAL calibrated P(up), 0..1 — null/undefined = no prediction stored. */
  calProb: number | null | undefined;
  /** Ensemble legs behind the number (0 when there is no read). */
  nUsed?: number;
  /** Symbol-agent evidence tier: personal|regime|global|static; ""/undefined
   *  = no agent row yet (reads as the honest static tier). */
  tier?: string | null;
  /** Own-samples march toward the personal-model gate ("12/40"). */
  tierProgress?: TierProgress;
  /** Optional daily closes for the inline sparkline (lg only). */
  sparkCloses?: number[];
  size?: "sm" | "lg";
  horizon?: string;
  /** Explicit honesty gate — true forces NO READ YET even if a number exists. */
  gated?: boolean;
  className?: string;
}) {
  const mode = useViewMode();
  const vd = probVerdict(calProb, { gated });
  // "" tier = no symbol_models row stored yet — that IS the static tier
  // (nothing learned anywhere), never "unknown".
  const badge = tierBadge(tier === "" ? "static" : tier, tierProgress?.nSamples, tierProgress?.threshold);

  const noRead = vd.pct === null;
  // Mute weak leans: a 53% must not shout like a 75%.
  const muted = vd.confidence !== null && vd.confidence < 0.25;
  const color = noRead
    ? "var(--faint)"
    : muted
      ? `color-mix(in srgb, ${vd.color} 60%, var(--dim))`
      : vd.color;

  const headline = noRead ? NO_READ_TEXT : `${vd.label} — ${vd.pct}%`;
  const tooltip = noRead
    ? `${symbol}: no calibrated ${horizon} prediction stored yet — a missing number is never dressed up as a verdict. ${badge.detail}`
    : `${symbol}: calibrated ${vd.pct}% chance the price is higher at the ${horizon} horizon — ${vd.confidenceWord} (50% would be a coin flip). Blend of ${nUsed} signal${nUsed === 1 ? "" : "s"}. ${badge.detail} ${BACKTEST_NOTE}.`;

  // ── sm: row-embeddable (tables / sidebar) ─────────────────────────────
  if (size === "sm") {
    return (
      <span className={`inline-flex min-w-[96px] max-w-[190px] flex-col gap-[3px] ${className}`} title={tooltip}>
        <span
          className="inline-flex items-center gap-1 text-[0.7rem] font-bold tracking-wider"
          style={{ color }}
        >
          <span aria-hidden="true">{vd.arrow}</span>
          <span>{noRead ? "NO READ YET" : `${vd.label} — ${vd.pct}%`}</span>
        </span>
        <ConfidenceMeter pct={vd.pct} color={color} h={3} />
        {/* the honest tier badge — visible in BOTH modes, never tooltip-only */}
        <span className="truncate text-[0.62rem] leading-tight" style={{ color: "var(--faint)" }}>
          {noRead ? "still collecting evidence" : badge.label}
        </span>
      </span>
    );
  }

  // ── lg: hero card ─────────────────────────────────────────────────────
  return (
    <section className={`panel ${className}`} title={tooltip}>
      <div className="panel-h flex-wrap gap-2">
        VERDICT · {symbol}
        {market && <span className="chip uppercase tracking-wider">{market}</span>}
        <span className="chip tnum">{horizon}</span>
        <span
          className="ml-auto text-[0.66rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          {BACKTEST_NOTE}
        </span>
      </div>
      <div className="flex flex-col gap-3 px-4 py-4">
        <div className="flex flex-wrap items-center gap-3">
          <span aria-hidden="true" className="text-4xl leading-none font-black" style={{ color }}>
            {vd.arrow}
          </span>
          <div className="flex min-w-0 flex-col">
            <span className="text-lg font-extrabold tracking-[0.08em]" style={{ color }}>
              {headline}
            </span>
            <span className="text-[0.74rem]" style={{ color: "var(--dim)" }}>
              {noRead
                ? "no calibrated prediction stored for this symbol yet — nothing is invented in the meantime"
                : mode === "pro"
                  ? `calibrated P(up) ${((calProb as number) * 100).toFixed(1)}% · ${vd.confidenceWord} · blend n=${nUsed}`
                  : `${vd.pct}% chance the price is higher at the ${horizon} horizon — ${vd.confidenceWord} (50% would be a coin flip)`}
            </span>
          </div>
          {sparkCloses && sparkCloses.length > 0 && (
            <span className="ml-auto shrink-0">
              <Sparkline closes={sparkCloses} width={132} height={38} />
            </span>
          )}
        </div>

        <div className="flex flex-col gap-1">
          <div className="flex items-center justify-between text-[0.64rem]" style={{ color: "var(--faint)" }}>
            <span>↓ down</span>
            <span>coin flip</span>
            <span>up ↑</span>
          </div>
          <ConfidenceMeter pct={vd.pct} color={color} h={6} />
        </div>

        {/* the honest evidence-tier badge — always visible */}
        <div className="flex flex-wrap items-center gap-2">
          <span
            className="chip px-2 py-[1px] text-[0.68rem]"
            style={{
              color: tier === "personal" ? "var(--accent)" : "var(--dim)",
              borderColor: tier === "personal" ? "var(--accent)" : "var(--border)",
            }}
            title={badge.detail}
          >
            {badge.label}
          </span>
          {mode === "simple" && !noRead && (
            <span className="text-[0.68rem]" style={{ color: "var(--faint)" }}>
              blend of {nUsed} signal{nUsed === 1 ? "" : "s"}
            </span>
          )}
        </div>
      </div>
    </section>
  );
}
