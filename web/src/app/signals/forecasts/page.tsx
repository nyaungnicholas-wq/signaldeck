"use client";

// Forecast page, rebuilt as a MODEL RACE: three honestly-graded models —
// walk-forward logistic (api.forecast), GBM and mean-reversion
// (api.modelForecasts) — side by side for one symbol + horizon, each P(up)
// shown RIGHT NEXT to its out-of-sample grade. The honesty rule is unchanged:
// a probability with lift <= 0 is grayed out and labeled noise; a model the
// trainer hasn't produced renders an honest empty lane. When 2+ models pass
// the gate, the agreement strip says whether they land on the same side —
// disagreement is a signal too.

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  api,
  HORIZONS,
  pollMs,
  POLL_DEFAULT,
  POLL_SLOW,
  screenerRows,
  type Forecast,
  type Horizon,
  type Market,
  type ModelForecast,
  type WatchRow,
} from "@/lib/api";
import { ago, fmtTs } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import ProOnly from "@/components/ProOnly";
import HorizonChips from "@/components/symbol/HorizonChips";
import ModelRaceCard, { type RaceStats } from "@/components/signals/forecasts/ModelRaceCard";
import AgreementStrip, { type AgreementEntry } from "@/components/signals/forecasts/AgreementStrip";
import ModelExplainer from "@/components/signals/forecasts/ModelExplainer";
import ScoreEvolution from "@/components/signals/forecasts/ScoreEvolution";

interface Selected {
  symbol: string;
  market: Market;
}

/** Both endpoints for one symbol, keyed so switching symbols never shows
 *  stale lanes. mfErr keeps the logistic lane alive if only the model-legs
 *  poll fails (surfaced as a visible chip, per house rules). */
interface RaceData {
  key: string;
  fc: Forecast[];
  mf: ModelForecast[];
  mfErr: string | null;
}

/** The three lanes, in fixed race order. */
const LANES = [
  {
    key: "logistic" as const,
    name: "logistic (walk-forward)",
    plainName: "straight-line model",
    tagline: "linear read of the stored features — the baseline every other model must justify itself against.",
  },
  {
    key: "gbm" as const,
    name: "GBM (boosted trees)",
    plainName: "pattern-finder (trees)",
    tagline: "non-linear: gradient-boosted decision trees hunting interactions a straight line can't see.",
  },
  {
    key: "meanrev" as const,
    name: "mean-reversion (costed)",
    plainName: "snap-back model",
    tagline: "contrarian: fades the momentum lean; graded net of a round-trip cost.",
    gradeNote: "graded net of round-trip cost",
  },
];

export default function ForecastPage() {
  const [rows, setRows] = useState<WatchRow[] | null>(null);
  const [rowsErr, setRowsErr] = useState<string | null>(null);
  const [selected, setSelected] = useState<Selected | null>(null);
  const [horizon, setHorizon] = useState<Horizon>("1d");
  const [retryTick, setRetryTick] = useState(0);

  const [race, setRace] = useState<RaceData | null>(null);
  const [raceErrState, setRaceErrState] = useState<{ key: string; msg: string } | null>(null);

  // Load the watchlist (for the picker); default to the first active symbol.
  // Stage 5: signed out the watchlist 401s — fall back to the strongest-
  // scored symbols from the PUBLIC universe screener so the page still works.
  // Same fallback when the watchlist answers but is EMPTY (fresh account with
  // no subscriptions): the daemon still has trained models for the public
  // universe, so racing the screener's strongest symbols beats a dead page.
  useEffect(() => {
    let alive = true;
    const apply = (r: WatchRow[]) => {
      setRows(r);
      setRowsErr(null);
      setSelected((cur) => {
        if (cur && r.some((x) => x.symbol === cur.symbol && x.market === cur.market)) {
          return cur;
        }
        const first = r.find((x) => x.active) ?? r[0];
        return first ? { symbol: first.symbol, market: first.market } : null;
      });
    };
    const applyScreenerTop = () =>
      screenerRows()
        .then((all) => {
          if (!alive) return;
          const top = [...all]
            .sort(
              (a, b) =>
                Math.abs(b.scores?.["1d"]?.score ?? 0) - Math.abs(a.scores?.["1d"]?.score ?? 0),
            )
            .slice(0, 24);
          apply(top);
        })
        .catch((e2: unknown) => {
          if (!alive) return;
          setRowsErr(e2 instanceof Error ? e2.message : String(e2));
        });
    const load = () =>
      api
        .watchlist()
        .then((r) => {
          if (!alive) return;
          if (r.some((x) => x.active)) {
            apply(r);
            return;
          }
          return applyScreenerTop();
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          if (!msg.includes("401")) {
            setRowsErr(msg);
            return;
          }
          return applyScreenerTop();
        });
    load();
    // POLL_DEFAULT tier — managed loop (hidden-tab pause, failure backoff).
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  const selKey = selected ? `${selected.symbol}|${selected.market}` : "";

  // Poll BOTH model sources for the selected symbol. The logistic forecast is
  // the page's backbone (its failure is the page's failure); the model legs
  // degrade to a visible chip instead of taking the whole page down.
  useEffect(() => {
    if (!selected) return;
    let alive = true;
    const key = `${selected.symbol}|${selected.market}`;
    const load = () =>
      Promise.allSettled([
        api.forecast(selected.symbol, selected.market),
        api.modelForecasts(selected.symbol, selected.market),
      ]).then(([fcRes, mfRes]) => {
        if (!alive) return;
        if (fcRes.status === "rejected") {
          const e: unknown = fcRes.reason;
          setRaceErrState({ key, msg: e instanceof Error ? e.message : String(e) });
          return;
        }
        const mfOk = mfRes.status === "fulfilled";
        // The daemon encodes an empty result as JSON null (Go nil slice) —
        // normalize to [] so the lanes render honest empties, not a crash.
        setRace({
          key,
          fc: fcRes.value ?? [],
          mf: (mfOk ? mfRes.value : null) ?? [],
          mfErr: mfOk
            ? null
            : mfRes.reason instanceof Error
              ? mfRes.reason.message
              : String(mfRes.reason),
        });
        setRaceErrState(null);
      });
    load();
    // POLL_SLOW tier — the forecast-trainer refreshes models hourly.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [selected, retryTick]);

  const data = race && race.key === selKey ? race : null;
  const raceErr = raceErrState && raceErrState.key === selKey ? raceErrState.msg : null;

  // Which horizons actually have at least one trained model — drives the tabs.
  const availableHorizons = useMemo<Horizon[]>(() => {
    if (!data) return [];
    const s = new Set<Horizon>();
    data.fc.forEach((f) => s.add(f.horizon));
    data.mf.forEach((m) => s.add(m.horizon));
    return HORIZONS.filter((h) => s.has(h));
  }, [data]);

  // Keep the selected horizon valid as data arrives / symbols change —
  // derived, not corrected in an effect (no cascading render).
  const effHorizon: Horizon =
    availableHorizons.length === 0 || availableHorizons.includes(horizon)
      ? horizon
      : availableHorizons.includes("1d")
        ? "1d"
        : availableHorizons[0];

  // The three lanes for the selected horizon (null = honest empty lane).
  const laneStats = useMemo<Record<(typeof LANES)[number]["key"], RaceStats | null>>(() => {
    const logistic = data?.fc.find((f) => f.horizon === effHorizon) ?? null;
    const gbm = data?.mf.find((m) => m.model === "gbm" && m.horizon === effHorizon) ?? null;
    const meanrev = data?.mf.find((m) => m.model === "meanrev" && m.horizon === effHorizon) ?? null;
    return { logistic, gbm, meanrev };
  }, [data, effHorizon]);

  const agreementEntries = useMemo<AgreementEntry[]>(
    () =>
      LANES.flatMap((l) => {
        const s = laneStats[l.key];
        return s
          ? [{ key: l.key, name: l.name, plainName: l.plainName, prob: s.prob, lift: s.lift }]
          : [];
      }),
    [laneStats],
  );

  // newest training timestamp across every model — for the header chip
  const trainedAt = useMemo(() => {
    if (!data) return 0;
    let m = 0;
    data.fc.forEach((f) => (m = f.ts > m ? f.ts : m));
    data.mf.forEach((r) => (m = r.ts > m ? r.ts : m));
    return m;
  }, [data]);

  const activeRows = useMemo(
    () => (rows ? rows.filter((r) => r.active) : []),
    [rows],
  );

  const rowsLoading = rows === null && rowsErr === null;
  const rowsHardError = rows === null && rowsErr !== null;

  const raceLoading = selected !== null && data === null && raceErr === null;
  const raceHardError = selected !== null && data === null && raceErr !== null;
  const raceEmpty = data !== null && data.fc.length === 0 && data.mf.length === 0;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">MODEL RACE</h1>
        {selected && <span className="chip mono">{selected.symbol}</span>}
        {selected && (
          <span className="chip" style={{ color: "var(--dim)" }}>
            {selected.market}
          </span>
        )}
        {selected && trainedAt > 0 && (
          <span className="chip tnum" title={fmtTs(trainedAt)}>
            trained {ago(trainedAt)}
          </span>
        )}
        {data?.mfErr && (
          <span
            className="chip"
            style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
            title={data.mfErr}
          >
            model legs poll failed — logistic only
          </span>
        )}
        {rowsErr !== null && rows !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="signals-forecasts"
        text="Three models — a straight-line logistic, a boosted-tree pattern-finder, and a costed snap-back leg — race on the same symbol, each P(up) shown right next to its out-of-sample grade. A probability with no proven lift is labeled noise, and when 2+ graded models exist we say whether they agree."
      />

      {/* explainer — methodology: direct in PRO, folded (never deleted) in
          SIMPLE; the per-lane noise/beats-base-rate gates stay visible on
          every card in both modes. */}
      <ProOnly summary="Show how each model earns the right to a number">
        <ModelExplainer />
      </ProOnly>

      {/* symbol picker */}
      <section className="panel">
        <div className="panel-h">
          SYMBOL
          <span
            className="text-[0.75rem] font-normal normal-case tracking-normal"
            style={{ color: "var(--faint)" }}
          >
            pick a symbol to race its models
          </span>
        </div>

        {rowsLoading && <Skeleton lines={2} label="loading watchlist" className="m-4" />}

        {rowsHardError && (
          <ErrorState
            className="m-4"
            message={rowsErr ?? "watchlist unavailable"}
            hint="Is the daemon running? Start it with signaldeckd."
            retry={() => {
              setRowsErr(null);
              setRetryTick((t) => t + 1);
            }}
          />
        )}

        {rows !== null && activeRows.length === 0 && (
          <EmptyState
            className="m-4"
            message="No active symbols yet"
            detail="Subscribe to symbols on the watchlist page and the forecast-trainer will pick them up."
          />
        )}

        {activeRows.length > 0 && (
          <div
            role="group"
            aria-label="symbol picker"
            className="flex flex-wrap gap-2 px-4 py-3"
          >
            {activeRows.map((r) => {
              const active =
                selected?.symbol === r.symbol && selected?.market === r.market;
              return (
                <button
                  key={`${r.market}:${r.symbol}`}
                  type="button"
                  onClick={() => setSelected({ symbol: r.symbol, market: r.market })}
                  aria-pressed={active}
                  className="chip mono min-h-[40px] cursor-pointer transition-colors duration-150 hover:brightness-125"
                  style={{
                    color: active ? "var(--text)" : "var(--dim)",
                    borderColor: active ? "var(--accent)" : "var(--border)",
                    background: active ? "rgba(251,191,36,.08)" : "var(--panel2)",
                  }}
                >
                  {r.symbol}
                  <span className="ml-1.5 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    {r.market}
                  </span>
                </button>
              );
            })}
          </div>
        )}
      </section>

      {/* the race itself */}
      {selected && (
        <>
          {raceLoading && (
            <Skeleton lines={4} label={`loading models for ${selected.symbol}`} />
          )}

          {raceHardError && (
            <ErrorState
              message={raceErr ?? "forecast unavailable"}
              hint="Is the daemon running? Start it with signaldeckd."
              retry={() => {
                setRaceErrState(null);
                setRetryTick((t) => t + 1);
              }}
            />
          )}

          {raceEmpty && (
            <EmptyState
              message={`No models yet for ${selected.symbol}`}
              detail="The forecast-trainer runs hourly and needs ~150 daily bars before it can grade a model."
            />
          )}

          {data !== null && !raceEmpty && (
            <div className="flex flex-col gap-4">
              {/* horizon tabs */}
              <div className="flex flex-wrap items-center gap-2 px-1">
                <span className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                  HORIZON
                </span>
                <HorizonChips
                  value={effHorizon}
                  onChange={setHorizon}
                  available={availableHorizons}
                />
              </div>

              {/* agreement strip — do the graded models land on the same side? */}
              <AgreementStrip entries={agreementEntries} horizon={effHorizon} />

              {/* three lanes, side by side */}
              <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
                {LANES.map((l) => (
                  <ModelRaceCard
                    key={`${selKey}:${effHorizon}:${l.key}`}
                    name={l.name}
                    plainName={l.plainName}
                    tagline={l.tagline}
                    gradeNote={l.gradeNote}
                    horizon={effHorizon}
                    symbol={selected.symbol}
                    stats={laneStats[l.key]}
                  />
                ))}
              </div>

              {/* score evolution — the mechanical score's stored path (kept from the old page) */}
              <ScoreEvolution
                symbol={selected.symbol}
                market={selected.market}
                horizon={effHorizon}
              />

              <div className="px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                <Link
                  href={`/s/${selected.market}/${encodeURIComponent(selected.symbol)}`}
                  className="cursor-pointer text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]"
                >
                  Compare with {selected.symbol}&apos;s mechanical expectancy
                </Link>{" "}
                (what usually happens next by state) on the symbol page.
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
