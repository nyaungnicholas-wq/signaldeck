"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  structuralRegimesWithEarnings,
  volRegime,
  type EarningsWindowLabel,
  type StructRegimeForecast,
  type StructRegimesWithEarnings,
} from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import ExportMenu from "@/components/ExportMenu";
import { Reveal, StatTile, PageHero, MiniBar } from "@/components/ui/Kit";

const KIND_ORDER = ["trend21", "liquidity21", "vol21"] as const;

const KIND_TITLES: Record<string, string> = {
  trend21: "TREND",
  liquidity21: "LIQUIDITY",
  vol21: "VOLATILITY",
};

const SHOW_N = 12;

function pct(x: number): string {
  return `${(x * 100).toFixed(1)}%`;
}

function regimeColor(regime: string): string {
  if (/up|active|elevated|fill/i.test(regime)) return "var(--bid)";
  if (/down|quiet|calm|decrease/i.test(regime)) return "var(--ask)";
  return "var(--dim)";
}

function regimeChip(regime: string) {
  const color = regimeColor(regime);
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[0.75rem] font-medium"
      style={{ color, borderColor: `color-mix(in srgb, ${color} 40%, transparent)` }}
    >
      {regime}
    </span>
  );
}

function FragmentRow({
  f,
  isOpen,
  onToggle,
  ew,
  earningsNote,
}: {
  f: StructRegimeForecast;
  isOpen: boolean;
  onToggle: () => void;
  ew?: EarningsWindowLabel;
  earningsNote?: string;
}) {
  const reportHref = `/signals/report/${f.market}/${encodeURIComponent(f.symbol)}?kind=${f.kind}`;
  return (
    <>
      <tr
        className="cursor-pointer border-t border-white/5 hover:bg-white/[0.02]"
        onClick={onToggle}
      >
        <td className="w-6 py-2 pr-1">
          <button
            type="button"
            aria-expanded={isOpen}
            aria-label={`${isOpen ? "collapse" : "expand"} details for ${f.symbol}`}
            onClick={(e) => {
              e.stopPropagation();
              onToggle();
            }}
            className="cursor-pointer text-white/40 hover:text-white/80"
          >
            {isOpen ? "▾" : "▸"}
          </button>
        </td>
        <td className="py-2 pr-2">
          <Link
            className="text-white hover:underline mono"
            href={`/s/${f.market}/${encodeURIComponent(f.symbol)}`}
            onClick={(e) => e.stopPropagation()}
          >
            {f.symbol}
          </Link>
          {ew?.withinWindow ? (
            <span
              className="ml-1.5 rounded border border-amber-400/50 px-1 py-[1px] text-[0.625rem] text-amber-300"
              title={earningsNote ?? "estimated earnings inside the next 7 days"}
            >
              E~{ew.daysUntil}d
            </span>
          ) : null}
        </td>
        <td className="py-2 pr-2">
          <Link
            className="hover:underline"
            href={reportHref}
            onClick={(e) => e.stopPropagation()}
          >
            {regimeChip(f.regime)}
          </Link>
        </td>
        <td className="py-2 pr-2">
          <div className="flex items-center gap-2">
            <MiniBar value={f.conviction} max={1} color={regimeColor(f.regime)} height={4} />
            <span className="tnum text-white/80">{pct(f.conviction)}</span>
          </div>
        </td>
        <td className="py-2 pr-2">
          <div className="flex items-center gap-2">
            <MiniBar value={f.historicalAccuracy} max={1} color="var(--accent)" height={4} />
            <span className="tnum text-white/80">{pct(f.historicalAccuracy)}</span>
          </div>
          {/* The sample the tier was measured on, shown wherever the daemon
              recorded it. A crypto 98.5% rests on 68 rows across 4 quarters and
              should not read like the ~900-stock equity tables. Absent for the
              equity kinds because that loop never recorded per-tier sizes —
              render nothing rather than imply a number we do not have. */}
          {f.evidenceRows ? (
            <div
              className="tnum text-[10px] text-white/35"
              title={`This tier was measured on ${f.evidenceRows} samples across ${f.evidenceClusters} independent quarter blocks. Small samples move a lot on one new quarter.`}
            >
              n={f.evidenceRows}
              {f.evidenceClusters ? ` · ${f.evidenceClusters}q` : ""}
            </div>
          ) : null}
        </td>
        <td className="py-2 pr-2 text-white/60">{f.tier}</td>
        <td className="py-2 text-white/40 tnum">{ago(f.ts)}</td>
      </tr>
      {isOpen ? (
        <tr className="border-t border-white/[0.03] bg-white/[0.01]">
          <td colSpan={7} className="px-2 py-3">
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-[0.75rem] text-white/50">
              <span className="flex items-center gap-2">
                conviction
                <span className="tnum text-white/80">{pct(f.conviction)}</span>
              </span>
              <span>
                rank <span className="tnum text-white/80">#{f.rank}</span>
              </span>
              <span>
                horizon <span className="tnum text-white/80">{f.horizonDays} sessions</span>
              </span>
              <span>
                sample n=<span className="tnum text-white/80">{f.n}</span>
              </span>
              <span>
                as of{" "}
                <span className="tnum text-white/80">
                  {new Date(f.ts * 1000).toLocaleString()}
                </span>
              </span>
              <Link className="text-amber-300 hover:underline" href={reportHref}>
                detail report →
              </Link>
            </div>
          </td>
        </tr>
      ) : null}
    </>
  );
}

function toMarkdown(
  kind: string,
  list: StructRegimeForecast[],
  ews?: Record<string, EarningsWindowLabel>,
): string {
  // "backtested", not "measured" — and it matters most here, because this table
  // is EXPORTED: the number leaves the app stripped of every surrounding
  // caveat, so the column header is the only disclosure that travels with it.
  const head = "| symbol | call | conviction | backtested accuracy | tier | earnings | as of |";
  const sep = "|---|---|---|---|---|---|---|";
  const body = list.map((f) => {
    const ew = ews?.[f.symbol];
    return `| ${f.symbol} | ${f.regime} | ${pct(f.conviction)} | ${pct(f.historicalAccuracy)} | ${f.tier} | ${
      ew?.withinWindow ? `~${ew.daysUntil}d` : "—"
    } | ${new Date(f.ts * 1000).toISOString().slice(0, 10)} |`;
  });
  return [`**${kind}** — ${list.length} forecasts`, "", head, sep, ...body].join("\n");
}

function KindSection({
  kind,
  rows,
  doc,
  ews,
  earningsNote,
}: {
  kind: string;
  rows: StructRegimeForecast[];
  doc?: { what: string; accuracyTiers: Record<string, string>; caveat: string };
  ews?: Record<string, EarningsWindowLabel>;
  earningsNote?: string;
}) {
  const [showAll, setShowAll] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const list = showAll ? rows : rows.slice(0, SHOW_N);

  const topRegime = rows.length > 0
    ? rows.reduce((best, f) => f.conviction > best.conviction ? f : best, rows[0]).regime
    : null;

  return (
    <Reveal className="panel p-4">
      <div className="flex items-start justify-between gap-2 mb-3">
        <div>
          <h2 className="panel-h">{KIND_TITLES[kind] ?? kind.toUpperCase()}</h2>
          {topRegime && (
            <div className="mt-1 flex items-center gap-2">
              <span className="text-[0.75rem] text-white/40">Dominant:</span>
              {regimeChip(topRegime)}
            </div>
          )}
        </div>
        {list.length > 0 ? (
          <ExportMenu
            items={[
              {
                label: "copy as markdown",
                doneLabel: "copied",
                // `?.` — navigator.clipboard is undefined on a non-secure
                // origin, and a bare property access threw synchronously inside
                // the menu's click handler. ExportMenu now catches and reports,
                // but returning a rejected promise beats throwing.
                onClick: () =>
                  navigator.clipboard?.writeText(toMarkdown(kind, list, ews)),
              },
            ]}
          />
        ) : null}
      </div>

      {doc ? (
        <div className="mb-4 space-y-2">
          <p className="text-[0.75rem] text-white/50">{doc.what}</p>
          <div className="flex flex-wrap gap-2">
            {Object.entries(doc.accuracyTiers).map(([tier, acc]) => (
              <span
                key={tier}
                className="rounded-full bg-white/[0.05] px-2 py-0.5 text-[0.625rem] text-white/70"
              >
                {tier}: <span className="text-white/90 tnum">{acc}</span>
              </span>
            ))}
          </div>
          <p className="text-[0.75rem] text-white/40 italic">{doc.caveat}</p>
        </div>
      ) : null}

      {rows.length === 0 ? (
        <div className="py-6 text-center">
          <p className="text-[0.75rem] text-white/40">no forecasts right now</p>
          <p className="text-[0.625rem] text-white/25 mt-1">the regime runner writes rows on its 6h cadence</p>
        </div>
      ) : (
        <>
          <div className="table-wrap max-h-[420px] overflow-y-auto">
            <table className="v4-table w-full text-[0.75rem]">
              <thead>
                <tr className="text-left text-white/40">
                  <th className="sticky top-0 z-10 bg-[--bg] py-2 pr-1 font-normal w-6" aria-label="expand" />
                  <th className="sticky top-0 z-10 bg-[--bg] py-2 pr-2 font-normal">symbol</th>
                  <th className="sticky top-0 z-10 bg-[--bg] py-2 pr-2 font-normal">call</th>
                  <th className="sticky top-0 z-10 bg-[--bg] py-2 pr-2 font-normal">conviction</th>
                  <th className="sticky top-0 z-10 bg-[--bg] py-2 pr-2 font-normal">accuracy</th>
                  <th className="sticky top-0 z-10 bg-[--bg] py-2 pr-2 font-normal">tier</th>
                  <th className="sticky top-0 z-10 bg-[--bg] py-2 font-normal">as of</th>
                </tr>
              </thead>
              <tbody>
                {list.map((f) => {
                  const key = `${f.kind}-${f.symbol}`;
                  const isOpen = expanded === key;
                  return (
                    <FragmentRow
                      key={key}
                      f={f}
                      isOpen={isOpen}
                      onToggle={() => setExpanded(isOpen ? null : key)}
                      ew={ews?.[f.symbol]}
                      earningsNote={earningsNote}
                    />
                  );
                })}
              </tbody>
            </table>
          </div>
          {rows.length > SHOW_N ? (
            <button
              className="mt-3 text-[0.75rem] text-white/40 hover:text-white/80"
              onClick={() => setShowAll((v) => !v)}
            >
              {showAll ? "show fewer" : `show all ${rows.length}`}
            </button>
          ) : null}
        </>
      )}
    </Reveal>
  );
}

export default function RegimesPage() {
  const [data, setData] = useState<StructRegimesWithEarnings | null>(null);
  const [volCount, setVolCount] = useState<number | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let dead = false;
    const pull = () => {
      structuralRegimesWithEarnings()
        .then((d) => {
          if (!dead) {
            setData(d);
            setErr(null);
          }
        })
        .catch((e: unknown) => {
          if (!dead) setErr(e instanceof Error ? e.message : String(e));
        });
      volRegime()
        .then((v) => {
          if (!dead) setVolCount(v.forecasts?.length ?? 0);
        })
        .catch(() => undefined);
    };
    pull();
    const t = setInterval(pull, 120_000);
    return () => {
      dead = true;
      clearInterval(t);
    };
  }, []);

  const trendForecasts = data?.forecasts?.trend21 ?? [];
  const liqForecasts = data?.forecasts?.liquidity21 ?? [];
  const volForecasts = data?.forecasts?.vol21 ?? [];

  const currentStates = {
    trend: trendForecasts.length > 0
      ? trendForecasts.reduce((a, b) => a.conviction > b.conviction ? a : b, trendForecasts[0]).regime
      : null,
    liquidity: liqForecasts.length > 0
      ? liqForecasts.reduce((a, b) => a.conviction > b.conviction ? a : b, liqForecasts[0]).regime
      : null,
    volatility: volForecasts.length > 0
      ? volForecasts.reduce((a, b) => a.conviction > b.conviction ? a : b, volForecasts[0]).regime
      : null,
  };

  // null, not 0 — StatTile renders null as an em-dash. "0.0%" under a tile
  // labelled AVG ACCURACY on a page promising "the measured accuracy of each
  // state" reads as a model measured to be never right, when in fact nothing
  // has been measured at all.
  // Averages only forecasts that carry a number. historicalAccuracy === 0 is
  // this app's established sentinel for "not measured" — VolRegimeLead and
  // /market/breadth both render `> 0 ? pct(...) : "—"` on the same field — and
  // summing those zeros into the mean dragged the headline down with rows that
  // were never measured at all. Return null (em-dash) when nothing qualifies,
  // and expose the contributing count so the tile can say what it averaged.
  const avgAccuracy = (
    forecasts: StructRegimeForecast[],
  ): { pct: number | null; n: number } => {
    const measured = forecasts.filter((f) => f.historicalAccuracy > 0);
    if (measured.length === 0) return { pct: null, n: 0 };
    // historicalAccuracy is a 0-1 fraction; the tile renders with a % suffix.
    return {
      pct: (measured.reduce((sum, f) => sum + f.historicalAccuracy, 0) / measured.length) * 100,
      n: measured.length,
    };
  };

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Market Regimes"
        subtitle="The market's current structural state — trend, volatility and liquidity regimes, with the BACKTESTED accuracy of each state. These claims are not yet live records: no structural forecast has been graded."
        live
      />

      {err ? <ErrorState message={err} /> : null}
      {!data && !err ? <Skeleton lines={12} /> : null}

      {data ? (
        <>
          {/* Hero Band */}
          <Reveal className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {currentStates.trend && (
              <StatTile
                i={0}
                label="TREND REGIME"
                value={currentStates.trend}
                sub={`${trendForecasts.length} active forecasts`}
                glow="hud"
              />
            )}
            {currentStates.liquidity && (
              <StatTile
                i={1}
                label="LIQUIDITY REGIME"
                value={currentStates.liquidity}
                sub={`${liqForecasts.length} active forecasts`}
                glow="hud"
              />
            )}
            {currentStates.volatility && (
              <StatTile
                i={2}
                label="VOLATILITY REGIME"
                value={currentStates.volatility}
                sub={`${volForecasts.length} active forecasts`}
                glow="hud"
              />
            )}
            {/* "BACKTESTED", not "MEASURED": these are constants from
                structregime.go's offline lookup table, and all 37,857
                structural rows have resolved_at NULL — nothing here has ever
                been graded. sub= says how many forecasts actually carried a
                number, so an average over 3 of 40 cannot read as an average
                over 40. */}
            {(() => {
              const acc = avgAccuracy(trendForecasts.concat(liqForecasts).concat(volForecasts));
              return (
                <StatTile
                  i={3}
                  label="AVG BACKTESTED ACCURACY"
                  value={acc.pct}
                  decimals={1}
                  suffix="%"
                  sub={acc.n > 0 ? `over ${acc.n} measured forecast(s)` : "none measured yet"}
                  glow="accent"
                />
              );
            })()}
          </Reveal>

          {/* How to read this */}
          <Reveal className="panel p-4">
            <h2 className="panel-h mb-2">How to read this</h2>
            <p className="text-[0.75rem] text-white/50 leading-relaxed">
              Market regimes classify structural conditions across trend, liquidity, and volatility. 
              Each regime classification comes with a conviction score (% confidence) and measured historical accuracy
              from walk-forward testing — these numbers are transparently displayed for each forecast tier.
              Regimes persist until the underlying data shifts, making persistence itself a signal of market structure stability.
            </p>
          </Reveal>

          {/* Regime Sections */}
          <div className="space-y-3">
            {KIND_ORDER.map((k) => (
              <KindSection
                key={k}
                kind={k}
                rows={data.forecasts[k] ?? []}
                doc={data.kinds?.[k]}
                ews={data.earningsWindows}
                earningsNote={data.earningsNote}
              />
            ))}
          </div>

          {/* Methodology note */}
          <Reveal className="panel p-4">
            <div className="flex items-start gap-3">
              <div className="flex-1">
                <p className="text-[0.75rem] text-white/40 leading-relaxed">
                  {data.methodology}{" "}
                  <Link className="text-amber-300/80 hover:underline" href="/lab/research">
                    evidence lives in the research ledger →
                  </Link>
                </p>
                {volCount != null && (
                  <p className="text-[0.75rem] text-white/30 mt-1">
                    The quarterly vol regime ({volCount} live forecasts) stays on its own surface:{" "}
                    <span className="mono text-white/50">/api/vol-regime</span>.
                  </p>
                )}
              </div>
            </div>
          </Reveal>
        </>
      ) : null}
    </div>
  );
}
