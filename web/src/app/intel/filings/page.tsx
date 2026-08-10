"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { filings, pollMs, POLL_SLOW, type Filing } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import { Reveal, PageHero, StatTile } from "@/components/ui/Kit";
import GoalBanner from "@/components/home/GoalBanner";
import ShowAllBar from "@/components/ShowAllBar";
import { listLimitFor, useGoal } from "@/lib/goal";

const FORM_FILTERS = ["all", "4", "8-K", "10-Q", "10-K", "S-3", "424B", "SC 13D", "SC 13G"];

function formColor(form: string): string {
  const f = form.toUpperCase();
  if (f.startsWith("S-1") || f.startsWith("S-3") || f.startsWith("424B")) return "var(--ask)";
  if (f === "4" || f === "4/A" || f === "3" || f === "5" || f === "144") return "var(--accent)";
  if (f.startsWith("SC 13D")) return "var(--warn)";
  if (f === "8-K") return "var(--bid)";
  return "var(--dim)";
}

export default function FilingsPage() {
  const [rows, setRows] = useState<Filing[] | null>(null);
  const [note, setNote] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [form, setForm] = useState("all");
  const { symbol } = useIntelSymbol();
  const [retryTick, setRetryTick] = useState(0);
  const goal = useGoal();
  // Per-visit override of the goal's default length, not a stored preference.
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    let alive = true;
    const load = () =>
      filings(symbol || undefined, form === "all" ? undefined : form, 200)
        .then((r) => {
          if (!alive) return;
          setRows(r.filings ?? []);
          setNote(r.note);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (symbol && msg.includes("404")) {
            setRows([]);
            setErr(null);
            return;
          }
          setErr(msg);
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [form, symbol, retryTick]);

  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;
  const list = useMemo(() => rows ?? [], [rows]);

  // Filings are one-line rows, so the cap is the "compact" one — 25/50/all.
  // The API already returns up to 200; this bounds what lands on screen.
  const limit = listLimitFor(goal, "compact");
  const drawn = limit === null || expanded ? list : list.slice(0, limit);

  const stats = useMemo(() => {
    if (!list.length) return { count: 0, latest: "", forms: 0, symbols: 0 };
    // filedTs is unix SECONDS — ago() at line 193 divides Date.now() by 1000 to
    // compare against it. Feeding it raw to new Date() read it as milliseconds,
    // so the LATEST tile showed 1970-01-21 next to rows correctly saying "2h ago".
    const latest = new Date(Math.max(...list.map(f => f.filedTs * 1000))).toISOString().split('T')[0];
    const forms = new Set(list.map(f => f.form)).size;
    const symbols = new Set(list.map(f => f.symbolId)).size;
    return { count: list.length, latest, forms, symbols };
  }, [list]);

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Filings"
        subtitle="Fresh SEC filings decoded - what was filed, by whom, and why it matters."
        right={
          // A filter group whose selected state was carried by colour alone:
          // invisible to a screen reader, and invisible to the UX audit, which
          // is why this page reported one interactive control while showing
          // nine. aria-pressed says which one is on; 40px meets the target
          // budget the audit measures.
          <div role="group" aria-label="Filter by form type" className="flex flex-wrap gap-1">
            {FORM_FILTERS.map((f) => (
              <button
                key={f}
                type="button"
                onClick={() => setForm(f)}
                aria-pressed={f === form}
                title={f === "all" ? "Every form type" : `Show only form ${f}`}
                className="chip min-h-[40px] cursor-pointer px-2.5 transition-colors duration-150 hover:text-[var(--text)]"
                style={{
                  color: f === form ? "var(--accent)" : undefined,
                  borderColor: f === form ? "var(--accent)" : undefined,
                }}
              >
                {f}
              </button>
            ))}
          </div>
        }
      />

      <GoalBanner
        note={
          limit === null
            ? "Every stored filing, uncapped."
            : `The ${limit} most recent filings. Open the full list any time.`
        }
      />

      {loading && <Skeleton lines={6} label="loading filings feed" />}
      {hardError && (
        <ErrorState
          message={err ?? "filings unavailable"}
          hint="Is the daemon running? The filings-poller sweeps EDGAR every ~2h."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {rows !== null && (
        <div className="hud-panel">
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 p-4">
            <StatTile label="Total Filings" value={stats.count} i={0} glow="hud" />
            <StatTile label="Latest" value={stats.latest} i={1} />
            <StatTile label="Form Types" value={stats.forms} i={2} />
            <StatTile label="Companies" value={stats.symbols} decimals={0} i={3} />
          </div>
          <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {note}
          </p>
        </div>
      )}

      {rows !== null && (
        <section className="panel">
          <div className="panel-h">FILINGS FEED</div>
          {list.length === 0 ? (
            symbol || form !== "all" ? (
              <EmptyState
                className="m-4"
                message="No stored filings match this filter"
                detail="Widen the form filter or clear the shared symbol filter — or the poller simply hasn't swept this ticker's CIK yet (~2h rotation)."
              />
            ) : (
              <EmptyState
                className="m-4"
                message="No filings stored yet — SEC sweep in progress"
                detail="The filings-poller sweeps the EDGAR submissions API every ~2 hours (and at daemon boot), rotating through the universe; filings appear within ~2h of a completed sweep. EDGAR itself lags: Form 4 ~2 business days after the trade, 13F up to 45 days after quarter end."
              />
            )
          ) : (
            <Reveal>
              <ul className="v4-table">
                {drawn.map((f, i) => (
                  <li
                    key={f.id}
                    className="reveal-item grid grid-cols-12 gap-x-3 items-center px-4 py-2.5 text-[0.75rem]"
                    style={{ "--i": Math.min(i, 11) } as React.CSSProperties}
                  >
                    <Link
                      href={`/s/stocks/${encodeURIComponent(f.symbol ?? "")}`}
                      className="col-span-2 tnum font-bold hover:underline truncate"
                      title={f.symbol}
                      style={{ color: "var(--text)" }}
                    >
                      {f.symbol}
                    </Link>
                    <span
                      className="col-span-2 chip justify-self-start"
                      style={{ color: formColor(f.form), borderColor: formColor(f.form) }}
                    >
                      {f.form}
                    </span>
                    <span className="col-span-5 truncate" title={f.label} style={{ color: "var(--dim)" }}>
                      {f.label}
                    </span>
                    <span className="col-span-2 tnum text-right" style={{ color: "var(--faint)" }}>
                      {ago(f.filedTs)}
                    </span>
                    {f.url && (
                      <a
                        href={f.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="col-span-1 text-[var(--faint)] underline transition-colors duration-150 hover:text-[var(--text)]"
                      >
                        ↗
                      </a>
                    )}
                  </li>
                ))}
              </ul>
              <ShowAllBar
                shown={drawn.length}
                total={list.length}
                limit={limit}
                expanded={expanded}
                onToggle={setExpanded}
                noun="filings"
              />
            </Reveal>
          )}
        </section>
      )}
    </div>
  );
}
