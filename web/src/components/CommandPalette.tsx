"use client";

// COMMAND PALETTE — ⌘K / Ctrl+K (or the header "⌘K search" chip, which fires
// the `sd-cmdk` window event). One input, two result groups:
//   PAGES   — fuzzy match over the hardcoded route registry below (kept in
//             sync with src/app by hand; includes /today /deck /compare).
//   SYMBOLS — live lookup against the SEC companies directory via the SAME
//             api binding /intel/companies uses (companiesList({q})), 200ms
//             debounced. Enter navigates to /s/stocks/SYM; Tab (or the "peek"
//             button) opens the CompanyPeek slide-over instead.
// Keyboard: ↑/↓ move, Enter runs, Esc closes, Tab is captured (peek on a
// symbol row, otherwise a no-op) so focus stays trapped on the input. Body
// scroll is locked while open.

import { useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { companiesList, type CompanyDirRow } from "@/lib/api";
import { usePeek } from "@/components/CompanyPeek";

/** Fired by the header chip in Shell to open the palette without a keyboard. */
export const CMDK_EVENT = "sd-cmdk";

// Route registry — every reachable page (canonical post-redirect URLs only;
// legacy /markets/* and /signals/* 307 into these).
const PAGES: { label: string; href: string }[] = [
  { label: "Home — dashboard & daily briefing", href: "/" },
  { label: "Watchlist — your symbols as cards", href: "/watchlist" },
  { label: "Watchlist — compare two symbols", href: "/watchlist/compare" },
  { label: "Market — overview & screener", href: "/market/overview" },
  { label: "Market — trends", href: "/market/trends" },
  { label: "Market — signals & predictions", href: "/market/signals" },
  { label: "Market — regimes (validated)", href: "/market/regimes" },
  { label: "Market — macro", href: "/market/macro" },
  { label: "Market — unusual activity", href: "/market/unusual" },
  { label: "Market — activity & alerts", href: "/market/activity" },
  { label: "Intel — news", href: "/intel/news" },
  { label: "Intel — companies directory", href: "/intel/companies" },
  { label: "Intel — company lookup", href: "/intel/company" },
  { label: "Intel — SEC filings", href: "/intel/filings" },
  { label: "Intel — insider trades", href: "/intel/insiders" },
  { label: "Intel — institutions (13F)", href: "/intel/institutions" },
  { label: "Intel — short interest", href: "/intel/shorts" },
  { label: "Intel — smart money", href: "/intel/smart-money" },
  { label: "Lab — research desk", href: "/lab/desk" },
  { label: "Lab — live pipeline health", href: "/lab/live" },
  { label: "Lab — backtest", href: "/lab/backtest" },
  { label: "Lab — signal backtest", href: "/lab/signal-backtest" },
  { label: "Lab — confluence", href: "/lab/confluence" },
  { label: "Lab — debate", href: "/lab/debate" },
  { label: "Lab — evolution", href: "/lab/evolution" },
  { label: "Lab — forecasts", href: "/lab/forecasts" },
  { label: "Lab — evidence graph", href: "/lab/graph" },
  { label: "Lab — honesty report", href: "/lab/honesty" },
  { label: "Lab — insights", href: "/lab/insights" },
  { label: "Lab — market memory", href: "/lab/memory" },
  { label: "Lab — optimizer", href: "/lab/optimizer" },
  { label: "Lab — paper trading", href: "/lab/paper" },
  { label: "Lab — portfolio", href: "/lab/portfolio" },
  { label: "Lab — research ledger", href: "/lab/research" },
  { label: "Lab — risk", href: "/lab/risk" },
  { label: "Lab — scenario", href: "/lab/scenario" },
  { label: "Lab — strategies", href: "/lab/strategies" },
  { label: "Lab — system", href: "/lab/system" },
  { label: "Lab — system quality", href: "/lab/system/quality" },
  { label: "Lab — system agents", href: "/lab/system/agents" },
  { label: "Lab — system AI", href: "/lab/system/ai" },
  { label: "Lab — track record", href: "/lab/track-record" },
  { label: "Welcome — product tour", href: "/welcome" },
  { label: "HUD (personal live-trading sync)", href: "/hud" },
];

/** Substring beats subsequence; earlier match beats later. -1 = no match. */
function fuzzyScore(query: string, target: string): number {
  const q = query.toLowerCase();
  const t = target.toLowerCase();
  if (!q) return 0;
  const idx = t.indexOf(q);
  if (idx >= 0) return 1000 - idx;
  // subsequence: every query char appears in order
  let ti = 0;
  let gaps = 0;
  for (const ch of q) {
    const found = t.indexOf(ch, ti);
    if (found < 0) return -1;
    gaps += found - ti;
    ti = found + 1;
  }
  return 500 - Math.min(gaps, 400);
}

type Item =
  | { type: "page"; label: string; href: string }
  | { type: "symbol"; row: CompanyDirRow };

/** Stable empty reference so the derived symbol list keeps memo identity. */
const NO_SYMS: CompanyDirRow[] = [];

export default function CommandPalette() {
  const router = useRouter();
  const pathname = usePathname();
  const peek = usePeek();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [syms, setSyms] = useState<CompanyDirRow[]>([]);
  const [activeRaw, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  // ⌘K / Ctrl+K toggle + the header chip's event.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    const onEvt = () => setOpen(true);
    window.addEventListener("keydown", onKey);
    window.addEventListener(CMDK_EVENT, onEvt);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener(CMDK_EVENT, onEvt);
    };
  }, []);

  // Close on navigation, and clear the query state when the palette closes.
  // Both are adjusted during render (the prev-key pattern, cf. viz/BigCandle)
  // instead of from an effect, which would cascade an extra render pass.
  const [prevPath, setPrevPath] = useState(pathname);
  if (pathname !== prevPath) {
    setPrevPath(pathname);
    setOpen(false);
  }
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (!open) {
      setQuery("");
      setSyms([]);
      setActive(0);
    }
  }

  // Focus the input and lock body scroll while open — external-system sync,
  // the only work left in an effect here.
  useEffect(() => {
    if (!open) return;
    inputRef.current?.focus();
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = "";
    };
  }, [open]);

  // Symbol lookup — 200ms debounce, stale responses dropped by the cleanup.
  useEffect(() => {
    const q = query.trim();
    if (!open || !q) return;
    let dead = false;
    const t = setTimeout(() => {
      companiesList({ q, limit: 8 })
        .then((r) => !dead && setSyms(r.companies ?? []))
        .catch(() => !dead && setSyms([]));
    }, 200);
    return () => {
      dead = true;
      clearTimeout(t);
    };
  }, [open, query]);

  // Closed or empty query → the SYMBOLS group is simply not shown. Derived, so
  // the debounce effect never has to setState synchronously to hide it.
  const symHits = open && query.trim().length > 0 ? syms : NO_SYMS;

  const pageHits = useMemo(() => {
    const q = query.trim();
    if (!q) return PAGES.slice(0, 10);
    return PAGES.map((p) => ({ p, s: Math.max(fuzzyScore(q, p.label), fuzzyScore(q, p.href)) }))
      .filter((x) => x.s >= 0)
      .sort((a, b) => b.s - a.s)
      .slice(0, 8)
      .map((x) => x.p);
  }, [query]);

  const items: Item[] = useMemo(
    () => [
      ...pageHits.map((p) => ({ type: "page" as const, label: p.label, href: p.href })),
      ...symHits.map((row) => ({ type: "symbol" as const, row })),
    ],
    [pageHits, symHits],
  );

  // Cursor clamped by derivation, not by a setState-in-effect: the result list
  // shrinks as the query narrows, and the stored index may outlive it.
  const active = Math.min(activeRaw, Math.max(0, items.length - 1));

  // Keep the active row scrolled into view.
  useEffect(() => {
    listRef.current
      ?.querySelector(`[data-idx="${active}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [active]);

  const run = (it: Item) => {
    setOpen(false);
    if (it.type === "page") router.push(it.href);
    else router.push(`/s/stocks/${encodeURIComponent(it.row.ticker)}`);
  };
  const runPeek = (row: CompanyDirRow) => {
    setOpen(false);
    peek.open(row.ticker, "stocks");
  };

  const onInputKey = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive(Math.min(active + 1, items.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(Math.max(active - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      const it = items[active];
      if (it) run(it);
    } else if (e.key === "Tab") {
      // Focus trap: Tab never leaves the input. On a symbol row it is the
      // secondary action — open the CompanyPeek instead of navigating.
      e.preventDefault();
      const it = items[active];
      if (it && it.type === "symbol") runPeek(it.row);
    }
  };

  if (!open) return null;

  // Flat index across both groups: pages first, symbols offset past them. Read
  // positionally rather than from a counter mutated during render.
  return (
    <div
      className="fixed inset-0 z-[70] flex items-start justify-center px-3 pt-[12vh]"
      role="dialog"
      aria-modal="true"
      aria-label="command palette"
    >
      <button
        aria-label="close command palette"
        onClick={() => setOpen(false)}
        className="absolute inset-0 cursor-default"
        style={{ background: "rgba(3,6,12,0.6)", backdropFilter: "blur(3px)" }}
        tabIndex={-1}
      />
      <div
        className="pop-in relative flex w-full max-w-xl flex-col overflow-hidden rounded-2xl"
        style={{
          background: "rgba(10,16,26,0.88)",
          backdropFilter: "blur(24px) saturate(150%)",
          WebkitBackdropFilter: "blur(24px) saturate(150%)",
          border: "1px solid rgba(255,255,255,0.12)",
          boxShadow: "var(--shadow-2)",
        }}
      >
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={onInputKey}
          placeholder="Jump to a page or type a ticker / company…"
          aria-label="search pages and symbols"
          aria-activedescendant={items[active] ? `cmdk-opt-${active}` : undefined}
          className="w-full border-b bg-transparent px-4 py-3 text-sm outline-none"
          style={{ borderColor: "rgba(255,255,255,0.09)", color: "var(--text)" }}
        />
        <ul
          ref={listRef}
          role="listbox"
          aria-label="results"
          className="max-h-[50vh] overflow-y-auto p-2"
        >
          {pageHits.length > 0 && (
            <li
              aria-hidden="true"
              className="px-2 pb-1 pt-2 text-[0.75rem] font-semibold tracking-[0.14em]"
              style={{ color: "var(--faint)" }}
            >
              PAGES
            </li>
          )}
          {pageHits.map((p, i) => {
            return (
              <li key={p.href} role="presentation">
                <button
                  id={`cmdk-opt-${i}`}
                  data-idx={i}
                  role="option"
                  aria-selected={i === active}
                  tabIndex={-1}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => run({ type: "page", label: p.label, href: p.href })}
                  className="flex w-full cursor-pointer items-center justify-between gap-3 rounded-lg px-2 py-1.5 text-left text-[0.8rem]"
                  style={{
                    background: i === active ? "var(--panel3)" : "transparent",
                    color: i === active ? "var(--text)" : "var(--dim)",
                  }}
                >
                  <span className="truncate">{p.label}</span>
                  <span className="mono shrink-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {p.href}
                  </span>
                </button>
              </li>
            );
          })}
          {symHits.length > 0 && (
            <li
              aria-hidden="true"
              className="px-2 pb-1 pt-3 text-[0.75rem] font-semibold tracking-[0.14em]"
              style={{ color: "var(--faint)" }}
            >
              SYMBOLS
            </li>
          )}
          {symHits.map((row, n) => {
            const i = pageHits.length + n;
            return (
              <li key={row.cik + row.ticker} role="presentation">
                <div
                  id={`cmdk-opt-${i}`}
                  data-idx={i}
                  role="option"
                  aria-selected={i === active}
                  onMouseEnter={() => setActive(i)}
                  className="flex w-full items-center gap-3 rounded-lg px-2 py-1.5 text-[0.8rem]"
                  style={{
                    background: i === active ? "var(--panel3)" : "transparent",
                    color: i === active ? "var(--text)" : "var(--dim)",
                  }}
                >
                  <button
                    tabIndex={-1}
                    onClick={() => run({ type: "symbol", row })}
                    className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 text-left"
                  >
                    <span className="mono font-bold" style={{ color: "var(--accent)" }}>
                      {row.ticker}
                    </span>
                    <span className="truncate">{row.name}</span>
                  </button>
                  <button
                    tabIndex={-1}
                    onClick={() => runPeek(row)}
                    title="open the company peek (Tab)"
                    className="chip shrink-0 cursor-pointer px-2 py-[2px] text-[0.75rem] hover:text-[var(--accent)]"
                    style={{ textDecorationLine: "none" }}
                  >
                    peek ⇥
                  </button>
                </div>
              </li>
            );
          })}
          {items.length === 0 && (
            <li className="px-2 py-3 text-[0.8rem]" style={{ color: "var(--faint)" }}>
              {query.trim()
                ? "no matching pages or tracked companies"
                : "type to search pages and the companies directory"}
            </li>
          )}
        </ul>
        <div
          className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-3 py-2 text-[0.75rem]"
          style={{ borderColor: "rgba(255,255,255,0.07)", color: "var(--faint)" }}
        >
          <span>↑↓ move</span>
          <span>Enter open</span>
          <span>Tab peek symbol</span>
          <span>Esc close</span>
        </div>
      </div>
    </div>
  );
}
