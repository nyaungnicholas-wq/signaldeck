"use client";

// /portfolio — a discretionary trade log graded against what actually happened,
// plus a correlation heatmap so you can see what actually diversifies.
//
// Honesty framing is the product: every logged read carries the pressure score
// captured at entry, so the same out-of-sample honesty loop the model runs on
// itself is applied to YOUR calls. Positions poll every 5s; correlation is a
// heavier query, so it loads once and refreshes on demand.

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import {
  api,
  pollMs,
  type CorrelationResponse,
  type Market,
  type PortfolioResponse,
  type PositionRow,
  type WatchRow,
} from "@/lib/api";
import { ago, fmtPct, fmtPrice, fmtScore, fmtTs, scoreColor } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import PagePurpose from "@/components/PagePurpose";

// ── correlation cell color ──────────────────────────────────────────────
// +correlation (move together) → red (--ask); −correlation (diversifying) →
// green (--bid). Diagonal (self, r=1) is muted so the eye reads the off-axis.
function corrCell(r: number): { bg: string; fg: string } {
  if (!isFinite(r)) return { bg: "var(--panel2)", fg: "var(--faint)" };
  const a = Math.min(1, Math.abs(r));
  if (r >= 0) return { bg: `rgba(248,113,113,${(0.12 + a * 0.5).toFixed(3)})`, fg: "var(--text)" };
  return { bg: `rgba(52,211,153,${(0.12 + a * 0.5).toFixed(3)})`, fg: "var(--text)" };
}

function fmtR(r: number): string {
  if (!isFinite(r)) return "—";
  return `${r >= 0 ? "" : "−"}${Math.abs(r).toFixed(2)}`;
}

// ── summary bar ─────────────────────────────────────────────────────────
function SummaryBar({ stat }: { stat: PortfolioResponse["stat"] }) {
  const cells: { label: string; value: React.ReactNode }[] = [
    {
      label: "gross value",
      value: <span className="tnum">{fmtPrice(stat.GrossValue)}</span>,
    },
    {
      label: "total P&L",
      value: (
        <span className="tnum" style={{ color: scoreColor(stat.TotalPnLPct) }}>
          {fmtPct(stat.TotalPnLPct)}
          <span className="ml-1.5 text-[0.78rem]" style={{ color: "var(--faint)" }}>
            {stat.TotalPnLAbs >= 0 ? "+" : "−"}
            {fmtPrice(Math.abs(stat.TotalPnLAbs))}
          </span>
        </span>
      ),
    },
    {
      label: "winners / losers",
      value: (
        <span className="tnum">
          <span style={{ color: "var(--bid)" }}>{stat.Winners}</span>
          <span style={{ color: "var(--faint)" }}> / </span>
          <span style={{ color: "var(--ask)" }}>{stat.Losers}</span>
        </span>
      ),
    },
    {
      label: "best",
      value: stat.Best ? (
        <span style={{ color: "var(--bid)" }}>{stat.Best}</span>
      ) : (
        <span style={{ color: "var(--faint)" }}>—</span>
      ),
    },
    {
      label: "worst",
      value: stat.Worst ? (
        <span style={{ color: "var(--ask)" }}>{stat.Worst}</span>
      ) : (
        <span style={{ color: "var(--faint)" }}>—</span>
      ),
    },
  ];
  return (
    <section className="panel">
      <div className="panel-h">POSITIONS SUMMARY</div>
      <div className="grid grid-cols-2 gap-px sm:grid-cols-3 lg:grid-cols-5" style={{ background: "var(--border)" }}>
        {cells.map((c) => (
          <div key={c.label} className="px-4 py-3" style={{ background: "var(--panel)" }}>
            <div
              className="text-[0.75rem] tracking-wide"
              style={{ color: "var(--faint)" }}
              title={c.label === "total P&L" ? "total profit and loss across logged positions" : undefined}
            >
              {c.label}
            </div>
            <div className="mt-1 text-[0.9rem] font-semibold">{c.value}</div>
          </div>
        ))}
      </div>
    </section>
  );
}

// ── log-a-position form ─────────────────────────────────────────────────
function LogForm({
  watch,
  onLogged,
}: {
  watch: WatchRow[] | null;
  onLogged: () => void;
}) {
  const [pick, setPick] = useState("");
  const [qty, setQty] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  const options = useMemo(
    () =>
      (watch ?? [])
        .filter((w) => w.active)
        .map((w) => ({ key: `${w.market}:${w.symbol}`, symbol: w.symbol, market: w.market }))
        .sort((a, b) => a.symbol.localeCompare(b.symbol)),
    [watch],
  );

  const qtyNum = Number(qty);
  const chosen = options.find((o) => o.key === pick) ?? null;
  const valid = chosen !== null && isFinite(qtyNum) && qtyNum > 0;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!valid || !chosen || busy) return;
    setBusy(true);
    setErr(null);
    setOk(null);
    try {
      const res = await api.portfolioAdd(chosen.symbol, chosen.market, qtyNum, note.trim());
      setOk(
        `logged ${chosen.symbol} at ${fmtPrice(res.entryPrice)} · score ${fmtScore(res.scoreAtEntry)} captured`,
      );
      setQty("");
      setNote("");
      setPick("");
      onLogged();
    } catch (x) {
      setErr(x instanceof Error ? x.message : String(x));
    } finally {
      setBusy(false);
    }
  };

  const fieldStyle: React.CSSProperties = {
    background: "var(--panel2)",
    borderColor: "var(--border)",
    color: "var(--text)",
  };

  return (
    <section className="panel">
      <div className="panel-h">
        LOG A POSITION
        <span className="tnum" style={{ color: "var(--faint)" }}>
          entry price + pressure score captured server-side at latest close
        </span>
      </div>
      <form onSubmit={submit} className="flex flex-wrap items-end gap-x-4 gap-y-3 px-4 py-4 text-[0.78rem]">
        <label className="flex flex-col gap-1">
          <span style={{ color: "var(--faint)" }}>symbol</span>
          <select
            value={pick}
            onChange={(e) => setPick(e.target.value)}
            aria-label="symbol to log"
            disabled={options.length === 0}
            className="min-h-[40px] min-w-44 cursor-pointer rounded border px-2 py-1.5 text-[0.78rem] disabled:cursor-not-allowed disabled:opacity-50"
            style={fieldStyle}
          >
            <option value="">
              {options.length === 0 ? "no active symbols" : "choose a symbol…"}
            </option>
            {options.map((o) => (
              <option key={o.key} value={o.key}>
                {o.symbol} · {o.market}
              </option>
            ))}
          </select>
        </label>

        <label className="flex flex-col gap-1">
          <span style={{ color: "var(--faint)" }}>quantity</span>
          <input
            type="number"
            inputMode="decimal"
            min="0"
            step="any"
            value={qty}
            onChange={(e) => setQty(e.target.value)}
            placeholder="0"
            aria-label="quantity"
            className="tnum min-h-[40px] w-28 rounded border px-2.5 py-1.5 text-[0.78rem]"
            style={fieldStyle}
          />
        </label>

        <label className="flex min-w-48 flex-1 flex-col gap-1">
          <span style={{ color: "var(--faint)" }}>note (optional — your read)</span>
          <input
            type="text"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="why you took this — e.g. oversold bounce"
            aria-label="note"
            maxLength={280}
            className="min-h-[40px] w-full rounded border px-2.5 py-1.5 text-[0.78rem]"
            style={fieldStyle}
          />
        </label>

        <button
          type="submit"
          disabled={!valid || busy}
          className="min-h-[40px] cursor-pointer rounded border px-4 py-1.5 text-[0.78rem] font-semibold tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
          style={{
            borderColor: valid ? "var(--accent)" : "var(--border)",
            background: valid ? "rgba(251,191,36,.1)" : "var(--panel2)",
            color: valid ? "var(--text)" : "var(--dim)",
          }}
        >
          {busy ? "logging…" : "log position"}
        </button>
      </form>
      {(err || ok) && (
        <div
          className="border-t px-4 py-2 text-[0.78rem]"
          style={{ borderColor: "var(--border)", color: err ? "var(--bad)" : "var(--ok)" }}
        >
          {err ?? ok}
        </div>
      )}
    </section>
  );
}

// ── positions table ─────────────────────────────────────────────────────
function PositionsTable({
  positions,
  onClosed,
}: {
  positions: PositionRow[];
  onClosed: () => void;
}) {
  const [closingId, setClosingId] = useState<number | null>(null);
  const [rowErr, setRowErr] = useState<{ id: number; msg: string } | null>(null);

  const close = async (p: PositionRow) => {
    if (closingId !== null) return;
    setClosingId(p.id);
    setRowErr(null);
    try {
      await api.portfolioClose(p.id, p.symbol, p.market);
      onClosed();
    } catch (x) {
      setRowErr({ id: p.id, msg: x instanceof Error ? x.message : String(x) });
    } finally {
      setClosingId(null);
    }
  };

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[0.8rem]">
        <thead>
          <tr
            className="text-left text-[0.75rem] tracking-wide"
            style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}
          >
            <th className="px-3 py-2 font-medium">SYMBOL</th>
            <th className="px-2 py-2 text-right font-medium" title="quantity">QTY</th>
            <th className="px-2 py-2 text-right font-medium" title="entry price">ENTRY</th>
            <th className="px-2 py-2 text-right font-medium" title="latest price (or exit price if closed)">LAST</th>
            <th className="px-2 py-2 text-right font-medium" title="profit and loss, percent">P&L %</th>
            <th className="px-2 py-2 text-right font-medium" title="pressure score captured when you logged it">
              SCORE@ENTRY
            </th>
            <th className="px-3 py-2 font-medium">NOTE</th>
            <th className="px-2 py-2 text-right font-medium">LOGGED</th>
            <th className="px-2 py-2 font-medium">STATUS</th>
            <th className="px-2 py-2 font-medium" aria-label="actions" />
          </tr>
        </thead>
        <tbody className="tnum">
          {positions.map((p) => {
            const closing = closingId === p.id;
            const hasScore = isFinite(p.scoreAtEntry);
            return (
              <tr
                key={p.id}
                className="align-top transition-colors duration-150 hover:bg-[var(--panel2)]"
                style={{ borderBottom: "1px solid var(--border)" }}
              >
                <td className="px-3 py-2">
                  <Link
                    href={`/s/${p.market}/${encodeURIComponent(p.symbol)}`}
                    className="cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                  >
                    {p.symbol}
                  </Link>
                  <span className="ml-1.5 text-[0.75rem] font-normal" style={{ color: "var(--faint)" }}>
                    {p.market}
                  </span>
                </td>
                <td className="px-2 py-2 text-right">{p.qty}</td>
                <td className="px-2 py-2 text-right">{fmtPrice(p.entryPrice)}</td>
                <td className="px-2 py-2 text-right">
                  {p.open ? (
                    fmtPrice(p.lastPrice)
                  ) : (
                    <span title="exit price" style={{ color: "var(--dim)" }}>
                      {fmtPrice(p.exitPrice ?? p.lastPrice)}
                    </span>
                  )}
                </td>
                <td className="px-2 py-2 text-right" style={{ color: scoreColor(p.pnlPct) }}>
                  {fmtPct(p.pnlPct)}
                </td>
                <td className="px-2 py-2 text-right">
                  {hasScore ? (
                    <span style={{ color: scoreColor(p.scoreAtEntry) }}>
                      {fmtScore(p.scoreAtEntry)}
                    </span>
                  ) : (
                    <span style={{ color: "var(--faint)" }}>—</span>
                  )}
                </td>
                <td className="max-w-56 px-3 py-2">
                  {p.note ? (
                    <span className="block truncate" style={{ color: "var(--dim)" }} title={p.note}>
                      {p.note}
                    </span>
                  ) : (
                    <span style={{ color: "var(--faint)" }}>—</span>
                  )}
                </td>
                <td className="px-2 py-2 text-right whitespace-nowrap" style={{ color: "var(--dim)" }} title={fmtTs(p.entryTs)}>
                  {ago(p.entryTs)}
                </td>
                <td className="px-2 py-2">
                  <span
                    className="chip"
                    style={{
                      padding: "1px 8px",
                      color: p.open ? "var(--ok)" : "var(--faint)",
                      borderColor: p.open ? "rgba(52,211,153,.35)" : "var(--border)",
                    }}
                  >
                    {p.open ? "open" : "closed"}
                  </span>
                  {!p.open && p.exitTs ? (
                    <span className="ml-2 text-[0.75rem]" style={{ color: "var(--faint)" }} title={fmtTs(p.exitTs)}>
                      {ago(p.exitTs)}
                    </span>
                  ) : null}
                </td>
                <td className="px-2 py-2">
                  {p.open ? (
                    <button
                      type="button"
                      onClick={() => close(p)}
                      disabled={closing}
                      aria-label={`close ${p.symbol} position`}
                      className="cursor-pointer rounded border px-2.5 py-1 text-[0.75rem] transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
                      style={{ borderColor: "var(--border)", color: "var(--dim)", background: "var(--panel2)" }}
                    >
                      {closing ? "closing…" : "close"}
                    </button>
                  ) : null}
                  {rowErr && rowErr.id === p.id && (
                    <div className="mt-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
                      {rowErr.msg}
                    </div>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ── correlation panel ───────────────────────────────────────────────────
function plainCorr(pair: [string, string], r: number, high: boolean): string {
  if (!pair || !pair[0] || !pair[1] || !isFinite(r)) return "";
  const [a, b] = pair;
  if (high) {
    return `Most correlated: ${a} & ${b} at ${fmtR(r)} — they move together, little diversification.`;
  }
  return `Least correlated: ${a} & ${b} at ${fmtR(r)} — the strongest diversifier in the set.`;
}

function CorrelationPanel({
  data,
  err,
  loading,
  onRefresh,
  refreshing,
}: {
  data: CorrelationResponse | null;
  err: string | null;
  loading: boolean;
  onRefresh: () => void;
  refreshing: boolean;
}) {
  const symbols = data?.symbols ?? [];
  const matrix = data?.matrix ?? [];
  const enough = symbols.length >= 2 && matrix.length === symbols.length;

  return (
    <section className="panel">
      <div className="panel-h">
        CORRELATION — WHAT ACTUALLY DIVERSIFIES
        {data && enough && (
          <span
            className="tnum"
            title="0 = every symbol moves independently, 1 = perfectly diversified"
            style={{ color: "var(--dim)" }}
          >
            diversification{" "}
            <span style={{ color: scoreColor(data.diversification * 2 - 1) }}>
              {data.diversification.toFixed(2)}
            </span>
            <span style={{ color: "var(--faint)" }}> / 1</span>
          </span>
        )}
        <button
          type="button"
          onClick={onRefresh}
          disabled={refreshing}
          className="ml-auto min-h-[36px] cursor-pointer rounded border px-3 py-1 text-[0.75rem] tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
          style={{ borderColor: "var(--border)", color: "var(--dim)", background: "var(--panel2)" }}
        >
          {refreshing ? "refreshing…" : "refresh"}
        </button>
      </div>

      {loading && (
        <div className="p-4">
          <Skeleton lines={4} label="loading correlation matrix" className="border-0 p-0" />
        </div>
      )}

      {err && !data && (
        <ErrorState className="m-4" message={err} retry={onRefresh} />
      )}

      {data && !enough && (
        <EmptyState
          className="m-4"
          message="Not enough symbols with overlapping history to correlate."
          detail="Subscribe to at least two symbols, let daily bars accrue, then refresh."
        />
      )}

      {data && enough && (
        <>
          <div className="overflow-x-auto px-4 py-4">
            <table className="tnum border-separate" style={{ borderSpacing: 2 }}>
              <thead>
                <tr>
                  <th className="px-2 py-1" aria-hidden="true" />
                  {symbols.map((s) => (
                    <th
                      key={s}
                      scope="col"
                      className="px-1.5 py-1 text-[0.75rem] font-medium"
                      style={{ color: "var(--faint)" }}
                    >
                      {s}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {symbols.map((rowSym, i) => (
                  <tr key={rowSym}>
                    <th
                      scope="row"
                      className="pr-2 text-right text-[0.75rem] font-medium whitespace-nowrap"
                      style={{ color: "var(--faint)" }}
                    >
                      {rowSym}
                    </th>
                    {symbols.map((colSym, j) => {
                      const r = matrix[i]?.[j];
                      const diag = i === j;
                      const c = diag ? { bg: "var(--panel2)", fg: "var(--faint)" } : corrCell(r);
                      return (
                        <td
                          key={colSym}
                          title={`${rowSym} × ${colSym}: ${fmtR(r)}`}
                          className="h-9 w-11 rounded text-center text-[0.75rem]"
                          style={{ background: c.bg, color: c.fg }}
                        >
                          {diag ? "—" : fmtR(r)}
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div
            className="flex flex-wrap items-center gap-x-5 gap-y-1 border-t px-4 py-2.5 text-[0.75rem]"
            style={{ borderColor: "var(--border)", color: "var(--faint)" }}
          >
            <span className="flex items-center gap-1.5">
              <span className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: "rgba(248,113,113,.55)" }} />
              move together (+)
            </span>
            <span className="flex items-center gap-1.5">
              <span className="inline-block h-2.5 w-2.5 rounded-sm" style={{ background: "rgba(52,211,153,.55)" }} />
              move opposite (−) · diversifying
            </span>
          </div>

          <div className="border-t px-4 py-3 text-[0.75rem] leading-relaxed" style={{ borderColor: "var(--border)", color: "var(--dim)" }}>
            <p>{plainCorr(data.mostPair, data.mostR, true)}</p>
            <p className="mt-1">{plainCorr(data.leastPair, data.leastR, false)}</p>
          </div>
        </>
      )}
    </section>
  );
}

// ── page ────────────────────────────────────────────────────────────────
export default function PortfolioPage() {
  const [pf, setPf] = useState<PortfolioResponse | null>(null);
  const [pfErr, setPfErr] = useState<string | null>(null);
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  const [corr, setCorr] = useState<CorrelationResponse | null>(null);
  const [corrErr, setCorrErr] = useState<string | null>(null);
  const [corrRefreshing, setCorrRefreshing] = useState(false);

  // 5s poll: portfolio + watchlist (watchlist feeds the symbol picker).
  useEffect(() => {
    let alive = true;
    const load = () => {
      api
        .portfolio()
        .then((d) => {
          if (!alive) return;
          setPf(d);
          setPfErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setPfErr(e instanceof Error ? e.message : String(e));
        });
      api
        .watchlist()
        .then((w) => alive && setWatch(w))
        .catch(() => {
          /* picker just stays empty; portfolio error covers the outage */
        });
    };
    load();
    const t = setInterval(load, pollMs());
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [retryTick]);

  // Correlation: load once, refresh on demand (heavier query, not polled).
  const loadCorr = useMemo(
    () => () => {
      setCorrRefreshing(true);
      return api
        .correlation()
        .then((c) => {
          setCorr(c);
          setCorrErr(null);
        })
        .catch((e: unknown) => {
          setCorrErr(e instanceof Error ? e.message : String(e));
        })
        .finally(() => setCorrRefreshing(false));
    },
    [],
  );

  useEffect(() => {
    void loadCorr();
  }, [loadCorr]);

  const forcePoll = useMemo(
    () => () => {
      api
        .portfolio()
        .then((d) => {
          setPf(d);
          setPfErr(null);
        })
        .catch((e: unknown) => setPfErr(e instanceof Error ? e.message : String(e)));
    },
    [],
  );

  const positions = pf?.positions ?? [];
  const open = positions.filter((p) => p.open);
  const loading = pf === null && pfErr === null;
  const hardError = pf === null && pfErr !== null;
  const corrLoading = corr === null && corrErr === null;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">PORTFOLIO</h1>
        {pf !== null && (
          <>
            <span className="chip tnum">{positions.length} logged</span>
            <span className="chip tnum">{open.length} open</span>
          </>
        )}
        {pfErr !== null && pf !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="lab-portfolio"
        text="Are YOUR calls any good? Log discretionary trades and get graded against what actually happened — the same honesty loop the model runs on itself."
      />

      {/* honesty note — the reason this page exists */}
      <p className="px-1 text-[0.78rem] italic leading-relaxed" style={{ color: "var(--faint)" }}>
        This grades YOUR discretionary reads against what actually happened — the same honesty
        loop as the model, applied to you. The score beside each entry is the pressure reading at
        the moment you logged it; watch whether it was right.
      </p>

      {loading && <Skeleton lines={5} label="loading portfolio" />}

      {hardError && (
        <ErrorState
          message={
            /401/.test(pfErr ?? "")
              ? "Sign in to track your reads"
              : (pfErr ?? "could not load portfolio")
          }
          hint={
            /401/.test(pfErr ?? "")
              ? "The portfolio is scoped to your account (your entries, your scores-at-entry) — log in and this page will load."
              : undefined
          }
          retry={() => {
            setPfErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {pf !== null && (
        <>
          {positions.length > 0 && <SummaryBar stat={pf.stat} />}

          <LogForm watch={watch} onLogged={forcePoll} />

          <section className="panel">
            <div className="panel-h">
              POSITIONS
              <span className="tnum" style={{ color: "var(--faint)" }}>
                your logged reads, marked to the latest close
              </span>
            </div>
            {positions.length === 0 ? (
              <EmptyState
                className="m-4"
                message="No positions logged yet."
                detail="Pick a symbol above and log a read — entry price and the pressure score are captured at the latest close, so you can grade the call later."
              />
            ) : (
              <PositionsTable positions={positions} onClosed={forcePoll} />
            )}
          </section>
        </>
      )}

      <CorrelationPanel
        data={corr}
        err={corrErr}
        loading={corrLoading}
        onRefresh={loadCorr}
        refreshing={corrRefreshing}
      />
    </div>
  );
}
