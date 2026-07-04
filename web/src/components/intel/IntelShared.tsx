"use client";

// INTEL hub shared surface (Stage 5): ONE merged header + ONE symbol-filter
// box that follows the user across all five sub-tabs (News · Filings ·
// Insiders · Institutions · Congress). The filter lives in React context
// mounted from the hub layout, so it survives sub-tab navigation (the layout
// never remounts) — type "AAPL" on Filings, switch to Insiders, and the same
// filter is already applied. Pages read it via useIntelSymbol().
//
// HONESTY: the header states the provenance ("public-domain SEC + news
// data") once for the whole hub; each sub-tab keeps its own lag/proxy/
// dead-mirror notes — this header adds context, it never replaces them.

import { createContext, useContext, useState } from "react";

const IntelSymbolContext = createContext<{
  symbol: string;
  setSymbol: (s: string) => void;
}>({ symbol: "", setSymbol: () => {} });

/** The shared, uppercase-normalized symbol filter ("" = no filter). */
export function useIntelSymbol(): { symbol: string; setSymbol: (s: string) => void } {
  return useContext(IntelSymbolContext);
}

export default function IntelShared({ children }: { children: React.ReactNode }) {
  const [symbol, setSymbol] = useState("");
  const set = (s: string) => setSymbol(s.toUpperCase().trim());

  return (
    <IntelSymbolContext.Provider value={{ symbol, setSymbol: set }}>
      {/* merged hub header */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">INTEL</h1>
        <span className="text-[0.78rem]" style={{ color: "var(--faint)" }}>
          stock intelligence — public-domain SEC + news data, one place
        </span>
        <label className="ml-auto flex items-center gap-2 text-[0.75rem]">
          <span style={{ color: "var(--faint)" }}>symbol</span>
          <input
            value={symbol}
            onChange={(e) => set(e.target.value)}
            placeholder="filter all intel tabs…"
            aria-label="symbol filter shared across all intel sub-tabs"
            className="chip min-h-[40px] w-40 bg-transparent px-3 outline-none"
            style={{ color: "var(--text)" }}
          />
          {symbol && (
            <button
              type="button"
              onClick={() => set("")}
              className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
              aria-label="clear symbol filter"
              style={{ color: "var(--dim)" }}
            >
              clear
            </button>
          )}
        </label>
      </div>
      {children}
    </IntelSymbolContext.Provider>
  );
}
