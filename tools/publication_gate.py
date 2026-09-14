#!/usr/bin/env python3
"""Completeness assertion for a graded accuracy registry, run just before it is
published.

WHY AN EXIT CODE IS NOT ENOUGH

tools/selection_honesty.py post-processes the registry the pinned grader wrote,
merging an `honesty` block into each directional row. Its main() ends with

    return 1 if refused else 0

so exit 1 means "some rows were refused", which is a disclosure to publish, not
an outage. But an unhandled exception ALSO exits 1. A traceback on row three and
a clean run that refused row three were therefore the same byte to the callers,
and both ops/accuracy-registry.sh and ops/grade.sh discarded it (`|| true`, and
a log line reading "row-level refusals are expected"). A half-merged registry
published exactly like a complete one.

The question "did the checker actually finish?" cannot be answered by the
checker's own exit status. It can be answered by looking at what it produced,
which is all this module does: for every row that REQUIRES an honesty block, is
one there, from the right tool, with the right keys? A crash cannot answer that
yes.

This decides nothing scientific. It never reads a verdict, never compares a
number against a threshold, and never edits the artifact. It reports one of
three outcomes and lets the caller withhold:

    PASS        every requiring row is complete
    INCOMPLETE  the file parses, but something the checker should have written
                is missing, mis-sourced or truncated
    MALFORMED   the file is absent, unreadable, or not a JSON object

Run standalone:
    .venv/Scripts/python.exe tools/publication_gate.py --registry data/accuracy_registry.json
    .venv/Scripts/python.exe tools/publication_gate.py --selfcheck
"""

from __future__ import annotations

import argparse
import json
import sys
import tempfile
from pathlib import Path
from typing import Any

HONESTY_SOURCE = "tools/selection_honesty.py"
REQUIRED_HONESTY_KEYS = ("publishable", "one_sided", "reason", "result")


def _requires_honesty(row: Any) -> bool:
    """A row needs an honesty block iff selection_honesty could judge it.

    That tool skips any row whose family is not "direction", and returns None
    (merging nothing, printing "no breadth tallies, cannot judge") when the row
    carries no mean_daily_agreement. Demanding a block on those would make this
    gate refuse healthy registries, so the condition mirrors the producer.

    isinstance(x, bool) is excluded deliberately: bool subclasses int in Python,
    and a JSON `true` reaching this field means something upstream is wrong, not
    that the row has a 1.0 agreement.
    """
    if not isinstance(row, dict) or row.get("family") != "direction":
        return False
    breadth = row.get("breadth")
    if not isinstance(breadth, dict):
        return False
    agreement = breadth.get("mean_daily_agreement")
    return isinstance(agreement, (int, float)) and not isinstance(agreement, bool)


def _name(row: dict) -> str:
    return str(row.get("predictor") or "(unnamed row)")


def verify(path: str | Path) -> dict[str, Any]:
    """Inspect the registry at `path`. Never raises; a bad file is MALFORMED."""
    bad = {"outcome": "MALFORMED", "rows": 0, "required": 0, "checked": 0}
    try:
        with open(path, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except FileNotFoundError:
        return {**bad, "reason": f"no registry at {path}"}
    except json.JSONDecodeError as e:
        return {**bad, "reason": f"not valid JSON ({e})"}
    except OSError as e:
        return {**bad, "reason": f"unreadable ({e})"}

    if not isinstance(data, dict):
        return {**bad, "reason": f"top level is {type(data).__name__}, not an object"}

    generated = data.get("generated")
    if not isinstance(generated, str) or not generated.strip():
        return {"outcome": "INCOMPLETE", "rows": 0, "required": 0, "checked": 0,
                "reason": "no `generated` timestamp, so there is nothing to date this grade by"}

    rows = data.get("rows")
    if not isinstance(rows, list):
        return {"outcome": "INCOMPLETE", "rows": 0, "required": 0, "checked": 0,
                "reason": "`rows` is missing or is not a list"}
    if not rows:
        # A grade that graded nothing must not read as a clean grade.
        return {"outcome": "INCOMPLETE", "rows": 0, "required": 0, "checked": 0,
                "reason": "no rows: a registry with nothing in it is not a completed grade"}

    required = [r for r in rows if _requires_honesty(r)]
    base = {"rows": len(rows), "required": len(required)}

    missing = [_name(r) for r in required if not isinstance(r.get("honesty"), dict)]
    if missing:
        return {**base, "outcome": "INCOMPLETE", "checked": len(required) - len(missing),
                "reason": "missing honesty block on " + ", ".join(missing)
                          + " -- selection_honesty did not finish"}

    wrong = [_name(r) for r in required
             if r["honesty"].get("source") != HONESTY_SOURCE]
    if wrong:
        return {**base, "outcome": "INCOMPLETE", "checked": len(required) - len(wrong),
                "reason": "wrong source on " + ", ".join(wrong)
                          + f" -- expected {HONESTY_SOURCE}"}

    partial = [_name(r) for r in required
               if any(k not in r["honesty"] for k in REQUIRED_HONESTY_KEYS)]
    if partial:
        return {**base, "outcome": "INCOMPLETE", "checked": len(required) - len(partial),
                "reason": "incomplete honesty on " + ", ".join(partial)
                          + " -- expected keys " + ", ".join(REQUIRED_HONESTY_KEYS)}

    return {**base, "outcome": "PASS", "checked": len(required), "reason": ""}


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--registry", help="path to the staged accuracy registry JSON")
    ap.add_argument("--selfcheck", action="store_true",
                    help="run built-in assertions and exit")
    ap.add_argument("--json", action="store_true",
                    help="also print the result as one line of JSON")
    args = ap.parse_args(argv)

    if args.selfcheck:
        _selfcheck()
        print("PUBGATE SELFCHECK OK")
        return 0

    if not args.registry:
        ap.error("--registry is required unless --selfcheck is given")

    res = verify(args.registry)
    # ALWAYS prints, pass or fail. A check that is silent on success is
    # indistinguishable from one that never ran.
    line = ("PUBGATE {outcome} rows={rows} required={required} "
            "checked={checked} {reason}").format(**res).rstrip()
    print(line)
    if args.json:
        print(json.dumps(res, sort_keys=True))
    return 0 if res["outcome"] == "PASS" else 1


# ──────────────────────────────────────────────────────────────────────────────
# Self-check
# ──────────────────────────────────────────────────────────────────────────────

_GOOD_HONESTY = {
    "publishable": True, "one_sided": False, "reason": "",
    "result": {"schema": "skill/1"}, "source": HONESTY_SOURCE,
}


def _reg(rows, generated="2026-09-13T12:00:00"):
    return {"generated": generated, "rows": rows}


def _write(tmp: Path, name: str, text: str) -> str:
    p = tmp / name
    p.write_text(text, encoding="utf-8")
    return str(p)


def _verify_obj(tmp: Path, name: str, obj) -> dict:
    return verify(_write(tmp, name, json.dumps(obj)))


def _selfcheck() -> None:
    with tempfile.TemporaryDirectory() as td:
        tmp = Path(td)
        direction = {"predictor": "directional-ensemble (1d)", "family": "direction",
                     "breadth": {"mean_daily_agreement": 0.9}}

        # the happy path
        r = _verify_obj(tmp, "ok.json", _reg([{**direction, "honesty": _GOOD_HONESTY}]))
        assert r["outcome"] == "PASS", r
        assert r["required"] == 1 and r["checked"] == 1, r
        assert r["reason"] == "", r

        # a refused ROW is still complete: publishable False must PASS this gate,
        # because refusing a row is the disclosure, not a failure to produce one.
        refused = {**_GOOD_HONESTY, "publishable": False, "one_sided": True,
                   "reason": "accuracy is the one-sided base rate"}
        r = _verify_obj(tmp, "refused.json", _reg([{**direction, "honesty": refused}]))
        assert r["outcome"] == "PASS", r

        # the crash signature: nothing merged
        r = _verify_obj(tmp, "missing.json", _reg([direction]))
        assert r["outcome"] == "INCOMPLETE", r
        assert "missing honesty" in r["reason"], r
        assert "directional-ensemble (1d)" in r["reason"], r

        r = _verify_obj(tmp, "src.json",
                        _reg([{**direction, "honesty": {**_GOOD_HONESTY, "source": "elsewhere"}}]))
        assert r["outcome"] == "INCOMPLETE" and "wrong source" in r["reason"], r

        half = {k: v for k, v in _GOOD_HONESTY.items() if k != "result"}
        r = _verify_obj(tmp, "half.json", _reg([{**direction, "honesty": half}]))
        assert r["outcome"] == "INCOMPLETE" and "incomplete honesty" in r["reason"], r

        # exemptions: these must NOT be demanded of
        r = _verify_obj(tmp, "noagree.json",
                        _reg([{**direction, "breadth": {"mean_daily_agreement": None}}]))
        assert r["outcome"] == "PASS" and r["required"] == 0, r

        r = _verify_obj(tmp, "bench.json",
                        _reg([{"predictor": "prequential-majority (1d)", "family": "benchmark",
                               "breadth": {"mean_daily_agreement": 1.0}}]))
        assert r["outcome"] == "PASS" and r["required"] == 0, r

        # bool is not a number here, even though bool subclasses int
        r = _verify_obj(tmp, "boolagree.json",
                        _reg([{**direction, "breadth": {"mean_daily_agreement": True}}]))
        assert r["outcome"] == "PASS" and r["required"] == 0, r

        r = _verify_obj(tmp, "nobreadth.json",
                        _reg([{"predictor": "d", "family": "direction", "breadth": None}]))
        assert r["outcome"] == "PASS" and r["required"] == 0, r

        # malformed
        assert verify(str(tmp / "does-not-exist.json"))["outcome"] == "MALFORMED"
        assert verify(_write(tmp, "bad.json", "not json{"))["outcome"] == "MALFORMED"
        assert verify(_write(tmp, "list.json", "[]"))["outcome"] == "MALFORMED"

        # incomplete envelopes
        r = verify(_write(tmp, "nogen.json", json.dumps({"rows": [direction]})))
        assert r["outcome"] == "INCOMPLETE" and "generated" in r["reason"], r

        r = _verify_obj(tmp, "norows.json", _reg([]))
        assert r["outcome"] == "INCOMPLETE" and "no rows" in r["reason"], r

        r = verify(_write(tmp, "rowsobj.json",
                          json.dumps({"generated": "2026-09-13T00:00:00", "rows": {}})))
        assert r["outcome"] == "INCOMPLETE" and "rows" in r["reason"], r

        # every non-PASS outcome must carry a reason a human can act on
        for name, obj in (("m1", _reg([direction])), ("m2", _reg([]))):
            rr = _verify_obj(tmp, name + ".json", obj)
            assert rr["reason"].strip(), rr


if __name__ == "__main__":
    sys.exit(main())
