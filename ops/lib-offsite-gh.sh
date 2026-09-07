#!/bin/bash
# lib-offsite-gh.sh - GitHub release‑asset offsite backup helpers for SignalDeck
# Sources: ops/signaldeck-backup-offline.sh, ops/restore-rehearsal.sh
# Requires: bash, coreutils, gzip, gh (GitHub CLI, already authenticated)
# set -u safe; never prints tokens.

set -u

# gh_offsite_file_size PATH -> prints bytes (0 on failure)
gh_offsite_file_size() {
    local path="$1"
    local size
    if [[ -f "$path" ]]; then
        size=$(stat -c%s "$path" 2>/dev/null) || size=$(wc -c <"$path" 2>/dev/null) || size=0
    else
        size=0
    fi
    printf '%s\n' "$size"
}

# gh_offsite_upload_verified SRC REPO TAG TMPGZ
# Returns 0 on verified success, 1 on failure.
# Sets GH_VERIFIED_BYTES (0 unless verified) and GH_FAIL_REASON ("" on success).
gh_offsite_upload_verified() {
    local src="$1" repo="$2" tag="$3" tmpgz="$4"
    local gz_bytes name gzpath hash sha256path remote

    # compress
    if ! gzip -c "$src" >"$tmpgz" 2>>"${LOG:-/dev/null}"; then
        GH_FAIL_REASON="could not compress the backup for upload"
        return 1
    fi

    gz_bytes=$(gh_offsite_file_size "$tmpgz")
    if (( gz_bytes == 0 )); then
        GH_FAIL_REASON="compressed to 0 bytes"
        return 1
    fi
    if (( gz_bytes > 2000000000 )); then
        GH_FAIL_REASON="compressed backup is $gz_bytes bytes, over GitHub's 2 GB asset cap"
        return 1
    fi

    name=$(basename "$src").gz
    gzpath=$(dirname "$tmpgz")/$name
    mv "$tmpgz" "$gzpath"

    # ensure release exists
    if ! gh release view "$tag" --repo "$repo" >/dev/null 2>>"${LOG:-/dev/null}"; then
        if ! gh release create "$tag" --repo "$repo" \
            --title "SignalDeck DB backup $tag" \
            --notes "Automated off-machine backup of data/signaldeck.db (gzip). Private repo. Restore: gh release download $tag --repo $repo." >/dev/null 2>>"${LOG:-/dev/null}"; then
            GH_FAIL_REASON="could not create release $tag"
            rm -f "$gzpath"
            return 1
        fi
    fi

    # upload asset
    if ! gh release upload "$tag" "$gzpath" --repo "$repo" --clobber >/dev/null 2>>"${LOG:-/dev/null}"; then
        GH_FAIL_REASON="gh release upload failed"
        rm -f "$gzpath"
        return 1
    fi

    # sidecar SHA256
    hash=$(sha256sum "$gzpath" | awk '{print $1}')
    sha256path="${gzpath}.sha256"
    printf '%s  %s\n' "$hash" "$name" >"$sha256path"
    if ! gh release upload "$tag" "$sha256path" --repo "$repo" --clobber >/dev/null 2>>"${LOG:-/dev/null}"; then
        GH_FAIL_REASON="sidecar upload failed"
        rm -f "$gzpath" "$sha256path"
        return 1
    fi

    # verify remote size
    remote=$(gh api "repos/$repo/releases/tags/$tag" --jq ".assets[] | select(.name==\"$name\") | .size" 2>>"${LOG:-/dev/null}") || remote=0
    if [[ ! "$remote" =~ ^[0-9]+$ ]]; then
        remote=0
    fi
    if (( remote != gz_bytes )); then
        GH_FAIL_REASON="uploaded $gz_bytes bytes but GitHub reports $remote"
        rm -f "$gzpath" "$sha256path"
        return 1
    fi

    GH_VERIFIED_BYTES=$remote
    GH_FAIL_REASON=""
    rm -f "$gzpath" "$sha256path"
    return 0
}

# gh_offsite_prune REPO KEEP
# Delete backup-* releases beyond the newest KEEP; always returns 0.
gh_offsite_prune() {
    local repo="$1" keep="$2" created tag count=0
    while IFS= read -r created tag; do
        ((count++))
        if (( count > keep )); then
            gh release delete "$tag" --repo "$repo" --yes --cleanup-tag >/dev/null 2>>"${LOG:-/dev/null}"
            echo "gh prune: deleted $tag"
        fi
    done < <(gh release list --repo "$repo" --limit 200 --json tagName,createdAt \
        --jq '.[] | select(.tagName|startswith("backup-")) | "\(.createdAt) \(.tagName)"' 2>>"${LOG:-/dev/null}" | sort -r)
    return 0
}

# gh_offsite_newest_tag REPO
# Prints newest backup-* tag or nothing.
gh_offsite_newest_tag() {
    local repo="$1"
    gh release list --repo "$repo" --limit 200 --json tagName,createdAt \
        --jq '.[] | select(.tagName|startswith("backup-")) | "\(.createdAt) \(.tagName)"' 2>>"${LOG:-/dev/null}" | sort -r | head -n1 | awk '{print $2}'
}

# gh_offsite_download_newest REPO DESTDIR
# Downloads newest backup-* release, verifies sidecar, prints path of .db.gz.
# Returns 0 on success, 1 on failure (message on stderr).
gh_offsite_download_newest() {
    local repo="$1" destdir="$2" tag gzfile base sha256file
    tag=$(gh_offsite_newest_tag "$repo")
    if [[ -z "$tag" ]]; then
        echo "No backup-* release found" >&2
        return 1
    fi
    mkdir -p "$destdir"
    if ! gh release download "$tag" --repo "$repo" --dir "$destdir" \
        --pattern '*.db.gz' --pattern '*.sha256' --clobber >/dev/null 2>>"${LOG:-/dev/null}"; then
        echo "gh release download failed" >&2
        return 1
    fi
    gzfile=$(find "$destdir" -maxdepth 1 -name '*.db.gz' -print -quit)
    if [[ -z "$gzfile" ]]; then
        echo "No .db.gz downloaded" >&2
        return 1
    fi
    base=$(basename "$gzfile")
    sha256file="$destdir/${base}.sha256"
    if [[ ! -f "$sha256file" ]]; then
        echo "Missing sha256 sidecar" >&2
        return 1
    fi
    (cd "$destdir" && sha256sum -c "$(basename "$sha256file")") >/dev/null 2>>"${LOG:-/dev/null}"
    if [[ $? -ne 0 ]]; then
        echo "checksum mismatch" >&2
        return 1
    fi
    printf '%s\n' "$gzfile"
    return 0
}

# Self‑test when executed directly
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    tmpfile=$(mktemp)
    printf 'hello' >"$tmpfile"
    size=$(gh_offsite_file_size "$tmpfile")
    rm -f "$tmpfile"
    if [[ "$size" -eq 5 ]]; then
        echo "lib-offsite-gh selftest OK"
    else
        echo "lib-offsite-gh selftest FAIL" >&2
        exit 1
    fi
fi
