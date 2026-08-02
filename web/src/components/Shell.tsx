"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState, useSyncExternalStore } from "react";
import { api, type Me } from "@/lib/api";
import FreshnessBadge from "@/components/FreshnessBadge";
import OfflineBanner from "@/components/OfflineBanner";
import CommandPalette, { CMDK_EVENT } from "@/components/CommandPalette";
import FirstRunTour from "@/components/FirstRunTour";

// Nav consolidation (2026-07-19, user decision): 9 tabs → 5 clean hubs.
// HOME absorbs the old DASHBOARD + TODAY; WATCHLIST absorbs DECK + COMPARE;
// DESK and LIVE fold into LAB as sub-tabs. `href` is the hub's default
// sub-tab; `match` lists every pathname prefix that keeps the hub highlighted
// — INCLUDING the old flat URLs, so the active state is right during the brief
// moment before a next.config redirect lands.
const NAV: { href: string; label: string; match: string[] }[] = [
  { href: "/", label: "HOME", match: ["/", "/today"] },
  // MARKETS + SIGNALS merged into one MARKET hub. Legacy prefixes stay in
  // `match` so the hub highlights through the redirect.
  {
    href: "/market/overview",
    label: "MARKET",
    match: [
      "/market",
      "/signals",
      "/screener",
      "/trends",
      "/regime",
      "/macro",
      "/predict",
      "/alerts",
    ],
  },
  // WATCHLIST = the old DECK (watchlist cards) + COMPARE (two symbols), now
  // one hub with a compare sub-tab.
  {
    href: "/watchlist",
    label: "WATCHLIST",
    match: ["/watchlist", "/deck", "/compare"],
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
      // DESK (AI research desk) + LIVE (pipeline health) fold in here.
      "/desk",
      "/live",
    ],
  },
  // HUD (the user's personal PUSH-20 live-trading sync) is deliberately NOT in
  // the nav — it stays reachable directly at /hud but is personal, not part of
  // the product surface.
];

const READING_KEY = "sd-reading-mode";
const VIEW_KEY = "sd-view-mode";

// Both header toggles read localStorage through useSyncExternalStore, keyed
// off the same window events their toggles fire — SSR renders the default,
// the first client snapshot then reflects the persisted choice.
function subscribeViewMode(cb: () => void): () => void {
  window.addEventListener("sd-view-mode", cb);
  return () => window.removeEventListener("sd-view-mode", cb);
}
function getViewModePro(): boolean {
  try {
    return localStorage.getItem(VIEW_KEY) === "pro";
  } catch {
    return false;
  }
}
function subscribeReadingMode(cb: () => void): () => void {
  window.addEventListener("sd-reading-mode", cb);
  return () => window.removeEventListener("sd-reading-mode", cb);
}
function getReadingModeOn(): boolean {
  try {
    return localStorage.getItem(READING_KEY) === "on";
  } catch {
    return false;
  }
}
const getServerFalse = () => false;

/** Header toggle for SIMPLE/PRO language — persists in localStorage, flips
 *  `data-view-mode` on <html>, and fires an `sd-view-mode` event so every
 *  mounted <Plain> re-reads. Default is SIMPLE (plain English first); PRO
 *  leads with the raw numbers. Caveats/gates render in BOTH modes. */
function ViewModeToggle() {
  const pro = useSyncExternalStore(subscribeViewMode, getViewModePro, getServerFalse);

  // Keep <html data-view-mode> in sync (covers first mount + any change).
  useEffect(() => {
    document.documentElement.setAttribute("data-view-mode", pro ? "pro" : "simple");
  }, [pro]);

  const toggle = () => {
    const next = !pro;
    localStorage.setItem(VIEW_KEY, next ? "pro" : "simple");
    document.documentElement.setAttribute("data-view-mode", next ? "pro" : "simple");
    window.dispatchEvent(new Event("sd-view-mode"));
  };

  return (
    <button
      type="button"
      onClick={toggle}
      aria-pressed={pro}
      title={
        pro
          ? "PRO: raw metrics first, plain English as the subtitle"
          : "SIMPLE: plain English first, raw metrics small underneath — honesty gates show in both"
      }
      className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
      style={pro ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
    >
      {pro ? "pro" : "simple"}
    </button>
  );
}

/** Header toggle for reading mode — persists in localStorage and flips
 *  `data-reading-mode` on <html> so globals.css can restyle site-wide. */
function ReadingModeToggle() {
  const on = useSyncExternalStore(subscribeReadingMode, getReadingModeOn, getServerFalse);

  // Keep <html data-reading-mode> in sync (covers first mount + any change).
  useEffect(() => {
    document.documentElement.setAttribute("data-reading-mode", on ? "on" : "off");
  }, [on]);

  const toggle = () => {
    const next = !on;
    localStorage.setItem(READING_KEY, next ? "on" : "off");
    document.documentElement.setAttribute("data-reading-mode", next ? "on" : "off");
    window.dispatchEvent(new Event("sd-reading-mode"));
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
      href="/market/activity"
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
      .catch((e: unknown) => {
        if (!alive) return;
        // Only a 401 is "logged out" — a daemon outage must not flip the
        // chip into a login prompt (first load stays "unknown" instead).
        if (e instanceof Error && e.message.startsWith("API 401")) setMe(null);
      });
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

/** Daemon connectivity indicator — a coloured dot plus a plain-words label.
 *  Shown inline in the header on desktop and inside the menu panel on mobile. */
function DaemonStatus({ up }: { up: boolean | null }) {
  return (
    <span className="flex items-center gap-2">
      <span
        aria-label={up === null ? "checking daemon" : up ? "daemon connected" : "daemon offline"}
        className="inline-block h-2 w-2 shrink-0 rounded-full"
        style={{
          background: up === null ? "var(--faint)" : up ? "var(--ok)" : "var(--bad)",
          boxShadow: up ? "0 0 8px rgba(52,211,153,.7)" : undefined,
        }}
      />
      <span>
        {up === null ? "connecting" : up ? "daemon live" : "daemon offline — start signaldeckd"}
      </span>
    </span>
  );
}

/** Brand mark: amber signal-bars glyph + mono wordmark (one place, both
 *  header variants). */
function Brand() {
  return (
    <span className="inline-flex items-center gap-2">
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" className="shrink-0">
        <rect x="1" y="9" width="3.2" height="6" rx="1" fill="var(--accent)" opacity="0.4" />
        <rect x="6.4" y="5" width="3.2" height="10" rx="1" fill="var(--accent)" opacity="0.7" />
        <rect x="11.8" y="1" width="3.2" height="14" rx="1" fill="var(--accent)" />
      </svg>
      <span className="mono text-base font-extrabold tracking-[0.14em]">
        <span style={{ color: "var(--accent)" }}>SIGNAL</span>DECK
      </span>
    </span>
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

  // Close the mobile menu whenever navigation happens (guarded adjustment
  // during render — never paints the menu open on the new page).
  const [prevPathname, setPrevPathname] = useState(pathname);
  if (prevPathname !== pathname) {
    setPrevPathname(pathname);
    if (menuOpen) setMenuOpen(false);
  }

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
        className={`nav-link flex min-h-[40px] cursor-pointer items-center px-3 py-2 font-medium ${
          active ? "nav-link-active" : ""
        }`}
      >
        {n.label}
      </Link>
    );
  });

  // Public routes render minimal chrome (brand + footer) with no nav, freshness
  // chip, toggles or daemon dot: /login (sign-in), /proof (the shareable
  // public track-record + ledger page) and /accuracy (the registry verdicts).
  // A nav full of links that bounce to /login, or a "updated 0s ago" chip,
  // would both be misleading here.
  if (pathname === "/login" || pathname === "/proof" || pathname === "/accuracy") {
    return (
      <div className="mx-auto flex min-h-screen w-full max-w-[1400px] flex-col gap-4 p-3 sm:p-4">
        <header className="panel px-4 py-3 sm:px-5">
          <Link href="/" className="inline-flex shrink-0 items-center">
            <Brand />
          </Link>
        </header>
        <main className="flex flex-1 flex-col gap-4">{children}</main>
        <footer
          className="px-2 pb-2 text-[0.75rem] leading-relaxed"
          style={{ color: "var(--faint)" }}
        >
          SignalDeck measures and stores; it does not advise. Not financial advice.
        </footer>
      </div>
    );
  }

  return (
    <div className="mx-auto flex min-h-screen w-full max-w-[1400px] flex-col gap-4 p-3 sm:p-4">
      <OfflineBanner />
      {/* Sticky glass header: translucent panel + backdrop blur so content
          scrolling underneath reads as depth, not clutter. */}
      <header
        className="panel sticky top-3 z-40 px-4 py-3 backdrop-blur-xl sm:top-4 sm:px-5"
        style={{
          background: "color-mix(in srgb, var(--panel) 84%, transparent)",
        }}
      >
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <Link href="/" className="inline-flex shrink-0 items-center">
            <Brand />
            <span
              className="ml-3 hidden text-[0.75rem] tracking-wider xl:inline"
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
          <div
            className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-2 text-[0.75rem]"
            style={{ color: "var(--dim)" }}
          >
            {/* Command palette trigger — the keyboard-free way in; ⌘K/Ctrl+K
                fires the same event listener inside CommandPalette. */}
            <button
              type="button"
              onClick={() => window.dispatchEvent(new Event(CMDK_EVENT))}
              title="open the command palette — jump to any page or symbol"
              aria-label="open command palette (Command+K)"
              className="chip flex min-h-[40px] cursor-pointer items-center gap-1 px-3 transition-colors duration-150 hover:text-[var(--text)]"
            >
              <span className="mono">⌘K</span>
              <span className="hidden sm:inline">search</span>
            </button>
            <AlertsBell />
            <AuthChip />
            {/* Secondary controls: inline on desktop, folded into the menu panel
                on mobile so the header row can't overflow a phone width (which
                was pushing the menu off-canvas). */}
            <div className="hidden items-center gap-2 lg:flex">
              <FreshnessBadge />
              <ViewModeToggle />
              <ReadingModeToggle />
              <DaemonStatus up={up} />
            </div>
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
        {/* Mobile nav + settings (secondary controls live here on phones) */}
        {menuOpen && (
          <div id="mobile-nav" className="mt-3 border-t pt-3 lg:hidden" style={{ borderColor: "var(--border)" }}>
            <nav
              aria-label="Primary"
              className="grid grid-cols-2 gap-1 text-[0.78rem] tracking-[0.1em] sm:grid-cols-3"
            >
              {navLinks}
            </nav>
            <div
              className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 border-t pt-3 text-[0.75rem]"
              style={{ borderColor: "var(--border)", color: "var(--dim)" }}
            >
              <FreshnessBadge />
              <ViewModeToggle />
              <ReadingModeToggle />
              <DaemonStatus up={up} />
            </div>
          </div>
        )}
      </header>
      <CommandPalette />
      <FirstRunTour />
      <main className="flex flex-1 flex-col gap-4">{children}</main>
      <footer
        className="mt-2 border-t px-2 pt-3 pb-2 text-[0.75rem] leading-relaxed"
        style={{ color: "var(--faint)", borderColor: "var(--border)" }}
      >
        SignalDeck measures and stores; it does not advise. Every score decomposes into its
        components; every tendency ships with its sample size; the Honesty page grades the
        scores against what actually happened. Not financial advice.
      </footer>
    </div>
  );
}
