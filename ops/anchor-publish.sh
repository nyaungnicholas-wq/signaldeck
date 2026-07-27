#!/bin/bash
# Externally timestamp what the operator must not be able to rewrite.
#
# Five artifacts go to the public repo on every run:
#   1. The newest signed ledger anchor's publish line, appended to anchors.log.
#      A digest sitting in a third party's git history is the only evidence an
#      operator who holds the signing key cannot fabricate after the fact.
#   2. The pre-registration chain head (prereg.log) — the frozen structural
#      claims, grading protocol, and protocol document for the 2026-08-14
#      first grading, timestamped where the operator cannot rewrite them.
#   2b. PREREGISTRATION.md itself, copied verbatim — its SHA-256 is the
#      specHash of the chain's prereg-document record, so publishing the bytes
#      makes the frozen protocol checkable with nothing but `shasum -a 256`.
#   3. The freshly regenerated data/accuracy_registry.json — FAILED verdicts
#      included, nothing filtered. A publicly verifiable, deliberately
#      unflattering track record is the one moat a competitor cannot fabricate
#      overnight, and the commit history makes later retouching of the
#      registry's own past evident.
#   4. README.md, rendered verbatim from that registry — every predictor row
#      with n, accuracy, CI, prequential null, and verdict — so an outsider
#      reads the track record on the repo's front page without cloning or
#      running Python. Never hand-edited; regenerated on every publish.
#
# This script never creates or re-points a remote. The public repo must be
# cloned by the operator first; point SIGNALDECK_ANCHOR_REPO at the clone
# (default ~/.signaldeck/anchor-publish). An absent clone, or a run that
# publishes no anchor line, EXITS NON-ZERO: those are the two ways external
# timestamping silently does not happen, and a run that quietly succeeds while
# nothing leaves the machine is exactly the failure this script exists to
# prevent. (Until 2026-07-27 both were quiet skips, and four consecutive runs
# logged "no anchor available" while reporting success.)

set -uo pipefail

SD="/Users/natalienyaung/claude code/signaldeck"
LOG="$SD/logs/anchor-publish.log"
REPO="${SIGNALDECK_ANCHOR_REPO:-$HOME/.signaldeck/anchor-publish}"
API="http://127.0.0.1:8322"
REG="$SD/data/accuracy_registry.json"

# The A9 tunnel fix (2026-07-26) closed anonymous loopback reads, so API calls
# authenticate WHEN the operator has provisioned SIGNALDECK_API_TOKEN in
# daemon/.env. Absent token = anonymous requests, which still work whenever
# public reads are open. (${AUTH[@]+...} keeps set -u happy on bash 3.2.)
TOKEN=$(grep -m1 '^SIGNALDECK_API_TOKEN=' "$SD/daemon/.env" 2>/dev/null | cut -d= -f2-)
AUTH=()
[ -n "$TOKEN" ] && AUTH=(-H "Authorization: Bearer $TOKEN")

{
  echo "──────── $(date '+%Y-%m-%dT%H:%M:%S') ────────"

  # fail=1 means "this run did not do the job"; the exit status carries it out
  # to the LaunchAgent so a broken publisher is visible without reading the log.
  fail=0

  if [ ! -d "$REPO/.git" ]; then
    echo "FAIL: no public clone at $REPO — nothing was externally timestamped; set SIGNALDECK_ANCHOR_REPO to a clone of the public anchors repo"
    exit 1
  fi

  # 1. Newest anchor digest. Append-only and idempotent: a line already in
  #    anchors.log is not re-appended, so runs between anchor cadences are free.
  #    The API is tried first, but it requires the daemon up and a credential;
  #    the fallback reads ledger_anchors straight from SQLite and RECOMPUTES
  #    the publish line per daemon/internal/ledgeranchor/ledgeranchor.go
  #    (Message/Digest/PublishLine), same as the prereg block below does for
  #    its chain. Recomputation is also a check: if the recomputed digest does
  #    not equal the stored digest column the row is not published.
  touch "$REPO/anchors.log"
  line=$(curl -sf --max-time 10 -H "X-Signaldeck: 1" ${AUTH[@]+"${AUTH[@]}"} "$API/api/ledger/anchors?limit=1" \
    | jq -r '.anchors[0].publish // empty')
  if [ -z "$line" ]; then
    line=$(python3 - "$SD/data/signaldeck.db" <<'PY'
import hashlib, sqlite3, sys
con = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True)
row = con.execute("SELECT created_at, ledger_seq, ledger_count, head_hash, alg,"
                  " pub_key, sig, digest FROM ledger_anchors"
                  " ORDER BY seq DESC LIMIT 1").fetchone()
if not row:
    sys.exit(1)
created_at, lseq, lcount, head, alg, pub, sig, stored = row
if alg != "ed25519":
    sys.exit(1)  # unknown scheme — do not guess at the payload
msg = ("signaldeck-ledger-anchor|v1|alg=ed25519"
       f"|created_at={created_at}|ledger_seq={lseq}|ledger_count={lcount}|head={head}")
digest = hashlib.sha256(
    msg.encode() + b"\x1e" + sig.encode() + b"\x1e" + pub.encode()).hexdigest()
if digest != stored:
    sys.exit(1)  # stored digest does not reproduce — publish nothing
print(f"SIGNALDECK-LEDGER-ANCHOR v1 seq={lseq} count={lcount} ts={created_at} digest={digest}")
PY
    ) || line=""
    [ -n "$line" ] && echo "note: API unavailable — anchor line recomputed from data/signaldeck.db"
  fi
  if [ -n "$line" ]; then
    grep -qxF -- "$line" "$REPO/anchors.log" || printf '%s\n' "$line" >> "$REPO/anchors.log"
  else
    echo "FAIL: no anchor available from $API and none recomputable from data/signaldeck.db — the signed ledger head was NOT externally timestamped by this run"
    fail=1
  fi

  # 2. The pre-registration chain head. The frozen structural claims, the
  #    grading protocol, and the protocol document for the 2026-08-14 first
  #    grading live in prereg_records; a head digest in a third party's git
  #    history is what upgrades "registered on a recorded date" to "provably
  #    registered before the outcomes existed".
  #    Only a VERIFYING chain publishes — an unverified head proves nothing.
  #    Verification is by RECOMPUTATION, straight from the DB: every link is
  #    entry_hash = sha256(prev_hash ‖ 0x1E ‖ "ts=…|kind=…|specHash=…|note=…"),
  #    same construction as the Go side, so this neither needs the daemon up
  #    nor an API credential, and a broken link publishes nothing.
  touch "$REPO/prereg.log"
  pline=$(python3 - "$SD/data/signaldeck.db" <<'PY'
import hashlib, sqlite3, sys
con = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True)
rows = con.execute("SELECT seq, ts, kind, spec_hash, prev_hash, entry_hash, note"
                   " FROM prereg_records ORDER BY seq").fetchall()
prev = ""
for seq, ts, kind, sh, ph, eh, note in rows:
    payload = f"ts={ts}|kind={kind}|specHash={sh}|note={note}".encode()
    if ph != prev or hashlib.sha256(prev.encode() + b"\x1e" + payload).hexdigest() != eh:
        sys.exit(1)  # chain does not verify — print nothing
    prev = eh
if rows:
    print(f"prereg seq={rows[-1][0]} head={prev}")
PY
  )
  if [ -n "$pline" ]; then
    grep -qxF -- "$pline" "$REPO/prereg.log" || printf '%s\n' "$pline" >> "$REPO/prereg.log"
  else
    echo "WARN: prereg chain head unavailable or chain unverified — no prereg line published"
  fi

  # 2b. The protocol document, verbatim. The chain freezes its SHA-256 (the
  #     prereg-document record's specHash); publishing the bytes next to the
  #     head line lets anyone verify the frozen grading protocol without
  #     touching the daemon. Copied only when present, so the LaunchAgent
  #     survives a checkout that predates the document.
  [ -f "$SD/PREREGISTRATION.md" ] && cp "$SD/PREREGISTRATION.md" "$REPO/PREREGISTRATION.md"

  # 3. The honest track record, regenerated now and copied verbatim. If regen
  #    fails the last good copy still publishes — a stale-but-real registry
  #    beats a gap, and the gap would be invisible externally.
  python3 "$SD/tools/accuracy_registry.py" --json "$REG" >/dev/null 2>&1 \
    || echo "WARN: registry regeneration failed — publishing the last good copy"
  jq -e '.rows | length > 0' "$REG" >/dev/null 2>&1 \
    || { echo "ABORT: $REG unreadable or empty — nothing published"; exit 1; }
  cp "$REG" "$REPO/accuracy_registry.json"

  # 4. Human-readable render of the same registry, straight onto the repo's
  #    front page. The JSON stays canonical; the README is a verbatim
  #    rendering — every row, FAILED/INSUFFICIENT included, nothing filtered.
  #    Rendered to a temp file so a mid-render failure never leaves a
  #    truncated README in the public history.
  jq -r '
    def pct: if . == null then "—" else "\((. * 1000 | round) / 10)%" end;
    def n3: (. * 1000 | round) / 1000;
    def esc: tostring | gsub("\\|"; "\\|");
    def ci: if .ci then "[\(.ci[0] | n3), \(.ci[1] | n3)]" else (.ci_method // "—") end;
    [ "# SignalDeck — Public Track Record",
      "",
      "Every predictor SignalDeck tracks, rendered verbatim from [`accuracy_registry.json`](accuracy_registry.json) on every publish — FAILED and INSUFFICIENT verdicts included, nothing filtered. A deliberately unflattering public record is the point: it cannot be fabricated overnight, and the commit history of this repo makes later retouching of the past record evident.",
      "",
      "- Generated: \(.generated)",
      "- Minimum independent n for a verdict: \(.min_independent_n)",
      "- Survivorship epoch: \(.survivorship_epoch)",
      "- Null policy: \(.null_policy)",
      "",
      "| Predictor | Family | Band | n | Accuracy | 95% CI | Prequential null | Verdict | Note |",
      "|---|---|---|--:|--:|---|--:|---|---|",
      (.rows[] | "| \(.predictor | esc) | \(.family // "—") | \(.band | esc) | \(.live_n // 0) | \(.live_acc | pct) | \(ci) | \(.null_prequential | pct) | \(.verdict | esc) | \(.note // "" | esc) |"),
      "",
      "## Verifying this record",
      "",
      "- `accuracy_registry.json` — the canonical machine-readable registry; this README is a rendering of it, regenerated by `ops/anchor-publish.sh` on every push and never hand-edited.",
      "- `anchors.log` — signed ledger anchor digests, append-only; a digest in the git history of this repo is evidence the operator cannot fabricate after the fact.",
      "- `prereg.log` — pre-registration chain heads: structural claims frozen and timestamped before their outcomes existed.",
      "- `PREREGISTRATION.md` — the frozen 2026-08-14 structural grading protocol (claim freeze, null choice, min-n/min-days gates, no post-hoc slicing); its `shasum -a 256` must equal the specHash of the chain'\''s prereg-document record."
    ] | .[]
  ' "$REG" > "$REPO/README.md.tmp" \
    && mv "$REPO/README.md.tmp" "$REPO/README.md" \
    || { rm -f "$REPO/README.md.tmp"; echo "WARN: README render failed — publishing without README refresh"; }

  cd "$REPO" || exit 1
  git add anchors.log accuracy_registry.json prereg.log README.md
  [ -f PREREGISTRATION.md ] && git add PREREGISTRATION.md
  if git diff --cached --quiet; then
    echo "nothing new to publish"
    exit $fail
  fi
  git commit -q -m "anchor + accuracy registry $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  git push -q origin HEAD || { echo "PUSH FAILED — commit exists locally only"; exit 1; }
  echo "published $(git rev-parse --short HEAD)"
  exit $fail
} >> "$LOG" 2>&1
