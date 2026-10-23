#!/bin/bash
# Drives ops/cloud/web-assets-check.sh against a throwaway static server and
# asserts all three verdicts: 0 complete, 1 an asset is missing, 2 page down.
# Needs python3 (or SD_PYTHON) for http.server; nothing else is touched.
set -u
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
py="${SD_PYTHON:-python3}"
tmp="$(mktemp -d)"
srv=""
cleanup() { [ -n "$srv" ] && kill "$srv" 2>/dev/null; rm -rf "$tmp"; }
trap cleanup EXIT
fails=0
expect() { # NAME WANT GOT
  if [ "$2" = "$3" ]; then echo "  ok   $1"; else echo "  FAIL $1 (want exit $2, got $3)"; fails=$((fails + 1)); fi
}

mkdir -p "$tmp/www/_next/static/chunks" "$tmp/www/_next/static/css" "$tmp/www/_next/static/media"
cat > "$tmp/www/index.html" <<'HTML'
<html><head><link rel="stylesheet" href="/_next/static/css/app.css"></head>
<body><script src="/_next/static/chunks/main.js"></script><script src="/_next/static/chunks/main.js"></script></body></html>
HTML
printf '@font-face{src:url(../media/face.woff2)}' > "$tmp/www/_next/static/css/app.css"
printf 'console.log(1)' > "$tmp/www/_next/static/chunks/main.js"
printf 'x' > "$tmp/www/_next/static/media/face.woff2"
printf '<html><body>no assets</body></html>' > "$tmp/www/bare.html"

port=$(( 20000 + RANDOM % 20000 ))
(cd "$tmp/www" && exec "$py" -m http.server "$port" --bind 127.0.0.1 >/dev/null 2>&1) &
srv=$!
for _ in $(seq 1 50); do
  curl -s -o /dev/null "http://127.0.0.1:$port/index.html" && break
  sleep 0.2
done

bash "$here/web-assets-check.sh" -p "$port" --path /index.html >/dev/null; expect "complete build passes" 0 $?
bash "$here/web-assets-check.sh" -p "$port" --path /bare.html >/dev/null; expect "page with no assets fails" 1 $?
rm "$tmp/www/_next/static/media/face.woff2"
bash "$here/web-assets-check.sh" -p "$port" --path /index.html >/dev/null; expect "missing font fails" 1 $?
rm "$tmp/www/_next/static/chunks/main.js"
bash "$here/web-assets-check.sh" -p "$port" --path /index.html >/dev/null; expect "missing chunk fails" 1 $?
kill "$srv" 2>/dev/null; wait "$srv" 2>/dev/null; srv=""
bash "$here/web-assets-check.sh" -p "$port" --path /index.html --timeout 3 >/dev/null; expect "page down is UNAVAILABLE" 2 $?

[ "$fails" -eq 0 ] || { echo "web-assets-check selftest: $fails FAILED"; exit 1; }
echo "web-assets-check selftest OK"
