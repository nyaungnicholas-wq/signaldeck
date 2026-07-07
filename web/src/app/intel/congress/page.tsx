"use client";

// Congressional trades (Signal8 wave, Stage 2): public STOCK Act disclosures
// via the free Stock Watcher mirrors. HONESTY, prominently: disclosures lag
// 30-45 days BY LAW (never real-time), amounts are ranges not exact values,
// and the mirror-health status is shown when the free source itself is down
// (which it is as of 2026-07-04 — stored history keeps being served).

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { congress, type CongressMirrorStatus, type CongressTrade } from "@/lib/api";
import { fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import PagePurpose from "@/components/PagePurpose";

const POLL_MS = 120_000;

type ChamberFilter = "all" | "senate" | "house";

/** BUY green / SELL red / everything else neutral — same palette as insiders. */
function txColor(txType: string): string {
  if (txType === "purchase") return "var(--bid)";
  if (txType.startsWith("sale")) return "var(--ask)";
  return "var(--dim)";
}

function txBadge(txType: string): string {
  switch (txType) {
    case "purchase":
      return "BUY";
    case "sale_full":
      return "SELL (full)";
    case "sale_partial":
      return "SELL (partial)";
    case "sale":
      return "SELL";
    case "exchange":
      return "EXCHANGE";
    default:
      return txType.toUpperCase();
  }
}

/** True when the poller has checked and BOTH mirrors were down. */
function mirrorsDown(source?: CongressMirrorStatus | null): boolean {
  if (!source) return false;
  return source.senate?.ok === false && source.house?.ok === false;
}

export default function CongressPage() {
  const [rows, setRows] = useState<CongressTrade[] | null>(null);
  const [lagNote, setLagNote] = useState("");
  const [note, setNote] = useState("");
  const [source, setSource] = useState<CongressMirrorStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [chamber, setChamber] = useState<ChamberFilter>("all");
  // Stage 5: the symbol filter is the hub-wide one (matches the DISCLOSED
  // ticker text server-side, so it works even for tickers we don't track).
  const { symbol } = useIntelSymbol();
  const [member, setMember] = useState("");
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      congress(
        symbol || undefined,
        member || undefined,
        chamber === "all" ? undefined : chamber,
        200,
      )
        .then((r) => {
          if (!alive) return;
          setRows(r.trades ?? []);
          setLagNote(r.lagNote);
          setNote(r.note);
          setSource(r.source ?? null);
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
  }, [chamber, symbol, member, retryTick]);

  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;
  const list = useMemo(() => rows ?? [], [rows]);
  const down = mirrorsDown(source);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">CONGRESS</h1>
        {/* THE lag note — prominent, always visible, straight from the API. */}
        <span className="chip" style={{ color: "var(--warn)", borderColor: "var(--warn)" }}>
          disclosures lag 30–45 days by law
        </span>
        {down && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            free mirrors currently unreachable — showing stored history
          </span>
        )}
        {err !== null && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="intel-congress"
        text="Which stocks are members of Congress trading? STOCK Act disclosures lag 30-45 days by law, and amounts are ranges, not exact values."
      />

      {loading && <Skeleton lines={6} label="loading congressional trades" />}
      {hardError && (
        <ErrorState
          message={err ?? "congressional data unavailable"}
          hint="Is the daemon running? The congress-poller sweeps the free disclosure mirrors ~12h."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {rows !== null && (
        <section className="panel">
          <div className="panel-h flex-wrap gap-2">
            DISCLOSED STOCK TRANSACTIONS
            <span className="chip tnum">{list.length} shown</span>
            {symbol && (
              <span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                {symbol} — from the shared intel filter
              </span>
            )}
            <input
              value={member}
              onChange={(e) => setMember(e.target.value)}
              placeholder="filter member…"
              aria-label="filter by member"
              className="chip min-h-[36px] w-36 bg-transparent px-3 outline-none"
              style={{ color: "var(--text)" }}
            />
            <span className="ml-auto flex items-center gap-1" role="tablist" aria-label="chamber filter">
              {(["all", "senate", "house"] as ChamberFilter[]).map((c) => (
                <button
                  key={c}
                  type="button"
                  role="tab"
                  aria-selected={c === chamber}
                  onClick={() => setChamber(c)}
                  className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150"
                  style={{
                    color: c === chamber ? "var(--accent)" : "var(--dim)",
                    borderColor: c === chamber ? "var(--accent)" : "var(--border)",
                  }}
                >
                  {c}
                </button>
              ))}
            </span>
          </div>

          {/* full honesty note: lag + ranges + mirror provenance */}
          <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {lagNote} {note}
          </p>

          {list.length === 0 ? (
            symbol || member || chamber !== "all" ? (
              <EmptyState
                className="m-4"
                message="No stored disclosures match this filter"
                detail={
                  down
                    ? "Note the source outage below still applies: both free mirrors are down, so only already-stored history is searchable."
                    : "Clear the shared symbol filter, member, or chamber to widen the search."
                }
              />
            ) : (
              <EmptyState
                className="m-4"
                message="No congressional trades stored"
                detail={
                  down
                    ? "The free Stock Watcher mirrors are currently offline (both chambers), so nothing has been ingested yet. The poller keeps checking every ~12h and will backfill automatically if a mirror revives — or set SIGNALDECK_SENATE_TRADES_URL / SIGNALDECK_HOUSE_TRADES_URL to an alternate mirror."
                    : "The congress-poller sweeps the free disclosure mirrors every ~12h."
                }
              />
            )
          ) : (
            <ul style={{ borderTop: "1px solid var(--border)" }}>
              {list.map((t) => (
                <li
                  key={t.id}
                  className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-[0.8rem]"
                  style={{ borderBottom: "1px solid var(--border)" }}
                >
                  {t.symbolId !== null ? (
                    <Link
                      href={`/s/stocks/${encodeURIComponent(t.symbol)}`}
                      className="tnum w-16 font-bold hover:underline"
                      style={{ color: "var(--text)" }}
                    >
                      {t.symbol}
                    </Link>
                  ) : (
                    <span
                      className="tnum w-16 font-bold"
                      style={{ color: "var(--text)" }}
                      title="ticker as disclosed (not tracked by SignalDeck)"
                    >
                      {t.symbol}
                    </span>
                  )}
                  <span
                    className="chip"
                    style={{ color: txColor(t.txType), borderColor: txColor(t.txType) }}
                  >
                    {txBadge(t.txType)}
                  </span>
                  <span style={{ color: "var(--text)" }}>{t.member}</span>
                  <span className="chip" style={{ color: "var(--dim)" }}>
                    {t.chamber}
                  </span>
                  <span className="tnum" style={{ color: "var(--dim)" }} title="range as disclosed — exact amounts are not published">
                    {t.amountRange || "—"}
                  </span>
                  <span className="tnum ml-auto text-[0.72rem]" style={{ color: "var(--faint)" }}>
                    traded {t.txTs > 0 ? fmtDate(t.txTs) : "n/a"} · disclosed{" "}
                    {t.disclosedTs > 0 ? fmtDate(t.disclosedTs) : "n/a"}
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
