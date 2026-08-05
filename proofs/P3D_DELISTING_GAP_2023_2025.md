# P3D — Closing the 2023–2025 delisting gap

**Date:** 2026-08-04 · **Opened by:** `proofs/P3A_SURVIVORSHIP_BACKFILL.md` §3
· **Status: resolver fixed and measured; production merge pending the full-range fetch**

---

## 1. The gap

`proofs/P3A_SURVIVORSHIP_BACKFILL.md` measured the delisting record as plausible
for 2020–2022 and implausibly thin afterwards:

| year | delistings recorded | rate |
|---|---|---|
| 2021 | 333 | 25.1% |
| 2022 | 243 | 20.8% |
| **2023** | **25** | **2.6%** |
| **2024** | **26** | **2.7%** |
| **2025** | **43** | **4.3%** |

P3A attributed this to the vendor source being thin in recent years. **That was
half right and the wrong half was load-bearing.** The source is not thin — the
*resolver* could not name the companies it found.

## 2. Root cause: a resolver that cannot, by construction, resolve

`tools/alpha/fetch_form25.py` found the delistings correctly — 1,817 Form 25
filings — and then failed to turn CIKs into tickers, leaving **592 of 841
unresolved**. Its `cik_to_ticker()` reads
`https://www.sec.gov/files/company_tickers.json`.

**That file lists only CURRENTLY-LISTED companies.** A Form 25 filer is a company
that just delisted, so it is absent from that file *by construction*. The
resolver was asking a directory of the living for the names of the dead.

### The fix is not the obvious one

The obvious next step — `data.sec.gov/submissions/CIK##########.json` — **does
not work either**, and I asserted that it did before it was measured. I tested
the endpoint on two companies that are still listed (Array Technologies, CME),
saw tickers, and generalised. On 20 randomly-sampled genuinely-delisted CIKs:

- `tickers[]` empty — **20/20**
- `exchanges[]` empty — **20/20**
- `formerNames` present on 8/20, but it carries prior *company names*, never a symbol

The submissions feed draws from the same current-listings table. The Form 25
document itself carries issuer name, CIK, file number and
`descriptionClassSecurity` — **no ticker** (verified on JOANN's
`0001354457-24-000261`). `companyconcept/.../dei/TradingSymbol.json` returns
**HTTP 404** — that API does not serve string facts.

**What works:** EDGAR renders `dei:TradingSymbol` into `R1.htm`, the cover page
of every inline-XBRL filing. A delisted company's own last 8-K still names the
ticker it traded under. Route measurements at n=20:

| route | yield |
|---|---|
| iXBRL filename prefix (`{ticker}-{yyyymmdd}.htm`) | 6/20, and *disagreed with truth* on Casa Systems |
| **`R1.htm` `dei:TradingSymbol`, newest filing filed ≤ Form 25 date** | **18/20** |
| same, but newest filing overall | 17/20 |

The date precedence matters: all 3 disagreements favoured "filed before the
Form 25". Casa Systems resolves to `CASA` under that rule and to the
post-delisting `CASSQ` without it. Fund cover forms (`485BPOS`, `N-CSR`, `497`)
are excluded on purpose — a fund trust files one Form 25 per ETF share class and
its cover names one arbitrary member fund, which produced a false `TDSB`.

## 3. A second bug, larger than the first

Alpaca pads dead securities with flat, zero-volume carry-forward bars, so
`bars[-1]` is **not the last trade**. Delistings were being dated from padding:

| symbol | Form 25 filed | last *real* trade | error |
|---|---|---|---|
| GLOG | 2024-05-28 | 2021-06-08 | ~3 years |
| QIWI | 2024-09-06 | 2022-02-25 | ~2.5 years |

Only volume-bearing bars are now counted and dated.
`SELECT COUNT(*) FROM delisted_bar WHERE volume<=0` returns **0** — the
synthetic padding never enters the training set.

## 4. A third bug, blocking every `data.sec.gov` call

`sec_get()` hardcoded `Host: www.sec.gov` on every request. Verified
independently against the live API:

```
WITH hardcoded Host    -> HTTP Error 404: Not Found
WITHOUT Host           -> HTTP 200, 164379 bytes
```

Deleted; urllib derives Host from the URL.

## 5. Measured result (2024, like-for-like)

The report already on disk turned out to be a 2024-only run, which makes it an
exact baseline: its `form25_filings` (1,817) and `resolved_tickers` (249)
reproduce precisely.

| metric | before | after |
|---|---|---|
| unique CIKs | 841 | 841 |
| **resolved to tickers** | **249 (29.6%)** | **716 (85.1%)** |
| unresolved CIKs | 592 | 124 |
| **delistings added to staging** | **2 of 246 (0.8%)** | **373 of 716 (52.1%)** |
| bars added | 1,194 | 241,567 |

**Resolution improved 2.88×; unresolved fell 79.1%.**

The residual 124 (14.7%) lands on the predicted 14.6% ceiling, and it is not
failure — it is composition. Of the 67 residuals in the 2024 H1 population:
**43 fund/ETF trusts, 15 debt/LP/subsidiary co-issuers, 5 exchange CIKs
(NYSE LLC itself files Form 25s), and 2 genuine misses** (YanGuFang,
Next.e.GO — foreign private issuers with zero inline-XBRL filings). None of the
first 63 ever had an equity ticker to recover.

Cost: 445 CIKs in ~195 s at 0.15 s spacing, **zero HTTP 429**.

## 6. Merge rehearsal (on a copy, before production)

Pipeline is the existing composition — `fetch_form25.py` → `tag_delisted.py` →
`sdmaint import-delisted` — so no new import path was written.

```
$ ./bin/sdmaint.exe import-delisted -db <rehearsal copy> -staging <tagged> -dry-run
{ "staging_candidates": 373, "imported": 373, "bars_written": 241567,
  "refused_active": 0, "skipped_cohort": 0, "skipped_too_short": 0,
  "by_cohort": { "collapse": 165, "ordinary": 155, "spac_shell": 52, "spac_named": 1 } }
```

`refused_active: 0` — no recovered ticker collides with a currently-active
symbol, so the ticker-reuse guard has nothing to refuse. **165 `collapse`**
(died cheap, or below a fifth of peak) is the bankruptcy cohort — precisely the
population whose absence makes a model learn to buy falling knives.

## 7. Status and what remains

- **Resolver: fixed and verified.** `tools/alpha/test_form25_resolve.py` passes
  5/5 and is the tripwire for `R1.htm` being a *rendered artifact* rather than a
  documented API — if SEC changes the `defref_dei_TradingSymbol` anchor, the
  regex silently stops matching everywhere and only that test would catch it.
- **Full-range fetch (2023–2026) running.** The 2024 measurement above is one
  year; the earlier cost estimate was ~4× low because 841 CIKs is 2024 *alone*,
  not the whole span. Expect roughly 3,000–3,400 CIKs.
- **Production merge pending** that fetch, then a dry-run on a copy, then apply,
  then `sdmaint build-universe`.
- **Not done, and out of this phase's scope:** repairing the already-corrupt
  `delisted_at` rows in production that were dated from zero-volume padding
  (37 mis-dated), and the 18,841 zero-volume 1d bars already in `bars`. Both are
  in-DB corrections needing a Go command under the single-writer doctrine.
