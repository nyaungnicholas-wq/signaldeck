"use client";

// VALIDATED SIGNALS (why-it's-moving wave) — the symbol's structural regime
// stack: the part of this platform that survived re-validation.
//
// EDITORIAL ORDER IS EVIDENCE, NOT DECORATION. The volatility regime leads
// because it is the replicated, statistically-significant read (76.4% at top
// conviction, +20.7pp over the majority-class null, CI entirely positive,
// stable across 4 of 5 market eras). The trend regimes come last with their
// tradeability line, because they are ACCURATE BUT NOT TRADEABLE: the
// highest-conviction trend21 band hits 96% with a MEASURED NEGATIVE mean
// forward return. Accuracy and return are separate axes and this panel refuses
// to let the first imply the second.
//
// Sources, fetched in parallel and settled independently (useLive.ts pattern):
//   /api/signal-report (per symbol) — the stack, and the only place the
//                                     optional `tradeability` string appears;
//   /api/regimes                    — per-kind `what` / accuracyTiers /
//                                     caveat, rendered verbatim;
//   /api/vol-regime                 — the QUARTERLY vol forecast (vol63).
// One failing source never blanks the panel.

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  pollMs,
  POLL_SLOW,
  signalReport,
  structuralRegimes,
  volRegime,
  type Market,
  type StructRegimeForecast,
  type StructRegimeKindDoc,
  type VolRegimeForecast,
} from "@/lib/api";
import HelpTip from "@/components/HelpTip";

/** Reading order: the validated edge first, the un-tradeable trends last. */
const KIND_ORDER = [
  "vol21",
  "vol63",
  "liquidity21",
  "liquidity21-crypto",
  "trend21",
  "trend21-crypto",
  "trend63",
];

const KIND_LABEL: Record<string, string> = {
  vol21: "VOLATILITY REGIME · 21d",
  vol63: "VOLATILITY REGIME · quarterly",
  liquidity21: "LIQUIDITY REGIME · 21d",
  "liquidity21-crypto": "LIQUIDITY REGIME · 21d (crypto-measured)",
  trend21: "TREND REGIME · 21d",
  "trend21-crypto": "TREND REGIME · 21d (crypto-measured)",
  trend63: "TREND REGIME · quarterly",
};

/** The report page only accepts these kinds; crypto variants map to the base. */
function reportKind(kind: string): string {
  const base = kind.replace(/-crypto$/, "");
  return ["trend21", "liquidity21", "vol21", "vol63"].includes(base) ? base : "overview";
}

function pct(v: number | undefined, dec = 1): string {
  return v == null || !isFinite(v) ? "—" : `${(v * 100).toFixed(dec)}%`;
}

/** A regime row, normalized across the trend/liquidity/vol payload shapes. */
interface Row {
  kind: string;
  regime: string;
  conviction: number;
  historicalAccuracy: number;
  tier: string;
  n: number;
  horizonDays?: number;
  tradeability?: string;
}

function toRow(f: StructRegimeForecast): Row {
  return {
    kind: f.kind,
    regime: f.regime,
    conviction: f.conviction,
    historicalAccuracy: f.historicalAccuracy,
    tier: f.tier,
    n: f.n,
    horizonDays: f.horizonDays,
    tradeability: f.tradeability,
  };
}

function volToRow(f: VolRegimeForecast, tradeability?: string): Row {
  return {
    kind: "vol63",
    regime: f.regime,
    conviction: f.conviction,
    historicalAccuracy: f.historicalAccuracy,
    tier: f.tier,
    n: f.n,
    horizonDays: 63,
    tradeability,
  };
}

export default function ValidatedSignalsPanel({
  symbol,
  market,
}: {
  symbol: string;
  market: Market;
}) {
  // Per-symbol slices are keyed by symbol|market so a symbol switch invalidates
  // them without a bare setState in the effect body; the fleet-wide kind docs
  // are symbol-independent and need no key. Each source keeps its own state, so
  // one failure keeps the others' last-good values.
  const key = `${symbol}|${market}`;
  const [stackState, setStackState] =
    useState<{ key: string; rows: StructRegimeForecast[]; vol63: VolRegimeForecast | null } | null>(null);
  const [volState, setVolState] =
    useState<{ key: string; vol63: VolRegimeForecast | null } | null>(null);
  const [docs, setDocs] = useState<Record<string, StructRegimeKindDoc>>({});
  const [volCaveat, setVolCaveat] = useState<string>("");
  const [volTradeability, setVolTradeability] = useState<string | undefined>(undefined);
  const [errState, setErrState] = useState<{ key: string; msg: string | null } | null>(null);
  const stack = stackState && stackState.key === key ? stackState.rows : null;
  // Prefer the per-symbol report's vol63; fall back to the fleet-wide row.
  const vol63 =
    (stackState && stackState.key === key ? stackState.vol63 : null) ??
    (volState && volState.key === key ? volState.vol63 : null);
  const err = errState && errState.key === key ? errState.msg : null;
  const loading = errState?.key !== key;

  useEffect(() => {
    let alive = true;
    const k = `${symbol}|${market}`;
    const load = async () => {
      const [rep, regs, vol] = await Promise.allSettled([
        signalReport(symbol, market, "overview"),
        structuralRegimes(),
        volRegime(),
      ]);
      if (!alive) return;
      let firstErr: string | null = null;
      const note = (r: PromiseSettledResult<unknown>) => {
        if (r.status === "rejected" && firstErr === null) {
          firstErr = r.reason instanceof Error ? r.reason.message : String(r.reason);
        }
      };
      if (rep.status === "fulfilled") {
        // Go nil slices arrive as JSON null — normalize before any .map.
        setStackState({
          key: k,
          rows: rep.value.regimeStack ?? [],
          vol63: rep.value.vol63 ?? null,
        });
      } else note(rep);
      if (regs.status === "fulfilled") setDocs(regs.value.kinds ?? {});
      else note(regs);
      if (vol.status === "fulfilled") {
        setVolCaveat(vol.value.caveat);
        setVolTradeability(vol.value.tradeability);
        setVolState({
          key: k,
          vol63:
            (vol.value.forecasts ?? []).find(
              (f) => f.symbol === symbol && f.market === market,
            ) ?? null,
        });
      } else note(vol);
      // Written last: this is also the "first pass landed" marker for `loading`.
      setErrState({ key: k, msg: firstErr });
    };
    void load();
    // POLL_SLOW: the regime runner writes every 6h.
    const stop = pollMs(() => void load(), POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market]);

  // Merge the per-symbol stack with the quarterly vol forecast, then order by
  // EVIDENCE (vol first, trend last) rather than by whatever the API returned.
  const rows: Row[] = [
    ...(stack ?? []).filter((f) => f.symbol === symbol).map(toRow),
    ...(vol63 ? [volToRow(vol63, volTradeability)] : []),
  ].sort((a, b) => {
    const ia = KIND_ORDER.indexOf(a.kind);
    const ib = KIND_ORDER.indexOf(b.kind);
    return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib) || a.kind.localeCompare(b.kind);
  });

  return (
    <section className="panel" aria-label={`validated regime signals for ${symbol}`}>
      <div className="panel-h flex-wrap gap-2">
        <span>VALIDATED SIGNALS · {symbol}</span>
        <HelpTip label="why these are the validated ones">
          These are structure calls — which side of a moving average price sits
          on, whether volume and volatility stay in their current regime — and
          each reports the MEASURED walk-forward accuracy of its own conviction
          band. The volatility regime is the replicated, significant one and
          leads on purpose. None of them is a price-direction call; the
          experimental P(up) read is kept in its own section below precisely
          because its live skill measured negative.
        </HelpTip>
        <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
          measured walk-forward · per-band accuracy
        </span>
      </div>

      {err !== null && rows.length === 0 && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          {err}
        </p>
      )}
      {loading && rows.length === 0 && err === null && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading the validated regime stack…
        </p>
      )}
      {!loading && rows.length === 0 && err === null && (
        <p className="px-4 py-4 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          no regime forecasts stored for {symbol} yet — the regime runner needs
          enough clean daily history, and refuses a series it cannot trust rather
          than guessing. Honest absence, not an error.
        </p>
      )}

      {rows.map((r) => {
        const doc = docs[r.kind];
        const lead = r.kind === "vol21" || r.kind === "vol63";
        return (
          <div
            key={r.kind}
            className="border-t px-4 py-3"
            style={{
              borderColor: "var(--border)",
              // The validated edge gets the accent rail; trends deliberately don't.
              borderLeft: lead ? "2px solid var(--accent)" : undefined,
            }}
          >
            <div className="flex flex-wrap items-center gap-2">
              <span
                className="text-[0.75rem] font-bold tracking-[0.14em]"
                style={{ color: lead ? "var(--accent)" : "var(--dim)" }}
              >
                {KIND_LABEL[r.kind] ?? r.kind.toUpperCase()}
              </span>
              <span className="chip" style={{ color: "var(--text)" }}>
                {r.regime}
              </span>
              <span className="chip tnum">conviction {pct(r.conviction)}</span>
              <span className="chip tnum" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                banded accuracy {pct(r.historicalAccuracy)}
              </span>
              <span className="chip">{r.tier}</span>
              <span className="tnum chip">n={r.n.toLocaleString("en-US")}</span>
              <Link
                href={`/signals/report/${market}/${encodeURIComponent(symbol)}?kind=${reportKind(r.kind)}`}
                className="chip ml-auto inline-flex min-h-[36px] cursor-pointer items-center px-3 text-[0.75rem] transition-colors duration-150 hover:border-[var(--accent)]"
                style={{ color: "var(--accent)" }}
              >
                full report →
              </Link>
            </div>

            {/* TRADEABILITY — optional daemon field; when present it is the most
                important line in the row, because accuracy is not return. */}
            {r.tradeability && (
              <p
                className="mt-2 border-l-2 pl-2 text-[0.75rem] leading-relaxed"
                style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
              >
                {r.tradeability}
              </p>
            )}

            {doc?.what && (
              <p className="mt-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                {doc.what}
              </p>
            )}
            <div className="mt-1 flex flex-wrap items-center gap-2">
              {doc?.caveat && (
                <HelpTip label={`what the ${r.kind} accuracy does and does not mean`}>
                  {doc.caveat}
                </HelpTip>
              )}
              {r.kind === "vol63" && volCaveat && (
                <HelpTip label="what the quarterly vol regime does and does not mean">
                  {volCaveat}
                </HelpTip>
              )}
              {doc?.accuracyTiers &&
                Object.entries(doc.accuracyTiers).map(([tier, acc]) => (
                  <span key={tier} className="chip tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {tier}: {acc}
                  </span>
                ))}
            </div>
          </div>
        );
      })}
    </section>
  );
}
