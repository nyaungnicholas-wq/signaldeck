"use client";

// FINANCIALS panel (Signal8 wave, Stage 5): the symbol's headline SEC EDGAR
// company-facts — Revenues, EPS, SharesOutstanding, EntityPublicFloat — each
// with its as-of period end, plus small history sparklines for Revenues and
// EPS from the fundamentals table. Stocks only (crypto has no filings).
//
// HONESTY: everything here is XBRL as filed with the SEC, refreshed by the
// ~daily edgar-fetcher sweep. A metric the company never tags simply isn't
// shown a number ("—"); a symbol with no rows at all gets the explicit
// "EDGAR sweep pending" empty state — nothing is ever estimated here.

import { useEffect, useState } from "react";
import { fundamentals, type FundamentalRow } from "@/lib/api";
import { ago, fmtDate } from "@/lib/format";
import Spark from "@/components/Spark";

const POLL_MS = 5 * 60_000; // fundamentals move on filing cadence — poll slowly

function fmtUSDish(v: number): string {
  if (!isFinite(v)) return "—";
  const a = Math.abs(v);
  if (a >= 1e12) return `$${(v / 1e12).toFixed(2)}T`;
  if (a >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (a >= 1e6) return `$${(v / 1e6).toFixed(1)}M`;
  return `$${v.toFixed(2)}`;
}

function fmtCount(v: number): string {
  if (!isFinite(v)) return "—";
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  return v.toFixed(0);
}

/** Display config per metric: label + formatter. */
const METRICS: { key: string; label: string; fmt: (v: number) => string }[] = [
  { key: "Revenues", label: "REVENUE (period)", fmt: fmtUSDish },
  { key: "EPS", label: "EPS (diluted)", fmt: (v) => `$${v.toFixed(2)}` },
  { key: "SharesOutstanding", label: "SHARES OUT", fmt: fmtCount },
  { key: "EntityPublicFloat", label: "PUBLIC FLOAT", fmt: fmtUSDish },
];

export default function FinancialsPanel({ symbol }: { symbol: string }) {
  const [latest, setLatest] = useState<FundamentalRow[] | null>(null);
  const [revHist, setRevHist] = useState<FundamentalRow[]>([]);
  const [epsHist, setEpsHist] = useState<FundamentalRow[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      // Two calls: each returns the latest-metrics map plus ONE metric's
      // history — Revenues and EPS get the sparklines.
      Promise.all([fundamentals(symbol, "Revenues"), fundamentals(symbol, "EPS")])
        .then(([rev, eps]) => {
          if (!alive) return;
          setLatest(rev.metrics ?? []);
          setRevHist(rev.history ?? []);
          setEpsHist(eps.history ?? []);
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
  }, [symbol]);

  const byMetric = new Map<string, FundamentalRow>();
  for (const m of latest ?? []) byMetric.set(m.metric, m);
  // "Swept" means real financial facts exist — CIK/LatestFilingDate alone are
  // bookkeeping rows, not financials.
  const hasFinancials = METRICS.some((m) => byMetric.has(m.key));
  const fetchedAt = latest?.length ? Math.max(...latest.map((m) => m.fetchedAt)) : 0;

  return (
    <section className="panel" aria-label={`SEC EDGAR financials for ${symbol}`}>
      <div className="panel-h flex-wrap gap-2">
        FINANCIALS · {symbol}
        <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
          SEC EDGAR XBRL, as filed · swept ~daily
          {fetchedAt > 0 ? ` · fetched ${ago(fetchedAt)}` : ""}
        </span>
      </div>

      {err !== null && latest === null && (
        <p className="px-4 py-3 text-[0.78rem]" style={{ color: "var(--bad)" }}>
          {err}
        </p>
      )}
      {latest === null && err === null && (
        <p className="px-4 py-3 text-[0.78rem]" style={{ color: "var(--faint)" }}>
          loading EDGAR facts…
        </p>
      )}

      {latest !== null && !hasFinancials && (
        <p className="px-4 py-4 text-[0.78rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          EDGAR sweep pending — the edgar-fetcher rotates the universe ~daily; financials
          appear once this symbol&rsquo;s company-facts are swept (symbols that don&rsquo;t
          file with the SEC never will — honest absence, not an error).
        </p>
      )}

      {latest !== null && hasFinancials && (
        <>
          <div className="grid grid-cols-2 gap-0 lg:grid-cols-4">
            {METRICS.map((m) => {
              const row = byMetric.get(m.key);
              return (
                <div
                  key={m.key}
                  className="px-4 py-3"
                  style={{ borderTop: "1px solid var(--border)" }}
                >
                  <div className="text-[0.68rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
                    {m.label}
                  </div>
                  <div className="tnum mt-1 text-[1.05rem] font-bold" style={{ color: "var(--text)" }}>
                    {row ? m.fmt(row.value) : "—"}
                  </div>
                  <div className="tnum text-[0.66rem]" style={{ color: "var(--faint)" }}>
                    {row && row.asOf > 0 ? `as of ${fmtDate(row.asOf)}` : row ? "as filed" : "not tagged by this filer"}
                  </div>
                </div>
              );
            })}
          </div>

          {/* history sparklines — only when 2+ periods exist (a single point
              is a number, not a trend) */}
          {(revHist.length >= 2 || epsHist.length >= 2) && (
            <div
              className="flex flex-wrap items-end gap-x-8 gap-y-3 px-4 py-3"
              style={{ borderTop: "1px solid var(--border)" }}
            >
              {revHist.length >= 2 && (
                <div>
                  <div className="mb-1 text-[0.66rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
                    REVENUE HISTORY · {revHist.length} periods
                  </div>
                  <Spark values={revHist.map((h) => h.value)} width={160} height={36} />
                </div>
              )}
              {epsHist.length >= 2 && (
                <div>
                  <div className="mb-1 text-[0.66rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
                    EPS HISTORY · {epsHist.length} periods
                  </div>
                  <Spark values={epsHist.map((h) => h.value)} width={160} height={36} />
                </div>
              )}
              <p className="ml-auto max-w-72 text-[0.64rem] leading-snug" style={{ color: "var(--faint)" }}>
                mixed annual + quarterly period-ends as filed — a sawtooth shape is normal, not
                a data error.
              </p>
            </div>
          )}
        </>
      )}
    </section>
  );
}
