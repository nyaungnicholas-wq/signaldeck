# P11 — `bars-1d` completeness: the foundation under every PIT claim, measured

**Filed:** 2026-08-04
**Closes:** `STRATEGY_DECK.md` §14 action 6 — *"Audit the completeness of the `bars-1d`
history itself. Every point-in-time and survivorship claim now rests on it, and nothing
in the repository currently measures it."*
**Tool:** `tools/bars_completeness.py` (read-only, `mode=ro`, writes nothing)

---

## 1. Why this was the top of the list

`universe_membership` holds 1,854,228 rows and **every one carries
`source = 'bars-1d'`**. The point-in-time universe is therefore a projection of the
daily-bar history and nothing else. P3B verified the table is internally consistent — no
membership before a symbol's first bar, none after its last or after `delisted_at`, and
the universe correctly shrinks (1,501 symbols in 2021 against 998 in 2023).

None of that tests whether the bars themselves are complete. A symbol missing from the
bar history on a given day is simply absent from the universe that day, silently and
without contradiction. The verification could not see it, by construction.

## 2. Method, and its one assumption

There is no exchange calendar in this repository, and adding a dependency for one is its
own risk. The calendar is derived from the data: **the stocks symbol with the most daily
bars defines the trading sessions**, because a continuously-listed liquid name prints on
every session.

That resolved to **SPY, 1,907 sessions**.

The assumption is stated rather than hidden: *a session SPY itself missed is a session
this tool cannot see.* Crypto is measured separately against all calendar days, since it
trades seven days a week.

Two kinds of gap are distinguished, because they mean different things:

- **Interior gap** — missing days between bars the symbol did print. The universe
  under-counts it on those days. Bounded and visible.
- **Trailing gap** — the symbol stops printing before the calendar ends and carries no
  `delisted_at`. **This is the survivorship-relevant one**: the name leaves the universe
  without being recorded as dead, which is indistinguishable from "we stopped looking."

## 3. Result

```
calendar        1907 sessions, from SPY
stocks          1770 symbols, coverage 96.49% (1,849,056/1,916,310 symbol-days)
                532 symbols with an interior gap
                10 symbols stop early with NO delisted_at
crypto          7 symbols, coverage 100.00% (5,232/5,232 symbol-days)
```

### 3.1 The shortfall splits, and the split changes the reading

A third of the missing days belong to instruments that trade sporadically **by
construction** — rights, warrants and units (`.RT`, `.WS`, `W`/`R`/`U` suffixes). For
those, a day without a print is not a data defect.

| Instrument class | Expected | Actual | Coverage | Missing |
|---|---:|---:|---:|---:|
| Sporadic (rights / warrants / units) | 257,771 | 234,374 | **90.92%** | 23,397 |
| **Common stock** | 1,658,539 | 1,614,682 | **97.36%** | 43,857 |
| Total | 1,916,310 | 1,849,056 | 96.49% | 67,254 |

**34.8%** of all missing symbol-days are sporadic instruments.

The number that matters for any backtest over ordinary equities is therefore
**97.36%**, not 96.49%. The worst individual offenders confirm the classification —
`RQI.RT` at 2.03% coverage, `OPFI.WS`, `QTEXW`, `EVLVW` — these are rights and warrants
behaving normally.

### 3.2 The survivorship-relevant number is small, and that is the finding

**10 symbols of 1,770** stop printing at least 10 sessions before the calendar ends
while carrying no `delisted_at`:

```
XOMA 16 · RQI.RT 15 · QTEXW 14 · EVLVW 13 · OPFI.WS 12
EOSER 11 · FFAIW 11 · AMPGR 11 · CELUW 10 · WGSWW 10
```

Nine of the ten are warrants, rights or units. All ten are recent (10–16 sessions),
which is consistent with `P3A_SURVIVORSHIP_BACKFILL.md`'s statement that the live
delisting detector only began watching in 2026.

This **bounds** the residual survivorship exposure rather than merely restating it:
whatever the 2023–2025 under-coverage costs, it is not currently leaking a large
population of silently-vanished common stocks into the universe. One name (`XOMA`) is a
common stock and is worth a look on its own.

## 4. What this does and does not license

**Does:** the point-in-time universe rests on a bar history that is 97.36% complete for
common stock and 100% for crypto. That is a measured floor under P3B's verification,
where previously there was none.

**Does not:** 43,857 missing common-stock symbol-days remain. Any cross-sectional
denominator on an affected day is understated by however many names were missing. The
effect is small and diffuse rather than directional, but it is not zero, and this
measurement does not repair it — it sizes it.

`FC3` (survivorship) stays frozen for 2023–2025 on the strength of
`P3A_SURVIVORSHIP_BACKFILL.md`. Nothing here lifts it. This measurement addresses a
different question — *is the bar history complete* — and answers it with a number.

## 5. Reproduce

```bash
python tools/bars_completeness.py              # human-readable
python tools/bars_completeness.py --json       # machine-readable
python tools/bars_completeness.py --self-check # prove the arithmetic
```

The self-check builds an in-memory fixture whose answer is known by hand — a reference
symbol printing all 10 sessions, one symbol missing exactly 2 interior days, one
stopping early — and asserts the calendar, the expected/actual totals, the interior-gap
count and the trailing-gap floor. It passes:

```
self-check OK: coverage, interior gaps and the trailing floor all behave
```

A completeness tool that cannot demonstrate it counts correctly is worth no more than
the assumption it replaces.
