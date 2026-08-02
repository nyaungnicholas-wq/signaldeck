"use client";

import { useState } from "react";
import { ago } from "@/lib/format";
import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";
import ProOnly from "@/components/ProOnly";
import SavedViewsBar from "@/components/SavedViewsBar";
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

/**
 * PREDICT — the flagship, rebuilt around the composite SignalScore:
 *   1. HERO — one symbol's 1–10 forced-curve score + the calibrated edge
 *      line verbatim (available:false renders the API's reason, honestly);
 *   2. FACTOR TILES — the exactly-11 evidence legs with per-leg measured
 *      skill and visible gates;
 *   3. ADDITIVE LEDGER — the pp waterfall from the 50% coin baseline to the
 *      calibrated P(up), reconciled to its target;
 *   4. PAST-SIGNAL HISTORY — the resolved-outcomes calibration panel (kept):
 *      how past probabilities actually resolved, binned predicted vs real;
 *   5. ALPHA MODEL — the pooled cross-sectional model's OOS grade and, only
 *      while ungated, its top-20 relative scores (fleet-level, like 6);
 *   6. LEADERBOARD — the fleet ranked by score with rank-change movers.
 * Honesty is the product: every score keeps its gate ON the card — curve
 * notes, track labels, and gate reasons are chips and footnotes, not tooltips.
 * State + polling live in hooks/usePredictions; this file only composes.
 */
export default function PredictPage() {
  // Incremented by ErrorState retry buttons to re-kick the polling hooks.
  const [retryTick, setRetryTick] = useState(0);
  const [calHorizon, setCalHorizon] = useState<CalHorizon>("1d");

  const { watch, watchErr, setWatchErr, picked, setPicked, anonPicker } =
    usePredictionWatchlist(retryTick);
  const { comp, compErr, compAt, resetComp } = useComposite(picked, retryTick);
  const { cal, calLoading, calErr, clearCalErr } = useCalibration(calHorizon, retryTick);

  const watchHardError = watch === null && watchErr !== null;
  const compLoading = !!picked && comp === null && compErr === null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-1">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">PREDICT</h1>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          one calibrated probability + a forced-curve rank — with the evidence
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-2">
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
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="signals-predictions"
        text="One calibrated probability + a forced-curve rank with its evidence — never advice. The score is a rank on today's cross-section, the edge is measured against a coin flip, and every gate stays visible on the card."
      />

      {/* remembered symbol + horizon picks (this device only). No presets:
          the page state carries no calibrated-prob or sample-size filter, so
          a "Highest-confidence" preset would have nothing honest to apply. */}
      <SavedViewsBar
        pageKey="predictions"
        currentState={{
          symbol: picked?.symbol ?? null,
          market: picked?.market ?? null,
          calHorizon,
        }}
        onApply={(s) => {
          if (
            typeof s.symbol === "string" &&
            s.symbol !== "" &&
            (s.market === "crypto" || s.market === "stocks")
          ) {
            setPicked({ symbol: s.symbol, market: s.market });
          }
          if (s.calHorizon === "1d" || s.calHorizon === "1w") setCalHorizon(s.calHorizon);
        }}
      />

      {/* watchlist hard error */}
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

      {/* symbol picker */}
      <SymbolPickerPanel
        watch={watch}
        watchErr={watchErr}
        anonPicker={anonPicker}
        picked={picked}
        onPick={setPicked}
      />

      {/* 1 · HERO — the SignalScore card (handles loading/error/miss itself) */}
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

      {/* 1b · EXTERNAL CONTEXT — TradingView's own rating (dashed = context) */}
      {picked && (
        <div className="flex flex-wrap gap-2 px-1">
          <TVRatingChip symbol={picked.symbol} market={picked.market} />
        </div>
      )}

      {/* 2 · FACTOR TILES + 3 · ADDITIVE LEDGER — only when a verdict exists.
          Technical evidence detail: folded in SIMPLE mode, direct in PRO —
          the verdict, its gates and footnotes stay on the hero in both. */}
      {comp?.available && (
        <ProOnly summary="Show the evidence breakdown (11 factors + edge ledger)">
          <div className="flex flex-col gap-4">
            <FactorTiles factors={comp.factors} />
            <AdditiveLedger ledger={comp.ledger} calProb={comp.calProb} />
          </div>
        </ProOnly>
      )}

      {/* 4 · PAST-SIGNAL HISTORY — the resolved-outcomes panel, kept: binned
          predicted-vs-realized over every graded prediction, per horizon. */}
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

      {/* 5 · ALPHA MODEL — the pooled cross-sectional model (fleet-level):
          purged walk-forward OOS grade + gate, and only while ungated the
          top-20 relative scores. Handles its own loading/error/gated states. */}
      <AlphaModelPanel />

      {/* 6 · LEADERBOARD — the fleet ranked by SignalScore; a click re-aims
          the hero above. */}
      <CompositeLeaderboard onPick={(s, m) => setPicked({ symbol: s, market: m })} />
    </div>
  );
}
