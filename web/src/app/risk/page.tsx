"use client";

import { useEffect, useMemo, useState } from "react";
import {
  api,
  type Market,
  type RiskHolding,
  type RiskReport,
  type WatchRow,
} from "@/lib/api";
import { fmtPct } from "@/lib/format";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

// A holding row as edited in the builder. weight is a raw percent (need not
// sum to 100 — the backend normalizes). A stable id keeps React keys sane
// across add/remove without leaning on array index.
interface Row {
  id: number;
  symbol: string;
  market: Market;
  weight: string; // kept as string so the input can be empty mid-edit
}

const DEFAULT_NOTIONAL = 100000;

let ROW_SEQ = 1;
const newRow = (symbol = "", market: Market = "crypto", weight = ""): Row => ({
  id: ROW_SEQ++,
  symbol,
  market,
  weight,
});

// $ amount from a loss percent, always shown as the dollar magnitude lost.
function lossUSD(pct: number, notional: number): number {
  return (Math.abs(pct) / 100) * notional;
}

function fmtUSD(v: number): string {
  if (!isFinite(v)) return "—";
  return v.toLocaleString("en-US", { maximumFractionDigits: 0 });
}

/** One VaR figure: percent + dollar translation, framed as a loss. */
function VaRCell({
  label,
  labelTitle,
  pct,
  notional,
  hint,
  faded = false,
}: {
  label: string;
  labelTitle?: string;
  pct: number;
  notional: number;
  hint: string;
  faded?: boolean;
}) {
  const has = isFinite(pct);
  return (
    <div
      className="flex flex-col gap-1 px-4 py-4"
      style={{ borderRight: "1px solid var(--border)" }}
    >
      <div className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }} title={labelTitle}>
        {label}
      </div>
      <div
        className="tnum text-[1.5rem] font-bold leading-none"
        style={{ color: has ? "var(--bad)" : "var(--faint)", opacity: faded ? 0.75 : 1 }}
      >
        {has ? `−${Math.abs(pct).toFixed(2)}%` : "—"}
      </div>
      <div className="tnum text-[0.78rem]" style={{ color: "var(--dim)" }}>
        {has ? `−$${fmtUSD(lossUSD(pct, notional))}` : "—"}
      </div>
      <div className="mt-1 text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
        {hint}
      </div>
    </div>
  );
}

export default function RiskPage() {
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [watchErr, setWatchErr] = useState<string | null>(null);

  const [rows, setRows] = useState<Row[]>([newRow()]);
  const [notional, setNotional] = useState<number>(DEFAULT_NOTIONAL);

  const [report, setReport] = useState<RiskReport | null>(null);
  const [summary, setSummary] = useState<string>("");
  const [running, setRunning] = useState(false);
  const [runErr, setRunErr] = useState<string | null>(null);
  // notional the last successful run was priced at — VaR $ must reflect the
  // run, not a value the user edited afterward.
  const [ranNotional, setRanNotional] = useState<number>(DEFAULT_NOTIONAL);

  // Watchlist powers the chip-picker and the equal-weight shortcut. It is not
  // polled — this is a request-driven page — but we fetch once on mount.
  useEffect(() => {
    let alive = true;
    api
      .watchlist()
      .then((w) => {
        if (!alive) return;
        setWatch(w);
        setWatchErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setWatchErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, []);

  const picker = useMemo(() => watch ?? [], [watch]);

  const totalWeight = useMemo(
    () =>
      rows.reduce((s, r) => {
        const w = Number(r.weight);
        return s + (isFinite(w) && w > 0 ? w : 0);
      }, 0),
    [rows]
  );

  // Rows the daemon can actually price: a symbol and a positive weight.
  const validHoldings = useMemo<RiskHolding[]>(
    () =>
      rows
        .map((r) => ({ symbol: r.symbol.trim(), market: r.market, weight: Number(r.weight) }))
        .filter((h) => h.symbol.length > 0 && isFinite(h.weight) && h.weight > 0),
    [rows]
  );

  const setRow = (id: number, patch: Partial<Row>) =>
    setRows((rs) => rs.map((r) => (r.id === id ? { ...r, ...patch } : r)));

  const addRow = () => setRows((rs) => [...rs, newRow()]);
  const removeRow = (id: number) =>
    setRows((rs) => (rs.length <= 1 ? [newRow()] : rs.filter((r) => r.id !== id)));

  const equalWeightWatchlist = () => {
    if (picker.length === 0) return;
    const w = (100 / picker.length).toFixed(2);
    ROW_SEQ = 1;
    setRows(picker.map((p) => newRow(p.symbol, p.market, w)));
    setReport(null);
    setSummary("");
    setRunErr(null);
  };

  const run = () => {
    if (validHoldings.length === 0 || running) return;
    setRunning(true);
    setRunErr(null);
    const atNotional = notional > 0 ? notional : DEFAULT_NOTIONAL;
    api
      .risk(validHoldings, atNotional)
      .then((res) => {
        setReport(res.report);
        setSummary(res.summary);
        setRanNotional(res.report.NotionalUSD > 0 ? res.report.NotionalUSD : atNotional);
        setRunErr(null);
      })
      .catch((e: unknown) => {
        setReport(null);
        setSummary("");
        setRunErr(e instanceof Error ? e.message : String(e));
      })
      .finally(() => setRunning(false));
  };

  // Drivers sorted by share of portfolio risk, descending.
  const drivers = useMemo(() => {
    const cs = report?.Contributions ?? [];
    return [...cs].sort((a, b) => b.PctOfRisk - a.PctOfRisk);
  }, [report]);
  const maxPct = useMemo(
    () => drivers.reduce((m, c) => Math.max(m, c.PctOfRisk), 0),
    [drivers]
  );

  const canRun = validHoldings.length > 0 && !running;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">RISK</h1>
        <span className="chip">RiskLens</span>
        <span className="chip tnum">
          {validHoldings.length} holding{validHoldings.length === 1 ? "" : "s"}
        </span>
        <span
          className="chip tnum"
          style={{ color: totalWeight > 0 ? "var(--dim)" : "var(--faint)" }}
        >
          weights total {totalWeight.toFixed(0)}%
          {totalWeight > 0 && totalWeight !== 100 ? " · normalized" : ""}
        </span>
        {report !== null && (
          <span className="chip tnum">
            {(report.Confidence * 100).toFixed(0)}% confidence · 1-day
          </span>
        )}
      </div>

      {/* builder */}
      <section className="panel">
        <div className="panel-h">
          PORTFOLIO
          <span className="tnum" style={{ color: "var(--faint)" }}>
            weights need not sum to 100 — the model normalizes them
          </span>
        </div>

        <div className="flex flex-col gap-3 px-4 py-4">
          {/* holding rows */}
          <div className="flex flex-col gap-2">
            {rows.map((r, i) => (
              <div key={r.id} className="flex flex-wrap items-center gap-2">
                <input
                  type="text"
                  value={r.symbol}
                  onChange={(e) => setRow(r.id, { symbol: e.target.value.toUpperCase() })}
                  placeholder="symbol"
                  aria-label={`holding ${i + 1} symbol`}
                  list="risk-symbols"
                  spellCheck={false}
                  autoComplete="off"
                  className="w-32 rounded border px-2.5 py-1.5 text-[0.78rem] uppercase tnum"
                  style={{
                    background: "var(--panel2)",
                    borderColor: "var(--border)",
                    color: "var(--text)",
                  }}
                />
                <select
                  value={r.market}
                  onChange={(e) => setRow(r.id, { market: e.target.value as Market })}
                  aria-label={`holding ${i + 1} market`}
                  className="cursor-pointer rounded border px-2 py-1.5 text-[0.78rem]"
                  style={{
                    background: "var(--panel2)",
                    borderColor: "var(--border)",
                    color: "var(--text)",
                  }}
                >
                  <option value="crypto">crypto</option>
                  <option value="stocks">stocks</option>
                </select>
                <div className="flex items-center gap-1.5">
                  <input
                    type="number"
                    inputMode="decimal"
                    min={0}
                    step="any"
                    value={r.weight}
                    onChange={(e) => setRow(r.id, { weight: e.target.value })}
                    placeholder="weight"
                    aria-label={`holding ${i + 1} weight percent`}
                    className="w-24 rounded border px-2.5 py-1.5 text-right text-[0.78rem] tnum"
                    style={{
                      background: "var(--panel2)",
                      borderColor: "var(--border)",
                      color: "var(--text)",
                    }}
                  />
                  <span className="text-[0.78rem]" style={{ color: "var(--faint)" }}>
                    %
                  </span>
                </div>
                <button
                  type="button"
                  onClick={() => removeRow(r.id)}
                  aria-label={`remove holding ${i + 1}`}
                  className="cursor-pointer rounded border px-2 py-1.5 text-[0.78rem] transition-colors duration-150 hover:brightness-125"
                  style={{
                    background: "var(--panel2)",
                    borderColor: "var(--border)",
                    color: "var(--dim)",
                  }}
                >
                  remove
                </button>
              </div>
            ))}
            {/* one shared datalist for all symbol inputs */}
            <datalist id="risk-symbols">
              {picker.map((p) => (
                <option key={`${p.market}:${p.symbol}`} value={p.symbol}>
                  {p.market}
                </option>
              ))}
            </datalist>
          </div>

          {/* quick-pick chips from the watchlist */}
          {picker.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                add from watchlist
              </span>
              {picker.map((p) => (
                <button
                  key={`pick:${p.market}:${p.symbol}`}
                  type="button"
                  onClick={() =>
                    setRows((rs) => {
                      const blank = rs.find((r) => r.symbol.trim() === "");
                      if (blank)
                        return rs.map((r) =>
                          r.id === blank.id
                            ? { ...r, symbol: p.symbol, market: p.market }
                            : r
                        );
                      return [...rs, newRow(p.symbol, p.market, "")];
                    })
                  }
                  className="chip cursor-pointer transition-colors duration-150 hover:brightness-125"
                  style={{ color: "var(--dim)" }}
                  title={`add ${p.symbol} (${p.market})`}
                >
                  {p.symbol}
                </button>
              ))}
            </div>
          )}
          {watchErr !== null && watch === null && (
            <div className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              watchlist unavailable — type symbols manually.
            </div>
          )}

          {/* controls */}
          <div
            className="flex flex-wrap items-center gap-x-5 gap-y-3 pt-2"
            style={{ borderTop: "1px solid var(--border)" }}
          >
            <button
              type="button"
              onClick={addRow}
              className="min-h-[40px] cursor-pointer rounded border px-3 py-1.5 text-[0.78rem] transition-colors duration-150 hover:brightness-125"
              style={{
                background: "var(--panel2)",
                borderColor: "var(--border)",
                color: "var(--dim)",
              }}
            >
              + add holding
            </button>
            <button
              type="button"
              onClick={equalWeightWatchlist}
              disabled={picker.length === 0}
              className="min-h-[40px] cursor-pointer rounded border px-3 py-1.5 text-[0.78rem] transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
              style={{
                background: "var(--panel2)",
                borderColor: "var(--border)",
                color: "var(--dim)",
              }}
              title="replace with an equal-weight basket of the whole watchlist"
            >
              equal-weight my watchlist
            </button>

            <label className="flex items-center gap-2 text-[0.78rem]">
              <span style={{ color: "var(--faint)" }}>notional $</span>
              <input
                type="number"
                inputMode="numeric"
                min={0}
                step={1000}
                value={notional}
                onChange={(e) => setNotional(Number(e.target.value))}
                aria-label="portfolio notional in US dollars"
                className="w-32 rounded border px-2.5 py-1.5 text-right text-[0.78rem] tnum"
                style={{
                  background: "var(--panel2)",
                  borderColor: "var(--border)",
                  color: "var(--text)",
                }}
              />
            </label>

            <button
              type="button"
              onClick={run}
              disabled={!canRun}
              className="min-h-[40px] cursor-pointer rounded border px-4 py-1.5 text-[0.78rem] font-bold tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
              style={{
                background: "rgba(251,191,36,.10)",
                borderColor: "var(--accent)",
                color: "var(--accent)",
              }}
            >
              {running ? "running…" : "run risk"}
            </button>
          </div>
        </div>
      </section>

      {/* run error */}
      {runErr !== null && (
        <ErrorState
          message={runErr}
          hint="is the daemon running? start it with signaldeckd, then retry."
          retry={run}
        />
      )}

      {/* initial / empty state */}
      {report === null && runErr === null && (
        <section className="panel">
          <div className="px-4 py-10 text-center text-[0.78rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {validHoldings.length === 0 ? (
              <>
                add at least one holding — a symbol and a weight — then{" "}
                <span style={{ color: "var(--dim)" }}>run risk</span> to compute 1-day
                Value-at-Risk, the drivers behind it, and a set of stress scenarios.
              </>
            ) : (
              <>
                {validHoldings.length} holding{validHoldings.length === 1 ? "" : "s"} ready —
                press <span style={{ color: "var(--dim)" }}>run risk</span> to price 1-day VaR,
                risk drivers, and stress scenarios against{" "}
                <span className="tnum">${fmtUSD(notional > 0 ? notional : DEFAULT_NOTIONAL)}</span>{" "}
                notional.
              </>
            )}
          </div>
        </section>
      )}

      {/* results */}
      {report !== null && (
        <>
          {/* plain-English headline */}
          {summary && (
            <section className="panel">
              <div className="px-5 py-5">
                <p
                  className="text-[1.05rem] font-medium leading-relaxed"
                  style={{ color: "var(--text)" }}
                >
                  {summary}
                </p>
                <p className="mt-2 text-[0.78rem] tnum" style={{ color: "var(--faint)" }}>
                  priced against ${fmtUSD(ranNotional)} notional ·{" "}
                  {(report.Confidence * 100).toFixed(0)}% confidence · 1-day horizon
                </p>
              </div>
            </section>
          )}

          {/* VaR table */}
          <section className="panel">
            <div className="panel-h">
              VALUE AT RISK
              <span className="tnum" style={{ color: "var(--faint)" }}>
                worst plausible 1-day loss ·{" "}
                {(report.Confidence * 100).toFixed(0)}% confidence
              </span>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3">
              <VaRCell
                label="HISTORICAL VaR (95%)"
                labelTitle="Value at risk — the worst plausible 1-day loss at 95% confidence, from actual return history"
                pct={report.HistVaRPct}
                notional={ranNotional}
                hint="1 day in 20, losses have been at least this bad — measured from actual return history."
              />
              <VaRCell
                label="HISTORICAL CVaR (95%)"
                labelTitle="Conditional value at risk — the average loss on the worst 1-in-20 days"
                pct={report.HistCVaRPct}
                notional={ranNotional}
                hint="the average loss on those worst 1-in-20 days — how deep the tail actually runs."
              />
              <VaRCell
                label="PARAMETRIC VaR (95%)"
                labelTitle="Value at risk estimated from a normal distribution of returns"
                pct={report.ParamVaRPct}
                notional={ranNotional}
                hint="the normal-curve estimate — cleaner, but blind to fat tails."
                faded
              />
            </div>
            <div
              className="px-4 py-3 text-[0.75rem] leading-relaxed"
              style={{ borderTop: "1px solid var(--border)", color: "var(--faint)" }}
            >
              <span style={{ color: "var(--warn)" }}>note:</span> parametric VaR assumes normal
              returns — real tails are fatter, so it tends to understate risk. Historical VaR is
              the more honest number.
            </div>
          </section>

          {/* risk drivers */}
          <section className="panel">
            <div className="panel-h">
              RISK DRIVERS
              <span className="tnum" style={{ color: "var(--faint)" }}>
                where the portfolio&apos;s risk actually comes from
              </span>
            </div>
            {drivers.length === 0 ? (
              <EmptyState
                className="border-0"
                message="No risk contributions returned"
                detail="The daemon may lack return history for these symbols — let bars accrue and run again."
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-[0.8rem]">
                  <thead>
                    <tr style={{ borderBottom: "1px solid var(--border)" }}>
                      <th
                        scope="col"
                        className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                      >
                        SYMBOL
                      </th>
                      <th
                        scope="col"
                        className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                      >
                        SHARE OF RISK
                      </th>
                      <th
                        scope="col"
                        className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                      >
                        WEIGHT
                      </th>
                      <th
                        scope="col"
                        className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                        title="Annualized volatility — how much this symbol swings in a typical year"
                      >
                        ANNUAL VOL
                      </th>
                    </tr>
                  </thead>
                  <tbody className="tnum">
                    {drivers.map((c) => {
                      const share = isFinite(c.PctOfRisk) ? c.PctOfRisk : 0;
                      const barPct = maxPct > 0 ? (share / maxPct) * 100 : 0;
                      return (
                        <tr
                          key={c.Symbol}
                          className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                          style={{ borderBottom: "1px solid var(--border)" }}
                        >
                          <td className="px-3 py-2 font-bold" style={{ color: "var(--text)" }}>
                            {c.Symbol}
                          </td>
                          <td className="px-3 py-2">
                            <div className="flex items-center gap-2">
                              <div
                                className="h-2 flex-1 overflow-hidden rounded-full"
                                style={{ background: "var(--panel2)", minWidth: 80 }}
                                role="meter"
                                aria-valuemin={0}
                                aria-valuemax={100}
                                aria-valuenow={Number((share * 100).toFixed(1))}
                                aria-label={`${c.Symbol} share of portfolio risk`}
                              >
                                <div
                                  className="h-full transition-[width] duration-300"
                                  style={{ width: `${barPct}%`, background: "var(--accent)" }}
                                />
                              </div>
                              <span
                                className="w-12 shrink-0 text-right"
                                style={{ color: "var(--text)" }}
                              >
                                {(share * 100).toFixed(1)}%
                              </span>
                            </div>
                          </td>
                          <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                            {(c.Weight * 100).toFixed(1)}%
                          </td>
                          <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                            {isFinite(c.Vol) ? `${(c.Vol * 100).toFixed(1)}%` : "—"}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {/* stress scenarios */}
          <section className="panel">
            <div className="panel-h">
              STRESS SCENARIOS
              <span className="tnum" style={{ color: "var(--faint)" }}>
                what this book would have done in known shocks
              </span>
            </div>
            {(report.Scenarios ?? []).length === 0 ? (
              <EmptyState
                className="border-0"
                message="No stress scenarios returned for this portfolio"
                detail="Scenario replays need enough shared history across the holdings."
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-[0.8rem]">
                  <thead>
                    <tr style={{ borderBottom: "1px solid var(--border)" }}>
                      <th
                        scope="col"
                        className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                      >
                        SCENARIO
                      </th>
                      <th
                        scope="col"
                        className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                        title="Profit and loss"
                      >
                        P&amp;L
                      </th>
                      <th
                        scope="col"
                        className="px-3 py-2 text-right text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                      >
                        ON ${fmtUSD(ranNotional)}
                      </th>
                      <th
                        scope="col"
                        className="px-3 py-2 text-left text-[0.75rem] font-medium tracking-wide"
                        style={{ color: "var(--faint)" }}
                      >
                        DETAIL
                      </th>
                    </tr>
                  </thead>
                  <tbody className="tnum">
                    {report.Scenarios.map((s, i) => {
                      const has = isFinite(s.PnLPct);
                      const color = !has
                        ? "var(--faint)"
                        : s.PnLPct > 0
                          ? "var(--bid)"
                          : s.PnLPct < 0
                            ? "var(--ask)"
                            : "var(--dim)";
                      const usd = has ? (s.PnLPct / 100) * ranNotional : NaN;
                      return (
                        <tr
                          key={`${s.Name}:${i}`}
                          className="transition-colors duration-150 hover:bg-[var(--panel2)]"
                          style={{ borderBottom: "1px solid var(--border)" }}
                        >
                          <td className="px-3 py-2 font-bold" style={{ color: "var(--text)" }}>
                            {s.Name}
                          </td>
                          <td className="px-3 py-2 text-right" style={{ color }}>
                            {has ? fmtPct(s.PnLPct) : "—"}
                          </td>
                          <td className="px-3 py-2 text-right" style={{ color }}>
                            {has
                              ? `${usd >= 0 ? "+" : "−"}$${fmtUSD(Math.abs(usd))}`
                              : "—"}
                          </td>
                          <td className="px-3 py-2 text-[0.78rem]" style={{ color: "var(--dim)" }}>
                            {s.Detail || "—"}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}
