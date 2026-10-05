"use client";

import { Fragment, useEffect, useState } from "react";
import { api, pollMs, POLL_DEFAULT, type Quality, type DQEvent, type DataStats } from "@/lib/api";
import { ago, fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import ProOnly from "@/components/ProOnly";
import { PageHero } from "@/components/ui/Kit";

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

const TFS = ["1m", "1h", "1d"] as const;

const FRESH_S: Record<string, number> = {
  "1m": 30 * 60,
  "1h": 2 * 3600,
  "1d": 2 * 3600,
};

function freshColor(tf: string, to: number): string {
  if (!to) return "var(--faint)";
  const age = Date.now() / 1000 - to;
  return age < (FRESH_S[tf] ?? 2 * 3600) ? "var(--ok)" : "var(--warn)";
}

function kindStyle(kind: string): { color: string; border: string } {
  const k = (kind || "").toLowerCase();
  if (k.includes("gap")) return { color: "var(--bad)", border: "rgba(248,113,113,.35)" };
  if (k.includes("stale")) return { color: "var(--warn)", border: "rgba(251,191,36,.35)" };
  if (k.includes("resync")) return { color: "var(--dim)", border: "var(--border)" };
  return { color: "var(--dim)", border: "var(--border)" };
}

function IncidentRow({ ev }: { ev: DQEvent }) {
  const s = kindStyle(ev.kind);
  return (
    <li className="border-b px-4 py-2.5 last:border-b-0" style={{ borderColor: "var(--border)" }}>
      <div className="flex items-center gap-2 text-[0.75rem]">
        <span
          className="chip"
          style={{ color: s.color, borderColor: s.border, padding: "1px 8px" }}
        >
          {ev.kind || "event"}
        </span>
        {ev.symbol && (
          <span className="font-semibold" style={{ color: "var(--text)" }}>
            {ev.symbol}
          </span>
        )}
        <span className="ml-auto tnum shrink-0" style={{ color: "var(--faint)" }}>
          {ago(ev.ts)}
        </span>
      </div>
      {ev.detail && (
        <div className="mt-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {ev.detail}
        </div>
      )}
    </li>
  );
}

export default function QualityPage() {
  const [data, setData] = useState<Quality | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [stats, setStats] = useState<DataStats | null>(null);
  const [statsErr, setStatsErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () => {
      api
        .quality()
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
      api
        .dataStats()
        .then((d) => {
          if (!alive) return;
          setStats(d);
          setStatsErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setStatsErr(e instanceof Error ? e.message : String(e));
        });
    };
    load();
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const symbols = data?.symbols ?? [];
  const events = [...(data?.events ?? [])].sort((a, b) => b.ts - a.ts);

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="DATA QUALITY"
        subtitle="Is the stored data complete and fresh — and what went wrong lately? If it is not measured on this page, it was not measured."
      />

      {err && !data && (
        <ErrorState
          message={err}
          hint="Is the daemon running? Start signaldeckd and this page will pick it up."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}
      {!err && !data && <Skeleton lines={4} label="loading data quality" />}
      {err && data && (
        <div className="px-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          connection lost — showing last known data · {err}
        </div>
      )}

      {data && (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[3fr_2fr]">
          <section className="panel">
            <div className="panel-h">BAR COVERAGE — WHAT WE ACTUALLY HAVE</div>
            {symbols.length === 0 ? (
              <EmptyState
                message="No symbols tracked yet"
                detail="Subscribe to a symbol from the watchlist and coverage will appear here as bars land."
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="v4-table w-full text-[0.75rem]">
                  <thead>
                    <tr
                      className="text-left text-[0.75rem] tracking-wide"
                      style={{ color: "var(--faint)" }}
                    >
                      <th className="px-4 py-2 font-medium">SYMBOL</th>
                      <th className="px-2 py-2 font-medium" title="Bar timeframe (1 minute / 1 hour / 1 day)">
                        TF
                      </th>
                      <th className="px-2 py-2 text-right font-medium" title="Number of bars stored">
                        BARS
                      </th>
                      <th className="px-2 py-2 font-medium" title="Date range covered by stored bars">
                        SPAN
                      </th>
                      <th className="px-4 py-2 text-right font-medium" title="Time since the newest stored bar">
                        FRESHNESS
                      </th>
                    </tr>
                  </thead>
                  <tbody className="tnum">
                    {symbols.map((s) => (
                      <Fragment key={`${s.market}:${s.symbol}`}>
                        {TFS.map((tf, i) => {
                          const c = s.coverage?.[tf];
                          const has = !!c && c.bars > 0;
                          return (
                            <tr
                              key={tf}
                              style={
                                i === 0
                                  ? { borderTop: "1px solid var(--border)" }
                                  : undefined
                              }
                            >
                              <td className="px-4 py-1.5 align-top whitespace-nowrap">
                                {i === 0 && (
                                  <span className="flex items-baseline gap-2">
                                    <span className="font-semibold" style={{ color: "var(--text)" }}>
                                      {s.symbol}
                                    </span>
                                    <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                                      {s.market}
                                      {s.market === "crypto" ? " · 24/7" : ""}
                                      {!s.active ? " · inactive" : ""}
                                    </span>
                                  </span>
                                )}
                              </td>
                              <td className="px-2 py-1.5" style={{ color: "var(--dim)" }}>
                                {tf}
                              </td>
                              <td className="px-2 py-1.5 text-right">
                                {has ? (
                                  c.bars.toLocaleString("en-US")
                                ) : (
                                  <span style={{ color: "var(--faint)" }}>—</span>
                                )}
                              </td>
                              <td className="px-2 py-1.5 whitespace-nowrap" style={{ color: "var(--dim)" }}>
                                {has ? `${fmtDate(c.from)} → ${fmtDate(c.to)}` : "—"}
                              </td>
                              <td
                                className="px-4 py-1.5 text-right whitespace-nowrap"
                                title={
                                  has
                                    ? `fresh = updated within ${tf === "1m" ? "30m" : "2h"}`
                                    : undefined
                                }
                                style={{ color: has ? freshColor(tf, c.to) : "var(--faint)" }}
                              >
                                {has ? ago(c.to) : "no data"}
                              </td>
                            </tr>
                          );
                        })}
                      </Fragment>
                    ))}
                  </tbody>
                </table>
                <p
                  className="px-4 pb-3 pt-2 text-[0.75rem]"
                  style={{ color: "var(--faint)" }}
                >
                  &ldquo;fresh&rdquo; (green) = newest bar within 30m for 1m bars, within 2h
                  for 1h and 1d bars; older turns amber.
                </p>
              </div>
            )}
          </section>

          <section className="panel h-fit">
            <div className="panel-h">
              INCIDENTS
              <span className="ml-auto text-[0.75rem] normal-case tracking-normal" style={{ color: "var(--faint)" }}>
                stale · gap · resync
              </span>
            </div>
            {events.length === 0 ? (
              <EmptyState
                message="No incidents recorded"
                detail="The dq-auditor logs every stale feed, gap and resync here — an empty list means nothing tripped it yet."
              />
            ) : (
              <ul className="max-h-[560px] overflow-y-auto">
                {events.map((ev) => (
                  <IncidentRow key={ev.id} ev={ev} />
                ))}
              </ul>
            )}
          </section>
        </div>
      )}

      <section className="panel">
        <div className="panel-h">
          DATA GROWTH — NOTHING IS THROWN AWAY
          {stats && (
            <span
              className="ml-auto flex items-center gap-2 text-[0.75rem] normal-case tracking-normal tnum"
              style={{ color: "var(--faint)" }}
            >
              <span className="chip tnum">db {fmtBytes(stats.dbBytes)}</span>
              <span className="chip tnum">wal {fmtBytes(stats.walBytes)}</span>
              {stats.archiveBytes !== undefined && (
                <span
                  className="chip tnum"
                  title="Cold gzip-CSV archive on disk — every row pruned from the hot store is exported here first (DuckDB/pandas readable). Nothing is truly deleted."
                >
                  archive {fmtBytes(stats.archiveBytes)}
                </span>
              )}
            </span>
          )}
        </div>
        {stats?.retention && (
          <div
            className="px-4 py-2 text-[0.75rem] normal-case tracking-normal"
            style={{ color: "var(--dim)", borderBottom: "1px solid var(--border)" }}
          >
            Tiered retention (hot store, then archive + prune):{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              snapshots {stats.retention.snapshotsHours}h
            </span>{" "}
            ·{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              1m bars {stats.retention.bars1mDays}d
            </span>{" "}
            → 1h ·{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              1h bars {stats.retention.bars1hDays}d
            </span>{" "}
            → 1d ·{" "}
            <span style={{ color: "var(--ok)" }}>daily kept forever</span>
          </div>
        )}
        {statsErr && !stats && (
          <div className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--bad)" }}>
            data stats unavailable · {statsErr}
          </div>
        )}
        {!statsErr && !stats && <Skeleton lines={3} label="loading data stats" />}
        {stats && (
          <div className="px-4 py-3">
          <ProOnly summary="Show table-by-table detail">
          <div className="overflow-x-auto">
            <table className="v4-table w-full text-[0.75rem]">
              <thead>
                <tr
                  className="text-left text-[0.75rem] tracking-wide"
                  style={{ color: "var(--faint)" }}
                >
                  <th className="px-4 py-2 font-medium">TABLE</th>
                  <th className="px-2 py-2 text-right font-medium" title="Row count">
                    ROWS
                  </th>
                  <th className="px-2 py-2 font-medium" title="Oldest record">
                    OLDEST
                  </th>
                  <th className="px-4 py-2 font-medium" title="Newest record">
                    NEWEST
                  </th>
                </tr>
              </thead>
              <tbody className="tnum">
                {[...stats.tables]
                  .sort((a, b) => b.rows - a.rows)
                  .map((t) => (
                    <tr key={t.table} style={{ borderTop: "1px solid var(--border)" }}>
                      <td className="px-4 py-1.5 font-semibold" style={{ color: "var(--text)" }}>
                        {t.table}
                      </td>
                      <td className="px-2 py-1.5 text-right">
                        {t.rows > 0 ? (
                          t.rows.toLocaleString("en-US")
                        ) : (
                          <span style={{ color: "var(--faint)" }}>0</span>
                        )}
                      </td>
                      <td className="px-2 py-1.5 whitespace-nowrap" style={{ color: "var(--dim)" }}>
                        {t.minTs ? fmtDate(t.minTs) : "—"}
                      </td>
                      <td className="px-4 py-1.5 whitespace-nowrap" style={{ color: "var(--dim)" }}>
                        {t.maxTs ? fmtDate(t.maxTs) : "—"}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
          </ProOnly>
          </div>
        )}
      </section>
    </div>
  );
}
