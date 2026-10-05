"""Fallback anchor verifier for ops/anchor-publish.sh.
Usage: python anchor_fallback.py DB PINNED_PUBKEYS
Prints exactly one publish line and exits 0 only when the newest anchor's signature verifies under a pinned key,
its head equals the ledger's entry_hash at its seq and its row count matches; otherwise prints the reason to stderr and exits 1.
Requires the openssl binary (SD_OPENSSL env var overrides the path) for the Ed25519 check and refuses when it is missing.
"""

import sqlite3
import hashlib
import base64
import os
import re
import subprocess
import sys
import tempfile

# Strict hex, like Go's hex.DecodeString: bytes.fromhex also accepts spaces.
HEX = re.compile(r"[0-9a-fA-F]+")


def check(db_path, pinned_path, openssl="openssl"):
    try:
        with open(pinned_path, encoding="utf-8") as f:
            pinned = {line.split("#")[0].strip().lower() for line in f} - {""}
    except OSError as e:
        return None, f"pinned key file unreadable: {e}"
    # The daemon refuses a pinned file with any malformed key line
    # (ledgeranchor.ParseKeySet); so does this.
    if any(len(k) != 64 or not HEX.fullmatch(k) for k in pinned):
        return None, "pinned key file has a line that is not a 32-byte hex public key"

    # One read-only connection, so the anchor and the ledger rows it is checked
    # against come from one database state.
    conn = None
    try:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=60)
        row = conn.execute(
            "SELECT created_at, ledger_seq, ledger_count, head_hash, alg, pub_key, sig, digest "
            "FROM ledger_anchors ORDER BY seq DESC LIMIT 1"
        ).fetchone()
        if row is None:
            return None, "no anchor rows"
        created_at, ledger_seq, ledger_count, head_hash, alg, pub_key, sig, stored_digest = row
        if alg != "ed25519":
            return None, f"unknown signature scheme {alg}"
        if pub_key.lower() not in pinned:
            return None, f"newest anchor (seq {ledger_seq}) is signed by an unpinned key"
        ledger_row = conn.execute("SELECT entry_hash FROM prediction_ledger WHERE seq=?", (ledger_seq,)).fetchone()
        if ledger_row is None:
            return None, f"ledger seq {ledger_seq} no longer exists"
        if ledger_row[0] != head_hash:
            return None, f"the ledger no longer reproduces the signed head at seq {ledger_seq}"
        (got_count,) = conn.execute("SELECT COUNT(*) FROM prediction_ledger WHERE seq<=?", (ledger_seq,)).fetchone()
        if got_count != ledger_count:
            return None, f"row count to seq {ledger_seq} is {got_count}, not the anchored {ledger_count}"
    except sqlite3.Error as e:
        return None, f"database: {e}"
    finally:
        if conn:
            conn.close()

    msg = f"signaldeck-ledger-anchor|v1|alg=ed25519|created_at={created_at}|ledger_seq={ledger_seq}|ledger_count={ledger_count}|head={head_hash}".encode()
    if len(pub_key) != 64 or len(sig) != 128 or not HEX.fullmatch(pub_key) or not HEX.fullmatch(sig):
        return None, "malformed public key or signature"
    pub, sig_bytes = bytes.fromhex(pub_key), bytes.fromhex(sig)

    try:
        with tempfile.TemporaryDirectory() as td:
            kpem = os.path.join(td, "k.pem")
            mbin = os.path.join(td, "m.bin")
            sbin = os.path.join(td, "s.bin")
            header = bytes.fromhex("302a300506032b6570032100")
            with open(kpem, "w") as fk:
                fk.write("-----BEGIN PUBLIC KEY-----\n")
                fk.write(base64.b64encode(header + pub).decode())
                fk.write("\n-----END PUBLIC KEY-----\n")
            with open(mbin, "wb") as fm:
                fm.write(msg)
            with open(sbin, "wb") as fs:
                fs.write(sig_bytes)
            result = subprocess.run(
                [openssl, "pkeyutl", "-verify", "-pubin", "-inkey", kpem, "-rawin", "-in", mbin, "-sigfile", sbin],
                capture_output=True,
                text=True,
                timeout=60,
            )
    except (OSError, subprocess.SubprocessError) as e:
        return None, f"openssl unavailable, cannot verify the signature: {e}"

    if result.returncode != 0 and "Signature Verification Failure" not in result.stdout:
        # e.g. an OpenSSL older than 3.0 has no -rawin: no verdict, not tamper.
        why = (result.stderr.strip().splitlines() or ["no output"])[0]
        return None, f"openssl could not check the signature (OpenSSL 3 needed): {why}"
    if result.returncode != 0 or "Signature Verified Successfully" not in result.stdout:
        return None, "signature does not verify under the recorded public key"

    digest = hashlib.sha256(msg + b"\x1e" + sig.encode() + b"\x1e" + pub_key.encode()).hexdigest()
    if digest != stored_digest:
        return None, "stored digest does not reproduce"

    line = f"SIGNALDECK-LEDGER-ANCHOR v1 seq={ledger_seq} count={ledger_count} ts={created_at} digest={digest}"
    return line, None

def main(argv):
    if len(argv) != 2:
        print("usage: python anchor_fallback.py DB PINNED_PUBKEYS", file=sys.stderr)
        return 2
    db_path, pinned_path = argv
    openssl = os.environ.get("SD_OPENSSL", "openssl")
    line, reason = check(db_path, pinned_path, openssl)
    if line is not None:
        print(line)
        return 0
    else:
        print(f"anchor_fallback: refused: {reason}", file=sys.stderr)
        return 1

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))