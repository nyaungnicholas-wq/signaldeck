// Small HUD-local formatting helpers layered on the shared format module.

import { fmtPrice } from "@/lib/format";

/** Dollar amount; sign-aware; never renders "$—" for a true zero. */
export function usd(v?: number | null, signed = false): string {
  if (v == null || !isFinite(v)) return "—";
  const a = Math.abs(v);
  const body = a < 0.005 ? "0.00" : fmtPrice(a);
  const sign = v < -0.005 ? "−" : signed && v > 0.005 ? "+" : "";
  return `${sign}$${body}`;
}

/** Green for gains, red for losses, dim for flat/missing. */
export function pnlColor(v?: number | null): string {
  if (v == null || !isFinite(v) || v === 0) return "var(--dim)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

/** Coerce Alpaca's stringy numbers ("12", "657.31") to a finite number. */
export function num(v: unknown): number | null {
  if (typeof v === "number") return isFinite(v) ? v : null;
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v);
    return isFinite(n) ? n : null;
  }
  return null;
}

/** Quantity display: whole shares stay whole, fractional keep 2dp. */
export function fmtQty(v: unknown): string {
  const n = num(v);
  if (n == null) return "—";
  return Number.isInteger(n) ? n.toLocaleString("en-US") : n.toFixed(2);
}
