"use client";

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
import { PageHero, StatTile, Reveal } from "@/components/ui/Kit";

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
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  // `err && !data`, not bare `err`. The page polls on POLL_SLOW and keeps good
  // `data` across a failed poll, so one transient failure replaced a fully
  // loaded page with a full-screen error — and since only a SUCCESSFUL poll
  // clears err, it stayed that way for up to two minutes (five under backoff).
  // With data in hand the failure belongs in a chip, not in place of the page.
  if (err && !data) return <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />;
  if (!data) return <Skeleton lines={8} />;

  const indices = data.rows.filter((r) => r.group === "index");
  const sectors = data.rows.filter((r) => r.group === "sector");
  const kinds = Object.keys(data.breadth).sort();
  const firstKind = kinds[0];
  const firstCounts = firstKind ? data.breadth[firstKind] : null;
  const sorted = firstCounts ? Object.entries(firstCounts).sort((a, b) => b[1] - a[1]) : [];
  const total = sorted.reduce((sum, [, n]) => sum + n, 0);
  const topLabel = sorted[0]?.[0] ?? "";
  const topCount = sorted[0]?.[1] ?? 0;
  const bottomLabel = sorted[1]?.[0] ?? "";
  const bottomCount = sorted[1]?.[1] ?? 0;
  const pctTop = total > 0 ? (topCount / total) * 100 : 0;
  const pctBottom = total > 0 ? (bottomCount / total) * 100 : 0;

  const verdict = total === 0 ? "No data" :
    pctTop > 66 ? "Broad advance" : pctTop > 50 ? "Narrow rally" :
    pctBottom > 66 ? "Broad decline" : "Mixed signals";

  // Every row that actually carries a number. Testing only rows[0] let a single
  // measured first row unlock a tile that then averaged EVERY row — including
  // rows the table below renders as "—" because historicalAccuracy === 0 means
  // not measured. A row shown as no-data upstairs cannot count as 0% downstairs.
  const measuredRows = data.rows.filter((r) => r.historicalAccuracy > 0);
  const hasAccuracy = measuredRows.length > 0;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Breadth"
        subtitle="Is the whole market moving, or just a few big names? A rally led by five stocks is a different thing from one led by five hundred."
        // Hiding the page on a failed poll was wrong, but so is showing stale
        // numbers with nothing saying they are stale. Same chip the intel pages use.
        right={err !== null ? <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>poll failed — showing last data</span> : undefined}
      />

      {firstCounts && (
        <div className="hud-panel p-4">
          <div className="flex flex-col gap-2">
            <div className="flex justify-between text-[0.75rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>
              <span>{topLabel}</span>
              <span>{bottomLabel}</span>
            </div>
            <div className="relative h-8 w-full rounded" style={{ background: 'rgba(255,255,255,0.06)' }}>
              <div
                className="absolute left-0 top-0 h-full rounded-l bar-animate"
                style={{
                  width: `${pctTop}%`,
                  backgroundColor: 'var(--bid)',
                  transformOrigin: 'left',
                }}
              />
              <div
                className="absolute right-0 top-0 h-full rounded-r bar-animate"
                style={{
                  width: `${pctBottom}%`,
                  backgroundColor: 'var(--ask)',
                  transformOrigin: 'right',
                }}
              />
            </div>
            <div className="flex justify-between items-baseline">
              <span className="num-hero text-2xl glow-up">{pctTop.toFixed(1)}%</span>
              <span className="text-sm font-medium" style={{ color: 'var(--hud)' }}>{verdict}</span>
              <span className="num-hero text-2xl glow-down">{pctBottom.toFixed(1)}%</span>
            </div>
          </div>
        </div>
      )}

      <Reveal className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="Indices"
          value={data.indices}
          sub="tracked baskets"
          glow="hud"
          i={0}
        />
        <StatTile
          label="Sectors"
          value={data.sectors}
          sub="tracked baskets"
          glow="hud"
          i={1}
        />
        {hasAccuracy && (
          <StatTile
            label="Avg Backtested Accuracy"
            value={
              (measuredRows.reduce((sum, r) => sum + r.historicalAccuracy, 0) /
                measuredRows.length) *
              100
            }
            decimals={1}
            suffix="%"
            sub={`across ${measuredRows.length} of ${data.rows.length} baskets`}
            glow="accent"
            i={2}
          />
        )}
        {firstCounts && (
          <StatTile
            label="Top Call"
            value={topCount}
            sub={`${topLabel} (${(total > 0 ? (topCount / total) * 100 : 0).toFixed(1)}%)`}
            glow="up"
            i={3}
          />
        )}
      </Reveal>

      {kinds.length > 0 && (
        <section className="panel p-4">
          <div className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]" style={{ color: 'var(--dim)' }}>
            SECTOR BREADTH
          </div>
          <div className="mb-3 text-[0.72rem] leading-relaxed" style={{ color: 'var(--dim)' }}>
            How sector baskets split within each category — a lopsided split signals a market-wide state.
          </div>
          <div className="flex flex-col gap-3">
            {kinds.map((k, i) => (
              <BreadthRow key={k} kind={k} counts={data.breadth[k]} i={i} />
            ))}
          </div>
        </section>
      )}

      <RegimeTable title="Indices" rows={indices} />
      <RegimeTable title="Sectors" rows={sectors} />

      {data.uncovered && data.uncovered.length > 0 && (
        <div className="panel p-3 text-[0.78rem] leading-relaxed" style={{ color: 'var(--dim)' }}>
          <span className="font-bold">No call yet:</span> {data.uncovered.join(", ")}. Named rather than omitted.
        </div>
      )}
    </div>
  );
}

function BreadthRow({ kind, counts, i }: { kind: string; counts: Record<string, number>; i: number }) {
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1]);
  const total = entries.reduce((s, [, n]) => s + n, 0);
  if (total === 0) return null;

  return (
    <div className="reveal-item" style={{ "--i": i } as React.CSSProperties}>
      <div className="mb-1 flex items-baseline gap-2 text-[0.75rem]">
        <span className="font-bold">{kind}</span>
        <span style={{ color: 'var(--dim)' }}>
          {entries.map(([label, n]) => `${n} ${label}`).join(" · ")}
        </span>
      </div>
      <div className="flex h-2 w-full overflow-hidden rounded" style={{ background: 'rgba(255,255,255,0.06)' }}>
        {entries.map(([label, n], idx) => (
          <div
            key={label}
            title={`${label}: ${n} of ${total}`}
            className="bar-animate"
            style={{
              width: `${(n / total) * 100}%`,
              backgroundColor: idx === 0 ? 'var(--bid)' : idx === 1 ? 'var(--ask)' : 'var(--faint)',
              opacity: idx < 2 ? 0.85 : 0.45,
              transformOrigin: 'left',
              "--i": i,
            } as React.CSSProperties}
          />
        ))}
      </div>
    </div>
  );
}

function RegimeTable({ title, rows }: { title: string; rows: MarketRegimeRow[] }) {
  if (rows.length === 0) return null;
  const subtitle = title === "Indices"
    ? "Market-wide indices and their regime status"
    : "Sector baskets and their regime status";

  return (
    <Reveal className="panel p-4">
      <div className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]" style={{ color: 'var(--dim)' }}>
        {title.toUpperCase()}
      </div>
      <div className="mb-3 text-[0.72rem] leading-relaxed" style={{ color: 'var(--dim)' }}>
        {subtitle}
      </div>
      <div className="table-wrap overflow-x-auto">
        <table className="v4-table w-full min-w-[32rem]">
          <thead>
            <tr>
              <th>Basket</th>
              <th>Kind</th>
              <th>Call</th>
              <th className="text-right">Conviction</th>
              <th>Band</th>
              <th className="text-right">Accuracy</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const callColor = r.regime === "bullish" || r.regime === "elevated" ? "var(--bid)" :
                r.regime === "bearish" || r.regime === "depressed" ? "var(--ask)" : "var(--dim)";
              return (
                <tr key={`${r.symbol}-${r.kind}`}>
                  <td>
                    <span className="font-bold">{r.symbol}</span>{" "}
                    <span style={{ color: 'var(--dim)' }}>{r.name}</span>
                  </td>
                  <td>{r.kind}</td>
                  <td style={{ color: callColor }}>{r.regime}</td>
                  <td className="text-right tnum">{r.conviction.toFixed(2)}</td>
                  <td>{r.tier}</td>
                  <td className="text-right">
                    {r.historicalAccuracy > 0 ? `${(r.historicalAccuracy * 100).toFixed(1)}%` : "—"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Reveal>
  );
}
