"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

const NAV: { href: string; label: string }[] = [
  { href: "/", label: "WATCHLIST" },
  { href: "/screener", label: "SCREENER" },
  { href: "/trends", label: "TRENDS" },
  { href: "/predict", label: "PREDICT" },
  { href: "/regime", label: "REGIME" },
  { href: "/news", label: "NEWS" },
  { href: "/macro", label: "MACRO" },
  { href: "/forecast", label: "FORECAST" },
  { href: "/backtest", label: "BACKTEST" },
  { href: "/risk", label: "RISK" },
  { href: "/portfolio", label: "PORTFOLIO" },
  { href: "/ai", label: "AI" },
  { href: "/insights", label: "INSIGHTS" },
  { href: "/honesty", label: "HONESTY" },
  { href: "/quality", label: "QUALITY" },
  { href: "/agents", label: "AGENTS" },
  { href: "/hud", label: "PUSH-20" },
];

const READING_KEY = "sd-reading-mode";

/** Header toggle for reading mode — persists in localStorage and flips
 *  `data-reading-mode` on <html> so globals.css can restyle site-wide. */
function ReadingModeToggle() {
  const [on, setOn] = useState(false);

  useEffect(() => {
    const saved = localStorage.getItem(READING_KEY) === "on";
    setOn(saved);
    document.documentElement.setAttribute("data-reading-mode", saved ? "on" : "off");
  }, []);

  const toggle = () => {
    const next = !on;
    setOn(next);
    localStorage.setItem(READING_KEY, next ? "on" : "off");
    document.documentElement.setAttribute("data-reading-mode", next ? "on" : "off");
  };

  return (
    <button
      type="button"
      onClick={toggle}
      aria-pressed={on}
      title="Reading mode: larger text, more spacing, higher contrast"
      className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
      style={on ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
    >
      Aa {on ? "reading" : "compact"}
    </button>
  );
}

/** App chrome: brand bar + nav + daemon connectivity dot. */
export default function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const [up, setUp] = useState<boolean | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);

  useEffect(() => {
    let alive = true;
    const check = () =>
      api
        .health()
        .then(() => alive && setUp(true))
        .catch(() => alive && setUp(false));
    check();
    const t = setInterval(check, 10000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  // Close the mobile menu whenever navigation happens.
  useEffect(() => {
    setMenuOpen(false);
  }, [pathname]);

  const navLinks = NAV.map((n) => {
    const active = n.href === "/" ? pathname === "/" : pathname.startsWith(n.href);
    return (
      <Link
        key={n.href}
        href={n.href}
        aria-current={active ? "page" : undefined}
        className="flex min-h-[40px] cursor-pointer items-center rounded px-3 py-2 transition-colors duration-200"
        style={{
          color: active ? "var(--text)" : "var(--dim)",
          background: active ? "var(--panel2)" : "transparent",
          border: `1px solid ${active ? "var(--border)" : "transparent"}`,
        }}
      >
        {n.label}
      </Link>
    );
  });

  return (
    <div className="mx-auto flex min-h-screen w-full max-w-[1400px] flex-col gap-4 p-3 sm:p-4">
      <header className="panel px-4 py-3 sm:px-5">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <Link href="/" className="shrink-0">
            <span className="text-base font-extrabold tracking-[0.14em]">
              <span style={{ color: "var(--accent)" }}>SIGNAL</span>DECK
            </span>
            <span
              className="ml-3 hidden text-[0.72rem] tracking-wider xl:inline"
              style={{ color: "var(--faint)" }}
            >
              data-first market intelligence
            </span>
          </Link>
          {/* Desktop nav */}
          <nav
            aria-label="Primary"
            className="hidden flex-wrap items-center gap-1 text-[0.75rem] tracking-[0.1em] lg:flex"
          >
            {navLinks}
          </nav>
          <div className="ml-auto flex items-center gap-2 text-[0.75rem]" style={{ color: "var(--dim)" }}>
            <ReadingModeToggle />
            <span className="flex items-center gap-2">
              <span
                aria-label={up === null ? "checking daemon" : up ? "daemon connected" : "daemon offline"}
                className="inline-block h-2 w-2 rounded-full"
                style={{
                  background: up === null ? "var(--faint)" : up ? "var(--ok)" : "var(--bad)",
                  boxShadow: up ? "0 0 8px rgba(52,211,153,.7)" : undefined,
                }}
              />
              <span className="hidden sm:inline">
                {up === null ? "connecting" : up ? "daemon live" : "daemon offline — start signaldeckd"}
              </span>
            </span>
            {/* Mobile menu button */}
            <button
              type="button"
              onClick={() => setMenuOpen((o) => !o)}
              aria-expanded={menuOpen}
              aria-controls="mobile-nav"
              className="chip flex min-h-[40px] min-w-[40px] cursor-pointer items-center justify-center lg:hidden"
              style={menuOpen ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
            >
              {menuOpen ? "CLOSE" : "MENU"}
            </button>
          </div>
        </div>
        {/* Mobile nav */}
        {menuOpen && (
          <nav
            id="mobile-nav"
            aria-label="Primary"
            className="mt-3 grid grid-cols-2 gap-1 border-t pt-3 text-[0.78rem] tracking-[0.1em] sm:grid-cols-3 lg:hidden"
            style={{ borderColor: "var(--border)" }}
          >
            {navLinks}
          </nav>
        )}
      </header>
      <main className="flex flex-1 flex-col gap-4">{children}</main>
      <footer className="px-2 pb-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        SignalDeck measures and stores; it does not advise. Every score decomposes into its
        components; every tendency ships with its sample size; the Honesty page grades the
        scores against what actually happened. Not financial advice.
      </footer>
    </div>
  );
}
