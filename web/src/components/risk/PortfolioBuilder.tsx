"use client";

// Portfolio builder for /lab/risk — holding rows, watchlist quick-picks and
// the run controls, extracted from the old monolithic page (pure refactor;
// behavior identical). The CTA speaks Investigate language: it prices risk
// from stored history — it never prompts a trade.

import { type Market, type WatchRow } from "@/lib/api";
import { type Row } from "@/hooks/useRisk";

export default function PortfolioBuilder({
  rows,
  setRow,
  addRow,
  removeRow,
  picker,
  watch,
  watchErr,
  addFromWatchlist,
  equalWeightWatchlist,
  notional,
  setNotional,
  run,
  running,
  canRun,
}: {
  rows: Row[];
  setRow: (id: number, patch: Partial<Row>) => void;
  addRow: () => void;
  removeRow: (id: number) => void;
  picker: WatchRow[];
  watch: WatchRow[] | null;
  watchErr: string | null;
  addFromWatchlist: (p: WatchRow) => void;
  equalWeightWatchlist: () => void;
  notional: number;
  setNotional: (n: number) => void;
  run: () => void;
  running: boolean;
  canRun: boolean;
}) {
  return (
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
                className="w-32 rounded-lg border px-2.5 py-1.5 text-[0.75rem] uppercase tnum"
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
                className="cursor-pointer rounded-lg border px-2 py-1.5 text-[0.75rem]"
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
                  className="w-24 rounded-lg border px-2.5 py-1.5 text-right text-[0.75rem] tnum"
                  style={{
                    background: "var(--panel2)",
                    borderColor: "var(--border)",
                    color: "var(--text)",
                  }}
                />
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  %
                </span>
              </div>
              <button
                type="button"
                onClick={() => removeRow(r.id)}
                aria-label={`remove holding ${i + 1}`}
                className="cursor-pointer rounded-lg border px-2 py-1.5 text-[0.75rem] transition-colors duration-150 hover:brightness-125"
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
                onClick={() => addFromWatchlist(p)}
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
            className="min-h-[40px] cursor-pointer rounded-lg border px-3 py-1.5 text-[0.75rem] transition-colors duration-150 hover:brightness-125"
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
            className="min-h-[40px] cursor-pointer rounded-lg border px-3 py-1.5 text-[0.75rem] transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
            style={{
              background: "var(--panel2)",
              borderColor: "var(--border)",
              color: "var(--dim)",
            }}
            title="replace with an equal-weight basket of the whole watchlist"
          >
            equal-weight my watchlist
          </button>

          <label className="flex items-center gap-2 text-[0.75rem]">
            <span style={{ color: "var(--faint)" }}>notional $</span>
            <input
              type="number"
              inputMode="numeric"
              min={0}
              step={1000}
              value={notional}
              onChange={(e) => setNotional(Number(e.target.value))}
              aria-label="portfolio notional in US dollars"
              className="w-32 rounded-lg border px-2.5 py-1.5 text-right text-[0.75rem] tnum"
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
            className="min-h-[40px] cursor-pointer rounded-lg border px-4 py-1.5 text-[0.75rem] font-bold tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
            style={{
              background: "rgba(251,191,36,.10)",
              borderColor: "var(--accent)",
              color: "var(--accent)",
            }}
          >
            {running ? "investigating…" : "investigate risk"}
          </button>
        </div>
      </div>
    </section>
  );
}
