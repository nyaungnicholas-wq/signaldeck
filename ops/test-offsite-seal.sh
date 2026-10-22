#!/bin/bash
# Self-test for the SEALED off-site path in lib-offsite-gh.sh (2026-10-22), with
# `gh` and `sdmaint` stubbed so the question is what the functions DO: a
# configured key whose seal fails must never upload plaintext; a sealed upload
# sends only .enc assets plus a sidecar hashing the .enc; with no key the upload
# is today's plaintext one plus a WARNING; the download opens what was sealed.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir -p "$W/bin" "$W/home" "$W/remote" "$W/backups"
export USERPROFILE="$W/home" HOME="$W/home" LOG="$W/log" GH_CALLS="$W/gh.calls" \
    SDMAINT_CALLS="$W/sdmaint.calls" REMOTE="$W/remote" PATH="$W/bin:$PATH"
unset SIGNALDECK_OFFSITE_KEY_FILE SIGNALDECK_TOMBSTONE_LOG SEAL_FAIL

# gh: upload copies into $REMOTE, download copies matching names back out,
# api reports the remote size of the asset named in the --jq select.
cat > "$W/bin/gh" <<'STUB'
#!/bin/bash
echo "$*" >> "$GH_CALLS"
case "$1 ${2:-}" in
"release view"|"release create") exit 0 ;;
"release upload") cp "$4" "$REMOTE/" ;;
"release download")
    shift 3; dir=""; pats=()
    while [ $# -gt 0 ]; do
        case "$1" in --dir) dir="$2"; shift 2 ;; --pattern) pats+=("$2"); shift 2 ;; *) shift ;; esac
    done
    for f in "$REMOTE"/*; do
        [ -e "$f" ] || continue; b=$(basename "$f")
        for p in "${pats[@]}"; do case "$b" in $p) cp "$f" "$dir/"; break ;; esac; done
    done ;;
api*)
    name=$(printf '%s' "$*" | sed -n 's/.*select(.name=="\([^"]*\)").*/\1/p')
    [ -f "$REMOTE/$name" ] && wc -c < "$REMOTE/$name" | tr -d ' ' ;;
*) exit 1 ;;
esac
STUB
# sdmaint: "seals" by prefixing a SEALED line; refuses a missing key file.
cat > "$W/bin/sdmaint" <<'STUB'
#!/bin/bash
cmd="$1"; shift
while [ $# -gt 0 ]; do
    case "$1" in -key) key="$2" ;; -in) in="$2" ;; -out) out="$2" ;; esac; shift 2
done
echo "$cmd" >> "$SDMAINT_CALLS"
[ -f "$key" ] || exit 1
case "$cmd" in
offsite-seal) [ -n "${SEAL_FAIL:-}" ] && exit 1; { printf 'SEALED\n'; cat "$in"; } > "$out" ;;
offsite-open) [ "$(head -n1 "$in")" = SEALED ] || exit 1; tail -n +2 "$in" > "$out" ;;
*) exit 1 ;;
esac
STUB
chmod +x "$W/bin/gh" "$W/bin/sdmaint"

. "$(pwd)/ops/lib-offsite-gh.sh"
gh_offsite_sdmaint() { echo "$W/bin/sdmaint"; }
gh_offsite_newest_tag() { echo backup-20261022-131000; }

pass=0; fail=0
ok()   { printf '  ok    %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }

SRC="$W/backups/signaldeck-20261022-131000.db"; head -c 300000 /dev/urandom > "$SRC"
TMPGZ="$W/backups/.gh-20261022-131000.db.gz"; TAG=backup-20261022-131000
ENC=signaldeck-20261022-131000.db.gz.enc
reset() {
    rm -f "$REMOTE"/*; : > "$GH_CALLS"; : > "$SDMAINT_CALLS"
    unset SEAL_FAIL SIGNALDECK_OFFSITE_KEY_FILE SIGNALDECK_TOMBSTONE_LOG
}
up() { gh_offsite_upload_verified "$SRC" o/r "$TAG" "$TMPGZ" 2>"$W/err"; rc=$?; }
remote() { ls "$REMOTE" | sort | tr '\n' ' '; }
uploads() { grep -c '^release upload' "$GH_CALLS"; }
left() { ls "$W/backups" | wc -l | tr -d ' '; }
sealed_upload() {
    reset; : > "$W/backup.key"
    printf '{"uid":5,"email_sha256":"%s"}\n' "$(printf 'a%.0s' $(seq 64))" > "$W/deletions.jsonl"
    export SIGNALDECK_OFFSITE_KEY_FILE="$W/backup.key" SIGNALDECK_TOMBSTONE_LOG="$W/deletions.jsonl"
    up
}
dl() { out=$(gh_offsite_download_newest o/r "$1" 2>"$W/err"); rc=$?; }

echo "-- sealed upload ------------------------------------------------------"

# a. Key configured, seal fails: nothing is uploaded, no temp file is left.
reset; : > "$W/backup.key"; export SIGNALDECK_OFFSITE_KEY_FILE="$W/backup.key" SEAL_FAIL=1
up
check "seal failure returns 1" "$rc" "1"
check "seal failure says it refused plaintext" "$(printf '%s' "$GH_FAIL_REASON" | grep -c refusing)" "1"
check "seal failure uploads nothing" "$(uploads)" "0"
check "seal failure leaves the remote empty" "$(remote)" ""
check "seal failure leaves no temp file" "$(left)" "1"

# b. A configured key file that is missing fails closed the same way.
reset; export SIGNALDECK_OFFSITE_KEY_FILE="$W/missing.key"
up
check "missing key file returns 1" "$rc" "1"
check "missing key file uploads nothing" "$(uploads)" "0"

# c. Sealed upload: only .enc assets, sidecar hashes the .enc, temp files gone.
sealed_upload
check "sealed upload returns 0" "$rc" "0"
check "sealed upload sends only sealed assets" "$(remote)" "deletions.jsonl.enc $ENC $ENC.sha256 "
check "no plaintext .db.gz reached the remote" "$(ls "$REMOTE" | grep -c '\.db\.gz$')" "0"
check "the remote backup is the sealed file" "$(head -n1 "$REMOTE/$ENC")" "SEALED"
check "sidecar hash is the .enc's hash" "$(awk '{print $1}' "$REMOTE/$ENC.sha256")" "$(sha256sum "$REMOTE/$ENC" | awk '{print $1}')"
check "sidecar names the .enc" "$(awk '{print $2}' "$REMOTE/$ENC.sha256")" "$ENC"
check "sealed upload leaves no temp file" "$(left)" "1"

# d. No key: today's plaintext upload, with a WARNING, and sdmaint never runs.
reset
up
check "plaintext upload returns 0" "$rc" "0"
check "plaintext upload sends .db.gz + sidecar" "$(remote)" "signaldeck-20261022-131000.db.gz signaldeck-20261022-131000.db.gz.sha256 "
check "plaintext upload warns" "$(grep -c WARNING "$W/err")" "1"
check "plaintext upload never calls sdmaint" "$(wc -l < "$SDMAINT_CALLS" | tr -d ' ')" "0"

echo "-- sealed download ----------------------------------------------------"

# e. The download opens what the sealed upload sent, tombstone log included.
sealed_upload; check "setup upload (e)" "$rc" "0"
dl "$W/dl-e"
check "sealed download returns 0" "$rc" "0"
check "sealed download prints the .db.gz" "$out" "$W/dl-e/signaldeck-20261022-131000.db.gz"
check "opened backup decompresses to the source" "$(gzip -dc "$out" | cmp -s - "$SRC" && echo same)" "same"
check "tombstone log opened beside it" "$(cmp -s "$W/dl-e/deletions.jsonl" "$W/deletions.jsonl" && echo same)" "same"
check "the .enc is removed after opening" "$([ -e "$W/dl-e/$ENC" ] && echo left || echo gone)" "gone"

# f. A tampered sidecar is refused before any decryption runs.
sealed_upload; check "setup upload (f)" "$rc" "0"
printf '%s  %s\n' "$(printf '0%.0s' $(seq 64))" "$ENC" > "$REMOTE/$ENC.sha256"; : > "$SDMAINT_CALLS"
dl "$W/dl-f"
check "tampered sidecar is refused" "$([ "$rc" -ne 0 ] && echo refused)" "refused"
check "tampered sidecar prints no path" "$out" ""
check "tampered sidecar never reaches offsite-open" "$(grep -c offsite-open "$SDMAINT_CALLS")" "0"

# g. A sealed asset with no key configured at restore time is refused.
sealed_upload; check "setup upload (g)" "$rc" "0"
unset SIGNALDECK_OFFSITE_KEY_FILE
dl "$W/dl-g"
check "sealed asset without a key is refused" "$([ "$rc" -ne 0 ] && echo refused)" "refused"
check "and says no backup key is configured" "$(grep -c 'no backup key' "$W/err")" "1"

echo "----------------------------------------------------------------------"
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ] && echo "OFFSITE SEAL SELF-TEST OK"
exit "$fail"
