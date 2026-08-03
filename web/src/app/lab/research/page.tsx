"use client";

// RESEARCH — the Bayesian research ledger rendered honestly. Four surfaces:
// (1) the HISTORICAL EVIDENCE BASE (research_weeks coverage per market era —
// a survivor-universe backtest base, labeled as such); (2) the HYPOTHESIS
// LEDGER (prior → posterior with the full evidence chain expandable per
// belief — every Bayes factor, every failed self-attack, verbatim notes);
// (3) META-ANALYSIS (which signal families keep surviving, which attacks
// kill the most research); (4) the EVIDENCE GRAPH (why a hypothesis stands:
// its eras, attack record, and evidence kinds as relationships).
//
// Honesty rules mirrored from the daemon: backtest evidence is never
// presented as live; a "supported" badge requires replications AND regime
// coverage, not just a big number; decay flags call out beliefs whose edge
// is weakening or stale. Nothing here mutates anything.

import { useCallback, useEffect, useState } from "react";
import {
  pollMs,
  POLL_SLOW,
  researchGraph,
  researchLedger,
  type LedgerEvidence,
  type LedgerGate,
  type LedgerHypothesis,
  type ResearchGraph,
  type ResearchLedger,
} from "@/lib/api";
import { fmtTs } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import ErrorState from "@/components/ErrorState";
import Skeleton from "@/components/Skeleton";
import { PageHero, Reveal, DeltaBadge, AnimatedNumber, MiniBar, StatTile } from "@/components/ui/Kit";

// ── pure helpers ──────────────────────────────────────────────────────────

const STATUS_UI: Record<string, { label: string; color: string }> = {
  supported: { label: "supported", color: "var(--bid)" },
  tentative: { label: "tentative", color: "var(--warn)" },
  uncertain: { label: "uncertain", color: "var(--dim)" },
  doubtful: { label: "doubtful", color: "var(--faint)" },
  rejected: { label: "rejected", color: "var(--ask)" },
};

const KIND_UI: Record<string, { label: string; color: string }> = {
  experiment: { label: "experiment", color: "var(--dim)" },
  replication: { label: "replication (live)", color: "var(--bid)" },
  backtest: { label: "backtest", color: "var(--warn)" },
  attack: { label: "attack", color: "var(--ask)" },
  manual: { label: "manual", color: "var(--faint)" },
};

const ERA_LABEL: Record<string, string> = {
  pre_covid: "pre-COVID",
  covid_crash: "COVID crash",
  bull_2020_21: "2020-21 bull",
  bear_2022: "2022 bear",
  ai_rally_2023_25: "2023-25 AI rally",
  y2026: "2026",
};

function pct(v: number): string {
  return `${(v * 100).toFixed(0)}%`;
}

function fmtBF(bf: number): string {
  return bf >= 1 ? `×${bf.toFixed(2)}` : `×${bf.toFixed(3)}`;
}

function bfColor(bf: number): string {
  if (bf > 1.001) return "var(--bid)";
  if (bf < 0.999) return "var(--ask)";
  return "var(--dim)";
}

/** posterior bar: prior tick + posterior fill on a 0..1 track. */
function PosteriorBar({ prior, posterior }: { prior: number; posterior: number }) {
  return (
    <div
      className="relative h-2 w-full rounded-full bg-white/10"
      aria-label={`prior ${pct(prior)} to posterior ${pct(posterior)}`}
    >
      <div
        className="absolute inset-y-0 left-0 rounded-full"
        style={{
          width: `${Math.max(2, posterior * 100)}%`,
          background: posterior >= prior ? "var(--bid)" : "var(--ask)",
          opacity: 0.75,
        }}
      />
      <div
        className="absolute top-0 bottom-0 w-0.5 bg-white/60"
        style={{ left: `${prior * 100}%` }}
        title={`prior ${pct(prior)}`}
      />
    </div>
  );
}

function Chip({ text, color, title }: { text: string; color: string; title?: string }) {
  return (
    <span
      title={title}
      className="inline-block rounded-full border px-2 py-0.5 text-[0.75rem] tnum"
      style={{
        borderColor: color,
        color: color,
        whiteSpace: "nowrap",
      }}
    >
      {text}
    </span>
  );
}

// ── page ──────────────────────────────────────────────────────────────────

export default function ResearchPage() {
  const [ledger, setLedger] = useState<ResearchLedger | null>(null);
  const [graph, setGraph] = useState<ResearchGraph | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [retryTick, setRetryTick] = useState(0);

  const load = useCallback(() => {
    return Promise.all([
      researchLedger(),
      researchGraph().catch(() => null), // graph endpoint may lag a deploy
    ])
      .then(([l, g]) => {
        setLedger(l);
        setGraph(g);
        setErr(null);
      })
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  useEffect(() => pollMs(load, POLL_SLOW), [load, retryTick]);

  if (err && !ledger) {
    return (
      <main className="page-enter space-y-4" style={{ padding: 16 }}>
        <PageHero
          title="Research Ledger"
          subtitle="The engine's beliefs about its own discoveries: every hypothesis carries a fixed prior, an auditable evidence chain (historical era grades, live replications, self-attacks), and a Bayesian posterior."
        />
        <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />
      </main>
    );
  }
  if (!ledger) {
    return (
      <main className="page-enter space-y-4" style={{ padding: 16 }}>
        <PageHero
          title="Research Ledger"
          subtitle="The engine's beliefs about its own discoveries: every hypothesis carries a fixed prior, an auditable evidence chain (historical era grades, live replications, self-attacks), and a Bayesian posterior."
        />
        <Skeleton lines={10} />
      </main>
    );
  }

  const evByHyp = new Map<string, LedgerEvidence[]>();
  for (const e of ledger.evidence) {
    const arr = evByHyp.get(e.hypId) ?? [];
    arr.push(e);
    evByHyp.set(e.hypId, arr);
  }
  const decayById = new Map<string, { peak: number; edgeWeakening: boolean; stale: boolean }>();
  for (const d of ledger.decay) {
    decayById.set(d.id, d);
  }
  // gates may be absent on a daemon older than the tradability wave.
  const gateById = new Map<string, LedgerGate>();
  for (const g of ledger.gates ?? []) {
    gateById.set(g.id, g);
  }
  const weeks = ledger.weeks;

  return (
    <main className="page-enter space-y-4" style={{ padding: 16 }}>
      <PageHero
        title="Research Ledger"
        subtitle="The engine's beliefs about its own discoveries: every hypothesis carries a fixed prior, an auditable evidence chain (historical era grades, live replications, self-attacks), and a Bayesian posterior."
        right={
          <div className="flex gap-2 items-center">
            <span className="live-dot" />
            <span className="mono text-sm" style={{ color: "var(--hud)" }}>LIVE</span>
          </div>
        }
      />

      {/* ── summary stats ── */}
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="Hypotheses"
          value={ledger.hypotheses.length}
          decimals={0}
          glow="accent"
          i={0}
          delta={undefined}
          spark={undefined}
          sub="Tracked beliefs"
        />
        <StatTile
          label="Live Replications"
          value={ledger.liveVsBacktest.live}
          decimals={0}
          glow="hud"
          i={1}
          delta={undefined}
          spark={undefined}
          sub="Forward data grades"
        />
        <StatTile
          label="Backtest Eras"
          value={ledger.liveVsBacktest.backtest}
          decimals={0}
          glow="down"
          i={2}
          delta={undefined}
          spark={undefined}
          sub="Historical windows"
        />
        <StatTile
          label="Evidence Rows"
          value={Object.values(ledger.evidenceKinds).reduce((a, b) => a + b, 0)}
          decimals={0}
          glow="up"
          i={3}
          delta={undefined}
          spark={undefined}
          sub="Total evidence items"
        />
      </div>

      {/* ── coverage: the historical evidence base ── */}
      <section className="panel p-4">
        <h2 className="panel-h">HISTORICAL EVIDENCE BASE</h2>
        {!weeks || weeks.rows === 0 ? (
          <EmptyState
            message="No historical weeks yet"
            detail="The hist-backfill worker builds the 2020→present weekly evidence base from daily bars on its next pass."
          />
        ) : (
          <>
            <div className="flex gap-2 flex-wrap items-center">
              <Chip
                color="var(--dim)"
                text={`${weeks.rows.toLocaleString()} symbol-weeks`}
              />
              <Chip color="var(--dim)" text={`${weeks.symbols} symbols`} />
              <Chip color="var(--dim)" text={`${weeks.weeks} independent market weeks`} />
              <Chip
                color="var(--dim)"
                text={`${fmtTs(weeks.minTs)} → ${fmtTs(weeks.maxTs)}`}
              />
              {Object.entries(weeks.byEra)
                .sort((a, b) => b[1] - a[1])
                .map(([era, n]) => (
                  <Chip
                    key={era}
                    color="var(--faint)"
                    text={`${ERA_LABEL[era] ?? era}: ${n.toLocaleString()}`}
                  />
                ))}
            </div>
            <p className="mt-2 text-[0.75rem]" style={{ color: "var(--dim)" }}>
              Survivor-universe backtest base (today&apos;s symbols projected into the
              past) — every grade drawn from it carries a standing survivorship
              penalty in its evidence chain. Stocks only: free crypto history is
              capped at ~2 years, so no crypto rows are faked.
            </p>
          </>
        )}
        <div className="flex gap-2 mt-3 flex-wrap">
          <Chip
            color="var(--bid)"
            text={`live replications: ${ledger.liveVsBacktest.live}`}
            title="grades on fresh forward data after discovery"
          />
          <Chip
            color="var(--warn)"
            text={`backtest era grades: ${ledger.liveVsBacktest.backtest}`}
            title="disjoint historical windows the discovery never saw — but backtest, not live"
          />
          {Object.entries(ledger.evidenceKinds).map(([k, n]) => (
            <Chip key={k} color="var(--faint)" text={`${k}: ${n}`} />
          ))}
        </div>
      </section>

      {/* ── the hypothesis ledger ── */}
      <section className="panel hud-panel p-4">
        <h2 className="panel-h">
          HYPOTHESIS LEDGER — prior → posterior, evidence-weighted
        </h2>
        {ledger.hypotheses.length === 0 ? (
          <EmptyState message="No hypotheses yet" detail="The research-ledger worker seeds on its next pass." />
        ) : (
          <div className="table-wrap">
            <Reveal>
              <table className="v4-table">
                <thead>
                  <tr className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
                    <th className="px-2 py-1.5">ID</th>
                    <th className="px-2 py-1.5">BELIEF</th>
                    <th className="px-2 py-1.5">FAMILY</th>
                    <th className="px-2 py-1.5">PRIOR → POSTERIOR</th>
                    <th className="px-2 py-1.5">STATUS</th>
                    <th className="px-2 py-1.5" title="independent disjoint-window grades / of them arguing against / volatility regimes covered">
                      REPS·CONTRA·REGIMES
                    </th>
                    <th className="px-2 py-1.5">FLAGS</th>
                  </tr>
                </thead>
                <tbody>
                  {ledger.hypotheses.map((h, i) => (
                    <HypRow
                      key={h.id}
                      h={h}
                      chain={evByHyp.get(h.id) ?? []}
                      decay={decayById.get(h.id)}
                      gate={gateById.get(h.id)}
                      open={!!open[h.id]}
                      onToggle={() => setOpen((o) => ({ ...o, [h.id]: !o[h.id] }))}
                      i={i}
                    />
                  ))}
                </tbody>
              </table>
            </Reveal>
          </div>
        )}
      </section>

      {/* ── meta-analysis ── */}
      <div className="grid gap-4 sm:grid-cols-2">
        <section className="panel p-4">
          <h2 className="panel-h">SIGNAL FAMILIES — what keeps surviving</h2>
          <div className="table-wrap">
            <table className="v4-table">
              <tbody>
                {(ledger.meta.families ?? []).map((f, i) => (
                  <tr key={f.family} className="reveal-item" style={{ "--i": i } as React.CSSProperties}>
                    <td className="px-2 py-1.5 mono">{f.family}</td>
                    <td className="px-2 py-1.5 tnum" style={{ color: "var(--dim)" }}>{f.n} beliefs</td>
                    <td className="px-2 py-1.5">avg posterior <span className="tnum">{pct(f.avgPosterior)}</span></td>
                    <td className="px-2 py-1.5 tnum" style={{ color: "var(--dim)" }}>
                      {f.supported > 0 ? `${f.supported} supported · ` : ""}
                      {f.rejected > 0 ? `${f.rejected} rejected` : ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
        <section className="panel p-4">
          <h2 className="panel-h">ATTACK LETHALITY — what kills research</h2>
          <div className="table-wrap">
            <table className="v4-table">
              <tbody>
                {(ledger.meta.attacks ?? []).map((a, i) => (
                  <tr key={a.attack} className="reveal-item" style={{ "--i": i } as React.CSSProperties}>
                    <td className="px-2 py-1.5 mono">{a.attack}</td>
                    <td className="px-2 py-1.5 tnum" style={{ color: a.failed > 0 ? "var(--ask)" : "var(--dim)" }}>
                      {a.failed} failed
                    </td>
                    <td className="px-2 py-1.5 tnum" style={{ color: "var(--dim)" }}>of {a.run} run</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>

      {/* ── evidence graph ── */}
      {graph && graph.nodes.length > 0 && (
        <section className="panel p-4">
          <h2 className="panel-h">EVIDENCE GRAPH — why each belief stands</h2>
          <GraphList graph={graph} />
        </section>
      )}

      <p className="text-[0.75rem] text-[color:var(--faint)]" style={{ whiteSpace: "pre-wrap" }}>
        {ledger.discipline}
      </p>
    </main>
  );
}

const PURPOSE =
  "The engine's beliefs about its own discoveries: every hypothesis carries a fixed prior, an auditable evidence chain (historical era grades, live replications, self-attacks), and a Bayesian posterior. Backtest evidence is survivor-universe history — labeled and penalized, never passed off as a live record.";

function HypRow({
  h,
  chain,
  decay,
  gate,
  open,
  onToggle,
  i,
}: {
  h: LedgerHypothesis;
  chain: LedgerEvidence[];
  decay?: { peak: number; edgeWeakening: boolean; stale: boolean };
  gate?: LedgerGate;
  open: boolean;
  onToggle: () => void;
  i: number;
}) {
  const st = STATUS_UI[h.status] ?? { label: h.status, color: "var(--dim)" };
  return (
    <>
      <tr
        onClick={onToggle}
        className="reveal-item cursor-pointer"
        style={{ "--i": i } as React.CSSProperties}
        title={open ? "collapse evidence chain" : `show ${chain.length} evidence rows`}
      >
        <td className="px-2 py-1.5 font-semibold mono whitespace-nowrap">
          {open ? "▾ " : "▸ "}
          {h.id}
        </td>
        <td className="px-2 py-1.5 max-w-[420px]">
          {h.statement}
          {h.horizon ? (
            <span className="text-[color:var(--faint)]"> · {h.horizon}</span>
          ) : null}
        </td>
        <td className="px-2 py-1.5" style={{ color: "var(--dim)" }}>{h.family}</td>
        <td className="px-2 py-1.5">
          <div className="flex items-center gap-2">
            <span className="text-[0.75rem] tnum whitespace-nowrap" style={{ color: "var(--dim)" }}>
              {pct(h.prior)} → <b className="font-semibold">{pct(h.posterior)}</b>
            </span>
            <PosteriorBar prior={h.prior} posterior={h.posterior} />
          </div>
        </td>
        <td className="px-2 py-1.5">
          <Chip text={st.label} color={st.color} />
        </td>
        <td className="px-2 py-1.5 tnum whitespace-nowrap" style={{ color: "var(--dim)" }}>
          {h.replications} · {h.contradictions} · {h.regimes}
        </td>
        <td className="px-2 py-1.5 whitespace-nowrap">
          {decay?.edgeWeakening && (
            <Chip
              text={`weakening (peak ${pct(decay.peak)})`}
              color="var(--ask)"
              title="posterior has fallen well below its peak"
            />
          )}{" "}
          {decay?.stale && (
            <Chip text="stale" color="var(--faint)" title="no new grade in 60+ days" />
          )}{" "}
          {/* A high posterior held at "tentative" has to say why, or the band
              reads as arbitrary. The tradability gate is the usual reason. */}
          {h.posterior >= 0.85 && gate?.unmetGate && (
            <Chip
              text={gate.unmetGate.startsWith("no tradable form") ? "no position stated" : "gate unmet"}
              color="var(--ask)"
              title={gate.unmetGate}
            />
          )}
        </td>
      </tr>
      {open && (
        <tr>
          <td colSpan={7} className="px-2 py-2 pl-8">
            {gate && (
              <p className="text-[0.75rem] mb-1.5" style={{ color: "var(--dim)" }}>
                <b>tradable form:</b>{" "}
                {gate.tradableForm || "not stated — this belief has never been written as a position"}
                {gate.economicTest ? (
                  <>
                    {" · "}
                    <b>graded:</b> {gate.economicTest}
                  </>
                ) : (
                  " · never graded net of costs"
                )}
              </p>
            )}
            {(h.openQuestions ?? []).length > 0 && (
              <p className="text-[0.75rem] mb-1.5" style={{ color: "var(--dim)" }}>
                open questions: {(h.openQuestions ?? []).join(" · ")}
              </p>
            )}
            {chain.length === 0 ? (
              <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                No evidence yet — the posterior stands at its prior. That is the
                honest state, not an error.
              </p>
            ) : (
              <table className="v4-table">
                <tbody>
                  {chain.map((e, i) => {
                    const k = KIND_UI[e.kind] ?? { label: e.kind, color: "var(--dim)" };
                    return (
                      <tr key={i} className="border-dashed border-white/10 border-t">
                        <td className="px-2 py-1 whitespace-nowrap text-[0.75rem]" style={{ color: "var(--faint)" }}>
                          {fmtTs(e.ts)}
                        </td>
                        <td className="px-2 py-1">
                          <Chip text={k.label} color={k.color} />
                        </td>
                        <td className="px-2 py-1 whitespace-nowrap text-[0.75rem]" style={{ color: "var(--dim)" }}>
                          {e.n > 0 ? `${e.k}/${e.n} vs p₀ ${e.p0.toFixed(2)}` : ""}
                        </td>
                        <td className="px-2 py-1 font-semibold tnum" style={{ color: bfColor(e.bf) }}>
                          {fmtBF(e.bf)}
                        </td>
                        <td className="px-2 py-1 text-[0.75rem]" style={{ color: "var(--dim)" }}>{e.note}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </td>
        </tr>
      )}
    </>
  );
}

/** The graph as grouped relationships per hypothesis — legible beats pretty. */
function GraphList({ graph }: { graph: ResearchGraph }) {
  const label = new Map<string, ResearchGraph["nodes"][number]>();
  for (const n of graph.nodes) {
    label.set(n.id, n);
  }
  const byHyp = new Map<string, ResearchGraph["edges"]>();
  for (const e of graph.edges) {
    const fromNode = label.get(e.from);
    const key = fromNode?.kind === "hypothesis" ? e.from : e.to;
    const arr = byHyp.get(key) ?? [];
    arr.push(e);
    byHyp.set(key, arr);
  }
  const hyps = graph.nodes.filter((n) => n.kind === "hypothesis");
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {hyps.map((h, i) => {
        const edges = byHyp.get(h.id) ?? [];
        return (
          <div
            key={h.id}
            className="panel p-3 reveal-item"
            style={{ "--i": i } as React.CSSProperties}
          >
            <div className="flex justify-between items-baseline">
              <b className="mono text-sm">{h.id}</b>
              <span className="text-[0.75rem] tnum" style={{ color: "var(--dim)" }}>
                posterior <span className="tnum">{pct(h.posterior)}</span> · {h.status}
              </span>
            </div>
            <div className="flex gap-1.5 flex-wrap mt-2">
              {edges.map((e, i) => {
                const other = e.from === h.id ? e.to : e.from;
                const n = label.get(other);
                if (!n || n.kind === "hypothesis") {
                  return (
                    <Chip key={i} color="var(--dim)" text={`↔ ${other}`} title="related hypothesis (same family)" />
                  );
                }
                return (
                  <Chip
                    key={i}
                    color={bfColor(e.avgBF)}
                    text={`${n.label || other} ×${e.weight}`}
                    title={`avg Bayes factor ${e.avgBF.toFixed(2)} over ${e.weight} evidence rows`}
                  />
                );
              })}
              {edges.length === 0 && (
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>no evidence links yet</span>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
