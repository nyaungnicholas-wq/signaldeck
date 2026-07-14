"use client";

// SYSTEM HEALTH — the daemon + worker + data half of "is it live?". Daemon
// version / uptime / Alpaca link, a compact worker roll-up (newest run per
// worker, status dot + last-run age, an honest "stale" tag when the newest run
// is over an hour old) that links out to the full /lab/system/agents detail,
// and dataset freshness (db size + "updated <age> ago" + retention windows).

import Link from "next/link";
import { ago, secondsSince } from "@/lib/format";
import type { Health } from "@/hooks/useLive";
import type { DataStats, WorkerRun } from "@/lib/api";

// Newest run older than this reads as "stale" — long enough that a minute/hour
// -cadence worker isn't falsely flagged, short enough to catch a wedged pipe.
const STALE_AFTER_S = 3600;

function statusColor(status: WorkerRun["status"]): string {
  if (status === "ok") return "var(--ok)";
  if (status === "error") return "var(--bad)";
  return "var(--accent)";
}

function fmtUptime(s: number): string {
  if (!isFinite(s) || s < 0) return "—";
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}

function fmtBytes(n: number | undefined): string {
  if (n == null || !isFinite(n) || n <= 0) return "—";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(i === 0 || v >= 100 ? 0 : 1)} ${u[i]}`;
}

/** dot + word chip (colour never stands alone). */
function DotChip({ tone, label }: { tone: "ok" | "bad" | "warn" | "unknown"; label: string }) {
  const color = tone === "ok" ? "var(--ok)" : tone === "bad" ? "var(--bad)" : tone === "warn" ? "var(--warn)" : "var(--faint)";
  return (
    <span className="chip inline-flex items-center gap-1.5">
      <span
        role="img"
        aria-label={label}
        className="inline-block h-2 w-2 shrink-0 rounded-full"
        style={{ background: color }}
      />
      <span style={{ color: "var(--dim)" }}>{label}</span>
    </span>
  );
}

function WorkerRow({ run }: { run: WorkerRun }) {
  const running = run.status === "running";
  const ts = run.finishedAt ?? run.startedAt;
  const stale = !running && secondsSince(ts) > STALE_AFTER_S;
  return (
    <div className="flex items-center gap-2 rounded-lg border px-2.5 py-1.5" style={{ borderColor: "var(--border)" }}>
      <span
        role="img"
        aria-label={`${run.worker}: ${run.status}`}
        className={`inline-block h-2 w-2 shrink-0 rounded-full${running ? " animate-pulse" : ""}`}
        style={{ background: statusColor(run.status) }}
      />
      <span className="mono min-w-0 truncate text-[0.75rem] font-bold" style={{ color: "var(--text)" }} title={run.detail || run.worker}>
        {run.worker}
      </span>
      {stale && (
        <span className="chip" style={{ color: "var(--warn)", borderColor: "var(--warn)", padding: "0 6px" }}>
          stale
        </span>
      )}
      <span className="tnum ml-auto shrink-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {running ? "running…" : ago(ts)}
      </span>
    </div>
  );
}

export default function SystemHealth({
  health,
  workers,
  stats,
}: {
  health: Health | null;
  workers: WorkerRun[] | null;
  stats: DataStats | null;
}) {
  // Newest run per worker.
  const byWorker = new Map<string, WorkerRun>();
  for (const r of workers ?? []) {
    const cur = byWorker.get(r.worker);
    if (!cur || r.startedAt > cur.startedAt || (r.startedAt === cur.startedAt && r.id > cur.id)) {
      byWorker.set(r.worker, r);
    }
  }
  const latest = [...byWorker.values()].sort((a, b) => (b.finishedAt ?? b.startedAt) - (a.finishedAt ?? a.startedAt));
  const nRunning = latest.filter((r) => r.status === "running").length;
  const nError = latest.filter((r) => r.status === "error").length;

  const ret = stats?.retention;

  return (
    <section className="panel">
      <h2 className="panel-h">SYSTEM HEALTH</h2>
      <div className="flex flex-col gap-4 p-4">
        {/* Daemon */}
        <div className="flex flex-col gap-2">
          <div className="text-[0.75rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
            DAEMON
          </div>
          <div className="flex flex-wrap items-center gap-2 text-[0.75rem]">
            {health ? (
              <>
                <span className="chip tnum">v{health.version}</span>
                <span className="chip tnum">up {fmtUptime(health.uptimeS)}</span>
                <DotChip
                  tone={health.alpaca ? "ok" : "warn"}
                  label={health.alpaca ? "alpaca connected" : "alpaca disconnected"}
                />
              </>
            ) : (
              <span style={{ color: "var(--faint)" }}>daemon not reachable</span>
            )}
          </div>
        </div>

        {/* Workers */}
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[0.75rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
              WORKERS
            </span>
            {latest.length > 0 && (
              <>
                <span className="chip tnum">{latest.length} workers</span>
                {nRunning > 0 && (
                  <span className="chip tnum" style={{ color: "var(--accent)" }}>
                    {nRunning} running
                  </span>
                )}
                <span className="chip tnum" style={{ color: nError > 0 ? "var(--bad)" : "var(--dim)" }}>
                  {nError} error{nError === 1 ? "" : "s"}
                </span>
              </>
            )}
            <Link
              href="/lab/system/agents"
              className="ml-auto text-[0.75rem] text-[var(--accent)] transition-colors duration-150 hover:text-[var(--text)]"
            >
              full worker detail →
            </Link>
          </div>
          {workers === null ? (
            <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              Worker data is unavailable — the /api/agents fetch has not succeeded. This is not a
              claim that no workers have run.
            </p>
          ) : latest.length === 0 ? (
            <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              No worker runs recorded yet.
            </p>
          ) : (
            <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
              {latest.map((r) => (
                <WorkerRow key={r.worker} run={r} />
              ))}
            </div>
          )}
        </div>

        {/* Data */}
        <div className="flex flex-col gap-2">
          <div className="text-[0.75rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
            DATA
          </div>
          {stats ? (
            <div className="flex flex-wrap items-center gap-2 text-[0.75rem]">
              <span className="chip tnum">{fmtBytes(stats.dbBytes)} db</span>
              {stats.walBytes > 0 && <span className="chip tnum">{fmtBytes(stats.walBytes)} wal</span>}
              {stats.archiveBytes != null && stats.archiveBytes > 0 && (
                <span className="chip tnum">{fmtBytes(stats.archiveBytes)} archive</span>
              )}
              <span className="tnum" style={{ color: "var(--faint)" }}>
                updated {ago(stats.generatedAt)}
              </span>
              {ret && (
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  · retention: snapshots {ret.snapshotsHours}h · 1m {ret.bars1mDays}d · 1h {ret.bars1hDays}d
                  {ret.dailyForever ? " · daily kept forever" : ""}
                </span>
              )}
            </div>
          ) : (
            <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              Dataset accounting unavailable.
            </p>
          )}
        </div>
      </div>
    </section>
  );
}
