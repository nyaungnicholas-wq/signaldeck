"use client";

// MARKET › MACRO — the 2026-07-18 merge of the old /markets/macro and
// /markets/regimes pages into ONE compact read (user: "regime is messy, macro
// too — combine those two"). Three bands: MARKET STATE stat tiles, the fleet
// REGIME MAP with recent transitions, and the SECTOR ROTATION strip. Every
// number keeps its source note; the validated per-symbol regime forecasts
// stay on the REGIMES tab — this page is the fleet-level weather report.

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  api,
  type Macro,
  type RegimeResponse,
  type SectorAgg,
} from "@/lib/api";
import { ago, fmtPct } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";

const REGIME_COLORS: Record<string, string> = {
  uptrend: "var(--bid)",
  downtrend: "var(--ask)",
  range: "var(--dim)",
  squeeze: "var(--accent)",
};

function StatTile({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="panel flex flex-col items-center gap-1 px-4 py-3">
      <span className="tnum text-[1.5rem] font-bold leading-none">{value}</span>
      <span className="text-[0.7rem] font-medium tracking-[0.14em]" style={{ color: "var(--dim)" }}>
        {label}
      </span>
      {sub ? (
        <span className="text-center text-[0.7rem] leading-snug" style={{ color: "var(--faint)" }}>
          {sub}
        </span>
      ) : null}
    </div>
  );
}

export default function MacroCombinedPage() {
  const [macro, setMacro] = useState<Macro | null>(null);
  const [regime, setRegime] = useState<RegimeResponse | null>(null);
  const [sectors, setSectors] = useState<SectorAgg[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let dead = false;
    const pull = () => {
      api.macro().then((m) => !dead && setMacro(m)).catch((e: unknown) => !dead && setErr(String(e)));
      api.regime().then((r) => !dead && setRegime(r)).catch(() => undefined);
      api.sectors().then((s) => !dead && setSectors(s)).catch(() => undefined);
    };
    pull();
    const t = setInterval(pull, 120_000);
    return () => {
      dead = true;
      clearInterval(t);
    };
  }, []);

  const states = regime?.states ?? [];
  const dist: Record<string, number> = {};
  for (const s of states) dist[s.label] = (dist[s.label] ?? 0) + 1;
  const changes = (regime?.changes ?? []).slice(0, 10);

  return (
    <main className="mx-auto max-w-5xl space-y-4 px-4 py-6">
      <h1 className="text-lg font-semibold">MACRO &amp; REGIME</h1>
      <PagePurpose
        id="market-macro"
        text="The fleet-level weather report: market breadth and volatility state, the regime map across every tracked symbol, and sector rotation — compact by design. Per-symbol validated regime forecasts live on the REGIMES tab."
      />
      {err && !macro ? <ErrorState message={err} /> : null}
      {!macro && !err ? <Skeleton lines={8} /> : null}

      {macro ? (
        <section aria-label="market state">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <StatTile
              label="BREADTH"
              value={`${macro.breadthPct.toFixed(1)}%`}
              sub={`${macro.positive} of ${macro.scored} scored positive`}
            />
            <StatTile
              label="VOLATILITY"
              value={macro.volLabel || "—"}
              sub={`SPY realized-vol percentile ${macro.volPct.toFixed(0)}%`}
            />
            <StatTile
              label="REGIME MAP"
              value={String(states.length || "—")}
              sub="symbols classified below"
            />
            <StatTile
              label="AS OF"
              value={ago(macro.asOf)}
              sub="stored data, worker cadence"
            />
          </div>
          <p className="mt-1 text-[0.7rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {macro.note}
          </p>
        </section>
      ) : null}

      {states.length > 0 ? (
        <section className="panel" aria-label="regime map">
          <div className="panel-h">
            REGIME MAP
            <span className="font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
              trend/range/squeeze classification per symbol
            </span>
            <Link href="/market/regimes" className="ml-auto text-[0.7rem] hover:text-[var(--accent)]">
              validated regime forecasts →
            </Link>
          </div>
          <div className="flex flex-wrap gap-2 px-4 py-3">
            {Object.entries(dist)
              .sort((a, b) => b[1] - a[1])
              .map(([label, n]) => (
                <span
                  key={label}
                  className="chip tnum px-3 py-1"
                  style={{ color: REGIME_COLORS[label] ?? "var(--text)" }}
                >
                  {label}: {n}
                </span>
              ))}
          </div>
          {changes.length > 0 ? (
            <div className="border-t px-4 py-2" style={{ borderColor: "var(--border)" }}>
              <span className="text-[0.7rem] tracking-wider" style={{ color: "var(--faint)" }}>
                RECENT TRANSITIONS
              </span>
              <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
                {changes.map((c) => (
                  <span key={`${c.symbol}-${c.ts}`} className="text-[0.75rem]">
                    <Link
                      href={`/s/stocks/${encodeURIComponent(c.symbol)}`}
                      className="mono font-bold hover:text-[var(--accent)]"
                    >
                      {c.symbol}
                    </Link>{" "}
                    <span style={{ color: "var(--faint)" }}>
                      {c.from} → {c.to} · {ago(c.ts)}
                    </span>
                  </span>
                ))}
              </div>
            </div>
          ) : null}
        </section>
      ) : null}

      {sectors && sectors.length > 0 ? (
        <section className="panel" aria-label="sector rotation">
          <div className="panel-h">
            SECTOR ROTATION
            <span className="font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
              strongest → weakest by mean score
            </span>
          </div>
          <div className="table-wrap">
            <table className="w-full text-[0.75rem]">
              <thead>
                <tr style={{ borderBottom: "1px solid var(--border)" }}>
                  {["SECTOR", "MEAN SCORE", "1M RETURN", "N"].map((h, i) => (
                    <th
                      key={h}
                      className={`px-3 py-1.5 font-medium tracking-wide ${i === 0 ? "text-left" : "text-right"}`}
                      style={{ color: "var(--faint)" }}
                    >
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="tnum">
                {[...sectors]
                  .sort((a, b) => b.MeanScore - a.MeanScore)
                  .map((s) => (
                    <tr key={s.Sector} style={{ borderBottom: "1px solid var(--border)" }}>
                      <td className="px-3 py-1.5 font-medium">{s.Sector}</td>
                      <td className="px-3 py-1.5 text-right">{s.MeanScore.toFixed(3)}</td>
                      <td
                        className="px-3 py-1.5 text-right"
                        style={{ color: s.MeanRet1M >= 0 ? "var(--bid)" : "var(--ask)" }}
                      >
                        {fmtPct(s.MeanRet1M * 100)}
                      </td>
                      <td className="px-3 py-1.5 text-right" style={{ color: "var(--faint)" }}>
                        {s.N}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
    </main>
  );
}
