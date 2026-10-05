"""Frozen cases for the Form 25 ticker resolver. Run it: python test_form25_resolve.py

Four live EDGAR fetches plus one offline assert. R1.htm is a rendered artifact,
not a documented API -- if SEC changes the markup, SYM_RE stops matching and the
resolver silently returns None for everything. These asserts fail loudly instead.
"""
from fetch_form25 import (ticker_from_edgar, live_bars, trades_after_delisting,
                          has_spliced_gap)

# JOANN: the baseline. Ticker and exchange both come off the cover page.
assert ticker_from_edgar(1834585, "2024-04-09") == ("JOAN", "NASDAQ")

# Casa Systems: the date cutoff. Its LATEST cover says CASSQ (the OTC symbol it
# re-tickered to); CASA is the Nasdaq listing whose bars we want.
assert ticker_from_edgar(1333835, "2024-04-15")[0] == "CASA"

# The one that matters most: a fund trust must NOT resolve. Exchange Listed
# Funds Trust files one Form 25 per ETF share class and its fund-form cover
# names an arbitrary member fund (TDSB), which is not the delisted security.
# This fails if COVER_FORMS is ever widened carelessly.
assert ticker_from_edgar(1547950, "2024-01-08")[0] is None

# Agile Therapeutics: three later covers say "N/A" and must be rejected, and the
# 8-K that still says AGRX is the 7th filing back -- fails if R1_BUDGET drops.
assert ticker_from_edgar(1261249, "2024-06-03")[0] == "AGRX"

# Alpaca's zero-volume carry-forward padding is not a trade.
assert live_bars([{"t": "2024-01-02", "v": 9, "c": 5.0},
                  {"t": "2024-01-03", "v": 0, "c": 5.0}])[-1]["t"][:10] == "2024-01-02"

# Nor is a bar the vendor returned with volume but NO PRICE. LeddarTech (LDTC)
# has 373 of these from its pre-deSPAC shell period — real volume, OHLC all
# 0.0 — and they are worse than useless: a zero previous close makes
# close[k]/close[k-1] a division by zero, which is exactly how
# tools/xsfactor_edge.py died right after they were first imported.
assert live_bars([{"t": "2024-01-02", "v": 800, "c": 0.0},
                  {"t": "2024-01-03", "v": 8812, "c": 6.08}]) == \
       [{"t": "2024-01-03", "v": 8812, "c": 6.08}]


# ── The ETF ticker-reuse guard ────────────────────────────────────────────────
# company_tickers.json is a REGISTRANT->ticker map: no ETFs, no closed-end
# funds, and 6,208 of Alpaca's 13,324 active tradable symbols are missing from
# it. Funds are the dominant reuser of a freed 3-4 letter ticker, so that file
# alone leaks exactly where reuse concentrates. It let five through -- HLTH
# spliced Cue Health's close of 0.0502 onto the Tema Healthcare AI ETF's 25.40
# across a 778-day gap, a single step of +50,498% into the training set.
# These asserts are what stop that class coming back.
def _b(day, v=100.0):
    return {"t": day + "T00:00:00Z", "v": v, "c": 1.0}

# HLTH and CONX: trading resumes hundreds of days after the Form 25.
assert trades_after_delisting([_b("2024-06-05"), _b("2026-07-23")], "2024-06-11") is True
assert trades_after_delisting([_b("2024-05-03"), _b("2025-11-19")], "2024-05-10") is True
# A genuine delisting stops within days of its filing, either side of it.
assert trades_after_delisting([_b("2024-03-01"), _b("2024-03-05")], "2024-03-08") is False
# A settlement tail is not reuse; the grace window exists for it.
assert trades_after_delisting([_b("2024-03-01"), _b("2024-03-20")], "2024-03-08") is False
# The grace boundary is exact: 30 days passes, 31 does not.
assert trades_after_delisting([_b("2024-03-01"), _b("2024-04-07")], "2024-03-08") is False
assert trades_after_delisting([_b("2024-03-01"), _b("2024-04-08")], "2024-03-08") is True
# Zero-volume padding after the filing must NOT read as continued trading,
# otherwise every padded delisting looks like a reuse and none get imported.
assert trades_after_delisting([_b("2024-03-01"), _b("2026-01-01", v=0.0)], "2024-03-08") is False


# ── The spliced-series guard ──────────────────────────────────────────────────
# The case the other two guards structurally cannot see: a ticker recycled onto
# a company that ALSO later delisted. Its last bar sits near its own Form 25, so
# trades_after_delisting is silent; neither issuer is listed today, so
# company_tickers.json is silent. 10 of 1,231 kept symbols in the 2023-2026 run.
def _p(day, c, v=100.0):
    return {"t": day + "T00:00:00Z", "v": v, "c": c}

# RDUS: Radius Global Infrastructure, then Radius Recycling. ALTM: Altus
# Midstream, then Arcadium Lithium.
assert has_spliced_gap([_p("2022-08-12", 9.42), _p("2023-09-01", 30.83)]) is True
assert has_spliced_gap([_p("2022-02-22", 62.34), _p("2024-01-04", 6.80)]) is True

# BOTH controls matter more than the positives, because each protects a real
# delisting we must NOT throw away:
#   a genuine collapse is a big step with NO gap — that is the bankruptcy signal
assert has_spliced_gap([_p("2024-01-02", 10.0), _p("2024-01-03", 2.0)]) is False
#   a thinly-traded SPAC unit is a big gap with a FLAT price (MLACU: 10.46 -> 10.00
#   across 562 days). 108 of 1,231 have a >90d hole; only 10 also jump.
assert has_spliced_gap([_p("2023-05-31", 10.46), _p("2024-12-13", 10.00)]) is False
assert has_spliced_gap([_p("2024-01-02", 10.0), _p("2024-01-03", 9.0)]) is False

print("ok: 18/18")
