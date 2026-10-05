"use client";

// Risk page state hook — the state/fetching half of the old monolithic
// /lab/risk page (pure refactor; behavior identical). This is a
// request-driven page: the watchlist is fetched once on mount (no polling)
// and the report only recomputes when the user runs it.

import { useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  type Market,
  type RiskHolding,
  type RiskReport,
  type WatchRow,
} from "@/lib/api";

// A holding row as edited in the builder. weight is a raw percent (need not
// sum to 100 — the backend normalizes). A stable id keeps React keys sane
// across add/remove without leaning on array index.
export interface Row {
  id: number;
  symbol: string;
  market: Market;
  weight: string; // kept as string so the input can be empty mid-edit
}

export const DEFAULT_NOTIONAL = 100000;

let ROW_SEQ = 1;
const newRow = (symbol = "", market: Market = "crypto", weight = ""): Row => ({
  id: ROW_SEQ++,
  symbol,
  market,
  weight,
});

export interface RiskState {
  watch: WatchRow[] | null;
  watchErr: string | null;
  picker: WatchRow[];
  rows: Row[];
  setRow: (id: number, patch: Partial<Row>) => void;
  addRow: () => void;
  removeRow: (id: number) => void;
  addFromWatchlist: (p: WatchRow) => void;
  equalWeightWatchlist: () => void;
  notional: number;
  setNotional: (n: number) => void;
  totalWeight: number;
  validHoldings: RiskHolding[];
  run: () => void;
  running: boolean;
  runErr: string | null;
  canRun: boolean;
  report: RiskReport | null;
  summary: string;
  /** Notional the last successful run was priced at — VaR $ must reflect the
   *  run, not a value the user edited afterward. */
  ranNotional: number;
  /** Contributions sorted by share of portfolio risk, descending. */
  drivers: RiskReport["Contributions"];
  maxPct: number;
}

export default function useRisk(): RiskState {
  const [watch, setWatch] = useState<WatchRow[] | null>(null);
  const [watchErr, setWatchErr] = useState<string | null>(null);

  const [rows, setRows] = useState<Row[]>([newRow()]);
  const [notional, setNotional] = useState<number>(DEFAULT_NOTIONAL);

  const [report, setReport] = useState<RiskReport | null>(null);
  const [summary, setSummary] = useState<string>("");
  const [running, setRunning] = useState(false);
  const [runErr, setRunErr] = useState<string | null>(null);
  const [ranNotional, setRanNotional] = useState<number>(DEFAULT_NOTIONAL);

  // Unmount guard for the imperative run() below — same job as the `alive`
  // flag in the mount effect, but for requests fired from user actions.
  const aliveRef = useRef(true);
  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
    };
  }, []);

  // Watchlist powers the chip-picker and the equal-weight shortcut. It is not
  // polled — this is a request-driven page — but we fetch once on mount.
  useEffect(() => {
    let alive = true;
    api
      .watchlist()
      .then((w) => {
        if (!alive) return;
        setWatch(w);
        setWatchErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setWatchErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, []);

  const picker = useMemo(() => watch ?? [], [watch]);

  const totalWeight = useMemo(
    () =>
      rows.reduce((s, r) => {
        const w = Number(r.weight);
        return s + (isFinite(w) && w > 0 ? w : 0);
      }, 0),
    [rows]
  );

  // Rows the daemon can actually price: a symbol and a positive weight.
  const validHoldings = useMemo<RiskHolding[]>(
    () =>
      rows
        .map((r) => ({ symbol: r.symbol.trim(), market: r.market, weight: Number(r.weight) }))
        .filter((h) => h.symbol.length > 0 && isFinite(h.weight) && h.weight > 0),
    [rows]
  );

  const setRow = (id: number, patch: Partial<Row>) =>
    setRows((rs) => rs.map((r) => (r.id === id ? { ...r, ...patch } : r)));

  const addRow = () => setRows((rs) => [...rs, newRow()]);
  const removeRow = (id: number) =>
    setRows((rs) => (rs.length <= 1 ? [newRow()] : rs.filter((r) => r.id !== id)));

  const addFromWatchlist = (p: WatchRow) =>
    setRows((rs) => {
      const blank = rs.find((r) => r.symbol.trim() === "");
      if (blank)
        return rs.map((r) =>
          r.id === blank.id ? { ...r, symbol: p.symbol, market: p.market } : r
        );
      return [...rs, newRow(p.symbol, p.market, "")];
    });

  const equalWeightWatchlist = () => {
    if (picker.length === 0) return;
    const w = (100 / picker.length).toFixed(2);
    ROW_SEQ = 1;
    setRows(picker.map((p) => newRow(p.symbol, p.market, w)));
    setReport(null);
    setSummary("");
    setRunErr(null);
  };

  const run = () => {
    if (validHoldings.length === 0 || running) return;
    setRunning(true);
    setRunErr(null);
    const atNotional = notional > 0 ? notional : DEFAULT_NOTIONAL;
    api
      .risk(validHoldings, atNotional)
      .then((res) => {
        if (!aliveRef.current) return;
        setReport(res.report);
        setSummary(res.summary);
        setRanNotional(res.report.NotionalUSD > 0 ? res.report.NotionalUSD : atNotional);
        setRunErr(null);
      })
      .catch((e: unknown) => {
        if (!aliveRef.current) return;
        setReport(null);
        setSummary("");
        setRunErr(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (aliveRef.current) setRunning(false);
      });
  };

  // Drivers sorted by share of portfolio risk, descending.
  const drivers = useMemo(() => {
    const cs = report?.Contributions ?? [];
    return [...cs].sort((a, b) => b.PctOfRisk - a.PctOfRisk);
  }, [report]);
  const maxPct = useMemo(
    () => drivers.reduce((m, c) => Math.max(m, c.PctOfRisk), 0),
    [drivers]
  );

  return {
    watch,
    watchErr,
    picker,
    rows,
    setRow,
    addRow,
    removeRow,
    addFromWatchlist,
    equalWeightWatchlist,
    notional,
    setNotional,
    totalWeight,
    validHoldings,
    run,
    running,
    runErr,
    canRun: validHoldings.length > 0 && !running,
    report,
    summary,
    ranNotional,
    drivers,
    maxPct,
  };
}
