#!/bin/bash
# Drive s3_upload_verified() with a STUBBED aws CLI and assert both directions.
#
# The point of the function under test is that `aws s3 cp` exiting 0 does not
# prove the object arrived. So the case that matters most here is the one where
# the upload "succeeds" and head-object reports a SHORT object: that must fail,
# because a nightly log reading "offsite OK" over a truncated copy is precisely
# how this repo lost twelve days of disaster recovery once already.
#
# No network, no credentials, no AWS account.
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAILED=0

ok()   { printf '  ok   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; FAILED=1; }

TMPROOT="$(mktemp -d)"
trap 'rm -rf "$TMPROOT"' EXIT
mkdir -p "$TMPROOT/bin"
export PATH="$TMPROOT/bin:$PATH"
LOG="$TMPROOT/log"; export LOG

# Extract just the function under test plus the two helpers it uses, rather than
# sourcing the whole backup script (which would stop the daemon and take a real
# 5 GB backup as a side effect of running a unit test).
sed -n '/^file_size()/,/^}/p;/^sd_nosleep()/,/^}/p;/^s3_upload_verified()/,/^}/p' \
    "$SD/ops/signaldeck-backup-offline.sh" > "$TMPROOT/fns.sh"
# shellcheck disable=SC1090
. "$TMPROOT/fns.sh"
if ! type s3_upload_verified >/dev/null 2>&1; then
  echo "FAIL: could not extract s3_upload_verified from the backup script"; exit 1
fi
type sd_nosleep >/dev/null 2>&1 || sd_nosleep() { "$@"; }

SRC="$TMPROOT/backup.db"
head -c 200000 /dev/urandom > "$SRC"

# stub_aws CP_RC HEAD_OUTPUT — CP_RC is the exit code `aws s3 cp` reports;
# HEAD_OUTPUT is what head-object prints for ContentLength.
stub_aws() {
  cat > "$TMPROOT/bin/aws" <<STUB
#!/bin/bash
if [ "\$1" = "s3" ] && [ "\$2" = "cp" ]; then exit $1; fi
if [ "\$1" = "s3api" ] && [ "\$2" = "head-object" ]; then printf '%s\n' "$2"; exit 0; fi
exit 0
STUB
  chmod +x "$TMPROOT/bin/aws"
}

run() { s3_upload_verified "$SRC" "s3://bucket/nightly/backup.db.gz" "$TMPROOT/t.gz"; }

# The true size only exists after compression, so compute it the same way.
gzip -c "$SRC" > "$TMPROOT/ref.gz"; REAL="$(wc -c < "$TMPROOT/ref.gz" | tr -d ' ')"

# 1. Everything works: cp succeeds AND the remote size matches exactly.
stub_aws 0 "$REAL"
if run; then ok "verified upload is accepted (${S3_VERIFIED_BYTES} bytes)"
else bad "a correct upload was rejected: $S3_FAIL_REASON"; fi

# 2. THE ONE THAT MATTERS. cp exits 0 but the object is SHORT. Trusting the exit
#    status alone passes here; only reading the object back catches it.
stub_aws 0 "$((REAL - 1))"
if run; then bad "a SHORT remote object was accepted — exit 0 was trusted over the artifact"
else ok "short remote object rejected: $S3_FAIL_REASON"; fi

# 3. The object is missing entirely; head-object prints an error string. A
#    non-numeric value must never compare equal to a byte count.
stub_aws 0 "An error occurred (404) when calling the HeadObject operation"
if run; then bad "a MISSING remote object was accepted"
else ok "missing remote object rejected: $S3_FAIL_REASON"; fi

# 4. head-object prints None (a real CLI output for an absent query result).
stub_aws 0 "None"
if run; then bad "ContentLength=None was accepted"
else ok "None rejected: $S3_FAIL_REASON"; fi

# 5. The upload itself fails. Nothing may be reported as verified.
stub_aws 1 "$REAL"
if run; then bad "a FAILED upload was accepted"
else ok "failed upload rejected: $S3_FAIL_REASON"; fi

# 6. The temp file must not be left behind on any path — this runs nightly next
#    to a 5 GB database, and a leaked ~925 MB gz per failure fills the disk.
if [ -e "$TMPROOT/t.gz" ]; then bad "the temp .gz was left behind after a failure"
else ok "temp .gz cleaned up on every path"; fi

if [ "$FAILED" = "0" ]; then echo "test-offsite-s3: PASS"; else echo "test-offsite-s3: FAIL"; fi
exit "$FAILED"
