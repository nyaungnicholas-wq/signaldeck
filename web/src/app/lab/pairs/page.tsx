"use client";

import { useEffect, useState } from "react";
import { api, type PairsStudyPayload, type PairsArm, type PairsCostLevel } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";
import { PageHero, StatTile, Reveal, DeltaBadge, MiniBar, Gauge, Spark, AnimatedNumber } from "@/components/ui/Kit";

const pct = (v: number) => `${(v * 100 >= 0 ? "+" : "") + (v * 100).toFixed(3)}%`;
const pctPlain = (v: number) => `${(v * 100).toFixed(1)}%`;
const rho = (v: number) => (v >= 0 ? "+" : "") + v.toFixed(3);

export default function PairsPage() {
  const [data, setData] = useState<PairsStudyPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    api
      .pairsStudy()
      .then((d) => alive && (setData(d), setErr(null)))
      .catch((e: unknown) => alive && setErr(e instanceof Error ? e.message : String(e)));
    return () => {
      alive = false;
    };
  }, [retryTick]);

  if (err) return <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />;
  if (!data) return <Skeleton lines={8} />;

  const s = data.study;
  const zero = s.costs.find((c) => c.costBps === 0) ?? s.costs[0];
  const p = s.persistence;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Pairs Cointegration Study"
        subtitle="The negative result that retired a belief about cointegration-based pair selection edge."
        right={<span className="mono text-sm" style={{ color: 'var(--dim)' }}>frozen · {s.ranOn}</span>}
      />

      <Reveal className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="selection edge / trade"
          value={zero.selectionEdge * 100}
          decimals={3}
          suffix="%"
          sub="coint minus random at zero cost"
          delta={zero.selectionEdge * 100}
          spark={s.costs.map(c => c.selectionEdge * 100)}
          glow={zero.selectionEdge > 0 ? 'up' : 'down'}
          i={0}
        />
        <StatTile
          label="selected arm, 0 bps"
          value={zero.coint.meanRet * 100}
          decimals={3}
          suffix="%"
          sub="before friction"
          delta={zero.coint.meanRet * 100}
          i={1}
        />
        <StatTile
          label="correlation rank ρ"
          value={p.corrRho}
          decimals={3}
          sub="persists across windows"
          i={2}
        />
        <StatTile
          label="cointegration rank ρ"
          value={p.cointRho}
          decimals={3}
          sub="does not persist"
          i={3}
        />
      </Reveal>

      <section className="hud-panel p-4">
        <Reveal className="flex flex-col gap-3">
          <div className="panel-h flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-bold tracking-wider">VERDICT</h2>
            <span className="rounded px-2 py-0.5 text-xs font-bold" style={{ background: 'var(--ask)', color: 'var(--bg)' }}>
              DO NOT SHIP
            </span>
            <span className="ml-auto mono text-xs" style={{ color: 'var(--dim)' }}>
              ledger {data.ledgerTag}
            </span>
          </div>

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div className="reveal-item panel p-3" style={{ "--i": 0 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>95% CI</div>
              <div className="mono text-sm mt-1">[{pct(zero.coint.meanLo)}, {pct(zero.coint.meanHi)}]</div>
              <div className="text-[0.7rem] mt-1" style={{ color: 'var(--faint)' }}>block bootstrap</div>
            </div>
            <div className="reveal-item panel p-3" style={{ "--i": 1 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>excludes zero?</div>
              <div className={`text-sm font-bold mt-1 ${zero.coint.excludesZero ? 'glow-up' : 'glow-down'}`}>
                {zero.coint.excludesZero ? "yes" : "no"}
              </div>
            </div>
            <div className="reveal-item panel p-3" style={{ "--i": 2 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>selection edge</div>
              <div className="mt-1">
                <MiniBar
                  value={Math.abs(zero.selectionEdge)}
                  max={0.001}
                  color={zero.selectionEdge > 0 ? 'var(--bid)' : 'var(--ask)'}
                  i={0}
                />
              </div>
              <div className="mono text-xs mt-1">{pct(zero.selectionEdge)}</div>
            </div>
            <div className="reveal-item panel p-3" style={{ "--i": 3 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>pairs evaluated</div>
              <div className="text-xl font-bold mt-1">
                <AnimatedNumber value={p.pairsEvaluated} className="num-hero" />
              </div>
            </div>
          </div>

          <p className="text-sm leading-relaxed" style={{ color: 'var(--text)' }}>
            {s.verdict}
          </p>
        </Reveal>
      </section>

      <section className="panel p-4">
        <Reveal className="flex flex-col gap-3">
          <div className="panel-h">
            <h2 className="text-xs font-bold tracking-wider">THREE ARMS, IDENTICAL RULES — {zero.costBps} BPS</h2>
            <p className="text-xs mt-1" style={{ color: 'var(--dim)' }}>{data.readThisFirst}</p>
          </div>

          <div className="table-wrap overflow-x-auto">
            <table className="v4-table w-full min-w-[34rem]">
              <thead>
                <tr>
                  <th className="text-left font-normal">arm</th>
                  <th className="text-right font-normal">trades</th>
                  <th className="text-right font-normal">mean / trade</th>
                  <th className="text-right font-normal">95% CI</th>
                  <th className="text-right font-normal">win rate</th>
                  <th className="text-right font-normal">Sharpe</th>
                  <th className="text-right font-normal">stopped out</th>
                </tr>
              </thead>
              <tbody>
                {[zero.coint, zero.random, zero.worst].map((a, i) => (
                  <tr key={a.arm}>
                    <td className="py-2 mono">{a.arm}</td>
                    <td className="py-2 text-right tnum">{a.trades.toLocaleString()}</td>
                    <td className="py-2 text-right tnum">
                      <DeltaBadge value={a.meanRet * 100} decimals={3} />
                    </td>
                    <td className="py-2 text-right mono text-xs">[{pct(a.meanLo)}, {pct(a.meanHi)}]</td>
                    <td className="py-2 text-right tnum">{pctPlain(a.winRate)}</td>
                    <td className="py-2 text-right tnum">{a.sharpe.toFixed(2)}</td>
                    <td className="py-2 text-right tnum">
                      {a.trades > 0 ? pctPlain(a.exits.stop / a.trades) : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <p className="text-xs leading-relaxed" style={{ color: 'var(--faint)' }}>
            Entry {s.rules.entry}, exit {s.rules.exit}, stop {s.rules.stop}, force close{" "}
            {s.rules.forceClose}. Top {s.rules.pairsPerFold} pairs per block.
          </p>
        </Reveal>
      </section>

      <section className="panel p-4">
        <Reveal className="flex flex-col gap-3">
          <div className="panel-h">
            <h2 className="text-xs font-bold tracking-wider">WHY — WHAT PERSISTS AND WHAT DOES NOT</h2>
          </div>

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div className="reveal-item panel p-3" style={{ "--i": 0 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>correlation ρ</div>
              <div className="text-lg font-bold tnum glow-up">{rho(p.corrRho)}</div>
              <div className="text-xs mt-1" style={{ color: 'var(--faint)' }}>
                {p.corrRhoLo != null && p.corrRhoHi != null ? `[${rho(p.corrRhoLo)}, ${rho(p.corrRhoHi)}]` : 'withheld'}
              </div>
            </div>
            <div className="reveal-item panel p-3" style={{ "--i": 1 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>coint ρ</div>
              <div className="text-lg font-bold tnum glow-down">{rho(p.cointRho)}</div>
            </div>
            <div className="reveal-item panel p-3" style={{ "--i": 2 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>stays above median</div>
              <div className="text-lg font-bold tnum">{pctPlain(p.aboveMedian)}</div>
              <div className="text-[0.7rem]" style={{ color: 'var(--faint)' }}>null: 50%</div>
            </div>
            <div className="reveal-item panel p-3" style={{ "--i": 3 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>walk-forward blocks</div>
              <div className="text-lg font-bold tnum">{p.blocks}</div>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-2">
            <div className="reveal-item panel p-3" style={{ "--i": 4 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>top tercile persistence</div>
              <div className="flex items-end gap-2">
                <span className="text-lg font-bold tnum">{pctPlain(p.topTercile)}</span>
                <MiniBar value={p.topTercile} max={1} color="var(--accent)" i={0} />
              </div>
              <div className="text-[0.7rem]" style={{ color: 'var(--faint)' }}>null: 33%</div>
            </div>
            <div className="reveal-item panel p-3" style={{ "--i": 5 } as React.CSSProperties}>
              <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>top decile persistence</div>
              <div className="flex items-end gap-2">
                <span className="text-lg font-bold tnum">{pctPlain(p.topDecile)}</span>
                <MiniBar value={p.topDecile} max={1} color="var(--accent)" i={1} />
              </div>
              <div className="text-[0.7rem]" style={{ color: 'var(--faint)' }}>null: 10%</div>
            </div>
          </div>

          <p className="text-sm leading-relaxed">{s.mechanism}</p>
          <p className="text-xs leading-relaxed" style={{ color: 'var(--faint)' }}>
            {s.why}
          </p>
          <p className="text-xs leading-relaxed" style={{ color: 'var(--faint)' }}>
            Binary framings of forward co-movement persistence, over{" "}
            {p.pairsEvaluated.toLocaleString()} pairs. They corroborate the mechanism; they are NOT
            restatements of the ledgered 73.1%, which measured SPY-correlation tiering — a different
            quantity.
          </p>
        </Reveal>
      </section>

      <section className="panel p-4">
        <Reveal className="flex flex-col gap-3">
          <div className="panel-h">
            <h2 className="text-xs font-bold tracking-wider">COST SWEEP — SELECTED ARM</h2>
            <p className="text-xs mt-1" style={{ color: 'var(--dim)' }}>{s.method.costSweepReason}</p>
          </div>

          <div className="table-wrap overflow-x-auto">
            <table className="v4-table w-full min-w-[30rem]">
              <thead>
                <tr>
                  <th className="text-left font-normal">cost / side</th>
                  <th className="text-right font-normal">mean / trade</th>
                  <th className="text-right font-normal">95% CI</th>
                  <th className="text-right font-normal">Sharpe</th>
                  <th className="text-right font-normal">selection edge</th>
                  <th className="text-right font-normal">excludes zero</th>
                </tr>
              </thead>
              <tbody>
                {s.costs.map((c: PairsCostLevel) => (
                  <tr key={c.costBps}>
                    <td className="py-2 mono">{c.costBps.toFixed(1)} bps</td>
                    <td className="py-2 text-right tnum">{pct(c.coint.meanRet)}</td>
                    <td className="py-2 text-right mono text-xs">
                      [{pct(c.coint.meanLo)}, {pct(c.coint.meanHi)}]
                    </td>
                    <td className="py-2 text-right tnum">{c.coint.sharpe.toFixed(2)}</td>
                    <td className="py-2 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <span className="tnum">{pct(c.selectionEdge)}</span>
                        <MiniBar
                          value={Math.abs(c.selectionEdge)}
                          max={0.001}
                          color={c.selectionEdge > 0 ? 'var(--bid)' : 'var(--ask)'}
                          i={0}
                          height={4}
                        />
                      </div>
                    </td>
                    <td className="py-2 text-right tnum">
                      <span className={`font-bold ${c.coint.excludesZero ? 'glow-up' : 'glow-down'}`}>
                        {c.coint.excludesZero ? "yes" : "no"}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Reveal>
      </section>

      <ProOnly>
        <section className="panel p-4">
          <Reveal className="flex flex-col gap-3">
            <div className="panel-h">
              <h2 className="text-xs font-bold tracking-wider">METHOD</h2>
            </div>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              <div className="reveal-item panel p-3" style={{ "--i": 0 } as React.CSSProperties}>
                <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>Walk</div>
                <div className="text-sm mt-1">{s.method.window}</div>
              </div>
              <div className="reveal-item panel p-3" style={{ "--i": 1 } as React.CSSProperties}>
                <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>Universe</div>
                <div className="text-sm mt-1">{s.method.universe}</div>
              </div>
              <div className="reveal-item panel p-3" style={{ "--i": 2 } as React.CSSProperties}>
                <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>Frozen parameters</div>
                <div className="text-sm mt-1">{s.method.frozenParams}</div>
              </div>
              <div className="reveal-item panel p-3" style={{ "--i": 3 } as React.CSSProperties}>
                <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>Critical values</div>
                <div className="text-sm mt-1">{s.method.criticalValues}</div>
              </div>
              <div className="reveal-item panel p-3" style={{ "--i": 4 } as React.CSSProperties}>
                <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>Matched nulls</div>
                <div className="text-sm mt-1">{s.method.matchedNulls}</div>
              </div>
              <div className="reveal-item panel p-3" style={{ "--i": 5 } as React.CSSProperties}>
                <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>Clustered bootstrap</div>
                <div className="text-sm mt-1">{s.method.blockBootstrap}</div>
              </div>
              <div className="reveal-item panel p-3" style={{ "--i": 6 } as React.CSSProperties}>
                <div className="text-[0.7rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>Why frozen</div>
                <div className="text-sm mt-1">{data.whyFrozen}</div>
              </div>
            </div>
            <p className="text-xs" style={{ color: 'var(--faint)' }}>
              Reproduce: <code className="mono">{data.reproduce}</code> · full writeup in {data.writeup}
            </p>
          </Reveal>
        </section>
      </ProOnly>

      <section className="panel p-4">
        <Reveal className="flex flex-col gap-3">
          <div className="panel-h">
            <h2 className="text-xs font-bold tracking-wider">WHAT THIS TEST STILL CANNOT SEE</h2>
          </div>
          <ul className="flex list-disc flex-col gap-2 pl-4 text-sm leading-relaxed">
            {s.limitations.map((l, i) => (
              <li key={l} className="reveal-item" style={{ "--i": i } as React.CSSProperties}>{l}</li>
            ))}
          </ul>
        </Reveal>
      </section>

      <section className="panel p-4">
        <Reveal className="flex flex-col gap-3">
          <div className="panel-h">
            <h2 className="text-xs font-bold tracking-wider">CARRIED FORWARD</h2>
          </div>
          <ul className="flex list-disc flex-col gap-2 pl-4 text-sm leading-relaxed">
            {s.lessons.map((l, i) => (
              <li key={l} className="reveal-item" style={{ "--i": i } as React.CSSProperties}>{l}</li>
            ))}
          </ul>
        </Reveal>
      </section>

      <div className="panel p-4" style={{ borderLeft: '3px solid var(--crossed)' }}>
        <p className="text-sm leading-relaxed" style={{ color: 'var(--faint)' }}>
          {s.hypothesis}
        </p>
      </div>
    </div>
  );
}
