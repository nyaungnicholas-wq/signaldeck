# OpenTimestamps anchors for the pre-registration chain

## What is in here

`head-<date>.txt` records the pre-registration chain head on that date:

    prereg-chain-head <entry_hash of the newest prereg_records row>
    prereg-chain-seq  <its seq>
    stamped-at        <UTC timestamp>

`head-<date>.txt.ots` is its OpenTimestamps proof.

## Why it exists

PREREGISTRATION.md states the defect about itself, verbatim:

> The chain head has **NOT** been published anywhere outside this machine: as
> of 2026-07-27 no public anchors repository exists, so the registration date
> is asserted by the operator and cannot be checked by anyone else.

A hash-chain proves the operator did not edit an EARLIER record without
breaking the chain. It cannot prove the operator did not rewrite the WHOLE
chain, because they hold every key involved. Only a third party can establish
that, and OpenTimestamps is a third party that costs nothing, needs no account,
and takes only a hash -- which is what makes it compatible with keeping this
repository private.

## Verifying one

    ots verify head-2026-09-04.txt.ots

On a Linux host: the `ots` CLI cannot start on Windows, because it imports
python-bitcoinlib's EC bindings which need OpenSSL 1.0-era symbols that
OpenSSL 3 no longer exports. `tools/ots_stamp.py` exists for that reason and
uses the same official serializer without that dependency.

The proof will be PENDING until the Bitcoin attestation confirms, which takes
hours. Upgrade it with `ots upgrade <file>.ots`, which replaces the pending
calendar attestations with a Bitcoin block attestation.

## What this does and does not prove

It proves the hash existed at or before a Bitcoin block.

It proves NOTHING about anything before the first stamp. Anteriority for
records created earlier still rests on the operator's word. Anchoring on
2026-09-04 does not retroactively establish that seq 87 was filed on
2026-08-23; it establishes that from 2026-09-04 onward the chain cannot be
rewritten without the rewrite being detectable.

Saying otherwise would be exactly the kind of overstatement this platform
exists to avoid.

## The record

| date | chain seq | entry hash | calendars |
|---|---|---|---|
| 2026-09-04 | 104 | `788ccec5c4c99451b5827a8132736ae55ac6d894fcd42566c9f67a233f8c1f4e` | 3 of 3 |
