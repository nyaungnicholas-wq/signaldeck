"use client";
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { congress, pollMs, POLL_SLOW, type CongressMirrorStatus, type CongressTrade } from "@/lib/api";
import { fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import { Reveal, PageHero, StatTile } from "@/components/ui/Kit";

type ChamberFilter = "all" | "senate" | "house";
type SortKey = "date" | "size" | "symbol";

function txColor(txType: string): string {
  if (txType === "purchase") return "var(--bid)";
  if (txType.startsWith("sale")) return "var(--ask)";
  return "var(--dim)";
}
function txBadge(txType: string): string {
  switch (txType) {
    case "purchase": return "BUY";
    case "sale_full": return "SELL (full)";
    case "sale_partial": return "SELL (partial)";
    case "sale": return "SELL";
    case "exchange": return "EXCHANGE";
    default: return txType.toUpperCase();
  }
}
function mirrorsDown(source?: CongressMirrorStatus | null): boolean {
  if (!source) return false;
  return source.senate?.ok === false && source.house?.ok === false;
}

const SortArrow = ({ active, dir }: { active: boolean; dir: "asc" | "desc" }) => (
  <svg className={`inline ml-1 w-3 h-3 ${active ? "text-[var(--accent)]" : "text-[var(--dim)]"}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
    <path d={dir === "asc" ? "M12 5v14M5 12l7-7 7 7" : "M12 19V5M5 12l7 7 7-7"} />
  </svg>
);

export default function CongressPage() {
  const [rows, setRows] = useState<CongressTrade[] | null>(null);
  const [lagNote, setLagNote] = useState("");
  const [note, setNote] = useState("");
  const [source, setSource] = useState<CongressMirrorStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [chamber, setChamber] = useState<ChamberFilter>("all");
  const { symbol } = useIntelSymbol();
  const [member, setMember] = useState("");
  const [retryTick, setRetryTick] = useState(0);
  const [sortKey, setSortKey] = useState<SortKey>("date");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");

  useEffect(() => {
    let alive = true;
    const load = () =>
      congress(symbol || undefined, member || undefined, chamber === "all" ? undefined : chamber, 200)
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
    const stop = pollMs(load, POLL_SLOW);
    return () => { alive = false; stop(); };
  }, [chamber, symbol, member, retryTick]);

  const loading = rows === null && err === null;
  const hardError = rows === null && err !== null;
  const list = useMemo(() => rows ?? [], [rows]);
  const down = mirrorsDown(source);

  const { purchases, sales, latestDisclosed } = useMemo(() => {
    let purchases = 0, sales = 0, latestDisclosed = 0;
    list.forEach(t => {
      if (t.txType === "purchase") purchases++;
      if (t.txType.startsWith("sale")) sales++;
      if (t.disclosedTs > latestDisclosed) latestDisclosed = t.disclosedTs;
    });
    return { purchases, sales, latestDisclosed };
  }, [list]);

  const sortedList = useMemo(() => {
    const sorted = [...list];
    sorted.sort((a, b) => {
      const dir = sortDir === "asc" ? 1 : -1;
      if (sortKey === "date") return (a.disclosedTs - b.disclosedTs) * dir;
      if (sortKey === "symbol") return a.symbol.localeCompare(b.symbol) * dir;
      return (a.txTs - b.txTs) * dir;
    });
    return sorted;
  }, [list, sortKey, sortDir]);

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir(d => d === "asc" ? "desc" : "asc");
    else { setSortKey(key); setSortDir(key === "date" ? "desc" : "asc"); }
  };

  const heroControls = (
    <div className="flex flex-wrap items-center gap-2">
      <span className="chip" style={{ color: "var(--warn)", borderColor: "var(--warn)" }}>disclosures lag 30–45 days by law</span>
      {down && <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>free mirrors unreachable</span>}
      {err !== null && rows !== null && <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>poll failed</span>}
    </div>
  );

  return (
    <div className="page-enter space-y-4">
      <PageHero title="Congress Trades" subtitle="What lawmakers are buying and selling — disclosed trades, sorted so the signal stands out." right={heroControls} />

      <Reveal>
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {/* page size, not a universe count: the fetch caps at 200 and the
              response carries no total, so this can never exceed 200. */}
          <StatTile label="Disclosures Shown" value={list.length} glow="hud" i={0} />
          <StatTile label="Purchases" value={purchases} glow="up" i={1} />
          <StatTile label="Sales" value={sales} glow="down" i={2} />
          <StatTile label="Newest Disclosure" value={latestDisclosed > 0 ? fmtDate(latestDisclosed) : "—"} i={3} />
        </div>
      </Reveal>

      {loading && <Skeleton lines={6} label="loading congressional trades" />}
      {hardError && <ErrorState message={err ?? "data unavailable"} hint="Check daemon. Poller sweeps free mirrors ~12h." retry={() => { setErr(null); setRetryTick(t => t + 1); }} />}

      {rows !== null && (
        <section className="panel">
          <div className="panel-h flex-wrap gap-2">
            DISCLOSED TRANSACTIONS
            <span className="chip tnum">{list.length} shown</span>
            {symbol && <span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>{symbol}</span>}
            <input value={member} onChange={(e) => setMember(e.target.value)} placeholder="filter member…" aria-label="filter by member"
              className="chip min-h-[36px] w-36 bg-transparent px-3 outline-none" style={{ color: "var(--text)" }} />
            <span className="ml-auto flex items-center gap-1" role="tablist">
              {(["all", "senate", "house"] as ChamberFilter[]).map((c) => (
                <button key={c} type="button" role="tab" aria-selected={c === chamber} onClick={() => setChamber(c)}
                  className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
                  style={{ color: c === chamber ? "var(--accent)" : undefined, borderColor: c === chamber ? "var(--accent)" : undefined }}>
                  {c}
                </button>
              ))}
            </span>
          </div>

          <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>{lagNote} {note}</p>

          {sortedList.length === 0 ? (
            symbol || member || chamber !== "all" ? (
              <EmptyState className="m-4" message="No disclosures match filter" detail={down ? "Source outage — stored history only." : "Widen search."} />
            ) : (
              <EmptyState className="m-4" message="No stored trades" detail={down ? "Free mirrors offline. Poller checks ~12h." : "Poller sweeps mirrors ~12h."} />
            )
          ) : (
            <Reveal>
              <div className="overflow-x-auto table-wrap">
                <table className="w-full text-[0.75rem] v4-table">
                  <thead>
                    <tr className="border-b" style={{ borderColor: "var(--border)" }}>
                      <th className="px-4 py-2 text-left font-medium" style={{ color: "var(--dim)" }}>
                        <button className="flex items-center cursor-pointer hover:text-[var(--text)]" onClick={() => toggleSort("symbol")}>
                          Symbol <SortArrow active={sortKey === "symbol"} dir={sortDir} />
                        </button>
                      </th>
                      <th className="px-4 py-2 text-left font-medium" style={{ color: "var(--dim)" }}>Trade</th>
                      <th className="px-4 py-2 text-left font-medium" style={{ color: "var(--dim)" }}>Member</th>
                      <th className="px-4 py-2 text-left font-medium" style={{ color: "var(--dim)" }}>Chamber</th>
                      <th className="px-4 py-2 text-left font-medium" style={{ color: "var(--dim)" }}>Amount</th>
                      <th className="px-4 py-2 text-right font-medium" style={{ color: "var(--dim)" }}>
                        <button className="flex items-center justify-end cursor-pointer hover:text-[var(--text)]" onClick={() => toggleSort("date")}>
                          Disclosed <SortArrow active={sortKey === "date"} dir={sortDir} />
                        </button>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {sortedList.map((t, i) => {
                      const isBuy = t.txType === "purchase";
                      const borderStyle: React.CSSProperties = isBuy ? { borderLeft: "2px solid var(--bid)" } : t.txType.startsWith("sale") ? { borderLeft: "2px solid var(--ask)" } : {};
                      return (
                        <tr key={t.id} className="reveal-item hover:bg-[color:rgba(255,255,255,0.03)] transition-colors" style={{ "--i": Math.min(i, 12), ...borderStyle } as React.CSSProperties}>
                          <td className="px-4 py-2.5 font-bold mono" style={{ color: "var(--text)" }}>
                            {t.symbolId !== null ? (
                              <Link href={`/s/stocks/${encodeURIComponent(t.symbol)}`} className="hover:underline">{t.symbol}</Link>
                            ) : (
                              <span className="title-attr" title={`${t.symbol} — not tracked`}>{t.symbol}</span>
                            )}
                          </td>
                          <td className="px-4 py-2.5">
                            <span className="chip" style={{ color: txColor(t.txType), borderColor: txColor(t.txType) }}>{txBadge(t.txType)}</span>
                          </td>
                          <td className="px-4 py-2.5 truncate max-w-[180px] title-attr" style={{ color: "var(--text)" }}>{t.member}</td>
                          <td className="px-4 py-2.5" style={{ color: "var(--dim)" }}>{t.chamber}</td>
                          <td className="px-4 py-2.5 tnum" style={{ color: "var(--text)" }}>{t.amountRange || "—"}</td>
                          <td className="px-4 py-2.5 text-right tnum" style={{ color: "var(--faint)" }}>{t.disclosedTs > 0 ? fmtDate(t.disclosedTs) : "n/a"}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </Reveal>
          )}
        </section>
      )}
    </div>
  );
}