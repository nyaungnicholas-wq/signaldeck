"use client";

// SEC filings feed (Signal8 wave, Stage 1): plain-English labels over the raw
// EDGAR form types, with an honest data-lag note. Public-domain government
// data — free to show; NOT real-time by law/process.

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { filings, type Filing } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

const POLL_MS = 60_000;

/** Form families offered as one-tap filters (prefix match server-side). */
const FORM_FILTERS = ["all", "4", "8-K", "10-Q", "10-K", "S-3", "424B", "SC 13D", "SC 13G"];

/** Badge color per form family: dilution-shaped forms red-ish, insider forms
 *  accent, reports neutral. */
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
  const [symbol, setSymbol] = useState("");
  const [retryTick, setRetryTick] = useState(0);

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
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [form, symbol, retryTick]);

  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;
  const list = useMemo(() => rows ?? [], [rows]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">SEC FILINGS</h1>
        <span className="chip">plain-English feed</span>
        {err !== null && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

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
        <section className="panel">
          <div className="panel-h flex-wrap gap-2">
            FILINGS FEED
            <span className="chip tnum">{list.length} shown</span>
            <input
              value={symbol}
              onChange={(e) => setSymbol(e.target.value.toUpperCase().trim())}
              placeholder="filter symbol…"
              aria-label="filter by symbol"
              className="chip min-h-[36px] w-28 bg-transparent px-3 outline-none"
              style={{ color: "var(--text)" }}
            />
            <span className="ml-auto flex flex-wrap items-center gap-1" role="tablist" aria-label="form filter">
              {FORM_FILTERS.map((f) => (
                <button
                  key={f}
                  type="button"
                  role="tab"
                  aria-selected={f === form}
                  onClick={() => setForm(f)}
                  className="chip min-h-[36px] cursor-pointer px-2.5 transition-colors duration-150"
                  style={{
                    color: f === form ? "var(--accent)" : "var(--dim)",
                    borderColor: f === form ? "var(--accent)" : "var(--border)",
                  }}
                >
                  {f}
                </button>
              ))}
            </span>
          </div>

          {/* honest lag note, always visible */}
          <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {note}
          </p>

          {list.length === 0 ? (
            <EmptyState
              className="m-4"
              message="No filings stored yet"
              detail="The filings-poller sweeps the EDGAR submissions API every ~2 hours, rotating through the universe. Check back after a few sweeps."
            />
          ) : (
            <ul style={{ borderTop: "1px solid var(--border)" }}>
              {list.map((f) => (
                <li
                  key={f.id}
                  className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-[0.8rem]"
                  style={{ borderBottom: "1px solid var(--border)" }}
                >
                  <Link
                    href={`/s/stocks/${encodeURIComponent(f.symbol ?? "")}`}
                    className="tnum w-16 font-bold hover:underline"
                    style={{ color: "var(--text)" }}
                  >
                    {f.symbol}
                  </Link>
                  <span
                    className="chip"
                    style={{ color: formColor(f.form), borderColor: formColor(f.form) }}
                  >
                    {f.form}
                  </span>
                  <span style={{ color: "var(--dim)" }}>{f.label}</span>
                  <span className="tnum ml-auto text-[0.72rem]" style={{ color: "var(--faint)" }}>
                    {ago(f.filedTs)}
                  </span>
                  {f.url && (
                    <a
                      href={f.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-[0.72rem] underline"
                      style={{ color: "var(--faint)" }}
                    >
                      EDGAR ↗
                    </a>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </div>
  );
}
