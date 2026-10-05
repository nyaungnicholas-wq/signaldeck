#!/usr/bin/env python3
"""Check SignalDeck's public commitment log yourself. Standard library only.

    python verify.py REPO [--site URL] [--statements N] [--openssl PATH]
    python verify.py --selftest

REPO is a clone of github.com/nyaungnicholas-wq/signaldeck-anchors. Every line
printed starts PASS, FAIL, WARN, SKIP or INFO; the last line is the verdict:
PASS (exit 0) when every check ran and none FAILed, FAIL (exit 1), or
INCOMPLETE (exit 3) when none FAILed but a check was SKIPped -- without --site
the forecasts are never recomputed, and that is not a PASS. See VERIFY.md for
what each check proves and, as importantly, what it does not.
"""
import argparse
import base64
import bisect
import datetime as dt
import decimal
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.request

MAGIC = b"\x00OpenTimestamps\x00\x00Proof\x00\xbf\x89\xe2\xe8\x84\xe8\x92\x94"
NAME_RE = re.compile(r"^(\d{8}T\d{6}Z)\.txt$")
HEX = r"[0-9a-f]{64}"
LINE_RES = [
    re.compile(r"^signaldeck-statement v1$"),
    re.compile(r"^utc (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)$"),
    re.compile(r"^ledger-head seq=(\d+) head=(%s) intact=(true|false|unchecked)$" % HEX),
    re.compile(r"^anchor (none|SIGNALDECK-LEDGER-ANCHOR v1 .+)$"),
    re.compile(r"^prereg (none|seq=\d+ head=%s)$" % HEX),
    re.compile(r"^registry-sha256 (%s)$" % HEX),
    re.compile(r"^prereg-doc-sha256 (none|%s)$" % HEX),
]
TSAS = {  # TSA -> openssl ts -verify trust arguments, paths relative to REPO
    "freetsa": ["-CAfile", "tsa/freetsa-cacert.pem", "-untrusted", "tsa/freetsa-tsa.crt"],
    "digicert": ["-CAfile", "tsa/digicert-trusted-root-g4.pem"],
}
# SHA-256 of each trust root's DER encoding. The repo being verified supplies
# tsa/, so without a pin a rewritten repo could carry its own "FreeTSA" CA and
# backdated tokens. Compare these with certificates you fetch yourself from
# https://freetsa.org/files/ and DigiCert's published Trusted Root G4.
PINS = {
    "tsa/freetsa-cacert.pem": "a6379e7cecc05faa3cbf076013d745e327bbbaa38c0b9af22469d4701d18aabc",
    "tsa/freetsa-tsa.crt": "32e841a95cc1164101ffde41298ef2fc75c1c4372ef095e88a6bbd47dfb191fc",
    "tsa/digicert-trusted-root-g4.pem": "552f7bdcf1a7af9e6ce672017f4f12abf77240c78e761ac203d1d9d20ac89988",
}
HORIZON = {"1d": 86400, "1w": 604800}
MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
UTC = dt.timezone.utc


class Report:
    def __init__(self, quiet=False):
        self.n = dict.fromkeys(["PASS", "FAIL", "WARN", "SKIP", "INFO"], 0)
        self.quiet = quiet

    def __call__(self, kind, msg):
        self.n[kind] += 1
        if not self.quiet:
            print("%s %s" % (kind, msg))


def run(cmd, cwd=None):
    try:
        p = subprocess.run(cmd, cwd=cwd, capture_output=True)
    except OSError as e:
        return 127, str(e)
    return p.returncode, (p.stdout + p.stderr).decode("utf-8", "replace")


def git_bytes(repo, rev, path):
    """A file's bytes at a revision; b'' where it does not exist."""
    p = subprocess.run(["git", "-C", repo, "show", "%s:%s" % (rev, path)], capture_output=True)
    return p.stdout if p.returncode == 0 else b""


def go_g(v):
    """Go strconv.FormatFloat(v, 'g', -1, 64): shortest round-trip digits,
    exponent form iff the leading digit's decimal exponent is < -4 or >= 6."""
    v = float(v)
    if v != v:
        return "NaN"
    if v in (float("inf"), float("-inf")):
        return "+Inf" if v > 0 else "-Inf"
    neg = "-" if str(v).startswith("-") else ""
    if v == 0:
        return neg + "0"
    _, digits, exp = decimal.Decimal(repr(abs(v))).as_tuple()
    digits = list(digits)
    while len(digits) > 1 and digits[-1] == 0:
        digits.pop()
        exp += 1
    n, ds = len(digits), "".join(map(str, digits))
    e10 = exp + n - 1
    if e10 < -4 or e10 >= 6:
        mant = ds[0] + ("." + ds[1:] if n > 1 else "")
        return "%s%se%s%02d" % (neg, mant, "-" if e10 < 0 else "+", abs(e10))
    if e10 < 0:
        return neg + "0." + "0" * (-e10 - 1) + ds
    if n <= e10 + 1:
        return neg + ds + "0" * (e10 + 1 - n)
    return neg + ds[: e10 + 1] + "." + ds[e10 + 1:]


def entry_hash(prev, e):
    payload = "predicted_at=%d|symbol_id=%d|horizon=%s|bar_ts=%d|raw_prob=%s|cal_prob=%s|feature_hash=%s|model_version=%d" % (
        e["predictedAt"], e["symbolId"], e["horizon"], e["barTs"], go_g(e["rawProb"]),
        go_g(e["calProb"]), e["featureHash"], e["modelVersion"])
    return hashlib.sha256(prev.encode() + b"\x1e" + payload.encode()).hexdigest()


def parse_utc(s):
    return dt.datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=UTC)


def parse_statement(path):
    name = os.path.basename(path)
    m = NAME_RE.match(name)
    if not m:
        raise ValueError("%s: name is not YYYYMMDDTHHMMSSZ.txt" % name)
    raw = open(path, "rb").read()
    lines = raw.decode("utf-8", "replace").split("\n")
    if lines[-1] == "":
        lines.pop()
    if len(lines) != len(LINE_RES):
        raise ValueError("%s: %d lines, want %d" % (name, len(lines), len(LINE_RES)))
    g = []
    for i, (line, rx) in enumerate(zip(lines, LINE_RES), 1):
        mm = rx.match(line)
        if not mm:
            raise ValueError("%s line %d is malformed: %r" % (name, i, line))
        g.append(mm.groups())
    s = m.group(1)
    want = "%s-%s-%sT%s:%s:%sZ" % (s[0:4], s[4:6], s[6:8], s[9:11], s[11:13], s[13:15])
    if g[1][0] != want:
        raise ValueError("%s: utc %s does not match its name" % (name, g[1][0]))
    return {"name": name, "path": path, "raw": raw, "utc": parse_utc(want), "seq": int(g[2][0]),
            "head": g[2][1], "anchor": g[3][0], "registry": g[5][0], "doc": g[6][0], "ext": None}


def tsa_time(openssl, tsr):
    code, out = run([openssl, "ts", "-reply", "-in", tsr, "-text"])
    m = re.search(r"^Time stamp: (\w{3}) +(\d{1,2}) (\d{2}):(\d{2}):(\d{2})(?:\.\d+)? (\d{4}) GMT", out, re.M)
    if code or not m or m.group(1) not in MONTHS:
        return None
    return dt.datetime(int(m.group(6)), MONTHS.index(m.group(1)) + 1, int(m.group(2)),
                       int(m.group(3)), int(m.group(4)), int(m.group(5)), tzinfo=UTC)


def cert_fingerprint(path):
    """SHA-256 of the first PEM certificate's DER bytes, or None."""
    try:
        pem = open(path, encoding="ascii", errors="ignore").read()
    except OSError:
        return None
    m = re.search(r"-----BEGIN CERTIFICATE-----(.*?)-----END CERTIFICATE-----", pem, re.S)
    if not m:
        return None
    try:
        return hashlib.sha256(base64.b64decode("".join(m.group(1).split()))).hexdigest()
    except ValueError:
        return None


def verify_token(openssl, repo, data_path, tsr, tsa):
    trust = [os.path.join(repo, a) if a.startswith("tsa/") else a for a in TSAS[tsa]]
    code, out = run([openssl, "ts", "-verify", "-data", data_path, "-in", tsr] + trust)
    return code == 0 and "Verification: OK" in out


def ots_binds(ots_bytes, stmt_bytes):
    head = MAGIC + b"\x01\x08"
    return ots_bytes[: len(head)] == head and ots_bytes[len(head): len(head) + 32] == hashlib.sha256(stmt_bytes).digest()


def statement_adds(repo):
    """[(commit, path)] for every stamps/ change, oldest first, plus any non-add status."""
    code, out = run(["git", "-C", repo, "log", "--reverse", "--format=commit %H", "--name-status", "--", "stamps/"])
    commit, adds, bad = None, [], []
    for line in out.splitlines():
        if line.startswith("commit "):
            commit = line[7:]
        elif "\t" in line:
            status, path = line.split("\t", 1)
            if status == "A":
                adds.append((commit, status, path))
            elif not (status == "M" and path.endswith(".ots")):
                # An `ots upgrade` rewrites a .ots in place, adding the Bitcoin
                # attestation; check_ots still requires it to bind its statement.
                bad.append((commit, status, path))
    return adds, bad


def check_statements(repo, rep):
    d = os.path.join(repo, "stamps")
    names = sorted(n for n in os.listdir(d) if n.endswith(".txt")) if os.path.isdir(d) else []
    stmts = []
    for n in names:
        try:
            stmts.append(parse_statement(os.path.join(d, n)))
        except ValueError as e:
            rep("FAIL", "statement %s" % e)
    for a, b in zip(stmts, stmts[1:]):
        if b["seq"] < a["seq"]:
            rep("FAIL", "statements: %s head seq %d is below %s's %d" % (b["name"], b["seq"], a["name"], a["seq"]))
    first_at = {}
    for st in stmts:
        prior = first_at.setdefault(st["seq"], st)
        if prior["head"] != st["head"]:
            rep("FAIL", "statements: %s and %s commit to DIFFERENT heads at seq %d -- the ledger was rewritten "
                        "between them" % (prior["name"], st["name"], st["seq"]))
    if not names:
        rep("WARN", "statements: none yet")
    elif stmts:
        rep("PASS", "statements: %d parsed, newest %s head seq %d" % (len(stmts), stmts[-1]["name"], stmts[-1]["seq"]))
    return stmts


def check_tokens(repo, stmts, openssl, rep):
    trusted = {}
    for tsa, args in TSAS.items():
        files = [a for a in args if a.startswith("tsa/")]
        bad = [f for f in files if cert_fingerprint(os.path.join(repo, f)) != PINS[f]]
        for f in bad:
            rep("FAIL", "rfc3161 %s: %s is not the pinned certificate; tokens checked against it would prove nothing" % (tsa, f))
        trusted[tsa] = not bad
    for tsa in TSAS:
        if not trusted[tsa]:
            continue
        have = ok = 0
        for s in stmts:
            tsr = "%s.%s.tsr" % (s["path"], tsa)
            if not os.path.exists(tsr):
                continue
            have += 1
            t = tsa_time(openssl, tsr) if verify_token(openssl, repo, s["path"], tsr, tsa) else None
            if t is None:
                rep("FAIL", "rfc3161 %s: %s does not verify against its statement" % (tsa, os.path.basename(tsr)))
            elif t < s["utc"] - dt.timedelta(seconds=300):
                rep("FAIL", "rfc3161 %s: %s predates the statement it covers (%s < %s)" % (tsa, s["name"], t, s["utc"]))
            else:
                ok += 1
                s["ext"] = t if s["ext"] is None else min(s["ext"], t)
        if have:
            rep("PASS" if ok == have else "FAIL", "rfc3161 %s: %d/%d tokens verify" % (tsa, ok, have))
    for s in stmts:
        if s["ext"] is None:
            rep("WARN", "rfc3161: %s has no verified timestamp" % s["name"])


def check_ots(stmts, rep):
    k = 0
    for s in stmts:
        p = s["path"] + ".ots"
        if not os.path.exists(p):
            continue
        if ots_binds(open(p, "rb").read(), s["raw"]):
            k += 1
        else:
            rep("FAIL", "ots: %s.ots does not commit to its statement's bytes" % s["name"])
    if k:
        rep("INFO", "ots: %d proofs bind to their statements; confirm the Bitcoin attestation with the "
                    "OpenTimestamps client: ots verify stamps/<NAME>.txt.ots" % k)


def check_history(repo, stmts, rep):
    if shutil.which("git") is None or not os.path.isdir(os.path.join(repo, ".git")):
        rep("SKIP", "history: git or REPO/.git missing; append-only checks skipped")
        return
    for path in ("anchors.log", "prereg.log"):
        _, out = run(["git", "-C", repo, "log", "--reverse", "--format=%H", "--", path])
        prev, revs, broken = b"", out.split(), None
        for rev in revs:
            cur = git_bytes(repo, rev, path)
            if not cur.startswith(prev):
                broken = rev
                break
            prev = cur
        if broken:
            rep("FAIL", "history: commit %s changed or removed published lines of %s" % (broken[:12], path))
        else:
            rep("PASS", "history: %s only ever grew (%d revisions)" % (path, len(revs)))
    adds, bad = statement_adds(repo)
    for commit, status, path in bad:
        rep("FAIL", "history: %s was %s after publication in commit %s" % (path, status, commit[:12]))
    if not bad:
        rep("PASS", "history: %d stamps/ files, none modified, renamed or deleted after publication" % len(adds))
    added_in = {os.path.basename(p): c for c, _, p in adds}
    bound = 0
    for s in stmts:
        c = added_in.get(s["name"])
        if c is None:
            continue
        if hashlib.sha256(git_bytes(repo, c, "accuracy_registry.json")).hexdigest() != s["registry"]:
            rep("FAIL", "binding: %s's registry hash does not match the registry committed beside it" % s["name"])
        elif s["doc"] != "none" and hashlib.sha256(git_bytes(repo, c, "PREREGISTRATION.md")).hexdigest() != s["doc"]:
            rep("FAIL", "binding: %s's protocol hash does not match PREREGISTRATION.md committed beside it" % s["name"])
        else:
            bound += 1
    if stmts:
        rep("PASS" if bound == len(stmts) else "WARN",
            "binding: %d/%d statements match the registry and protocol committed with them" % (bound, len(stmts)))
    p = os.path.join(repo, "anchors.log")
    lines = [l for l in open(p, encoding="utf-8").read().split("\n") if l] if os.path.exists(p) else []
    named = {s["anchor"] for s in stmts}
    rep("INFO", "anchors: %d of %d anchors.log lines appear in a timestamped statement; the rest predate "
                "statements and carry no external timestamp beyond git" % (sum(l in named for l in lines), len(lines)))


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "signaldeck-verify/1"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read().decode("utf-8"))


def check_chain(site, stmts, n_stmts, rep):
    base = stmts[-n_stmts] if 2 <= n_stmts <= len(stmts) else None
    lo, running = (base["seq"] + 1, base["head"]) if base else (1, "")
    hi = stmts[-1]["seq"]
    heads = {}
    for st in stmts:
        if st["seq"] >= lo:
            heads.setdefault(st["seq"], []).append(st)
    seen, nxt, expect = [], lo, lo
    while expect <= hi:
        url = "%s/api/ledger/range?from=%d&limit=5000" % (site.rstrip("/"), nxt)
        try:
            page = fetch(url)
        except Exception as e:
            rep("FAIL", "chain: %s: %s" % (url, e))
            return None
        for e in page.get("entries") or []:
            if e["seq"] > hi:
                break
            if e["seq"] != expect or e["prevHash"] != running or entry_hash(running, e) != e["entryHash"]:
                rep("FAIL", "chain: entry seq %d does not link or does not reproduce its hash" % e["seq"])
                return None
            running, expect = e["entryHash"], expect + 1
            seen.append((e["seq"], e["horizon"], e["barTs"], e["predictedAt"]))
            for st in heads.get(e["seq"], []):
                if st["head"] != running:
                    rep("FAIL", "chain: recomputed head at seq %d differs from statement %s" % (e["seq"], st["name"]))
                    return None
        if expect <= hi and page.get("next") is None:
            rep("FAIL", "chain: the site's ledger ends at seq %d, before statement head %d" % (expect - 1, hi))
            return None
        nxt = page.get("next") or expect
    rep("PASS", "chain: recomputed seq %d..%d (%d entries); every statement head in that range matches" % (lo, hi, len(seen)))
    if base:
        rep("INFO", "chain: only the ledger after %s was recomputed; run with --statements 0 to re-check every "
                    "forecast from the first entry" % base["name"])
    return seen


def anteriority(entries, stmts):
    """{horizon: (anchored, total, [lag hours])}: a forecast counts when the first
    externally timestamped statement covering it predates its outcome window's end."""
    proven = [(s["seq"], s["ext"]) for s in stmts if s["ext"] is not None]
    seqs = [p[0] for p in proven]
    out = {}
    for seq, horizon, bar_ts, predicted_at in entries:
        if horizon not in HORIZON:
            continue
        a, n, lags = out.get(horizon, (0, 0, []))
        i = bisect.bisect_left(seqs, seq)
        ext = proven[i][1].timestamp() if i < len(proven) else None
        if ext is not None:
            lags.append((ext - predicted_at) / 3600)
            if ext < bar_ts + HORIZON[horizon]:
                a += 1
        out[horizon] = (a, n + 1, lags)
    return out


def verify(repo, site, n_stmts, openssl, rep):
    stmts = check_statements(repo, rep)
    check_tokens(repo, stmts, openssl, rep)
    check_ots(stmts, rep)
    check_history(repo, stmts, rep)
    if not site:
        rep("SKIP", "chain: pass --site to recompute the ledger between statements")
        return
    if not stmts:
        rep("SKIP", "chain: no statements to check against")
        return
    entries = check_chain(site, stmts, n_stmts, rep)
    for h, (a, n, lags) in sorted((anteriority(entries, stmts) if entries else {}).items()):
        lags.sort()
        med = "%.1fh" % lags[len(lags) // 2] if lags else "n/a"
        rep("INFO", "anteriority %s: %d/%d forecasts (%.1f%%) externally timestamped before their outcome "
                    "window closed; median lag from prediction to timestamp %s" % (h, a, n, 100.0 * a / n, med))


def main(argv):
    ap = argparse.ArgumentParser(description="Check SignalDeck's public commitment log.")
    ap.add_argument("repo", nargs="?")
    ap.add_argument("--site")
    # 0 = recompute from the first entry: the only walk that catches an edited
    # OLDER forecast. N >= 2 is the quick check of the last N-1 intervals.
    ap.add_argument("--statements", type=int, default=0)
    ap.add_argument("--openssl", default=os.environ.get("SD_OPENSSL", "openssl"))
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args(argv)
    if a.selftest:
        return selftest()
    if not a.repo:
        ap.print_usage()
        return 2
    rep = Report()
    verify(a.repo, a.site, a.statements, a.openssl, rep)
    if rep.n["FAIL"]:
        print("VERIFY RESULT: FAIL (%d fail, %d pass, %d warn)" % (rep.n["FAIL"], rep.n["PASS"], rep.n["WARN"]))
        return 1
    # A skipped check proved nothing, so it cannot contribute to a PASS: without
    # --site this used to print PASS having never recomputed a single forecast.
    if rep.n["SKIP"] or not rep.n["PASS"]:
        print("VERIFY RESULT: INCOMPLETE (%d pass, %d warn, %d skipped) -- nothing failed, but the checks marked "
              "SKIP above did not run, so this is not a PASS" % (rep.n["PASS"], rep.n["WARN"], rep.n["SKIP"]))
        return 3
    print("VERIFY RESULT: PASS (%d pass, %d warn)" % (rep.n["PASS"], rep.n["WARN"]))
    return 0


# ── self-test ────────────────────────────────────────────────────────────────

GO_VECTORS = [(0.0, "0"), (1.0, "1"), (0.5, "0.5"), (-0.25, "-0.25"), (0.123456789, "0.123456789"),
              (1 / 3, "0.3333333333333333"), (2 / 3, "0.6666666666666666"), (0.1 + 0.2, "0.30000000000000004"),
              (1e-05, "1e-05"), (5.123e-05, "5.123e-05"), (0.0001, "0.0001"), (0.00012345, "0.00012345"),
              (123456.0, "123456"), (999999.0, "999999"), (1e6, "1e+06"), (1.5e6, "1.5e+06"), (1e21, "1e+21"),
              (1e-07, "1e-07"), (0.999999999999, "0.999999999999"), (0.5000000000000001, "0.5000000000000001")]
REAL_CHAIN = json.loads("""[
{"seq":620001,"predictedAt":1790818021,"symbolId":10,"horizon":"1d","barTs":1790817960,"rawProb":0.474422524956232,"calProb":0.474422524956232,"featureHash":"4ddfd4f71009b5c851aa15fd7a6002aee03384b66eda686427d519099902a189","modelVersion":1,"prevHash":"37fc628c025b30b06f51fcc504b92d20a6d93e74741cd6409a73afdf213992b1","entryHash":"bb0e5f85df50c5f44aa781fe9e46b61c153036640d82cdd43b7102140f77fd8e"},
{"seq":620002,"predictedAt":1790818021,"symbolId":10,"horizon":"1w","barTs":1790817960,"rawProb":0.5099019554015459,"calProb":0.5099019554015459,"featureHash":"e945d6b354e4a15983fd379d2279d48da19993c5103b6ae4a5d5d5b97b387eaa","modelVersion":1,"prevHash":"bb0e5f85df50c5f44aa781fe9e46b61c153036640d82cdd43b7102140f77fd8e","entryHash":"60e126cdbb65bd3689b467d2befde72d17df9227dde03bd7c77dbd9901dc8ed0"},
{"seq":620003,"predictedAt":1790818021,"symbolId":535,"horizon":"1d","barTs":1790817960,"rawProb":0.5475452881399131,"calProb":0.5475452881399131,"featureHash":"99f3700f40ae31a776dde03cc4cfcbd71836947559dca589d2861df7528303ce","modelVersion":1,"prevHash":"60e126cdbb65bd3689b467d2befde72d17df9227dde03bd7c77dbd9901dc8ed0","entryHash":"6a48eea11abebb9a5e919c423cf5b2ea30e62a6d77d82110199b45a5fcbcdd8e"},
{"seq":539683,"predictedAt":1790129663,"symbolId":760,"horizon":"1w","barTs":1790129220,"rawProb":1,"calProb":1,"featureHash":"e2b2dd0b32d86f5bbaf8d0f0719590ac3b967e3e757925a47a6c069be89fcb22","modelVersion":1,"prevHash":"519d7ed430c29d4df0b59ade5c554286837c854054a83512a6afbb0bf1f73c54","entryHash":"6cb701fc992410e090acd6c8b71d6a06530eae80cc6160ecee278adad7276857"}
]""")
STMT = ("signaldeck-statement v1\nutc 2026-10-01T04:31:22Z\nledger-head seq=7 head=%s intact=true\n"
        "anchor SIGNALDECK-LEDGER-ANCHOR v1 seq=7 count=7 ts=1 digest=%s\nprereg none\n"
        "registry-sha256 %s\nprereg-doc-sha256 none\n")


def selftest():
    fails = []

    def expect(cond, what):
        if not cond:
            fails.append(what)

    for v, want in GO_VECTORS:
        expect(go_g(v) == want, "go_g(%r) = %r, want %r" % (v, go_g(v), want))
    for e in REAL_CHAIN:
        expect(entry_hash(e["prevHash"], e) == e["entryHash"], "real ledger entry %d does not reproduce" % e["seq"])
    bent = dict(REAL_CHAIN[1], calProb=REAL_CHAIN[1]["calProb"] + 1e-12)
    expect(entry_hash(bent["prevHash"], bent) != bent["entryHash"], "a 1e-12 change to calProb went unnoticed")
    head = MAGIC + b"\x01\x08"
    expect(ots_binds(head + hashlib.sha256(b"x").digest() + b"\xf0\x10" + os.urandom(16), b"x"), "valid .ots rejected")
    expect(not ots_binds(head + hashlib.sha256(b"y").digest(), b"x"), ".ots for other bytes accepted")
    expect(not ots_binds(b"\x01" + head[1:] + hashlib.sha256(b"x").digest(), b"x"), ".ots with a bad magic accepted")
    entries = [(5, "1d", 1000, 900), (6, "1d", 1000 - 90000, 900), (7, "1w", 0, 0), (9, "1d", 10 ** 10, 0)]
    res = anteriority(entries, [{"seq": 8, "ext": dt.datetime.fromtimestamp(1000 + 86400 - 1, UTC)}])
    expect(res["1d"][:2] == (1, 3), "anteriority 1d counted %r, want (1, 3)" % (res["1d"][:2],))
    expect(res["1w"][:2] == (1, 1), "anteriority 1w counted %r, want (1, 1)" % (res["1w"][:2],))

    fix = os.environ.get("SD_TSA_FIXTURE")
    openssl = os.environ.get("SD_OPENSSL", "openssl")
    if not fix or shutil.which("git") is None or run([openssl, "version"])[0] != 0:
        print("selftest needs git, openssl and SD_TSA_FIXTURE (a dir with s.txt, its two .tsr and the tsa certs)")
        return 2
    with tempfile.TemporaryDirectory() as tmp:
        repo = os.path.join(tmp, "repo")
        os.makedirs(os.path.join(repo, "tsa"))
        os.makedirs(os.path.join(repo, "stamps"))
        for c in ("freetsa-cacert.pem", "freetsa-tsa.crt", "digicert-trusted-root-g4.pem"):
            shutil.copy(os.path.join(fix, c), os.path.join(repo, "tsa", c))
        bent_s = os.path.join(tmp, "s.txt")
        with open(os.path.join(fix, "s.txt"), "rb") as f:
            raw = f.read()
        with open(bent_s, "wb") as f:
            f.write(raw[:-1] + bytes([raw[-1] ^ 1]))
        for tsa in TSAS:
            tsr = os.path.join(fix, "s.txt.%s.tsr" % tsa)
            expect(verify_token(openssl, repo, os.path.join(fix, "s.txt"), tsr, tsa), "%s token did not verify" % tsa)
            expect(tsa_time(openssl, tsr) == dt.datetime(2026, 10, 1, 4, 31, 22, tzinfo=UTC), "%s time misread" % tsa)
            expect(not verify_token(openssl, repo, bent_s, tsr, tsa), "%s token verified changed bytes" % tsa)

        good = os.path.join(repo, "stamps", "20261001T043122Z.txt")
        reg = b'{"rows": []}\n'
        with open(good, "w", encoding="utf-8", newline="\n") as f:
            f.write(STMT % ("a" * 64, "b" * 64, hashlib.sha256(reg).hexdigest()))
        expect(parse_statement(good)["seq"] == 7, "valid statement did not parse")
        for name, body in (("20261001T043123Z.txt", STMT % ("a" * 64, "b" * 64, "c" * 64)),
                           ("20261001T043124Z.txt", "\n".join((STMT % ("a" * 64, "b" * 64, "c" * 64)).split("\n")[:6]))):
            p = os.path.join(tmp, name)
            with open(p, "w", encoding="utf-8", newline="\n") as f:
                f.write(body)
            try:
                parse_statement(p)
                fails.append("%s should not parse" % name)
            except ValueError:
                pass

        def git(*args):
            return run(["git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "core.autocrlf=false"] + list(args))

        git("init", "-q")
        with open(os.path.join(repo, "accuracy_registry.json"), "wb") as f:
            f.write(reg)
        with open(os.path.join(repo, "anchors.log"), "w", encoding="utf-8", newline="\n") as f:
            f.write("SIGNALDECK-LEDGER-ANCHOR v1 seq=7 count=7 ts=1 digest=%s\n" % ("b" * 64))
        ots = good + ".ots"
        with open(ots, "wb") as f:
            f.write(MAGIC + b"\x01\x08" + hashlib.sha256(open(good, "rb").read()).digest())
        git("add", "-A")
        git("commit", "-q", "-m", "one")
        rep = Report(quiet=True)
        verify(repo, None, 2, openssl, rep)
        expect(rep.n["FAIL"] == 0 and rep.n["WARN"] >= 1, "clean repo: %r" % rep.n)

        # Two statements that disagree about the head at one seq are a rewrite,
        # caught with no network at all.
        twin = os.path.join(repo, "stamps", "20261001T043130Z.txt")
        with open(twin, "w", encoding="utf-8", newline="\n") as f:
            f.write((STMT % ("c" * 64, "b" * 64, hashlib.sha256(reg).hexdigest())).replace(
                "utc 2026-10-01T04:31:22Z", "utc 2026-10-01T04:31:30Z"))
        rep = Report(quiet=True)
        check_statements(repo, rep)
        expect(rep.n["FAIL"] == 1, "two statements with different heads at one seq gave %d FAILs, want 1" % rep.n["FAIL"])
        os.remove(twin)

        # A tsa/ file that is not the pinned certificate disarms its TSA.
        cert = os.path.join(repo, "tsa", "freetsa-tsa.crt")
        shutil.copy(os.path.join(fix, "digicert-trusted-root-g4.pem"), cert)
        rep = Report(quiet=True)
        check_tokens(repo, [], openssl, rep)
        expect(rep.n["FAIL"] == 1, "an unpinned FreeTSA certificate gave %d FAILs, want 1" % rep.n["FAIL"])
        shutil.copy(os.path.join(fix, "freetsa-tsa.crt"), cert)

        log = os.path.join(repo, "anchors.log")
        with open(log, "a", encoding="utf-8", newline="\n") as f:
            f.write("second\n")
        # An `ots upgrade` rewrites the proof in place; that alone is no rewrite.
        with open(ots, "ab") as f:
            f.write(b"\x00upgraded")
        git("commit", "-q", "-am", "append")
        rep = Report(quiet=True)
        check_history(repo, [], rep)
        expect(rep.n["FAIL"] == 0, "an append and an ots upgrade were reported as a rewrite: %r" % rep.n)
        with open(log, "w", encoding="utf-8", newline="\n") as f:
            f.write("SIGNALDECK-LEDGER-ANCHOR v1 seq=7 count=7 ts=1 digest=%s\nchanged\n" % ("b" * 64))
        with open(good, "a", encoding="utf-8", newline="\n") as f:
            f.write("tampered\n")
        git("commit", "-q", "-am", "rewrite")
        rep = Report(quiet=True)
        check_history(repo, [], rep)
        expect(rep.n["FAIL"] == 2, "a rewritten log line and a modified statement gave %d FAILs, want 2" % rep.n["FAIL"])

    for f in fails:
        print("FAIL selftest: %s" % f)
    if fails:
        return 1
    print("SELFTEST OK")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
