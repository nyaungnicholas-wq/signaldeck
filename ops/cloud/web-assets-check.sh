#!/bin/bash
# Linux port of ops/web-assets-check.ps1
# Same exit codes: 0 complete, 1 incomplete, 2 unavailable
# Requires: bash 4+, curl, grep -oE, sort, mktemp

set -u

PORT=8323
PATH_ARG='/'
TIMEOUT=20
QUIET=0

usage() {
    cat >&2 <<'EOF'
Usage: web-assets-check.sh [-p PORT] [--path PATH] [--timeout SEC] [--quiet]
Defaults: PORT=8323, PATH=/, SEC=20
EOF
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) PORT="$2"; shift 2 ;;
        --path) PATH_ARG="$2"; shift 2 ;;
        --timeout) TIMEOUT="$2"; shift 2 ;;
        --quiet) QUIET=1; shift ;;
        *) usage ;;
    esac
done

BASE="http://127.0.0.1:${PORT}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

say() {
    [[ $QUIET -eq 0 ]] && printf '%s\n' "$1"
}

# --- 1. fetch the page ---
HTTP_CODE=$(curl -s --noproxy '*' -m "$TIMEOUT" -o "$WORK/page" -w '%{http_code}' "${BASE}${PATH_ARG}" 2>/dev/null)
CURL_EXIT=$?

if [[ $CURL_EXIT -ne 0 || ! $HTTP_CODE =~ ^[0-9]+$ || $HTTP_CODE -lt 200 || $HTTP_CODE -ge 400 ]]; then
    if [[ $CURL_EXIT -ne 0 ]]; then
        printf 'web-assets: UNAVAILABLE %s%s did not answer (curl exit %d)\n' "$BASE" "$PATH_ARG" "$CURL_EXIT"
    else
        printf 'web-assets: UNAVAILABLE %s%s -> HTTP %s\n' "$BASE" "$PATH_ARG" "$HTTP_CODE"
    fi
    exit 2
fi

# Check for empty/whitespace body
if [[ ! -s "$WORK/page" ]] || [[ -z "$(cat "$WORK/page" | tr -d '[:space:]')" ]]; then
    printf 'web-assets: UNAVAILABLE %s%s answered %s with an empty body\n' "$BASE" "$PATH_ARG" "$HTTP_CODE"
    exit 2
fi

# --- 2. collect unique asset refs from page ---
mapfile -t REFS < <(grep -oE '/_next/static/[A-Za-z0-9_./-]+\.(css|js)' "$WORK/page" | sort -u)

if [[ ${#REFS[@]} -eq 0 ]]; then
    printf 'web-assets: FAIL %s%s referenced no /_next/static assets at all\n' "$BASE" "$PATH_ARG"
    exit 1
fi

# --- 3. fetch each ref, validate, collect font refs from CSS ---
declare -A EXPECTED
for r in "${REFS[@]}"; do
    if [[ "$r" == *.css ]]; then
        EXPECTED["$r"]='text/css'
    else
        EXPECTED["$r"]='javascript'
    fi
done

BAD=()
CHECKED=0
FONT_REFS=()
declare -A FONT_SEEN

for url in "${REFS[@]}"; do
    CHECKED=$((CHECKED + 1))
    RESPONSE=$(curl -s --noproxy '*' -m "$TIMEOUT" -o "$WORK/asset" -w '%{http_code} %{content_type}' "${BASE}${url}" 2>/dev/null)
    CURL_EXIT=$?

    HTTP_CODE="${RESPONSE%% *}"
    CONTENT_TYPE="${RESPONSE#* }"

    if [[ $CURL_EXIT -ne 0 || ! $HTTP_CODE =~ ^[0-9]+$ || $HTTP_CODE -lt 200 || $HTTP_CODE -ge 400 ]]; then
        CODE=0
        if [[ $CURL_EXIT -eq 0 ]]; then
            CODE="$HTTP_CODE"
        fi
        BAD+=("${url} -> HTTP ${CODE}")
        continue
    fi

    EXPECTED_FRAG="${EXPECTED[$url]}"
    if [[ "$CONTENT_TYPE" != *"$EXPECTED_FRAG"* ]]; then
        BAD+=("${url} -> 200 but Content-Type '${CONTENT_TYPE}', expected ${EXPECTED_FRAG}")
        continue
    fi

    if [[ "$url" == *.css ]]; then
        while IFS= read -r font_match; do
            # font_match looks like: url(../media/name.woff2)
            # Extract the name part
            name="${font_match#url(../media/}"
            name="${name%)}"
            font_url="/_next/static/media/${name}"
            if [[ -z "${FONT_SEEN[$font_url]:-}" ]]; then
                FONT_SEEN["$font_url"]=1
                FONT_REFS+=("$font_url")
            fi
        done < <(grep -oE 'url\(\.\./media/[A-Za-z0-9_.-]+\)' "$WORK/asset" | sort -u)
    fi
done

# --- 4. fetch each font ref ---
for font in "${FONT_REFS[@]}"; do
    CHECKED=$((CHECKED + 1))
    HTTP_CODE=$(curl -s --noproxy '*' -m "$TIMEOUT" -o /dev/null -w '%{http_code}' "${BASE}${font}" 2>/dev/null)
    CURL_EXIT=$?

    if [[ $CURL_EXIT -ne 0 || ! $HTTP_CODE =~ ^[0-9]+$ || $HTTP_CODE -lt 200 || $HTTP_CODE -ge 400 ]]; then
        CODE=0
        if [[ $CURL_EXIT -eq 0 ]]; then
            CODE="$HTTP_CODE"
        fi
        BAD+=("${font} -> HTTP ${CODE} (font)")
    fi
done

# --- 5/6. verdict ---
if [[ ${#BAD[@]} -gt 0 ]]; then
    printf 'web-assets: FAIL port %s %s -- %d of %d assets unserved\n' "$PORT" "$PATH_ARG" "${#BAD[@]}" "$CHECKED"
    for b in "${BAD[@]}"; do
        printf '  %s\n' "$b"
    done
    printf '  The page answered 200 while these did not. A status-code probe cannot see this.\n'
    printf '  Usual cause: this instance predates the current web/.next build. Restart it.\n'
    exit 1
fi

N_CHUNK=${#REFS[@]}
N_FONT=${#FONT_REFS[@]}

if [[ $QUIET -eq 1 ]]; then
    printf 'web-assets: OK port %s (%d assets)\n' "$PORT" "$CHECKED"
else
    printf 'web-assets: OK port %s %s -- %d assets (%d chunk, %d font) all served\n' "$PORT" "$PATH_ARG" "$CHECKED" "$N_CHUNK" "$N_FONT"
fi
exit 0