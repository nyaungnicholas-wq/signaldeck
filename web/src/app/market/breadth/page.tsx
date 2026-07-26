"use client";

// MARKET › BREADTH — the same structural calls, read across the market instead
// of one name at a time.
//
// The page leads with BREADTH because that is the only thing this view adds. A
// single sector reading "elevated" is noise and was already visible on the
// per-symbol surfaces; eleven of eleven reading elevated is a market state, and
// nothing in the platform could show that before. So the breadth bars come
// first and the per-basket rows come second.
//
// Two things are stated rather than implied: the accuracy tiers were measured
// on single stocks and are INHERITED here unadjusted, and any basket without a
// call is named. A sector silently dropped from a market-wide read looks like a
// neutral call, which is a claim nobody made.

import { useEffect, useState } from "react";
import {
  api,
  pollMs,
  POLL_SLOW,
  type MarketRegimesPayload,
  type MarketRegimeRow,
} from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";

const pct = (v: number) => `${(v * 100).toFixed(1)}%`;

export default function MarketBreadthPage() {
  const [data, setData] = useState<MarketRegimesPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .marketRegimes()
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // The regime runner writes every six hours; polling faster re-renders the
    // same call.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  if (err) return <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />;
  if (!data) return <Skeleton lines={8} />;

  const indices = data.rows.filter((r) => r.group === "index");
  const sectors = data.rows.filter((r) => r.group === "sector");
  const kinds = Object.keys(data.breadth).sort();

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">BREADTH</h1>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {data.indices} indices · {data.sectors} sectors
        </span>
      </div>

      <PagePurpose
        id="market-breadth"
        text={
          "The trend, volatility and liquidity calls you already get per symbol, pointed at the index and sector baskets " +
          "and grouped so the market-wide picture is one glance. No new predictor and no new claim — these calls existed, " +
          "spread across hundreds of single names where nothing market-wide was visible."
        }
      />

      {/* Breadth first: the number this page exists to show. */}
      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <h2
          className="mb-1 text-[0.7rem] font-bold tracking-[0.16em]"
          style={{ color: "var(--faint)" }}
        >
          SECTOR BREADTH
        </h2>
        <p className="mb-3 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          How the sector baskets split within each kind. One sector on its own is noise; a lopsided
          split is a market state.
        </p>
        {kinds.length === 0 ? (
          <p className="text-[0.8rem]">
            No sector calls yet — the regime runner writes every six hours, and newly registered
            baskets need daily bars before they get one.
          </p>
        ) : (
          <div className="flex flex-col gap-3">
            {kinds.map((k) => (
              <BreadthRow key={k} kind={k} counts={data.breadth[k]} />
            ))}
          </div>
        )}
      </section>

      <RegimeTable title="INDICES" rows={indices} />
      <RegimeTable title="SECTORS" rows={sectors} />

      {data.uncovered && data.uncovered.length > 0 && (
        <section
          className="rounded border p-3 text-[0.78rem] leading-relaxed"
          style={{ borderColor: "var(--line)", background: "var(--panel)" }}
        >
          <b>No call yet:</b> {data.uncovered.join(", ")}. Named rather than omitted — a sector
          quietly missing from a market-wide read looks like a neutral call, which is a claim nobody
          made. Newly registered baskets need daily bars before the regime runner can score them.
        </section>
      )}

      <ProOnly>
        <section
          className="rounded border p-3"
          style={{ borderColor: "var(--line)", background: "var(--panel)" }}
        >
          <h2
            className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]"
            style={{ color: "var(--faint)" }}
          >
            WHAT THESE NUMBERS ARE
          </h2>
          <dl className="flex flex-col gap-2 text-[0.75rem] leading-relaxed">
            <Note term="How to read it" text={data.howToRead} />
            <Note term="Where the accuracy came from" text={data.inheritedAccuracy} />
            <Note term="Why this page exists" text={data.whyThisExists} />
          </dl>
        </section>
      </ProOnly>

      <p
        className="rounded border p-3 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--line)", color: "var(--faint)" }}
      >
        {data.tradeability}
      </p>
    </div>
  );
}

/** One kind's split across the sector baskets, as a proportional bar. */
function BreadthRow({ kind, counts }: { kind: string; counts: Record<string, number> }) {
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1]);
  const total = entries.reduce((s, [, n]) => s + n, 0);
  if (total === 0) return null;
  return (
    <div>
      <div className="mb-1 flex items-baseline gap-2 text-[0.75rem]">
        <span className="font-bold">{kind}</span>
        <span style={{ color: "var(--faint)" }}>
          {entries.map(([label, n]) => `${n} ${label}`).join(" · ")}
        </span>
      </div>
      <div className="flex h-2 w-full overflow-hidden rounded" style={{ background: "var(--line)" }}>
        {entries.map(([label, n], i) => (
          <div
            key={label}
            title={`${label}: ${n} of ${total}`}
            style={{
              width: `${(n / total) * 100}%`,
              background: i === 0 ? "var(--fg)" : "var(--faint)",
              opacity: i === 0 ? 0.85 : 0.45,
            }}
          />
        ))}
      </div>
    </div>
  );
}

function RegimeTable({ title, rows }: { title: string; rows: MarketRegimeRow[] }) {
  if (rows.length === 0) return null;
  return (
    <section
      className="rounded border p-3"
      style={{ borderColor: "var(--line)", background: "var(--panel)" }}
    >
      <h2
        className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]"
        style={{ color: "var(--faint)" }}
      >
        {title}
      </h2>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[32rem] text-[0.75rem]">
          <thead>
            <tr style={{ color: "var(--faint)" }}>
              <th className="text-left font-normal">basket</th>
              <th className="text-left font-normal">kind</th>
              <th className="text-left font-normal">call</th>
              <th className="text-right font-normal">conviction</th>
              <th className="text-left font-normal">band</th>
              <th className="text-right font-normal">
                <span className="inline-flex items-center gap-1">
                  band accuracy
                  <HelpTip label="band accuracy">
                    Measured on single stocks, not on baskets. Inherited unchanged rather than
                    adjusted by a guess.
                  </HelpTip>
                </span>
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={`${r.symbol}-${r.kind}`} style={{ borderTop: "1px solid var(--line)" }}>
                <td className="py-1">
                  <span className="font-bold">{r.symbol}</span>{" "}
                  <span style={{ color: "var(--faint)" }}>{r.name}</span>
                </td>
                <td className="py-1">{r.kind}</td>
                <td className="py-1">{r.regime}</td>
                <td className="py-1 text-right">{r.conviction.toFixed(2)}</td>
                <td className="py-1">{r.tier}</td>
                <td className="py-1 text-right">
                  {r.historicalAccuracy > 0 ? pct(r.historicalAccuracy) : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Note({ term, text }: { term: string; text: string }) {
  return (
    <div>
      <dt className="text-[0.68rem] font-bold tracking-wide" style={{ color: "var(--faint)" }}>
        {term}
      </dt>
      <dd>{text}</dd>
    </div>
  );
}
