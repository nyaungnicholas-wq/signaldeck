# TARGETS: new file -> drafts/research_tests/test_corpus_integrity.py
# APPLY:   .venv/Scripts/python.exe -m pytest drafts/research_tests/ -q  (repo root)
#
# Corpus-level checks over all 258 research/eighty/*.py. These are static
# (AST/regex) rather than behavioural: the scripts each open the live DB at
# module scope, so they cannot be executed under test. A static check that can
# only fail for a real structural reason still beats a mock that cannot fail.

import ast
import re

import pytest

from conftest import RESEARCH

FILES = sorted(RESEARCH.glob("*.py"))


def _src(p):
    return p.read_text(encoding="utf-8", errors="replace")


def test_the_corpus_is_the_expected_size():
    assert len(FILES) == 258, len(FILES)


@pytest.mark.parametrize("path", FILES, ids=lambda p: p.stem)
def test_every_research_file_parses(path):
    """FINDING (4 expected failures): a file that does not parse never ran.

    Whatever verdict the registry holds for these hypotheses was not produced
    by the code now sitting on disk.
    """
    try:
        ast.parse(_src(path), filename=str(path))
    except SyntaxError as e:
        pytest.fail(f"{path.name} does not parse: line {e.lineno}: {e.msg}")


@pytest.mark.parametrize("path", FILES, ids=lambda p: p.stem)
def test_no_research_file_opens_the_db_read_write(path):
    """The research corpus must be read-only against the live DB.

    Every script is expected to use the mode=ro URI. A plain path opens
    read-write and a stray write would corrupt the record the whole platform
    is graded on.
    """
    s = _src(path)
    if "sqlite3.connect" not in s:
        pytest.skip("no DB access")
    assert "mode=ro" in s, f"{path.name} connects to sqlite without mode=ro"


# --------------------------------------------------------------------------
# the base-rate control
# --------------------------------------------------------------------------

_PREC = re.compile(r"^[ \t]*(?:\w+_)?precision\s*=\s*(.+?)(?:\s*#.*)?$", re.M)
_BASE = re.compile(r"^[ \t]*(?:\w+_)?base_rate\s*=\s*(.+?)(?:\s*#.*)?$", re.M)


def _degenerate(path):
    s = _src(path)
    if "BASE_RATE" not in s:
        return None
    precs = {m.group(1).strip() for m in _PREC.finditer(s)}
    bases = {m.group(1).strip() for m in _BASE.finditer(s)}
    shared = precs & bases
    return shared or None


REPORTING = [p for p in FILES if "BASE_RATE" in _src(p)]


@pytest.mark.parametrize("path", REPORTING, ids=lambda p: p.stem)
def test_base_rate_is_not_a_copy_of_precision(path):
    """FINDING (expected failures): BASE_RATE is meant to be the null a
    hypothesis must beat. In these files it is assigned the SAME expression as
    PRECISION, so PRECISION - BASE_RATE is identically zero and the control
    measures nothing. Any downstream gate of the form
    'precision > base_rate + margin' can never fire; one of the form
    'precision >= base_rate' always fires.

    The honest null is the unconditional hit rate over all OPPORTUNITIES in the
    same window, not the hit rate inside the issued subset -- which is the
    definition of precision.
    """
    shared = _degenerate(path)
    assert not shared, (
        f"{path.name}: precision and base_rate share the expression "
        f"{sorted(shared)!r} -- the base-rate control is degenerate")


def test_the_degenerate_base_rate_pattern_is_reported_in_bulk():
    """Aggregate view, so the count lands in one place rather than N failures."""
    bad = {p.stem: sorted(_degenerate(p)) for p in REPORTING if _degenerate(p)}
    assert not bad, (
        f"{len(bad)}/{len(REPORTING)} files that print BASE_RATE compute it "
        f"from the same expression as PRECISION: {sorted(bad)}")


# --------------------------------------------------------------------------
# dead gates
# --------------------------------------------------------------------------

@pytest.mark.parametrize("path", FILES, ids=lambda p: p.stem)
def test_no_function_is_unconditionally_none(path):
    """Catches the h0241 bug class statically: a window guard that can never be
    satisfied makes the whole function dead, and its caller's `if x is None:
    continue` then silently drops every row.

    Flags `if len(<name>) < K: return None` where <name> was built by a
    comprehension over range(1, len(...)) of a K-element slice -- i.e. a
    guard demanding K items from a list that can hold at most K-1.
    """
    try:
        tree = ast.parse(_src(path), filename=str(path))
    except SyntaxError:
        pytest.skip("does not parse (covered by test_every_research_file_parses)")

    for fn in [n for n in ast.walk(tree)
               if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))]:
        # sizes of lists built as [... for i in range(1, len(X))] -> len(X)-1
        derived = {}
        for node in ast.walk(fn):
            if isinstance(node, ast.Assign) and isinstance(node.value, ast.ListComp):
                gen = node.value.generators[0]
                it = gen.iter
                if (isinstance(it, ast.Call) and getattr(it.func, "id", "") == "range"
                        and len(it.args) == 2
                        and isinstance(it.args[0], ast.Constant)
                        and it.args[0].value == 1):
                    tgt = node.targets[0]
                    if isinstance(tgt, ast.Name):
                        derived[tgt.id] = it.args[1]
        for node in ast.walk(fn):
            if not (isinstance(node, ast.If) and isinstance(node.test, ast.Compare)):
                continue
            t = node.test
            if not (isinstance(t.left, ast.Call)
                    and getattr(t.left.func, "id", "") == "len"
                    and isinstance(t.left.args[0], ast.Name)
                    and t.left.args[0].id in derived
                    and isinstance(t.ops[0], ast.Lt)
                    and isinstance(t.comparators[0], ast.Constant)):
                continue
            k = t.comparators[0].value
            src_len = derived[t.left.args[0].id]
            # the source is len(closes) where closes came from a K-wide slice;
            # resolve K when it is a literal range(a, b) width in the same fn
            width = _slice_width(fn, src_len)
            if width is not None and width - 1 < k:
                pytest.fail(
                    f"{path.name}:{fn.lineno} {fn.name}(): guard "
                    f"`len({t.left.args[0].id}) < {k}` can never be satisfied -- "
                    f"the list holds at most {width - 1} items, so the function "
                    f"returns None for every input")


def _slice_width(fn, len_arg):
    """Width of the list len_arg measures, when built from a literal range.

    len_arg arrives as the second argument of `range(1, <expr>)`, which in the
    real cases is `len(closes)` -- unwrap that to the Name `closes` first.
    """
    if (isinstance(len_arg, ast.Call)
            and getattr(len_arg.func, "id", "") == "len"
            and len_arg.args and isinstance(len_arg.args[0], ast.Name)):
        len_arg = len_arg.args[0]
    if not isinstance(len_arg, ast.Name):
        return None
    for node in ast.walk(fn):
        if not (isinstance(node, ast.Assign) and isinstance(node.value, ast.ListComp)):
            continue
        tgt = node.targets[0]
        if not (isinstance(tgt, ast.Name) and tgt.id == len_arg.id):
            continue
        it = node.value.generators[0].iter
        if not (isinstance(it, ast.Call) and getattr(it.func, "id", "") == "range"
                and len(it.args) == 2):
            return None
        lo, hi = it.args
        # range(end_idx - A, end_idx + B) -> width A + B
        a = _offset(lo)
        b = _offset(hi)
        if a is None or b is None:
            return None
        return b - a
    return None


def _offset(node):
    """Constant offset of `X - c` / `X + c` / `X` relative to the same base X."""
    if isinstance(node, ast.Name):
        return 0
    if isinstance(node, ast.BinOp) and isinstance(node.right, ast.Constant):
        if isinstance(node.op, ast.Sub):
            return -node.right.value
        if isinstance(node.op, ast.Add):
            return node.right.value
    return None
