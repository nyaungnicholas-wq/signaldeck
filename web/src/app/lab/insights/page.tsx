"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, pollMs, POLL_DEFAULT, type Insight } from "@/lib/api";
import { ago } from "@/lib/format";
import ReportLink from "@/components/signals/ReportLink";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useViewMode } from "@/components/Plain";
import EvidencePanel from "@/components/signals/insights/EvidencePanel";
import TrendingTokensStrip from "@/components/signals/insights/TrendingTokensStrip";
import KindChips, {
  ALL_KINDS,
  UNLABELED,
  type KindOption,
} from "@/components/signals/insights/KindChips";
import { evidenceKind, kindLabel, parseEvidence } from "@/components/signals/insights/evidence";
import { PageHero, StatTile, Reveal } from "@/components/ui/Kit";
import GoalBanner from "@/components/home/GoalBanner";
import ShowAllBar from "@/components/ShowAllBar";
import { insightsLayoutFor, useGoal } from "@/lib/goal";

const FEED_LIMIT = 100;

function inferMarket(symbol: string): "crypto" | "stocks" {
  return symbol.includes("/") ? "crypto" : "stocks";
}

function byTsDesc(a: Insight, b: Insight): number {
  return (b.ts ?? 0) - (a.ts ?? 0);
}

function InsightCard({ ins, i }: { ins: Insight; i: number }) {
  const mode = useViewMode();
  const isMarket = ins.scope === "market";
  const symbol = ins.symbol ?? "";
  const kind = evidenceKind(parseEvidence(ins.data));
  return (
    <article
      className="panel reveal-item px-4 py-4"
      style={{ "--i": i } as React.CSSProperties}
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
              <>
                <Link
                  href={`/s/${inferMarket(symbol)}/${encodeURIComponent(symbol)}`}
                  className="chip mono cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                  title={`open the ${symbol} symbol page`}
                >
                  {symbol}
                </Link>
                <ReportLink symbol={symbol} market={inferMarket(symbol)} kind="overview" />
              </>
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
      <EvidencePanel data={ins.data} />
      <p className="mt-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {mode === "simple"
          ? "written from stored data — a description, not a forecast"
          : "measured tendency from stored data · not a forecast"}
      </p>
    </article>
  );
}

export default function InsightsPage() {
  const [insights, setInsights] = useState<Insight[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [kind, setKind] = useState<string>(ALL_KINDS);
  const [kindRows, setKindRows] = useState<Insight[] | null>(null);
  const [kindErr, setKindErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);
  const goal = useGoal();
  const layout = insightsLayoutFor(goal);
  // Lifting the cap is a per-visit choice, not a stored preference — the goal
  // owns the default and this only overrides it while you are here.
  const [expanded, setExpanded] = useState(false);

  const serverKind = kind !== ALL_KINDS && kind !== UNLABELED;

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
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [retryTick]);

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
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [kind, serverKind, retryTick]);

  const sorted = useMemo(() => [...(insights ?? [])].sort(byTsDesc), [insights]);

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

  const visible: Insight[] | null = useMemo(() => {
    if (serverKind) return kindRows === null ? null : [...kindRows].sort(byTsDesc);
    if (kind === UNLABELED)
      return sorted.filter((i) => !evidenceKind(parseEvidence(i.data)));
    return sorted;
  }, [serverKind, kindRows, kind, sorted]);

  // The cap is applied AFTER the kind filter, so switching kinds always shows
  // the newest matching cards rather than an arbitrary slice of the old set.
  const drawn: Insight[] =
    visible === null || layout.cardLimit === null || expanded
      ? (visible ?? [])
      : visible.slice(0, layout.cardLimit);

  const loading = insights === null && err === null;
  const hardError = insights === null && err !== null;

  const latestTs = sorted.length > 0 ? sorted[0].ts : null;
  const stats = useMemo(() => {
    if (!insights) return { total: 0, kinds: 0, unlabeled: 0 };
    return {
      total: sorted.length,
      kinds: kindInfo.counts.size + (kindInfo.unlabeled > 0 ? 1 : 0),
      unlabeled: kindInfo.unlabeled,
    };
  }, [insights, sorted.length, kindInfo]);

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Insights"
        subtitle="AI-generated notes and briefings from stored data, showing measured tendencies — not forecasts."
        right={
          <div className="flex items-center gap-2">
            {insights !== null && (
              <span className="chip tnum">{stats.total} stored</span>
            )}
            {latestTs !== null && (
              <span className="chip tnum">latest {ago(latestTs)}</span>
            )}
            {err !== null && insights !== null && (
              <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
                poll failed — showing last data
              </span>
            )}
          </div>
        }
      />

      {insights !== null && sorted.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-3">
          {/* stats.total is sorted.length, i.e. the FEED_LIMIT=100 window —
              a page size, not the number stored. */}
          <StatTile
            label="Insights Shown"
            value={stats.total}
            i={0}
          />
          <StatTile
            label="Distinct Kinds"
            value={stats.kinds}
            i={1}
          />
          <StatTile
            label="Unlabeled"
            value={stats.unlabeled}
            sub={stats.unlabeled > 0 ? `${Math.round((stats.unlabeled / stats.total) * 100)}% of shown` : undefined}
            i={2}
          />
        </div>
      )}

      <GoalBanner note={layout.note} />

      {/* The primer is exactly what a newcomer needs and exactly what someone
          on their fiftieth visit scrolls past. It leads for two goals and
          folds for the third — demoted, never deleted. */}
      {layout.showPrimer ? (
        <Reveal className="grid gap-3">
          <div className="panel reveal-item" style={{ "--i": 0 } as React.CSSProperties}>
            <div className="panel-h">HOW TO READ THIS FEED</div>
            <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
              Every insight is generated from stored data with the numbers inline — headlines state
              measured tendencies, never forecasts. The evidence expander on each item shows the raw
              inputs it was written from; kind chips re-query the daemon server-side, never hide rows.
            </p>
          </div>
        </Reveal>
      ) : (
        <details className="panel">
          <summary
            className="flex min-h-[44px] cursor-pointer list-none items-center px-4 py-2 text-[0.8rem] sm:px-5"
            style={{ color: "var(--dim)" }}
          >
            How to read this feed
          </summary>
          <p
            className="border-t px-4 py-3 text-[0.75rem] leading-relaxed"
            style={{ borderColor: "var(--border)", color: "var(--dim)" }}
          >
            Every insight is generated from stored data with the numbers inline — headlines state
            measured tendencies, never forecasts. The evidence expander on each item shows the raw
            inputs it was written from; kind chips re-query the daemon server-side, never hide rows.
          </p>
        </details>
      )}

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
        <section className="hud-panel">
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
            <>
              <Reveal className="grid gap-3">
                {drawn.map((ins, i) => (
                  <InsightCard key={ins.id} ins={ins} i={i} />
                ))}
              </Reveal>
              <ShowAllBar
                shown={drawn.length}
                total={visible.length}
                limit={layout.cardLimit}
                expanded={expanded}
                onToggle={setExpanded}
                noun="insights"
              />
            </>
          )}
        </section>
      )}
    </div>
  );
}
