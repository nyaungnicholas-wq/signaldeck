"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, companiesList, type CompanyDirRow } from "@/lib/api";
import {
  HORIZONS,
  JOURNAL_FOOTER,
  JOURNAL_IMMUTABLE,
  horizonLabel,
  statsHeadline,
  beatsDrift,
  statusLabel,
  type Journal,
  type JournalCall,
  type CallDirection,
} from "@/lib/journal";

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// The member's calls are dated on the exchange's clock, like their grades.
function etDate(ts: number): string {
  return new Date(ts * 1000).toLocaleDateString("en-CA", { timeZone: "America/New_York" });
}

function whenLine(call: JournalCall): string {
  switch (call.status) {
    case "open":
      return `Grades on the ${call.resolveDate} close`;
    case "withdrawn":
      return "Withdrawn before its entry session";
    case "void":
      return `Void: the close did not move, or no data for the ${call.resolveDate} close`;
    default:
      return `Graded on the ${call.resolveDate} close`;
  }
}

export default function MemberJournal() {
  const [journal, setJournal] = useState<Journal | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [reloadTick, setReloadTick] = useState(0);

  const [symbol, setSymbol] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<{ q: string; rows: CompanyDirRow[] } | null>(null);
  const [searchErr, setSearchErr] = useState<string | null>(null);
  const [direction, setDirection] = useState<CallDirection>("up");
  const [horizon, setHorizon] = useState<number>(5);
  const [note, setNote] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [confirmingErr, setConfirmingErr] = useState<string | null>(null);
  const [confirmingBusy, setConfirmingBusy] = useState(false);

  const [withdrawBusy, setWithdrawBusy] = useState<string | null>(null);
  const [withdrawErr, setWithdrawErr] = useState<{ key: string; msg: string } | null>(null);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const j = await api.journal();
        if (alive) {
          setJournal(j);
          setLoadErr(null);
        }
      } catch (e) {
        if (alive) setLoadErr(errText(e));
      }
    };
    void load();
    return () => {
      alive = false;
    };
  }, [reloadTick]);

  const q = query.trim();
  useEffect(() => {
    if (q.length < 1) return;
    let alive = true;
    const t = setTimeout(() => {
      companiesList({ q, limit: 8 }).then(
        (res) => {
          if (alive) {
            setResults({ q, rows: res.companies ?? [] });
            setSearchErr(null);
          }
        },
        (e: unknown) => {
          if (alive) setSearchErr(errText(e));
        }
      );
    }, 250);
    return () => {
      alive = false;
      clearTimeout(t);
    };
  }, [q]);
  const shown = results && results.q === q && q.length > 0 ? results.rows : [];

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!symbol) return;
    setConfirming(true);
    setConfirmingErr(null);
  };

  const handleConfirmCall = async () => {
    if (!symbol) return;
    setConfirmingBusy(true);
    setConfirmingErr(null);
    try {
      const j = await api.journalCall({
        symbol,
        market: "stocks",
        call: direction,
        horizon,
        note,
      });
      setJournal(j);
      setSymbol(null);
      setQuery("");
      setResults(null);
      setDirection("up");
      setHorizon(5);
      setNote("");
      setConfirming(false);
    } catch (e) {
      setConfirmingErr(errText(e));
    } finally {
      setConfirmingBusy(false);
    }
  };

  const handleWithdraw = async (id: number) => {
    setWithdrawBusy(`${id}`);
    setWithdrawErr(null);
    try {
      const j = await api.journalWithdraw(id);
      setJournal(j);
    } catch (e) {
      setWithdrawErr({ key: `${id}`, msg: errText(e) });
      // The refusal usually means the entry session has started: reload so
      // the row stops offering a withdraw the server will not take.
      setReloadTick((t) => t + 1);
    } finally {
      setWithdrawBusy(null);
    }
  };

  return (
    <section className="panel flex flex-col gap-4 px-4 py-4" aria-labelledby="member-journal-h">
      <header className="flex flex-wrap items-baseline justify-between gap-2">
        <h1 id="member-journal-h" className="m-0 text-lg font-bold">
          My calls
        </h1>
        {journal && (
          <span className="text-[0.8rem]" style={{ color: "var(--dim)" }}>
            {journal.caps.todayLeft} calls left today · {journal.caps.openLeft} open slots
          </span>
        )}
      </header>

      {journal && (
        <div className="chip flex flex-col gap-1 px-3 py-2">
          <p className="m-0">{statsHeadline(journal.stats)}</p>
          <p className="m-0">
            {`${journal.stats.hits} hits · ${journal.stats.misses} misses · ${journal.stats.open} open · ${journal.stats.void} void`}
          </p>
          {beatsDrift(journal.stats) === true && (
            <p className="m-0 text-[0.75rem]">
              Your hit rate&rsquo;s interval sits above the always-up baseline.
            </p>
          )}
          {beatsDrift(journal.stats) === false && (
            <p className="m-0 text-[0.75rem]">
              Your hit rate&rsquo;s interval sits below the always-up baseline.
            </p>
          )}
          {beatsDrift(journal.stats) === null && !journal.stats.withheld && (
            <p className="m-0 text-[0.75rem]">
              The always-up baseline is inside your interval: no edge over drift is shown yet.
            </p>
          )}
        </div>
      )}

      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        <div>
          <label htmlFor="journal-symbol-search" className="text-[0.75rem] tracking-wide" style={{ color: "var(--dim)" }}>
            US stock or ETF
          </label>
          <input
            id="journal-symbol-search"
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Ticker or company name"
            autoComplete="off"
            className="chip min-h-[40px] w-full max-w-[28rem] px-3"
            style={{ color: "var(--text)" }}
          />
          {searchErr && (
            <p role="alert" className="m-0 text-[0.8rem]" style={{ color: "var(--ask)" }}>
              {searchErr}
            </p>
          )}
          {shown.length > 0 && (
            <ul className="m-0 flex max-w-[40rem] list-none flex-col gap-1 p-0">
              {shown.map((c) => (
                <li key={c.ticker} className="flex items-center gap-3 border-b py-1" style={{ borderColor: "var(--border)" }}>
                  <span className="mono w-20 shrink-0 font-bold">{c.ticker}</span>
                  <span className="min-w-0 flex-1 truncate text-[0.85rem]">{c.name}</span>
                  <span className="hidden text-[0.75rem] sm:inline" style={{ color: "var(--faint)" }}>
                    {c.exchange || "—"}
                  </span>
                  {c.tracked ? (
                    <button
                      type="button"
                      onClick={() => {
                        setSymbol(c.ticker);
                        setQuery("");
                        setResults(null);
                      }}
                      className="chip min-h-[36px] cursor-pointer px-3 text-[0.8rem]"
                      style={{ color: "var(--accent)" }}
                    >
                      Pick
                    </button>
                  ) : (
                    <button type="button" disabled className="chip min-h-[36px] px-3 text-[0.8rem]" style={{ color: "var(--faint)" }}>
                      Not tracked
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
          {symbol && (
            <div className="flex items-center gap-2">
              <span className="mono font-bold">{symbol}</span>
              <button
                type="button"
                onClick={() => {
                  setSymbol(null);
                  setQuery("");
                  setResults(null);
                  setConfirming(false);
                }}
                className="chip min-h-[36px] cursor-pointer px-3 text-[0.8rem]"
                style={{ color: "var(--accent)" }}
              >
                Change
              </button>
            </div>
          )}
        </div>

        <div className="flex flex-col gap-1">
          <span id="journal-direction" className="text-[0.75rem] tracking-wide" style={{ color: "var(--dim)" }}>
            Direction
          </span>
          <div className="flex gap-2" role="group" aria-labelledby="journal-direction">
            <button
              type="button"
              aria-pressed={direction === "up"}
              onClick={() => setDirection("up")}
              className={`chip min-h-[36px] px-3 text-[0.8rem] ${direction === "up" ? "bg-[var(--bid)]/20" : ""}`}
              style={{ color: direction === "up" ? "var(--bid)" : "var(--text)" }}
            >
              Up
            </button>
            <button
              type="button"
              aria-pressed={direction === "down"}
              onClick={() => setDirection("down")}
              className={`chip min-h-[36px] px-3 text-[0.8rem] ${direction === "down" ? "bg-[var(--ask)]/20" : ""}`}
              style={{ color: direction === "down" ? "var(--ask)" : "var(--text)" }}
            >
              Down
            </button>
          </div>
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="journal-horizon" className="text-[0.75rem] tracking-wide" style={{ color: "var(--dim)" }}>
            Horizon
          </label>
          <select
            id="journal-horizon"
            value={horizon}
            onChange={(e) => setHorizon(Number(e.target.value))}
            className="chip min-h-[36px] w-full px-3"
            style={{ color: "var(--text)" }}
          >
            {HORIZONS.map((h) => (
              <option key={h.value} value={h.value}>
                {h.label}
              </option>
            ))}
          </select>
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="journal-note" className="text-[0.75rem] tracking-wide" style={{ color: "var(--dim)" }}>
            Note (only you see this)
          </label>
          <textarea
            id="journal-note"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            maxLength={280}
            className="chip min-h-[60px] w-full px-3 py-2"
            style={{ color: "var(--text)" }}
          />
          <div className="flex justify-end">
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {note.length}/280
            </span>
          </div>
        </div>

        <button
          type="submit"
          disabled={!symbol}
          className="chip min-h-[36px] w-full cursor-pointer px-3"
          style={{ color: "var(--accent)" }}
        >
          Review call
        </button>
      </form>

      {confirming && symbol && (
        <div role="dialog" aria-label="Confirm call" className="flex flex-col gap-3 p-4 border" style={{ borderColor: "var(--border)" }}>
          <p className="m-0 text-[0.9rem] font-medium">
            {symbol} {direction === "up" ? "Up" : "Down"} over {horizonLabel(horizon)}
          </p>
          <p className="m-0 text-[0.85rem] leading-relaxed">{JOURNAL_IMMUTABLE}</p>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={handleConfirmCall}
              disabled={confirmingBusy}
              className="chip min-h-[36px] cursor-pointer px-3"
              style={{ color: "var(--accent)" }}
            >
              Confirm call
            </button>
            <button
              type="button"
              onClick={() => setConfirming(false)}
              className="chip min-h-[36px] cursor-pointer px-3"
            >
              Back
            </button>
          </div>
          {confirmingErr && (
            <p role="alert" className="m-0 text-[0.8rem]" style={{ color: "var(--ask)" }}>
              {confirmingErr}
            </p>
          )}
        </div>
      )}

      {loadErr ? (
        <div className="flex flex-wrap items-center gap-3">
          <p role="alert" className="m-0 text-[0.85rem]" style={{ color: "var(--ask)" }}>
            {loadErr}
          </p>
          <button type="button" onClick={() => setReloadTick((t) => t + 1)} className="chip min-h-[36px] cursor-pointer px-3">
            Retry
          </button>
        </div>
      ) : journal === null ? (
        <p className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
          Loading your calls…
        </p>
      ) : journal.calls.length === 0 ? (
        <p className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
          No calls yet. Make one above; it is graded after its horizon closes.
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {journal.calls.map((call) => {
            const key = `${call.id}`;
            return (
              <li key={key} className="flex flex-col gap-2 border-b pb-3" style={{ borderColor: "var(--border)" }}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="mono font-bold">{call.symbol}</span>
                  <span className="min-w-0 flex-1 truncate text-[0.85rem]">
                    {call.call === "up" ? "Up" : "Down"} · {horizonLabel(call.horizon)}
                  </span>
                  <span
                    className="chip px-2 py-1 text-[0.75rem]"
                    style={{
                      color:
                        call.outcome === "hit"
                          ? "var(--bid)"
                          : call.outcome === "miss"
                          ? "var(--ask)"
                          : "var(--dim)",
                    }}
                  >
                    {statusLabel(call)}
                  </span>
                </div>
                <p className="m-0 text-[0.8rem]" style={{ color: "var(--dim)" }}>
                  Made {etDate(call.createdTs)} · {whenLine(call)}
                </p>
                {call.note && (
                  <p className="m-0 text-[0.8rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                    {call.note}
                  </p>
                )}
                {call.canWithdraw && (
                  <div>
                    <button
                      type="button"
                      onClick={() => void handleWithdraw(call.id)}
                      disabled={withdrawBusy === key}
                      className="chip min-h-[36px] cursor-pointer px-3 text-[0.8rem]"
                    >
                      Withdraw
                    </button>
                  </div>
                )}
                {withdrawErr?.key === key && (
                  <p role="alert" className="m-0 text-[0.8rem]" style={{ color: "var(--ask)" }}>
                    {withdrawErr.msg}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        {JOURNAL_FOOTER}{" "}
        <Link href="/accuracy" className="underline">
          How SignalDeck grades itself
        </Link>
      </p>
    </section>
  );
}
