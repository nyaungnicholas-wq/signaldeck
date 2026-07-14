"use client";

// COMPANIES DIRECTORY (Signal8 wave, Stage 5): the full SEC-registered
// company table — free EDGAR company_tickers_exchange.json (~10.4k rows,
// synced daily) joined server-side to OUR tracked data where present.
//
// HONESTY RULES (rendered, not implied):
//   - untracked rows show name/exchange/sector only; every market column is
//     "—" — never a fabricated price/mcap;
//   - prices are stored daily closes on worker cadence, not live quotes (the
//     API note is rendered verbatim below the table);
//   - sector is the SEC's own SIC industry description; "" means "not
//     classified by a filings sweep yet", which renders as "—" too;
//   - a mcap filter excludes unknown-mcap rows AND says how many it excluded.
//
// Tracked rows link to /s/stocks/SYM. Untracked rows get a "track" button
// through the existing POST /api/candidates/add path (respects the stream
// cap — a 409 shows the daemon's message verbatim).

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  addCandidate,
  companiesList,
  type CompaniesResponse,
  type CompanyDirRow,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice } from "@/lib/format";
import PagePurpose from "@/components/PagePurpose";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";

const PAGE_SIZE = 50;

/** Mcap bucket presets (dollars). 0 = unbounded on that side. */
const MCAP_BUCKETS: { key: string; label: string; min: number; max: number }[] = [
  { key: "any", label: "any mcap", min: 0, max: 0 },
  { key: "mega", label: "mega ≥ $200B", min: 200e9, max: 0 },
  { key: "large", label: "large $10–200B", min: 10e9, max: 200e9 },
  { key: "mid", label: "mid $2–10B", min: 2e9, max: 10e9 },
  { key: "small", label: "small $300M–2B", min: 300e6, max: 2e9 },
  { key: "micro", label: "micro < $300M", min: 1, max: 300e6 },
];

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

  // filters
  const [search, setSearch] = useState("");
  const [sector, setSector] = useState("");
  const [exchange, setExchange] = useState("");
  const [bucket, setBucket] = useState("any");
  const [trackedOnly, setTrackedOnly] = useState(false);
  const [offset, setOffset] = useState(0);

  const [resp, setResp] = useState<CompaniesResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  // per-row track-button state
  const [busy, setBusy] = useState<string | null>(null);
  const [trackMsg, setTrackMsg] = useState<string | null>(null);
  const [locallyTracked, setLocallyTracked] = useState<Record<string, boolean>>({});

  // The hub-wide symbol filter doubles as the search when the local box is
  // empty (same UX as the other intel sub-tabs).
  const effectiveQ = search.trim() !== "" ? search.trim() : intelSym;

  // Debounced fetch: filters reset the page; the query fires 250ms after the
  // last keystroke so typing doesn't hammer the daemon.
  const debounce = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Any filter change resets pagination — guarded adjustment during render,
  // so the fetch effect below already sees offset 0 (no double fetch).
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
        // 409 = stream cap; 401 = not logged in — show the daemon's words.
        setTrackMsg(e instanceof Error ? e.message : String(e));
      })
      .finally(() => setBusy(null));
  };

  const loading = resp === null && err === null;
  const emptyDirectory = resp !== null && resp.directoryCount === 0;

  return (
    <div className="flex flex-col gap-3">
      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="intel-companies"
        text="Every SEC-registered company in one directory — and which of them SignalDeck actually tracks. Untracked rows honestly show a dash, never a fabricated price."
      />
      {/* header + filters */}
      <div className="panel flex flex-wrap items-center gap-2 px-3 py-2">
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

      {/* track-action feedback (409 cap message etc., verbatim) */}
      {trackMsg !== null && (
        <p
          className="panel px-4 py-2 text-[0.75rem] leading-relaxed"
          role="status"
          style={{ color: "var(--dim)" }}
        >
          {trackMsg}
        </p>
      )}

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
        <section className="panel">
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
            <table className="w-full text-[0.75rem]">
              <thead>
                <tr
                  className="text-left text-[0.75rem] tracking-[0.12em]"
                  style={{ color: "var(--dim)", borderBottom: "1px solid var(--border)" }}
                >
                  <th className="px-3 py-2">TICKER</th>
                  <th className="px-3 py-2">NAME</th>
                  <th className="px-3 py-2">EXCH</th>
                  <th className="px-3 py-2 text-right">PRICE</th>
                  <th className="px-3 py-2 text-right">CHG%</th>
                  <th className="px-3 py-2 text-right">MKT CAP</th>
                  <th className="px-3 py-2">SECTOR (SIC)</th>
                  <th className="px-3 py-2 text-right">VOLUME</th>
                  <th className="px-3 py-2 text-right">FLOAT</th>
                  <th className="px-3 py-2 text-right">SHARES</th>
                  <th className="px-3 py-2 text-right">STATUS</th>
                </tr>
              </thead>
              <tbody className="tnum">
                {rows.map((c) => {
                  const tracked = c.tracked || locallyTracked[c.ticker] === true;
                  const chg = c.dayChangePct;
                  return (
                    <tr
                      key={c.ticker}
                      className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                      style={{ borderBottom: "1px solid var(--border)" }}
                    >
                      <td className="px-3 py-2 font-bold">
                        {tracked ? (
                          <Link
                            href={`/s/stocks/${encodeURIComponent(c.ticker)}`}
                            className="cursor-pointer transition-colors duration-150 hover:text-[var(--accent)]"
                            title={`Investigate ${c.ticker} — open its symbol page`}
                          >
                            {c.ticker}
                          </Link>
                        ) : (
                          <span style={{ color: "var(--dim)" }}>{c.ticker}</span>
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
                      <td className="px-3 py-2 text-right">{fmtBig(c.mcap)}</td>
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
                          <span className="chip" style={{ color: "var(--ok)", borderColor: "var(--ok)" }}>
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

          {/* pagination */}
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

          {/* honesty notes, verbatim */}
          <div className="flex flex-col gap-1 px-3 pb-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            <p>{resp.note}</p>
            <p>{resp.mcapNote}</p>
          </div>
        </section>
      )}
    </div>
  );
}
