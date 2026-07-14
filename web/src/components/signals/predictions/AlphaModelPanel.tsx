"use client";

// ALPHA MODEL — the pooled cross-sectional model (/api/alphax), fleet-level
// like the leaderboard below it: one model trained across the whole universe,
// graded on purged walk-forward OOS splits, predicting RELATIVE
// outperformance vs the same-day universe median — never absolute direction.
// Honesty framing is non-negotiable: a gated horizon renders its stated
// gateReason verbatim and never a score; scoresNote + note render verbatim
// under the score grid. Per-horizon tabs appear only when BOTH horizons have
// a graded model — otherwise the missing horizon's refusal line shows small.

import Link from "next/link";
import { useEffect, useState } from "react";
import { alphaX, pollMs, POLL_SLOW, type AlphaX, type AlphaXHorizon } from "@/lib/api";
import { useViewMode } from "@/components/Plain";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

const AX_HORIZONS = ["1d", "1w"] as const;
type AxHorizon = (typeof AX_HORIZONS)[number];

/** Loaded payload with ages pre-rendered at fetch time (render stays pure). */
interface AxSlot {
  data: AlphaX;
  gradedAgo: Partial<Record<AxHorizon, string>>;
}

/** Age string computed once at fetch time — no Date.now() in render. */
function agoAtFetch(ts?: number): string | undefined {
  if (!ts) return undefined;
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts));
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** Alpha scores carry no market — infer it from the symbol shape (pairs like
 *  BTC/USD contain "/" → crypto), same as the insights feed. */
function inferMarket(symbol: string): "crypto" | "stocks" {
  return symbol.includes("/") ? "crypto" : "stocks";
}

const fmtPp = (f: number) => `${f >= 0 ? "+" : ""}${(f * 100).toFixed(1)}pp`;
const fmtPct1 = (f: number) => `${(f * 100).toFixed(1)}%`;

/** The OOS grade card: lift headline + the grade chips. */
function GradeCard({ h, gradedAgo }: { h: AlphaXHorizon; gradedAgo?: string }) {
  const g = h.grade;
  if (!g) return null;
  const liftColor = g.lift > 0 ? "var(--bid)" : g.lift < 0 ? "var(--ask)" : "var(--dim)";
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
      <span
        className="tnum text-lg font-extrabold"
        style={{ color: liftColor }}
        title="measured OOS accuracy minus the base rate — the model's whole claim to an edge"
      >
        {fmtPp(g.lift)} <span className="text-[0.75rem] font-normal">vs base</span>
      </span>
      <div className="flex flex-wrap items-center gap-2">
        <span className="chip tnum" title="area under the ROC curve on the OOS test rows">
          AUC {g.auc.toFixed(2)}
        </span>
        <span
          className="chip tnum"
          title="OOS accuracy vs the naive always-majority base rate"
        >
          {fmtPct1(g.accuracy)} acc vs {fmtPct1(g.baseRate)} base
        </span>
        {g.brier !== undefined && (
          <span className="chip tnum" title="Brier score on the OOS test rows (lower is better)">
            brier {g.brier.toFixed(3)}
          </span>
        )}
        <span className="chip tnum" title="pooled train rows / out-of-sample test rows">
          train {g.nTrain} · test {g.nTest}
        </span>
        <span
          className="chip"
          style={{ color: "var(--dim)" }}
          title="graded on time-ordered splits with a purge gap between train and test — no lookahead leakage"
        >
          purged walk-forward OOS
        </span>
        {gradedAgo && (
          <span className="chip tnum" style={{ color: "var(--faint)" }}>
            graded {gradedAgo}
          </span>
        )}
      </div>
    </div>
  );
}

/** One horizon's body: grade + gate + (only while ungated) the score grid. */
function HorizonBody({ h, gradedAgo }: { h: AlphaXHorizon; gradedAgo?: string }) {
  const scores = h.topScores ?? [];
  const maxProb = scores.reduce((m, s) => Math.max(m, s.prob), 0) || 1;
  return (
    <div className="flex flex-col">
      <GradeCard h={h} gradedAgo={gradedAgo} />

      {/* gate state — the stated reason, verbatim, never softened */}
      {(h.gated || !h.available) && (
        <p
          className="px-4 pb-3 text-[0.75rem] leading-relaxed"
          style={{ color: "var(--warn)" }}
        >
          gated — {h.gateReason ?? "no reason stated"}
        </p>
      )}

      {/* top-20 current scores — only ever present while ungated */}
      {!h.gated && h.available && scores.length > 0 && (
        <>
          <div className="grid grid-cols-1 gap-x-4 gap-y-1.5 px-4 pb-2 sm:grid-cols-2">
            {scores.map((s) => (
              <div key={s.symbol} className="flex items-center gap-2">
                <Link
                  href={`/s/${inferMarket(s.symbol)}/${encodeURIComponent(s.symbol)}`}
                  className="chip mono w-24 cursor-pointer justify-center font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                  title={`open the ${s.symbol} symbol page`}
                >
                  {s.symbol}
                </Link>
                <div
                  className="h-2 flex-1 overflow-hidden rounded-sm"
                  style={{ background: "var(--panel2)" }}
                  role="img"
                  aria-label={`P(top half) ${fmtPct1(s.prob)}`}
                >
                  <div
                    className="h-full"
                    style={{
                      width: `${Math.max(2, (s.prob / maxProb) * 100)}%`,
                      background: "var(--bid)",
                      opacity: 0.7,
                    }}
                  />
                </div>
                <span className="tnum w-12 text-right text-[0.75rem]" style={{ color: "var(--dim)" }}>
                  {fmtPct1(s.prob)}
                </span>
              </div>
            ))}
          </div>
          {h.scoresNote && (
            <p className="px-4 pb-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              {h.scoresNote}
            </p>
          )}
        </>
      )}
    </div>
  );
}

export default function AlphaModelPanel() {
  const [slot, setSlot] = useState<AxSlot | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const [tab, setTab] = useState<AxHorizon>("1d");
  const mode = useViewMode();

  useEffect(() => {
    let alive = true;
    const load = () =>
      alphaX()
        .then((d) => {
          if (!alive) return;
          setSlot({
            data: d,
            gradedAgo: {
              "1d": agoAtFetch(d.horizons["1d"]?.ts),
              "1w": agoAtFetch(d.horizons["1w"]?.ts),
            },
          });
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // POLL_SLOW tier — the alpha-trainer regrades on worker cadence, not live.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const loading = slot === null && err === null;
  const hardError = slot === null && err !== null;

  const data = slot?.data ?? null;
  const h1d = data?.horizons["1d"];
  const h1w = data?.horizons["1w"];
  const bothAvailable = !!h1d?.available && !!h1w?.available;
  // Tabs only when both horizons carry a graded model; otherwise pin to the
  // one that does (derived, not corrected in an effect).
  const shown: AxHorizon = bothAvailable ? tab : h1d?.available ? "1d" : h1w?.available ? "1w" : "1d";
  const shownH = shown === "1d" ? h1d : h1w;
  const otherKey: AxHorizon = shown === "1d" ? "1w" : "1d";
  const otherH = shown === "1d" ? h1w : h1d;

  return (
    <section className="panel">
      <div className="panel-h">
        {mode === "simple"
          ? "ALPHA MODEL — which stocks should beat the pack"
          : "CROSS-SECTIONAL ALPHA MODEL"}
        {bothAvailable && (
          <div className="ml-auto flex gap-2" role="group" aria-label="alpha model horizon">
            {AX_HORIZONS.map((h) => (
              <button
                key={h}
                type="button"
                onClick={() => setTab(h)}
                aria-pressed={shown === h}
                className="chip tnum cursor-pointer transition-colors duration-150 hover:brightness-125"
                style={{
                  color: shown === h ? "var(--text)" : "var(--dim)",
                  borderColor: shown === h ? "var(--accent)" : "var(--border)",
                }}
              >
                {h}
              </button>
            ))}
          </div>
        )}
      </div>

      {loading && <Skeleton lines={3} label="loading the alpha model" className="m-4" />}

      {hardError && (
        <ErrorState
          className="m-4"
          message={err ?? "alpha model unavailable"}
          hint="Is the daemon running? Start it with signaldeckd."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {data !== null && (
        <>
          {shownH ? (
            <HorizonBody h={shownH} gradedAgo={slot?.gradedAgo[shown]} />
          ) : (
            <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
              no {shown} model status in the payload yet
            </p>
          )}

          {/* the other horizon's honest refusal line — small, never hidden */}
          {!bothAvailable && otherH && !otherH.available && (
            <p className="px-4 pb-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {otherKey}: {otherH.gateReason ?? "no graded model yet"}
            </p>
          )}

          {/* API note — verbatim, the whole honesty contract in one line */}
          <p
            className="px-4 pb-3 text-[0.75rem] leading-relaxed"
            style={{ color: "var(--faint)", borderTop: "1px solid var(--border)", paddingTop: "0.5rem" }}
          >
            {data.note}
          </p>
        </>
      )}
    </section>
  );
}
