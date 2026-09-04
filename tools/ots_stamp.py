"""OpenTimestamps stamper that avoids the ots CLI's OpenSSL 3/BN_add import failure.

Why this exists instead of calling `ots`:
The official `ots` command-line tool imports python-bitcoinlib EC bindings that
dlopen an OpenSSL 1.0-era libeay32. On systems with only OpenSSL 3 the required
symbols (e.g. BN_add) are missing, causing an ImportError before any work can
be done. Stamping a file only needs a SHA256 and an HTTP POST to a calendar,
so we can use the pure‑Python `opentimestamps` library directly.

Why we do NOT hand‑roll the .ots format:
A malformed proof that cannot be verified is worse than no proof: it *looks*
like evidence but is useless. Using the library's official serializer guarantees
conformance to the OpenTimestamps specification.

What an OpenTimestamp proves and what it does NOT:
It proves that a given hash existed at or before a particular Bitcoin block
(the attestation). It says nothing about the existence of the hashed data
before that first stamp; any claim of earlier anteriority still relies on the
operator's word. Only the hash leaves the machine: no source file or other
data is transmitted.
"""

import argparse
import hashlib
import os
import sys

from opentimestamps.calendar import RemoteCalendar
from opentimestamps.core.op import OpSHA256, OpAppend
from opentimestamps.core.serialize import BytesSerializationContext
from opentimestamps.core.timestamp import DetachedTimestampFile, Timestamp

CALENDARS = (
    "https://alice.btc.calendar.opentimestamps.org",
    "https://bob.btc.calendar.opentimestamps.org",
    "https://finney.calendar.eternitywall.com",
)
# Multiple calendars increase availability: a timestamp is only as available
# as the calendar holding it. If one operator disappears, the others still
# provide attestation.


def stamp(path: str, timeout: int = 30) -> tuple[str, int]:
    """Stamp a file into Bitcoin via OpenTimestamps calendars.

    Returns (hexdigest, success_count). Raises SystemExit if no calendar
    succeeds.
    """
    with open(path, "rb") as f:
        payload = f.read()
    digest = hashlib.sha256(payload).digest()

    file_ts = Timestamp(digest)
    detached = DetachedTimestampFile(OpSHA256(), file_ts)

    success = 0
    for url in CALENDARS:
        try:
            nonce = os.urandom(16)
            # Blind the timestamp with a random append then hash.
            nonced = file_ts.ops.add(OpAppend(nonce))
            blinded = nonced.ops.add(OpSHA256())
            # Submit the blinded message to the calendar.
            result = RemoteCalendar(url).submit(blinded.msg, timeout=timeout)
            # Merge the calendar's attestation back into our blinded timestamp.
            blinded.merge(result)
            success += 1
            print(f"OK {url}")
        except Exception as e:  # Catch any error, report but continue.
            print(f"FAIL {url}: {e}", file=sys.stderr)

    if success == 0:
        raise SystemExit(
            "REFUSING to write a proof: no calendar attestations obtained. "
            "An .ots file with no attestation would look like evidence and be none."
        )

    ctx = BytesSerializationContext()
    detached.serialize(ctx)
    ots_data = ctx.getbytes()
    ots_path = path + ".ots"
    with open(ots_path, "wb") as f:
        f.write(ots_data)

    return digest.hex(), success


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Stamp a file into Bitcoin using OpenTimestamps without the ots CLI."
    )
    parser.add_argument("path", help="File to stamp")
    parser.add_argument(
        "--timeout",
        type=int,
        default=30,
        help="HTTP timeout in seconds for each calendar (default: 30)",
    )
    args = parser.parse_args()

    if not os.path.isfile(args.path):
        print(f"Error: '{args.path}' is not a file", file=sys.stderr)
        return 2

    digest_hex, success_count = stamp(args.path, args.timeout)
    print(f"SHA256: {digest_hex}")
    print(
        f"Wrote {args.path}.ots with {success_count} of {len(CALENDARS)} calendar attestations."
    )
    print(
        "The Bitcoin attestation is PENDING and typically takes several hours to appear in a block."
    )
    print(
        "Once confirmed, upgrade the proof on a Linux host with: ots upgrade <file>.ots"
    )
    print("Anyone can later verify with: ots verify <file>.ots")
    return 0


if __name__ == "__main__":
    sys.exit(main())