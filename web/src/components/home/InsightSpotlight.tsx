"use client";

// INSIGHT SPOTLIGHT — the dashboard's single decisive hero. One top insight
// picked by a DETERMINISTIC priority ladder over the /api/dashboard payload
// (+ the session alerts feed when the caller has one):
//   1. today's unresolved (unseen) alert on a watchlist symbol
//   2. a regime change today
//   3. the highest-confidence calibrated prediction with n ≥ the gate (30)
//   4. the biggest anomaly in the merged feed (largest |z|)
//   5. today's daily-briefing headline
//   fallback: an honest market-breadth summary sentence (or "no read yet").
//
// HONESTY: nothing here is fabricated — every headline restates stored data,
// the confidence chip shows the REAL calibrated probability + sample size or
// says "still learning", anomalies keep their "descriptive, predicts nothing"
// framing, and the standing not-financial-advice caption is always visible.

import Link from "next/link";
import { useMemo } from "react";
import type { AlertRow, DashboardResponse, DashWatchSpark, Market } from "@/lib/api";
import { ago } from "@/lib/format";
import { probVerdict } from "@/lib/plain";

type Verb = "Monitor" | "Investigate" | "Compare";

export interface TopInsight {
  /** One plain-English headline sentence. */
  headline: string;
  /** Optional supporting line (alert detail, gate caption, …). */
  detail?: string;
  /** REAL calibrated P(up) when one applies AND clears the sample gate. */
  prob: number | null;
  /** Resolved outcomes behind that probability (0 when prob is null). */
  n: number;
  /** Payload timestamp this insight was derived from (unix seconds). */
  freshTs: number;
  verb: Verb;
  actionLabel: string;
  href: string;
  /** Small provenance label ("alert · breakout", "briefing", …). */
  source: string;
}

/** Today's date key in America/New_York (the workers' clock). */
function nyToday(): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York" }).format(new Date());
}

function nyDayOf(ts: number): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York" }).format(
    new Date(ts * 1000),
  );
}

function symbolHref(symbol: string, market?: Market): string {
  const m: Market = market ?? (symbol.includes("/") ? "crypto" : "stocks");
  return `/s/${m}/${encodeURIComponent(symbol)}`;
}

/** Calibrated probability for a spark ONLY when its own sample gate passes. */
function gatedProb(
  spark: DashWatchSpark | undefined,
  minN: number,
): { prob: number | null; n: number } {
  if (!spark || spark.calProb1d == null) return { prob: null, n: 0 };
  const n = spark.nSamples1d ?? 0;
  if (n < minN) return { prob: null, n: 0 };
  return { prob: spark.calProb1d, n };
}

function alertHeadline(a: AlertRow): string {
  const sym = a.symbol ?? "A watched symbol";
  switch (a.kind) {
    case "breakout":
      return `${sym} broke out of its recent range today.`;
    case "regime_change":
      return `${sym}'s price regime changed today — its stored behavior label flipped.`;
    case "prediction_high":
      return `The model's calibrated read on ${sym} crossed its bullish alert threshold today.`;
    case "prediction_low":
      return `The model's calibrated read on ${sym} crossed its bearish alert threshold today.`;
    default:
      return `${sym} fired a ${a.kind.replace(/_/g, " ")} alert today.`;
  }
}

/** Deterministic priority ladder — exported pure so it stays testable. */
export function pickTopInsight(
  dash: DashboardResponse,
  alerts?: AlertRow[] | null,
): TopInsight {
  const today = nyToday();
  const sparks = dash.watchlist?.sparks ?? [];
  const minN = dash.gauges.confidence.minResolvedN || 30;
  const sparkOf = (symbol?: string) => sparks.find((s) => s.symbol === symbol);
  const watchSyms = new Set(sparks.map((s) => s.symbol));

  // 1 — today's unresolved (unseen) alert on a watchlist symbol.
  const p1 = (alerts ?? [])
    .filter((a) => !a.seen && a.symbol !== undefined && watchSyms.has(a.symbol) && nyDayOf(a.ts) === today)
    .sort((x, y) => y.ts - x.ts || y.id - x.id)[0];
  if (p1?.symbol) {
    const { prob, n } = gatedProb(sparkOf(p1.symbol), minN);
    return {
      headline: alertHeadline(p1),
      detail: p1.detail,
      prob,
      n,
      freshTs: p1.ts,
      verb: "Investigate",
      actionLabel: p1.symbol,
      href: symbolHref(p1.symbol, p1.market),
      source: `alert · ${p1.kind.replace(/_/g, " ")}`,
    };
  }

  // 2 — a regime change today (unseen first, then newest).
  const p2 = (alerts ?? [])
    .filter((a) => a.kind === "regime_change" && nyDayOf(a.ts) === today)
    .sort((x, y) => Number(x.seen) - Number(y.seen) || y.ts - x.ts || y.id - x.id)[0];
  if (p2) {
    const { prob, n } = gatedProb(sparkOf(p2.symbol), minN);
    return {
      headline: alertHeadline(p2),
      detail: p2.detail,
      prob,
      n,
      freshTs: p2.ts,
      verb: p2.symbol ? "Investigate" : "Monitor",
      actionLabel: p2.symbol ?? "market regimes",
      href: p2.symbol ? symbolHref(p2.symbol, p2.market) : "/market/regimes",
      source: "alert · regime change",
    };
  }

  // 3 — highest-confidence calibrated prediction with n ≥ gate (skip coin
  // flips: |p−0.5|·2 < 0.1 is "no clear lean", not an insight).
  const p3 = sparks
    .filter((s) => s.calProb1d != null && (s.nSamples1d ?? 0) >= minN)
    .map((s) => ({ s, conf: Math.abs((s.calProb1d as number) - 0.5) * 2 }))
    .filter((c) => c.conf >= 0.1)
    .sort((x, y) => y.conf - x.conf || x.s.symbol.localeCompare(y.s.symbol))[0];
  if (p3) {
    const p = p3.s.calProb1d as number;
    const pct = Math.round(p * 100);
    return {
      headline: `The model ${p >= 0.5 ? "leans up" : "leans down"} on ${p3.s.symbol}: a ${pct}% calibrated chance of rising over the next day.`,
      detail: "Backtested calibration on stored history — the strongest gated read on your watchlist right now.",
      prob: p,
      n: p3.s.nSamples1d ?? 0,
      freshTs: dash.asOf,
      verb: "Investigate",
      actionLabel: p3.s.symbol,
      href: symbolHref(p3.s.symbol, p3.s.market),
      source: "calibrated prediction · 1d",
    };
  }

  // 4 — the biggest anomaly in the merged feed (largest |z|; ties → newest).
  const p4 = (dash.feed.items ?? [])
    .filter((it) => it.kind === "anomaly")
    .sort(
      (x, y) =>
        (Number.isFinite(y.z as number) ? Math.abs(y.z as number) : -1) -
          (Number.isFinite(x.z as number) ? Math.abs(x.z as number) : -1) || y.ts - x.ts,
    )[0];
  if (p4) {
    const { prob, n } = gatedProb(sparkOf(p4.symbol), minN);
    return {
      headline: `Unusual activity: ${p4.title.replace(/\.$/, "")}.`,
      detail:
        "Descriptive unusualness measured against the symbol's own trailing baseline — it predicts nothing.",
      prob,
      n,
      freshTs: p4.ts,
      verb: p4.symbol ? "Investigate" : "Monitor",
      actionLabel: p4.symbol ?? "unusual activity",
      href: p4.symbol ? symbolHref(p4.symbol, p4.market) : "/market/unusual",
      source: `anomaly${p4.sub ? ` · ${p4.sub}` : ""}`,
    };
  }

  // 5 — today's daily-briefing headline (same NY-day rule as the pinned card).
  const p5 = (dash.feed.items ?? [])
    .filter((it) => it.kind === "briefing" && nyDayOf(it.ts) === today)
    .sort((x, y) => y.ts - x.ts)[0];
  if (p5) {
    return {
      headline: p5.title.replace(/\.$/, "") + ".",
      detail: p5.detail,
      prob: null,
      n: 0,
      freshTs: p5.ts,
      verb: "Monitor",
      actionLabel: "today's briefing",
      href: "/lab/insights",
      source: "daily briefing",
    };
  }

  // Fallback — market-breadth summary (always renders its gate caption).
  const b = dash.gauges.breadth;
  if (b.hasData && b.n > 0) {
    const pct = Math.round((b.advancers / b.n) * 100);
    const shape = pct >= 60 ? "a broad advance" : pct <= 40 ? "a broad decline" : "a mixed tape";
    return {
      headline: `${b.advancers} of ${b.n} tracked symbols closed higher (${pct}%) — ${shape} on the latest stored closes.`,
      detail: b.caption,
      prob: null,
      n: 0,
      freshTs: dash.asOf,
      verb: "Compare",
      actionLabel: "the movers",
      href: "/market/overview",
      source: "market breadth",
    };
  }
  return {
    headline: "Not enough fresh daily bars to read market breadth yet.",
    detail: b.caption,
    prob: null,
    n: 0,
    freshTs: dash.asOf,
    verb: "Monitor",
    actionLabel: "the markets",
    href: "/market/overview",
    source: "market breadth",
  };
}

/** The ONE top insight, rendered as the dashboard hero. */
export default function InsightSpotlight({
  dash,
  alerts,
  className = "",
}: {
  dash: DashboardResponse;
  alerts?: AlertRow[] | null;
  className?: string;
}) {
  const ins = useMemo(() => pickTopInsight(dash, alerts), [dash, alerts]);
  const vd = probVerdict(ins.prob, { n: ins.n });

  return (
    <section
      className={`panel relative ${className}`}
      style={{ borderColor: "color-mix(in srgb, var(--accent) 45%, var(--border))" }}
      aria-labelledby="spotlight-headline"
    >
      <div className="panel-h">
        <span style={{ color: "var(--accent)" }}>TOP INSIGHT</span>
        <span className="chip px-2 py-[1px] text-[0.75rem]">{ins.source}</span>
        <span
          className="chip tnum ml-auto px-2 py-[1px] text-[0.75rem]"
          // tabIndex lets the [title]:focus-visible fallback (globals.css)
          // surface the caveat for keyboard users until this migrates to
          // <HelpTip>.
          tabIndex={0}
          title="derived from the timestamps the daemon put on this payload — never a live quote"
        >
          data as of {ago(ins.freshTs)}
        </span>
      </div>

      <div className="flex flex-col gap-3 px-5 py-5">
        <p
          id="spotlight-headline"
          className="m-0 text-[1.05rem] leading-snug font-bold lg:text-[1.2rem]"
        >
          {ins.headline}
        </p>

        {ins.detail ? (
          <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {ins.detail}
          </p>
        ) : null}

        <div className="flex flex-wrap items-center gap-3">
          {ins.prob !== null ? (
            <span
              className="chip tnum px-3 py-[5px] text-[0.75rem] font-bold"
              style={{ color: vd.color }}
              tabIndex={0}
              title={`calibrated P(up) over 1 day — ${vd.confidenceWord}; n = resolved outcomes behind it`}
            >
              {Math.round(ins.prob * 100)}% chance of rising · n={ins.n}
            </span>
          ) : (
            <span
              className="chip px-3 py-[5px] text-[0.75rem]"
              style={{ color: "var(--faint)" }}
              tabIndex={0}
              title="no calibrated probability has cleared the 30-resolved-outcome gate for this item yet"
            >
              still learning
            </span>
          )}

          <Link
            href={ins.href}
            className="inline-flex min-h-[44px] cursor-pointer items-center gap-2 rounded-lg border px-4 text-[0.85rem] font-bold tracking-wide transition-colors duration-150 hover:bg-[var(--accent-dim)]"
            style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
          >
            {ins.verb} {ins.actionLabel}
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M3 8h10M9 4l4 4-4 4"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </Link>
        </div>

        <p className="m-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          Backtested calibration graded against stored outcomes — descriptive, not a forecast.
          Not financial advice.
        </p>
      </div>
    </section>
  );
}
