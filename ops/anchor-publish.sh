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
#   5. stamps/<UTC>.txt, a statement binding the CURRENT ledger head, the
#      anchor line, the prereg head and the hashes of the registry and protocol,
#      timestamped by two RFC 3161 authorities and OpenTimestamps (see step 5
#      below for why git dates alone prove nothing), plus verify.py, VERIFY.md
#      and the TSA certificates a stranger needs to check all of it offline.
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

# Repo root, derived from this script's own location rather than hardcoded.
# Same defect already fixed in ops/accuracy-registry.sh; it was left standing
# here, so off the original Mac this resolved nowhere and the anchor never
# published.
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"
LOG="$SD/logs/anchor-publish.log"
REPO="${SIGNALDECK_ANCHOR_REPO:-$HOME/.signaldeck/anchor-publish}"
API="http://127.0.0.1:8322"
REG="$SD/data/accuracy_registry.json"
# Same spelling as ops/research-liveness.sh so the two cannot end up reading
# different databases. Written to only on a successful push (see the end).
DB="${SIGNALDECK_DB:-$SD/data/signaldeck.db}"

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
    echo "FAIL: no anchors clone at $REPO — nothing was externally timestamped; set SIGNALDECK_ANCHOR_REPO to a clone of github.com/nyaungnicholas-wq/signaldeck-anchors"
    exit 1
  fi

  # 0. Verify first. A verify call writes a signed anchor when one is due, so
  #    step 1 below publishes a fresh one instead of whatever the last visitor
  #    to /api/ledger/verify left behind (measured 2026-09-30: the newest
  #    anchors were 17 hours to 7 days apart). Its `intact` verdict also goes
  #    into the statement in step 5. Failure only downgrades it to "unchecked".
  intact=unchecked
  vcount=0
  vjson=$(curl -sf --max-time 60 -H "X-Signaldeck: 1" ${AUTH[@]+"${AUTH[@]}"} "$API/api/ledger/verify") || vjson=""
  if [ -n "$vjson" ]; then
    vres=$(printf '%s' "$vjson" | "$(sd_py)" -c 'import json, sys
try:
    d = json.load(sys.stdin)
    print("true" if d.get("intact") is True else "false", int(d.get("count") or 0))
except Exception:
    print("unchecked 0")')
    intact=${vres%% *}
    vcount=${vres##* }
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
  # Python, not jq: jq is NOT installed under the Git Bash that runs this
  # repo's scheduled tasks, so every jq line in this file was a `command not
  # found`. That is why logs/anchor-publish.log stops at 2026-07-27 and the
  # anchors repo (private today) — the chain head daemon/internal/pipeline/prereg.go
  # names — has received nothing since. sd_py is the same interpreter shim the
  # SQLite fallback below already uses.
  line=$(curl -sf --max-time 10 -H "X-Signaldeck: 1" ${AUTH[@]+"${AUTH[@]}"} "$API/api/ledger/anchors?limit=1" \
    | "$(sd_py)" -c 'import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
anchors = d.get("anchors") or []
if anchors and anchors[0].get("publish"):
    print(anchors[0]["publish"])
')
  if [ -z "$line" ]; then
    line=$("$(sd_py)" - "$SD/data/signaldeck.db" <<'PY'
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
  pline=$("$(sd_py)" - "$SD/data/signaldeck.db" <<'PY'
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
  #    sd_py, not `python3`: under Git Bash `python3` is a Microsoft Store alias
  #    stub that resolves on PATH and exits non-zero, so this regeneration had
  #    NEVER run on Windows — every publish silently took the WARN branch and
  #    shipped whatever copy was already on disk.
  # No regrade here (removed 2026-09-08): the registry is published exactly as ops/accuracy-registry.sh left it, envelope and all — see git log for why.
  # tools/render_track_record.py refuses an unreadable or rows-empty registry
  # itself (exit 1, nothing on stdout), so the emptiness guard and the README
  # render are one check now instead of two spellings that could disagree.
  # A REFUSED envelope is the one rows-empty shape that renders (a refusal
  # notice, no figures, exit 0): the refusal itself is what gets published.
  # Rendered to a temp file so a mid-render failure never leaves a truncated
  # README in the public history.
  "$(sd_py)" "$SD/tools/render_track_record.py" "$REG" > "$REPO/README.md.tmp" \
    || { rm -f "$REPO/README.md.tmp"; echo "ABORT: $REG unreadable or empty — nothing published"; exit 1; }
  cp "$REG" "$REPO/accuracy_registry.json"

  # 4. Human-readable render of the same registry, straight onto the repo's
  #    front page. The JSON stays canonical; the README is a verbatim
  #    rendering — every row, FAILED/INSUFFICIENT included, nothing filtered.
  #    Rendered to a temp file so a mid-render failure never leaves a
  #    truncated README in the public history.
  mv "$REPO/README.md.tmp" "$REPO/README.md"

  # 5. The timestamped statement (2026-09-30). Everything above lands in git,
  #    and a git date is whatever the committer's clock says: an operator can
  #    force-push a rewritten history with backdated commits and nothing above
  #    would show it. This step writes one small file binding the CURRENT
  #    ledger head (read straight from SQLite, so it does not wait for the
  #    anchor cadence), the anchor line, the prereg head and the hashes of the
  #    registry and protocol, and has it timestamped by parties this machine
  #    does not control: two RFC 3161 authorities (a signed time anyone checks
  #    offline with openssl) and OpenTimestamps (Bitcoin). verify.py recomputes
  #    the ledger chain to each published head via /api/ledger/range and
  #    reports how many forecasts were timestamped before their outcome window
  #    closed -- which is the claim "graded honestly" actually rests on.
  mkdir -p "$REPO/stamps" "$REPO/tsa"
  cp "$SD"/ops/tsa/*.pem "$SD"/ops/tsa/*.crt "$REPO/tsa/"
  cp "$SD/tools/verify_public_record.py" "$REPO/verify.py"
  cp "$SD/ops/anchors-VERIFY.md" "$REPO/VERIFY.md"
  # One byte sequence everywhere. The statement hashes the registry, and the
  # registry is written CRLF on this machine while git stores it LF, so a hash
  # of the working copy could never match what a verifier checks out (measured
  # 2026-09-30: 1551 CRs on disk, 0 in the blob). Normalise to LF here, and
  # `* -text` makes every checkout on every platform hand back exactly these
  # bytes. PREREGISTRATION.md is deliberately not touched: it is LF already and
  # its SHA-256 is frozen in the prereg chain. The two logs are included because
  # a Windows clone checks them out CRLF: committed that way under `* -text`,
  # the old blob would stop being a prefix of the new one, and verify.py's
  # append-only check would report a rewrite that is only a line ending.
  sed -i 's/\r$//' "$REPO/README.md" "$REPO/accuracy_registry.json" "$REPO/VERIFY.md" \
    "$REPO/verify.py" "$REPO/anchors.log" "$REPO/prereg.log" "$REPO"/tsa/*
  printf '%s\n' '# Hashed and timestamped byte-for-byte: never convert line endings.' \
    '* -text' > "$REPO/.gitattributes"
  # Leftovers of a killed run (an unstamped statement, a .tmp token) must never
  # ride along into a later commit: published, an orphan statement binds the
  # wrong registry and fails verify.py forever, and deleting it later fails the
  # never-modified check. Untracked files under stamps/ are only ever ours.
  git -C "$REPO" clean -fq -- stamps 2>/dev/null
  stamp_files=()

  headline=$("$(sd_py)" - "$DB" <<'PY'
import sqlite3, sys
con = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True, timeout=60)
row = con.execute("SELECT seq, entry_hash FROM prediction_ledger"
                  " ORDER BY seq DESC LIMIT 1").fetchone()
if row:
    print(f"seq={row[0]} head={row[1]}")
PY
  ) || headline=""
  if [ -z "$headline" ]; then
    echo "FAIL: ledger head unreadable from $DB — no statement written, nothing timestamped"
    fail=1
  else
    # `intact=true` vouches only for what the step-0 verify walked: if entries
    # landed between that walk and this read, say "unchecked" instead.
    hseq=$(printf '%s' "$headline" | sed -n 's/^seq=\([0-9]*\) .*/\1/p')
    [ "$intact" = true ] && [ "$vcount" != "$hseq" ] && intact=unchecked
    docsha=none
    [ -f "$REPO/PREREGISTRATION.md" ] && docsha=$(sha256sum "$REPO/PREREGISTRATION.md" | cut -d' ' -f1)
    body=$(printf '%s\n' "ledger-head $headline intact=$intact" "anchor ${line:-none}" \
      "${pline:-prereg none}" \
      "registry-sha256 $(sha256sum "$REPO/accuracy_registry.json" | cut -d' ' -f1)" \
      "prereg-doc-sha256 $docsha")
    # The newest PUBLISHED statement (an orphan would have been cleaned above),
    # and it counts as current only if something actually timestamped it: an
    # unstamped one is re-issued, never left standing behind "unchanged".
    prev=$(git -C "$REPO" ls-files -- 'stamps/*.txt' | sort | tail -1)
    if [ -n "$prev" ] && [ "$(tail -n +3 "$REPO/$prev")" = "$body" ] \
       && { compgen -G "$REPO/$prev.*.tsr" >/dev/null || [ -e "$REPO/$prev.ots" ]; }; then
      echo "statement unchanged since $(basename "$prev") — not re-stamped"
    else
      name=$(date -u '+%Y%m%dT%H%M%SZ')
      stmt="$REPO/stamps/$name.txt"
      printf 'signaldeck-statement v1\nutc %s-%s-%sT%s:%s:%sZ\n%s\n' \
        "${name:0:4}" "${name:4:2}" "${name:6:2}" "${name:9:2}" "${name:11:2}" "${name:13:2}" \
        "$body" > "$stmt"
      stamped=0
      tsq="$stmt.tsq.tmp"
      openssl ts -query -data "$stmt" -sha256 -cert -out "$tsq" 2>/dev/null
      # rfc3161 NAME URL VERIFY-ARGS...: keep a token only if it verifies
      # against the certificates verify.py will use.
      rfc3161() {
        local tsa=$1 url=$2; shift 2
        local out="$stmt.$tsa.tsr"
        if curl -sf --max-time 30 -H 'Content-Type: application/timestamp-query' \
             --data-binary @"$tsq" -o "$out.tmp" "$url" \
           && openssl ts -verify -data "$stmt" -in "$out.tmp" "$@" 2>&1 | grep -q 'Verification: OK'; then
          mv "$out.tmp" "$out"; echo "rfc3161 $tsa: OK"; return 0
        fi
        rm -f "$out.tmp"; echo "WARN: rfc3161 $tsa failed — $name not stamped by it"; return 1
      }
      rfc3161 freetsa https://freetsa.org/tsr \
        -CAfile "$REPO/tsa/freetsa-cacert.pem" -untrusted "$REPO/tsa/freetsa-tsa.crt" && stamped=$((stamped+1))
      rfc3161 digicert http://timestamp.digicert.com \
        -CAfile "$REPO/tsa/digicert-trusted-root-g4.pem" && stamped=$((stamped+1))
      rm -f "$tsq"
      # The venv interpreter, not sd_py: sd_py is the system Python, which has
      # no opentimestamps library, so this stamp would fail on every run.
      if "$SD/.venv/Scripts/python.exe" "$SD/tools/ots_stamp.py" "$stmt"; then
        stamped=$((stamped+1))
      else
        echo "WARN: OpenTimestamps stamp failed for $name"
      fi
      if [ "$stamped" -eq 0 ]; then
        echo "FAIL: statement $name was timestamped by NO third party — committed, but it proves nothing beyond git"
        fail=1
      fi
      # Exactly this run's files go into the commit; see the clean above.
      for f in "$stmt" "$stmt.ots" "$stmt.freetsa.tsr" "$stmt.digicert.tsr"; do
        [ -e "$f" ] && stamp_files+=("stamps/$(basename "$f")")
      done
    fi
  fi

  cd "$REPO" || exit 1
  # One path at a time: a single missing path makes a combined `git add` abort
  # whole, and then NOTHING publishes, the statement included (found in the
  # 2026-09-30 dry run, with verify.py not yet in place).
  for p in anchors.log accuracy_registry.json prereg.log README.md PREREGISTRATION.md \
           .gitattributes tsa verify.py VERIFY.md ${stamp_files[@]+"${stamp_files[@]}"}; do
    [ -e "$p" ] && git add -- "$p"
  done
  if git diff --cached --quiet; then
    echo "nothing new to publish"
    exit $fail
  fi
  # Checked: an unchecked failure here used to fall through to the push and the
  # "published" stamp below, reporting a publish that never happened.
  if ! git commit -q -m "anchor + accuracy registry + statement $(date -u '+%Y-%m-%dT%H:%M:%SZ')"; then
    echo "FAIL: git commit failed in $REPO — nothing published"
    exit 1
  fi
  git push -q origin HEAD || { echo "PUSH FAILED — commit exists locally only"; exit 1; }
  # RECORD THE PUSH, NOT THE COMMIT. This is the only durable evidence that
  # anything actually LEFT the machine, and it is deliberately written after
  # `git push` succeeds rather than after `git commit`: a local commit provides
  # none of the third-party-timestamp guarantee this script exists to create.
  #
  # Nothing recorded a successful publish before, which is why the 16-day
  # outage from 2026-07-27 was invisible — anchors kept being SIGNED locally
  # (ledger_anchors held 10 rows, newest 2026-08-10) while none of them were
  # ever externally timestamped, and no check could tell the difference.
  # tools/anchor_liveness.py reads this key.
  # ...and CHECK that it was written. This exit status was unread, so the line
  # below printed "published" whatever happened. Measured 2026-08-12: both the
  # 20:27 and 20:48 runs logged "Error in 2nd command line argument: database is
  # locked" from this very statement and then reported success — after the 18:02
  # commit that gave sd_sqlite a 120s busy timeout, so the timeout is not always
  # enough against the live daemon. The push had already left the machine and the
  # durable evidence of it had not, which is precisely the blindness the comment
  # above describes, reintroduced one line below the warning about it.
  if ! sd_sqlite "$DB" "INSERT OR REPLACE INTO meta(k,v) VALUES('anchor_last_published','$(date +%s)');" 2>>"$LOG"; then
    echo "PUSHED $(git rev-parse --short HEAD) BUT DID NOT RECORD IT: the anchor_last_published"
    echo "stamp failed (see the error just above — 'database is locked' is the usual cause)."
    echo "The commit LEFT the machine; the only durable evidence that it did was not written,"
    echo "so tools/anchor_liveness.py will keep reporting STALE until this is re-run against"
    echo "an unlocked database. Refusing to report a publish that cannot be demonstrated."
    exit 1
  fi
  echo "published $(git rev-parse --short HEAD)"
  exit $fail
} >> "$LOG" 2>&1
