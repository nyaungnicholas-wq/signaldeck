"""Regression gate for the SPYX protocol. Run this before trusting any result from it.

Covers the five invariants the protocol depends on:
  leakage            - signals are lagged; a later price cannot move an earlier weight
  timestamp align    - weights, returns, rf and benchmark share one index
  costs              - turnover and financing both bite, monotonically
  returns            - the accounting identity reconstructs net returns exactly
  benchmark matching - the benchmark is measured on precisely the strategy's dates

Exits non-zero if any invariant breaks. Never opens the seal.
"""
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
PY = sys.executable

# Each entry: (label, script, sentinel that must appear in stdout)
SUITE = [
    ("evalcore accounting + seal guard", "evalcore.py", "SELFCHECK OK"),
    ("strategies lag + exposure", "strategies.py", "SELFCHECK OK"),
    ("finalist adversarial audit", "audit_finalist.py", "AUDIT CLEAN"),
]


def main() -> int:
    env = dict(os.environ)
    env.pop("SPYX_SEAL", None)  # the suite must pass with the seal CLOSED

    failures = []
    for label, script, sentinel in SUITE:
        path = os.path.join(HERE, script)
        if not os.path.exists(path):
            print(f"[SKIP] {label}: {script} not present")
            continue
        proc = subprocess.run([PY, path], cwd=HERE, capture_output=True, text=True, env=env)
        ok = proc.returncode == 0 and sentinel in proc.stdout
        print(f"[{'PASS' if ok else 'FAIL'}] {label} ({script})")
        if not ok:
            failures.append(label)
            tail = (proc.stdout or "")[-1500:] + (proc.stderr or "")[-1500:]
            print(f"    expected {sentinel!r}, rc={proc.returncode}")
            for line in tail.strip().splitlines()[-15:]:
                print(f"    | {line}")

    # The seal guard is the one invariant worth asserting here directly, because every
    # other check in this file would still pass if it silently stopped working.
    sys.path.insert(0, HERE)
    import evalcore  # noqa: E402

    assert not evalcore.seal_is_open(), "seal must be closed while the suite runs"
    try:
        evalcore.load("SPY", evalcore.DEV_START, evalcore.SEAL_END)
    except PermissionError:
        print("[PASS] seal guard refuses reads past 2021-01-01")
    else:
        print("[FAIL] seal guard did NOT refuse a read past 2021-01-01")
        failures.append("seal guard")

    print(f"\nSPYX SUITE passed={len(SUITE) + 1 - len(failures)} failed={len(failures)}")
    if failures:
        print("FAILED: " + ", ".join(failures))
        return 1
    print("SPYX SUITE OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
