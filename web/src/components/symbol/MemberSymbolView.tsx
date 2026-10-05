"use client";

// The symbol page as a MEMBER sees it. Price comes from TradingView's widget
// (TradingView carries that data licence); everything else is SignalDeck's own
// validated regime reads with their measured accuracy, the symbol's model tier,
// and public-domain intel (SEC filings, Form 4, 13F, XBRL fundamentals, STOCK
// Act trades, and FINRA short data only while the daemon opens it to members,
// SIGNALDECK_MEMBER_FINRA). Every panel here reads only routes in the daemon's
// memberRoutes; the operator page next door reads ~27 more.

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, type Market } from "@/lib/api";
import TradingViewChart from "@/components/TradingViewChart";
import ValidatedSignalsPanel from "@/components/symbol/ValidatedSignalsPanel";
import SymbolAgentPanel from "@/components/symbol/SymbolAgentPanel";
import FilingsIntelPanel from "@/components/symbol/FilingsIntelPanel";
import FinancialsPanel from "@/components/symbol/FinancialsPanel";
import ShortInterestPanel from "@/components/symbol/ShortInterestPanel";
import ShortVolumePanel from "@/components/symbol/ShortVolumePanel";
import CongressChip from "@/components/symbol/CongressChip";
import HypotheticalNote from "@/components/HypotheticalNote";
import { useMe } from "@/hooks/useMe";

export default function MemberSymbolView({ symbol, market }: { symbol: string; market: Market }) {
  const [name, setName] = useState<string>("");
  const [watching, setWatching] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  // FINRA short data is operator-only unless the daemon says otherwise; the
  // panels are not even mounted until it does, so no refused fetch fires.
  const finra = useMe().me?.memberFinra === true;

  useEffect(() => {
    let alive = true;
    api.companyProfile(symbol).then(
      (p) => {
        // The symbols table often holds a bare ticker; the SEC directory name
        // ("Apple Inc.") is the one a reader recognises.
        if (alive) setName(p.company?.name || p.name || "");
      },
      () => {}, // the name is decoration; the page stands without it
    );
    return () => {
      alive = false;
    };
  }, [symbol]);

  useEffect(() => {
    let alive = true;
    api.memberWatchlist().then(
      (rows) => {
        if (alive) setWatching(rows.some((r) => r.symbol === symbol && r.market === market));
      },
      () => {
        if (alive) setWatching(null);
      },
    );
    return () => {
      alive = false;
    };
  }, [symbol, market, tick]);

  const toggle = async () => {
    setBusy(true);
    setErr(null);
    try {
      if (watching) await api.unwatch(symbol, market);
      else await api.watch(symbol, market);
      setTick((t) => t + 1);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <header className="panel flex flex-wrap items-center justify-between gap-3 px-4 py-3">
        <div className="min-w-0">
          <h1 className="mono m-0 text-xl font-bold">{symbol}</h1>
          <p className="m-0 text-[0.8rem]" style={{ color: "var(--dim)" }}>
            {name || (market === "crypto" ? "Crypto" : "US stock")}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {watching !== null && (
            <button
              type="button"
              onClick={() => void toggle()}
              disabled={busy}
              aria-pressed={watching}
              className="chip flex min-h-[40px] cursor-pointer items-center px-3"
              style={watching ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
            >
              {watching ? "Watching ✓" : "Watch"}
            </button>
          )}
          <Link href="/watchlist" className="chip flex min-h-[40px] items-center px-3">
            Your watchlist
          </Link>
        </div>
        {err && (
          <p role="alert" className="m-0 w-full text-[0.8rem]" style={{ color: "var(--ask)" }}>
            {err}
          </p>
        )}
      </header>

      <TradingViewChart symbol={symbol} market={market} />
      <ValidatedSignalsPanel symbol={symbol} market={market} memberView />
      <SymbolAgentPanel symbol={symbol} market={market} memberView />
      {market === "stocks" && (
        <>
          <CongressChip symbol={symbol} />
          <FinancialsPanel symbol={symbol} />
          <FilingsIntelPanel symbol={symbol} />
          {finra && <ShortInterestPanel symbol={symbol} />}
          {finra && <ShortVolumePanel symbol={symbol} />}
        </>
      )}

      <p className="m-0 px-1 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        Regime reads are SignalDeck&rsquo;s own forecasts, each shown with the accuracy it measured in
        backtests; how they fare live is graded on the{" "}
        <Link href="/accuracy" className="underline">
          record
        </Link>
        . Filings, insider and holder data are public SEC records{finra ? "; short data is FINRA's" : ""}. Not financial advice.
      </p>
      <HypotheticalNote short className="px-1" />
    </div>
  );
}
