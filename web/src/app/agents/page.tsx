"use client";

// /agents — daemon worker fleet. Polls api.agents() and groups runs by
// worker name; known agents are always shown, even before their first run.

import { useEffect, useState } from "react";
import { api, pollMs, type WorkerRun } from "@/lib/api";
import { ago } from "@/lib/format";

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
  if (status === "ok") return "var(--ok)";
  if (status === "error") return "var(--bad)";
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
}: {
  name: string;
  role: string;
  runs: WorkerRun[]; // newest first
}) {
  const last = runs[0];
  const history = runs.slice(0, 10).reverse(); // oldest → newest, left → right
  const running = last?.status === "running";
  const duration =
    !last || running || !last.finishedAt
      ? "running"
      : fmtDur(last.finishedAt - last.startedAt);

  return (
    <div className="panel flex flex-col gap-2 p-4">
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
                    : last.status === "error"
                      ? "rgba(248,113,113,.5)"
                      : "rgba(251,191,36,.6)"
                }`
              : undefined,
          }}
        />
        <span className="text-[0.82rem] font-bold tracking-wide" style={{ color: "var(--text)" }}>
          {name}
        </span>
        <span className="ml-auto tnum text-[0.68rem]" style={{ color: "var(--faint)" }}>
          {last ? ago(last.startedAt) : ""}
        </span>
      </div>

      <div className="text-[0.68rem] leading-relaxed" style={{ color: "var(--dim)" }}>
        {role}
      </div>

      {last ? (
        <>
          <div className="flex items-center gap-2 text-[0.7rem]">
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
              className="truncate text-[0.68rem]"
              title={last.detail}
              style={{ color: "var(--faint)" }}
            >
              {last.detail}
            </div>
          )}
        </>
      ) : (
        <div className="text-[0.7rem] italic" style={{ color: "var(--faint)" }}>
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
              className="inline-block h-2.5 w-2.5 cursor-pointer rounded-[3px] transition-transform duration-150 hover:scale-125"
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
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

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
  const nError = lastByAgent.filter((r) => r.status === "error").length;
  const lastTs = Math.max(0, ...(runs ?? []).map((r) => r.finishedAt ?? r.startedAt));

  return (
    <div className="flex flex-col gap-4">
      {/* Header row */}
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-sm font-bold tracking-[0.16em]">AGENTS</h1>
        <span className="chip tnum">{cards.length} workers</span>
        {nRunning > 0 && (
          <span className="chip tnum" style={{ color: "var(--accent)" }}>
            {nRunning} running
          </span>
        )}
        <span
          className="chip tnum"
          style={{ color: nError > 0 ? "var(--bad)" : "var(--dim)" }}
        >
          {nError} error{nError === 1 ? "" : "s"}
        </span>
        {lastTs > 0 && (
          <span className="text-[0.68rem] tnum" style={{ color: "var(--faint)" }}>
            last activity {ago(lastTs)}
          </span>
        )}
      </div>

      {err && !runs && (
        <div className="panel px-5 py-4 text-[0.78rem]" style={{ color: "var(--bad)" }}>
          {err}
          <div className="mt-1 text-[0.7rem]" style={{ color: "var(--dim)" }}>
            Is the daemon running? Start signaldeckd and the worker fleet will report in.
          </div>
        </div>
      )}
      {!err && !runs && (
        <div className="px-1 text-[0.72rem]" style={{ color: "var(--faint)" }}>
          loading…
        </div>
      )}
      {err && runs && (
        <div className="px-1 text-[0.7rem]" style={{ color: "var(--bad)" }}>
          connection lost — showing last known data · {err}
        </div>
      )}

      {runs && (
        <>
          {runs.length === 0 && (
            <div className="px-1 text-[0.72rem]" style={{ color: "var(--faint)" }}>
              No worker runs recorded yet — the daemon is up but its workers haven&apos;t
              ticked. Cards below show the full fleet and will light up as runs land.
            </div>
          )}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
            {cards.map((c) => (
              <AgentCard key={c.name} name={c.name} role={c.role} runs={c.runs} />
            ))}
          </div>
        </>
      )}
    </div>
  );
}
