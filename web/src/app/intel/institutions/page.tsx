"use client";

// Institutional holdings (Signal8 wave, Stage 1): 13F-HR positions of the
// curated notable-manager list. HONESTY, prominently: 13F snapshots are
// QUARTERLY and filed up to 45 days after quarter end — positions may have
// changed since; the API's lag note is rendered verbatim. Issuer→symbol
// matching is best-effort by name; unmatched rows honestly keep no ticker.

import { Suspense, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  institutionsByManager,
  institutionsBySymbol,
  institutionsOverview,
  type InstHolding,
  type InstitutionsOverview,
} from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";

function fmtUSD(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  if (Math.abs(v) >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (Math.abs(v) >= 1e6) return `$${(v / 1e6).toFixed(2)}M`;
  if (Math.abs(v) >= 1e3) return `$${(v / 1e3).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

function fmtShares(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  return v.toLocaleString();
}

/** Manager drill-down: one manager's latest stored 13F book. */
function ManagerHoldings({ manager }: { manager: string }) {
  const [rows, setRows] = useState<InstHolding[] | null>(null);
  const [note, setNote] = useState("");
  const [cik, setCik] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    institutionsByManager(manager, 200)
      .then((r) => {
        if (!alive) return;
        setRows(r.holdings ?? []);
        setNote(r.note);
        setCik(r.cik);
        setErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [manager, retryTick]);

  const list = useMemo(() => rows ?? [], [rows]);
  const managerName = list.length > 0 ? list[0].manager : manager;
  const period = list.length > 0 ? list[0].period : "";

  if (rows === null && err !== null) {
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? 13F books are stored by the 13f-poller (24h cadence)."
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  }
  if (rows === null) return <Skeleton lines={6} label="loading manager holdings" />;

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        <span>{managerName.toUpperCase()} — LATEST 13F BOOK</span>
        {period && <span className="chip tnum">quarter end {period}</span>}
        {cik && <span className="chip tnum">CIK {cik}</span>}
        <span className="chip tnum">{list.length} positions</span>
        <Link
          href="/intel/institutions"
          className="chip ml-auto min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
        >
          ← all managers
        </Link>
      </div>

      {/* the honest quarterly-lag note, verbatim from the API */}
      <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        {note}
      </p>

      {list.length === 0 ? (
        <EmptyState
          className="m-4"
          message="No holdings stored for this manager yet"
          detail="The 13f-poller rotates through the curated list daily and stores each manager's latest 13F-HR once per report period."
        />
      ) : (
        <div style={{ overflowX: "auto" }}>
          <table className="w-full text-[0.78rem]">
            <thead>
              <tr
                className="text-left text-[0.68rem] tracking-wider"
                style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}
              >
                <th className="px-4 py-2 font-normal">ISSUER (AS FILED)</th>
                <th className="px-2 py-2 font-normal">SYMBOL</th>
                <th className="px-2 py-2 font-normal">CUSIP</th>
                <th className="px-2 py-2 text-right font-normal">SHARES</th>
                <th className="px-4 py-2 text-right font-normal">VALUE (AS REPORTED)</th>
              </tr>
            </thead>
            <tbody>
              {list.map((h) => (
                <tr key={h.cusip} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td className="px-4 py-2" style={{ color: "var(--text)" }}>
                    {h.name}
                  </td>
                  <td className="px-2 py-2">
                    {h.symbol ? (
                      <Link
                        href={`/s/stocks/${encodeURIComponent(h.symbol)}`}
                        className="tnum font-bold hover:text-[var(--accent)]"
                        style={{ color: "var(--text)" }}
                      >
                        {h.symbol}
                      </Link>
                    ) : (
                      <span title="issuer name not matched to a tracked symbol (best-effort matching, honest null)" style={{ color: "var(--faint)" }}>
                        unmatched
                      </span>
                    )}
                  </td>
                  <td className="tnum px-2 py-2" style={{ color: "var(--dim)" }}>
                    {h.cusip}
                  </td>
                  <td className="tnum px-2 py-2 text-right" style={{ color: "var(--dim)" }}>
                    {fmtShares(h.shares)}
                  </td>
                  <td className="tnum px-4 py-2 text-right" style={{ color: "var(--text)" }}>
                    {fmtUSD(h.value)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

/** Overview: managers with stored books + the curated watch list. */
function ManagersOverview() {
  const [resp, setResp] = useState<InstitutionsOverview | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    institutionsOverview()
      .then((r) => {
        if (!alive) return;
        setResp(r);
        setErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [retryTick]);

  if (resp === null && err !== null) {
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? The 13f-poller needs nothing but SEC EDGAR."
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  }
  if (resp === null) return <Skeleton lines={6} label="loading institutional managers" />;

  const stored = resp.managers ?? [];
  const storedCiks = new Set(stored.map((m) => m.cik));
  const pending = resp.curated.filter((c) => !storedCiks.has(String(c.cik)));

  return (
    <div className="flex flex-col gap-4">
      <section className="panel">
        <div className="panel-h flex-wrap gap-2">
          STORED 13F BOOKS
          <span className="chip tnum">{stored.length} of {resp.curated.length} curated managers</span>
        </div>

        {/* the honest quarterly-lag note, verbatim from the API */}
        <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {resp.note}
        </p>

        {stored.length === 0 ? (
          <EmptyState
            className="m-4"
            message="No 13F books stored yet — SEC sweep in progress"
            detail="The 13f-poller rotates through the curated managers (rate-limited per SEC policy, runs at daemon boot and daily) — books appear within ~2h of a completed sweep. 13F itself is quarterly and filed up to 45 days after quarter end."
          />
        ) : (
          <ul style={{ borderTop: "1px solid var(--border)" }}>
            {stored.map((m) => (
              <li key={m.cik} style={{ borderBottom: "1px solid var(--border)" }}>
                <Link
                  href={`/intel/institutions?manager=${encodeURIComponent(m.cik)}`}
                  className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 transition-colors duration-150 hover:bg-[var(--panel2)]"
                >
                  <span className="font-bold" style={{ color: "var(--text)" }}>
                    {m.manager}
                  </span>
                  <span className="chip tnum">quarter end {m.period}</span>
                  <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
                    {m.positions} positions
                  </span>
                  <span className="tnum ml-auto" style={{ color: "var(--text)" }}>
                    {fmtUSD(m.totalValue)}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>

      {pending.length > 0 && (
        <section className="panel">
          <div className="panel-h flex-wrap gap-2">
            CURATED — NOT YET STORED
            <span className="chip tnum">{pending.length}</span>
            <span
              className="text-[0.68rem] font-normal normal-case tracking-normal"
              style={{ color: "var(--faint)" }}
            >
              watched managers whose latest 13F-HR hasn&rsquo;t been swept in yet
            </span>
          </div>
          <div className="flex flex-wrap gap-2 px-4 py-3">
            {pending.map((c) => (
              <Link
                key={c.cik}
                href={`/intel/institutions?manager=${encodeURIComponent(String(c.cik))}`}
                className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
              >
                {c.name}
              </Link>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

/** Stage 5: shared-filter view — which curated managers hold one symbol. */
function SymbolHolders({ symbol }: { symbol: string }) {
  const [rows, setRows] = useState<InstHolding[] | null>(null);
  const [note, setNote] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    institutionsBySymbol(symbol, 100)
      .then((r) => {
        if (!alive) return;
        setRows(r.holdings ?? []);
        setNote(r.note);
        setErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        const msg = e instanceof Error ? e.message : String(e);
        // Exact-ticker API: unknown/partial symbol 404s — a filter miss.
        if (msg.includes("404")) {
          setRows([]);
          setErr(null);
          return;
        }
        setErr(msg);
      });
    return () => {
      alive = false;
    };
  }, [symbol, retryTick]);

  if (rows === null && err !== null) {
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? 13F books are stored by the 13f-poller."
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  }
  if (rows === null) return <Skeleton lines={4} label={`loading ${symbol} holders`} />;

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        <span>WHO HOLDS {symbol}</span>
        <span className="chip tnum">{rows.length} curated managers</span>
        <span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
          from the shared intel filter
        </span>
      </div>
      {note && (
        <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {note}
        </p>
      )}
      {rows.length === 0 ? (
        <EmptyState
          className="m-4"
          message={`No stored 13F position in ${symbol}`}
          detail="Either no curated manager reported it last quarter, that ticker isn't tracked, or the 13f-poller hasn't swept the relevant books yet (SEC sweep in progress — books appear within ~2h of a completed sweep)."
        />
      ) : (
        <ul style={{ borderTop: "1px solid var(--border)" }}>
          {rows.map((h) => (
            <li
              key={`${h.manager}:${h.cusip}`}
              className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-[0.8rem]"
              style={{ borderBottom: "1px solid var(--border)" }}
            >
              <span className="font-bold" style={{ color: "var(--text)" }}>
                {h.manager}
              </span>
              <span className="chip tnum">quarter end {h.period}</span>
              <span className="tnum" style={{ color: "var(--dim)" }}>
                {fmtShares(h.shares)} sh
              </span>
              <span className="tnum ml-auto" style={{ color: "var(--text)" }}>
                {fmtUSD(h.value)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function InstitutionsInner() {
  const params = useSearchParams();
  const manager = params.get("manager") ?? "";
  // Stage 5: the hub-wide symbol filter flips this tab to "who holds it".
  const { symbol } = useIntelSymbol();

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">INSTITUTIONS</h1>
        <span className="chip">SEC 13F-HR · quarterly, filed up to 45 days after quarter end</span>
      </div>
      {symbol ? (
        <SymbolHolders symbol={symbol} />
      ) : manager ? (
        <ManagerHoldings manager={manager} />
      ) : (
        <ManagersOverview />
      )}
    </div>
  );
}

export default function InstitutionsPage() {
  // useSearchParams requires a Suspense boundary for prerendering.
  return (
    <Suspense fallback={<Skeleton lines={6} label="loading institutions" />}>
      <InstitutionsInner />
    </Suspense>
  );
}
