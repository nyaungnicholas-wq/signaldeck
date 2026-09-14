#!/usr/bin/env bash
# Does NEXT_PUBLIC_SIGNALDECK_PUBLIC actually change what an anonymous visitor
# gets? Asserted against the BUILT BUNDLE, not against the Dockerfile's text.
#
# WHY. The flag is documented in .env.example and DEPLOY.md and read in two
# places (AuthGate.tsx picks the redirect target for a 401; proof/page.tsx
# suppresses operator-only remediation copy). Until 2026-09-13 the Dockerfile
# had no ARG for it, so every container image shipped in PRIVATE mode whatever
# the operator set -- DEPLOY.md was instructing something that could not work.
#
# A test that grepped the Dockerfile for the string would have passed the moment
# the ARG line was added and told you nothing about whether the value reaches
# the browser. NEXT_PUBLIC_* is inlined by the compiler, so the only honest
# check is what came out the other end.
#
# MEASURED 2026-09-13, and the reason the assertion is shaped the way it is:
#
#   flag unset:  401===a.status?window.location.replace("1"===t.default.env.NEXT_PUBLIC_SIGNALDECK_PU...
#   flag = 1:    401===t.status?window.location.replace("/")
#
# Unset, the compiler leaves a RUNTIME lookup. `process.env` is not populated in
# the browser, so the comparison is against undefined, it is false forever, and
# every anonymous visitor goes to /login. That is the defect, and it is invisible
# in source -- the source looks conditional.
#
# Builds into web/.next-test via SIGNALDECK_DIST_DIR so it never disturbs
# web/.next or the running instances.
#
# Run: bash ops/test-public-profile.sh
set -uo pipefail

SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB="$SD/web"
DIST=".next-test"
OUT="$WEB/$DIST"

fail=0
ok()   { echo "  PASS  $1"; }
bad()  { echo "  FAIL  $1"; fail=$((fail+1)); }

cleanup() { rm -rf "$OUT"; }
trap cleanup EXIT

build_with() {
  local value="$1"
  rm -rf "$OUT"
  ( cd "$WEB" && SIGNALDECK_DIST_DIR="$DIST" NEXT_PUBLIC_SIGNALDECK_PUBLIC="$value" \
      npm run build >/tmp/sd-profile-build.log 2>&1 )
  local rc=$?
  if [ $rc -ne 0 ]; then
    echo "build with NEXT_PUBLIC_SIGNALDECK_PUBLIC=$value FAILED (exit $rc)"
    tail -20 /tmp/sd-profile-build.log
    exit 2
  fi
}

# Everything the compiler emitted for the client, as one blob.
chunks() { cat "$OUT"/static/chunks/*.js 2>/dev/null; }

echo "1. public profile (NEXT_PUBLIC_SIGNALDECK_PUBLIC=1)"
build_with 1
blob="$(chunks)"
if grep -q 'location.replace("/")' <<<"$blob"; then
  ok 'an anonymous 401 redirects to "/" -- the front door'
else
  bad 'the public build does not redirect an anonymous visitor to "/"'
fi
if grep -q 'NEXT_PUBLIC_SIGNALDECK_PUBLIC' <<<"$blob"; then
  bad 'the flag survived as a RUNTIME lookup; process.env is empty in the browser, so it can never be true'
else
  ok 'the flag was inlined at build time, not left for the browser to resolve'
fi

echo "2. private profile (NEXT_PUBLIC_SIGNALDECK_PUBLIC=0)"
build_with 0
blob="$(chunks)"
if grep -q 'location.replace("/login")' <<<"$blob"; then
  ok 'an anonymous 401 redirects to "/login" -- gated, as a private host should be'
else
  bad 'the private build does not send an anonymous visitor to /login'
fi

echo "3. the two profiles differ"
# The whole point. If these ever compile identically the flag is decorative
# again, whatever the Dockerfile says.
build_with 1; a="$(chunks | grep -c 'location.replace("/")')"
build_with 0; b="$(chunks | grep -c 'location.replace("/")')"
if [ "$a" -gt "$b" ]; then
  ok "public build folds the redirect to \"/\" ($a site(s)) where private does not ($b)"
else
  bad "public and private builds produced the same redirect -- the flag changes nothing"
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "test-public-profile: FAILED ($fail)"
  exit 1
fi
echo "test-public-profile: OK - 4 assertions passed"
exit 0
