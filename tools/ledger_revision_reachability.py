#!/usr/bin/env python3
"""Warn, daily, before git gc turns a deleted ref into a permanently stripped verdict.

tools/accuracy_registry.py (sha256 pinned in the pre-registration chain; guards
ship beside it, never inside it) strips the verdict from a WHOLE predictor family
when ANY post-epoch row of prediction_ledger (family = horizon: 1d, 1w) or
regime_outcomes (family = kind: trend21, liquidity21-crypto, ...) cites a revision
that `git cat-file -e <rev>^{commit}` cannot resolve. Post-epoch means
predicted_at >= REVISION_EPOCH_TS, or ts >= REVISION_EPOCH_TS, in UTC seconds.
The epoch is fixed at 2026-08-04, so the strip is permanent.

The gap: a commit reachable from NO ref still resolves until `git gc` prunes it,
and reflog entries keep it alive for about 30 days. So a deleted ref strips
verdicts silently, weeks later, at the first gc. ops/githooks/reference-transaction
sees ref transactions only: never gc, reflog expiry, or a deletion made in another
clone. ops/ledger-provenance.sh records only revisions already on the remote, so
an unpushed orphan is invisible to CI as well.

Measured 2026-09-10, after refs keep/pre-rewrite-2026-09-10 and
keep/orphan-613bd2e5-2026-08-03 were deleted on request: b84670c9 resolves, no
ref reaches it, and it carries 252 post-epoch 1w rows plus 7 liquidity21-crypto
and 7 trend21-crypto rows. The first gc after about 2026-10-10 strips those three
families for good. 613bd2e5 has pre-epoch rows only and is exempt.

So the ledger itself is asked, daily (ops/check-grader-health.ps1), while the
commit can still be re-attached (`git branch keep/<name> <sha>`, or a fetch from
the backup bundle) or a revision-epoch correction chained. For every DISTINCT
post-epoch revision this reports: resolvable, reachable from some ref (the same
`rev-list -1 <rev> --not --all` primitive the hook and CI use), row counts per
family, and the newest row date.

Read-only (sqlite URI mode=ro). Writes nothing, suppresses nothing: it is the
early warning for what the pinned grader will do.

Exit codes: 0 = every post-epoch revision resolves and some ref reaches it;
1 = at least one is resolvable-but-unreachable or unresolvable (offenders
printed); 2 = the check could not run (database missing or unreadable).

Run: python tools/ledger_revision_reachability.py [--db PATH] [--repo PATH] [-v]
     python tools/ledger_revision_reachability.py __selfcheck   (prints SELFCHECK OK)
"""

from __future__ import annotations

import argparse
import datetime as dt
import os
import sqlite3
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from accuracy_registry import DEFAULT_DB, REPO_ROOT, REVISION_EPOCH, REVISION_EPOCH_TS, revision_resolvable  # noqa: E402


def git(repo: str, *args: str, env: dict | None = None) -> subprocess.CompletedProcess:
    try:
        return subprocess.run(
            ["git", "-C", repo, *args],
            capture_output=True,
            text=True,
            env=env,
        )
    except OSError:
        return subprocess.CompletedProcess(["git", *args], 127, "", "")


def resolvable(rev: str, repo: str) -> bool:
    if not rev or rev.endswith("+dirty"):
        return False
    return git(repo, "cat-file", "-e", rev + "^{commit}").returncode == 0


def reachable(rev: str, repo: str) -> bool:
    p = git(repo, "rev-list", "-1", rev, "--not", "--all")
    return p.returncode == 0 and p.stdout.strip() == ""


def classify(rev: str, repo: str) -> str:
    if not resolvable(rev, repo):
        return "UNRESOLVABLE"
    if not reachable(rev, repo):
        return "UNREACHABLE"
    return "ok"


def fmt_date(ts: int) -> str:
    return dt.datetime.fromtimestamp(ts, dt.timezone.utc).date().isoformat()


def scan(db_path: str, epoch_ts: int) -> dict[str, dict]:
    uri = "file:" + os.path.abspath(db_path).replace(os.sep, "/") + "?mode=ro"
    con = sqlite3.connect(uri, uri=True)
    try:
        cur = con.cursor()
        out: dict[str, dict] = {}
        for table, family_col, ts_col in (
            ("prediction_ledger", "horizon", "predicted_at"),
            ("regime_outcomes", "kind", "ts"),
        ):
            cur.execute(
                f"""
                SELECT revision, {family_col}, COUNT(*), MAX({ts_col})
                FROM {table}
                WHERE {ts_col} >= ?
                GROUP BY 1, 2
                """,
                (epoch_ts,),
            )
            for rev, family, count, newest in cur.fetchall():
                key = rev if rev is not None else ""
                if key not in out:
                    out[key] = {"families": {}, "newest": 0}
                label = family or "(none)"
                out[key]["families"][label] = out[key]["families"].get(label, 0) + count
                if newest > out[key]["newest"]:
                    out[key]["newest"] = newest
        return out
    finally:
        con.close()


def run(
    db_path: str, repo: str, verbose: bool = False, out=print
) -> int:
    try:
        revs = scan(db_path, REVISION_EPOCH_TS)
    except sqlite3.Error as e:
        out(f"LEDGER REVISIONS ERROR: cannot read {db_path}: {e}")
        return 2

    offenders = []
    for key in sorted(revs):
        status = classify(key, repo)
        line = (
            f"{key or '(unstamped)'}  {status}  newest={fmt_date(revs[key]['newest'])}  "
            + " ".join(f"{fam}={n}" for fam, n in sorted(revs[key]["families"].items()))
        )
        if verbose:
            out(line)
        if status != "ok":
            offenders.append(line)

    n = len(revs)
    if not offenders:
        out(f"LEDGER REVISIONS OK n={n}")
        return 0

    out(
        f"LEDGER REVISIONS BROKEN: {len(offenders)} of {n} post-epoch revisions cannot be attributed (epoch {REVISION_EPOCH.isoformat()})"
    )
    for line in offenders:
        out("  " + line)
    out(
        "  UNREACHABLE = resolves today but no ref reaches it: the next git gc prunes it and the grader strips those families permanently."
    )
    out(
        "  UNRESOLVABLE = git cannot resolve it (or the stamp is +dirty or missing): the grader strips those families NOW."
    )
    return 1


def selfcheck() -> int:
    with tempfile.TemporaryDirectory() as tmp:
        repo = os.path.join(tmp, "repo")
        os.makedirs(repo)
        env = dict(
            os.environ,
            GIT_AUTHOR_NAME="t",
            GIT_AUTHOR_EMAIL="t@example.invalid",
            GIT_COMMITTER_NAME="t",
            GIT_COMMITTER_EMAIL="t@example.invalid",
        )

        def g(*args: str) -> str:
            p = git(repo, "-c", "commit.gpgsign=false", *args, env=env)
            assert p.returncode == 0, f"git {' '.join(args)} failed: {p.stderr}"
            return p.stdout.strip()

        g("init", "-q")
        g("commit", "--allow-empty", "-q", "-m", "a")
        KEPT = g("rev-parse", "HEAD")
        g("checkout", "-q", "-b", "doomed")
        g("commit", "--allow-empty", "-q", "-m", "b")
        ORPHAN = g("rev-parse", "HEAD")
        g("checkout", "-q", "-")
        g("branch", "-q", "-D", "doomed")
        FAKE = "0" * 40
        FAKE_PRE = "f" * 40

        assert resolvable(KEPT, repo) and reachable(KEPT, repo)
        assert resolvable(ORPHAN, repo) and not reachable(ORPHAN, repo)
        assert not resolvable(FAKE, repo)
        assert not resolvable("", repo)
        assert not resolvable(KEPT + "+dirty", repo)
        assert classify(KEPT, repo) == "ok"
        assert classify(ORPHAN, repo) == "UNREACHABLE"
        assert classify(FAKE, repo) == "UNRESOLVABLE"

        head = git(REPO_ROOT, "rev-parse", "HEAD").stdout.strip()
        assert head
        for x in (head, FAKE, "", head + "+dirty"):
            assert resolvable(x, REPO_ROOT) == revision_resolvable(x), x
        assert resolvable(head, REPO_ROOT) is True

        db = os.path.join(tmp, "t.db")
        con = sqlite3.connect(db)
        try:
            cur = con.cursor()
            cur.execute(
                "CREATE TABLE prediction_ledger (seq INTEGER PRIMARY KEY, predicted_at INTEGER, horizon TEXT, revision TEXT)"
            )
            cur.execute(
                "CREATE TABLE regime_outcomes (id INTEGER PRIMARY KEY, ts INTEGER, kind TEXT, revision TEXT)"
            )
            E = REVISION_EPOCH_TS
            cur.executemany(
                "INSERT INTO prediction_ledger (predicted_at, horizon, revision) VALUES (?, ?, ?)",
                [
                    (E + 100, "1d", KEPT),
                    (E + 100, "1d", KEPT),
                    (E + 200, "1w", KEPT),
                    (E + 86400 * 3, "1w", ORPHAN),
                    (E + 86400 * 3, "1w", ORPHAN),
                    (E + 86400 * 3, "1w", ORPHAN),
                    (E - 1, "1w", FAKE_PRE),
                    (E - 1, "1w", FAKE_PRE),
                    (E - 1, "1w", FAKE_PRE),
                    (E - 1, "1w", FAKE_PRE),
                    (E - 1, "1w", FAKE_PRE),
                    (E + 50, "1d", None),
                    (E + 60, "1d", KEPT + "+dirty"),
                ],
            )
            cur.executemany(
                "INSERT INTO regime_outcomes (ts, kind, revision) VALUES (?, ?, ?)",
                [
                    (E + 300, "trend21", KEPT),
                    (E + 400, "vol21", FAKE),
                    (E + 400, "vol21", FAKE),
                    (E - 5, "trend21", ORPHAN),
                    (E + 86400 * 3 + 7, "trend21-crypto", ORPHAN),
                ],
            )
            con.commit()
        finally:
            con.close()

        revs = scan(db, E)
        assert set(revs) == {KEPT, ORPHAN, FAKE, "", KEPT + "+dirty"}
        assert revs[ORPHAN]["families"] == {"1w": 3, "trend21-crypto": 1}
        assert revs[ORPHAN]["newest"] == E + 86400 * 3 + 7
        assert revs[KEPT]["families"] == {"1d": 2, "1w": 1, "trend21": 1}
        assert revs[FAKE]["families"] == {"vol21": 2}
        assert revs[""]["families"] == {"1d": 1}
        assert FAKE_PRE not in revs

        lines = []
        rc = run(db, repo, out=lines.append)
        text = "\n".join(lines)
        assert rc == 1
        assert ORPHAN + "  UNREACHABLE" in text
        assert FAKE + "  UNRESOLVABLE" in text
        assert "(unstamped)  UNRESOLVABLE" in text
        assert KEPT + "+dirty  UNRESOLVABLE" in text
        assert "1w=3 trend21-crypto=1" in text
        assert f"newest={fmt_date(E + 86400 * 3 + 7)}" in text
        assert FAKE_PRE not in text
        assert KEPT + "  ok" not in text
        assert lines[0].startswith("LEDGER REVISIONS BROKEN: 4 of 5 ")

        lines2 = []
        assert run(db, repo, verbose=True, out=lines2.append) == 1
        assert KEPT + "  ok" in "\n".join(lines2)

        con = sqlite3.connect(db)
        try:
            cur = con.cursor()
            for table in ("prediction_ledger", "regime_outcomes"):
                cur.execute(
                    f"DELETE FROM {table} WHERE revision IS NULL OR revision != ?",
                    (KEPT,),
                )
            con.commit()
        finally:
            con.close()

        lines3 = []
        assert run(db, repo, out=lines3.append) == 0
        assert lines3 == ["LEDGER REVISIONS OK n=1"], lines3

        missing = os.path.join(tmp, "nope.db")
        lines4 = []
        assert run(missing, repo, out=lines4.append) == 2
        assert not os.path.exists(missing)
        assert lines4 and lines4[0].startswith("LEDGER REVISIONS ERROR")

    print("SELFCHECK OK")
    return 0


def main(argv: list[str] | None = None) -> int:
    if argv is None:
        argv = sys.argv[1:]
    if argv[:1] == ["__selfcheck"]:
        return selfcheck()
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--db", default=DEFAULT_DB, help="Path to SQLite database")
    parser.add_argument(
        "--repo", default=REPO_ROOT, help="Path to git repository (default: %(default)s)"
    )
    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="print every post-epoch revision, not only the offenders",
    )
    args = parser.parse_args(argv)
    return run(args.db, args.repo, args.verbose)


if __name__ == "__main__":
    sys.exit(main())