"use client";

// REGIMES — the 2026-07-17 alpha-discovery loop's validated forecasts beyond
// the quarterly vol regime: trend21 / liquidity21 / vol21 standing regimes and
// live gap-fill events. House honesty rules: every number shown next to a call
// is the MEASURED walk-forward accuracy at that conviction tier (served by the
// daemon in-payload, never invented client-side), and each kind renders its
// caveat inline — the liquidity "persistence IS the skill" note and the trend
// survivorship note are part of the product, not footnotes.

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
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import ExportMenu from "@/components/ExportMenu";

const KIND_ORDER = ["trend21", "liquidity21", "vol21"];

const KIND_TITLES: Record<string, string> = {
  trend21: "TREND — still on this side of the 200-day average in a month?",
  liquidity21: "LIQUIDITY — active or quiet dollar volume next month?",
  vol21: "VOLATILITY (monthly) — elevated or calm next 21 sessions?",
};

const SHOW_N = 12;

function pct(x: number): string {
  return `${(x * 100).toFixed(1)}%`;
}

function regimeTone(regime: string): string {
  if (/up|active|elevated|fill/.test(regime)) return "text-emerald-400";
  return "text-sky-400";
}

// Sticky-header background: solid (not translucent) so scrolled rows never
// bleed through the header text. Matches the section's zinc-950 surface.
const TH_STICKY = "sticky top-0 z-10 bg-zinc-950 py-1 pr-2 font-normal";

/** One forecast row + its inline expandable detail strip. The whole row
 *  toggles; inner links stopPropagation so navigation still works. */
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
  /** Earnings-window label for this symbol (credibility wave) — a label, never a filter. */
  ew?: EarningsWindowLabel;
  earningsNote?: string;
}) {
  const reportHref = `/signals/report/${f.market}/${encodeURIComponent(f.symbol)}?kind=${f.kind}`;
  return (
    <>
      <tr
        className="cursor-pointer border-t border-zinc-900 hover:bg-zinc-900/40"
        onClick={onToggle}
      >
        <td className="w-6 py-1 pr-1">
          <button
            type="button"
            aria-expanded={isOpen}
            aria-label={`${isOpen ? "collapse" : "expand"} details for ${f.symbol}`}
            onClick={(e) => {
              e.stopPropagation();
              onToggle();
            }}
            className="cursor-pointer text-zinc-500 hover:text-zinc-200"
          >
            {isOpen ? "▾" : "▸"}
          </button>
        </td>
        <td className="py-1 pr-2">
          <Link
            className="text-zinc-100 hover:underline"
            href={`/s/${f.market}/${encodeURIComponent(f.symbol)}`}
            onClick={(e) => e.stopPropagation()}
          >
            {f.symbol}
          </Link>
          {ew?.withinWindow ? (
            <span
              className="ml-1.5 rounded border border-amber-400/50 px-1 py-[1px] text-[10px] text-amber-300"
              title={earningsNote ?? "estimated earnings inside the next 7 days"}
            >
              E~{ew.daysUntil}d
            </span>
          ) : null}
        </td>
        <td className={`py-1 pr-2 ${regimeTone(f.regime)}`}>
          <Link
            className="hover:underline"
            href={reportHref}
            title="open the detail report for this signal"
            onClick={(e) => e.stopPropagation()}
          >
            {f.regime} →
          </Link>
        </td>
        <td className="py-1 pr-2 text-zinc-300">{pct(f.conviction)}</td>
        <td className="py-1 pr-2 text-zinc-100">{pct(f.historicalAccuracy)}</td>
        <td className="py-1 pr-2 text-zinc-400">{f.tier}</td>
        <td className="py-1 text-zinc-500">{ago(f.ts)}</td>
      </tr>
      {isOpen ? (
        <tr className="border-t border-zinc-900/60 bg-zinc-900/30">
          <td colSpan={7} className="px-2 py-2">
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-[0.75rem] text-zinc-400">
              <span className="flex items-center gap-2">
                conviction
                <span
                  className="inline-block h-1.5 w-32 overflow-hidden rounded-full bg-zinc-800"
                  role="img"
                  aria-label={`conviction ${pct(f.conviction)}`}
                >
                  <span
                    className="block h-full rounded-full bg-amber-400"
                    style={{ width: `${Math.max(0, Math.min(1, f.conviction)) * 100}%` }}
                  />
                </span>
                <span className="text-zinc-200">{pct(f.conviction)}</span>
              </span>
              <span>
                rank <span className="text-zinc-200">#{f.rank}</span>
              </span>
              <span>
                horizon <span className="text-zinc-200">{f.horizonDays} sessions</span>
              </span>
              <span>
                sample n=<span className="text-zinc-200">{f.n}</span>
              </span>
              <span>
                as of{" "}
                <span className="text-zinc-200">
                  {new Date(f.ts * 1000).toLocaleString()}
                </span>
              </span>
              <Link className="text-amber-300/90 hover:underline" href={reportHref}>
                detail report →
              </Link>
            </div>
          </td>
        </tr>
      ) : null}
    </>
  );
}

/** Serializes the currently-VISIBLE slice of a kind's forecast table to a
 *  markdown table (export unification #23) — what you see is what you copy. */
function toMarkdown(
  kind: string,
  list: StructRegimeForecast[],
  ews?: Record<string, EarningsWindowLabel>,
): string {
  const head = "| symbol | call | conviction | measured accuracy | tier | earnings | as of |";
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
  // Compaction UX: each row expands in place to a detail strip (conviction
  // bar, rank, horizon, exact as-of, report link) instead of cramming every
  // column into the dense table.
  const [expanded, setExpanded] = useState<string | null>(null);
  const list = showAll ? rows : rows.slice(0, SHOW_N);
  return (
    <section className="rounded border border-zinc-800 bg-zinc-950/60 p-4">
      <div className="flex items-start justify-between gap-2">
        <h2 className="text-sm font-semibold tracking-wide text-zinc-200">
          {KIND_TITLES[kind] ?? kind.toUpperCase()}
        </h2>
        {list.length > 0 ? (
          <ExportMenu
            items={[
              {
                label: "copy as markdown",
                doneLabel: "copied",
                onClick: () =>
                  navigator.clipboard.writeText(toMarkdown(kind, list, ews)),
              },
            ]}
          />
        ) : null}
      </div>
      {doc ? (
        <>
          <p className="mt-1 text-xs text-zinc-400">{doc.what}</p>
          <div className="mt-2 flex flex-wrap gap-2">
            {Object.entries(doc.accuracyTiers).map(([tier, acc]) => (
              <span
                key={tier}
                className="rounded bg-zinc-900 px-2 py-0.5 text-[11px] text-zinc-300"
              >
                {tier}: <span className="text-zinc-100">{acc}</span>
              </span>
            ))}
          </div>
          <p className="mt-2 text-[11px] leading-relaxed text-amber-200/70">
            {doc.caveat}
          </p>
        </>
      ) : null}
      {rows.length === 0 ? (
        <div className="mt-3">
          <EmptyState
            message="no forecasts right now"
            detail="the regime runner writes rows on its 6h cadence; thin history refuses honestly"
          />
        </div>
      ) : (
        <>
          {/* .table-wrap = horizontal scroll on narrow screens; the capped
              height gives the sticky header something to stick inside. */}
          <div className="table-wrap mt-3 max-h-[420px] overflow-y-auto">
            <table className="w-full text-xs leading-tight">
              <thead>
                <tr className="text-left text-zinc-500">
                  <th className={TH_STICKY} aria-label="expand" />
                  <th className={TH_STICKY}>symbol</th>
                  <th className={TH_STICKY}>call</th>
                  <th className={TH_STICKY}>conviction</th>
                  <th className={TH_STICKY}>measured accuracy</th>
                  <th className={TH_STICKY}>tier</th>
                  <th className="sticky top-0 z-10 bg-zinc-950 py-1 font-normal">as of</th>
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
              className="mt-2 text-[11px] text-zinc-400 hover:text-zinc-200"
              onClick={() => setShowAll((v) => !v)}
            >
              {showAll ? "show fewer" : `show all ${rows.length}`}
            </button>
          ) : null}
        </>
      )}
    </section>
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

  return (
    <main className="mx-auto max-w-5xl space-y-4 px-4 py-6">
      <h1 className="text-lg font-semibold text-zinc-100">REGIMES</h1>
      <PagePurpose
        id="signals-regimes"
        text="Market-STRUCTURE predictions with measured, independently re-verified accuracy — the high-conviction tiers clear 70%+ (trend up to 97%), and every forecast reports its own conviction band's number, so a weak call honestly says a weak number. Not price direction: that tops out ~52-55%, re-proven in the same research loop. Each kind carries its caveat inline."
      />
      {err ? <ErrorState message={err} /> : null}
      {!data && !err ? <Skeleton lines={12} /> : null}
      {data ? (
        <>
          <p className="text-[11px] leading-relaxed text-zinc-500">
            {data.methodology}{" "}
            <Link className="text-zinc-300 hover:underline" href="/lab/research">
              evidence lives in the research ledger →
            </Link>
            {volCount != null ? (
              <>
                {" "}
                The quarterly vol regime ({volCount} live forecasts) stays on its own
                surface: <span className="text-zinc-400">/api/vol-regime</span>.
              </>
            ) : null}
          </p>
          {KIND_ORDER.map((k) => (
            <KindSection
              key={k}
              kind={k}
              rows={data.forecasts[k] ?? []}
              doc={data.kinds[k]}
              ews={data.earningsWindows}
              earningsNote={data.earningsNote}
            />
          ))}
        </>
      ) : null}
    </main>
  );
}
