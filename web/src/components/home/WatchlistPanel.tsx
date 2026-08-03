"use client";

// YOUR WATCHLIST panel — extracted from the old monolithic page.tsx (pure
// refactor; behavior identical). Sparkline + day% + the honest 1d VerdictCard
// per row, plus add/unwatch. The .dash-input styles moved here with the form.

import Link from "next/link";
import { useState } from "react";
import { api, type DashboardResponse, type DashWatchSpark, type Market } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import VerdictCard from "@/components/VerdictCard";
import Sparkline from "@/components/viz/Sparkline";
import EmptyState from "@/components/EmptyState";
import { changeColor } from "@/components/home/helpers";

function WatchRowItem({
  r,
  onUnwatch,
}: {
  r: DashWatchSpark;
  onUnwatch: (r: DashWatchSpark) => void;
}) {
  return (
    <li
      className="flex flex-wrap items-center gap-x-2 gap-y-1 border-t px-3 py-2"
      style={{ borderColor: "var(--border)" }}
    >
      <Link
        href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
        className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 transition-colors duration-150 hover:text-[var(--accent)]"
        aria-label={`open ${r.symbol} (${r.market})`}
      >
        <span className="mono w-14 shrink-0 truncate text-[0.75rem] font-bold tracking-wide">
          {r.symbol}
        </span>
        <Sparkline closes={r.closes ?? []} width={88} height={26} />
        <span className="tnum ml-auto shrink-0 text-[0.75rem]" style={{ color: changeColor(r.dayChangePct) }}>
          {(r.closes?.length ?? 0) > 1 ? fmtPct(r.dayChangePct) : "—"}
        </span>
      </Link>
      {/* 2026-07-18 technical readouts — server-derived from the same
          sparkline closes; "—" = window too short (honest absence). */}
      <span
        className="tnum basis-full text-[0.7rem]"
        style={{ color: "var(--faint)" }}
        title="RSI(14) · distance to SMA20 · 20-day realized vol (annualized) — from stored daily closes"
      >
        RSI {r.rsi14 != null ? r.rsi14.toFixed(0) : "—"}
        {" · "}SMA20 {r.sma20DistPct != null ? fmtPct(r.sma20DistPct) : "—"}
        {" · "}σ20 {r.vol20AnnPct != null ? `${r.vol20AnnPct.toFixed(0)}%` : "—"}
      </span>
      {/* the 1d verdict card — REAL calibrated prob or the honest
          "NO READ YET", with the evidence-tier badge always visible. */}
      <VerdictCard
        size="sm"
        symbol={r.symbol}
        market={r.market}
        calProb={r.calProb1d ?? null}
        nUsed={r.nUsed1d ?? 0}
        tier={r.tier1d ?? ""}
        tierProgress={{ nSamples: r.nSamples1d ?? 0, threshold: r.tierThreshold ?? 0 }}
        className="shrink-0"
      />
      <button
        type="button"
        onClick={() => onUnwatch(r)}
        aria-label={`stop watching ${r.symbol}`}
        className="inline-flex min-h-[40px] min-w-[40px] shrink-0 cursor-pointer items-center justify-center text-[0.75rem] text-[var(--faint)] transition-colors duration-150 hover:text-[var(--bad)]"
      >
        ×
      </button>
    </li>
  );
}

export default function WatchlistPanel({
  wl,
  onChanged,
}: {
  wl: NonNullable<DashboardResponse["watchlist"]>;
  onChanged: () => void;
}) {
  const [sym, setSym] = useState("");
  const [mkt, setMkt] = useState<Market>("stocks");
  const [adding, setAdding] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  async function onAdd(e: React.FormEvent) {
    e.preventDefault();
    const s = sym.trim().toUpperCase();
    if (!s || adding) return;
    setAdding(true);
    setActionError(null);
    try {
      await api.subscribe(s, mkt);
      setSym("");
      onChanged();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setAdding(false);
    }
  }

  function onUnwatch(r: DashWatchSpark) {
    if (!window.confirm(`Stop watching ${r.symbol} (${r.market})?`)) return;
    setActionError(null);
    api
      .unsubscribe(r.symbol, r.market)
      .then(onChanged)
      .catch((err) => setActionError(err instanceof Error ? err.message : String(err)));
  }

  const sparks = wl.sparks ?? [];
  return (
    <section className="panel" aria-label="watchlist">
      <style>{`
        .dash-input {
          background: var(--panel2);
          border: 1px solid var(--border);
          border-radius: 8px;
          padding: 6px 10px;
          min-height: 40px;
          font-size: .75rem;
          color: var(--text);
          font-family: inherit;
        }
        .dash-input::placeholder { color: var(--faint); }
        .dash-add { color: var(--dim); }
        .dash-add:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
        .dash-add:disabled { color: var(--faint); cursor: default; }
      `}</style>
      <div className="panel-h">
        <span>WATCHLIST</span>
        <span className="chip tnum ml-auto px-2 py-[1px] text-[0.75rem]">{sparks.length} tracked</span>
      </div>

      {sparks.length === 0 ? (
        <EmptyState
          message="Nothing on your watchlist yet"
          detail="Add a symbol and we start recording its data, scoring it, and telling you when something changes. Try AAPL, or BTC/USD for crypto. History and scores fill in on the daemon's own cadence."
          action={{ label: "Add your first symbol →", href: "/welcome" }}
        />
      ) : (
        <ul className="m-0 list-none p-0">
          {sparks.map((r) => (
            <WatchRowItem key={`${r.market}:${r.symbol}`} r={r} onUnwatch={onUnwatch} />
          ))}
        </ul>
      )}

      <form
        className="flex flex-wrap items-center gap-2 border-t px-3 py-2"
        style={{ borderColor: "var(--border)" }}
        onSubmit={onAdd}
      >
        <label htmlFor="wl-add-sym" className="sr-only">
          symbol to add
        </label>
        <input
          id="wl-add-sym"
          className="dash-input tnum w-24 min-w-0 flex-1 uppercase"
          value={sym}
          onChange={(e) => {
            setSym(e.target.value);
            if (actionError) setActionError(null);
          }}
          placeholder={mkt === "crypto" ? "BTC/USD" : "AAPL"}
          spellCheck={false}
          autoComplete="off"
        />
        <select
          className="dash-input cursor-pointer"
          value={mkt}
          onChange={(e) => setMkt(e.target.value as Market)}
          aria-label="market"
        >
          <option value="stocks">stocks</option>
          <option value="crypto">crypto</option>
        </select>
        <button
          type="submit"
          disabled={adding || sym.trim() === ""}
          className="dash-input dash-add cursor-pointer transition-colors duration-150"
        >
          {adding ? "adding…" : "Monitor"}
        </button>
      </form>
      {actionError && (
        <p role="alert" className="px-3 pb-2 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          {actionError}
        </p>
      )}
      <p
        className="border-t px-3 py-2 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: "var(--faint)" }}
      >
        {wl.note}
      </p>
    </section>
  );
}
