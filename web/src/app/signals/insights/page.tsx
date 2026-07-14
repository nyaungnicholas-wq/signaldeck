"use client";

// SIGNALS → INSIGHTS — the AI-written feed, rebuilt on the Benzinga-WIIM
// pattern with SignalDeck honesty: every headline keeps its "not a
// forecast" line ON the item, and every item exposes the receipts — the
// stored data blob it was generated from — via the EVIDENCE expander.
// Kind chips (data.kind) filter SERVER-SIDE through /api/insights?kind=;
// counts are read from the latest polled window. Malformed/empty evidence
// blobs simply render no expander (parseEvidence never throws).

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, pollMs, POLL_DEFAULT, type Insight } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";
import { useViewMode } from "@/components/Plain";
import EvidencePanel from "@/components/signals/insights/EvidencePanel";
import TrendingTokensStrip from "@/components/signals/insights/TrendingTokensStrip";
import KindChips, {
  ALL_KINDS,
  UNLABELED,
  type KindOption,
} from "@/components/signals/insights/KindChips";
import { evidenceKind, kindLabel, parseEvidence } from "@/components/signals/insights/evidence";

const FEED_LIMIT = 100;

/** Infer the market from the symbol shape — insight rows don't carry it.
    Pairs like BTC/USD contain "/" → crypto; bare tickers → stocks. */
function inferMarket(symbol: string): "crypto" | "stocks" {
  return symbol.includes("/") ? "crypto" : "stocks";
}

function byTsDesc(a: Insight, b: Insight): number {
  return (b.ts ?? 0) - (a.ts ?? 0);
}

function InsightCard({ ins }: { ins: Insight }) {
  const mode = useViewMode();
  const isMarket = ins.scope === "market";
  const symbol = ins.symbol ?? "";
  const kind = evidenceKind(parseEvidence(ins.data));
  return (
    <article
      className="px-4 py-4 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <div className="flex flex-wrap items-center gap-2">
        {isMarket ? (
          <span
            className="chip"
            style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
          >
            market
          </span>
        ) : (
          <>
            <span className="chip">symbol</span>
            {symbol && (
              <Link
                href={`/s/${inferMarket(symbol)}/${encodeURIComponent(symbol)}`}
                className="chip mono cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                title={`open the ${symbol} symbol page`}
              >
                {symbol}
              </Link>
            )}
          </>
        )}
        {kind && (
          <span className="chip" style={{ color: "var(--faint)" }} title={`data.kind=${kind}`}>
            {kindLabel(kind)}
          </span>
        )}
        <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {ago(ins.ts)}
        </span>
      </div>
      <h2 className="mt-2 text-sm font-bold" style={{ color: "var(--text)" }}>
        {ins.headline || "(untitled insight)"}
      </h2>
      {ins.body && (
        <p
          className="mt-1.5 whitespace-pre-wrap text-[0.75rem] leading-relaxed"
          style={{ color: "var(--dim)" }}
        >
          {ins.body}
        </p>
      )}
      {/* the receipts: labeled bullets from the raw evidence blob */}
      <EvidencePanel data={ins.data} />
      {/* honesty line — stays on EVERY item, chip-visible, never a tooltip */}
      <p className="mt-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {mode === "simple"
          ? "written from stored data — a description, not a forecast"
          : "measured tendency from stored data · not a forecast"}
      </p>
    </article>
  );
}

export default function InsightsPage() {
  // base feed: unfiltered window — drives header chips + kind counts + "all"
  const [insights, setInsights] = useState<Insight[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  // kind feed: server-side ?kind= results when a real kind chip is active
  const [kind, setKind] = useState<string>(ALL_KINDS);
  const [kindRows, setKindRows] = useState<Insight[] | null>(null);
  const [kindErr, setKindErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  const serverKind = kind !== ALL_KINDS && kind !== UNLABELED;

  // resets live here (event handler), not in the effect — avoids the
  // set-state-in-effect cascade; the effect below only fetches.
  const selectKind = (k: string) => {
    setKind(k);
    setKindRows(null);
    setKindErr(null);
  };

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .insights(FEED_LIMIT)
        .then((rows) => {
          if (!alive) return;
          setInsights(Array.isArray(rows) ? rows : []);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // POLL_DEFAULT tier — managed loop (hidden-tab pause, failure backoff).
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

  // server-side kind query — re-fetched (and re-polled) per selected kind
  useEffect(() => {
    if (!serverKind) return;
    let alive = true;
    const load = () =>
      api
        .insights(FEED_LIMIT, kind)
        .then((rows) => {
          if (!alive) return;
          setKindRows(Array.isArray(rows) ? rows : []);
          setKindErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setKindErr(e instanceof Error ? e.message : String(e));
        });
    load();
    // POLL_DEFAULT tier — same cadence as the unfiltered window above.
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [kind, serverKind, retryTick]);

  const sorted = useMemo(() => [...(insights ?? [])].sort(byTsDesc), [insights]);

  // distinct kinds present in the window, with counts (+ the untagged rest)
  const kindInfo = useMemo(() => {
    const counts = new Map<string, number>();
    let unlabeled = 0;
    for (const i of sorted) {
      const k = evidenceKind(parseEvidence(i.data));
      if (k) counts.set(k, (counts.get(k) ?? 0) + 1);
      else unlabeled++;
    }
    return { counts, unlabeled };
  }, [sorted]);

  const kindOptions = useMemo<KindOption[]>(() => {
    const opts: KindOption[] = [
      {
        key: ALL_KINDS,
        label: "all",
        count: sorted.length,
        title: `every insight in the latest window (last ${FEED_LIMIT})`,
      },
    ];
    const entries = [...kindInfo.counts.entries()].sort(
      (a, b) => b[1] - a[1] || a[0].localeCompare(b[0]),
    );
    for (const [k, c] of entries) {
      opts.push({
        key: k,
        label: kindLabel(k),
        count: c,
        title: `data.kind=${k} — re-queried server-side via ?kind=`,
      });
    }
    if (kindInfo.unlabeled > 0) {
      opts.push({
        key: UNLABELED,
        label: "unlabeled",
        count: kindInfo.unlabeled,
        title: "insights whose evidence blob carries no kind tag — filtered locally",
      });
    }
    // keep the active chip visible even if its kind rolled out of the window
    if (serverKind && !kindInfo.counts.has(kind)) {
      opts.push({
        key: kind,
        label: kindLabel(kind),
        count: kindRows?.length ?? 0,
        title: `data.kind=${kind} — re-queried server-side via ?kind=`,
      });
    }
    return opts;
  }, [sorted.length, kindInfo, serverKind, kind, kindRows]);

  // null = the active server-side kind query is still loading
  const visible: Insight[] | null = useMemo(() => {
    if (serverKind) return kindRows === null ? null : [...kindRows].sort(byTsDesc);
    if (kind === UNLABELED)
      return sorted.filter((i) => !evidenceKind(parseEvidence(i.data)));
    return sorted;
  }, [serverKind, kindRows, kind, sorted]);

  const loading = insights === null && err === null;
  const hardError = insights === null && err !== null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">INSIGHTS</h1>
        {insights !== null && (
          <span className="chip tnum">{sorted.length} stored</span>
        )}
        {insights !== null && sorted.length > 0 && (
          <span className="chip tnum">latest {ago(sorted[0].ts)}</span>
        )}
        {err !== null && insights !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="signals-insights"
        text="What has the AI written about your symbols and the market — daily briefings and notes, grounded only in data the platform actually stored? Expand any item's evidence to see the exact numbers behind the sentence."
      />

      {/* what this page is */}
      <section className="panel">
        <div className="panel-h">HOW TO READ THIS FEED</div>
        <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          Every insight is generated from stored data with the numbers inline — headlines state
          measured tendencies, never forecasts. The evidence expander on each item shows the raw
          inputs it was written from; kind chips re-query the daemon server-side, never hide rows.
        </p>
      </section>

      {/* fleet-wide headline attention — a context strip above the feed;
          renders nothing until tokens exist (absent beats a dead panel) */}
      <TrendingTokensStrip />

      {loading && <Skeleton lines={4} label="loading insights" />}

      {hardError && (
        <ErrorState
          message={err ?? "insights unavailable"}
          hint="Is the daemon running? Start it with signaldeckd."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {insights !== null && (
        <section className="panel">
          <div className="panel-h">
            FEED
            {serverKind && kindRows !== null && (
              <span className="chip tnum ml-2" title={`GET /api/insights?kind=${kind}`}>
                server-filtered · {kindRows.length}
              </span>
            )}
            {serverKind && kindErr !== null && kindRows !== null && (
              <span
                className="chip ml-2"
                style={{ color: "var(--bad)", borderColor: "var(--bad)" }}
              >
                kind poll failed — showing last data
              </span>
            )}
            <div className="ml-auto">
              <KindChips options={kindOptions} active={kind} onSelect={selectKind} />
            </div>
          </div>

          {visible === null ? (
            kindErr !== null ? (
              <ErrorState
                message={kindErr}
                hint={`The server-side ?kind=${kind} query failed — is the daemon running?`}
                retry={() => {
                  setKindErr(null);
                  setRetryTick((t) => t + 1);
                }}
              />
            ) : (
              <Skeleton lines={4} label={`loading ${kindLabel(kind)} insights`} />
            )
          ) : visible.length === 0 ? (
            sorted.length === 0 ? (
              <EmptyState
                className="m-4"
                message="No insights yet"
                detail="The insight-writer runs every 15 minutes once data flows — check back shortly."
              />
            ) : (
              <EmptyState
                className="m-4"
                message="Nothing matches this kind"
                detail="Try the “all” chip to see every stored insight."
              />
            )
          ) : (
            <div>
              {visible.map((ins) => (
                <InsightCard key={ins.id} ins={ins} />
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}
