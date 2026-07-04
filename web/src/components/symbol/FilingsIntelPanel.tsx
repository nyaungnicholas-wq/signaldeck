"use client";

// Signal8 wave, Stage 1 — per-symbol SEC intelligence panel: recent filings
// (plain-English), insider activity (honest P/S vs mechanics), institutional
// holders (13F, quarterly + ≤45d lag), and the dilution badge. Stocks only —
// crypto has no SEC filings. All data is public-domain EDGAR, and every
// sub-section states its legal lag.

import { useEffect, useState } from "react";
import {
  dilution,
  filings,
  insiders,
  institutionsBySymbol,
  type DilutionFlag,
  type Filing,
  type InsiderTrade,
  type InstHolding,
} from "@/lib/api";
import { ago } from "@/lib/format";

const POLL_MS = 120_000;

function levelColor(level: string): string {
  switch (level) {
    case "high":
      return "var(--bad)";
    case "elevated":
      return "var(--warn)";
    case "low":
      return "var(--ok)";
    default:
      return "var(--dim)"; // unknown — never derived yet
  }
}

function fmtUSD(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  if (Math.abs(v) >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (Math.abs(v) >= 1e6) return `$${(v / 1e6).toFixed(2)}M`;
  if (Math.abs(v) >= 1e3) return `$${(v / 1e3).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

export default function FilingsIntelPanel({ symbol }: { symbol: string }) {
  const [rows, setRows] = useState<Filing[] | null>(null);
  const [trades, setTrades] = useState<InsiderTrade[] | null>(null);
  const [holders, setHolders] = useState<InstHolding[] | null>(null);
  const [flag, setFlag] = useState<DilutionFlag | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      Promise.all([
        filings(symbol, undefined, 8),
        insiders(symbol, undefined, 8),
        institutionsBySymbol(symbol, 8),
        dilution(symbol),
      ])
        .then(([f, i, h, d]) => {
          if (!alive) return;
          setRows(f.filings ?? []);
          setTrades(i.trades ?? []);
          setHolders(h.holdings ?? []);
          setFlag(d);
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

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        SEC INTELLIGENCE · {symbol}
        {flag && (
          <span
            className="chip"
            style={{ color: levelColor(flag.level), borderColor: levelColor(flag.level) }}
            title={flag.note}
          >
            dilution {flag.level}
          </span>
        )}
        <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
          EDGAR · lags by law (Form 4 ~2bd, 13F ≤45d)
        </span>
      </div>

      {err !== null && rows === null && (
        <p className="px-4 py-3 text-[0.78rem]" style={{ color: "var(--bad)" }}>
          {err}
        </p>
      )}
      {rows === null && err === null && (
        <p className="px-4 py-3 text-[0.78rem]" style={{ color: "var(--faint)" }}>
          loading SEC data…
        </p>
      )}

      {rows !== null && (
        <div className="grid grid-cols-1 gap-0 lg:grid-cols-3">
          {/* Recent filings */}
          <div className="px-4 py-3" style={{ borderTop: "1px solid var(--border)" }}>
            <div className="mb-2 text-[0.72rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
              RECENT FILINGS
            </div>
            {rows.length === 0 ? (
              <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                none stored yet — the poller sweeps ~2h.
              </p>
            ) : (
              <ul className="flex flex-col gap-1.5">
                {rows.map((f) => (
                  <li key={f.id} className="flex items-baseline gap-2 text-[0.75rem]">
                    <span className="chip shrink-0">{f.form}</span>
                    <span className="min-w-0 flex-1 truncate" style={{ color: "var(--dim)" }} title={f.label}>
                      {f.label}
                    </span>
                    <span className="tnum shrink-0 text-[0.68rem]" style={{ color: "var(--faint)" }}>
                      {ago(f.filedTs)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {/* Insider activity */}
          <div className="px-4 py-3" style={{ borderTop: "1px solid var(--border)" }}>
            <div className="mb-2 text-[0.72rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
              INSIDER ACTIVITY
            </div>
            {(trades ?? []).length === 0 ? (
              <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                no parsed Form 4s yet.
              </p>
            ) : (
              <ul className="flex flex-col gap-1.5">
                {(trades ?? []).map((t) => (
                  <li key={t.accession} className="flex items-baseline gap-2 text-[0.75rem]">
                    <span
                      className="chip shrink-0"
                      style={{
                        color: t.code === "P" ? "var(--bid)" : t.code === "S" ? "var(--ask)" : "var(--dim)",
                        borderColor: t.code === "P" ? "var(--bid)" : t.code === "S" ? "var(--ask)" : "var(--border)",
                      }}
                      title={t.codeLabel}
                    >
                      {t.openMarket ? (t.code === "P" ? "BUY" : "SELL") : t.code}
                    </span>
                    <span className="min-w-0 flex-1 truncate" style={{ color: "var(--dim)" }} title={`${t.insider} ${t.title}`}>
                      {t.insider}
                    </span>
                    <span className="tnum shrink-0" style={{ color: "var(--text)" }}>
                      {fmtUSD(t.value)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            <p className="mt-2 text-[0.66rem] leading-snug" style={{ color: "var(--faint)" }}>
              only BUY/SELL are open-market; other codes are grants/exercises.
            </p>
          </div>

          {/* Institutional holders */}
          <div className="px-4 py-3" style={{ borderTop: "1px solid var(--border)" }}>
            <div className="mb-2 text-[0.72rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
              NOTABLE 13F HOLDERS
            </div>
            {(holders ?? []).length === 0 ? (
              <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                none of the ~25 tracked managers reported a position (or 13Fs not swept yet).
              </p>
            ) : (
              <ul className="flex flex-col gap-1.5">
                {(holders ?? []).map((h) => (
                  <li key={`${h.cik}-${h.cusip}`} className="flex items-baseline gap-2 text-[0.75rem]">
                    <span className="min-w-0 flex-1 truncate" style={{ color: "var(--dim)" }}>
                      {h.manager}
                    </span>
                    <span className="tnum shrink-0" style={{ color: "var(--text)" }}>
                      {fmtUSD(h.value)}
                    </span>
                    <span className="tnum shrink-0 text-[0.66rem]" style={{ color: "var(--faint)" }}>
                      {h.period}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* dilution evidence, when any */}
      {flag && flag.reasons.length > 0 && (
        <div className="px-4 py-3" style={{ borderTop: "1px solid var(--border)" }}>
          <div className="mb-1 text-[0.72rem] tracking-[0.12em]" style={{ color: levelColor(flag.level) }}>
            DILUTION EVIDENCE
          </div>
          <ul className="flex flex-col gap-1">
            {flag.reasons.map((r) => (
              <li key={r} className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
                · {r}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-[0.66rem]" style={{ color: "var(--faint)" }}>
            descriptive evidence, not a prediction.
          </p>
        </div>
      )}
    </section>
  );
}
