"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  addCandidate,
  companiesList,
  type CompaniesResponse,
  type CompanyDirRow,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import { StatTile, PageHero, MiniBar } from "@/components/ui/Kit";

// Module scope, not inside CompaniesPage: a component declared in another
// component's render body is a NEW type on every render, so React unmounts and
// remounts the subtree instead of updating it (react-hooks/static-components).
// What it closed over — the active sort — is passed in explicitly.
const SortArrow = ({ column, sortKey, sortDir }: { column: SortKey; sortKey: SortKey; sortDir: SortDir }) => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="inline ml-1">
    {sortKey === column ? (
      <path d={sortDir === "asc" ? "M12 5v14M5 12l7-7 7 7" : "M12 19V5M19 12l-7 7-7-7"} />
    ) : (
      <path d="M12 5v14M5 12l7-7 7 7" opacity="0.4" />
    )}
  </svg>
);

type SortKey = "mcap" | "dayChangePct" | "volume";
type SortDir = "asc" | "desc";

const PAGE_SIZE = 50;

const MCAP_BUCKETS = [
  { key: "any", label: "any mcap", min: 0, max: 0 },
  { key: "mega", label: "mega ≥ $200B", min: 200e9, max: 0 },
  { key: "large", label: "large $10–200B", min: 10e9, max: 200e9 },
  { key: "mid", label: "mid $2–10B", min: 2e9, max: 10e9 },
  { key: "small", label: "small $300M–2B", min: 300e6, max: 2e9 },
  { key: "micro", label: "micro < $300M", min: 1, max: 300e6 },
] as const;

function fmtBig(v: number | null): string {
  if (v === null || !isFinite(v) || v <= 0) return "—";
  if (v >= 1e12) return `$${(v / 1e12).toFixed(2)}T`;
  if (v >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `$${(v / 1e6).toFixed(1)}M`;
  return `$${v.toFixed(0)}`;
}

function fmtShares(v: number | null): string {
  if (v === null || !isFinite(v) || v <= 0) return "—";
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  return v.toFixed(0);
}

function fmtVol(v: number | null): string {
  if (v === null || !isFinite(v) || v <= 0) return "—";
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toFixed(0);
}

export default function CompaniesPage() {
  const { symbol: intelSym } = useIntelSymbol();
  const [search, setSearch] = useState("");
  const [sector, setSector] = useState("");
  const [exchange, setExchange] = useState("");
  const [bucket, setBucket] = useState("any");
  const [trackedOnly, setTrackedOnly] = useState(false);
  const [offset, setOffset] = useState(0);
  const [resp, setResp] = useState<CompaniesResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [trackMsg, setTrackMsg] = useState<string | null>(null);
  const [locallyTracked, setLocallyTracked] = useState<Record<string, boolean>>({});
  const [sortKey, setSortKey] = useState<SortKey>("mcap");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  
  const effectiveQ = search.trim() !== "" ? search.trim() : intelSym;
  const debounce = useRef<ReturnType<typeof setTimeout> | null>(null);
  const filterSig = JSON.stringify([effectiveQ, sector, exchange, bucket, trackedOnly]);
  const [prevFilterSig, setPrevFilterSig] = useState(filterSig);
  if (prevFilterSig !== filterSig) {
    setPrevFilterSig(filterSig);
    setOffset(0);
  }

  useEffect(() => {
    let alive = true;
    const b = MCAP_BUCKETS.find((x) => x.key === bucket) ?? MCAP_BUCKETS[0];
    const load = () =>
      companiesList({
        q: effectiveQ || undefined,
        sector: sector || undefined,
        exchange: exchange || undefined,
        mcapMin: b.min,
        mcapMax: b.max,
        tracked: trackedOnly,
        limit: PAGE_SIZE,
        offset,
      })
        .then((r) => {
          if (!alive) return;
          setResp(r);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          setErr(
            msg.includes("404")
              ? "the running daemon predates GET /api/companies — restart signaldeckd with the current binary and the directory appears"
              : msg,
          );
        });
    if (debounce.current) clearTimeout(debounce.current);
    debounce.current = setTimeout(load, 250);
    return () => {
      alive = false;
      if (debounce.current) clearTimeout(debounce.current);
    };
  }, [effectiveQ, sector, exchange, bucket, trackedOnly, offset, retryTick]);

  const rows = useMemo(() => resp?.companies ?? [], [resp]);
  const total = resp?.total ?? 0;
  const pageFrom = total === 0 ? 0 : offset + 1;
  const pageTo = Math.min(offset + rows.length, total);

  const track = (c: CompanyDirRow) => {
    setBusy(c.ticker);
    setTrackMsg(null);
    addCandidate(c.ticker, "stocks")
      .then(() => {
        setLocallyTracked((m) => ({ ...m, [c.ticker]: true }));
        setTrackMsg(`${c.ticker} is now monitored — backfill starts now; market data fills in shortly.`);
      })
      .catch((e: unknown) => {
        setTrackMsg(e instanceof Error ? e.message : String(e));
      })
      .finally(() => setBusy(null));
  };

  const loading = resp === null && err === null;
  const emptyDirectory = resp !== null && resp.directoryCount === 0;

  const sortedRows = useMemo(() => {
    const sorted = [...rows];
    sorted.sort((a, b) => {
      const dir = sortDir === "asc" ? 1 : -1;
      switch (sortKey) {
        case "mcap":
          return ((a.mcap ?? 0) - (b.mcap ?? 0)) * dir;
        case "dayChangePct":
          return ((a.dayChangePct ?? 0) - (b.dayChangePct ?? 0)) * dir;
        case "volume":
          return ((a.volume ?? 0) - (b.volume ?? 0)) * dir;
        default:
          return 0;
      }
    });
    return sorted;
  }, [rows, sortKey, sortDir]);

  const maxMcap = useMemo(() => {
    if (rows.length === 0) return 0;
    return Math.max(...rows.map((r) => r.mcap ?? 0));
  }, [rows]);

  const stats = useMemo(() => {
    if (rows.length === 0) return { totalTracked: 0, avgChange: 0, biggestCompany: "" };
    const trackedRows = rows.filter((r) => r.tracked || locallyTracked[r.ticker] === true);
    const changes = rows.filter((r) => r.dayChangePct !== null).map((r) => r.dayChangePct!);
    const avgChange = changes.length > 0 ? changes.reduce((a, b) => a + b, 0) / changes.length : 0;
    const biggest = rows.reduce((max, r) => (r.mcap ?? 0) > (max.mcap ?? 0) ? r : max, rows[0]);
    return {
      totalTracked: trackedRows.length,
      avgChange,
      biggestCompany: biggest.ticker,
    };
  }, [rows, locallyTracked]);

  const handleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("desc");
    }
  };

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Companies"
        subtitle="Every SEC-registered company in one directory — fundamentals and profile at a glance."
        right={
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={intelSym ? `search (hub filter: ${intelSym})` : "search ticker or name…"}
              aria-label="search companies by ticker or name"
              className="chip min-h-[40px] w-52 bg-transparent px-3 outline-none"
              style={{ color: "var(--text)" }}
            />
            <select
              value={sector}
              onChange={(e) => setSector(e.target.value)}
              aria-label="filter by SIC sector"
              className="chip min-h-[40px] max-w-64 cursor-pointer bg-transparent px-2"
              style={{ color: sector ? "var(--text)" : "var(--dim)", background: "var(--panel)" }}
            >
              <option value="">all sectors (SIC)</option>
              {(resp?.sectors ?? []).map((s) => (
                <option key={s.value} value={s.value}>
                  {s.value} ({s.n})
                </option>
              ))}
            </select>
            <select
              value={exchange}
              onChange={(e) => setExchange(e.target.value)}
              aria-label="filter by exchange"
              className="chip min-h-[40px] cursor-pointer bg-transparent px-2"
              style={{ color: exchange ? "var(--text)" : "var(--dim)", background: "var(--panel)" }}
            >
              <option value="">all exchanges</option>
              {(resp?.exchanges ?? []).map((x) => (
                <option key={x.value} value={x.value}>
                  {x.value} ({x.n})
                </option>
              ))}
            </select>
            <select
              value={bucket}
              onChange={(e) => setBucket(e.target.value)}
              aria-label="filter by market-cap bucket"
              className="chip min-h-[40px] cursor-pointer bg-transparent px-2"
              style={{ color: bucket !== "any" ? "var(--text)" : "var(--dim)", background: "var(--panel)" }}
            >
              {MCAP_BUCKETS.map((b) => (
                <option key={b.key} value={b.key}>
                  {b.label}
                </option>
              ))}
            </select>
            <button
              type="button"
              onClick={() => setTrackedOnly((v) => !v)}
              aria-pressed={trackedOnly}
              className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
              title="Show only symbols we track (have market data for)"
              style={{
                color: trackedOnly ? "var(--accent)" : undefined,
                borderColor: trackedOnly ? "var(--accent)" : undefined,
              }}
            >
              tracked only
            </button>
            {resp !== null && (
              <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {resp.directoryCount.toLocaleString()} SEC registrants
                {resp.lastSyncTs > 0 ? ` · synced ${ago(resp.lastSyncTs)}` : ""}
              </span>
            )}
          </div>
        }
      />

      {trackMsg !== null && (
        <div className="panel px-4 py-2 text-[0.75rem] leading-relaxed reveal-item" style={{ "--i": 0 } as React.CSSProperties} role="status">
          <span style={{ color: "var(--dim)" }}>{trackMsg}</span>
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="COMPANIES" value={total} i={0} />
        <StatTile label="TRACKED" value={stats.totalTracked} i={1} />
        <StatTile label="BIGGEST" value={stats.biggestCompany} sub={fmtBig(rows.find(r => r.ticker === stats.biggestCompany)?.mcap ?? 0)} i={2} glow="hud" />
        <StatTile label="AVG CHG" value={stats.avgChange} decimals={2} suffix="%" i={3} glow={stats.avgChange >= 0 ? "up" : "down"} />
      </div>

      {loading && <Skeleton lines={8} label="loading company directory" />}

      {err !== null && resp === null && (
        <ErrorState
          message={err}
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {emptyDirectory && (
        <EmptyState
          message="Directory not synced yet"
          detail="companies-sync mirrors the SEC company map (one free EDGAR request) on its 24h cadence — usually within seconds of daemon start."
        />
      )}

      {resp !== null && !emptyDirectory && rows.length === 0 && (
        <EmptyState
          message="No companies match these filters"
          detail={
            resp.unknownMcapExcluded > 0
              ? `${resp.unknownMcapExcluded} row(s) excluded because their mcap is unknown (EDGAR hasn't covered them) — mcap filters only apply to known values.`
              : "Try clearing the search or widening the filters."
          }
        />
      )}

      {resp !== null && rows.length > 0 && (
        <section className="panel hud-panel">
          <div className="panel-h flex-wrap gap-2">
            COMPANIES
            <span className="chip tnum">{total.toLocaleString()} match</span>
            <span className="chip tnum">{resp.trackedCount} tracked</span>
            {resp.unknownMcapExcluded > 0 && (
              <span className="chip tnum" style={{ color: "var(--warn)", borderColor: "var(--warn)" }}>
                {resp.unknownMcapExcluded} excluded — mcap unknown
              </span>
            )}
            <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
              daily closes, worker cadence — not live quotes
            </span>
          </div>

          <div className="table-wrap">
            <table className="v4-table w-full text-[0.75rem]">
              <thead>
                <tr
                  className="text-left text-[0.75rem] tracking-[0.12em]"
                  style={{ color: "var(--dim)", borderBottom: "1px solid var(--border)" }}
                >
                  <th className="px-3 py-2">TICKER</th>
                  <th className="px-3 py-2">NAME</th>
                  <th className="px-3 py-2">EXCH</th>
                  <th className="px-3 py-2 text-right">PRICE</th>
                  <th className="px-3 py-2 text-right cursor-pointer select-none" onClick={() => handleSort("dayChangePct")}>
                    CHG%<SortArrow column="dayChangePct" sortKey={sortKey} sortDir={sortDir} />
                  </th>
                  <th className="px-3 py-2 text-right cursor-pointer select-none" onClick={() => handleSort("mcap")}>
                    MKT CAP<SortArrow column="mcap" sortKey={sortKey} sortDir={sortDir} />
                  </th>
                  <th className="px-3 py-2">SECTOR (SIC)</th>
                  <th className="px-3 py-2 text-right cursor-pointer select-none" onClick={() => handleSort("volume")}>
                    VOLUME<SortArrow column="volume" sortKey={sortKey} sortDir={sortDir} />
                  </th>
                  <th className="px-3 py-2 text-right">FLOAT</th>
                  <th className="px-3 py-2 text-right">SHARES</th>
                  <th className="px-3 py-2 text-right">STATUS</th>
                </tr>
              </thead>
              <tbody className="tnum">
                {sortedRows.map((c, i) => {
                  const tracked = c.tracked || locallyTracked[c.ticker] === true;
                  const chg = c.dayChangePct;
                  const rowBorder = chg !== null ? (chg >= 0 ? "var(--bid)" : "var(--ask)") : "transparent";
                  return (
                    <tr
                      key={c.ticker}
                      className="reveal-item transition-colors duration-150 hover:bg-[var(--panel2)]"
                      style={{ 
                        "--i": Math.min(i, 12),
                        borderBottom: "1px solid var(--border)",
                        borderLeft: `3px solid ${rowBorder}`
                      } as React.CSSProperties}
                    >
                      <td className="px-3 py-2 font-bold">
                        {tracked ? (
                          <Link
                            href={`/s/stocks/${encodeURIComponent(c.ticker)}`}
                            className="mono cursor-pointer transition-colors duration-150 hover:text-[var(--accent)]"
                            title={`Investigate ${c.ticker} — open its symbol page`}
                          >
                            {c.ticker}
                          </Link>
                        ) : (
                          <span className="mono" style={{ color: "var(--dim)" }}>{c.ticker}</span>
                        )}
                      </td>
                      <td
                        className="max-w-64 truncate px-3 py-2"
                        style={{ color: "var(--dim)" }}
                        title={c.name}
                      >
                        {c.name || "—"}
                      </td>
                      <td className="px-3 py-2" style={{ color: "var(--faint)" }}>
                        {c.exchange || "—"}
                      </td>
                      <td className="px-3 py-2 text-right">
                        {c.price !== null ? fmtPrice(c.price) : "—"}
                      </td>
                      <td
                        className="px-3 py-2 text-right"
                        style={{
                          color:
                            chg === null ? "var(--faint)" : chg >= 0 ? "var(--bid)" : "var(--ask)",
                        }}
                      >
                        {chg !== null ? fmtPct(chg) : "—"}
                      </td>
                      <td className="px-3 py-2 text-right">
                        <div className="flex items-center justify-end gap-2">
                          <MiniBar value={c.mcap ?? 0} max={maxMcap} color="var(--accent)" i={i} />
                          <span>{fmtBig(c.mcap)}</span>
                        </div>
                      </td>
                      <td
                        className="max-w-56 truncate px-3 py-2"
                        style={{ color: c.sicDesc ? "var(--dim)" : "var(--faint)" }}
                        title={c.sicDesc || "not classified by a filings sweep yet"}
                      >
                        {c.sicDesc || "—"}
                      </td>
                      <td className="px-3 py-2 text-right">{fmtVol(c.volume)}</td>
                      <td className="px-3 py-2 text-right">{fmtBig(c.float)}</td>
                      <td className="px-3 py-2 text-right">{fmtShares(c.sharesOutstanding)}</td>
                      <td className="px-3 py-2 text-right">
                        {tracked ? (
                          <span className="chip" style={{ color: "var(--bid)", borderColor: "var(--bid)" }}>
                            tracked
                          </span>
                        ) : (
                          <button
                            type="button"
                            disabled={busy !== null}
                            onClick={() => track(c)}
                            title={`Monitor ${c.ticker} (needs login; respects the stream cap — a 409 shows the cap message)`}
                            className="chip min-h-[40px] cursor-pointer px-3 font-bold transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
                            style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
                          >
                            {busy === c.ticker ? "…" : "monitor"}
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className="flex flex-wrap items-center gap-2 px-3 py-2" style={{ borderTop: "1px solid var(--border)" }}>
            <button
              type="button"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              className="chip min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              ← prev
            </button>
            <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {pageFrom.toLocaleString()}–{pageTo.toLocaleString()} of {total.toLocaleString()}
            </span>
            <button
              type="button"
              disabled={offset + PAGE_SIZE >= total}
              onClick={() => setOffset(offset + PAGE_SIZE)}
              className="chip min-h-[40px] cursor-pointer px-4 transition-colors duration-150 hover:text-[var(--text)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              next →
            </button>
            <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
              untracked rows have no market data yet — &ldquo;monitor&rdquo; starts ingestion
            </span>
          </div>

          <div className="flex flex-col gap-1 px-3 pb-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            <p>{resp.note}</p>
            <p>{resp.mcapNote}</p>
          </div>
        </section>
      )}
    </div>
  );
}
