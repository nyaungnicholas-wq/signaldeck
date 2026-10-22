"use client";
import { useState, type FormEvent } from "react";
import { api, ApiError } from "@/lib/api";
import { CRYPTO_PAUSED_LABEL } from "@/lib/cryptoPause";

export default function OrderForm({ onFilled }: { onFilled: () => void }) {
  const [symbol, setSymbol] = useState("");
  const [side, setSide] = useState<"buy" | "sell">("buy");
  const [qty, setQty] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  // A pair like BTC/USD is crypto, which is paused: say so, never submit it.
  const cryptoPaused = symbol.includes("/");

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (cryptoPaused) return;
    setBusy(true);
    setStatus(null);
    try {
      const r = await api.paperOrder(symbol.trim().toUpperCase(), "stocks", side, Number(qty));
      setStatus({
        kind: "ok",
        text: `Filled ${r.fill.Side} ${r.fill.Qty} ${symbol.trim().toUpperCase()} @ ${r.fill.Px.toFixed(2)} (cost ${r.fill.Cost.toFixed(2)}${r.fill.Capped ? ", size capped by liquidity" : ""}). Cash ${r.cash.toFixed(2)}, equity ${r.equity.toFixed(2)}. Quote bar ${r.quoteAgeS}s old.`,
      });
      onFilled();
    } catch (err: unknown) {
      const refused = err instanceof ApiError && (err.status === 400 || err.status === 409);
      const msg = err instanceof Error ? err.message : String(err);
      setStatus({ kind: "err", text: (refused ? "Refused: " : "Error: ") + msg });
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel">
      <div className="panel-h">MANUAL ORDER (SIMULATED)</div>
      <form className="flex flex-wrap items-end gap-3 px-4 py-3" onSubmit={submit}>
        <label className="flex flex-col gap-1 text-[0.7rem]" style={{ color: "var(--faint)" }}>
          Symbol
          <input
            className="mono rounded-lg px-3 py-1 text-[0.8rem]"
            style={{ border: "1px solid var(--border)", background: "transparent", color: "var(--text)" }}
            value={symbol}
            onChange={e => setSymbol(e.target.value.toUpperCase())}
            placeholder="SPY"
            required
          />
        </label>
        <label className="flex flex-col gap-1 text-[0.7rem]" style={{ color: "var(--faint)" }}>
          Side
          <select
            className="mono rounded-lg px-3 py-1 text-[0.8rem]"
            style={{ border: "1px solid var(--border)", background: "transparent", color: "var(--text)" }}
            value={side}
            onChange={e => setSide(e.target.value as "buy" | "sell")}
          >
            <option value="buy">buy</option>
            <option value="sell">sell</option>
          </select>
        </label>
        <label className="flex flex-col gap-1 text-[0.7rem]" style={{ color: "var(--faint)" }}>
          Qty
          <input
            type="number"
            min={0}
            step="any"
            className="mono rounded-lg px-3 py-1 text-[0.8rem]"
            style={{ border: "1px solid var(--border)", background: "transparent", color: "var(--text)" }}
            value={qty}
            onChange={e => setQty(e.target.value)}
            required
          />
        </label>
        <button
          type="submit"
          disabled={busy || cryptoPaused || symbol.trim() === "" || qty === ""}
          className="mono rounded-lg px-3 py-1 text-[0.8rem]"
          style={{ border: "1px solid var(--border)", background: "transparent", color: "var(--text)" }}
        >
          PLACE SIMULATED ORDER
        </button>
      </form>
      {cryptoPaused && (
        <p className="px-4 pb-2 text-[0.75rem]" style={{ color: "var(--ask)" }}>
          Stocks only: {CRYPTO_PAUSED_LABEL}.
        </p>
      )}
      {status && (
        <p className="px-4 pb-2 text-[0.75rem]" style={{ color: status.kind === "ok" ? "var(--ok)" : "var(--ask)" }}>
          {status.text}
        </p>
      )}
      <p className="px-4 pb-3 text-[0.7rem]" style={{ color: "var(--faint)" }}>
        Simulated market orders against the newest 1-minute bar with the same spread and impact model as the flagship books. Refused when no live bar exists (market closed). Long-only. Not live money, not advice.
      </p>
    </section>
  );
}