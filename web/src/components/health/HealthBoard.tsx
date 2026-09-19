"use client";

/**
 * HEALTH BOARD — SignalDeck grading its own user experience, in public.
 *
 * Two evidence streams, one score:
 *   - `/ux-score.json`, written by `e2e/ux-audit.spec.ts` (the crawl)
 *   - the counters in localStorage, written by UxProbe (the behaviour)
 *
 * The crawl JSON is a `SiteScore` that was computed WITHOUT behaviour, so this
 * component pulls the raw measurements back out and re-scores them with the
 * live signals blended in. That way there is exactly one scoring implementation
 * — `scoreSite` — and the page can never disagree with the report file.
 */

import type { ReactElement } from "react";
import { useEffect, useState } from "react";
import Link from "next/link";
import {
  CATEGORIES,
  LEVELS,
  levelFor,
  scoreSite,
  MIN_SESSIONS,
  HEALTH_STALE_DAYS,
  type PageMeasurement,
  type SiteScore,
} from "@/lib/rubric";
import {
  confidenceLabel,
  confidenceScore,
  exportSignals,
  resetSignals,
  useSignals,
} from "@/lib/ux";

const HISTORY_KEY = "sd-ux-history";
const MAX_HISTORY = 20;

interface HistoryPoint {
  at: string;
  overall: number;
  /**
   * How many routes that crawl covered.
   *
   * A score is an AVERAGE OVER ROUTES, so two crawls are only comparable when
   * they averaged the same set. Adding 16 routes to the audit moved the number
   * 80.8 -> 79.7 with nothing about the app changed, and the trend happily
   * rendered that as "-1 since the first check" -- a delta that measured the
   * route list, not the product.
   *
   * Optional because points written before this existed carry no count. They
   * are kept (the history is a user's own record and is not worth discarding)
   * but they can never match a known count, so they are excluded from the
   * delta rather than silently compared against.
   */
  pages?: number;
}

function readHistory(): HistoryPoint[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(HISTORY_KEY) ?? "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (p): p is HistoryPoint =>
        typeof p === "object" && p !== null && typeof (p as HistoryPoint).at === "string",
    );
  } catch {
    return [];
  }
}

/** Append one point per distinct crawl, so reloading the page cannot pad the trend. */
function recordHistory(at: string, overall: number, pages: number): HistoryPoint[] {
  const history = readHistory();
  const existing = history.findIndex((p) => p.at === at);
  if (existing >= 0 && history[existing].pages !== undefined) return history;

  // A point written before `pages` existed is UPGRADED rather than skipped.
  // Without this, any crawl a viewer had already loaded stayed coverage-less
  // forever: the early return fired on the matching timestamp, the point never
  // learned how many routes it covered, and it could therefore never serve as a
  // baseline. The score it holds is still correct, and the count we are filling
  // in is the count of the crawl it was written from -- the same `at`.
  const next =
    existing >= 0
      ? history.map((p, i) => (i === existing ? { ...p, pages } : p))
      : [...history, { at, overall, pages }].slice(-MAX_HISTORY);
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(next));
  } catch {
    // Trend is a nicety; the score itself does not depend on it.
  }
  return next;
}

function tone(score: number | null): string {
  if (score === null) return "var(--faint)";
  if (score >= 7.5) return "var(--ok)";
  if (score >= 6) return "var(--warn, var(--accent))";
  return "var(--bad, var(--down))";
}

function Bar({ score }: { score: number | null }): ReactElement {
  return (
    <div
      aria-hidden="true"
      style={{ height: 4, background: "var(--border)", borderRadius: 999, minWidth: 60 }}
    >
      <div
        style={{
          width: `${((score ?? 0) / 10) * 100}%`,
          height: "100%",
          background: tone(score),
          borderRadius: 999,
          transition: "width 400ms ease",
        }}
      />
    </div>
  );
}

const fmt = (n: number | null, digits = 1) => (n === null ? "—" : n.toFixed(digits));

export default function HealthBoard(): ReactElement {
  const [crawl, setCrawl] = useState<SiteScore | null>(null);
  const [missing, setMissing] = useState(false);
  const [history, setHistory] = useState<HistoryPoint[]>([]);
  // Whole days since the crawl, or null when the file carries no usable date --
  // which must read as "unknown" and never as "fresh".
  //
  // Held in STATE and stamped in the effect below, not derived during render.
  // Date.now() in a render body is impure (react-hooks/purity caught it): the
  // same props would produce different output on the server and the client, and
  // a concurrent re-render could show a different age with no data change.
  const [staleDays, setStaleDays] = useState<number | null>(null);
  const signals = useSignals();

  useEffect(() => {
    let live = true;
    fetch("/ux-score.json", { cache: "no-store" })
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))))
      .then((json: SiteScore) => {
        if (!live) return;
        setCrawl(json);
        setHistory(recordHistory(json.generatedAt, json.overall, json.pagesCrawled));
        const ms = json.generatedAt ? Date.parse(json.generatedAt) : NaN;
        setStaleDays(
          Number.isFinite(ms) ? Math.floor((Date.now() - ms) / 86_400_000) : null,
        );
      })
      .catch(() => live && setMissing(true));
    return () => {
      live = false;
    };
  }, []);

  if (missing) {
    return (
      <section className="panel">
        <div className="panel-h">
          <span>NOT GRADED YET</span>
        </div>
        <div className="px-4 py-4 sm:px-5" style={{ color: "var(--dim)" }}>
          <p>
            SignalDeck has not checked itself yet. The check crawls every page, measures how
            clear and usable each one is, and writes the score here.
          </p>
          <p className="mt-2">
            Run <code>npm run ux:audit</code> and reload this page.
          </p>
        </div>
      </section>
    );
  }

  if (!crawl) {
    return (
      <section className="panel" aria-busy="true">
        <div className="panel-h">
          <span>CHECKING&hellip;</span>
        </div>
        <div className="px-4 py-4 sm:px-5" style={{ color: "var(--dim)" }}>
          Loading the last self-check.
        </div>
      </section>
    );
  }

  // Re-score with behaviour blended in. One scoring implementation, two inputs.
  const measurements: PageMeasurement[] = crawl.pages.map((p) => p.measurement);
  const site = scoreSite(measurements, signals, crawl.generatedAt);
  const level = site.level;
  const confidence = confidenceScore(signals);
  const enoughSessions = signals.sessions >= MIN_SESSIONS;

  // Compare only crawls that covered the SAME routes. The baseline is the
  // earliest point whose coverage matches this one; when there is no such
  // point -- the first run at a new coverage, which is exactly when the number
  // jumps for reasons that have nothing to do with the app -- there is no
  // honest delta to show, so none is shown.
  const comparable = history.filter((p) => p.pages === crawl.pagesCrawled);
  const first = comparable[0];
  const delta =
    first && comparable.length > 1 && first.at !== crawl.generatedAt
      ? site.overall - first.overall
      : null;

  return (
    <div className="flex flex-col gap-4">
      {/* ── The score ─────────────────────────────────────────────────── */}
      <section className="panel" aria-label="overall self-grade">
        <div className="panel-h">
          <span>SIGNALDECK HEALTH SCORE</span>
          <span className="ml-auto tnum" style={{ color: "var(--dim)" }}>
            {crawl.pagesCrawled} pages checked
          </span>
        </div>
        <div className="flex flex-wrap items-end gap-x-6 gap-y-2 px-4 py-4 sm:px-5">
          <div className="flex items-baseline gap-2">
            <span
              className="tnum"
              style={{ fontSize: "3rem", lineHeight: 1, color: tone(site.overall / 10) }}
            >
              {Math.round(site.overall)}
            </span>
            <span style={{ color: "var(--faint)" }}>/ 100</span>
          </div>
          <div className="flex flex-col gap-1">
            <strong style={{ color: tone(site.overall / 10), letterSpacing: "0.08em" }}>
              {level.label.toUpperCase()}
            </strong>
            <span style={{ color: "var(--dim)", maxWidth: "48ch" }}>{level.meaning}</span>
          </div>
          {delta !== null && (
            <div className="ml-auto text-right">
              <div className="tnum" style={{ color: delta >= 0 ? "var(--ok)" : "var(--down)" }}>
                {delta >= 0 ? "+" : ""}
                {delta.toFixed(0)}
              </div>
              <div style={{ color: "var(--faint)", fontSize: "0.75rem" }}>
                since the first check of all {crawl.pagesCrawled} pages
              </div>
            </div>
          )}
        </div>
        <div
          className="border-t px-4 py-2 text-[0.8rem] sm:px-5"
          style={{ borderColor: "var(--border)", color: "var(--faint)" }}
        >
          Checked {crawl.generatedAt ? new Date(crawl.generatedAt).toLocaleString() : "—"}.{" "}
          {/* A DATE IS NOT A FRESHNESS CLAIM. The score comes from
              e2e/ux-audit.spec.ts, which is a manual `npm run ux:audit` that no
              pipeline and no scheduled task invokes — so this number only moves
              when a person remembers to move it, and it was four days old when
              that was noticed. The date was already printed, but a reader has to
              subtract to learn anything from it, and an unqualified "81 / 100"
              above reads as current however old it is.
              Said out loud past the threshold, in the same voice the rest of
              this page uses about its own limits. */}
          {staleDays !== null && staleDays >= HEALTH_STALE_DAYS && (
            <>
              <strong style={{ color: "var(--warn, var(--text))" }}>
                That is {staleDays} days ago — this score describes the app as it was then, not
                as it is now.
              </strong>{" "}
              The crawl is run by hand (<code>npm run ux:audit</code>); nothing refreshes it on a
              schedule.{" "}
            </>
          )}
          {enoughSessions
            ? `Includes how ${signals.sessions} real visits actually went.`
            : `Based on the pages alone — real-visit signals join in at ${MIN_SESSIONS} sessions (currently ${signals.sessions}).`}{" "}
          {/* Stated, not hidden: the crawl measures a live app whose data
              changes between runs, so two checks of unchanged code differ by
              about half a point. Without this line a 1-point move reads as
              progress. */}
          The check runs against live data, so repeat runs vary by roughly
          &plusmn;0.5. Treat a move smaller than that as noise.
        </div>
      </section>

      {/* ── The one thing to fix ──────────────────────────────────────── */}
      {site.weakest && (
        <section className="panel" aria-label="weakest area">
          <div className="panel-h">
            <span>FIX THIS FIRST</span>
          </div>
          <div className="flex flex-col gap-2 px-4 py-4 sm:px-5">
            <div>
              <strong style={{ color: "var(--text)" }}>{site.weakest.category.label}</strong>{" "}
              <span className="tnum" style={{ color: tone(site.weakest.score) }}>
                {fmt(site.weakest.score)}/10
              </span>{" "}
              <span style={{ color: "var(--faint)" }}>
                — {site.weakest.category.question}
              </span>
            </div>
            <p style={{ color: "var(--dim)", maxWidth: "68ch" }}>{site.recommendation}</p>
          </div>
        </section>
      )}

      {/* ── Category breakdown ────────────────────────────────────────── */}
      <section className="panel" aria-label="scores by category">
        <div className="panel-h">
          <span>WHAT WAS GRADED</span>
        </div>
        <div className="overflow-x-auto">
          <table className="v4-table w-full text-[0.82rem]">
            <thead>
              <tr>
                <th className="text-left">Category</th>
                <th className="text-left">What it asks</th>
                <th className="text-right">Score</th>
                <th className="text-left">How it&rsquo;s doing</th>
                <th className="text-right">Pages</th>
                <th className="text-right">Behaviour</th>
                <th className="text-left">Weakest pages</th>
              </tr>
            </thead>
            <tbody>
              {site.categories.map((c) => (
                <tr key={c.category.key}>
                  <td style={{ color: "var(--text)" }}>{c.category.label}</td>
                  <td style={{ color: "var(--faint)" }}>{c.category.question}</td>
                  <td className="tnum text-right" style={{ color: tone(c.score) }}>
                    {fmt(c.score)}
                  </td>
                  <td style={{ minWidth: 80 }}>
                    <Bar score={c.score} />
                  </td>
                  <td className="tnum text-right" style={{ color: "var(--dim)" }}>
                    {fmt(c.fromPages)}
                  </td>
                  <td className="tnum text-right" style={{ color: "var(--dim)" }}>
                    {fmt(c.fromSignals)}
                  </td>
                  <td style={{ color: "var(--faint)", fontSize: "0.75rem" }}>
                    {c.worstRoutes.join(", ") || "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div
          className="border-t px-4 py-2 text-[0.75rem] sm:px-5"
          style={{ borderColor: "var(--border)", color: "var(--faint)" }}
        >
          &ldquo;Pages&rdquo; is what the crawler measured. &ldquo;Behaviour&rdquo; is how real
          visits went. A dash means there is not enough evidence yet — it is never a guess.
        </div>
      </section>

      {/* ── Live confidence ───────────────────────────────────────────── */}
      <section className="panel" aria-label="user confidence">
        <div className="panel-h">
          <span>USER CONFIDENCE SCORE</span>
          <span className="ml-auto" style={{ color: "var(--faint)", fontSize: "0.75rem" }}>
            this browser only
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3 sm:px-5">
          <span className="tnum" style={{ fontSize: "2rem", color: tone(confidence / 10) }}>
            {confidence}
          </span>
          <div className="flex flex-col">
            <strong style={{ color: "var(--text)" }}>{confidenceLabel(confidence)}</strong>
            <span style={{ color: "var(--faint)", fontSize: "0.78rem" }}>
              {signals.sessions === 0
                ? "No visits recorded yet."
                : `From ${signals.sessions} visit${signals.sessions === 1 ? "" : "s"} on this device.`}
            </span>
          </div>
        </div>
        <ul className="grid grid-cols-2 gap-x-4 px-4 pb-3 text-[0.8rem] sm:grid-cols-4 sm:px-5">
          {(
            [
              ["Got stuck", signals.stalls, "90s+ on a page without touching anything"],
              ["Clicked nothing", signals.deadClicks, "clicked something that wasn't a button"],
              ["Went back and forth", signals.backThrash, "returned to a page within 20 seconds"],
              ["Asked for help", signals.helpOpens, "opened the help panel"],
              ["Finished a step", signals.tasksCompleted, "completed a setup step"],
              ["Took a suggestion", signals.promptsTaken, "clicked a suggested next step"],
              ["Left without doing anything", signals.bounces, "a visit with no interaction"],
              ["Lost patience", signals.rageClicks, "clicked the same spot repeatedly"],
            ] as const
          ).map(([label, value, why]) => (
            <li key={label} className="flex flex-col py-1">
              <span className="tnum" style={{ color: "var(--text)" }}>
                {value}
              </span>
              <span style={{ color: "var(--dim)" }}>{label}</span>
              <span style={{ color: "var(--faint)", fontSize: "0.7rem" }}>{why}</span>
            </li>
          ))}
        </ul>
        <div
          className="flex flex-wrap gap-3 border-t px-4 py-2 sm:px-5"
          style={{ borderColor: "var(--border)" }}
        >
          <button
            type="button"
            className="inline-flex min-h-[40px] cursor-pointer items-center hover:underline"
            style={{ color: "var(--accent)" }}
            onClick={() => {
              void navigator.clipboard?.writeText(exportSignals());
            }}
          >
            Copy my signals
          </button>
          <button
            type="button"
            className="inline-flex min-h-[40px] cursor-pointer items-center hover:underline"
            style={{ color: "var(--dim)" }}
            onClick={resetSignals}
          >
            Clear my signals
          </button>
        </div>
      </section>

      {/* ── Trend ─────────────────────────────────────────────────────── */}
      {history.length > 1 && (
        <section className="panel" aria-label="score over time">
          <div className="panel-h">
            <span>IS IT GETTING BETTER?</span>
          </div>
          <ul className="flex flex-col px-4 py-2 text-[0.8rem] sm:px-5">
            {history
              .slice()
              .reverse()
              .map((p, i, arr) => {
                const prev = arr[i + 1];
                const change = prev ? p.overall - prev.overall : null;
                return (
                  <li key={p.at} className="flex items-center gap-3 py-1">
                    <span className="tnum" style={{ color: "var(--faint)", minWidth: "11ch" }}>
                      {new Date(p.at).toLocaleDateString()}
                    </span>
                    <span className="tnum" style={{ color: "var(--text)", minWidth: "4ch" }}>
                      {Math.round(p.overall)}
                    </span>
                    <span style={{ color: "var(--faint)" }}>{levelFor(p.overall).label}</span>
                    {change !== null && (
                      <span
                        className="tnum ml-auto"
                        style={{ color: change >= 0 ? "var(--ok)" : "var(--down)" }}
                      >
                        {change >= 0 ? "+" : ""}
                        {change.toFixed(0)}
                      </span>
                    )}
                  </li>
                );
              })}
          </ul>
        </section>
      )}

      {/* ── The rubric ────────────────────────────────────────────────── */}
      <section className="panel" aria-label="how the score is read">
        <div className="panel-h">
          <span>HOW TO READ THE SCORE</span>
        </div>
        <div className="overflow-x-auto">
          <table className="v4-table w-full text-[0.82rem]">
            <thead>
              <tr>
                <th className="text-left">Score</th>
                <th className="text-left">Level</th>
                <th className="text-left">What it means</th>
                <th className="text-left">What we do about it</th>
              </tr>
            </thead>
            <tbody>
              {LEVELS.map((l) => {
                const here = l.key === level.key;
                return (
                  <tr
                    key={l.key}
                    style={{ background: here ? "var(--panel-alt, transparent)" : undefined }}
                  >
                    <td className="tnum">
                      {l.min}&ndash;{l.max}
                    </td>
                    <td style={{ color: here ? "var(--text)" : "var(--dim)" }}>
                      {l.label}
                      {here && (
                        <span style={{ color: "var(--accent)" }}> &larr; we are here</span>
                      )}
                    </td>
                    <td style={{ color: "var(--dim)" }}>{l.meaning}</td>
                    <td style={{ color: "var(--faint)" }}>{l.action}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <p className="px-1 text-[0.78rem]" style={{ color: "var(--faint)" }}>
        This grade covers how usable SignalDeck is, not whether its forecasts are any good.
        For that, see <Link href="/lab/track-record" style={{ color: "var(--accent)" }}>the track record</Link>.{" "}
        Categories are weighted: {CATEGORIES.map((c) => `${c.label} ${Math.round(c.weight * 100)}%`).join(", ")}.
      </p>
    </div>
  );
}
