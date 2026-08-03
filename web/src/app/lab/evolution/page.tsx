"use client";

import { useEffect, useState } from "react";
import {
  modelEvolution,
  pollMs,
  POLL_SLOW,
  selfAudit,
  type AuditStatus,
  type EvolutionWeightSeries,
  type FactorSkillSeries,
  type ModelEvolution,
  type SelfAudit,
  type SelfAuditFinding,
} from "@/lib/api";
import { fmtTs } from "@/lib/format";
import { legName } from "@/components/signals/predictions/compositeUi";
import { useViewMode, type ViewMode } from "@/components/Plain";
import PagePurpose from "@/components/PagePurpose";
import EmptyState from "@/components/EmptyState";
import ErrorState from "@/components/ErrorState";
import Skeleton from "@/components/Skeleton";
import { PageHero } from "@/components/ui/Kit";

function fmtAge(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

function fmtIc(v: number): string {
  return `${v >= 0 ? "+" : ""}${v.toFixed(3)}`;
}

function icColor(v: number): string {
  if (v > 0) return "var(--bid)";
  if (v < 0) return "var(--ask)";
  return "var(--dim)";
}

const STATUS_UI: Record<AuditStatus, { label: string; color: string }> = {
  ok: { label: "ok", color: "var(--ok)" },
  degrading: { label: "degrading", color: "var(--bad)" },
  sign_flip: { label: "sign flip", color: "var(--bad)" },
  over_confident: { label: "over-confident", color: "var(--warn)" },
  under_confident: { label: "under-confident", color: "var(--warn)" },
  insufficient: { label: "withheld", color: "var(--faint)" },
};

const HORIZON_WORD: Record<string, string> = {
  "1h": "1-hour",
  "1d": "1-day",
  "1w": "1-week",
};

function metricName(metric: string, mode: ViewMode): string {
  if (mode === "pro") return metric;
  const i = metric.indexOf(":");
  const family = i === -1 ? metric : metric.slice(0, i);
  const arg = i === -1 ? "" : metric.slice(i + 1);
  if (family === "calibration") return `${HORIZON_WORD[arg] ?? arg} calibration`;
  if (family === "prediction_bias") return `${HORIZON_WORD[arg] ?? arg} prediction bias`;
  if (family === "factor_ic") return `${legName(arg, "simple")} skill`;
  return metric.replace(/[_:]+/g, " ");
}

const FAMILIES: { prefix: string; title: string; sub: string }[] = [
  {
    prefix: "calibration:",
    title: "CALIBRATION",
    sub: "do the stated probabilities match what happened?",
  },
  {
    prefix: "prediction_bias:",
    title: "BIAS",
    sub: "is the model systematically too bullish or bearish?",
  },
  {
    prefix: "factor_ic:",
    title: "FACTOR IC",
    sub: "is each ensemble leg still carrying information?",
  },
];

export default function EvolutionPage() {
  const [audit, setAudit] = useState<SelfAudit | null>(null);
  const [evo, setEvo] = useState<ModelEvolution | null>(null);
  const [fetchedAt, setFetchedAt] = useState(0);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      Promise.all([selfAudit(), modelEvolution(30)])
        .then(([a, m]) => {
          if (!alive) return;
          setAudit(a);
          setEvo(m);
          setErr(null);
          setFetchedAt(Math.floor(Date.now() / 1000));
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

  const loading = (!audit || !evo) && !err;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="MODEL EVOLUTION"
        subtitle="How the learned model changes over time — measured, not narrated"
        right={
          <span className="chip tnum">
            {loading ? (
              <span style={{ color: "var(--faint)" }}>loading…</span>
            ) : fetchedAt ? (
              `updated ${fmtTs(fetchedAt)}`
            ) : (
              "—"
            )}
          </span>
        }
      />

      <PagePurpose
        id="lab-evolution"
        text="The model watches itself: measured factor skill, learned weights, and a deterministic self-audit of calibration drift and bias — evolution you can verify, not marketing."
      />

      {err && (!audit || !evo) && (
        <ErrorState
          message={err}
          hint="Is the daemon running? Start signaldeckd and this page will pick it up."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}
      {loading && <Skeleton lines={6} label="loading model evolution" />}

      {audit && evo && (
        <>
          <SelfAuditPanel audit={audit} fetchedAt={fetchedAt} />
          <FactorSkillPanel series={evo.factorSkill} days={evo.days} />
          <WeightsPanel series={evo.weights} days={evo.days} />

          <section
            className="panel flex flex-col gap-2 p-4 text-[0.75rem]"
            style={{ color: "var(--faint)" }}
          >
            <p>{audit.note}</p>
            <p>{evo.note}</p>
            <p>
              Backtested / measured on the platform&rsquo;s own stored outcomes — NOT a
              live track record.
            </p>
          </section>
        </>
      )}
    </div>
  );
}

function SelfAuditPanel({ audit, fetchedAt }: { audit: SelfAudit; fetchedAt: number }) {
  const mode = useViewMode();

  if (audit.empty) {
    return (
      <EmptyState
        message="No audit findings yet — the first audit runs tonight."
        detail="The self-auditor is a once-per-day worker; its findings appear here after the first run."
      />
    );
  }

  return (
    <section className="panel">
      <div className="panel-h">
        <span>SELF-AUDIT · DAILY · DETERMINISTIC</span>
        <span
          className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal tnum"
          style={{ color: "var(--faint)" }}
        >
          generated {fmtTs(audit.generatedTs)}
        </span>
      </div>
      {FAMILIES.map((fam) => {
        const rows = audit.findings.filter((f) => f.metric.startsWith(fam.prefix));
        if (rows.length === 0) return null;
        return (
          <div key={fam.prefix}>
            <div
              className="flex flex-wrap items-baseline gap-x-3 px-4 pt-3 pb-1 text-[0.75rem] tracking-[0.14em]"
              style={{ color: "var(--faint)" }}
            >
              <span>{fam.title}</span>
              <span className="font-normal normal-case tracking-normal">{fam.sub}</span>
            </div>
            {rows.map((f) => (
              <FindingRow key={f.metric} f={f} mode={mode} fetchedAt={fetchedAt} />
            ))}
          </div>
        );
      })}
    </section>
  );
}

function FindingRow({
  f,
  mode,
  fetchedAt,
}: {
  f: SelfAuditFinding;
  mode: ViewMode;
  fetchedAt: number;
}) {
  const ui = STATUS_UI[f.status] ?? { label: f.status, color: "var(--dim)" };
  return (
    <div
      className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2"
      style={{ borderTop: "1px solid var(--border)" }}
    >
      <span
        className="chip shrink-0"
        style={{ color: ui.color, borderColor: ui.color }}
        title={`audit status: ${f.status}`}
      >
        {ui.label}
      </span>
      <span className="shrink-0 text-[0.75rem]" style={{ color: "var(--text)" }}>
        {metricName(f.metric, mode)}
      </span>
      <span className="min-w-0 flex-1 basis-60 text-[0.75rem]" style={{ color: "var(--dim)" }}>
        {f.detail}
      </span>
      <span className="chip tnum shrink-0" style={{ color: "var(--faint)" }}>
        {fmtAge(fetchedAt - f.ts)}
      </span>
    </div>
  );
}

function FactorSkillPanel({ series, days }: { series: FactorSkillSeries[]; days: number }) {
  const mode = useViewMode();
  return (
    <section className="panel">
      <div className="panel-h">
        <span>FACTOR SKILL TREND</span>
        <span
          className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          measured IC per ensemble leg, trailing {days}d — below-gate points withheld, not plotted
        </span>
      </div>
      {series.length === 0 ? (
        <p className="px-4 py-4 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          No factor-skill history in the window yet — measurements accrue as predictions
          resolve.
        </p>
      ) : (
        series.map((s) => {
          const shown = s.points.filter((p) => p.status !== "insufficient");
          const withheld = s.points.length - shown.length;
          const latest = shown.length > 0 ? shown[shown.length - 1] : null;
          const name = legName(s.leg, mode);
          return (
            <div
              key={s.leg}
              className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5"
              style={{ borderTop: "1px solid var(--border)" }}
            >
              <span className="w-44 shrink-0 text-[0.75rem]" style={{ color: "var(--text)" }}>
                {name}
              </span>
              <MiniSeries
                points={shown.map((p) => ({ ts: p.ts, v: p.ic }))}
                zero
                color="var(--dim)"
                label={`${name} IC trend`}
              />
              <span
                className="tnum w-16 shrink-0 text-right text-[0.85rem] font-semibold"
                style={{ color: latest ? icColor(latest.ic) : "var(--faint)" }}
                title="latest measured IC — correlation between this leg's signal and realized forward returns"
              >
                {latest ? fmtIc(latest.ic) : "—"}
              </span>
              {shown.length === 1 && (
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  1 measurement so far — trend needs time
                </span>
              )}
              {shown.length === 0 && (
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  all measurements withheld — below the skill gate
                </span>
              )}
              {withheld > 0 && (
                <span
                  className="text-[0.75rem]"
                  style={{ color: "var(--faint)" }}
                  title="points below the n≥30 independent-sample gate are excluded from the line, never plotted"
                >
                  {withheld} gap{withheld === 1 ? "" : "s"} withheld
                </span>
              )}
            </div>
          );
        })
      )}
    </section>
  );
}

function WeightsPanel({ series, days }: { series: EvolutionWeightSeries[]; days: number }) {
  const mode = useViewMode();
  const [picked, setPicked] = useState<string | null>(null);
  const regimes: string[] = [];
  for (const s of series) if (!regimes.includes(s.regime)) regimes.push(s.regime);
  const fallback = regimes.includes("all") ? "all" : (regimes[0] ?? null);
  const regime = picked !== null && regimes.includes(picked) ? picked : fallback;
  const rows = series.filter((s) => s.regime === regime);

  return (
    <section className="panel">
      <div className="panel-h">
        <span>LEARNED WEIGHTS</span>
        {regimes.length > 0 && (
          <div
            role="group"
            aria-label="Regime cell"
            className="ml-auto flex flex-wrap items-center gap-1 font-normal normal-case tracking-normal"
          >
            {regimes.map((r) => {
              const active = r === regime;
              return (
                <button
                  key={r}
                  type="button"
                  onClick={() => setPicked(r)}
                  aria-pressed={active}
                  className="chip min-h-[32px] cursor-pointer px-2.5 transition-colors duration-150 hover:text-[var(--text)]"
                  title={`show the learned blend weights for the ${r} regime cell`}
                  style={
                    active ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined
                  }
                >
                  {r}
                </button>
              );
            })}
          </div>
        )}
      </div>
      {rows.length === 0 ? (
        <p className="px-4 py-4 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          No learned weights in the window yet — the adaptive blender snapshots its weights
          as it updates.
        </p>
      ) : (
        <>
          {rows.map((s) => {
            const latest = s.points.length > 0 ? s.points[s.points.length - 1] : null;
            const name = legName(s.leg, mode);
            return (
              <div
                key={`${s.regime}:${s.leg}`}
                className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5"
                style={{ borderTop: "1px solid var(--border)" }}
              >
                <span className="w-44 shrink-0 text-[0.75rem]" style={{ color: "var(--text)" }}>
                  {name}
                </span>
                <MiniSeries
                  points={s.points.map((p) => ({ ts: p.ts, v: p.weight }))}
                  step
                  color="var(--accent)"
                  label={`${name} learned weight over time`}
                />
                <span
                  className="tnum w-16 shrink-0 text-right text-[0.85rem] font-semibold"
                  style={{ color: "var(--text)" }}
                  title="latest learned blend weight — this leg's share of the ensemble in this regime cell"
                >
                  {latest ? `${(latest.weight * 100).toFixed(0)}%` : "—"}
                </span>
                {s.points.length === 1 && (
                  <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    1 measurement so far — trend needs time
                  </span>
                )}
              </div>
            );
          })}
          <p className="px-4 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            Weights are the adaptive blender&rsquo;s snapshots over the trailing {days}d —
            each regime cell&rsquo;s weights sum to 100% across its legs. Missing snapshots
            are honest gaps, not zeros.
          </p>
        </>
      )}
    </section>
  );
}

function MiniSeries({
  points,
  step = false,
  zero = false,
  width = 160,
  height = 40,
  color = "var(--accent)",
  label,
}: {
  points: { ts: number; v: number }[];
  step?: boolean;
  zero?: boolean;
  width?: number;
  height?: number;
  color?: string;
  label: string;
}) {
  const padX = 3;
  const padY = 4;
  const common = { display: "inline-block", verticalAlign: "middle" } as const;

  if (points.length === 0) {
    return (
      <svg
        width={width}
        height={height}
        role="img"
        aria-label={`${label}: no measurements in the window`}
        style={common}
      >
        <title>no measurements in the window — an honest gap, not a flat line</title>
        <line
          x1={padX}
          y1={height / 2}
          x2={width - padX}
          y2={height / 2}
          stroke="var(--border)"
          strokeDasharray="2 4"
        />
      </svg>
    );
  }

  if (points.length === 1) {
    return (
      <svg
        width={width}
        height={height}
        role="img"
        aria-label={`${label}: 1 measurement so far — trend needs time`}
        style={common}
      >
        <title>1 measurement so far — trend needs time</title>
        <circle cx={width / 2} cy={height / 2} r={2.5} fill={color} />
      </svg>
    );
  }

  const vs = points.map((p) => p.v);
  let min = Math.min(...vs);
  let max = Math.max(...vs);
  if (zero) {
    min = Math.min(min, 0);
    max = Math.max(max, 0);
  }
  const span = max - min || 1;
  const t0 = points[0].ts;
  const tSpan = points[points.length - 1].ts - t0 || 1;
  const x = (ts: number) => padX + ((ts - t0) / tSpan) * (width - 2 * padX);
  const y = (v: number) => height - padY - ((v - min) / span) * (height - 2 * padY);
  const last = points[points.length - 1];

  let path = `M ${x(points[0].ts).toFixed(1)} ${y(points[0].v).toFixed(1)}`;
  for (let i = 1; i < points.length; i++) {
    const px = x(points[i].ts).toFixed(1);
    const py = y(points[i].v).toFixed(1);
    path += step ? ` H ${px} V ${py}` : ` L ${px} ${py}`;
  }

  return (
    <svg
      width={width}
      height={height}
      role="img"
      aria-label={`${label}: ${points.length} measurements`}
      style={common}
    >
      {zero && min < 0 && max > 0 && (
        <line
          x1={padX}
          y1={y(0)}
          x2={width - padX}
          y2={y(0)}
          stroke="var(--faint)"
          strokeOpacity={0.45}
          strokeDasharray="2 3"
        />
      )}
      <path d={path} fill="none" stroke={color} strokeWidth={1.5} strokeLinejoin="round" />
      <circle cx={x(last.ts)} cy={y(last.v)} r={2} fill={color} />
    </svg>
  );
}
