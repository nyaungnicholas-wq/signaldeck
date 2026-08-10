#!/usr/bin/env python3
"""SWARM 3 PART A — reproducible proof that the two design-effect estimators agree.

tools/accuracy_registry.py:design_effect claims in its docstring to be "the same
estimator as clusterstat.DesignEffect in the Go daemon, deliberately: a registry
verdict and a canary decision must never disagree about the same numbers."
Nothing in the tree checks that claim. This script does.

It imports the REAL registry function and builds a scratch Go module from a
byte-identical COPY of daemon/internal/clusterstat/clusterstat.go, then pushes
identical day tallies through both and compares. Nothing under daemon/ is built
or modified; the scratch module lives in the system temp dir.

RUN (from the repo root):
    .venv/Scripts/python.exe round2-drafts/swarm3_partA_parity_check.py

Exit 0 = parity holds. Exit 1 = a real divergence, with the counterexample printed.
Read-only: touches no database, no daemon, no git state.
"""
from __future__ import annotations

import json
import random
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
GO_SRC = REPO / "daemon" / "internal" / "clusterstat" / "clusterstat.go"

sys.path.insert(0, str(REPO / "tools"))
from accuracy_registry import design_effect  # noqa: E402  the real grader function

MAIN_GO = '''package main

import (
	"encoding/json"
	"fmt"
	"os"

	"parity/clusterstat"
)

func main() {
	dec := json.NewDecoder(os.Stdin)
	for {
		var raw [][2]int
		if err := dec.Decode(&raw); err != nil {
			return
		}
		days := make([]clusterstat.Day, len(raw))
		for i, r := range raw {
			days[i] = clusterstat.Day{Day: int64(i), N: r[0], Hits: r[1]}
		}
		d, ok := clusterstat.DesignEffect(days)
		if !ok {
			fmt.Println("NONE")
			continue
		}
		fmt.Printf("%.17g\\n", d)
	}
}
'''


def build_go(workdir: Path) -> Path:
    """Scratch module around an UNMODIFIED copy of clusterstat.go."""
    (workdir / "clusterstat").mkdir(parents=True)
    shutil.copy2(GO_SRC, workdir / "clusterstat" / "clusterstat.go")
    (workdir / "go.mod").write_text("module parity\n\ngo 1.21\n")
    (workdir / "main.go").write_text(MAIN_GO)
    exe = workdir / ("parity.exe" if sys.platform == "win32" else "parity")
    subprocess.run(["go", "build", "-o", str(exe), "."], cwd=workdir, check=True)
    return exe


def cases() -> list[tuple[str, list[tuple[int, int]]]]:
    out: list[tuple[str, list[tuple[int, int]]]] = [
        # The two guards PART A exists to check, and the shapes that reach them.
        ("degenerate p=1, 10 equal days of 100", [(100, 100)] * 10),
        ("degenerate p=0, 10 equal days of 100", [(100, 0)] * 10),
        ("degenerate p=1, wildly unequal days", [(7, 7), (1046, 1046), (3, 3), (500, 500)]),
        ("maximal clustering, non-degenerate", [(100, 100)] * 5 + [(100, 0)] * 5),
        ("zero clustering (every day at pooled p)", [(100, 50)] * 10),
        ("anti-clustered -> the 1.0 floor", [(2, 1)] * 6),
        ("k=2 minimum", [(10, 3), (10, 8)]),
        ("k=1 -> unmeasurable", [(10, 3)]),
        ("k=0 -> unmeasurable", []),
        ("n=0 -> unmeasurable", [(0, 0), (0, 0)]),
        ("empty day mixed in", [(0, 0), (100, 40), (100, 60), (50, 25)]),
        ("one hit in a huge sample", [(1000, 1), (1000, 0), (1000, 0), (1000, 0)]),
        ("one miss in a huge sample", [(1000, 999), (1000, 1000), (1000, 1000)]),
        ("live-shaped: 19 unequal days",
         [(7, 3), (1046, 500), (900, 401), (12, 9), (830, 402), (1001, 470), (3, 0),
          (777, 390), (640, 300), (1046, 601), (9, 8), (512, 240), (333, 180),
          (1044, 490), (88, 40), (960, 470), (5, 5), (720, 350), (410, 190)]),
    ]
    rng = random.Random(20260806)
    for i in range(400):
        days = []
        for _ in range(rng.randint(2, 30)):
            n = rng.choice([0, 1, 2, 3, 7, 50, 300, 1046])
            days.append((n, rng.randint(0, n) if n else 0))
        out.append((f"fuzz#{i}", days))
    # biased toward degenerate / near-degenerate p, where the guards decide
    for i in range(200):
        mode = rng.choice(["all1", "all0", "near1", "near0"])
        days = []
        for _ in range(rng.randint(2, 20)):
            n = rng.choice([1, 5, 100, 1046])
            h = (n if mode == "all1" else 0 if mode == "all0"
                 else max(0, n - rng.choice([0, 0, 0, 1])) if mode == "near1"
                 else min(n, rng.choice([0, 0, 0, 1])))
            days.append((n, h))
        out.append((f"degfuzz#{i}", days))
    return out


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="sd-parity-") as tmp:
        exe = build_go(Path(tmp))
        cs = cases()
        payload = "".join(json.dumps([[n, h] for n, h in d]) + "\n" for _, d in cs)
        go_out = subprocess.run([str(exe)], input=payload, capture_output=True,
                                text=True, check=True).stdout.strip().split("\n")
    assert len(go_out) == len(cs), (len(go_out), len(cs))

    bad = []
    for (tag, days), g in zip(cs, go_out):
        py = design_effect(days)
        if g == "NONE":
            ok = py is None
        else:
            ok = py is not None and abs(py - float(g)) <= 1e-12 * max(1.0, abs(float(g)))
        if not ok:
            bad.append((tag, days[:8], py, g))

    print(f"clusterstat.DesignEffect (Go) vs accuracy_registry.design_effect (Python)")
    print(f"cases={len(cs)}  mismatches={len(bad)}")
    for tag, days, py, g in bad[:20]:
        print(f"  MISMATCH {tag}: days={days} python={py!r} go={g}")
    if bad:
        return 1
    print("PARITY HOLDS - the docstring claim is true.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
