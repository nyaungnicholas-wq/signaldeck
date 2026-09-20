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
one there, from the right tool, with the right keys AND THE RIGHT KINDS OF
VALUE?

WHY "PRESENT" IS NOT ENOUGH EITHER (audit F01, 2026-09-20)

The first version of this gate asked only whether a key existed. Three shapes
walked straight through it:

    {"generated": "not-a-date", "rows": [null]}

        PASS, rows=1, required=0, checked=0. `generated` was merely a non-empty
        string, and a null row is not a dict, so `_requires_honesty` said "not
        my problem" and the gate reported a clean grade of nothing.

    ...{"honesty": {"source": "tools/selection_honesty.py", "publishable": null,
                    "one_sided": null, "reason": null, "result": null}}

        PASS, checked=1. Every required key was present. Every one of them was
        null. That is precisely the shape a post-processor leaves when it merged
        a skeleton and died before filling it.

    ...{"breadth": {"mean_daily_agreement": NaN}}

        PASS. json.load accepts the bare NaN and Infinity literals by default,
        and 1e400 parses to inf with no literal at all.

So the checks below are about SHAPE, and only shape. A value must be the kind of
thing the producer emits: a bool where verdict() returns a bool, a string where
it returns a string, a typed record where skillschema.build() returns one, a
real finite number where a rate belongs. Nothing here reads a verdict, compares
a number against a threshold, or edits the artifact -- a REFUSED row (publishable
false, one_sided true, with its reason) is a complete row and PASSES, because
refusing is the disclosure this pipeline exists to publish.

It reports one of three outcomes and lets the caller withhold:

    PASS        every requiring row is complete
    INCOMPLETE  the file parses, but something the checker should have written
                is missing, mis-sourced, mistyped or truncated
    MALFORMED   the file is absent, unreadable, not a JSON object, or carries a
                value JSON cannot honestly represent (NaN/Infinity)

Run standalone:
    .venv/Scripts/python.exe tools/publication_gate.py --registry data/accuracy_registry.json
    .venv/Scripts/python.exe tools/publication_gate.py --selfcheck
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import sys
import tempfile
from pathlib import Path
from typing import Any

HONESTY_SOURCE = "tools/selection_honesty.py"
REQUIRED_HONESTY_KEYS = ("publishable", "one_sided", "reason", "result")

# The typed record skillschema.build() stamps. Duplicated here rather than
# imported so this gate stays standalone -- it is copied into the container by
# ops/test-docker-build.sh and must not acquire a new import to run. _selfcheck
# asserts the two copies still agree whenever skillschema is importable, so the
# duplication cannot drift silently.
RESULT_STATUSES = frozenset({"SUPPORTED", "WITHHELD", "NO_INTERVAL"})
RESULT_REASON_CODES = frozenset({"OK", "NULL_INTERVAL_OVERLAP", "NO_PUBLISHED_INTERVAL"})


class _NonFinite(ValueError):
    """A bare NaN/Infinity literal reached the parser."""


def _reject_constant(token: str) -> Any:
    raise _NonFinite(f"JSON constant {token} is not a number a grade can carry")


def _is_real(x: Any) -> bool:
    """A finite int/float. bool is excluded: it subclasses int, and a JSON
    `true` arriving where a rate belongs means something upstream is wrong."""
    return isinstance(x, (int, float)) and not isinstance(x, bool) and math.isfinite(x)


def _first_non_finite(node: Any, path: str = "$") -> str | None:
    """Locate a float that is inf/nan without having been a NaN literal.

    parse_constant catches the bare tokens; it is never called for `1e400`,
    which the float parser turns into inf on its own. A registry carrying an
    infinite rate is not a grade, whichever way it got there.
    """
    if isinstance(node, float) and not math.isfinite(node):
        return path
    if isinstance(node, dict):
        for k, v in node.items():
            hit = _first_non_finite(v, f"{path}.{k}")
            if hit:
                return hit
    elif isinstance(node, list):
        for i, v in enumerate(node):
            hit = _first_non_finite(v, f"{path}[{i}]")
            if hit:
                return hit
    return None


def _parses_as_timestamp(s: str) -> bool:
    """The grader stamps `generated` with datetime.isoformat(). Accept that, and
    the trailing-Z spelling the API layer uses, and nothing else -- "not-a-date"
    is a non-empty string, which is all the old check asked for."""
    try:
        dt.datetime.fromisoformat(s.strip().replace("Z", "+00:00"))
    except ValueError:
        return False
    return True


def _requires_honesty(row: Any) -> bool:
    """A row needs an honesty block iff selection_honesty could judge it.

    That tool skips any row whose family is not "direction", and returns None
    (merging nothing, printing "no breadth tallies, cannot judge") when the row
    carries no mean_daily_agreement. Demanding a block on those would make this
    gate refuse healthy registries, so the condition mirrors the producer.

    _is_real excludes bool deliberately, and now excludes NaN/inf too: an
    agreement that is not a real number is not a tally the producer could have
    judged, and treating it as one would demand a block the producer never
    wrote. The malformed-value scan above catches it first anyway.
    """
    if not isinstance(row, dict) or row.get("family") != "direction":
        return False
    breadth = row.get("breadth")
    if not isinstance(breadth, dict):
        return False
    return _is_real(breadth.get("mean_daily_agreement"))


def _name(row: dict) -> str:
    return str(row.get("predictor") or "(unnamed row)")


def _honesty_faults(h: dict) -> list[str]:
    """Shape faults in one honesty block. Empty list means well-formed.

    Every entry names a KIND, never a value judgement. A refused row -- False,
    True, a long reason -- has no faults.
    """
    faults: list[str] = []
    if not isinstance(h.get("publishable"), bool):
        faults.append(f"publishable is {type(h.get('publishable')).__name__}, not a boolean")
    if not isinstance(h.get("one_sided"), bool):
        faults.append(f"one_sided is {type(h.get('one_sided')).__name__}, not a boolean")
    if not isinstance(h.get("reason"), str):
        faults.append(f"reason is {type(h.get('reason')).__name__}, not a string")
    elif h.get("publishable") is False and not h["reason"].strip():
        # A refusal is only a disclosure if it says what it refused and why. An
        # empty reason beside publishable=false is a half-written refusal.
        faults.append("publishable is false but reason is empty")

    res = h.get("result")
    if not isinstance(res, dict):
        faults.append(f"result is {type(res).__name__}, not the typed record skillschema builds")
    elif not res:
        faults.append("result is an empty object")
    else:
        if not isinstance(res.get("schema_version"), int) or isinstance(res.get("schema_version"), bool):
            faults.append("result.schema_version is not an integer")
        if res.get("status") not in RESULT_STATUSES:
            faults.append(f"result.status {res.get('status')!r} is not one of "
                          + "/".join(sorted(RESULT_STATUSES)))
        if res.get("reason_code") not in RESULT_REASON_CODES:
            faults.append(f"result.reason_code {res.get('reason_code')!r} is not one of "
                          + "/".join(sorted(RESULT_REASON_CODES)))

    # Merged in the same statement as `result`, so its absence or its wrong type
    # is the same half-finished write.
    resolv = h.get("resolvability")
    if not isinstance(resolv, dict):
        faults.append(f"resolvability is {type(resolv).__name__}, not an object")
    elif not isinstance(resolv.get("supported"), (bool, type(None))):
        faults.append("resolvability.supported is neither a boolean nor null")

    # calls_up is legitimately None when the horizon has no graded calls.
    cu = h.get("calls_up", None)
    if cu is not None and not _is_real(cu):
        faults.append(f"calls_up is {type(cu).__name__}, not a real number or null")
    return faults


def verify(path: str | Path) -> dict[str, Any]:
    """Inspect the registry at `path`. Never raises; a bad file is MALFORMED."""
    bad = {"outcome": "MALFORMED", "rows": 0, "required": 0, "checked": 0}
    try:
        with open(path, "r", encoding="utf-8") as fh:
            data = json.load(fh, parse_constant=_reject_constant)
    except FileNotFoundError:
        return {**bad, "reason": f"no registry at {path}"}
    except _NonFinite as e:
        return {**bad, "reason": f"not valid JSON ({e})"}
    except json.JSONDecodeError as e:
        return {**bad, "reason": f"not valid JSON ({e})"}
    except OSError as e:
        return {**bad, "reason": f"unreadable ({e})"}

    if not isinstance(data, dict):
        return {**bad, "reason": f"top level is {type(data).__name__}, not an object"}

    nf = _first_non_finite(data)
    if nf:
        return {**bad, "reason": f"non-finite number at {nf}: a grade cannot carry inf or nan"}

    def incomplete(reason: str, rows: int = 0, required: int = 0, checked: int = 0) -> dict[str, Any]:
        return {"outcome": "INCOMPLETE", "rows": rows, "required": required,
                "checked": checked, "reason": reason}

    generated = data.get("generated")
    if not isinstance(generated, str) or not generated.strip():
        return incomplete("no `generated` timestamp, so there is nothing to date this grade by")
    if not _parses_as_timestamp(generated):
        return incomplete(f"`generated` is {generated!r}, which is not a timestamp, "
                          "so this grade cannot be dated")

    rows = data.get("rows")
    if not isinstance(rows, list):
        return incomplete("`rows` is missing or is not a list")
    if not rows:
        # A grade that graded nothing must not read as a clean grade.
        return incomplete("no rows: a registry with nothing in it is not a completed grade")

    nonobj = [str(i) for i, r in enumerate(rows) if not isinstance(r, dict)]
    if nonobj:
        # `[null]` used to reach PASS here: a null row is not a dict, so nothing
        # required an honesty block and the gate reported a clean grade of zero
        # checked rows.
        return incomplete("row(s) at index " + ", ".join(nonobj) + " are not objects",
                          rows=len(rows))

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

    mistyped = [(_name(r), _honesty_faults(r["honesty"])) for r in required]
    mistyped = [(n, f) for n, f in mistyped if f]
    if mistyped:
        detail = "; ".join(f"{n}: " + ", ".join(f) for n, f in mistyped)
        return {**base, "outcome": "INCOMPLETE", "checked": len(required) - len(mistyped),
                "reason": "malformed honesty on " + detail}

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

_GOOD_RESULT = {
    "schema_version": 1, "status": "SUPPORTED", "reason_code": "OK", "reason": "",
    "predictor": "directional-ensemble", "metric": "accuracy",
}
_GOOD_RESOLV = {"supported": True, "reason": "", "overlap_lo": 0.51, "overlap_hi": 0.55}
_GOOD_HONESTY = {
    "publishable": True, "one_sided": False, "reason": "",
    "resolvability": _GOOD_RESOLV, "result": _GOOD_RESULT,
    "calls_up": 0.4812797032572157, "source": HONESTY_SOURCE,
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
    # The duplicated vocabularies must not drift from the producer's.
    try:
        import skillschema  # noqa: PLC0415  -- optional, see RESULT_STATUSES
    except ImportError:
        pass
    else:
        assert set(skillschema.REASON_CODES) == set(RESULT_REASON_CODES), (
            "skillschema.REASON_CODES has moved on without this gate")

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
                   "reason": "accuracy is the one-sided base rate",
                   "result": {**_GOOD_RESULT, "status": "WITHHELD",
                              "reason_code": "NULL_INTERVAL_OVERLAP"}}
        r = _verify_obj(tmp, "refused.json", _reg([{**direction, "honesty": refused}]))
        assert r["outcome"] == "PASS", r

        # so must an INSUFFICIENT one: no interval published is a state the
        # producer emits, not a broken write.
        insufficient = {**_GOOD_HONESTY,
                        "resolvability": {"supported": None, "reason": "no published interval"},
                        "result": {**_GOOD_RESULT, "status": "NO_INTERVAL",
                                   "reason_code": "NO_PUBLISHED_INTERVAL"}}
        r = _verify_obj(tmp, "insufficient.json", _reg([{**direction, "honesty": insufficient}]))
        assert r["outcome"] == "PASS", r

        # calls_up is legitimately null when the horizon had no graded calls
        r = _verify_obj(tmp, "nocalls.json",
                        _reg([{**direction, "honesty": {**_GOOD_HONESTY, "calls_up": None}}]))
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

        # ── F01, the audit fixtures ────────────────────────────────────────────
        # (1) a null row inside a non-empty rows list
        r = verify(_write(tmp, "nullrow.json", '{"generated":"not-a-date","rows":[null]}'))
        assert r["outcome"] == "INCOMPLETE", r
        assert "not a timestamp" in r["reason"], r

        r = _verify_obj(tmp, "nullrow2.json", _reg([None]))
        assert r["outcome"] == "INCOMPLETE" and "not objects" in r["reason"], r
        assert r["rows"] == 1, r

        r = _verify_obj(tmp, "listrow.json", _reg([direction, ["x"]]))
        assert r["outcome"] == "INCOMPLETE" and "index 1" in r["reason"], r

        # (2) every required key present, every one of them null
        nulls = {"source": HONESTY_SOURCE, "publishable": None, "one_sided": None,
                 "reason": None, "result": None}
        r = _verify_obj(tmp, "nulls.json", _reg([{**direction, "honesty": nulls}]))
        assert r["outcome"] == "INCOMPLETE", r
        assert "malformed honesty" in r["reason"], r
        for frag in ("publishable is NoneType", "one_sided is NoneType",
                     "reason is NoneType", "result is NoneType"):
            assert frag in r["reason"], (frag, r)
        assert r["checked"] == 0, r

        # (3) NaN, both spellings it can arrive in
        r = verify(_write(tmp, "nan.json",
                          '{"generated":"2026-09-13T12:00:00","rows":'
                          '[{"family":"direction","predictor":"x",'
                          '"breadth":{"mean_daily_agreement":NaN}}]}'))
        assert r["outcome"] == "MALFORMED" and "NaN" in r["reason"], r

        r = verify(_write(tmp, "inf.json",
                          '{"generated":"2026-09-13T12:00:00","rows":'
                          '[{"family":"direction","predictor":"x",'
                          '"breadth":{"mean_daily_agreement":1e400}}]}'))
        assert r["outcome"] == "MALFORMED" and "non-finite" in r["reason"], r

        # ── other half-written shapes ─────────────────────────────────────────
        # a boolean where a verdict belongs
        for key, val in (("publishable", "yes"), ("one_sided", 1), ("reason", 7)):
            r = _verify_obj(tmp, f"type_{key}.json",
                            _reg([{**direction, "honesty": {**_GOOD_HONESTY, key: val}}]))
            assert r["outcome"] == "INCOMPLETE" and key in r["reason"], (key, r)

        # a refusal that does not say what it refused
        r = _verify_obj(tmp, "silentrefusal.json",
                        _reg([{**direction, "honesty": {**_GOOD_HONESTY,
                                                        "publishable": False, "reason": "  "}}]))
        assert r["outcome"] == "INCOMPLETE" and "reason is empty" in r["reason"], r

        # a skeleton result: merged, then never filled
        for res, frag in (({}, "empty object"),
                          ({"schema_version": 1}, "status"),
                          ({"schema_version": "1", "status": "SUPPORTED", "reason_code": "OK"},
                           "schema_version"),
                          ({"schema_version": 1, "status": "FINE", "reason_code": "OK"}, "status"),
                          ({"schema_version": 1, "status": "SUPPORTED", "reason_code": "SURE"},
                           "reason_code")):
            r = _verify_obj(tmp, f"res_{frag.split()[0]}_{len(res)}.json",
                            _reg([{**direction, "honesty": {**_GOOD_HONESTY, "result": res}}]))
            assert r["outcome"] == "INCOMPLETE" and frag in r["reason"], (res, r)

        # resolvability is merged in the same statement as result
        r = _verify_obj(tmp, "noresolv.json",
                        _reg([{**direction,
                               "honesty": {k: v for k, v in _GOOD_HONESTY.items()
                                           if k != "resolvability"}}]))
        assert r["outcome"] == "INCOMPLETE" and "resolvability" in r["reason"], r

        r = _verify_obj(tmp, "badcalls.json",
                        _reg([{**direction, "honesty": {**_GOOD_HONESTY, "calls_up": "0.48"}}]))
        assert r["outcome"] == "INCOMPLETE" and "calls_up" in r["reason"], r

        # ── exemptions: these must NOT be demanded of ─────────────────────────
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

        for stamp in ("not-a-date", "2026-13-45T99:99:99", "", "   "):
            r = _verify_obj(tmp, "stamp.json", _reg([direction], generated=stamp))
            assert r["outcome"] == "INCOMPLETE", (stamp, r)

        # the spellings the pipeline really produces must still be accepted
        for stamp in ("2026-09-13T14:42:10", "2026-09-20T17:45:19.143157Z",
                      "2026-09-13T14:42:10+00:00"):
            r = _verify_obj(tmp, "stampok.json",
                            _reg([{**direction, "honesty": _GOOD_HONESTY}], generated=stamp))
            assert r["outcome"] == "PASS", (stamp, r)

        r = _verify_obj(tmp, "norows.json", _reg([]))
        assert r["outcome"] == "INCOMPLETE" and "no rows" in r["reason"], r

        r = verify(_write(tmp, "rowsobj.json",
                          json.dumps({"generated": "2026-09-13T00:00:00", "rows": {}})))
        assert r["outcome"] == "INCOMPLETE" and "rows" in r["reason"], r

        # every non-PASS outcome must carry a reason a human can act on
        for name, obj in (("m1", _reg([direction])), ("m2", _reg([])),
                          ("m3", _reg([None])), ("m4", _reg([{**direction, "honesty": nulls}]))):
            rr = _verify_obj(tmp, name + ".json", obj)
            assert rr["reason"].strip(), rr


if __name__ == "__main__":
    sys.exit(main())
