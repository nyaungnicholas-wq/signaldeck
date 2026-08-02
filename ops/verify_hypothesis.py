"""Gate for a generated hypothesis script.

`python <script>` is not a check. It proves the file PARSES, and best-of-N will
happily spend 30 draws finding the artifact that parses while measuring nothing:
the draw that won before this existed was `{"script": "python.exe ..."}`, a JSON
blob that is also a valid Python dict literal, so it ran, exited 0, and passed.

A check weaker than the requirement selects for whatever satisfies the check.
This one runs the script and requires the numbers the protocol asks for, so a
stub cannot win.

Usage:  python ops/verify_hypothesis.py research/eighty/h0007.py
Exit 0 only if the script ran and emitted a real measurement (or an honest
INSUFFICIENT=1). Non-zero otherwise, with the reason on stdout.
"""
import subprocess
import sys

REQUIRED = ["ISSUED", "OPPORTUNITIES", "PRECISION", "BASE_RATE",
            "DISTINCT_DAYS", "EFFECTIVE_N", "SEALED_PRECISION"]
TIMEOUT_SEC = 900


def parse_kv(out):
    kv = {}
    for line in out.splitlines():
        if "=" in line:
            k, _, v = line.partition("=")
            kv[k.strip()] = v.strip()
    return kv


def main():
    if len(sys.argv) != 2:
        print("usage: verify_hypothesis.py <script.py>")
        return 2
    target = sys.argv[1]
    try:
        p = subprocess.run([sys.executable, target], capture_output=True,
                           text=True, timeout=TIMEOUT_SEC)
    except subprocess.TimeoutExpired:
        print("FAIL: exceeded %ds" % TIMEOUT_SEC)
        return 1

    out = (p.stdout or "") + (p.stderr or "")
    if p.returncode != 0:
        print("FAIL: exit %d\n%s" % (p.returncode, out[-2000:]))
        return 1

    kv = parse_kv(out)

    # An honest refusal is a legitimate result; a silent one is not.
    if kv.get("INSUFFICIENT") == "1":
        print("OK: script declared INSUFFICIENT=1")
        return 0

    missing = [k for k in REQUIRED if k not in kv]
    if missing:
        print("FAIL: ran but printed no measurement; missing %s" % ", ".join(missing))
        return 1

    # Present-but-unparseable is the same failure as absent, and a script that
    # prints PRECISION=nan has not measured anything either.
    for k in REQUIRED:
        try:
            val = float(kv[k])
        except ValueError:
            print("FAIL: %s=%r is not a number" % (k, kv[k]))
            return 1
        if val != val:  # NaN
            print("FAIL: %s is NaN" % k)
            return 1

    if float(kv["ISSUED"]) <= 0:
        print("FAIL: ISSUED=%s -- no calls issued, nothing was measured" % kv["ISSUED"])
        return 1

    print("OK: %s" % " ".join("%s=%s" % (k, kv[k]) for k in REQUIRED))
    return 0


if __name__ == "__main__":
    sys.exit(main())
