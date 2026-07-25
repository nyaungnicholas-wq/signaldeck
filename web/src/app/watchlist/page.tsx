"use client";

// MY DECK — the user's watchlist as glass cards: price + day change, the ~60d
// sparkline, the symbol's validated regime stack (ONE structuralRegimes()
// call grouped client-side — never a signalReport per card), and its latest
// alert. Honest empty state when the watchlist is empty; alerts are
// session-scoped and degrade to absence when the call fails.

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  api,
  structuralRegimes,
  type AlertRow,
  type StructRegimeForecast,
  type StructRegimes,
  type WatchRow,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice } from "@/lib/format";
import { changeColor } from "@/components/home/helpers";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import Sparkline from "@/components/viz/Sparkline";
import { usePeek } from "@/components/CompanyPeek";

function pct(x: number): string {
  return `${(x * 100).toFixed(1)}%`;
}

function DeckCard({
  row,
  stack,
  alert,
}: {
  row: WatchRow;
  stack: StructRegimeForecast[];
  alert: AlertRow | null;
}) {
  const peek = usePeek();
  const sym = encodeURIComponent(row.symbol);
  return (
    <article className="panel flex flex-col gap-2 p-4">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <button
          type="button"
          onClick={() => peek.open(row.symbol, row.market)}
          className="mono cursor-pointer text-base font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
          title={`peek ${row.symbol}`}
        >
          {row.symbol}
        </button>
        <span className="min-w-0 truncate text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {row.name}
        </span>
        <span className="tnum ml-auto text-[0.85rem]">{fmtPrice(row.lastClose)}</span>
        <span className="tnum text-[0.75rem]" style={{ color: changeColor(row.dayChangePct) }}>
          {fmtPct(row.dayChangePct)}
        </span>
      </div>

      <Sparkline closes={row.spark ?? []} width={220} height={40} />

      {stack.length > 0 ? (
        <div className="flex flex-wrap gap-1.5 text-[0.75rem]">
          {stack.map((f) => (
            <Link
              key={f.kind}
              href={`/signals/report/${f.market}/${sym}?kind=${f.kind}`}
              className="chip cursor-pointer px-2 py-[2px] transition-colors duration-150 hover:text-[var(--accent)]"
              title={`measured accuracy at this conviction band: ${pct(f.historicalAccuracy)}`}
            >
              {f.kind}: {f.regime} ({pct(f.historicalAccuracy)} band)
            </Link>
          ))}
        </div>
      ) : (
        <p className="m-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          no validated regime forecasts on this symbol yet
        </p>
      )}

      {alert ? (
        <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          <span className="chip mr-1.5 px-2 py-[1px] tracking-wider" style={{ color: "var(--crossed)", borderColor: "var(--crossed)" }}>
            alert
          </span>
          {alert.detail}{" "}
          <span className="tnum" style={{ color: "var(--faint)" }}>
            {ago(alert.ts)}
          </span>
        </p>
      ) : null}

      <div className="mt-auto flex flex-wrap gap-x-4 pt-1 text-[0.75rem]">
        <Link
          href={`/signals/report/${row.market}/${sym}?kind=overview`}
          className="cursor-pointer text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
        >
          report →
        </Link>
        <Link
          href={`/s/${row.market}/${sym}`}
          className="cursor-pointer text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
        >
          full page →
        </Link>
      </div>
    </article>
  );
}

export default function DeckPage() {
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [watchErr, setWatchErr] = useState<string | null>(null);
  const [struct, setStruct] = useState<StructRegimes | null>(null);
  // alerts are session-scoped (401 logged out) — absence is honest, not fatal
  const [alerts, setAlerts] = useState<AlertRow[] | null>(null);
  const [tick, setTick] = useState(0);
  const retry = () => setTick((t) => t + 1);

  useEffect(() => {
    let dead = false;
    api
      .watchlist()
      .then((w) => !dead && (setWatch(w ?? []), setWatchErr(null)))
      .catch((e: unknown) => !dead && setWatchErr(e instanceof Error ? e.message : String(e)));
    structuralRegimes()
      .then((s) => !dead && setStruct(s))
      .catch(() => undefined);
    api
      .alerts(false, 100)
      .then((a) => !dead && setAlerts(a ?? []))
      .catch(() => !dead && setAlerts(null));
    return () => {
      dead = true;
    };
  }, [tick]);

  // ONE regimes payload serves every card: group by symbol client-side.
  const stackBySymbol = new Map<string, StructRegimeForecast[]>();
  if (struct) {
    for (const rows of Object.values(struct.forecasts)) {
      for (const f of rows) {
        const list = stackBySymbol.get(f.symbol) ?? [];
        list.push(f);
        stackBySymbol.set(f.symbol, list);
      }
    }
  }

  const latestAlertFor = (symbol: string): AlertRow | null => {
    const rows = (alerts ?? []).filter((a) => a.symbol === symbol);
    if (rows.length === 0) return null;
    return rows.reduce((best, a) => (a.ts > best.ts ? a : best), rows[0]);
  };

  return (
    <main className="mx-auto max-w-6xl space-y-4 px-4 py-6">
      <h1 className="mono text-lg font-semibold">MY WATCHLIST</h1>
      <PagePurpose
        id="deck"
        text="your watchlist as one card per symbol: stored last close + day change, the ~60-day sparkline, the validated regime stack with its measured accuracy bands, and the latest alert. Prices are worker-cadence daily closes, never live quotes."
      />

      {watchErr ? (
        <ErrorState
          message={`Couldn't load your watchlist: ${watchErr}`}
          retry={retry}
          hint="The watchlist is per-user — log in and retry. If the daemon is down, start signaldeckd."
        />
      ) : null}
      {!watch && !watchErr ? <Skeleton lines={8} /> : null}

      {watch && watch.length === 0 ? (
        <>
          <EmptyState
            message="Your deck is empty"
            detail="Nothing is faked in the meantime — add symbols from the screener and each one becomes a card here."
          />
          <Link
            href="/markets/screener"
            className="chip inline-flex min-h-[40px] cursor-pointer items-center px-4 text-[0.75rem] tracking-wider transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
          >
            Open the screener →
          </Link>
        </>
      ) : null}

      {watch && watch.length > 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {watch.map((row) => (
            <DeckCard
              key={`${row.market}-${row.symbol}`}
              row={row}
              stack={stackBySymbol.get(row.symbol) ?? []}
              alert={latestAlertFor(row.symbol)}
            />
          ))}
        </div>
      ) : null}
    </main>
  );
}
