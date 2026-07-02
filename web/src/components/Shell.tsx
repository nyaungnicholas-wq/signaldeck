"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

const NAV: { href: string; label: string }[] = [
  { href: "/", label: "WATCHLIST" },
  { href: "/screener", label: "SCREENER" },
  { href: "/trends", label: "TRENDS" },
  { href: "/forecast", label: "FORECAST" },
  { href: "/backtest", label: "BACKTEST" },
  { href: "/risk", label: "RISK" },
  { href: "/portfolio", label: "PORTFOLIO" },
  { href: "/insights", label: "INSIGHTS" },
  { href: "/honesty", label: "HONESTY" },
  { href: "/quality", label: "QUALITY" },
  { href: "/agents", label: "AGENTS" },
  { href: "/hud", label: "PUSH-20" },
];

/** App chrome: brand bar + nav + daemon connectivity dot. */
export default function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const [up, setUp] = useState<boolean | null>(null);

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

  return (
    <div className="mx-auto flex min-h-screen max-w-[1400px] flex-col gap-4 p-4">
      <header className="panel flex flex-wrap items-center gap-x-5 gap-y-2 px-5 py-3">
        <Link href="/" className="shrink-0">
          <span className="text-base font-extrabold tracking-[0.14em]">
            <span style={{ color: "var(--accent)" }}>SIGNAL</span>DECK
          </span>
          <span className="ml-3 hidden text-[0.68rem] tracking-wider md:inline" style={{ color: "var(--faint)" }}>
            data-first market intelligence
          </span>
        </Link>
        <nav aria-label="Primary" className="flex flex-wrap items-center gap-1 text-[0.7rem] tracking-[0.12em]">
          {NAV.map((n) => {
            const active = n.href === "/" ? pathname === "/" : pathname.startsWith(n.href);
            return (
              <Link
                key={n.href}
                href={n.href}
                className="cursor-pointer rounded px-2.5 py-1.5 transition-colors duration-200"
                style={{
                  color: active ? "var(--text)" : "var(--dim)",
                  background: active ? "var(--panel2)" : "transparent",
                  border: `1px solid ${active ? "var(--border)" : "transparent"}`,
                }}
              >
                {n.label}
              </Link>
            );
          })}
        </nav>
        <div className="ml-auto flex items-center gap-2 text-[0.72rem]" style={{ color: "var(--dim)" }}>
          <span
            aria-label={up === null ? "checking daemon" : up ? "daemon connected" : "daemon offline"}
            className="inline-block h-2 w-2 rounded-full"
            style={{
              background: up === null ? "var(--faint)" : up ? "var(--ok)" : "var(--bad)",
              boxShadow: up ? "0 0 8px rgba(52,211,153,.7)" : undefined,
            }}
          />
          {up === null ? "connecting" : up ? "daemon live" : "daemon offline — start signaldeckd"}
        </div>
      </header>
      <main className="flex flex-1 flex-col gap-4">{children}</main>
      <footer className="px-2 pb-2 text-[0.66rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        SignalDeck measures and stores; it does not advise. Every score decomposes into its
        components; every tendency ships with its sample size; the Honesty page grades the
        scores against what actually happened. Not financial advice.
      </footer>
    </div>
  );
}
