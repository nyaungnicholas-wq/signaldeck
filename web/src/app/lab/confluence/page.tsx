"use client";

import { useEffect, useState } from "react";
import {
  confluence,
  confluenceTop,
  confluenceTrack,
  pollMs,
  POLL_SLOW,
  type ConfluenceResponse,
  type ConfluenceTopResponse,
  type ConfluenceTrackResponse,
  type ConfluenceVote,
  type Money,
} from "@/lib/api";
import { ago } from "@/lib/format";
import ReportLink from "@/components/signals/ReportLink";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import { PageHero, StatTile as KitStatTile } from "@/components/ui/Kit";

const FAMILY_LABEL: Record<string, string> = {
  smart_money: "Smart money",
  trend: "Trend / regime",
  prediction: "Model prediction",
  rel_strength: "Relative strength",
  breakout: "Breakout",
};

function dirColor(dir: number): string {
  if (dir > 0) return "var(--bid)";
  if (dir < 0) return "var(--ask)";
  return "var(--dim)";
}
function dirText(dir: number): string {
  if (dir > 0) return "LONG";
  if (dir < 0) return "SHORT";
  return "none";
}
function dirArrow(dir: number): string {
  if (dir > 0) return "▲";
  if (dir < 0) return "▼";
  return "·";
}
function pct(frac: number, signed = true): string {
  if (!isFinite(frac)) return "—";
  const v = frac * 100;
  const s = signed && v > 0 ? "+" : "";
  return `${s}${v.toFixed(2)}%`;
}

function StatTile({
  label,
  value,
  hint,
  accent,
}: {
  label: string;
  value: string;
  hint?: string;
  accent?: string;
}) {
  return (
    <div className="panel px-3 py-2.5">
      <div className="text-[0.65rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
        {label}
      </div>
      <div className="tnum text-[0.95rem] font-bold" style={{ color: accent ?? "var(--text)" }}>
        {value}
      </div>
      {hint ? (
        <div className="text-[0.65rem]" style={{ color: "var(--dim)" }}>
          {hint}
        </div>
      ) : null}
    </div>
  );
}

function MoneyTiles({ m }: { m: Money }) {
  return (
    <div className="grid grid-cols-2 gap-2 px-4 py-3 sm:grid-cols-3">
      <KitStatTile
        label="expectancy / trade"
        value={pct(m.expectancy)}
        sub="avg net profit per trade — what matters"
        glow={m.expectancy >= 0 ? "up" : "down"}
        i={0}
      />
      <KitStatTile
        label="profit factor"
        value={m.profitFactorValid ? m.profitFactor.toFixed(2) : "—"}
        sub="Σ wins ÷ Σ losses · >1 profits"
        i={1}
      />
      <KitStatTile
        label="payoff ratio"
        value={m.payoffRatioValid ? m.payoffRatio.toFixed(2) : "—"}
        sub="avg win ÷ avg loss"
        i={2}
      />
      <KitStatTile label="avg win" value={pct(m.avgWin)} glow="up" i={3} />
      <KitStatTile label="avg loss" value={pct(-m.avgLoss)} glow="down" i={4} />
      <KitStatTile
        label="win rate"
        value={pct(m.winRate, false)}
        sub="descriptive only — NOT profit"
        i={5}
      />
    </div>
  );
}

function MoneyScoreboard() {
  const [data, setData] = useState<ConfluenceTrackResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      confluenceTrack()
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (alive) setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  if (data === null && err === null) return <Skeleton lines={4} label="loading money scoreboard" />;
  if (data === null && err !== null)
    return (
      <ErrorState
        message={err}
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  if (!data) return null;

  const gated = !data.money;
  return (
    <section className="hud-panel">
      <div className="panel-h flex-wrap gap-2">
        MONEY SCOREBOARD
        <span className="chip tnum" style={{ color: "var(--faint)" }}>
          {data.resolved} independent resolutions
        </span>
        <span className="chip ml-auto" style={{ color: data.live ? "var(--bid)" : "var(--faint)" }}>
          forward-tracked · no lookahead
        </span>
      </div>
      {data.caveat ? (
        <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
          {data.caveat}
        </p>
      ) : null}
      {gated || !data.money ? (
        <EmptyState message="Scoreboard accruing" detail={data.note} />
      ) : (
        <>
          <MoneyTiles m={data.money} />
          {data.note ? (
            <p className="px-4 pb-3 text-[0.7rem]" style={{ color: "var(--dim)" }}>
              {data.note}
            </p>
          ) : null}
          {data.byDirection ? (
            <div className="px-4 pb-4">
              <div className="text-[0.65rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
                by direction
              </div>
              <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                <span>
                  LONG expectancy{" "}
                  <span className="tnum" style={{ color: "var(--text)" }}>
                    {pct(data.byDirection.long.expectancy)}
                  </span>{" "}
                  ({data.byDirection.long.trades} trades)
                </span>
                <span>
                  SHORT expectancy{" "}
                  <span className="tnum" style={{ color: "var(--text)" }}>
                    {pct(data.byDirection.short.expectancy)}
                  </span>{" "}
                  ({data.byDirection.short.trades} trades)
                </span>
              </div>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

function Leaderboard({ onPick }: { onPick: (symbol: string) => void }) {
  const [data, setData] = useState<ConfluenceTopResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [onlySetups, setOnlySetups] = useState(true);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      confluenceTop(undefined, 50, onlySetups)
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (alive) setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [onlySetups, retryTick]);

  if (data === null && err === null) return <Skeleton lines={6} label="loading confluence setups" />;
  if (data === null && err !== null)
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? confluence-scorer refreshes every 30m."
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  if (!data) return null;

  const rows = data.rows ?? [];
  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        CONFLUENCE SETUPS
        <label
          className="chip ml-auto flex items-center gap-1.5"
          style={{ color: "var(--faint)", cursor: "pointer" }}
        >
          <input
            type="checkbox"
            checked={onlySetups}
            onChange={(e) => setOnlySetups(e.target.checked)}
          />
          setups only
        </label>
      </div>
      {data.caveat ? (
        <p className="px-4 py-2 text-[0.72rem] leading-relaxed" style={{ color: "var(--warn)" }}>
          {data.caveat}
        </p>
      ) : null}
      {!data.available || rows.length === 0 ? (
        <EmptyState
          message={onlySetups ? "No setups right now" : "No confluence scored yet"}
          detail={
            data.reason ??
            "a setup needs ≥3 independent families agreeing — often there are none, which is the honest answer, not a bug."
          }
        />
      ) : (
        <ul style={{ borderTop: "1px solid var(--border)" }}>
          {rows.map((r) => (
            <li key={`${r.market}:${r.symbol}`} style={{ borderBottom: "1px solid var(--border)" }}>
              <button
                type="button"
                onClick={() => onPick(r.symbol)}
                className="flex w-full flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-left text-[0.75rem] transition-colors duration-150 hover:bg-[var(--panel3)]"
              >
                <span className="tnum w-20 font-bold" style={{ color: "var(--text)" }}>
                  {r.symbol}
                </span>
                <span
                  className="chip"
                  style={{ color: dirColor(r.direction), borderColor: dirColor(r.direction) }}
                >
                  {dirArrow(r.direction)} {dirText(r.direction)}
                </span>
                <span className="chip tnum" style={{ color: "var(--faint)" }}>
                  {r.agree}× agree
                </span>
                {r.isSetup ? (
                  <span className="chip" style={{ color: "var(--bid)", borderColor: "var(--bid)" }}>
                    SETUP
                  </span>
                ) : null}
                <ReportLink symbol={r.symbol} market={r.market} kind="composite" />
                <span
                  className="tnum ml-auto font-bold"
                  style={{ color: dirColor(r.direction) }}
                >
                  {r.score >= 0 ? "+" : ""}
                  {r.score.toFixed(2)}
                </span>
                <span className="tnum text-[0.7rem]" style={{ color: "var(--faint)" }}>
                  {ago(r.ts)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function SymbolDetail({ symbol }: { symbol: string }) {
  const [data, setData] = useState<ConfluenceResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      confluence(symbol)
        .then((d) => {
          if (!alive) return;
          setData(d);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (msg.includes("404")) {
            setData({
              available: false,
              symbol,
              caveat: "",
              reason: "not a tracked symbol — confluence is universe-scoped",
            });
            setErr(null);
            return;
          }
          setErr(msg);
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, retryTick]);

  if (data === null && err === null)
    return <Skeleton lines={5} label={`loading ${symbol} confluence`} />;
  if (data === null && err !== null)
    return (
      <ErrorState
        message={err}
        hint="Is the daemon running? confluence-scorer refreshes every 30m."
        retry={() => {
          setErr(null);
          setRetryTick((t) => t + 1);
        }}
      />
    );
  if (data && !data.available)
    return (
      <EmptyState
        message={`No confluence for ${symbol} yet`}
        detail={
          data.reason ??
          "no independent signals present for this symbol yet — nothing stored rather than a fabricated read"
        }
      />
    );
  if (!data) return null;

  const votes = data.votes ?? [];
  const dir = data.direction ?? 0;
  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        {data.symbol} CONFLUENCE
        <span className="chip" style={{ color: dirColor(dir), borderColor: dirColor(dir) }}>
          {dirArrow(dir)} {dirText(dir)}
        </span>
        {data.isSetup ? (
          <span className="chip" style={{ color: "var(--bid)", borderColor: "var(--bid)" }}>
            SETUP
          </span>
        ) : (
          <span className="chip" style={{ color: "var(--dim)" }}>
            no setup
          </span>
        )}
        {data.ts ? (
          <span className="chip ml-auto tnum" style={{ color: "var(--faint)" }}>
            {ago(data.ts)}
          </span>
        ) : null}
      </div>
      {data.caveat ? (
        <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
          {data.caveat}
        </p>
      ) : null}
      <div className="grid grid-cols-3 gap-2 px-4 py-3">
        <StatTile label="agree" value={String(data.agree ?? 0)} hint="families same way" />
        <StatTile label="dissent" value={String(data.dissent ?? 0)} hint="families opposing" />
        <StatTile label="present" value={String(data.present ?? 0)} hint="families with data" />
      </div>
      {votes.length > 0 ? (
        <ul style={{ borderTop: "1px solid var(--border)" }}>
          {votes.map((v: ConfluenceVote) => (
            <li
              key={v.family}
              className="flex flex-wrap items-baseline gap-x-2 px-4 py-2.5"
              style={{ borderBottom: "1px solid var(--border)" }}
            >
              <span className="text-[0.85rem] font-bold" style={{ color: dirColor(v.dir) }}>
                {dirArrow(v.dir)}
              </span>
              <span className="w-28 text-[0.8rem] font-bold" style={{ color: "var(--text)" }}>
                {FAMILY_LABEL[v.family] ?? v.family}
              </span>
              <span className="min-w-0 flex-1 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {v.reason}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          no independent families present this pass.
        </p>
      )}
    </section>
  );
}

export default function ConfluencePage() {
  const [symbol, setSymbol] = useState<string>("");
  const [input, setInput] = useState<string>("");

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="CONFLUENCE"
        subtitle="Independent signal families agree on a direction, gated by expected profit — not win rate."
        right={
          <span className="chip">agreement gate · expected profit</span>
        }
      />

      <PagePurpose
        id="signals-confluence"
        text="A setup is flagged ONLY when several INDEPENDENT signal families — smart money, trend, the model, relative strength, breakout — agree on a direction. Fewer, higher-quality reads. It is scored by EXPECTED PROFIT (expectancy), not win rate, and the record stays gated until enough forward-tracked resolutions exist. This is not a guarantee — a setup is a higher-quality prior, not a promise."
      />

      <MoneyScoreboard />
      <Leaderboard onPick={setSymbol} />

      <section className="panel">
        <div className="panel-h">INSPECT A SYMBOL</div>
        <form
          className="flex gap-2 px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            const v = input.trim().toUpperCase();
            if (v) setSymbol(v);
          }}
        >
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="ticker e.g. AAPL"
            aria-label="symbol to inspect"
            className="tnum"
            style={{
              flex: 1,
              background: "var(--panel2)",
              border: "1px solid var(--border)",
              color: "var(--text)",
              padding: "0.4rem 0.6rem",
              borderRadius: 4,
            }}
          />
          <button
            type="submit"
            className="chip min-h-[36px] cursor-pointer px-3"
            style={{ color: "var(--text)", borderColor: "var(--border)" }}
          >
            show votes
          </button>
        </form>
      </section>

      {symbol ? (
        <>
          <SymbolDetail symbol={symbol} />
          <button
            type="button"
            onClick={() => setSymbol("")}
            className="chip w-fit min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
          >
            ← clear symbol
          </button>
        </>
      ) : (
        <p className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Pick a setup above or type a ticker to see its full vote transparency — every independent
          family, which way it points, and why.
        </p>
      )}
    </div>
  );
}
