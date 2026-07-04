"use client";

// Insider trades (Signal8 wave, Stage 1): parsed SEC Form 4 filings. HONEST
// classification: only P (open-market buy) and S (open-market sale) are
// conviction trades — grants/exercises/gifts are labeled as mechanics, and
// the ~2-business-day legal filing lag is stated on the page.

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { insiders, type InsiderTrade } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";

const POLL_MS = 60_000;

type CodeFilter = "all" | "P" | "S";

function codeColor(t: InsiderTrade): string {
  if (t.code === "P") return "var(--bid)";
  if (t.code === "S") return "var(--ask)";
  return "var(--dim)"; // grants/exercises/etc — neutral by design
}

function fmtUSD(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  if (Math.abs(v) >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (Math.abs(v) >= 1e6) return `$${(v / 1e6).toFixed(2)}M`;
  if (Math.abs(v) >= 1e3) return `$${(v / 1e3).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

export default function InsidersPage() {
  const [rows, setRows] = useState<InsiderTrade[] | null>(null);
  const [note, setNote] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [code, setCode] = useState<CodeFilter>("all");
  // Stage 5: the symbol filter is the hub-wide one (shared across sub-tabs).
  const { symbol } = useIntelSymbol();
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      insiders(symbol || undefined, code === "all" ? undefined : code, 200)
        .then((r) => {
          if (!alive) return;
          setRows(r.trades ?? []);
          setNote(r.note);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          // Exact-ticker API: unknown/partial symbol 404s — a filter miss,
          // not an outage; never leave a stale unfiltered list behind.
          if (symbol && msg.includes("404")) {
            setRows([]);
            setErr(null);
            return;
          }
          setErr(msg);
        });
    load();
    const t = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [code, symbol, retryTick]);

  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;
  const list = useMemo(() => rows ?? [], [rows]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">INSIDERS</h1>
        <span className="chip">SEC Form 4 · filed ~2 business days after the trade</span>
        {err !== null && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {loading && <Skeleton lines={6} label="loading insider trades" />}
      {hardError && (
        <ErrorState
          message={err ?? "insider data unavailable"}
          hint="Is the daemon running? Form 4s are parsed by the filings-poller (~2h sweeps)."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {rows !== null && (
        <section className="panel">
          <div className="panel-h flex-wrap gap-2">
            INSIDER TRANSACTIONS
            <span className="chip tnum">{list.length} shown</span>
            {symbol && (
              <span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                {symbol} — from the shared intel filter
              </span>
            )}
            <span className="ml-auto flex items-center gap-1" role="tablist" aria-label="code filter">
              {(["all", "P", "S"] as CodeFilter[]).map((c) => (
                <button
                  key={c}
                  type="button"
                  role="tab"
                  aria-selected={c === code}
                  onClick={() => setCode(c)}
                  className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150"
                  title={c === "P" ? "open-market buys only" : c === "S" ? "open-market sells only" : "all codes"}
                  style={{
                    color: c === code ? "var(--accent)" : "var(--dim)",
                    borderColor: c === code ? "var(--accent)" : "var(--border)",
                  }}
                >
                  {c === "P" ? "buys" : c === "S" ? "sells" : "all"}
                </button>
              ))}
            </span>
          </div>

          {/* honest classification + lag note, always visible */}
          <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {note}
          </p>

          {list.length === 0 ? (
            symbol || code !== "all" ? (
              <EmptyState
                className="m-4"
                message="No parsed insider trades match this filter"
                detail="Clear the shared symbol filter or widen the code filter — or this ticker's Form 4s simply haven't been swept yet (~2h rotation; unknown/partial tickers match nothing)."
              />
            ) : (
              <EmptyState
                className="m-4"
                message="No insider trades parsed yet — SEC sweep in progress"
                detail="Form 4s are fetched and parsed as the filings-poller sweeps the universe (~2h cadence and at daemon boot, rate-limited per SEC policy) — trades appear within ~2h of a completed sweep. By law a Form 4 is filed ~2 business days AFTER the trade."
              />
            )
          ) : (
            <ul style={{ borderTop: "1px solid var(--border)" }}>
              {list.map((t) => (
                <li
                  key={t.accession}
                  className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-[0.8rem]"
                  style={{ borderBottom: "1px solid var(--border)" }}
                >
                  <Link
                    href={`/s/stocks/${encodeURIComponent(t.symbol ?? "")}`}
                    className="tnum w-16 font-bold hover:underline"
                    style={{ color: "var(--text)" }}
                  >
                    {t.symbol}
                  </Link>
                  <span
                    className="chip"
                    style={{ color: codeColor(t), borderColor: codeColor(t) }}
                    title={t.codeLabel}
                  >
                    {t.codeLabel}
                  </span>
                  <span style={{ color: "var(--text)" }}>{t.insider}</span>
                  {t.title && <span style={{ color: "var(--faint)" }}>({t.title})</span>}
                  <span className="tnum" style={{ color: codeColor(t) }}>
                    {t.shares > 0 ? `${t.shares.toLocaleString()} sh` : "—"}
                    {t.price > 0 ? ` @ $${t.price.toFixed(2)}` : ""}
                  </span>
                  <span className="tnum" style={{ color: "var(--dim)" }}>
                    {fmtUSD(t.value)}
                  </span>
                  <span className="tnum ml-auto text-[0.72rem]" style={{ color: "var(--faint)" }}>
                    filed {ago(t.filedTs)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </div>
  );
}
