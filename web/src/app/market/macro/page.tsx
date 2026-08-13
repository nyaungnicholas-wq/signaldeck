"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  api,
  type Macro,
  type RankedRow,
  type RegimeResponse,
  type SectorAgg,
} from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import ProOnly from "@/components/ProOnly";
import Push20Macro from "@/components/macro/Push20Macro";
import RankingTable from "@/components/regime/RankingTable";
import CalendarsCard from "@/components/CalendarsCard";
import EarningsEstCard from "@/components/EarningsEstCard";
import { PageHero, StatTile, DeltaBadge, Gauge, Reveal, AnimatedNumber } from "@/components/ui/Kit";

const REGIME_COLORS: Record<string, string> = {
  uptrend: "var(--bid)",
  downtrend: "var(--ask)",
  range: "var(--dim)",
  squeeze: "var(--accent)",
};

export default function MacroCombinedPage() {
  const [macro, setMacro] = useState<Macro | null>(null);
  const [regime, setRegime] = useState<RegimeResponse | null>(null);
  const [sectors, setSectors] = useState<SectorAgg[] | null>(null);
  const [ranking, setRanking] = useState<RankedRow[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // Which of the SECONDARY feeds failed. Named individually rather than as one
  // boolean so the banner can say what is missing instead of "something broke".
  const [failed, setFailed] = useState<string[]>([]);

  // Dedup: the poll re-runs every 2 minutes, so a persistently dead feed must
  // not grow the list without bound.
  const addFailed = (current: string[], name: string) =>
    current.includes(name) ? current : [...current, name];

  useEffect(() => {
    let dead = false;
    const pull = () => {
      // Only api.macro() used to reach `err`; the other three swallowed their
      // failures, so REGIME MAP rendered "—" under the label "symbols
      // classified" and the sectors and ranking sections simply ceased to exist
      // with nothing saying why. A section that vanishes on failure is
      // indistinguishable from one with nothing to show.
      api.macro().then((m) => !dead && setMacro(m)).catch((e: unknown) => !dead && setErr(String(e)));
      api.regime().then((r) => !dead && setRegime(r)).catch(() => !dead && setFailed((f) => addFailed(f, "regime")));
      api.sectors().then((s) => !dead && setSectors(s)).catch(() => !dead && setFailed((f) => addFailed(f, "sectors")));
      api.ranking().then((r) => !dead && setRanking(r)).catch(() => !dead && setFailed((f) => addFailed(f, "ranking")));
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

  const riskEvidence = macro ? {
    riskOn: [
      macro.breadthPct > 50 && `Breadth ${macro.breadthPct.toFixed(1)}% > 50`,
      macro.volPct < 30 && `Volatility percentile ${macro.volPct.toFixed(0)}% < 30`,
    ].filter(Boolean) as string[],
    riskOff: [
      macro.breadthPct < 50 && `Breadth ${macro.breadthPct.toFixed(1)}% < 50`,
      macro.volPct > 70 && `Volatility percentile ${macro.volPct.toFixed(0)}% > 70`,
    ].filter(Boolean) as string[]
  } : null;

  const riskScore = macro ? (macro.breadthPct + (100 - macro.volPct)) / 2 : 50;

  return (
    <main className="page-enter mx-auto max-w-5xl space-y-4 px-4 py-6">
      <PageHero title="Macro" subtitle="The backdrop every trade lives in — rates, dollar, volatility and risk appetite at a glance." />

      {err && !macro ? <ErrorState message={err} /> : null}
      {/* A failed secondary feed must not read as an empty one. Without this the
          sections below just disappear, which looks exactly like having nothing
          to show. */}
      {failed.length > 0 ? (
        <p className="text-[0.72rem]" style={{ color: "var(--ask)" }}>
          {failed.join(", ")} could not be loaded — those sections are MISSING, not empty.
        </p>
      ) : null}
      {!macro && !err ? <Skeleton lines={8} /> : null}

      {macro && (
        <Reveal className="space-y-4">
          <section className="panel p-4">
            <div className="panel-h">MARKET STATE</div>
            <p className="mb-3 text-[0.75rem]" style={{ color: 'var(--dim)' }}>Current volatility regime and market breadth readings.</p>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <StatTile label="BREADTH" value={macro.breadthPct} decimals={1} suffix="%" sub={`${macro.positive} of ${macro.scored} scored positive`} delta={macro.breadthPct - 50} spark={[macro.breadthPct]} i={0} glow={macro.breadthPct > 50 ? "up" : "down"} />
              <StatTile label="VOLATILITY" value={macro.volLabel || "—"} sub={`SPY realized-vol percentile ${macro.volPct.toFixed(0)}%`} delta={macro.volPct - 50} i={1} />
              <StatTile label="REGIME MAP" value={String(states.length || "—")} sub="symbols classified" i={2} />
              <StatTile label="AS OF" value={new Date(macro.asOf * 1000).toISOString().slice(0, 10)} sub="stored data, worker cadence" i={3} />
            </div>
            <p className="mt-2 text-[0.75rem]" style={{ color: 'var(--faint)' }}>{macro.note}</p>
          </section>

          <section className="hud-panel p-4">
            <div className="panel-h">RISK DIAL</div>
            <p className="mb-4 text-[0.75rem]" style={{ color: 'var(--dim)' }}>Synthesized risk-on/off signal from breadth and volatility.</p>
            <div className="flex flex-col items-center gap-4 sm:flex-row sm:justify-around">
              <Gauge value={riskScore} min={0} max={100} label="RISK SCORE" color={riskScore > 60 ? 'var(--bid)' : riskScore < 40 ? 'var(--ask)' : 'var(--accent)'} size={140} />
              {riskEvidence && (
                <div className="grid w-full grid-cols-2 gap-4 text-[0.75rem] sm:w-auto sm:min-w-[300px]">
                  <div className="space-y-1">
                    <div className="font-semibold uppercase tracking-wider" style={{ color: 'var(--bid)' }}>Risk-On Evidence</div>
                    {riskEvidence.riskOn.length > 0 ? riskEvidence.riskOn.map((item, i) => (
                      <div key={i} className="flex items-start gap-2" style={{ color: 'var(--dim)' }}>
                        <svg width="8" height="8" viewBox="0 0 8 8" className="mt-0.5 flex-shrink-0" style={{ color: 'var(--bid)' }}><polygon points="4,1 7,6 1,6" fill="currentColor"/></svg>
                        <span>{item}</span>
                      </div>
                    )) : <div style={{ color: 'var(--faint)' }}>No strong signals</div>}
                  </div>
                  <div className="space-y-1">
                    <div className="font-semibold uppercase tracking-wider" style={{ color: 'var(--ask)' }}>Risk-Off Evidence</div>
                    {riskEvidence.riskOff.length > 0 ? riskEvidence.riskOff.map((item, i) => (
                      <div key={i} className="flex items-start gap-2" style={{ color: 'var(--dim)' }}>
                        <svg width="8" height="8" viewBox="0 0 8 8" className="mt-0.5 flex-shrink-0" style={{ color: 'var(--ask)' }}><polygon points="4,7 7,2 1,2" fill="currentColor"/></svg>
                        <span>{item}</span>
                      </div>
                    )) : <div style={{ color: 'var(--faint)' }}>No strong signals</div>}
                  </div>
                </div>
              )}
            </div>
          </section>

          {states.length > 0 && (
            <section className="panel p-4">
              <div className="panel-h">REGIME MAP</div>
              <p className="mb-3 text-[0.75rem]" style={{ color: 'var(--dim)' }}>Trend, range, and squeeze classifications across all tracked symbols.</p>
              <div className="flex flex-wrap gap-2 mb-3">
                {Object.entries(dist)
                  .sort((a, b) => b[1] - a[1])
                  .map(([label, n]) => (
                    <span key={label} className="chip tnum px-3 py-1" style={{ color: REGIME_COLORS[label] ?? 'var(--text)' }}>
                      {label}: {n}
                    </span>
                  ))}
              </div>
              {changes.length > 0 && (
                <div>
                  <div className="text-[0.7rem] tracking-wider mb-1" style={{ color: 'var(--faint)' }}>RECENT TRANSITIONS</div>
                  <div className="flex flex-wrap gap-x-4 gap-y-1">
                    {changes.map((c) => (
                      <span key={`${c.symbol}-${c.ts}`} className="text-[0.75rem]">
                        <Link href={`/s/stocks/${encodeURIComponent(c.symbol)}`} className="mono font-bold hover:text-[var(--accent)]">
                          {c.symbol}
                        </Link>{' '}
                        <span style={{ color: 'var(--faint)' }}>
                          {c.from} → {c.to} · {ago(c.ts)}
                        </span>
                      </span>
                    ))}
                  </div>
                </div>
              )}
              <Link href="/market/regimes" className="inline-block mt-3 text-[0.7rem] hover:text-[var(--accent)]">
                validated regime forecasts →
              </Link>
            </section>
          )}

          {sectors && sectors.length > 0 && (
            <section className="panel p-4">
              <div className="panel-h">SECTOR ROTATION</div>
              <p className="mb-3 text-[0.75rem]" style={{ color: 'var(--dim)' }}>Mean scores and recent performance from strongest to weakest.</p>
              <div className="table-wrap">
                <table className="w-full text-[0.75rem]">
                  <thead>
                    <tr style={{ borderBottom: '1px solid var(--border)' }}>
                      {['SECTOR', 'MEAN SCORE', '1M RETURN', 'N'].map((h, i) => (
                        <th key={h} className={`px-3 py-1.5 font-medium tracking-wide ${i === 0 ? 'text-left' : 'text-right'}`} style={{ color: 'var(--faint)' }}>
                          {h}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="tnum">
                    {[...sectors]
                      .sort((a, b) => b.MeanScore - a.MeanScore)
                      .map((s) => (
                        <tr key={s.Sector} style={{ borderBottom: '1px solid var(--border)' }}>
                          <td className="px-3 py-1.5 font-medium">{s.Sector}</td>
                          <td className="px-3 py-1.5 text-right">
                            <AnimatedNumber value={s.MeanScore} decimals={3} />
                          </td>
                          <td className="px-3 py-1.5 text-right">
                            <DeltaBadge value={s.MeanRet1M * 100} />
                          </td>
                          <td className="px-3 py-1.5 text-right" style={{ color: 'var(--faint)' }}>
                            {s.N}
                          </td>
                        </tr>
                      ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}

          {macro && (
            <ProOnly summary="Show the PUSH-20 trader-gate detail">
              <div className="panel p-4">
                <Push20Macro data={macro.push20Macro} />
              </div>
            </ProOnly>
          )}

          {ranking && ranking.length > 0 && (
            <ProOnly summary="Show the relative-strength ranking table">
              <RankingTable rows={ranking} />
            </ProOnly>
          )}

          <CalendarsCard />
          <EarningsEstCard />
        </Reveal>
      )}
    </main>
  );
}
