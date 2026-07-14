"use client";

// AI BULL / BEAR DEBATE — the adversarial surface. Two LLM agents are handed the
// SAME grounded digest of a symbol's measured numbers and argue opposite sides;
// a judge then rules on what the DATA actually supports. The judge is bound by
// the house honesty rule — when the forecast lift is <= 0 it must return
// "NEUTRAL / NO EDGE", so a confident-sounding argument can't manufacture an edge
// the numbers don't carry. Everything the agents saw is shown verbatim in the
// grounded-digest disclosure, so the verdict is auditable, not a black box.
//
// On-demand only: api.debate is a single ~45s POST (no polling). The button and
// input disable for the round; a clear "Debating SYM — bull, bear, and judge…"
// status plus a skeleton stand in until the verdict lands.

import { useState } from "react";
import { api, type DebateResult } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import ProOnly from "@/components/ProOnly";

const AMBER = "var(--accent)";
const AMBER_BG = "rgba(251,191,36,.10)";

/** Direction read of the judge's verdict — color, arrow AND the verdict word, so
 *  the call never rides on color alone (a11y). Matches on LONG/SHORT substrings
 *  so the exact daemon wording ("LEAN LONG", "NEUTRAL / NO EDGE") can drift
 *  without breaking the mapping. */
function verdictView(verdict: string): { color: string; arrow: string } {
  const v = verdict.toUpperCase();
  if (v.includes("LONG")) return { color: "var(--ok)", arrow: "▲" };
  if (v.includes("SHORT")) return { color: "var(--bad)", arrow: "▼" };
  return { color: "var(--faint)", arrow: "■" };
}

/** Confidence as a MONOCHROME brightness ramp — deliberately not green/red, so it
 *  can't be misread as a second direction signal (confidence is orthogonal to
 *  which side won). */
function confColor(confidence: string): string {
  const c = confidence.toLowerCase();
  if (c === "high") return "var(--text)";
  if (c === "medium") return "var(--dim)";
  return "var(--faint)";
}

/** Amber pill action button in the app's composer style (see lab/system/ai). */
function ActionButton({
  onClick,
  disabled,
  children,
  label,
}: {
  onClick: () => void;
  disabled: boolean;
  children: React.ReactNode;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      className="min-h-[40px] cursor-pointer rounded-lg border px-5 py-2 text-[0.75rem] font-bold tracking-wide transition-colors duration-150 disabled:cursor-not-allowed"
      style={{
        borderColor: disabled ? "var(--border)" : AMBER,
        background: disabled ? "var(--panel2)" : AMBER_BG,
        color: disabled ? "var(--faint)" : AMBER,
      }}
    >
      {children}
    </button>
  );
}

/** Amber "thinking…" line with an aria-live region for the ~45s LLM round. */
function Thinking({ note }: { note: string }) {
  return (
    <div
      role="status"
      aria-live="polite"
      className="flex items-center gap-2 text-[0.75rem]"
      style={{ color: AMBER }}
    >
      <span
        aria-hidden="true"
        className="inline-block h-2 w-2 animate-pulse rounded-full"
        style={{ background: AMBER, boxShadow: "0 0 8px rgba(251,191,36,.6)" }}
      />
      {note}
    </div>
  );
}

/** One side of the argument — BULL (green) / BEAR (red), subtle accent header. */
function SidePanel({
  side,
  color,
  tint,
  arrow,
  text,
}: {
  side: string;
  color: string;
  tint: string;
  arrow: string;
  text: string;
}) {
  return (
    <section className="panel">
      <div className="panel-h" style={{ color, background: tint, borderColor: color }}>
        <span aria-hidden="true">{arrow}</span>
        {side}
      </div>
      <p
        className="px-4 py-4 text-[0.82rem] leading-relaxed whitespace-pre-wrap"
        style={{ color: "var(--dim)" }}
      >
        {text || "—"}
      </p>
    </section>
  );
}

export default function DebatePage() {
  const [input, setInput] = useState("NVDA");
  const [activeSymbol, setActiveSymbol] = useState<string | null>(null);
  const [result, setResult] = useState<DebateResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = () => {
    const sym = input.trim().toUpperCase();
    if (!sym || loading) return;
    // Clear the prior round so no stale verdict/error lingers under the spinner.
    setActiveSymbol(sym);
    setResult(null);
    setError(null);
    setLoading(true);
    api
      .debate(sym)
      .then((r) => setResult(r))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  };

  const canRun = input.trim().length > 0 && !loading;
  const view = result ? verdictView(result.verdict) : null;
  // Before the first run: nothing requested, nothing failed, nothing returned.
  const idle = !loading && error === null && result === null;

  return (
    <div className="flex flex-col gap-4">
      {/* header */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">BULL / BEAR DEBATE</h1>
        {activeSymbol && <span className="chip mono">{activeSymbol}</span>}
        {result && !result.disabled && result.model && (
          <span className="chip tnum" title="the model that argued both the bull and bear sides">
            agents: {result.model}
          </span>
        )}
        {result && !result.disabled && result.judgeModel && (
          <span className="chip tnum" title="the model that ruled on the two arguments">
            judge: {result.judgeModel}
          </span>
        )}
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="signals-debate"
        text="Two AI agents argue opposite sides of the same measured numbers; a judge rules on what the DATA supports."
      />

      {/* symbol + run */}
      <section className="panel">
        <div className="panel-h">
          STAGE A DEBATE
          <span
            className="text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--faint)" }}
          >
            one symbol, two agents, one judge
          </span>
        </div>
        <div className="flex flex-wrap items-end gap-3 px-4 py-4">
          <label htmlFor="debate-symbol" className="flex flex-col gap-1.5">
            <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
              symbol
            </span>
            <input
              id="debate-symbol"
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  run();
                }
              }}
              disabled={loading}
              placeholder="e.g. NVDA"
              autoCapitalize="characters"
              autoComplete="off"
              spellCheck={false}
              className="mono w-40 rounded-lg border px-3 py-2.5 text-[0.85rem] uppercase disabled:cursor-not-allowed"
              style={{ background: "var(--panel2)", borderColor: "var(--border)", color: "var(--text)" }}
            />
          </label>
          <ActionButton onClick={run} disabled={!canRun} label="Run the bull versus bear debate">
            {loading ? "Debating…" : "Run debate"}
          </ActionButton>
          {loading ? (
            <Thinking note={`Debating ${activeSymbol} — bull, bear, and judge…`} />
          ) : (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              Enter to run · the round takes ~45s
            </span>
          )}
        </div>
      </section>

      {/* before first run */}
      {idle && (
        <EmptyState
          message="Enter a symbol to stage a debate."
          detail="Two agents get the same grounded numbers and argue long vs short; the judge rules on what the data supports."
        />
      )}

      {/* loading — status note above, skeleton stands in for the verdict/panels */}
      {loading && <Skeleton lines={6} label={`debating ${activeSymbol}`} />}

      {/* hard failure (daemon unreachable, timeout, non-2xx) */}
      {error !== null && (
        <ErrorState
          message={error}
          hint="Is the daemon running? Start it with signaldeckd. The debate is a ~45s call, so a timeout can also surface here — retry."
          retry={run}
        />
      )}

      {/* AI layer has no key — the daemon answers with disabled:true */}
      {result && result.disabled && (
        <div className="panel px-4 py-5" style={{ borderColor: "var(--warn)" }}>
          <div className="text-[0.82rem] font-bold tracking-wide" style={{ color: "var(--warn)" }}>
            The debate agents are off
          </div>
          <p className="mt-1.5 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            The AI layer has no key configured, so there is nothing to argue with. Add{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              SIGNALDECK_NVIDIA_KEY
            </span>{" "}
            to{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              daemon/.env
            </span>{" "}
            and restart the daemon to stage debates.
          </p>
        </div>
      )}

      {/* verdict + arguments */}
      {result && !result.disabled && view && (
        <>
          {/* JUDGE'S VERDICT */}
          <section className="panel">
            <div className="panel-h">
              JUDGE&apos;S VERDICT
              <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
                grounded in {result.symbol}&apos;s measured numbers
              </span>
            </div>
            <div className="flex flex-col gap-4 px-4 py-4">
              <div className="flex flex-wrap items-center gap-3">
                <span aria-hidden="true" className="text-2xl leading-none" style={{ color: view.color }}>
                  {view.arrow}
                </span>
                <span className="text-2xl font-bold tracking-wide" style={{ color: view.color }}>
                  {result.verdict}
                </span>
                <span
                  className="chip"
                  style={{ color: confColor(result.confidence), borderColor: "var(--border-strong)" }}
                  title="the judge's stated confidence in this call — a brightness ramp, not a direction"
                >
                  confidence: <span className="font-bold">{result.confidence}</span>
                </span>
              </div>

              {result.cruxes && result.cruxes.length > 0 && (
                <div className="flex flex-col gap-1.5">
                  <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                    DECIDING DATA POINTS
                  </span>
                  <ul
                    className="flex list-disc flex-col gap-1.5 pl-5 text-[0.82rem] leading-relaxed"
                    style={{ color: "var(--dim)" }}
                  >
                    {result.cruxes.map((c, i) => (
                      <li key={i}>{c}</li>
                    ))}
                  </ul>
                </div>
              )}

              {result.rationale && (
                <div className="flex flex-col gap-1.5">
                  <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                    WHY
                  </span>
                  <p
                    className="text-[0.82rem] leading-relaxed whitespace-pre-wrap"
                    style={{ color: "var(--text)" }}
                  >
                    {result.rationale}
                  </p>
                </div>
              )}
            </div>
          </section>

          {/* BULL vs BEAR — side by side, subtle green / red accents */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <SidePanel side="BULL" color="var(--ok)" tint="var(--bid-dim)" arrow="▲" text={result.bull} />
            <SidePanel side="BEAR" color="var(--bad)" tint="var(--ask-dim)" arrow="▼" text={result.bear} />
          </div>

          {/* grounded digest — the identical raw numbers both sides saw */}
          {result.digest && (
            <ProOnly summary="Show the grounded numbers both agents saw">
              <section className="panel">
                <div className="panel-h">
                  GROUNDED DIGEST
                  <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
                    identical input to both agents — nothing else
                  </span>
                </div>
                <pre
                  className="mono overflow-x-auto px-4 py-4 text-[0.75rem] leading-relaxed whitespace-pre-wrap"
                  style={{ color: "var(--dim)" }}
                >
                  {result.digest}
                </pre>
              </section>
            </ProOnly>
          )}
        </>
      )}

      {/* honest caveat — always visible */}
      <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        Not advice. The judge is bound to call &lsquo;no edge&rsquo; when the forecast lift ≤ 0;
        grounded only in SignalDeck&apos;s own measured numbers.
      </p>
    </div>
  );
}
