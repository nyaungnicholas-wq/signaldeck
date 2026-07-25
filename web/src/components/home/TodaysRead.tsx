"use client";

// TODAY'S READ — the decisive opening card the dashboard leads with.
//
// IT NOW LEADS WITH THE VALIDATED SIGNAL, NOT THE DIRECTIONAL ONE. That change
// is evidence-driven, not cosmetic. An independent re-validation (fresh
// reimplementation over 968 stocks / 1,900 trading days, non-overlapping
// windows, date-clustered CIs) measured both reads this card could open with:
//
//   VOLATILITY REGIME (vol21) — replicated and significant. 76.4% accuracy at
//   top conviction, +20.7pp over the majority-class null, CI entirely positive,
//   stable in 4 of 5 market eras. Balanced median-split label, so accuracy
//   genuinely IS skill here.
//
//   DIRECTION (P(up) 1d) — PROVEN NEGATIVE live skill: 48.1% directional
//   accuracy against a 54.5% majority-class null over 12,931 independent
//   symbol-days, the whole CI below the null, and raising conviction makes it
//   WORSE. It is not a read; it is an experiment.
//
// So the headline is the structural call and the directional probability is
// demoted to a clearly-labelled footnote that states its own negative record.
// Leading with the directional number — as this card used to — put the
// platform's least trustworthy output in its most prominent slot.
//
// TRADEABILITY: a high hit rate is not a profitable trade. Trend-persistence
// calls are ~96% accurate at top conviction yet carry a MEASURED NEGATIVE mean
// forward return, because high conviction means price is far from its SMA200,
// i.e. extended, and extended names mean-revert. When the daemon ships a
// `tradeability` string for a forecast it is rendered verbatim.

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import type {
  DashboardResponse,
  DashWatchSpark,
  Market,
  StructRegimeForecast,
} from "@/lib/api";
import { structuralRegimes } from "@/lib/api";

function symbolHref(symbol: string, market?: Market): string {
  const m: Market = market ?? (symbol.includes("/") ? "crypto" : "stocks");
  return `/s/${m}/${encodeURIComponent(symbol)}`;
}

function reportHref(symbol: string, market: string, kind: string): string {
  return `/signals/report/${market}/${encodeURIComponent(symbol)}?kind=${encodeURIComponent(kind)}`;
}

/** The daemon may ship a measured tradeability note alongside a forecast (the
 *  accuracy-vs-forward-return disclosure). Optional so this card keeps working
 *  on daemon builds that predate the field. */
type Forecast = StructRegimeForecast & { tradeability?: string };

/** Validated kinds, best first. vol21 replicated as genuine skill on a balanced
 *  label; the trend/liquidity kinds are accurate but are persistence
 *  statistics, so they rank below it and must carry their caveat. */
const VALIDATED_ORDER = ["vol21", "trend21", "liquidity21", "trend63"] as const;

export interface BestRead {
  fc: Forecast;
  /** true when this kind is a persistence statistic rather than measured skill */
  persistenceOnly: boolean;
}

/** Pick the single best-evidenced VALIDATED read across the watchlist. Exported
 *  pure so the choice stays testable. Ranking: preferred kind first (vol21),
 *  then highest conviction, then highest measured accuracy. Below a 0.5
 *  conviction there is no read — the banded accuracy there is barely better
 *  than a coin flip. */
export function pickValidatedRead(
  byKind: Record<string, Forecast[]>,
  watchSymbols: Set<string>,
): BestRead | null {
  for (const kind of VALIDATED_ORDER) {
    const rows = (byKind[kind] ?? []).filter(
      (f) => f.conviction >= 0.5 && (watchSymbols.size === 0 || watchSymbols.has(f.symbol)),
    );
    if (rows.length === 0) continue;
    const top = [...rows].sort(
      (a, b) =>
        b.conviction - a.conviction ||
        b.historicalAccuracy - a.historicalAccuracy ||
        a.symbol.localeCompare(b.symbol),
    )[0];
    return { fc: top, persistenceOnly: kind !== "vol21" };
  }
  return null;
}

/** The experimental directional probability, kept only as a demoted footnote.
 *  Unlike the old headline this deliberately does NOT gate on n — a large n does
 *  not rescue a signal whose measured accuracy sits below its own base rate. */
function directionalNote(dash: DashboardResponse): DashWatchSpark | null {
  const sparks = dash.watchlist?.sparks ?? [];
  const ranked = sparks
    .filter((s) => s.calProb1d != null)
    .sort(
      (a, b) =>
        Math.abs((b.calProb1d as number) - 0.5) - Math.abs((a.calProb1d as number) - 0.5),
    );
  return ranked[0] ?? null;
}

function pct(x: number): string {
  return `${Math.round(x * 100)}%`;
}

export default function TodaysRead({ dash }: { dash: DashboardResponse }) {
  const [byKind, setByKind] = useState<Record<string, Forecast[]> | null>(null);
  const [failed, setFailed] = useState(false);

  // One fetch of the validated regime forecasts. Failure is non-fatal: the card
  // falls back to an honest empty state rather than to the directional number.
  useEffect(() => {
    let alive = true;
    structuralRegimes()
      .then((r) => {
        if (alive) setByKind(r.forecasts as Record<string, Forecast[]>);
      })
      .catch(() => {
        if (alive) setFailed(true);
      });
    return () => {
      alive = false;
    };
  }, []);

  const watchSymbols = useMemo(
    () => new Set((dash.watchlist?.sparks ?? []).map((s) => s.symbol)),
    [dash],
  );
  const best = useMemo(
    () => (byKind ? pickValidatedRead(byKind, watchSymbols) : null),
    [byKind, watchSymbols],
  );
  const dir = useMemo(() => directionalNote(dash), [dash]);
  const watchlistEmpty = (dash.watchlist?.sparks ?? []).length === 0;

  return (
    <section
      className="panel relative"
      style={{ borderColor: "color-mix(in srgb, var(--accent) 45%, var(--border))" }}
      aria-labelledby="todays-read-h"
    >
      <div className="panel-h flex-wrap gap-2">
        <span style={{ color: "var(--accent)" }}>TODAY&rsquo;S READ</span>
        <span className="chip px-2 py-[1px] text-[0.75rem]">
          {best ? `validated signal · ${best.fc.kind}` : "evidence gate"}
        </span>
        <Link
          href="/proof"
          className="ml-auto cursor-pointer text-[0.75rem] font-semibold tracking-wide transition-colors duration-150"
          style={{ color: "var(--accent)" }}
        >
          see the receipts →
        </Link>
      </div>

      {best ? (
        <div className="flex flex-col gap-3 px-5 py-5">
          <p
            id="todays-read-h"
            className="m-0 text-[1.05rem] leading-snug font-bold lg:text-[1.2rem]"
          >
            {best.fc.symbol}:{" "}
            <span style={{ color: "var(--accent)" }}>{best.fc.regime}</span> over the next{" "}
            <span className="tnum">{best.fc.horizonDays}</span> sessions.
          </p>

          <p
            className="m-0 max-w-[75ch] text-[0.8rem] leading-relaxed"
            style={{ color: "var(--dim)" }}
          >
            Measured accuracy in this conviction band:{" "}
            <span className="tnum font-semibold" style={{ color: "var(--text)" }}>
              {pct(best.fc.historicalAccuracy)}
            </span>{" "}
            (conviction <span className="tnum">{best.fc.conviction.toFixed(2)}</span>
            {best.fc.n > 0 ? (
              <>
                {" "}
                · n=<span className="tnum">{best.fc.n}</span>
              </>
            ) : null}
            {best.fc.tier ? ` · ${best.fc.tier}` : ""}). This is a{" "}
            {best.persistenceOnly ? "persistence statistic" : "measured-skill forecast"} about
            market <em>structure</em>, not a price target.
          </p>

          {/* the accuracy-vs-return disclosure, verbatim when the daemon ships it */}
          {best.fc.tradeability ? (
            <p
              className="m-0 max-w-[75ch] rounded-lg border px-3 py-2 text-[0.75rem] leading-relaxed"
              style={{ borderColor: "var(--warn)", color: "var(--warn)" }}
            >
              {best.fc.tradeability}
            </p>
          ) : best.persistenceOnly ? (
            <p
              className="m-0 max-w-[75ch] rounded-lg border px-3 py-2 text-[0.75rem] leading-relaxed"
              style={{ borderColor: "var(--warn)", color: "var(--warn)" }}
            >
              A high hit rate here is not a profitable trade — trend-persistence calls are
              most accurate exactly when price is most extended, and extended names
              mean-revert. Read this as situational awareness.
            </p>
          ) : null}

          <div className="flex flex-wrap items-center gap-2">
            <Link
              href={symbolHref(best.fc.symbol, best.fc.market as Market)}
              className="inline-flex min-h-[44px] w-fit cursor-pointer items-center gap-2 rounded-lg border px-4 text-[0.85rem] font-bold tracking-wide transition-colors duration-150 hover:bg-[var(--accent-dim)]"
              style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
            >
              Investigate {best.fc.symbol}
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
            <Link
              href={reportHref(best.fc.symbol, best.fc.market, best.fc.kind)}
              className="inline-flex min-h-[44px] w-fit cursor-pointer items-center text-[0.75rem] font-semibold tracking-wide transition-colors duration-150"
              style={{ color: "var(--accent)" }}
            >
              why this fired →
            </Link>
          </div>

          {/* DEMOTED: the directional probability, carrying its own negative record */}
          {dir?.calProb1d != null && (
            <p
              className="m-0 max-w-[75ch] border-t pt-3 text-[0.75rem] leading-relaxed"
              style={{ borderColor: "var(--border)", color: "var(--faint)" }}
            >
              <span className="font-bold tracking-wide">EXPERIMENTAL · not a read:</span> the
              direction model currently puts {dir.symbol} at{" "}
              <span className="tnum">{pct(dir.calProb1d)}</span> P(up, 1d). Its live forward
              record is <span className="tnum">48.1%</span> directional accuracy against a{" "}
              <span className="tnum">54.5%</span> always-guess-the-majority baseline over
              12,931 independent symbol-days — measurably worse than guessing, and raising
              its conviction makes it worse still. Shown for transparency, not to act on.{" "}
              <Link
                href="/lab/track-record"
                className="cursor-pointer font-semibold underline transition-colors duration-150"
                style={{ color: "var(--accent)" }}
              >
                the full record
              </Link>
            </p>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-2 px-5 py-6">
          <p
            id="todays-read-h"
            className="m-0 text-[1.05rem] leading-snug font-bold lg:text-[1.2rem]"
          >
            {byKind === null && !failed ? "Checking today's signals…" : "No qualified read today."}
          </p>
          <p
            className="m-0 max-w-[70ch] text-[0.8rem] leading-relaxed"
            style={{ color: "var(--dim)" }}
          >
            {failed
              ? "The validated regime forecasts are unavailable right now — the daemon may be between passes. Nothing is shown rather than falling back to the experimental direction model."
              : byKind === null
                ? "Loading the validated market-structure forecasts."
                : watchlistEmpty
                  ? "Add symbols to your watchlist and their validated volatility- and trend-regime calls will surface here as the workers score them."
                  : "Nothing on your watchlist currently carries a validated regime call above a 0.5 conviction. Below that the measured accuracy is barely better than a coin flip, so there is no read — and saying so is the point."}
          </p>
          <Link
            href="/market/regimes"
            className="mt-1 inline-flex w-fit cursor-pointer items-center gap-1.5 text-[0.75rem] font-semibold tracking-wide transition-colors duration-150"
            style={{ color: "var(--accent)" }}
          >
            browse all validated regime forecasts →
          </Link>
        </div>
      )}
    </section>
  );
}
