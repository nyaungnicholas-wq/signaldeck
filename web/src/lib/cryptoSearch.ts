import { screenerRows, type CompanyDirRow } from "@/lib/api";
export const CRYPTO_EXCHANGE = "crypto";

type WatchRow = { symbol: string; market: "stocks" | "crypto"; name: string; active: boolean; lastClose: number; dayChangePct: number; };

let cryptoPromise: Promise<WatchRow[]> | null = null;

function getWatchRows(): Promise<WatchRow[]> {
  if (!cryptoPromise) {
    cryptoPromise = screenerRows().then(rows => rows as WatchRow[]).catch(err => { cryptoPromise = null; throw err; });
  }
  return cryptoPromise;
}

export async function cryptoRows(q: string, limit = 5): Promise<CompanyDirRow[]> {
  const query = q.trim().toUpperCase();
  if (!query) return [];
  const rows = await getWatchRows();
  const filtered = rows.filter(r => r.market === "crypto" && (r.symbol.toUpperCase().includes(query) || r.name.toUpperCase().includes(query)));
  const sorted = [...filtered].sort((a, b) => {
    const aStart = a.symbol.toUpperCase().startsWith(query) ? 0 : 1;
    const bStart = b.symbol.toUpperCase().startsWith(query) ? 0 : 1;
    if (aStart !== bStart) return aStart - bStart;
    return a.symbol.localeCompare(b.symbol);
  });
  const result: CompanyDirRow[] = sorted.map(r => ({
    ticker: r.symbol,
    name: r.name ?? r.symbol,
    exchange: CRYPTO_EXCHANGE,
    cik: 0,
    sic: "",
    sicDesc: "crypto",
    tracked: true,
    price: Number.isFinite(r.lastClose) ? r.lastClose : null,
    dayChangePct: Number.isFinite(r.dayChangePct) ? r.dayChangePct : null,
    volume: null,
    mcap: null,
    sharesOutstanding: null,
    float: null
  }));
  return result.slice(0, limit);
}