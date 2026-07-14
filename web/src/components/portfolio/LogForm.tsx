"use client";

// Log-a-position form — extracted from the old monolithic /lab/portfolio
// page (pure refactor; behavior identical). CTA speaks Monitor language:
// logging a read puts it under the honesty loop's watch — it never prompts
// a trade.

import { useMemo, useState } from "react";
import { api, type WatchRow } from "@/lib/api";
import { fmtPrice, fmtScore } from "@/lib/format";

export default function LogForm({
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
      <form onSubmit={submit} className="flex flex-wrap items-end gap-x-4 gap-y-3 px-4 py-4 text-[0.75rem]">
        <label className="flex flex-col gap-1">
          <span style={{ color: "var(--faint)" }}>symbol</span>
          <select
            value={pick}
            onChange={(e) => setPick(e.target.value)}
            aria-label="symbol to log"
            disabled={options.length === 0}
            className="min-h-[40px] min-w-44 cursor-pointer rounded-lg border px-2 py-1.5 text-[0.75rem] disabled:cursor-not-allowed disabled:opacity-50"
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
            className="tnum min-h-[40px] w-28 rounded-lg border px-2.5 py-1.5 text-[0.75rem]"
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
            className="min-h-[40px] w-full rounded-lg border px-2.5 py-1.5 text-[0.75rem]"
            style={fieldStyle}
          />
        </label>

        <button
          type="submit"
          disabled={!valid || busy}
          className="min-h-[40px] cursor-pointer rounded-lg border px-4 py-1.5 text-[0.75rem] font-semibold tracking-wide transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
          style={{
            borderColor: valid ? "var(--accent)" : "var(--border)",
            background: valid ? "rgba(251,191,36,.1)" : "var(--panel2)",
            color: valid ? "var(--text)" : "var(--dim)",
          }}
        >
          {busy ? "logging…" : "log & monitor"}
        </button>
      </form>
      {(err || ok) && (
        <div
          className="border-t px-4 py-2 text-[0.75rem]"
          style={{ borderColor: "var(--border)", color: err ? "var(--bad)" : "var(--ok)" }}
        >
          {err ?? ok}
        </div>
      )}
    </section>
  );
}
