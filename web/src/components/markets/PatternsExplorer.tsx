"use client";

// PATTERNS EXPLORER — the 2026-07-18 TRENDS-tab upgrade: "what kind of
// candlestick patterns are firing?" Pick a symbol → every pattern detected on
// its recent daily bars, each annotated with the MEASURED edge on that
// symbol's own history (or an honest null). The API's weak/context-only
// caveat renders verbatim — patterns are descriptive, never advice.

import { useEffect, useState } from "react";
import {
  candlePatterns,
  type CandlePatterns,
  type Market,
} from "@/lib/api";
import { fmtDate } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

const QUICK = ["SPY", "QQQ", "NVDA", "AAPL", "TSLA", "AMD"];

function biasColor(b: -1 | 0 | 1): string {
  if (b > 0) return "var(--bid)";
  if (b < 0) return "var(--ask)";
  return "var(--dim)";
}

export default function PatternsExplorer() {
  const [symbol, setSymbol] = useState("SPY");
  const [input, setInput] = useState("");
  const [data, setData] = useState<CandlePatterns | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let dead = false;
    setData(null);
    setErr(null);
    candlePatterns(symbol, "stocks" as Market, "1d")
      .then((d) => !dead && setData(d))
      .catch((e: unknown) => !dead && setErr(e instanceof Error ? e.message : String(e)));
    return () => {
      dead = true;
    };
  }, [symbol]);

  const rows = (data?.bars ?? [])
    .flatMap((b) => b.patterns.map((p) => ({ ts: b.ts, ...p })))
    .reverse()
    .slice(0, 25);

  return (
    <section className="panel" aria-label="candlestick patterns">
      <div className="panel-h flex-wrap gap-2">
        <span>CANDLESTICK PATTERNS</span>
        <span className="font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
          recent daily-bar patterns + each one&apos;s measured record on this symbol
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 px-3 py-2">
        {QUICK.map((s) => (
          <button
            key={s}
            type="button"
            aria-pressed={symbol === s}
            onClick={() => setSymbol(s)}
            className="chip mono min-h-[36px] cursor-pointer px-2 text-[0.75rem] transition-colors duration-150 hover:text-[var(--text)]"
            style={symbol === s ? { color: "var(--text)", borderColor: "var(--accent)" } : undefined}
          >
            {s}
          </button>
        ))}
        <form
          className="flex items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault();
            if (input.trim()) setSymbol(input.trim().toUpperCase());
          }}
        >
          <label htmlFor="pattern-symbol" className="sr-only">
            symbol
          </label>
          <input
            id="pattern-symbol"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="other…"
            className="chip mono w-24 bg-transparent px-2 py-1 text-[0.75rem] outline-none focus:border-[var(--accent)]"
          />
        </form>
      </div>

      {!data && !err ? (
        <div className="p-3">
          <Skeleton lines={5} label={`scanning ${symbol}`} />
        </div>
      ) : null}
      {err ? (
        <div className="p-3">
          <ErrorState message={err} />
        </div>
      ) : null}

      {data && rows.length === 0 ? (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          no patterns detected on {symbol}&apos;s recent daily bars — most bars have no
          named shape, which is the honest state.
        </p>
      ) : null}

      {rows.length > 0 ? (
        <div className="table-wrap">
          <table className="w-full text-[0.75rem]">
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border)" }}>
                {["DATE", "PATTERN", "BIAS", "MEASURED ON THIS SYMBOL"].map((h, i) => (
                  <th
                    key={h}
                    className={`px-3 py-1.5 font-medium tracking-wide ${i < 2 ? "text-left" : i === 2 ? "text-center" : "text-left"}`}
                    style={{ color: "var(--faint)" }}
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={`${r.ts}-${r.name}-${i}`} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td className="tnum px-3 py-1.5" style={{ color: "var(--faint)" }}>
                    {fmtDate(r.ts)}
                  </td>
                  <td className="px-3 py-1.5">
                    <span className="font-medium">{r.name}</span>{" "}
                    <span style={{ color: "var(--faint)" }}>{r.desc}</span>
                  </td>
                  <td className="px-3 py-1.5 text-center" style={{ color: biasColor(r.bias) }}>
                    {r.bias > 0 ? "bullish" : r.bias < 0 ? "bearish" : "neutral"}
                  </td>
                  <td className="tnum px-3 py-1.5" style={{ color: "var(--dim)" }}>
                    {r.measured
                      ? `hit ${(r.measured.hitRate * 100).toFixed(0)}% · mean fwd ${(r.measured.meanFwd * 100).toFixed(2)}% · n=${r.measured.n}`
                      : "not enough history to measure — no claim"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {data?.note ? (
        <p className="border-t px-4 py-2 text-[0.7rem] leading-relaxed" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
          {data.note}
        </p>
      ) : null}
    </section>
  );
}
