// Shared formatting helpers — keep every page's numbers consistent.

export function fmtPrice(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  if (v >= 1000) return v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  if (v >= 1) return v.toFixed(2);
  return v.toPrecision(4);
}

export function fmtPct(v: number, signed = true): string {
  if (!isFinite(v)) return "—";
  const s = signed && v > 0 ? "+" : "";
  return `${s}${v.toFixed(1)}%`;
}

export function fmtScore(v: number): string {
  if (!isFinite(v)) return "—";
  return `${v >= 0 ? "+" : ""}${v.toFixed(2)}`;
}

export function fmtTs(ts: number): string {
  if (!ts) return "—";
  return new Date(ts * 1000).toLocaleString("en-US", {
    month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
  });
}

export function fmtDate(ts: number): string {
  if (!ts) return "—";
  return new Date(ts * 1000).toLocaleDateString("en-US", {
    year: "numeric", month: "short", day: "numeric",
  });
}

export function ago(ts: number): string {
  if (!ts) return "never";
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

// Seconds elapsed since a unix-seconds timestamp. Keeps the wall-clock read
// (Date.now) inside this module so callers can compare an age against a
// threshold without an impure call in a component's render body.
export function secondsSince(ts: number): number {
  if (!ts) return Infinity;
  return Math.max(0, Math.floor(Date.now() / 1000 - ts));
}

// Score verdict wording — MUST match the daemon's insight writer buckets.
export function verdict(score: number): string {
  if (score >= 0.5) return "strong buy pressure";
  if (score >= 0.15) return "mild buy pressure";
  if (score > -0.15) return "balanced";
  if (score > -0.5) return "mild sell pressure";
  return "strong sell pressure";
}

export function scoreColor(score: number): string {
  if (score >= 0.15) return "var(--bid)";
  if (score <= -0.15) return "var(--ask)";
  return "var(--dim)";
}

// Humanize expectancy state keys ("rsi:low|trend:above|mom:up|rvol:high").
export function humanizeState(key: string): string {
  if (!key) return "insufficient history";
  const words: Record<string, string> = {
    "rsi:low": "oversold RSI",
    "rsi:mid": "neutral RSI",
    "rsi:high": "overbought RSI",
    "trend:above": "above long-term trend",
    "trend:below": "below long-term trend",
    "mom:up": "positive momentum",
    "mom:down": "negative momentum",
    "rvol:high": "elevated volume",
    "rvol:normal": "normal volume",
  };
  return key.split("|").map((p) => words[p] ?? p).join(", ");
}
