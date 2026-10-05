"use client";

import { useEffect, useState } from "react";
import { api, pollMs, POLL_DEFAULT, type WorkerRun } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { PageHero, StatTile, Reveal } from "@/components/ui/Kit";

const KNOWN_AGENTS: { name: string; role: string }[] = [
  { name: "crypto-live", role: "streams TickStream's consolidated book at 1Hz" },
  { name: "stock-streamer", role: "Alpaca IEX live minute bars" },
  { name: "backfiller", role: "pulls 2y history for new symbols" },
  { name: "backfill-reconciler", role: "re-enqueues under-covered symbols (self-healing)" },
  { name: "crypto-bars", role: "refreshes Kraken OHLC" },
  { name: "stock-bars", role: "tops up official daily stock bars" },
  { name: "downsampler", role: "1m→1h rollups + retention" },
  { name: "signal-runner", role: "recomputes pressure scores" },
  { name: "expectancy-runner", role: "rebuilds tendency tables" },
  { name: "outcome-resolver", role: "grades past scores (honesty)" },
  { name: "insight-writer", role: "writes the plain-English reads" },
  { name: "hud-sync", role: "syncs PUSH-20 trader HUD" },
  { name: "dq-auditor", role: "flags stale/gapped data" },
];

function statusColor(status: WorkerRun["status"]): string {
  if (status === "ok") return "var(--bid)";
  if (status === "error" || status === "timeout") return "var(--ask)";
  return "var(--accent)";
}

function fmtDur(s: number): string {
  if (!isFinite(s) || s < 0) return "—";
  if (s < 1) return "<1s";
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

function AgentCard({
  name,
  role,
  runs,
  i,
}: {
  name: string;
  role: string;
  runs: WorkerRun[];
  i: number;
}) {
  const last = runs[0];
  const history = runs.slice(0, 10).reverse();
  const running = last?.status === "running";
  const duration =
    !last || running || !last.finishedAt
      ? "running"
      : fmtDur(last.finishedAt - last.startedAt);

  return (
    <div className="panel reveal-item flex flex-col gap-2 p-4" style={{ "--i": i } as React.CSSProperties}>
      <div className="flex items-center gap-2">
        <span
          aria-label={last ? `status: ${last.status}` : "status: no runs yet"}
          className={`inline-block h-2.5 w-2.5 shrink-0 rounded-full${running ? " animate-pulse" : ""}`}
          style={{
            background: last ? statusColor(last.status) : "var(--faint)",
            boxShadow: last
              ? `0 0 8px ${
                  last.status === "ok"
                    ? "rgba(52,211,153,.5)"
                    : last.status === "error" || last.status === "timeout"
                      ? "rgba(248,113,113,.5)"
                      : "rgba(251,191,36,.6)"
                }`
              : undefined,
          }}
        />
        <span className="mono text-[0.82rem] font-bold tracking-wide" style={{ color: "var(--text)" }}>
          {name}
        </span>
        <span className="ml-auto tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {last ? ago(last.startedAt) : ""}
        </span>
      </div>

      <div className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
        {role}
      </div>

      {last ? (
        <>
          <div className="flex items-center gap-2 text-[0.75rem]">
            <span
              className="chip"
              style={{ color: statusColor(last.status), padding: "1px 8px" }}
            >
              {last.status}
            </span>
            <span className="tnum" style={{ color: "var(--dim)" }}>
              {running ? "running…" : duration}
            </span>
          </div>
          {last.detail && (
            <div
              className="truncate text-[0.75rem]"
              title={last.detail}
              style={{ color: "var(--faint)" }}
            >
              {last.detail}
            </div>
          )}
        </>
      ) : (
        <div className="text-[0.75rem] italic" style={{ color: "var(--faint)" }}>
          no runs yet
        </div>
      )}

      {/* history strip: last 10 runs, oldest → newest */}
      <div
        className="mt-auto flex items-center gap-1 pt-1"
        aria-label={`${name}: last ${history.length} runs`}
      >
        {history.length > 0 ? (
          history.map((r) => (
            <span
              key={r.id}
              title={`${r.status} · ${ago(r.startedAt)}${r.detail ? ` · ${r.detail}` : ""}`}
              className="inline-block h-2.5 w-2.5 cursor-pointer rounded-[3px] transition-colors duration-150 hover:brightness-125"
              style={{
                background: statusColor(r.status),
                opacity: r.status === "ok" ? 0.75 : 1,
              }}
            />
          ))
        ) : (
          <span
            className="inline-block h-2.5 w-full rounded-[3px] border border-dashed"
            style={{ borderColor: "var(--border)" }}
            aria-hidden="true"
          />
        )}
      </div>
    </div>
  );
}

export default function AgentsPage() {
  const [runs, setRuns] = useState<WorkerRun[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .agents()
        .then((d) => {
          if (!alive) return;
          setRuns(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  // Group runs by worker, newest first inside each group.
  const byWorker = new Map<string, WorkerRun[]>();
  for (const r of runs ?? []) {
    const list = byWorker.get(r.worker) ?? [];
    list.push(r);
    byWorker.set(r.worker, list);
  }
  for (const list of byWorker.values()) {
    list.sort((a, b) => b.startedAt - a.startedAt || b.id - a.id);
  }

  // Known agents in canonical order, then any unknown workers seen in runs.
  const cards: { name: string; role: string; runs: WorkerRun[] }[] = KNOWN_AGENTS.map(
    (a) => ({ ...a, runs: byWorker.get(a.name) ?? [] }),
  );
  const knownNames = new Set(KNOWN_AGENTS.map((a) => a.name));
  const extras = [...byWorker.keys()].filter((w) => !knownNames.has(w)).sort();
  for (const w of extras) {
    cards.push({ name: w, role: "unregistered worker", runs: byWorker.get(w) ?? [] });
  }

  const lastByAgent = cards.map((c) => c.runs[0]).filter(Boolean) as WorkerRun[];
  const nRunning = lastByAgent.filter((r) => r.status === "running").length;
  const nError = lastByAgent.filter((r) => r.status === "error" || r.status === "timeout").length;
  const nOk = lastByAgent.filter((r) => r.status === "ok").length;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Agent Fleet"
        subtitle="Status and execution history for all background workers in the daemon fleet"
        live
      />

      {/* Hero band. Every value is gated on `runs`, because KNOWN_AGENTS is a
          13-entry client-side constant and `cards` is seeded from it: with
          /api/agents down, this band rendered "Workers 13 · Active 0 · Healthy
          0 · Errors 0" — a complete, real-looking fleet census sourced entirely
          from a literal, sitting above the error state that correctly said the
          fleet could not be read. The per-agent cards below already degrade
          honestly; this band did not.
          `delta={0}` is also gone: DeltaBadge treats 0 as a value, so all four
          tiles carried a green ▲0.0% that no comparison produced. */}
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="Workers"
          value={runs ? cards.length : null}
          i={0}
        />
        <StatTile
          label="Active"
          value={runs ? nRunning : null}
          i={1}
        />
        <StatTile
          label="Healthy"
          value={runs ? nOk : null}
          i={2}
          spark={runs ? lastByAgent.map(r => r.status === "ok" ? 1 : 0) : undefined}
        />
        <StatTile
          label="Errors"
          value={runs ? nError : null}
          i={3}
          glow={nError > 0 ? "down" : undefined}
        />
      </div>

      {err && !runs && (
        <ErrorState
          message={err}
          hint="Is the daemon running? Start signaldeckd and the worker fleet will report in."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}
      {!err && !runs && <Skeleton lines={4} label="loading worker fleet" />}
      {err && runs && (
        <div className="px-1 text-[0.75rem]" style={{ color: "var(--ask)" }}>
          connection lost — showing last known data · {err}
        </div>
      )}

      {runs && (
        <>
          {runs.length === 0 && (
            <EmptyState
              message="No worker runs recorded yet"
              detail="The daemon is up but its workers haven't ticked. Cards below show the full fleet and will light up as runs land."
            />
          )}
          <div className="hud-panel p-4">
            <div className="panel-h mb-3 text-[0.75rem] uppercase tracking-wider" style={{ color: "var(--dim)" }}>
              Fleet Status
            </div>
            <Reveal>
              <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
                {cards.map((c, i) => (
                  <AgentCard key={c.name} name={c.name} role={c.role} runs={c.runs} i={i} />
                ))}
              </div>
            </Reveal>
          </div>
          <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            A card with no runs means that worker hasn&apos;t completed a run recently enough to
            survive run-log pruning (rare-cadence workers like 13f-poller / fred-poller / backup
            run every 6–24h) — it does not mean the worker is broken. Each worker&apos;s last runs
            are floor-protected on the current daemon build, so cards fill in after the next
            completed run.
          </p>
        </>
      )}
    </div>
  );
}
