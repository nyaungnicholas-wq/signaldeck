#!/bin/bash
# Self-test for gh_offsite_prune, in the shape of ops/test-offsite-newest-tag.sh:
# `gh` is stubbed on PATH so the question is what the function DECIDES.
#
# This exists because the prune never deleted anything. `IFS= read -r created
# tag` does not split, so tag was always empty; `gh release delete ""` failed and
# the next line printed "gh prune: deleted " regardless. By 2026-10-03 the repo
# held 12 backup releases (11.8 GB) against KEEP 7, one of them assetless.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir -p "$W/bin"

# list: "createdAt tag" lines filtered to backup-* (as the real --jq does);
# view: ASSETS_<tag> asset count; delete: records the tag, fails if FAIL_<tag>.
cat > "$W/bin/gh" <<'STUB'
#!/bin/bash
key() { printf '%s' "$1" | sed 's/[^[:alnum:]]/_/g'; }
case "${2:-}" in
list)
    printf '%s\n' "${RELEASES:-}" | while read -r created tag; do
        case "${tag:-}" in backup-*) printf '%s %s\n' "$created" "$tag" ;; esac
    done ;;
view)
    var="ASSETS_$(key "${3:-}")"; printf '%s\n' "${!var:-0}" ;;
delete)
    [ -n "${3:-}" ] || exit 1
    var="FAIL_$(key "$3")"; [ -n "${!var:-}" ] && exit 1
    printf '%s\n' "$3" >> "$DELETED" ;;
*) exit 1 ;;
esac
STUB
chmod +x "$W/bin/gh"
export PATH="$W/bin:$PATH" DELETED="$W/deleted"

. "$(pwd)/ops/lib-offsite-gh.sh"

pass=0; fail=0
ok()   { printf '  ok    %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }

reset_fixtures() {
    unset "${!ASSETS_@}" "${!FAIL_@}" 2>/dev/null
    export RELEASES=""
    : > "$DELETED"
}
# releases N...: one line per tag, ALL with the same createdAt (as live).
releases() {
    local t lines=""
    for t in "$@"; do lines+="2026-09-17T06:57:11Z $t"$'\n'; done
    export RELEASES="${lines%$'\n'}"
}

echo "-- gh_offsite_prune ---------------------------------------------------"

# a. Nine good releases, KEEP 7: the two OLDEST BY TAG go, even though every
#    createdAt is identical and the list arrives out of order.
reset_fixtures
releases backup-20260921-131000 backup-20260916-131000 backup-20260924-131000 \
    backup-20260918-131000 backup-20260923-131000 backup-20260922-131000 \
    backup-20260925-131000 backup-20260926-131000 backup-20260929-131000
for t in 20260916 20260918 20260921 20260922 20260923 20260924 20260925 20260926 20260929; do
    export "ASSETS_backup_${t}_131000=1"
done
out=$(gh_offsite_prune o/r 7)
check "deletes the two oldest by tag" "$(sort "$DELETED" | tr '\n' ' ')" "backup-20260916-131000 backup-20260918-131000 "
check "reports what it deleted" "$(printf '%s\n' "$out" | grep -c '^gh prune: deleted backup-')" "2"
check "never reports an empty name" "$(printf '%s\n' "$out" | grep -c '^gh prune: deleted $')" "0"

# b. An assetless release neither counts toward KEEP nor is deleted.
reset_fixtures
releases backup-20261001-131127 backup-20260930-131000 backup-20261002-131000 backup-20260929-131000
export ASSETS_backup_20261002_131000=1 ASSETS_backup_20260930_131000=1 ASSETS_backup_20260929_131000=1
out=$(gh_offsite_prune o/r 2)
check "assetless release is not deleted, oldest good one is" "$(tr '\n' ' ' < "$DELETED")" "backup-20260929-131000 "
check "assetless release is reported as kept" "$(printf '%s\n' "$out" | grep -c 'kept backup-20261001-131127')" "1"

# c. A failed delete says so instead of claiming success, and the loop goes on.
reset_fixtures
releases backup-20260929-131000 backup-20260930-131000 backup-20261001-131000 backup-20261002-131000
for t in 20260929 20260930 20261001 20261002; do export "ASSETS_backup_${t}_131000=1"; done
export FAIL_backup_20260930_131000=1
out=$(gh_offsite_prune o/r 2)
check "failed delete is reported as FAILED" "$(printf '%s\n' "$out" | grep -c 'FAILED to delete backup-20260930-131000')" "1"
check "the next one is still deleted" "$(tr '\n' ' ' < "$DELETED")" "backup-20260929-131000 "

# d. At or under KEEP nothing is touched.
reset_fixtures
releases backup-20261001-131000 backup-20261002-131000
export ASSETS_backup_20261001_131000=1 ASSETS_backup_20261002_131000=1
out=$(gh_offsite_prune o/r 7)
check "nothing deleted under KEEP" "$(wc -l < "$DELETED" | tr -d ' ')" "0"

echo "----------------------------------------------------------------------"
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ] && echo "OFFSITE PRUNE SELF-TEST OK"
exit "$fail"
