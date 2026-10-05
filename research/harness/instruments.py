"""Classify a US-listed ticker as common equity or a non-equity instrument.

WHY THIS EXISTS. `research/dirfix/extract.py` filtered funds by NAME regex
(`FUND_PAT`). Measured 2026-08-29 that filter leaked 353 non-common-equity
instruments -- 13.1% of the 2,694 symbols that survived it: 254 SPAC units,
58 warrants, 12 rights, 18 preferred/notes, and 11 closed-end municipal bond
funds (BAF, BBF, BBK, BFY, BSD, BSE, EVY, MTT ...). Each has a return
distribution that is not the equity one: warrants and rights are option-like
and dominate tail statistics, SPAC units sit pinned near $10 pre-deal, and
closed-end muni funds are interest-rate instruments.

Extending the name regex is NOT the fix -- it false-positives on real
operating companies: Aardvark Therapeutics (AARD), Core Mark (CORE), CyberArk
(CYBR), American Woodmark (AMWD), Cimarex (XEC). String matching cannot
separate these from the instruments above.

EDGAR SIC codes were evaluated and REJECTED: only 951 of 2,943 stock symbols
join the `companies` table, and none of the closed-end funds appear in it.

So the primary rule here is the exchange TICKER-SUFFIX convention, which is
deterministic rather than lexical, with the name patterns kept only for the
cases a suffix cannot express (ETFs and closed-end funds).
"""
import re

FUND_PAT = (r"ETF|ETN|Fund|ProShares|Direxion|iShares|SPDR|Invesco|Vanguard|"
            r"UltraShort|UltraPro|Ultra|Bear|Bull|[23]X|"
            r"Leveraged|Select Sector|Daily Target|Index Trust")

# Closed-end funds and fixed-income wrappers filed under market='stocks'.
# Deliberately does NOT match bare "Trust": "QTS REALTY TRUST" is a real REIT,
# and "Ordinary Shares" / "American Depositary Shares" are real foreign
# issuers. That exclusion is why the muni funds slipped past FUND_PAT.
CEF_PAT = (r"Municipal|Muni(cipal)? |Income Trust|Income Investment|"
           r"Bond Trust|Bond Fund|Defined Opportunity|Senior Loan|"
           r"Term Trust|Strategic Muni")

# Nasdaq/NYSE 5th-letter class suffixes. Applied ONLY to 5-character tickers,
# so 4-character names like CYBR and AARD are untouched.
# 'L' is deliberately EXCLUDED: GOOGL is common stock.
CLASS_SUFFIX = ("W", "R", "U", "P")

_FUND_RE = re.compile(FUND_PAT, re.IGNORECASE)
_CEF_RE = re.compile(CEF_PAT, re.IGNORECASE)


def instrument_class(symbol, name):
    """Return None for common equity, else a short string naming why it is not."""
    name = name or ""
    # Dotted form first: "BYN.U" is 5 characters and ends in U, so the
    # 5th-letter rule would claim it and mislabel a dotted class suffix.
    if "." in symbol and symbol.rsplit(".", 1)[-1] in ("U", "W", "R"):
        return "dotsuffix"
    if len(symbol) == 5 and symbol[-1] in CLASS_SUFFIX:
        return "suffix-" + symbol[-1]
    if _CEF_RE.search(name):
        return "closed-end"
    if _FUND_RE.search(name):
        return "fund"
    return None


def is_common_equity(symbol, name):
    return instrument_class(symbol, name) is None


def demo():
    equities = [
        ("CYBR", "CyberArk Software Ltd."),
        ("AARD", "Aardvark Therapeutics, Inc. Common Stock"),
        ("CORE", "Core Mark Holding Co Inc Common Stock"),
        ("AMWD", "AMERICAN WOODMARK CORP"),
        ("XEC", "Cimarex Energy"),
        ("AMK", "AssetMark Financial Holdings, Inc."),
        ("QTS", "QTS REALTY TRUST, INC."),
        ("GOOGL", "Alphabet Inc. Class A"),
        ("AAPL", "Apple Inc."),
        ("PHH", "Park Ha Biological Technology Co., Ltd. Ordinary Shares"),
    ]
    for sym, nm in equities:
        assert is_common_equity(sym, nm), (
            "%s (%s) must be common equity, got %r"
            % (sym, nm, instrument_class(sym, nm)))

    non_equity = [
        ("AACIW", "Warrant", "suffix-W"),
        ("EOSER", "Rights", "suffix-R"),
        ("AACQU", "Units", "suffix-U"),
        ("ACGLP", "Preferred", "suffix-P"),
        ("BYN.U", "", "dotsuffix"),
        ("BAF", "BLACKROCK MUNICIPAL INCOME INVESTMENT QUALITY TRUST", "closed-end"),
        ("SPY", "SPDR S&P 500 ETF Trust", "fund"),
        ("TSLZ", "T-Rex 2X Inverse Tesla Daily Target ETF", "fund"),
    ]
    for sym, nm, want in non_equity:
        got = instrument_class(sym, nm)
        assert got == want, "%s: want %r, got %r" % (sym, want, got)

    assert instrument_class("GOOGL", "Alphabet Inc. Class A") is None, (
        "GOOGL was flagged -- this is precisely why 'L' is excluded from "
        "CLASS_SUFFIX; a 5-letter L suffix is a share class, not a preferred")

    assert is_common_equity("AAPL", None) is True, "name=None must not raise"


if __name__ == "__main__":
    demo()
    print("OK")
