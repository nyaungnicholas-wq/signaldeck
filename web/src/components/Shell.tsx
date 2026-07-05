"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { api, type Me } from "@/lib/api";

// Stage 2 nav consolidation: 24 flat entries → 6 hubs (user decision; HUD
// stays separate). `href` is the hub's default sub-tab; `match` lists every
// pathname prefix that keeps the hub highlighted — INCLUDING the old flat
// URLs, so the active state is right even during the brief moment before a
// next.config redirect lands.
const NAV: { href: string; label: string; match: string[] }[] = [
  { href: "/", label: "DASHBOARD", match: ["/"] },
  {
    href: "/markets/screener",
    label: "MARKETS",
    match: ["/markets", "/screener", "/trends", "/regime", "/macro"],
  },
  {
    href: "/signals/predictions",
    label: "SIGNALS",
    match: ["/signals", "/predict", "/forecast", "/insights", "/alerts"],
  },
  {
    href: "/intel/news",
    label: "INTEL",
    match: ["/intel", "/news", "/filings", "/insiders", "/institutions", "/congress"],
  },
  {
    href: "/lab/backtest",
    label: "LAB",
    match: [
      "/lab",
      "/backtest",
      "/signal-backtest",
      "/risk",
      "/portfolio",
      "/paper",
      "/track-record",
      "/honesty",
      "/quality",
      "/agents",
      "/ai",
    ],
  },
  // HUD (the user's personal PUSH-20 live-trading sync) is deliberately NOT in
  // the nav — it stays reachable directly at /hud but is personal, not part of
  // the product surface.
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

/** Header bell: unread-alert count, polled every 30s. Hidden while logged
 *  out (the alerts endpoint is session-scoped and returns 401). Links to
 *  the /alerts page. */
function AlertsBell() {
  const [count, setCount] = useState<number | null>(null); // null = hide

  useEffect(() => {
    let alive = true;
    let paused = false; // after a 401/offline, stop the 30s cadence…
    const load = () =>
      api
        .alerts(true, 100)
        .then((rows) => {
          if (!alive) return;
          paused = false;
          setCount(rows.length);
        })
        .catch(() => {
          if (!alive) return;
          paused = true; // …and only re-probe on focus/seen events
          setCount(null); // 401 / offline → hide
        });
    load();
    const t = setInterval(() => {
      if (!paused) load();
    }, 30000);
    const onSeen = () => load(); // refresh immediately after "mark all read"
    const onFocus = () => {
      if (paused) load(); // cheap re-probe after logging in on another page
    };
    window.addEventListener("sd-alerts-seen", onSeen);
    window.addEventListener("focus", onFocus);
    return () => {
      alive = false;
      clearInterval(t);
      window.removeEventListener("sd-alerts-seen", onSeen);
      window.removeEventListener("focus", onFocus);
    };
  }, []);

  if (count === null) return null;
  return (
    <Link
      href="/signals/alerts"
      aria-label={`alerts — ${count} unread`}
      title={`${count} unread alert(s)`}
      className="chip flex min-h-[40px] cursor-pointer items-center gap-1.5 px-3 transition-colors duration-150 hover:text-[var(--text)]"
      style={count > 0 ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
    >
      <span aria-hidden="true">◉</span>
      <span className="tnum">{count}</span>
      <span className="hidden sm:inline">alerts</span>
    </Link>
  );
}

/** Header auth chip: shows who is signed in (session cookie); click to log
 *  out. Logged out / daemon unreachable → links to /login instead. */
function AuthChip() {
  const pathname = usePathname();
  const router = useRouter();
  const [me, setMe] = useState<Me | null | undefined>(undefined); // undefined = unknown

  useEffect(() => {
    let alive = true;
    api
      .me()
      .then((m) => alive && setMe(m))
      .catch(() => alive && setMe(null)); // 401 / offline
    return () => {
      alive = false;
    };
  }, [pathname]); // re-check after login/logout navigations

  if (me === undefined) return null;
  if (me === null) {
    if (pathname === "/login") return null; // already there
    return (
      <Link
        href="/login"
        className="chip flex min-h-[40px] cursor-pointer items-center px-3 transition-colors duration-150 hover:text-[var(--text)]"
      >
        login
      </Link>
    );
  }
  return (
    <button
      type="button"
      onClick={() => {
        api
          .logout()
          .catch(() => {})
          .finally(() => router.replace("/login"));
      }}
      title={`signed in as ${me.username} — click to log out`}
      aria-label={`signed in as ${me.username} — log out`}
      className="chip flex min-h-[40px] cursor-pointer items-center gap-1.5 px-3 transition-colors duration-150 hover:text-[var(--text)]"
    >
      <span aria-hidden="true" style={{ color: "var(--accent)" }}>
        ◈
      </span>
      <span className="max-w-[10ch] truncate">{me.username}</span>
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
    const active =
      n.href === "/"
        ? pathname === "/"
        : n.match.some((m) => pathname === m || pathname.startsWith(m + "/"));
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
            <AlertsBell />
            <AuthChip />
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
