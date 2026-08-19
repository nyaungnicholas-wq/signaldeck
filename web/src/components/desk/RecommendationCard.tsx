"use client";

// RECOMMENDATION CARD — the research desk's flagship. One symbol's explainable
// verdict, rendered top-to-bottom exactly as stored: the DECISION, the measured
// CONFIDENCE, a FAIR VALUE (or an honest "n/a" with its reason), the current
// price, the modeled EXPECTED RETURN, the KEY DRIVERS and RISKS in plain
// English, and a Bull/Base/Bear PROBABILITY DISTRIBUTION grounded in analogous
// historical setups. available:false renders the API's reason, never a
// fabricated call. The disclaimer always rides the card — this is research, not
// advice. The multi-agent panel (agents) and the audit trail (audit) travel in
// the same payload but render in their own sibling components.

import { ago, fmtPrice } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

// ── shapes (local; the desk does not import from lib/api) — exported so the
// page and the sibling panels reuse one definition instead of drifting copies ──
export interface FairValue {
  available: boolean;
  value: number;
  method: string;
  note: string;
}
export interface DistOutcome {
  prob: number;
  ret: number;
}
export interface Distribution {
  available: boolean;
  bull: DistOutcome;
  base: DistOutcome;
  bear: DistOutcome;
  source: string;
}
export interface RecoAgent {
  role: string;
  stance: "bullish" | "bearish" | "neutral" | string;
  view: string;
}
export interface RecoAudit {
  seq: number;
  hash: string;
  intact: boolean;
  sources: string[];
  modelVersions: Record<string, string>;
  assumptions: string[];
  timestamp: number;
  reproducible: boolean;
}
export interface RecoAvailable {
  available: true;
  symbol: string;
  market: string;
  asOf: number;
  decision: string;
  confidenceLabel: string;
  band: "low" | "moderate" | "high";
  measuredAccuracyPct: number;
  fairValue: FairValue;
  hasPrice: boolean;
  currentPrice: number;
  hasExpectedReturn: boolean;
  expectedReturnPct: number;
  keyDrivers: string[];
  risks: string[];
  distribution: Distribution;
  agents: RecoAgent[];
  assumptions: string[];
  disclaimer: string;
  audit: RecoAudit;
}
export interface RecoUnavailable {
  available: false;
  symbol: string;
  market: string;
  reason: string;
}
export type Recommendation = RecoAvailable | RecoUnavailable;

// ── local helpers ──
const clamp = (n: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, n));
/** Distribution prob/ret arrive from the API ALREADY as percentages (prob is
 *  0–100 and sums to 100; ret is already ×100) — render them directly, never
 *  multiply by 100 again. */
const num = (x: number) => (Number.isFinite(x) ? x : 0);

/** Decision → semantic color: buy-side green, sell-side red, watch amber,
 *  hold/unknown neutral. */
function decisionColor(d: string): string {
  const s = (d || "").toLowerCase();
  if (s === "buy" || s === "accumulate") return "var(--ok)";
  if (s === "reduce" || s === "avoid") return "var(--bad)";
  if (s === "watch") return "var(--warn)";
  return "var(--dim)";
}

/** Confidence band → color: high=green, low=amber, moderate=neutral. */
function bandColor(band?: string): string {
  if (band === "high") return "var(--ok)";
  if (band === "low") return "var(--warn)";
  return "var(--dim)";
}

function Label({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-[0.75rem] font-medium tracking-wide" style={{ color: "var(--faint)" }}>
      {children}
    </span>
  );
}

/** A framed stat cell (fair value / current price / expected return). */
function Tile({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div
      className="flex flex-col gap-1 rounded-lg px-3 py-2.5"
      style={{ background: "var(--panel2)", border: "1px solid var(--border)" }}
    >
      <Label>{label}</Label>
      {children}
    </div>
  );
}

/** One Bull/Base/Bear row: a probability-width bar plus prob% and return%. */
function DistRow({ label, o, color }: { label: string; o?: DistOutcome; color: string }) {
  // The parent's `available` flag does NOT imply these two figures were
  // computed — the same trap ProofStrip documents, where `?? 0` rendered a
  // missing return as "0.00%" and invented a flat-performance claim from
  // absence. It is worse on this row: an absent probability rendered as "0%"
  // reads as the model RULING OUT that scenario, which is the opposite of "not
  // measured". `num()` maps a non-finite input to 0, so the rawness has to be
  // tested BEFORE it is coerced. Absent reads as an em-dash, matching the Gauge
  // contract (`hasData ? fmt(value) : "—"`).
  const hasProb = Number.isFinite(o?.prob as number);
  const hasRet = Number.isFinite(o?.ret as number);
  const prob = hasProb ? clamp(num(o!.prob), 0, 100) : 0;
  const ret = hasRet ? num(o!.ret) : 0;
  return (
    <div className="flex items-center gap-2 text-[0.75rem]">
      <span className="w-10 shrink-0" style={{ color: "var(--dim)" }}>
        {label}
      </span>
      <div className="relative h-3 flex-1 overflow-hidden rounded" style={{ background: "var(--panel2)" }}>
        <div
          className="absolute inset-y-0 left-0 rounded transition-[width] duration-300"
          style={{ width: `${prob}%`, background: color, opacity: 0.85 }}
        />
      </div>
      <span className="tnum w-24 shrink-0 text-right" style={{ color: "var(--faint)" }}>
        {hasProb ? `${prob.toFixed(0)}%` : "—"} ·{" "}
        {hasRet ? `${ret >= 0 ? "+" : ""}${ret.toFixed(1)}%` : "—"}
      </span>
    </div>
  );
}

function BulletList({ items, empty }: { items: string[]; empty: string }) {
  const list = Array.isArray(items) ? items : [];
  if (list.length === 0) {
    return (
      <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {empty}
      </p>
    );
  }
  return (
    <ul className="flex flex-col gap-1">
      {list.map((x, i) => (
        <li key={i} className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          · {x}
        </li>
      ))}
    </ul>
  );
}

export default function RecommendationCard({
  symbol,
  market,
  data,
  err,
  retry,
}: {
  symbol: string;
  market: string;
  data: Recommendation | null;
  err: string | null;
  retry: () => void;
}) {
  if (err && data === null) {
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? Start signaldeckd and the recommendation recovers on its own."
        retry={retry}
      />
    );
  }

  if (data === null) {
    return <Skeleton lines={6} label={`loading recommendation for ${symbol} (${market})`} />;
  }

  // honest miss: no verdict for this symbol yet — the API's reason IS the content
  if (!data.available) {
    return (
      <section className="panel">
        <div className="panel-h flex-wrap gap-2">
          RECOMMENDATION
          <span className="chip tnum">
            {data.symbol} <span style={{ color: "var(--faint)" }}>{data.market}</span>
          </span>
        </div>
        <div className="px-4 py-4">
          <p className="text-[0.9rem] font-bold" style={{ color: "var(--warn)" }}>
            no recommendation for {data.symbol} yet
          </p>
          <p className="mt-1.5 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            {data.reason}
          </p>
        </div>
      </section>
    );
  }

  const d = data;
  const accuracy = Number.isFinite(d.measuredAccuracyPct) ? `${d.measuredAccuracyPct.toFixed(0)}%` : "—";

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        RECOMMENDATION
        <span className="chip tnum">
          {d.symbol} <span style={{ color: "var(--faint)" }}>{d.market}</span>
        </span>
        <span className="chip tnum" title="when this verdict was computed">
          {d.asOf ? `updated ${ago(d.asOf)}` : "—"}
        </span>
        <span
          className="chip ml-auto font-bold"
          style={{ color: bandColor(d.band), borderColor: bandColor(d.band) }}
          title="how much to trust this call — a separate axis from the decision"
        >
          {d.confidenceLabel}
        </span>
      </div>

      <div className="flex flex-col gap-4 px-4 py-4">
        {/* DECISION + CONFIDENCE — the headline */}
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
          <div className="flex flex-col gap-1">
            <Label>DECISION</Label>
            <span
              className="text-[2.1rem] font-extrabold leading-none"
              style={{ color: decisionColor(d.decision) }}
            >
              {d.decision}
            </span>
          </div>
          <div className="flex flex-col gap-1">
            <Label>CONFIDENCE</Label>
            <span className="text-[0.9rem] font-bold" style={{ color: bandColor(d.band) }}>
              {d.confidenceLabel}
            </span>
            <span className="tnum text-[0.75rem]" style={{ color: "var(--faint)" }}>
              measured accuracy {accuracy}
            </span>
          </div>
        </div>

        {/* FAIR VALUE · CURRENT PRICE · EXPECTED RETURN */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Tile label="FAIR VALUE">
            {d.fairValue?.available ? (
              <>
                <span className="tnum text-[1rem] font-bold" style={{ color: "var(--text)" }}>
                  {fmtPrice(d.fairValue.value)}
                </span>
                <span className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
                  {d.fairValue.method}
                </span>
                {d.fairValue.note ? (
                  <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
                    {d.fairValue.note}
                  </span>
                ) : null}
              </>
            ) : (
              <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
                n/a — {d.fairValue?.note || "not modeled"}
              </span>
            )}
          </Tile>

          <Tile label="CURRENT PRICE">
            <span className="tnum text-[1rem] font-bold" style={{ color: "var(--text)" }}>
              {d.hasPrice ? fmtPrice(d.currentPrice) : "—"}
            </span>
            {!d.hasPrice && (
              <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                no live price
              </span>
            )}
          </Tile>

          {d.hasExpectedReturn ? (
            <Tile label="VS 20× EARNINGS">
              <span
                className="tnum text-[1rem] font-bold"
                style={{ color: d.expectedReturnPct >= 0 ? "var(--ok)" : "var(--bad)" }}
              >
                {d.expectedReturnPct >= 0 ? "+" : ""}
                {Number.isFinite(d.expectedReturnPct) ? d.expectedReturnPct.toFixed(1) : "—"}%
              </span>
              <span className="text-[0.75rem] leading-snug" style={{ color: "var(--faint)" }}>
                premium/discount to a plain 20× earnings benchmark — a valuation gauge, NOT a
                forecast. Premium-multiple growth names sit far below it by design.
              </span>
            </Tile>
          ) : null}
        </div>

        {/* KEY DRIVERS + RISKS */}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-1.5">
            <Label>KEY DRIVERS</Label>
            <BulletList items={d.keyDrivers} empty="none listed" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>RISKS</Label>
            <BulletList items={d.risks} empty="none listed" />
          </div>
        </div>

        {/* PROBABILITY DISTRIBUTION */}
        <div className="flex flex-col gap-2">
          <Label>PROBABILITY DISTRIBUTION</Label>
          {d.distribution?.available ? (
            <>
              <DistRow label="Bull" o={d.distribution.bull} color="var(--ok)" />
              <DistRow label="Base" o={d.distribution.base} color="var(--dim)" />
              <DistRow label="Bear" o={d.distribution.bear} color="var(--bad)" />
              {d.distribution.source ? (
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  source: {d.distribution.source}
                </span>
              ) : null}
            </>
          ) : (
            <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              no analogous historical setups yet
            </p>
          )}
        </div>
      </div>

      {/* disclaimer — always visible, this is research, not advice */}
      {d.disclaimer ? (
        <div className="px-4 py-2.5" style={{ borderTop: "1px solid var(--border)" }}>
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {d.disclaimer}
          </p>
        </div>
      ) : null}
    </section>
  );
}
