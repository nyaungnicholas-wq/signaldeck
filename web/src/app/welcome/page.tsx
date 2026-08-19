"use client";

// /welcome — a 60-second intent-based onboarding in four steps:
//   1. what brings you here — sets the goal, which sets SIMPLE/PRO and how much
//      surface the nav shows. Reversible from the Help panel at any time.
//   2. pick symbols to watch (REAL api.subscribe calls — on 401 we say "log
//      in first" and link /login; success is never faked)
//   3. how alerts work — the honest rulebook (alerts fire automatically for
//      watched symbols; there is NO per-user alert config to invent)
//   4. the daily briefing (once per NY day, at/after 7am) → dashboard.
// "Skip" is always visible; both Skip and Finish set sd-onboarded="1".
//
// The onboarded flag, the goal and the view-mode coupling all live in
// lib/goal.ts — this page owns none of that state, it just drives it.

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { api, type Market } from "@/lib/api";
import { ALERT_RULES } from "@/components/signals/alerts/rules";
import { GOALS, applyGoal, markOnboarded, useGoal } from "@/lib/goal";

const CURATED: { symbol: string; market: Market }[] = [
  { symbol: "SPY", market: "stocks" },
  { symbol: "QQQ", market: "stocks" },
  { symbol: "NVDA", market: "stocks" },
  { symbol: "TSLA", market: "stocks" },
  { symbol: "AAPL", market: "stocks" },
  { symbol: "AMD", market: "stocks" },
  { symbol: "COIN", market: "stocks" },
  { symbol: "BTC/USD", market: "crypto" },
];

// The four automatic alert kinds, in the rulebook's display order.
const ALERT_KINDS = ["breakout", "regime_change", "prediction_high", "prediction_low"];

const STEP_TITLES = [
  "What brings you here?",
  "What do you want to watch?",
  "How should we alert you?",
  "Your daily briefing",
];

/** get() throws "API 401: …", post() throws the daemon's error string. */
function isAuthError(msg: string): boolean {
  return /\b401\b|unauthori[sz]ed|not logged in|no session|log ?in/i.test(msg);
}

function CheckIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M3 8.5l3.5 3.5L13 5"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export default function WelcomePage() {
  const router = useRouter();
  const goal = useGoal();
  const [step, setStep] = useState(1);
  const [loggedIn, setLoggedIn] = useState<boolean | null>(null); // null = unknown
  const [needLogin, setNeedLogin] = useState(false);
  const [added, setAdded] = useState<Set<string>>(new Set()); // "market:symbol"
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sym, setSym] = useState("");
  const [mkt, setMkt] = useState<Market>("stocks");
  const headingRef = useRef<HTMLHeadingElement | null>(null);
  const mounted = useRef(false);

  // Session preflight: mark chips that are ALREADY on the watchlist. On 401
  // we surface the honest "log in first" notice instead of faking adds.
  useEffect(() => {
    let alive = true;
    api
      .me()
      .then(() => {
        if (alive) setLoggedIn(true);
        return api.watchlist();
      })
      .then((rows) => {
        if (alive && Array.isArray(rows)) {
          setAdded(new Set(rows.map((r) => `${r.market}:${r.symbol}`)));
        }
      })
      .catch((err) => {
        if (!alive) return;
        if (isAuthError(err instanceof Error ? err.message : String(err))) setLoggedIn(false);
        // daemon unreachable → leave unknown; adds will report their own error
      });
    return () => {
      alive = false;
    };
  }, []);

  // Keyboard users land on the new step's heading after navigating.
  useEffect(() => {
    if (mounted.current) headingRef.current?.focus();
    else mounted.current = true;
  }, [step]);

  async function addSymbol(symbol: string, market: Market) {
    const key = `${market}:${symbol}`;
    if (added.has(key) || pending !== null) return;
    setPending(key);
    setError(null);
    try {
      await api.subscribe(symbol, market);
      setAdded((prev) => new Set(prev).add(key));
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (isAuthError(msg)) setNeedLogin(true);
      else setError(msg);
    } finally {
      setPending(null);
    }
  }

  function onAddFree(e: React.FormEvent) {
    e.preventDefault();
    const s = sym.trim().toUpperCase();
    if (!s) return;
    const market: Market = s.includes("/") ? "crypto" : mkt;
    void addSymbol(s, market).then(() => setSym(""));
  }

  function finish() {
    markOnboarded();
    router.push("/");
  }

  const showLoginNotice = needLogin || loggedIn === false;

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 px-4 py-6">
      {/* header: brand + the ALWAYS-visible skip */}
      <div className="flex items-center gap-3">
        <div>
          <div className="text-xs tracking-widest" style={{ color: "var(--faint)" }}>
            SIGNALDECK
          </div>
          <h1 className="m-0 text-lg font-bold">WELCOME — 60-second setup</h1>
        </div>
        <Link
          href="/"
          onClick={markOnboarded}
          className="ml-auto inline-flex min-h-[44px] cursor-pointer items-center px-3 text-[0.75rem] underline transition-colors duration-150 hover:text-[var(--accent)]"
          style={{ color: "var(--dim)" }}
        >
          Skip — go to dashboard
        </Link>
      </div>

      {/* stepper */}
      <nav aria-label="setup progress">
        <ol className="m-0 flex list-none flex-wrap gap-2 p-0">
          {STEP_TITLES.map((t, i) => {
            const n = i + 1;
            const active = n === step;
            return (
              <li
                key={t}
                aria-current={active ? "step" : undefined}
                className="chip flex items-center gap-2 px-3 py-[6px] text-[0.75rem]"
                style={
                  active
                    ? { borderColor: "var(--accent)", color: "var(--accent)" }
                    : n < step
                      ? { color: "var(--bid)" }
                      : undefined
                }
              >
                <span className="tnum font-bold">{n}</span>
                <span>{t}</span>
                {n < step ? <CheckIcon /> : null}
              </li>
            );
          })}
        </ol>
      </nav>
      <p className="m-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        step {step} of {STEP_TITLES.length}
      </p>

      <section className="panel">
        <div className="panel-h">
          <span>
            STEP {step} · {STEP_TITLES[step - 1].toUpperCase()}
          </span>
        </div>

        <div className="flex flex-col gap-4 px-5 py-5">
          <h2 ref={headingRef} tabIndex={-1} className="m-0 text-base font-bold outline-none">
            {STEP_TITLES[step - 1]}
          </h2>

          {/* STEP 1 — intent. Picking a goal sets the view mode (SIMPLE hides
              the 35 specialist surfaces behind the /advanced door; PRO shows
              everything) and where "done" lands you. Changeable later from the
              Help panel, which is why nothing here is a one-way door. */}
          {step === 1 && (
            <>
              <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                This just decides how much SignalDeck shows you up front. Nothing is locked away —
                you can change it any time from the ? menu at the top of the screen.
              </p>

              <ul className="m-0 flex list-none flex-col gap-2 p-0">
                {GOALS.map((g) => {
                  const active = goal === g.key;
                  return (
                    <li key={g.key}>
                      <button
                        type="button"
                        aria-pressed={active}
                        onClick={() => {
                          applyGoal(g.key);
                          setStep(2);
                        }}
                        className="chip flex min-h-[44px] w-full cursor-pointer flex-col items-start gap-0.5 px-4 py-3 text-left transition-colors duration-150 hover:border-[var(--accent)]"
                        style={active ? { borderColor: "var(--accent)" } : undefined}
                      >
                        <span
                          className="inline-flex items-center gap-2 text-sm font-bold"
                          style={{ color: active ? "var(--accent)" : "var(--text)" }}
                        >
                          {g.title}
                          {active ? <CheckIcon /> : null}
                        </span>
                        <span
                          className="text-[0.75rem] leading-relaxed"
                          style={{ color: "var(--dim)" }}
                        >
                          {g.blurb}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </>
          )}

          {step === 2 && (
            <>
              <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                Pick a few symbols and SignalDeck starts recording their data, scoring them and
                grading itself. Tap to add — each one goes straight onto your real watchlist.
              </p>

              {showLoginNotice && (
                <p
                  role="status"
                  className="m-0 rounded-lg border px-3 py-2 text-[0.75rem] leading-relaxed"
                  style={{ borderColor: "var(--warn)", color: "var(--warn)" }}
                >
                  Watchlists are per-account — log in first.{" "}
                  <Link
                    href="/login"
                    className="cursor-pointer font-bold underline"
                    style={{ color: "var(--accent)" }}
                  >
                    Sign in
                  </Link>{" "}
                  and come back; nothing was saved.
                </p>
              )}

              {error && (
                <p
                  role="alert"
                  className="m-0 rounded-lg border px-3 py-2 text-[0.75rem]"
                  style={{ borderColor: "var(--bad)", color: "var(--bad)" }}
                >
                  {error}
                </p>
              )}

              <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
                {CURATED.map(({ symbol, market }) => {
                  const key = `${market}:${symbol}`;
                  const isAdded = added.has(key);
                  const isPending = pending === key;
                  return (
                    <li key={key}>
                      <button
                        type="button"
                        onClick={() => addSymbol(symbol, market)}
                        aria-pressed={isAdded}
                        disabled={isPending}
                        className="chip tnum inline-flex min-h-[44px] cursor-pointer items-center gap-2 px-4 text-[0.75rem] font-bold tracking-wide transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-wait"
                        style={
                          isAdded ? { borderColor: "var(--bid)", color: "var(--bid)" } : undefined
                        }
                      >
                        {symbol}
                        <span className="text-[0.75rem] tracking-normal" style={{ color: "var(--faint)" }}>
                          {market}
                        </span>
                        {isAdded ? <CheckIcon /> : null}
                        {isPending ? <span aria-hidden="true">…</span> : null}
                        <span className="sr-only">
                          {isAdded ? "— on your watchlist" : isPending ? "— adding" : "— add to watchlist"}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>

              <form className="flex flex-wrap items-center gap-2" onSubmit={onAddFree}>
                <label htmlFor="welcome-add-sym" className="sr-only">
                  symbol to add
                </label>
                <input
                  id="welcome-add-sym"
                  value={sym}
                  onChange={(e) => {
                    setSym(e.target.value);
                    if (error) setError(null);
                  }}
                  placeholder={mkt === "crypto" ? "BTC/USD" : "AAPL"}
                  spellCheck={false}
                  autoComplete="off"
                  className="tnum min-h-[44px] w-32 min-w-0 flex-1 rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 text-sm uppercase text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
                />
                <select
                  aria-label="market"
                  value={mkt}
                  onChange={(e) => setMkt(e.target.value as Market)}
                  className="min-h-[44px] cursor-pointer rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
                >
                  <option value="stocks">stocks</option>
                  <option value="crypto">crypto</option>
                </select>
                <button
                  type="submit"
                  className="chip inline-flex min-h-[44px] cursor-pointer items-center px-4 text-[0.75rem] font-bold transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
                >
                  Add
                </button>
              </form>

              <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                The daemon backfills history and scores on its own cadence — new symbols show
                &quot;no read yet&quot; until enough evidence exists. That&apos;s honesty, not a bug.
              </p>
            </>
          )}

          {step === 3 && (
            <>
              <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                Honest answer: you don&apos;t configure anything. Alerts fire automatically for
                symbols you watch, from four published rules — the exact thresholds are public:
              </p>

              <ul className="m-0 flex list-none flex-col gap-3 p-0">
                {ALERT_KINDS.map((k) => {
                  const r = ALERT_RULES[k];
                  if (!r) return null;
                  return (
                    <li key={k} className="flex flex-wrap items-baseline gap-2">
                      <span
                        className="chip px-2 py-[2px] text-[0.75rem] font-bold tracking-wider"
                        style={{ color: r.color, borderColor: r.color }}
                      >
                        {r.label}
                      </span>
                      <span className="min-w-0 flex-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                        {r.rule}
                      </span>
                    </li>
                  );
                })}
              </ul>

              <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                Every fired alert lands in your dashboard sidebar and on{" "}
                <Link
                  href="/market/activity"
                  className="cursor-pointer font-bold underline transition-colors duration-150 hover:text-[var(--accent)]"
                  style={{ color: "var(--accent)" }}
                >
                  Signals → Alerts
                </Link>
                , where each kind cites its full methodology. Regime changes are descriptive —
                a label flip, not a forecast.
              </p>
            </>
          )}

          {step === 4 && (
            <>
              <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                Once per day — at or after 7:00am New York time — the briefing worker writes a
                plain-English morning brief grounded only in data SignalDeck actually stored:
                your symbols, the regime, anomalies and filings. It pins itself to the top of the
                dashboard feed for the day and is archived under Signals → Insights.
              </p>
              <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                Nothing in it is a recommendation: every sentence carries its evidence, and the
                platform grades its own predictions against what actually happened on{" "}
                <span className="font-bold">PROOF IT WORKS</span> at the bottom of the dashboard.
              </p>
              <button
                type="button"
                onClick={finish}
                className="inline-flex min-h-[44px] cursor-pointer items-center gap-2 self-start rounded-lg border px-5 text-[0.85rem] font-bold tracking-wide transition-colors duration-150 hover:bg-[var(--accent-dim)]"
                style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
              >
                Go to your dashboard
                <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                  <path
                    d="M3 8h10M9 4l4 4-4 4"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              </button>
            </>
          )}
        </div>

        {/* step controls */}
        <div
          className="flex items-center gap-2 border-t px-5 py-3"
          style={{ borderColor: "var(--border)" }}
        >
          {step > 1 && (
            <button
              type="button"
              onClick={() => setStep((s) => Math.max(1, s - 1))}
              className="chip inline-flex min-h-[44px] cursor-pointer items-center px-4 text-[0.75rem] font-bold transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
            >
              Back
            </button>
          )}
          {step < STEP_TITLES.length && (
            <button
              type="button"
              // STEP_TITLES.length, not a literal 3: step is 1-indexed, so the
              // hardcoded clamp stopped at "How should we alert you?" and made
              // "Your daily briefing" — the only step carrying finish() and
              // markOnboarded() — unreachable. Continue rendered on step 3 and
              // did nothing; the stepper still read "step 3 of 4", and the only
              // way out of onboarding was Skip.
              onClick={() => setStep((s) => Math.min(STEP_TITLES.length, s + 1))}
              className="ml-auto inline-flex min-h-[44px] cursor-pointer items-center rounded-lg border px-5 text-[0.85rem] font-bold tracking-wide transition-colors duration-150 hover:bg-[var(--accent-dim)]"
              style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
            >
              Continue
            </button>
          )}
        </div>
      </section>

      <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        SignalDeck records market data and grades its own predictions against what actually
        happened. Backtested — not live trading. Not financial advice.
      </p>
    </div>
  );
}
