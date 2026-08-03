"use client";

import { useState } from "react";
import { ago } from "@/lib/format";
import ErrorState from "@/components/ErrorState";
import ProOnly from "@/components/ProOnly";
import CalibrationPanel from "@/components/predict/CalibrationPanel";
import SignalScoreHero from "@/components/signals/predictions/SignalScoreHero";
import TVRatingChip from "@/components/signals/predictions/TVRatingChip";
import FactorTiles from "@/components/signals/predictions/FactorTiles";
import AdditiveLedger from "@/components/signals/predictions/AdditiveLedger";
import AlphaModelPanel from "@/components/signals/predictions/AlphaModelPanel";
import CompositeLeaderboard from "@/components/signals/predictions/CompositeLeaderboard";
import SymbolPickerPanel from "@/components/signals/predictions/SymbolPickerPanel";
import {
  useCalibration,
  useComposite,
  usePredictionWatchlist,
  type CalHorizon,
} from "@/hooks/usePredictions";
import { PageHero, StatTile } from "@/components/ui/Kit";

export default function PredictPage() {
  const [retryTick, setRetryTick] = useState(0);
  const [calHorizon, setCalHorizon] = useState<CalHorizon>("1d");

  const { watch, watchErr, setWatchErr, picked, setPicked, anonPicker } =
    usePredictionWatchlist(retryTick);
  const { comp, compErr, compAt, resetComp } = useComposite(picked, retryTick);
  const { cal, calLoading, calErr, clearCalErr } = useCalibration(calHorizon, retryTick);

  const watchHardError = watch === null && watchErr !== null;
  const compLoading = !!picked && comp === null && compErr === null;

  // Stats for hero band
  const totalSignals = watch?.length ?? 0;
  const bullishCount = watch?.filter(s => (s.scores?.["1d"]?.score ?? 0) > 0).length ?? 0;
  const bearishCount = watch?.filter(s => (s.scores?.["1d"]?.score ?? 0) < 0).length ?? 0;
  const strongestSymbol = watch && watch.length
    ? watch.reduce((max, s) => (Math.abs(s.scores?.["1d"]?.score ?? 0) > Math.abs(max.scores?.["1d"]?.score ?? 0) ? s : max)).symbol
    : "—";

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Signals"
        subtitle="Every active pressure signal, ranked by conviction — grouped so you see the story, not a wall of tickers."
        right={
          <div className="flex items-center gap-2">
            {picked && (
              <span className="chip tnum">
                {picked.symbol} <span style={{ color: "var(--faint)" }}>{picked.market}</span>
              </span>
            )}
            <span className="chip tnum">
              {compLoading ? (
                <span style={{ color: "var(--faint)" }}>loading…</span>
              ) : compAt ? (
                `updated ${ago(compAt)}`
              ) : (
                "—"
              )}
            </span>
          </div>
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Total Signals" value={totalSignals} glow="hud" i={0} />
        <StatTile label="Bullish" value={bullishCount} glow="up" i={1} />
        <StatTile label="Bearish" value={bearishCount} glow="down" i={2} />
        <StatTile label="Strongest" value={strongestSymbol} glow="hud" i={3} />
      </div>

      {watchHardError && (
        <ErrorState
          message={watchErr ?? "could not load the watchlist"}
          hint="is the daemon running? start it with signaldeckd and the picker will populate."
          retry={() => {
            setWatchErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      <SymbolPickerPanel
        watch={watch}
        watchErr={watchErr}
        anonPicker={anonPicker}
        picked={picked}
        onPick={setPicked}
      />

      {picked && (
        <SignalScoreHero
          symbol={picked.symbol}
          market={picked.market}
          data={comp}
          err={compErr}
          retry={() => {
            resetComp();
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {picked && (
        <div className="flex flex-wrap gap-2 px-1">
          <TVRatingChip symbol={picked.symbol} market={picked.market} />
        </div>
      )}

      {comp?.available && (
        <ProOnly summary="Show the evidence breakdown (11 factors + edge ledger)">
          <div className="flex flex-col gap-4">
            <FactorTiles factors={comp.factors} />
            <AdditiveLedger ledger={comp.ledger} calProb={comp.calProb} />
          </div>
        </ProOnly>
      )}

      <ProOnly summary="Show past-signal history (how probabilities resolved)">
        <div className="flex flex-col gap-1">
          <p className="px-1 text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
            PAST-SIGNAL HISTORY — how past calibrated probabilities actually resolved
          </p>
          <CalibrationPanel
            horizon={calHorizon}
            onHorizon={setCalHorizon}
            data={cal}
            loading={calLoading}
            err={calErr}
            retry={() => {
              clearCalErr();
              setRetryTick((t) => t + 1);
            }}
          />
        </div>
      </ProOnly>

      <AlphaModelPanel />

      <CompositeLeaderboard onPick={(s, m) => setPicked({ symbol: s, market: m })} />
    </div>
  );
}
