"use client";

// Strategy composer for /lab/backtest — the plain-English rule textarea,
// example fillers, symbol picker and run button, extracted from the old
// monolithic page (pure refactor; behavior identical). The CTA speaks
// Compare language: a backtest compares a rule against stored history —
// it never prompts a trade.

import { type Market, type WatchRow } from "@/lib/api";
import { fmtPct } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

const EXAMPLES = [
  "50/200 moving-average crossover",
  "buy when RSI below 30, sell above 70",
  "buy above the 200-day, sell below",
];

export default function StrategyComposer({
  text,
  setText,
  watch,
  watchErr,
  retryWatch,
  symbol,
  market,
  pick,
  selectedRow,
  run,
  running,
  canRun,
  softErr,
  hardErr,
}: {
  text: string;
  setText: (t: string) => void;
  watch: WatchRow[] | null;
  watchErr: string | null;
  retryWatch: () => void;
  symbol: string | null;
  market: Market;
  pick: (symbol: string, market: Market) => void;
  selectedRow: WatchRow | null;
  run: () => void;
  running: boolean;
  canRun: boolean;
  softErr: string | null;
  hardErr: string | null;
}) {
  return (
    <section className="panel">
      <div className="panel-h">STRATEGY — PLAIN ENGLISH</div>
      <div className="flex flex-col gap-4 px-4 py-4">
        <label className="flex flex-col gap-1.5">
          <span
            className="text-[0.75rem] tracking-wide"
            style={{ color: "var(--faint)" }}
          >
            describe the rules
          </span>
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter") run();
            }}
            rows={2}
            placeholder="e.g. 50/200 moving-average crossover"
            aria-label="strategy in plain English"
            className="w-full resize-y rounded-lg border px-3 py-2.5 text-[0.85rem] leading-relaxed"
            style={{
              background: "var(--panel2)",
              borderColor: "var(--border)",
              color: "var(--text)",
            }}
          />
        </label>

        {/* example fillers */}
        <div className="flex flex-wrap items-center gap-1.5">
          <span
            className="text-[0.75rem]"
            style={{ color: "var(--faint)" }}
          >
            try
          </span>
          {EXAMPLES.map((ex) => (
            <button
              key={ex}
              type="button"
              onClick={() => setText(ex)}
              className="chip cursor-pointer transition-colors duration-150 hover:brightness-125"
              style={{ borderColor: "var(--border)" }}
            >
              {ex}
            </button>
          ))}
        </div>

        {/* symbol picker */}
        <div className="flex flex-col gap-1.5">
          <span
            className="text-[0.75rem] tracking-wide"
            style={{ color: "var(--faint)" }}
          >
            symbol
          </span>
          {watch === null && watchErr === null && (
            <Skeleton lines={2} label="loading symbols" className="border-0 p-0" />
          )}
          {watch !== null && watch.length === 0 && (
            <EmptyState
              className="border-0 p-0"
              message="Your watchlist is empty"
              detail="Subscribe to symbols first, then a backtest has bars to run on."
            />
          )}
          {watchErr !== null && watch === null && (
            <ErrorState
              className="border-0 p-0"
              message="could not load symbols"
              hint="is the daemon running? start it with signaldeckd."
              retry={retryWatch}
            />
          )}
          {watch !== null && watch.length > 0 && (
            <div
              className="flex flex-wrap gap-1.5"
              role="group"
              aria-label="symbol picker"
            >
              {watch.map((row) => {
                const active =
                  row.symbol === symbol && row.market === market;
                return (
                  <button
                    key={`${row.market}:${row.symbol}`}
                    type="button"
                    aria-pressed={active}
                    onClick={() => pick(row.symbol, row.market)}
                    className="chip cursor-pointer tnum transition-colors duration-150 hover:brightness-125"
                    style={{
                      color: active ? "var(--text)" : "var(--dim)",
                      borderColor: active ? "var(--accent)" : "var(--border)",
                      background: active
                        ? "rgba(251,191,36,.08)"
                        : "var(--panel2)",
                    }}
                  >
                    {row.symbol}
                    <span
                      className="ml-1.5 text-[0.75rem]"
                      style={{ color: "var(--faint)" }}
                    >
                      {row.market}
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>

        {/* run — Compare framing: history, never a trade prompt */}
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={run}
            disabled={!canRun}
            className="min-h-[40px] cursor-pointer rounded-lg border px-5 py-2 text-[0.75rem] font-bold tracking-wide transition-colors duration-150 disabled:cursor-not-allowed"
            style={{
              borderColor: canRun ? "var(--accent)" : "var(--border)",
              background: canRun ? "rgba(251,191,36,.10)" : "var(--panel2)",
              color: canRun ? "var(--accent)" : "var(--faint)",
            }}
          >
            {running ? "comparing…" : "Compare against history"}
          </button>
          <span
            className="text-[0.75rem]"
            style={{ color: "var(--faint)" }}
          >
            ⌘/Ctrl+Enter to run
            {selectedRow && (
              <>
                {" · "}
                {selectedRow.symbol} last {fmtPct(selectedRow.dayChangePct)}{" "}
                on the day
              </>
            )}
          </span>
        </div>

        {/* soft (parse / history) feedback — the API's helpful message */}
        {softErr && (
          <div
            className="rounded-lg border px-3 py-2.5 text-[0.75rem] leading-relaxed whitespace-pre-wrap"
            style={{
              color: "var(--warn)",
              borderColor: "var(--warn)",
              background: "rgba(251,191,36,.06)",
            }}
          >
            {softErr}
          </div>
        )}

        {/* hard (daemon down) error */}
        {hardErr && (
          <ErrorState
            className="border-0 p-0"
            message={hardErr}
            hint="is the daemon running? start it with signaldeckd, then retry."
            retry={run}
          />
        )}
      </div>
    </section>
  );
}
