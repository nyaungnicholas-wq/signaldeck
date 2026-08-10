# TARGETS: new file -> drafts/research_tests/conftest.py  (test-only; nothing in
#          research/ or tools/ is modified or imported for its side effects)
# APPLY:   already in place. Run from the repo root:
#            .venv/Scripts/python.exe -m pytest drafts/research_tests/ -q
#          No install step, no PYTHONPATH, no conftest anywhere else.
#
# WHY A LOADER INSTEAD OF `import`:
#   research/eighty/h*.py are SCRIPTS, not modules. Every one of them opens
#   data/signaldeck.db and runs queries at module scope (248/254 contain
#   "sqlite3"). `import h0209` would hit the live DB and, for most files,
#   sys.exit(). So `load_defs()` below parses the file and executes ONLY its
#   imports, module-level constants and def/class statements. The function
#   bodies under test are byte-for-byte the ones in research/ -- nothing is
#   copied, re-typed or stubbed. If a function's body ever changes, these tests
#   see the change on the next run.

from __future__ import annotations

import ast
import math
import sqlite3
import sys
import types
from pathlib import Path

import numpy as np
import pytest

REPO = Path(__file__).resolve().parents[2]
RESEARCH = REPO / "research" / "eighty"
TOOLS = REPO / "tools"

# sys.path shim: makes tools/ importable with no install. research/eighty is
# deliberately NOT put on sys.path -- see the loader note above.
if str(TOOLS) not in sys.path:
    sys.path.insert(0, str(TOOLS))


# --------------------------------------------------------------------------
# loading definitions out of a script without running the script
# --------------------------------------------------------------------------

_SAFE_TOP = (ast.Import, ast.ImportFrom, ast.FunctionDef, ast.AsyncFunctionDef,
             ast.ClassDef)


def load_defs(path: Path) -> types.SimpleNamespace:
    """Exec only imports / constants / defs from a research script.

    Raises SyntaxError if the file does not parse -- that is a real result, not
    a harness problem, and test_integrity.py asserts on it.
    """
    src = path.read_text(encoding="utf-8", errors="replace")
    tree = ast.parse(src, filename=str(path))

    kept: list[ast.stmt] = []
    for node in tree.body:
        if isinstance(node, _SAFE_TOP):
            kept.append(node)
        elif isinstance(node, (ast.Assign, ast.AnnAssign)):
            # module-level constants the defs close over -- LITERALS ONLY.
            # Anything referencing another name would drag in the script's
            # execution order (and anything containing a Call would open the DB).
            value = node.value
            if value is None:
                continue
            try:
                ast.literal_eval(value)
            except (ValueError, TypeError, SyntaxError, MemoryError):
                continue
            kept.append(node)

    mod = ast.Module(body=kept, type_ignores=[])
    ast.fix_missing_locations(mod)
    ns: dict = {"__name__": f"_loaded_{path.stem}", "__file__": str(path)}
    exec(compile(mod, str(path), "exec"), ns)  # noqa: S102 - test harness
    return types.SimpleNamespace(**ns)


@pytest.fixture(scope="session")
def research_dir() -> Path:
    return RESEARCH


@pytest.fixture(scope="session")
def hyp():
    """hyp("h0209") -> namespace of that hypothesis's pure functions."""
    cache: dict[str, types.SimpleNamespace] = {}

    def _load(stem: str):
        if stem not in cache:
            cache[stem] = load_defs(RESEARCH / f"{stem}.py")
        return cache[stem]

    return _load


# --------------------------------------------------------------------------
# schema fixture -- transcribed from the live DB, not invented
# --------------------------------------------------------------------------
# Source of truth (read-only, 2026-08-06):
#   sqlite3 "file:data/signaldeck.db?mode=ro" ".schema bars" ...
# Subset chosen by what research/eighty actually SELECTs FROM, by frequency:
#   bars 227, symbols 92, prediction_outcomes 92, insider_trades 39,
#   sentiment_features 23, stocktwits_sentiment 11.
# Indexes and REFERENCES are kept because they encode real constraints (the
# tf CHECK and the WITHOUT ROWID PKs are what make duplicate-bar bugs surface).

SCHEMA = """
CREATE TABLE symbols (
  id       INTEGER PRIMARY KEY,
  symbol   TEXT NOT NULL,
  market   TEXT NOT NULL CHECK (market IN ('crypto','stocks')),
  name     TEXT NOT NULL DEFAULT '',
  active   INTEGER NOT NULL DEFAULT 1,
  added_at INTEGER NOT NULL,
  stream   INTEGER NOT NULL DEFAULT 0,
  delisted_at INTEGER,
  UNIQUE (symbol, market)
);
CREATE TABLE bars (
  symbol_id INTEGER NOT NULL,
  tf        TEXT NOT NULL CHECK (tf IN ('1m','1h','1d')),
  ts        INTEGER NOT NULL,
  open REAL NOT NULL, high REAL NOT NULL, low REAL NOT NULL, close REAL NOT NULL,
  volume REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (symbol_id, tf, ts)
) WITHOUT ROWID;
CREATE INDEX idx_bars_tf_sym_ts ON bars (tf, symbol_id, ts, close);
CREATE TABLE prediction_outcomes (
  symbol_id   INTEGER NOT NULL,
  horizon     TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  prob        REAL NOT NULL,
  up          INTEGER,
  fwd_return  REAL,
  resolved_at INTEGER,
  basis_epoch INTEGER,
  PRIMARY KEY (symbol_id, horizon, ts)
) WITHOUT ROWID;
CREATE TABLE sentiment_features (
  symbol_id  INTEGER NOT NULL REFERENCES symbols(id),
  day        TEXT    NOT NULL,
  n_polar    INTEGER NOT NULL,
  n_all      INTEGER NOT NULL,
  mean_score REAL    NOT NULL,
  pos        INTEGER NOT NULL DEFAULT 0,
  neg        INTEGER NOT NULL DEFAULT 0,
  hedged     INTEGER NOT NULL DEFAULT 0,
  ver        INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, day)
) WITHOUT ROWID;
CREATE TABLE stocktwits_sentiment (
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  ts        INTEGER NOT NULL,
  bullish   INTEGER NOT NULL,
  bearish   INTEGER NOT NULL,
  untagged  INTEGER NOT NULL,
  total     INTEGER NOT NULL,
  PRIMARY KEY (symbol_id, ts)
);
CREATE TABLE insider_trades (
  accession TEXT PRIMARY KEY,
  symbol_id INTEGER NOT NULL REFERENCES symbols(id),
  insider   TEXT NOT NULL DEFAULT '',
  title     TEXT NOT NULL DEFAULT '',
  code      TEXT NOT NULL DEFAULT '',
  shares    REAL NOT NULL DEFAULT 0,
  price     REAL NOT NULL DEFAULT 0,
  value     REAL NOT NULL DEFAULT 0,
  tx_ts     INTEGER NOT NULL DEFAULT 0,
  filed_ts  INTEGER NOT NULL DEFAULT 0
);
"""

DAY = 86400
EPOCH0 = 1_600_000_000  # arbitrary fixed Monday-ish anchor; determinism matters, the date does not


@pytest.fixture
def db() -> sqlite3.Connection:
    """In-memory DB carrying the real schema subset. Never touches the live file."""
    con = sqlite3.connect(":memory:")
    con.executescript(SCHEMA)
    con.row_factory = sqlite3.Row
    yield con
    con.close()


@pytest.fixture
def rng() -> np.random.Generator:
    """Seeded generator. Same stream every run, on every machine."""
    return np.random.default_rng(20260806)


@pytest.fixture
def gbm(rng):
    """Deterministic geometric-Brownian close series -> list[float].

    Positive by construction, so log-return code is exercised on valid input.
    """

    def _make(n: int = 400, s0: float = 100.0, sigma: float = 0.02,
              mu: float = 0.0003, seed: int | None = None) -> list[float]:
        g = np.random.default_rng(seed) if seed is not None else rng
        shocks = g.normal(mu, sigma, size=n - 1)
        return [float(x) for x in s0 * np.exp(np.concatenate([[0.0], np.cumsum(shocks)]))]

    return _make


@pytest.fixture
def bars(gbm, rng):
    """Bars in the tuple shapes the research scripts actually unpack.

    shape= selects the layout, named for the file that uses it:
      "ts_close"        -> (ts, close)                      h0241
      "ts_o_c_v"        -> (ts, open, close, volume)        h0209
      "ts_close_vol"    -> (ts, close, volume)              h0248
      "d_o_h_l_c_v"     -> (date, open, high, low, close, volume)  h0221
    """

    def _make(n: int = 400, shape: str = "ts_close", seed: int = 7,
              vol: float = 1e6):
        closes = gbm(n=n, seed=seed)
        g = np.random.default_rng(seed + 1)
        vols = [float(v) for v in g.uniform(0.5 * vol, 1.5 * vol, size=n)]
        opens = [c * (1 + float(x)) for c, x in
                 zip(closes, g.normal(0, 0.002, size=n))]
        ts = [EPOCH0 + i * DAY for i in range(n)]
        if shape == "ts_close":
            return [(ts[i], closes[i]) for i in range(n)]
        if shape == "ts_o_c_v":
            return [(ts[i], opens[i], closes[i], vols[i]) for i in range(n)]
        if shape == "ts_close_vol":
            return [(ts[i], closes[i], vols[i]) for i in range(n)]
        if shape == "d_o_h_l_c_v":
            return [(ts[i], opens[i], max(opens[i], closes[i]),
                     min(opens[i], closes[i]), closes[i], vols[i])
                    for i in range(n)]
        raise ValueError(f"unknown shape {shape!r}")

    return _make


@pytest.fixture
def calibrated_probs(rng):
    """(ps, ys) that are genuinely calibrated: P(y=1 | p) == p by construction.

    Used as the reference point for Brier decomposition and reliability tests:
    a calibrated forecaster must have ~zero reliability term.
    """

    def _make(n: int = 20000):
        ps = rng.uniform(0.02, 0.98, size=n)
        ys = (rng.uniform(size=n) < ps).astype(int)
        return [float(p) for p in ps], [int(y) for y in ys]

    return _make


def brier_decomposition(ps, ys):
    """Murphy (1973): BS = reliability - resolution + uncertainty, EXACTLY.

    Grouped by DISTINCT forecast value, which is the form in which the identity
    is exact. (Equal-width binning of a continuous forecast leaves a within-bin
    covariance residual, so the identity would only hold approximately and a
    test asserting equality on it would be asserting something false. Tests that
    need this identity quantize their forecasts first -- see quantize().)

    Written here in the harness on purpose: it is the INDEPENDENT reference that
    the production brier() in tools/leg_audit.py is checked against.
    """
    n = len(ps)
    base = sum(ys) / n
    groups: dict[float, list[int]] = {}
    for i, p in enumerate(ps):
        groups.setdefault(p, []).append(i)
    rel = res = 0.0
    for p, idx in groups.items():
        nk = len(idx)
        ok = sum(ys[i] for i in idx) / nk
        rel += nk * (p - ok) ** 2
        res += nk * (ok - base) ** 2
    return {"reliability": rel / n, "resolution": res / n,
            "uncertainty": base * (1 - base)}


def quantize(ps, step: float = 0.05):
    """Snap forecasts to a finite grid so the exact decomposition applies."""
    return [round(round(p / step) * step, 10) for p in ps]


def reliability_bins(ps, ys, nbins: int = 10):
    """Equal-width reliability curve -> [(bin_lo, n, mean_p, obs_rate), ...].

    Empty bins are omitted rather than imputed: a bin with no forecasts has no
    observed rate, and inventing one is exactly the kind of fabrication these
    tests exist to catch.
    """
    out = []
    for b in range(nbins):
        lo, hi = b / nbins, (b + 1) / nbins
        idx = [i for i, p in enumerate(ps)
               if p >= lo and (p < hi or (b == nbins - 1 and p <= hi))]
        if not idx:
            continue
        out.append((lo, len(idx),
                    sum(ps[i] for i in idx) / len(idx),
                    sum(ys[i] for i in idx) / len(idx)))
    return out


def is_finite(x) -> bool:
    return x is not None and isinstance(x, float) and math.isfinite(x)
