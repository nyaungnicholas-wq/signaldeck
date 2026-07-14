"use client";

import { useEffect, useRef, useState } from "react";
import {
  api,
  pollMs,
  POLL_FAST,
  type AIStatus,
  type AnalystBrief,
  type ChatAnswer,
  type FilingResult,
} from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";

const AMBER = "var(--accent)";
const AMBER_BG = "rgba(251,191,36,.10)";

/** The honesty line repeated across the app — read-only, grounded, not advice. */
const HONESTY =
  "Every AI answer is grounded in SignalDeck's stored data; the agents are read-only and cite their numbers. Not financial advice.";

/** Amber pill button in the app's composer style. Disabled → faint + border. */
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

/** A small amber "thinking…" line with an aria-live region for awaiting LLM calls. */
function Thinking({ note }: { note?: string }) {
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
      {note ?? "thinking…"}
    </div>
  );
}

/** Inline soft error (daily cap, disabled, parse) in --bad. */
function InlineError({ msg }: { msg: string }) {
  return (
    <div
      className="rounded-lg border px-3 py-2.5 text-[0.75rem] leading-relaxed whitespace-pre-wrap"
      style={{
        color: "var(--bad)",
        borderColor: "var(--bad)",
        background: "rgba(248,113,113,.06)",
      }}
    >
      {msg}
    </div>
  );
}

/* ─────────────────────────── HEADER ─────────────────────────── */

function StatusHeader({ status }: { status: AIStatus | null }) {
  const enabled = status?.enabled === true;
  const stats = status?.stats;
  const calls = stats?.calls ?? 0;
  const cap = stats?.dailyCap ?? 0;
  const nearCap = cap > 0 && calls > cap * 0.8;

  return (
    <div className="flex flex-wrap items-center gap-2 px-1">
      <h1 className="text-sm font-bold tracking-[0.18em]">AI AGENTS</h1>
      {status === null ? (
        <span className="chip" style={{ color: "var(--faint)" }}>
          loading…
        </span>
      ) : (
        <>
          <span
            className="chip flex items-center gap-1.5"
            style={{
              color: enabled ? "var(--ok)" : "var(--bad)",
              borderColor: enabled ? "var(--ok)" : "var(--bad)",
            }}
          >
            <span
              aria-hidden="true"
              className="inline-block h-2 w-2 rounded-full"
              style={{
                background: enabled ? "var(--ok)" : "var(--bad)",
                boxShadow: enabled ? "0 0 8px rgba(52,211,153,.7)" : undefined,
              }}
            />
            {enabled ? "enabled" : "disabled"}
          </span>
          {status.model && (
            <span className="chip tnum" title="active model">
              {status.model}
            </span>
          )}
          {cap > 0 && (
            <span
              className="chip tnum"
              aria-label={`AI calls today ${calls} of cap ${cap}`}
              style={{
                color: nearCap ? "var(--warn)" : undefined,
                borderColor: nearCap ? "var(--warn)" : undefined,
              }}
            >
              calls today: {calls} / cap {cap}
            </span>
          )}
          {stats && stats.lastCallTs > 0 && (
            <span className="chip tnum" style={{ color: "var(--faint)" }}>
              last {ago(stats.lastCallTs)}
            </span>
          )}
          {stats?.lastError ? (
            <span
              className="chip"
              style={{ color: "var(--bad)", borderColor: "var(--bad)" }}
            >
              last error: {stats.lastError.slice(0, 48)}
            </span>
          ) : null}
        </>
      )}
    </div>
  );
}

/* ─────────────────────────── ANALYST ─────────────────────────── */

function AnalystPanel({ enabled }: { enabled: boolean }) {
  const [brief, setBrief] = useState<AnalystBrief | null>(null);
  const [running, setRunning] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const generate = () => {
    if (running || !enabled) return;
    setRunning(true);
    setErr(null);
    api
      .aiAnalyst()
      .then((b) => setBrief(b))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setRunning(false));
  };

  const perSymbol = brief?.perSymbol ? Object.entries(brief.perSymbol) : [];
  const softDisabled = brief?.disabled === true;
  const softError = brief?.error;

  return (
    <section className="panel">
      <div className="panel-h">
        MARKET ANALYST
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          live LLM read of the stored tape
        </span>
      </div>
      <div className="flex flex-col gap-4 px-4 py-4">
        <div className="flex flex-wrap items-center gap-3">
          <ActionButton
            onClick={generate}
            disabled={running || !enabled}
            label="Generate market brief"
          >
            {running ? "thinking…" : "Generate brief"}
          </ActionButton>
          {running && <Thinking note="the analyst is reading the tape…" />}
          {!enabled && (
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              AI is off — enable it to generate a brief.
            </span>
          )}
        </div>

        {err && <InlineError msg={err} />}
        {softError && <InlineError msg={softError} />}
        {softDisabled && !softError && (
          <InlineError msg="the analyst is disabled on the daemon." />
        )}

        {brief && !softDisabled && !softError && (
          <div className="flex flex-col gap-4">
            {brief.market && (
              <div className="flex flex-col gap-1.5">
                <span
                  className="text-[0.75rem] tracking-wide"
                  style={{ color: "var(--faint)" }}
                >
                  MARKET
                </span>
                <p
                  className="text-[0.85rem] leading-relaxed whitespace-pre-wrap"
                  style={{ color: "var(--text)" }}
                >
                  {brief.market}
                </p>
              </div>
            )}

            {perSymbol.length > 0 && (
              <div className="flex flex-col gap-1.5">
                <span
                  className="text-[0.75rem] tracking-wide"
                  style={{ color: "var(--faint)" }}
                >
                  PER SYMBOL
                </span>
                <ul className="flex flex-col gap-2.5">
                  {perSymbol.map(([sym, note]) => (
                    <li key={sym} className="flex flex-col gap-1 lg:flex-row lg:gap-3">
                      <span className="chip tnum shrink-0 self-start">{sym}</span>
                      <span
                        className="text-[0.75rem] leading-relaxed"
                        style={{ color: "var(--dim)" }}
                      >
                        {note}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {brief.model && (
              <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                model: {brief.model}
              </span>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

/* ─────────────────────────── CHAT ─────────────────────────── */

type Turn = { q: string; a?: string; model?: string; err?: string };

function ChatPanel({ enabled }: { enabled: boolean }) {
  const [turns, setTurns] = useState<Turn[]>([]);
  const [input, setInput] = useState("");
  const [awaiting, setAwaiting] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [turns, awaiting]);

  const ask = () => {
    const q = input.trim();
    if (!q || awaiting || !enabled) return;
    const idx = turns.length;
    setTurns((t) => [...t, { q }]);
    setInput("");
    setAwaiting(true);
    api
      .aiChat(q)
      .then((res: ChatAnswer) => {
        setTurns((t) => {
          const next = [...t];
          if (res.error || res.disabled) {
            next[idx] = {
              ...next[idx],
              err: res.error ?? "AI is disabled on the daemon.",
            };
          } else {
            next[idx] = { ...next[idx], a: res.text ?? "(no answer)", model: res.model };
          }
          return next;
        });
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        setTurns((t) => {
          const next = [...t];
          next[idx] = { ...next[idx], err: msg };
          return next;
        });
      })
      .finally(() => setAwaiting(false));
  };

  const canAsk = input.trim().length > 0 && !awaiting && enabled;

  return (
    <section className="panel">
      <div className="panel-h">
        CHAT
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          ask about the stored data
        </span>
      </div>
      <div className="flex flex-col gap-3 px-4 py-4">
        {turns.length > 0 && (
          <div
            ref={scrollRef}
            className="flex max-h-[360px] flex-col gap-3 overflow-y-auto"
            role="log"
            aria-label="conversation"
          >
            {turns.map((t, i) => (
              <div key={i} className="flex flex-col gap-1.5">
                <div className="flex items-start gap-2">
                  <span className="chip shrink-0 self-start">you</span>
                  <p
                    className="text-[0.82rem] leading-relaxed"
                    style={{ color: "var(--text)" }}
                  >
                    {t.q}
                  </p>
                </div>
                {t.err ? (
                  <div className="flex items-start gap-2">
                    <span
                      className="chip shrink-0 self-start"
                      style={{ borderColor: "var(--bad)", color: "var(--bad)" }}
                    >
                      error
                    </span>
                    <p
                      className="text-[0.75rem] leading-relaxed whitespace-pre-wrap"
                      style={{ color: "var(--bad)" }}
                    >
                      {t.err}
                    </p>
                  </div>
                ) : t.a !== undefined ? (
                  <div className="flex items-start gap-2">
                    <span
                      className="chip shrink-0 self-start"
                      style={{ borderColor: AMBER, color: AMBER }}
                    >
                      ai
                    </span>
                    <div className="flex flex-col gap-1">
                      <p
                        className="text-[0.82rem] leading-relaxed whitespace-pre-wrap"
                        style={{ color: "var(--dim)" }}
                      >
                        {t.a}
                      </p>
                      {t.model && (
                        <span
                          className="tnum text-[0.75rem]"
                          style={{ color: "var(--faint)" }}
                        >
                          {t.model}
                        </span>
                      )}
                    </div>
                  </div>
                ) : (
                  <div className="pl-1">
                    <Thinking />
                  </div>
                )}
              </div>
            ))}
          </div>
        )}

        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-1 flex-col gap-1.5" style={{ minWidth: 220 }}>
            <span
              className="text-[0.75rem] tracking-wide"
              style={{ color: "var(--faint)" }}
            >
              your question
            </span>
            <input
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  ask();
                }
              }}
              disabled={awaiting || !enabled}
              placeholder="e.g. which symbol has the strongest 1d buy pressure?"
              aria-label="chat question"
              className="w-full rounded-lg border px-3 py-2.5 text-[0.85rem] disabled:cursor-not-allowed"
              style={{
                background: "var(--panel2)",
                borderColor: "var(--border)",
                color: "var(--text)",
              }}
            />
          </label>
          <ActionButton onClick={ask} disabled={!canAsk} label="Ask the chat agent">
            {awaiting ? "asking…" : "Ask"}
          </ActionButton>
        </div>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          Enter to send{!enabled && " · AI is off"}
        </span>
      </div>
    </section>
  );
}

/* ─────────────────────────── FILINGMIND ─────────────────────────── */

function FilingSubPanel({
  label,
  color,
  body,
}: {
  label: string;
  color: string;
  body: string;
}) {
  return (
    <div
      className="flex flex-col"
      style={{ border: "1px solid var(--border)", borderRadius: 8, overflow: "hidden" }}
    >
      <div
        className="px-3 py-2 text-[0.75rem] font-bold tracking-[0.14em]"
        style={{ color, borderBottom: "1px solid var(--border)", background: "var(--panel2)" }}
      >
        {label}
      </div>
      <p
        className="px-3 py-3 text-[0.75rem] leading-relaxed whitespace-pre-wrap"
        style={{ color: "var(--dim)" }}
      >
        {body || "—"}
      </p>
    </div>
  );
}

function FilingPanel({ enabled }: { enabled: boolean }) {
  const [text, setText] = useState("");
  const [result, setResult] = useState<FilingResult | null>(null);
  const [running, setRunning] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const analyze = () => {
    const t = text.trim();
    if (!t || running || !enabled) return;
    setRunning(true);
    setErr(null);
    api
      .aiFiling(t)
      .then((r) => setResult(r))
      .catch((e: unknown) => {
        setErr(e instanceof Error ? e.message : String(e));
        setResult(null);
      })
      .finally(() => setRunning(false));
  };

  const canAnalyze = text.trim().length > 0 && !running && enabled;
  const softDisabled = result?.Disabled === true;
  const softError = result?.error;

  return (
    <section className="panel">
      <div className="panel-h">
        FILINGMIND
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          bull / bear / red flags from a filing
        </span>
      </div>
      <div className="flex flex-col gap-4 px-4 py-4">
        <label className="flex flex-col gap-1.5">
          <span
            className="text-[0.75rem] tracking-wide"
            style={{ color: "var(--faint)" }}
          >
            filing text
          </span>
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter") analyze();
            }}
            rows={7}
            disabled={!enabled}
            placeholder="Paste a company's 10-K / 10-Q / earnings text…"
            aria-label="filing text to analyze"
            className="w-full resize-y rounded-lg border px-3 py-2.5 text-[0.82rem] leading-relaxed disabled:cursor-not-allowed"
            style={{
              background: "var(--panel2)",
              borderColor: "var(--border)",
              color: "var(--text)",
            }}
          />
        </label>

        <div className="flex flex-wrap items-center gap-3">
          <ActionButton onClick={analyze} disabled={!canAnalyze} label="Analyze filing">
            {running ? "analyzing…" : "Analyze"}
          </ActionButton>
          {running && <Thinking note="reading the filing…" />}
          <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
            ⌘/Ctrl+Enter to analyze
            {!enabled && " · AI is off"}
          </span>
        </div>

        {err && <InlineError msg={err} />}
        {softError && <InlineError msg={softError} />}
        {softDisabled && !softError && (
          <InlineError msg="FilingMind is disabled on the daemon." />
        )}

        {result && !softDisabled && !softError && (
          <div className="flex flex-col gap-3">
            {result.Truncated && (
              <span
                className="chip self-start"
                style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
              >
                input was truncated — analysis covers the leading text only
              </span>
            )}
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
              <FilingSubPanel label="BULL" color="var(--ok)" body={result.Bull ?? ""} />
              <FilingSubPanel label="BEAR" color="var(--bad)" body={result.Bear ?? ""} />
              <FilingSubPanel
                label="RED FLAGS"
                color="var(--warn)"
                body={result.RedFlags ?? ""}
              />
            </div>
            {result.Model && (
              <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
                model: {result.Model}
              </span>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

/* ─────────────────────────── CHARTERS ─────────────────────────── */

function ChartersPanel({ charters }: { charters: Record<string, string> | undefined }) {
  const entries = charters ? Object.entries(charters) : [];
  return (
    <section className="panel">
      <div className="panel-h">
        AGENT CHARTERS
        <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
          each agent&apos;s operating rules — nothing hidden
        </span>
      </div>
      <div className="px-4 py-4">
        {entries.length === 0 ? (
          charters === undefined ? (
            <Skeleton lines={2} label="loading charters" className="border-0 p-0" />
          ) : (
            <EmptyState
              className="border-0 p-0"
              message="No charters published by the daemon"
              detail="Each agent's operating rules will appear here once the daemon exposes them."
            />
          )
        ) : (
          <div className="flex flex-col gap-2">
            {entries.map(([name, rule]) => (
              <details
                key={name}
                className="rounded-lg border"
                style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
              >
                <summary
                  className="cursor-pointer px-3 py-2.5 text-[0.75rem] font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
                  style={{ color: "var(--text)" }}
                >
                  {name}
                </summary>
                <p
                  className="px-3 pb-3 text-[0.75rem] leading-relaxed whitespace-pre-wrap"
                  style={{ color: "var(--dim)" }}
                >
                  {rule}
                </p>
              </details>
            ))}
          </div>
        )}
      </div>
    </section>
  );
}

/* ─────────────────────────── PAGE ─────────────────────────── */

export default function AIPage() {
  const [status, setStatus] = useState<AIStatus | null>(null);
  const [statusErr, setStatusErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .aiStatus()
        .then((s) => {
          if (!alive) return;
          setStatus(s);
          setStatusErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setStatusErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // Call counts / cap move only when someone uses the agents — fast tier
    // keeps the "calls today" chip honest without tick-rate polling.
    const stop = pollMs(load, POLL_FAST);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const enabled = status?.enabled === true;

  return (
    <div className="flex flex-col gap-4">
      <StatusHeader status={status} />

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-system-ai"
        text="What can the built-in AI do with your stored data? Its status, hard spend cap and chat surface — it answers from recorded data, not the open internet."
      />

      {/* honesty framing — always visible */}
      <section className="panel">
        <div className="panel-h">HOW THESE AGENTS STAY HONEST</div>
        <p
          className="px-4 py-3 text-[0.75rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          {HONESTY}
        </p>
      </section>

      {/* status loading */}
      {status === null && statusErr === null && (
        <Skeleton lines={3} label="loading AI status" />
      )}

      {/* hard error — daemon unreachable */}
      {status === null && statusErr !== null && (
        <ErrorState
          message={statusErr}
          retry={() => {
            setStatusErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {/* AI off — prominent, but sections still render (disabled) */}
      {status !== null && !enabled && (
        <div
          className="panel px-4 py-5"
          style={{ borderColor: "var(--warn)" }}
        >
          <div
            className="text-[0.82rem] font-bold tracking-wide"
            style={{ color: "var(--warn)" }}
          >
            AI is off
          </div>
          <p
            className="mt-1.5 text-[0.75rem] leading-relaxed"
            style={{ color: "var(--dim)" }}
          >
            Add{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              SIGNALDECK_NVIDIA_KEY
            </span>{" "}
            to{" "}
            <span className="tnum" style={{ color: "var(--text)" }}>
              daemon/.env
            </span>{" "}
            and restart the daemon. The agents below stay visible so you can see
            what they do.
          </p>
        </div>
      )}

      <AnalystPanel enabled={enabled} />
      <ChatPanel enabled={enabled} />
      <FilingPanel enabled={enabled} />
      <ChartersPanel charters={status?.charters} />
    </div>
  );
}
