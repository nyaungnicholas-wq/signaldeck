# Checking SignalDeck's record yourself

This repository is SignalDeck's public commitment log. Nothing here has to be taken on trust: every claim below can be checked with `git`, `openssl` and Python 3.10+, without an account and without asking SignalDeck anything.

## The short version

```bash
git clone https://github.com/nyaungnicholas-wq/signaldeck-anchors
cd signaldeck-anchors
python verify.py .
```

Add `--site <SignalDeck address>` to also download the forecast ledger and recompute it against the published heads:

```bash
python verify.py . --site https://<current SignalDeck address>
```

The last line says `VERIFY RESULT: PASS` or `VERIFY RESULT: FAIL`, and the lines above it say which check produced which verdict.

## What is in here

| File | What it is |
|---|---|
| `stamps/<UTC time>.txt` | A statement: the forecast ledger's head at that moment, the newest signed anchor, the pre-registration chain head, and the SHA-256 of the registry and protocol files. Written up to four times a day, only when something changed. |
| `stamps/*.txt.freetsa.tsr`, `*.digicert.tsr` | RFC 3161 timestamps from FreeTSA and DigiCert over that statement. Each is a signed time from an authority SignalDeck does not control, and verifies offline. |
| `stamps/*.txt.ots` | An OpenTimestamps proof that anchors the statement in the Bitcoin blockchain. |
| `anchors.log` | Every signed ledger anchor, oldest first. Append-only. |
| `prereg.log` | The pre-registration chain head over time. Append-only. |
| `PREREGISTRATION.md` | The grading protocol, byte for byte. Its SHA-256 is frozen in the pre-registration chain. |
| `accuracy_registry.json`, `README.md` | The graded track record, failed verdicts included, regenerated on every publish. |
| `tsa/` | The certificates needed to check the RFC 3161 timestamps without trusting your system's certificate store. |
| `verify.py` | The checker. Standard library only; read it before you run it. |

## What the checker proves

1. **Each statement existed by its timestamp.** `openssl ts -verify` checks each RFC 3161 token against the statement's exact bytes. A token cannot be produced after the fact for different content.
2. **Nothing published was rewritten.** It walks this repository's git history: `anchors.log` and `prereg.log` only ever grew, and no statement or proof was ever modified or deleted after it was added.
3. **The forecasts on the site are the ones that were committed** (with `--site`). It downloads the ledger by sequence number from `/api/ledger/range`, recomputes every hash link, and checks the result equals the head each statement committed to. Any edited, inserted, deleted or reordered forecast breaks the match.
4. **Which forecasts were committed before their outcome could be known** (with `--site`). For each forecast it finds the first timestamped statement that covers it and compares that time with the end of the forecast's horizon. It reports the share per horizon, plainly, including the ones that miss.

## What it does not prove

- **Anything before the first statement.** Forecasts and anchors from before the first file in `stamps/` are covered only from that statement's date onward. Their earlier dates rest on SignalDeck's word, and the checker says so rather than counting them.
- **That a forecast is any good.** Timestamps prove when something was said, not whether it was right. The graded results are in `README.md`, failures included.
- **The Bitcoin confirmation, on its own.** The checker confirms each `.ots` proof belongs to its statement. To confirm the Bitcoin attestation itself, run the OpenTimestamps client: `ots verify stamps/<file>.txt.ots`.

If a check fails, that failure is the finding. Please report it as an issue on this repository.
