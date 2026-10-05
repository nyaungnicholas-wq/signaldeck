"use client";

// SYMBOL picker for the PREDICT page — extracted verbatim from the old
// monolithic page. Renders the watchlist (or the signed-out universe
// fallback) as pressable chips; loading/empty states live here so the page
// stays a thin composition.

import Skeleton from "@/components/Skeleton";
import EmptyState from "@/components/EmptyState";
import type { WatchRow } from "@/lib/api";
import type { Picked } from "@/hooks/usePredictions";

export default function SymbolPickerPanel({
  watch,
  watchErr,
  anonPicker,
  picked,
  onPick,
}: {
  watch: WatchRow[] | null;
  watchErr: string | null;
  anonPicker: boolean;
  picked: Picked | null;
  onPick: (p: Picked) => void;
}) {
  const watchLoading = watch === null && watchErr === null;

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        SYMBOL
        {anonPicker && (
          <span
            className="text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--faint)" }}
          >
            signed out — showing the 24 strongest-scored universe symbols; log in to pick from
            your own watchlist
          </span>
        )}
        {watchErr && watch !== null && (
          <span
            className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--bad)" }}
          >
            poll failed — showing last list
          </span>
        )}
      </div>
      <div className="px-4 py-3">
        {watchLoading ? (
          <Skeleton lines={2} label="loading symbols" className="border-0 p-0" />
        ) : watch && watch.length === 0 ? (
          <EmptyState
            message="No symbols tracked yet"
            detail="Subscribe to a symbol and it will appear here as a pickable chip."
            className="border-0 p-0"
          />
        ) : (
          <div role="group" aria-label="Pick a symbol" className="flex flex-wrap gap-1.5">
            {(watch ?? []).map((r) => {
              const active = !!picked && picked.symbol === r.symbol && picked.market === r.market;
              return (
                <button
                  key={`${r.market}:${r.symbol}`}
                  type="button"
                  onClick={() => onPick({ symbol: r.symbol, market: r.market })}
                  aria-pressed={active}
                  className="chip mono cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
                  style={
                    active
                      ? { color: "var(--accent)", borderColor: "var(--accent)" }
                      : undefined
                  }
                >
                  {r.symbol}
                  <span className="ml-1.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {r.market}
                  </span>
                </button>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}
