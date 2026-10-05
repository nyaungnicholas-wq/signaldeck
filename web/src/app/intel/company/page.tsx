"use client";

// COMPANY DIGITAL TWIN (capstones wave): a per-company dossier assembled
// entirely from FREE public SEC data — EDGAR identity + SIC sector, same-sector
// peers, Form 4 executives, 13F institutional holders, recent filings, and
// XBRL fundamentals. One optional on-demand LLM paragraph summarizes it.
//
// HONESTY RULES (rendered, not implied):
//   - the twin is DELIBERATELY loaded (symbol + Load), because each load pulls
//     several SEC datasets and the AI paragraph costs a model call — so summary
//     is OFF by default behind its own button;
//   - only Form 4 codes P (open-market buy) and S (open-market sale) are
//     conviction trades — grants/exercises/gifts are labeled as mechanics and
//     kept neutral, exactly like the Insiders sub-tab;
//   - crypto / untracked symbols have no EDGAR identity (company === null); the
//     page says so instead of fabricating one;
//   - the daemon's own note about what the twin CANNOT know (product/supplier/
//     customer/patent/lawsuit graphs) renders verbatim at the bottom.

import { useState } from "react";
import { api, type CompanyProfile } from "@/lib/api";
import { fmtDate } from "@/lib/format";
import PagePurpose from "@/components/PagePurpose";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

/** Compact USD — mirrors the Insiders sub-tab's fmtUSD ($1.2B / $3.4M / $5.6K). */
function fmtUSD(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  const a = Math.abs(v);
  if (a >= 1e12) return `$${(v / 1e12).toFixed(2)}T`;
  if (a >= 1e9) return `$${(v / 1e9).toFixed(2)}B`;
  if (a >= 1e6) return `$${(v / 1e6).toFixed(2)}M`;
  if (a >= 1e3) return `$${(v / 1e3).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

/** Compact count (no currency) — for share quantities. */
function fmtCount(v: number): string {
  if (!isFinite(v) || v === 0) return "—";
  const a = Math.abs(v);
  if (a >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (a >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (a >= 1e3) return `${(v / 1e3).toFixed(1)}K`;
  return v.toLocaleString("en-US");
}

/**
 * Fundamentals are heterogeneous (Revenues in $, EPS per-share, SharesOutstanding
 * as a count, dates as epochs, CIK as an int). Format by the metric name so a
 * share count never gets a fabricated "$" and a date never gets compacted.
 */
function fmtFundamental(metric: string, value: number): string {
  const m = metric.toLowerCase();
  if (!isFinite(value)) return "—";
  if (m.includes("date") || m.includes("filing")) return fmtDate(value);
  if (m === "cik") return String(Math.round(value));
  if (m.includes("eps") || m.includes("pershare") || m.includes("per share")) {
    return `$${value.toFixed(2)}`;
  }
  const moneyish =
    /(revenue|income|asset|cash|debt|sales|equity|liabilit|value|profit|expense|capital|margin)/.test(m);
  const a = Math.abs(value);
  const compact =
    a >= 1e12 ? `${(value / 1e12).toFixed(2)}T`
    : a >= 1e9 ? `${(value / 1e9).toFixed(2)}B`
    : a >= 1e6 ? `${(value / 1e6).toFixed(2)}M`
    : a >= 1e3 ? `${(value / 1e3).toFixed(1)}K`
    : value.toLocaleString("en-US", { maximumFractionDigits: 2 });
  return moneyish ? `$${compact}` : compact;
}

/**
 * Honest Form 4 code reading. Only P (open-market buy) and S (open-market sale)
 * are conviction trades and get bid/ask color; everything else is a mechanic
 * (grant, exercise, gift, tax withholding …) and stays neutral — same
 * classification the Insiders sub-tab renders.
 */
function insiderCode(code: string): { label: string; color: string } {
  const c = (code || "").toUpperCase();
  if (c === "P") return { label: "buy (P)", color: "var(--bid)" };
  if (c === "S") return { label: "sell (S)", color: "var(--ask)" };
  const mech: Record<string, string> = {
    A: "grant (A)",
    M: "option exercise (M)",
    X: "option exercise (X)",
    G: "gift (G)",
    F: "tax withheld (F)",
    C: "conversion (C)",
    D: "disposition to issuer (D)",
    J: "other (J)",
    W: "acquired by will (W)",
  };
  return { label: mech[c] ?? (c ? `${c} — mechanics` : "—"), color: "var(--dim)" };
}

/** One label/value pair in the identity header. */
function Meta({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-[0.75rem] tracking-[0.12em]" style={{ color: "var(--faint)" }}>
        {label}
      </dt>
      <dd className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }} title={title}>
        {value}
      </dd>
    </div>
  );
}

// Shared table-header row style (matches the Companies sub-tab).
const THEAD = "text-left text-[0.75rem] tracking-[0.12em]";
const THEAD_STYLE = { color: "var(--dim)", borderBottom: "1px solid var(--border)" } as const;
const ROW_STYLE = { borderBottom: "1px solid var(--border)" } as const;

export default function CompanyTwinPage() {
  // Deliberate load model: type a symbol, press Load. Default example NVDA.
  const [input, setInput] = useState("NVDA");
  const [data, setData] = useState<CompanyProfile | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [errHint, setErrHint] = useState<string | undefined>(undefined);
  // AI paragraph is a separate, opt-in call — its own loading/error state so a
  // failed summary never wipes the loaded dossier.
  const [summarizing, setSummarizing] = useState(false);
  const [summaryErr, setSummaryErr] = useState<string | null>(null);

  const load = (sym: string, summary = false) => {
    const s = sym.trim().toUpperCase();
    if (!s) return;
    if (summary) {
      setSummarizing(true);
      setSummaryErr(null);
    } else {
      setLoading(true);
      setErr(null);
      setErrHint(undefined);
    }
    api
      .companyProfile(s, summary)
      .then((r) => {
        setData(r);
        // summary=true but no paragraph => the daemon's LLM is off or capped.
        if (summary && !r.profile) {
          setSummaryErr(
            "No AI profile returned — the daemon's LLM may be disabled or the daily call cap is reached.",
          );
        }
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        const is404 = msg.includes("404");
        const friendly = is404
          ? `${s} isn't in SignalDeck's SEC data — it may be untracked, delisted, or not an EDGAR registrant.`
          : msg;
        if (summary) {
          setSummaryErr(friendly);
        } else {
          setErr(friendly);
          setErrHint(
            is404
              ? "Try a US-listed SEC filer such as NVDA, AAPL or MSFT. Crypto and many micro-caps have no EDGAR identity."
              : undefined,
          );
        }
      })
      .finally(() => {
        if (summary) setSummarizing(false);
        else setLoading(false);
      });
  };

  const initial = data === null && !loading && err === null;

  const company = data?.company ?? null;
  const peers = data?.peers ?? [];
  const insiders = data?.insiders ?? [];
  const holders = data?.holders ?? [];
  const filings = data?.filings ?? [];
  const fundamentals = data?.fundamentals ?? [];

  return (
    <div className="flex flex-col gap-3">
      <PagePurpose
        id="intel-company"
        text="A company digital twin assembled from free SEC data: identity, sector, peers, executives, holders, filings, fundamentals."
      />

      {/* symbol + Load control bar (Enter submits) */}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          load(input);
        }}
        className="panel flex flex-wrap items-center gap-2 px-3 py-2"
      >
        <span aria-hidden="true" className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          symbol
        </span>
        <input
          value={input}
          onChange={(e) => setInput(e.target.value.toUpperCase())}
          placeholder="e.g. NVDA"
          aria-label="company symbol to assemble its digital twin"
          className="chip mono min-h-[40px] w-40 bg-transparent px-3 outline-none"
          style={{ color: "var(--text)" }}
        />
        <button
          type="submit"
          disabled={loading || input.trim() === ""}
          className="chip min-h-[40px] cursor-pointer px-4 font-bold transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
          style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
        >
          {loading ? "Loading…" : "Load"}
        </button>
        {data && (
          <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
            loaded {data.symbol}
          </span>
        )}
      </form>

      {loading && <Skeleton lines={10} label={`assembling ${input} digital twin`} />}

      {!loading && err !== null && (
        <ErrorState
          message={err}
          hint={errHint}
          retry={() => load(input)}
        />
      )}

      {initial && (
        <EmptyState
          message="Enter a symbol and press Load to assemble its digital twin."
          detail="The twin is built from free SEC data — EDGAR identity, sector peers, executive (Form 4) trades, institutional (13F) holders, recent filings and XBRL fundamentals. Default example: NVDA."
        />
      )}

      {!loading && err === null && data && (
        <>
          {/* IDENTITY HEADER */}
          <section className="panel">
            <div className="panel-h flex-wrap gap-2">
              COMPANY DIGITAL TWIN
              <span className="chip" style={{ color: "var(--dim)" }}>
                {data.market || "—"}
              </span>
              <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                free public SEC data
              </span>
            </div>
            <div className="flex flex-col gap-3 px-4 py-3">
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <span className="mono text-lg font-bold" style={{ color: "var(--text)" }}>
                  {data.symbol}
                </span>
                <span style={{ color: "var(--dim)" }}>{data.name || company?.name || "—"}</span>
              </div>
              {company ? (
                <dl className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4">
                  <Meta
                    label="SECTOR (SIC)"
                    value={company.sicDesc || "—"}
                    title={company.sicDesc || "not classified by a filings sweep yet"}
                  />
                  <Meta label="EXCHANGE" value={company.exchange || "—"} />
                  <Meta label="SIC" value={company.sic || "—"} />
                  <Meta label="CIK" value={company.cik ? String(company.cik) : "—"} />
                </dl>
              ) : (
                <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                  No SEC identity on file —{" "}
                  {data.market === "crypto"
                    ? "crypto assets don't file with the SEC"
                    : "this symbol isn't matched to an EDGAR registrant"}
                  . The twin shows only what free public data provides for it.
                </p>
              )}
            </div>
          </section>

          {/* PEERS */}
          <section className="panel">
            <div className="panel-h flex-wrap gap-2">
              SECTOR PEERS
              <span className="chip tnum">{peers.length}</span>
              <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                same SIC industry — click to load its twin
              </span>
            </div>
            <div className="flex flex-wrap gap-2 px-4 py-3">
              {peers.length === 0 ? (
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  no sector peers on file
                </span>
              ) : (
                peers.map((p) => (
                  <button
                    key={p.ticker}
                    type="button"
                    onClick={() => {
                      setInput(p.ticker);
                      load(p.ticker);
                    }}
                    className="chip mono min-h-[32px] cursor-pointer px-3 font-bold transition-colors duration-150 hover:text-[var(--accent)]"
                    title={`${p.name || p.ticker} — load its digital twin`}
                  >
                    {p.ticker}
                  </button>
                ))
              )}
            </div>
          </section>

          {/* DATA CARDS — responsive 2-up grid */}
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            {/* EXECUTIVES / INSIDER FORM 4 */}
            <section className="panel">
              <div className="panel-h flex-wrap gap-2">
                EXECUTIVES · INSIDER FORM 4
                <span className="chip tnum">{insiders.length}</span>
                <span
                  className="ml-auto text-[0.75rem]"
                  style={{ color: "var(--faint)" }}
                  title="Only P (open-market buy) and S (open-market sale) are conviction trades; grants and exercises are mechanics."
                >
                  P=buy · S=sell · others = mechanics
                </span>
              </div>
              {insiders.length === 0 ? (
                <EmptyState
                  className="m-3"
                  message="No parsed insider filings"
                  detail="Form 4s are swept on a ~2h rotation; unmatched or crypto symbols have none. By law a Form 4 is filed ~2 business days after the trade."
                />
              ) : (
                <div className="table-wrap">
                  <table className="w-full text-[0.75rem]">
                    <caption className="sr-only">
                      Insider Form 4 transactions for {data.symbol}
                    </caption>
                    <thead>
                      <tr className={THEAD} style={THEAD_STYLE}>
                        <th scope="col" className="px-3 py-2">INSIDER</th>
                        <th scope="col" className="px-3 py-2">TITLE</th>
                        <th scope="col" className="px-3 py-2">CODE</th>
                        <th scope="col" className="px-3 py-2 text-right">VALUE</th>
                        <th scope="col" className="px-3 py-2 text-right">DATE</th>
                      </tr>
                    </thead>
                    <tbody className="tnum">
                      {insiders.slice(0, 12).map((t, i) => {
                        const c = insiderCode(t.code);
                        return (
                          <tr key={`${t.insider}-${t.ts}-${i}`} style={ROW_STYLE}>
                            <td className="px-3 py-2" style={{ color: "var(--text)" }}>
                              {t.insider || "—"}
                            </td>
                            <td
                              className="max-w-48 truncate px-3 py-2"
                              style={{ color: "var(--dim)" }}
                              title={t.title || undefined}
                            >
                              {t.title || "—"}
                            </td>
                            <td className="px-3 py-2">
                              <span className="chip" style={{ color: c.color, borderColor: c.color }}>
                                {c.label}
                              </span>
                            </td>
                            <td className="px-3 py-2 text-right" style={{ color: c.color }}>
                              {fmtUSD(t.value)}
                            </td>
                            <td className="px-3 py-2 text-right" style={{ color: "var(--faint)" }}>
                              {fmtDate(t.ts)}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
              {insiders.length > 12 && (
                <div className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  showing 12 of {insiders.length}
                </div>
              )}
            </section>

            {/* INSTITUTIONAL HOLDERS / 13F */}
            <section className="panel">
              <div className="panel-h flex-wrap gap-2">
                INSTITUTIONAL HOLDERS · 13F
                <span className="chip tnum">{holders.length}</span>
                <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  quarterly, lags up to 45 days
                </span>
              </div>
              {holders.length === 0 ? (
                <EmptyState
                  className="m-3"
                  message="No 13F holders on file"
                  detail="13F positions come from the curated manager set and lag up to 45 days after quarter-end; many symbols have none yet."
                />
              ) : (
                <div className="table-wrap">
                  <table className="w-full text-[0.75rem]">
                    <caption className="sr-only">
                      Institutional 13F holders of {data.symbol}
                    </caption>
                    <thead>
                      <tr className={THEAD} style={THEAD_STYLE}>
                        <th scope="col" className="px-3 py-2">MANAGER</th>
                        <th scope="col" className="px-3 py-2 text-right">VALUE</th>
                        <th scope="col" className="px-3 py-2 text-right">SHARES</th>
                      </tr>
                    </thead>
                    <tbody className="tnum">
                      {holders.slice(0, 12).map((h, i) => (
                        <tr key={`${h.manager}-${i}`} style={ROW_STYLE}>
                          <td
                            className="max-w-64 truncate px-3 py-2"
                            style={{ color: "var(--text)" }}
                            title={h.manager || undefined}
                          >
                            {h.manager || "—"}
                          </td>
                          <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                            {fmtUSD(h.value)}
                          </td>
                          <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                            {fmtCount(h.shares)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {holders.length > 12 && (
                <div className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  showing 12 of {holders.length}
                </div>
              )}
            </section>

            {/* FUNDAMENTALS */}
            <section className="panel">
              <div className="panel-h flex-wrap gap-2">
                FUNDAMENTALS
                <span className="chip tnum">{fundamentals.length}</span>
                <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  SEC EDGAR XBRL
                </span>
              </div>
              {fundamentals.length === 0 ? (
                <EmptyState
                  className="m-3"
                  message="No fundamentals on file"
                  detail="Company facts come from SEC EDGAR XBRL — a symbol with none hasn't been swept yet or doesn't file with the SEC."
                />
              ) : (
                <dl className="grid grid-cols-1 gap-x-6 gap-y-1 px-4 py-3 text-[0.75rem] sm:grid-cols-2">
                  {fundamentals.map((f, i) => (
                    <div
                      key={`${f.metric}-${i}`}
                      className="flex items-baseline justify-between gap-3 py-1"
                      style={{ borderBottom: "1px solid var(--border)" }}
                    >
                      <dt style={{ color: "var(--dim)" }}>{f.metric}</dt>
                      <dd
                        className="tnum"
                        style={{ color: "var(--text)" }}
                        title={f.asOf ? `as of ${fmtDate(f.asOf)}` : undefined}
                      >
                        {fmtFundamental(f.metric, f.value)}
                      </dd>
                    </div>
                  ))}
                </dl>
              )}
            </section>

            {/* FILINGS */}
            <section className="panel">
              <div className="panel-h flex-wrap gap-2">
                SEC FILINGS
                <span className="chip tnum">{filings.length}</span>
                <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  links open on SEC.gov
                </span>
              </div>
              {filings.length === 0 ? (
                <EmptyState
                  className="m-3"
                  message="No recent filings on file"
                  detail="Filings are pulled from SEC EDGAR; unmatched or crypto symbols have none."
                />
              ) : (
                <ul style={{ borderTop: "1px solid var(--border)" }}>
                  {filings.slice(0, 12).map((f, i) => (
                    <li
                      key={`${f.url}-${i}`}
                      className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-4 py-2.5 text-[0.75rem]"
                      style={ROW_STYLE}
                    >
                      <span className="chip mono shrink-0">{f.form || "—"}</span>
                      <a
                        href={f.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="min-w-0 flex-1 transition-colors duration-150 hover:text-[var(--accent)]"
                        style={{ color: "var(--text)" }}
                        title="open this filing on SEC.gov EDGAR"
                      >
                        {f.label || f.title || "SEC filing"}{" "}
                        <span aria-hidden="true">↗</span>
                        <span className="sr-only"> (opens in a new tab on SEC.gov)</span>
                      </a>
                      <span className="tnum ml-auto shrink-0" style={{ color: "var(--faint)" }}>
                        {fmtDate(f.filedTs)}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              {filings.length > 12 && (
                <div className="px-3 py-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  showing 12 of {filings.length}
                </div>
              )}
            </section>
          </div>

          {/* AI PROFILE — opt-in, one model call */}
          <section className="panel">
            <div className="panel-h flex-wrap gap-2">
              AI PROFILE
              {data.profileModel && (
                <span className="chip" style={{ color: "var(--accent)", borderColor: "var(--accent)" }}>
                  {data.profileModel}
                </span>
              )}
              <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
                off by default · costs one model call
              </span>
            </div>
            <div className="flex flex-col gap-3 px-4 py-3">
              {data.profile ? (
                <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                  {data.profile}
                </p>
              ) : (
                <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                  Generate a one-paragraph plain-English profile from the public data above. It is
                  produced on demand — one model call — and off by default.
                </p>
              )}
              {summaryErr && (
                <p className="text-[0.75rem]" role="alert" style={{ color: "var(--bad)" }}>
                  {summaryErr}
                </p>
              )}
              <div>
                <button
                  type="button"
                  onClick={() => load(data.symbol, true)}
                  disabled={summarizing}
                  aria-busy={summarizing}
                  className="chip min-h-[40px] cursor-pointer px-4 font-bold transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
                  style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
                >
                  {summarizing
                    ? "Generating…"
                    : data.profile
                      ? "Regenerate AI summary"
                      : "Generate AI summary"}
                </button>
              </div>
            </div>
          </section>

          {/* HONEST CAVEAT — the daemon's own note, verbatim */}
          {data.note && (
            <p
              className="panel px-4 py-2 text-[0.75rem] leading-relaxed"
              style={{ color: "var(--faint)" }}
            >
              {data.note}
            </p>
          )}
        </>
      )}
    </div>
  );
}
