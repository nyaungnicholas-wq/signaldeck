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
  type LedgerHypothesis,
  type ResearchGraph,
  type ResearchLedger,
} from "@/lib/api";
import { fmtTs } from "@/lib/format";
import PagePurpose from "@/components/PagePurpose";
import EmptyState from "@/components/EmptyState";
import ErrorState from "@/components/ErrorState";
import Skeleton from "@/components/Skeleton";

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
      style={{
        position: "relative",
        height: 8,
        borderRadius: 4,
        background: "var(--panel-2, rgba(128,128,128,.15))",
        minWidth: 90,
      }}
      aria-label={`prior ${pct(prior)} to posterior ${pct(posterior)}`}
    >
      <div
        style={{
          position: "absolute",
          left: 0,
          top: 0,
          bottom: 0,
          width: `${Math.max(2, posterior * 100)}%`,
          borderRadius: 4,
          background: posterior >= prior ? "var(--bid)" : "var(--ask)",
          opacity: 0.75,
        }}
      />
      <div
        style={{
          position: "absolute",
          left: `${prior * 100}%`,
          top: -2,
          bottom: -2,
          width: 2,
          background: "var(--fg)",
          opacity: 0.6,
        }}
        title={`prior ${pct(prior)}`}
      />
    </div>
  );
}

function Chip({ text, color, title }: { text: string; color: string; title?: string }) {
  return (
    <span
      title={title}
      style={{
        display: "inline-block",
        padding: "1px 8px",
        borderRadius: 10,
        fontSize: 11,
        border: `1px solid ${color}`,
        color,
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
      <main style={{ padding: 16 }}>
        <PagePurpose id="lab-research" text={PURPOSE} />
        <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />
      </main>
    );
  }
  if (!ledger) {
    return (
      <main style={{ padding: 16 }}>
        <PagePurpose id="lab-research" text={PURPOSE} />
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
  const decayById = new Map(ledger.decay.map((d) => [d.id, d]));
  const weeks = ledger.weeks;

  return (
    <main style={{ padding: 16, display: "grid", gap: 16 }}>
      <PagePurpose id="lab-research" text={PURPOSE} />

      {/* ── coverage: the historical evidence base ── */}
      <section>
        <h2 style={{ fontSize: 13, letterSpacing: 1, opacity: 0.8 }}>
          HISTORICAL EVIDENCE BASE
        </h2>
        {!weeks || weeks.rows === 0 ? (
          <EmptyState
            message="No historical weeks yet"
            detail="The hist-backfill worker builds the 2020→present weekly evidence base from daily bars on its next pass."
          />
        ) : (
          <>
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
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
            <p style={{ fontSize: 12, color: "var(--dim)", marginTop: 6 }}>
              Survivor-universe backtest base (today&apos;s symbols projected into the
              past) — every grade drawn from it carries a standing survivorship
              penalty in its evidence chain. Stocks only: free crypto history is
              capped at ~2 years, so no crypto rows are faked.
            </p>
          </>
        )}
        <div style={{ display: "flex", gap: 8, marginTop: 8, flexWrap: "wrap" }}>
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
      <section>
        <h2 style={{ fontSize: 13, letterSpacing: 1, opacity: 0.8 }}>
          HYPOTHESIS LEDGER — prior → posterior, evidence-weighted
        </h2>
        {ledger.hypotheses.length === 0 ? (
          <EmptyState message="No hypotheses yet" detail="The research-ledger worker seeds on its next pass." />
        ) : (
          <div style={{ overflowX: "auto" }}>
            <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
              <thead>
                <tr style={{ textAlign: "left", color: "var(--dim)", fontSize: 11 }}>
                  <th style={{ padding: "6px 8px" }}>ID</th>
                  <th style={{ padding: "6px 8px" }}>BELIEF</th>
                  <th style={{ padding: "6px 8px" }}>FAMILY</th>
                  <th style={{ padding: "6px 8px" }}>PRIOR → POSTERIOR</th>
                  <th style={{ padding: "6px 8px" }}>STATUS</th>
                  <th style={{ padding: "6px 8px" }} title="independent disjoint-window grades / of them arguing against / volatility regimes covered">
                    REPS·CONTRA·REGIMES
                  </th>
                  <th style={{ padding: "6px 8px" }}>FLAGS</th>
                </tr>
              </thead>
              <tbody>
                {ledger.hypotheses.map((h) => (
                  <HypRow
                    key={h.id}
                    h={h}
                    chain={evByHyp.get(h.id) ?? []}
                    decay={decayById.get(h.id)}
                    open={!!open[h.id]}
                    onToggle={() => setOpen((o) => ({ ...o, [h.id]: !o[h.id] }))}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* ── meta-analysis ── */}
      <section style={{ display: "grid", gap: 16, gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))" }}>
        <div>
          <h2 style={{ fontSize: 13, letterSpacing: 1, opacity: 0.8 }}>
            SIGNAL FAMILIES — what keeps surviving
          </h2>
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
            <tbody>
              {(ledger.meta.families ?? []).map((f) => (
                <tr key={f.family} style={{ borderTop: "1px solid var(--panel-2, rgba(128,128,128,.15))" }}>
                  <td style={{ padding: "6px 8px" }}>{f.family}</td>
                  <td style={{ padding: "6px 8px", color: "var(--dim)" }}>{f.n} beliefs</td>
                  <td style={{ padding: "6px 8px" }}>avg posterior {pct(f.avgPosterior)}</td>
                  <td style={{ padding: "6px 8px", color: "var(--dim)" }}>
                    {f.supported > 0 ? `${f.supported} supported · ` : ""}
                    {f.rejected > 0 ? `${f.rejected} rejected` : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div>
          <h2 style={{ fontSize: 13, letterSpacing: 1, opacity: 0.8 }}>
            ATTACK LETHALITY — what kills research
          </h2>
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
            <tbody>
              {(ledger.meta.attacks ?? []).map((a) => (
                <tr key={a.attack} style={{ borderTop: "1px solid var(--panel-2, rgba(128,128,128,.15))" }}>
                  <td style={{ padding: "6px 8px" }}>{a.attack}</td>
                  <td style={{ padding: "6px 8px", color: a.failed > 0 ? "var(--ask)" : "var(--dim)" }}>
                    {a.failed} failed
                  </td>
                  <td style={{ padding: "6px 8px", color: "var(--dim)" }}>of {a.run} run</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* ── evidence graph ── */}
      {graph && graph.nodes.length > 0 && (
        <section>
          <h2 style={{ fontSize: 13, letterSpacing: 1, opacity: 0.8 }}>
            EVIDENCE GRAPH — why each belief stands
          </h2>
          <GraphList graph={graph} />
        </section>
      )}

      <p style={{ fontSize: 11, color: "var(--faint)", whiteSpace: "pre-wrap" }}>
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
  open,
  onToggle,
}: {
  h: LedgerHypothesis;
  chain: LedgerEvidence[];
  decay?: { peak: number; edgeWeakening: boolean; stale: boolean };
  open: boolean;
  onToggle: () => void;
}) {
  const st = STATUS_UI[h.status] ?? { label: h.status, color: "var(--dim)" };
  return (
    <>
      <tr
        onClick={onToggle}
        style={{
          borderTop: "1px solid var(--panel-2, rgba(128,128,128,.15))",
          cursor: "pointer",
        }}
        title={open ? "collapse evidence chain" : `show ${chain.length} evidence rows`}
      >
        <td style={{ padding: "6px 8px", fontWeight: 600, whiteSpace: "nowrap" }}>
          {open ? "▾ " : "▸ "}
          {h.id}
        </td>
        <td style={{ padding: "6px 8px", maxWidth: 420 }}>
          {h.statement}
          {h.horizon ? (
            <span style={{ color: "var(--faint)" }}> · {h.horizon}</span>
          ) : null}
        </td>
        <td style={{ padding: "6px 8px", color: "var(--dim)" }}>{h.family}</td>
        <td style={{ padding: "6px 8px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ color: "var(--dim)", fontSize: 12, whiteSpace: "nowrap" }}>
              {pct(h.prior)} → <b style={{ color: "var(--fg)" }}>{pct(h.posterior)}</b>
            </span>
            <PosteriorBar prior={h.prior} posterior={h.posterior} />
          </div>
        </td>
        <td style={{ padding: "6px 8px" }}>
          <Chip text={st.label} color={st.color} />
        </td>
        <td style={{ padding: "6px 8px", color: "var(--dim)", whiteSpace: "nowrap" }}>
          {h.replications} · {h.contradictions} · {h.regimes}
        </td>
        <td style={{ padding: "6px 8px", whiteSpace: "nowrap" }}>
          {decay?.edgeWeakening && (
            <Chip
              text={`weakening (peak ${pct(decay.peak)})`}
              color="var(--ask)"
              title="posterior has fallen well below its peak"
            />
          )}{" "}
          {decay?.stale && (
            <Chip text="stale" color="var(--faint)" title="no new grade in 60+ days" />
          )}
        </td>
      </tr>
      {open && (
        <tr>
          <td colSpan={7} style={{ padding: "0 8px 10px 24px" }}>
            {(h.openQuestions ?? []).length > 0 && (
              <p style={{ fontSize: 12, color: "var(--dim)", margin: "6px 0" }}>
                open questions: {(h.openQuestions ?? []).join(" · ")}
              </p>
            )}
            {chain.length === 0 ? (
              <p style={{ fontSize: 12, color: "var(--faint)" }}>
                No evidence yet — the posterior stands at its prior. That is the
                honest state, not an error.
              </p>
            ) : (
              <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12 }}>
                <tbody>
                  {chain.map((e, i) => {
                    const k = KIND_UI[e.kind] ?? { label: e.kind, color: "var(--dim)" };
                    return (
                      <tr key={i} style={{ borderTop: "1px dashed var(--panel-2, rgba(128,128,128,.12))" }}>
                        <td style={{ padding: "4px 8px", whiteSpace: "nowrap", color: "var(--faint)" }}>
                          {fmtTs(e.ts)}
                        </td>
                        <td style={{ padding: "4px 8px" }}>
                          <Chip text={k.label} color={k.color} />
                        </td>
                        <td style={{ padding: "4px 8px", whiteSpace: "nowrap", color: "var(--dim)" }}>
                          {e.n > 0 ? `${e.k}/${e.n} vs p₀ ${e.p0.toFixed(2)}` : ""}
                        </td>
                        <td style={{ padding: "4px 8px", fontWeight: 600, color: bfColor(e.bf) }}>
                          {fmtBF(e.bf)}
                        </td>
                        <td style={{ padding: "4px 8px", color: "var(--dim)" }}>{e.note}</td>
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
  const label = new Map(graph.nodes.map((n) => [n.id, n]));
  const byHyp = new Map<string, typeof graph.edges>();
  for (const e of graph.edges) {
    const fromNode = label.get(e.from);
    const key = fromNode?.kind === "hypothesis" ? e.from : e.to;
    const arr = byHyp.get(key) ?? [];
    arr.push(e);
    byHyp.set(key, arr);
  }
  const hyps = graph.nodes.filter((n) => n.kind === "hypothesis");
  return (
    <div style={{ display: "grid", gap: 8, gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}>
      {hyps.map((h) => {
        const edges = byHyp.get(h.id) ?? [];
        return (
          <div
            key={h.id}
            style={{
              border: "1px solid var(--panel-2, rgba(128,128,128,.15))",
              borderRadius: 8,
              padding: 10,
            }}
          >
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
              <b style={{ fontSize: 13 }}>{h.id}</b>
              <span style={{ fontSize: 11, color: "var(--dim)" }}>
                posterior {pct(h.posterior)} · {h.status}
              </span>
            </div>
            <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginTop: 6 }}>
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
                <span style={{ fontSize: 12, color: "var(--faint)" }}>no evidence links yet</span>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
