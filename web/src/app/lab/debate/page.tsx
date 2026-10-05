"use client";

import { useState } from "react";
import { api, type DebateResult } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import ProOnly from "@/components/ProOnly";
import { PageHero, StatTile, Reveal } from "@/components/ui/Kit";

function verdictView(verdict: string): { color: string; arrow: string } {
  const v = verdict.toUpperCase();
  if (v.includes("LONG")) return { color: "var(--bid)", arrow: "▲" };
  if (v.includes("SHORT")) return { color: "var(--ask)", arrow: "▼" };
  return { color: "var(--dim)", arrow: "■" };
}

function confColor(confidence: string): string {
  const c = confidence.toLowerCase();
  if (c === "high") return "var(--text)";
  if (c === "medium") return "var(--dim)";
  return "var(--faint)";
}

// confNumber() lived here and mapped high/medium/low to 80/50/20. It is gone
// rather than unused: both call sites rendered its output as a percentage, and
// a helper that manufactures precision the judge never reported is the kind of
// thing that gets re-adopted by the next tile that needs "a number".

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
        borderColor: disabled ? "var(--border)" : "var(--accent)",
        background: disabled ? "var(--panel2)" : "rgba(251,191,36,.10)",
        color: disabled ? "var(--faint)" : "var(--accent)",
      }}
    >
      {children}
    </button>
  );
}

function Thinking({ note }: { note: string }) {
  return (
    <div
      role="status"
      aria-live="polite"
      className="flex items-center gap-2 text-[0.75rem]"
      style={{ color: "var(--accent)" }}
    >
      <span
        aria-hidden="true"
        className="inline-block h-2 w-2 animate-pulse rounded-full"
        style={{ background: "var(--accent)", boxShadow: "0 0 8px rgba(251,191,36,.6)" }}
      />
      {note}
    </div>
  );
}

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
    <section className="panel reveal-item" style={{ "--i": 4 } as React.CSSProperties}>
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
  const idle = !loading && error === null && result === null;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Bull / Bear Debate"
        subtitle="Two AI agents argue opposite sides of the same measured numbers; a judge rules on what the data supports."
        right={
          result && !result.disabled && (
            <div className="flex gap-2">
              {result.model && (
                <span className="text-[0.75rem] mono" style={{ color: "var(--faint)" }}>
                  agents: {result.model}
                </span>
              )}
              {result.judgeModel && (
                <span className="text-[0.75rem] mono" style={{ color: "var(--faint)" }}>
                  judge: {result.judgeModel}
                </span>
              )}
            </div>
          )
        }
      />

      {result && !result.disabled && view && (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <StatTile
            label="Verdict"
            value={result.verdict}
            glow={view.arrow === "▲" ? "up" : view.arrow === "▼" ? "down" : undefined}
            sub={result.symbol}
            i={0}
          />
          {/* The judge returns a WORD — high/medium/low. The 80/50/20 that used
              to render here with a % sign were invented by the client and read
              as a measured probability. Show what was actually returned. */}
          <StatTile
            label="Confidence"
            value={result.confidence}
            i={1}
          />
          <StatTile
            label="Cruxes"
            value={result.cruxes?.length ?? 0}
            decimals={0}
            sub="deciding data points"
            i={2}
          />
          <StatTile
            label="Status"
            value={loading ? "Debating…" : activeSymbol ? "Complete" : "Idle"}
            sub={activeSymbol ?? undefined}
            i={3}
          />
        </div>
      )}

      <section className="panel reveal-item" style={{ "--i": 0 } as React.CSSProperties}>
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

      {idle && (
        <EmptyState
          message="Enter a symbol to stage a debate."
          detail="Two agents get the same grounded numbers and argue long vs short; the judge rules on what the data supports."
        />
      )}

      {loading && <Skeleton lines={6} label={`debating ${activeSymbol}`} />}

      {error !== null && (
        <ErrorState
          message={error}
          hint="Is the daemon running? Start it with signaldeckd. The debate is a ~45s call, so a timeout can also surface here — retry."
          retry={run}
        />
      )}

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

      {result && !result.disabled && view && (
        <Reveal className="space-y-4">
          <section className="hud-panel reveal-item" style={{ "--i": 0 } as React.CSSProperties}>
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
                <span className="text-2xl font-bold tracking-wide tnum" style={{ color: view.color }}>
                  {result.verdict}
                </span>
                <span
                  className="rounded-full border px-2 py-0.5 text-[0.75rem] tnum"
                  style={{ color: confColor(result.confidence), borderColor: "var(--border)" }}
                  title="the judge's stated confidence in this call"
                >
                  confidence: <span className="font-bold">{result.confidence}</span>
                </span>
              </div>

              <div className="flex items-center justify-between">
                {/* Was a <Gauge value={confNumber(...)} min={0} max={100} />.
                    The judge returns the WORD high/medium/low; 80/50/20 were
                    invented here, and Kit's Gauge prints its value in the dial,
                    so the page showed "80 / CONFIDENCE" — an unmeasured number
                    with two significant figures. The word is the whole finding. */}
                <div
                  className="flex flex-col items-center justify-center"
                  style={{ width: 120, height: 120 }}
                >
                  <span className="num-hero text-xl uppercase" style={{ color: view.color }}>
                    {result.confidence}
                  </span>
                  <span className="text-[0.75rem] uppercase tracking-wider" style={{ color: "var(--dim)" }}>
                    Confidence
                  </span>
                </div>
                {result.cruxes && result.cruxes.length > 0 && (
                  <div className="flex flex-col gap-1.5 flex-1 ml-4">
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
              </div>

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

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <SidePanel side="BULL" color="var(--bid)" tint="rgba(52,211,153,.08)" arrow="▲" text={result.bull} />
            <SidePanel side="BEAR" color="var(--ask)" tint="rgba(248,113,113,.08)" arrow="▼" text={result.bear} />
          </div>

          {result.digest && (
            <ProOnly summary="Show the grounded numbers both agents saw">
              <section className="panel reveal-item" style={{ "--i": 5 } as React.CSSProperties}>
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
        </Reveal>
      )}

      <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        Not advice. The judge is bound to call &lsquo;no edge&rsquo; when the forecast lift ≤ 0;
        grounded only in SignalDeck&apos;s own measured numbers.
      </p>
    </div>
  );
}
