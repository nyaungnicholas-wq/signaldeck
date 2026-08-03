"use client";

import { useEffect, useMemo, useState } from "react";
import { api, notifyStatus, pollMs, POLL_FAST, type AlertRow, type NotifyStatusResponse } from "@/lib/api";
import { PageHero, StatTile, Reveal } from "@/components/ui/Kit";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

function relativeTime(ts: number): string {
  const now = Date.now();
  const then = new Date(ts).getTime();
  const diff = Math.floor((now - then) / 1000);
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.floor(diff/60)}m`;
  if (diff < 86400) return `${Math.floor(diff/3600)}h`;
  return `${Math.floor(diff/86400)}d`;
}

function dayKey(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return "Today";
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

const dotColor: Record<string, string> = {
  bid: "var(--bid)",
  ok: "var(--bid)",
  ask: "var(--ask)",
  bad: "var(--ask)",
  accent: "var(--accent)",
  warn: "var(--accent)",
  hud: "var(--hud)",
  info: "var(--hud)",
};

export default function ActivityPage() {
  const [rows, setRows] = useState<AlertRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [marking, setMarking] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [unseenOnly, setUnseenOnly] = useState(false);
  const [kindFilter, setKindFilter] = useState<string | null>(null);
  // Fetched but not rendered yet — keep the setter so the poll below stays a
  // real request, and drop the unread binding.
  const [, setDeliveries] = useState<NotifyStatusResponse | null>(null);

  useEffect(() => {
    let alive = true;
    notifyStatus().then((d) => { if (alive) setDeliveries(d); }).catch(() => {});
    return () => { alive = false; };
  }, []);

  useEffect(() => {
    let alive = true;
    const load = () => api.alerts(unseenOnly, 100)
      .then((data) => { if (!alive) return; setRows(data); setError(null); })
      .catch((e) => { if (!alive) return; setError(e instanceof Error ? e.message : String(e)); });
    load();
    const stop = pollMs(load, POLL_FAST);
    return () => { alive = false; stop(); };
  }, [tick, unseenOnly]);

  const filtered = useMemo(() => (kindFilter ? (rows ?? []).filter((r) => r.kind === kindFilter) : (rows ?? [])), [rows, kindFilter]);

  const kinds = useMemo(() => {
    const present = new Set((rows ?? []).map((r) => r.kind));
    if (kindFilter) present.add(kindFilter);
    return [...present].sort();
  }, [rows, kindFilter]);

  const stats = useMemo(() => {
    if (!rows) return { eventsToday: 0, topSymbol: '', topType: '', errors: 0 };
    const now = new Date();
    const todayStr = now.toDateString();
    const eventsToday = rows.filter(r => new Date(r.ts).toDateString() === todayStr).length;

    const symbolCounts: Record<string, number> = {};
    const typeCounts: Record<string, number> = {};
    let errors = 0;
    rows.forEach(r => {
      if (r.symbol) symbolCounts[r.symbol] = (symbolCounts[r.symbol] || 0) + 1;
      typeCounts[r.kind] = (typeCounts[r.kind] || 0) + 1;
      if (r.kind === 'ask' || r.kind === 'bad') errors++;
    });

    const topSymbol = Object.entries(symbolCounts).sort((a, b) => b[1] - a[1])[0]?.[0] || '';
    const topType = Object.entries(typeCounts).sort((a, b) => b[1] - a[1])[0]?.[0] || '';
    return { eventsToday, topSymbol, topType, errors };
  }, [rows]);

  const grouped = useMemo(() => {
    const map = new Map<string, AlertRow[]>();
    filtered.forEach(row => {
      const key = dayKey(row.ts);
      const arr = map.get(key) || [];
      arr.push(row);
      map.set(key, arr);
    });
    return [...map.entries()];
  }, [filtered]);

  async function onMarkAll() {
    if (marking) return;
    setMarking(true);
    setActionError(null);
    try {
      await api.markAlertsSeen();
      window.dispatchEvent(new Event("sd-alerts-seen"));
      setTick((t) => t + 1);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setMarking(false);
    }
  }

  const signedOut = error !== null && /401/.test(error);

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Activity"
        subtitle="What the system has been doing — every scan, signal and event as a readable timeline."
        right={
          <div className="flex items-center gap-2">
            {unseenOnly && <span className="live-dot" />}
            <button
              type="button"
              aria-pressed={unseenOnly}
              onClick={() => setUnseenOnly((u) => !u)}
              className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:bg-[var(--panel3)]"
              style={unseenOnly ? { color: "var(--accent)", borderColor: "var(--accent)" } : { color: "var(--dim)" }}
            >
              unread only {unseenOnly ? "✓" : ""}
            </button>
            <button
              type="button"
              onClick={onMarkAll}
              disabled={marking || stats.eventsToday === 0}
              className="chip min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:bg-[var(--panel3)] hover:text-[var(--text)] disabled:cursor-default"
              style={stats.eventsToday > 0 ? { color: "var(--accent)", borderColor: "var(--accent)" } : { color: "var(--faint)" }}
            >
              {marking ? "marking…" : "mark all read"}
            </button>
          </div>
        }
        live
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Events Today" value={stats.eventsToday} glow="hud" i={0} />
        <StatTile label="Most Active Symbol" value={stats.topSymbol} i={1} />
        <StatTile label="Most Active Type" value={stats.topType} i={2} />
        <StatTile label="Errors / Warnings" value={stats.errors} glow={stats.errors > 0 ? "down" : undefined} i={3} />
      </div>

      {actionError && <div role="alert" className="text-[0.75rem]" style={{ color: "var(--ask)" }}>{actionError}</div>}

      {kinds.length > 1 && (
        <div className="flex flex-wrap items-center gap-1" role="tablist" aria-label="activity type filter">
          <button
            type="button"
            role="tab"
            aria-selected={kindFilter === null}
            onClick={() => setKindFilter(null)}
            className="chip min-h-[36px] cursor-pointer px-3 text-[0.75rem] transition-colors duration-150 hover:bg-[var(--panel3)]"
            style={{ color: kindFilter === null ? "var(--accent)" : "var(--dim)", borderColor: kindFilter === null ? "var(--accent)" : "var(--border)" }}
          >
            all
          </button>
          {kinds.map((k) => {
            const active = kindFilter === k;
            const count = (rows ?? []).filter((a) => a.kind === k).length;
            const color = dotColor[k] || "var(--dim)";
            return (
              <button
                key={k}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => setKindFilter(active ? null : k)}
                className="chip min-h-[36px] cursor-pointer px-3 text-[0.75rem] tracking-wider transition-colors duration-150 hover:bg-[var(--panel3)]"
                style={{ color: active ? color : "var(--dim)", borderColor: active ? color : "var(--border)" }}
              >
                {k} <span className="tnum">{count}</span>
              </button>
            );
          })}
        </div>
      )}

      {rows === null && !error && <Skeleton lines={5} label="loading activity" />}
      {rows === null && error && (
        <ErrorState
          message={signedOut ? "Sign in to see activity" : error}
          hint={signedOut ? "Activity is scoped to your account — log in." : undefined}
          retry={() => { setError(null); setTick((t) => t + 1); }}
        />
      )}
      {rows !== null && rows.length === 0 && (
        <EmptyState
          message={unseenOnly ? "No unread events" : "No alerts yet — that is normal"}
          detail={
            unseenOnly
              ? "You're caught up — switch off “unread only” to see everything."
              : "We only ping you when something actually changes: breakouts, regime shifts, and unusually strong or weak signals on the symbols you track. Nothing to configure."
          }
          action={unseenOnly ? undefined : { label: "Add symbols to track →", href: "/welcome" }}
        />
      )}

      {grouped.length > 0 && (
        <div className="relative ml-4 border-l border-[color:var(--dim)] border-opacity-20">
          {grouped.map(([day, dayRows]) => (
            <Reveal key={day} className="mb-6">
              <div className="panel p-3 mb-4 text-xs font-bold uppercase tracking-wider" style={{ color: "var(--hud)" }}>{day}</div>
              {dayRows.map((row, ri) => {
                const color = dotColor[row.kind] || "var(--dim)";
                return (
                  <div
                    key={row.id}
                    className="reveal-item relative pl-6 pb-4"
                    style={{ "--i": Math.min(ri, 12) } as React.CSSProperties}
                  >
                    <div className="absolute left-0 top-1 h-2 w-2 rounded-full" style={{ backgroundColor: color }} />
                    <div className="flex justify-between items-start gap-2">
                      <div>
                        <div className="text-sm" style={{ color: "var(--text)" }}>
                          <span className="mono font-bold mr-2">{row.symbol}</span>
                          {row.detail}
                        </div>
                      </div>
                      <div className="text-xs tnum whitespace-nowrap" style={{ color: "var(--faint)" }}>
                        {relativeTime(row.ts)}
                      </div>
                    </div>
                  </div>
                );
              })}
            </Reveal>
          ))}
        </div>
      )}
    </div>
  );
}
