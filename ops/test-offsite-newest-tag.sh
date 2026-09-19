#!/bin/bash
# Self-test for gh_offsite_newest_tag, in the shape ops/test-docker-build.sh
# already uses: `gh` is stubbed on PATH so the question is what the function
# DECIDES, not whether GitHub answers.
#
# This exists because of a real outage. On 2026-09-17 the nightly upload got
# HTTP 500 from uploads.github.com AFTER creating the release, so the tag was
# left carrying zero assets. The old implementation took the newest backup-*
# tag without looking at its assets, and `gh release download` on an assetless
# release exits 1 -- so every --from-github restore failed for about a day
# while a perfectly good backup sat in the release directly below it.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir -p "$W/bin"

# The stub emulates the two gh calls the function makes, INCLUDING the --jq
# filter to backup-* tags. Emulating that filter matters: without it, case (f)
# would pass for the wrong reason -- the non-backup tag being skipped for
# having no assets rather than never being considered at all.
cat > "$W/bin/gh" <<'STUB'
#!/bin/bash
if [ "${2:-}" = "list" ]; then
    printf '%s\n' "${RELEASES:-}" | while read -r created tag; do
        case "${tag:-}" in backup-*) printf '%s %s\n' "$created" "$tag" ;; esac
    done
elif [ "${2:-}" = "view" ]; then
    var="ASSETS_$(printf '%s' "${3:-}" | sed 's/[^[:alnum:]]/_/g')"
    printf '%s\n' "${!var:-0}"
else
    exit 1
fi
STUB
chmod +x "$W/bin/gh"
export PATH="$W/bin:$PATH"

. "$(pwd)/ops/lib-offsite-gh.sh"

pass=0; fail=0
ok()   { printf '  ok    %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }

# The stub is a separate PROCESS, so every fixture has to be exported. Setting
# them as plain shell variables leaves the stub blind and every case returns
# empty -- which silently PASSES the two cases that expect empty. That failure
# mode is why reset_fixtures() unsets everything between cases.
reset_fixtures() {
    unset "${!ASSETS_@}" 2>/dev/null
    export RELEASES=""
}

echo "-- gh_offsite_newest_tag ----------------------------------------------"

# a. The ordinary case: newest release carries a backup, so take it.
reset_fixtures
export RELEASES=$'2026-09-17T12:00:00Z backup-20260917-120000\n2026-09-18T13:10:09Z backup-20260918-131009'
export ASSETS_backup_20260917_120000=1 ASSETS_backup_20260918_131009=1
check "newest release has assets" "$(gh_offsite_newest_tag o/r)" "backup-20260918-131009"

# b. THE REGRESSION. Newest tag exists but carries nothing: the 2026-09-17
#    shape exactly. Must fall through to the release that actually has a backup.
reset_fixtures
export RELEASES=$'2026-09-17T12:00:00Z backup-20260917-120000\n2026-09-18T13:10:09Z backup-20260918-131009'
export ASSETS_backup_20260917_120000=1 ASSETS_backup_20260918_131009=0
check "newest release lacks assets (regression)" "$(gh_offsite_newest_tag o/r)" "backup-20260917-120000"

# c. Two failed nights in a row still has to reach a good copy.
reset_fixtures
export RELEASES=$'2026-09-16T10:00:00Z backup-20260916-100000\n2026-09-17T11:00:00Z backup-20260917-110000\n2026-09-18T12:00:00Z backup-20260918-120000'
export ASSETS_backup_20260916_100000=1 ASSETS_backup_20260917_110000=0 ASSETS_backup_20260918_120000=0
check "two assetless tops then asset" "$(gh_offsite_newest_tag o/r)" "backup-20260916-100000"

# d. Nothing usable anywhere: return empty so the caller fails CLOSED rather
#    than handing a restore a tag it cannot download.
reset_fixtures
export RELEASES=$'2026-09-16T10:00:00Z backup-20260916-100000\n2026-09-17T11:00:00Z backup-20260917-110000'
export ASSETS_backup_20260916_100000=0 ASSETS_backup_20260917_110000=0
check "all releases assetless" "$(gh_offsite_newest_tag o/r)" ""

# e. Empty repo.
reset_fixtures
check "no releases" "$(gh_offsite_newest_tag o/r)" ""

# f. A newer non-backup tag must be filtered out, not merely skipped.
reset_fixtures
export RELEASES=$'2026-09-18T13:10:09Z backup-20260918-131009\n2026-09-17T12:00:00Z backup-20260917-120000\n2026-09-19T00:00:00Z v2.0.0'
export ASSETS_backup_20260917_120000=1 ASSETS_backup_20260918_131009=1 ASSETS_v2_0_0=1
check "non-backup newer tag ignored" "$(gh_offsite_newest_tag o/r)" "backup-20260918-131009"

echo ""
echo "offsite-newest-tag self-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
