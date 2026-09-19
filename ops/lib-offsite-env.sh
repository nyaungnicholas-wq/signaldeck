# offsite-env: load SIGNALDECK_OFFSITE_S3 and SIGNALDECK_OFFSITE_DIR from daemon/.env
# 2026-09-01: nightly backup script missed offsite copy because scheduled task
# did not source daemon/.env, leaving vars unset. This library makes .env the
# single source of truth: the backup script should prepend:
#   . "$(dirname "$0")/lib-offsite-env.sh" && sd_offsite_env_from_dotenv "$SD/daemon/.env"
# Behavior: environment wins, .env fills gaps, values are never logged.

sd_offsite_env_from_dotenv() {
    local dotenv_path="$1" name line value
    if [[ ! -f "$dotenv_path" ]]; then
        printf 'offsite-env: %s absent\n' "$dotenv_path" >&2
        for name in SIGNALDECK_OFFSITE_S3 SIGNALDECK_OFFSITE_DIR SIGNALDECK_OFFSITE_GH_REPO; do
            printf 'offsite-env: %s unset\n' "$name" >&2
        done
        return 0
    fi
    for name in SIGNALDECK_OFFSITE_S3 SIGNALDECK_OFFSITE_DIR SIGNALDECK_OFFSITE_GH_REPO; do
        if [[ -n "${!name:-}" ]]; then
            printf 'offsite-env: %s from env\n' "$name" >&2
            continue
        fi
        # first uncommented NAME= line (an `export NAME=` form is accepted); the
        # value is everything after the first '=', minus CR, edge whitespace and
        # one pair of matching quotes. No sed: an ERE pattern without -E silently
        # yielded the whole line as the value (2026-09-01 draft).
        line=$(grep -m1 -E "^[[:space:]]*(export[[:space:]]+)?${name}[[:space:]]*=" "$dotenv_path" 2>/dev/null || true)
        value="${line#*=}"
        value="${value%$'\r'}"
        value="${value%"${value##*[![:space:]]}"}"
        value="${value#"${value%%[![:space:]]*}"}"
        if [[ ${#value} -ge 2 && ( ( "${value:0:1}" == '"' && "${value: -1}" == '"' ) || ( "${value:0:1}" == "'" && "${value: -1}" == "'" ) ) ]]; then
            value="${value:1:${#value}-2}"
        fi
        if [[ -n "$value" ]]; then
            export "$name=$value"
            printf 'offsite-env: %s from dotenv\n' "$name" >&2
        else
            printf 'offsite-env: %s unset\n' "$name" >&2
        fi
    done
    return 0
}

if [ "${BASH_SOURCE[0]}" = "$0" ] && [ "$#" -eq 1 ] && [ "$1" = "--selftest" ]; then
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    # case1: neither set, both from .env
    (
        unset SIGNALDECK_OFFSITE_S3 SIGNALDECK_OFFSITE_DIR OTHER
        dotenv="$tmpdir/.env"
        printf '# SIGNALDECK_OFFSITE_S3=s3://commented-out\r\n' > "$dotenv"
        printf 'SIGNALDECK_OFFSITE_S3="s3://bucket-a/prefix"\r\n' >> "$dotenv"
        printf 'SIGNALDECK_OFFSITE_DIR=E:\SignalDeckBackups\r\n' >> "$dotenv"
        printf 'OTHER=1\r\n' >> "$dotenv"
        sd_offsite_env_from_dotenv "$dotenv"
        if [ "$SIGNALDECK_OFFSITE_S3" != 's3://bucket-a/prefix' ] || [ "$SIGNALDECK_OFFSITE_DIR" != 'E:\SignalDeckBackups' ] || [ -n "${OTHER:-}" ]; then
            exit 1
        fi
    ) 2>"$tmpdir/stderr1"
    if [ $? -ne 0 ] || grep -q 'bucket-a\|preset' "$tmpdir/stderr1"; then
        echo "FAIL: case1" >&2
        exit 1
    fi

    # case2: SIGNALDECK_OFFSITE_S3 preset, environment wins
    (
        unset SIGNALDECK_OFFSITE_S3 SIGNALDECK_OFFSITE_DIR OTHER
        export SIGNALDECK_OFFSITE_S3='s3://preset'
        dotenv="$tmpdir/.env"
        printf '# SIGNALDECK_OFFSITE_S3=s3://commented-out\r\n' > "$dotenv"
        printf 'SIGNALDECK_OFFSITE_S3="s3://bucket-a/prefix"\r\n' >> "$dotenv"
        printf 'SIGNALDECK_OFFSITE_DIR=E:\SignalDeckBackups\r\n' >> "$dotenv"
        printf 'OTHER=1\r\n' >> "$dotenv"
        sd_offsite_env_from_dotenv "$dotenv"
        if [ "$SIGNALDECK_OFFSITE_S3" != 's3://preset' ] || [ "$SIGNALDECK_OFFSITE_DIR" != 'E:\SignalDeckBackups' ]; then
            exit 1
        fi
    ) 2>"$tmpdir/stderr2"
    if [ $? -ne 0 ] || grep -q 'bucket-a\|preset' "$tmpdir/stderr2"; then
        echo "FAIL: case2" >&2
        exit 1
    fi

    # case3: .env absent -> both unset, absent line printed
    (
        unset SIGNALDECK_OFFSITE_S3 SIGNALDECK_OFFSITE_DIR OTHER
        dotenv="$tmpdir/nonexistent.env"
        sd_offsite_env_from_dotenv "$dotenv"
        if [ -n "${SIGNALDECK_OFFSITE_S3:-}" ] || [ -n "${SIGNALDECK_OFFSITE_DIR:-}" ]; then
            exit 1
        fi
    ) 2>"$tmpdir/stderr3"
    if [ $? -ne 0 ] || ! grep -q "offsite-env: $tmpdir/nonexistent.env absent" "$tmpdir/stderr3" || grep -q 'bucket-a\|preset' "$tmpdir/stderr3"; then
        echo "FAIL: case3" >&2
        exit 1
    fi

    # case4: ensure OTHER not leaked after case1
    (
        unset SIGNALDECK_OFFSITE_S3 SIGNALDECK_OFFSITE_DIR OTHER
        dotenv="$tmpdir/.env"
        printf '# SIGNALDECK_OFFSITE_S3=s3://commented-out\r\n' > "$dotenv"
        printf 'SIGNALDECK_OFFSITE_S3="s3://bucket-a/prefix"\r\n' >> "$dotenv"
        printf 'SIGNALDECK_OFFSITE_DIR=E:\SignalDeckBackups\r\n' >> "$dotenv"
        printf 'OTHER=1\r\n' >> "$dotenv"
        sd_offsite_env_from_dotenv "$dotenv"
        if [ -n "${OTHER:-}" ]; then
            exit 1
        fi
    ) 2>"$tmpdir/stderr4"
    if [ $? -ne 0 ] || grep -q 'bucket-a\|preset' "$tmpdir/stderr4"; then
        echo "FAIL: case4" >&2
        exit 1
    fi

    echo "PASS: offsite env from dotenv (4 cases)"
    exit 0
fi