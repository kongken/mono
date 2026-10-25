#!/usr/bin/env bash
# Container acceptance for the ozid-web image (deploy/ozid-web): builds it for a reserved example
# API base, starts it, and checks what the real Nginx answers: /healthz, the page and its static
# assets, the Content-Security-Policy and X-Frame-Options it sends, and that a missing or illegal
# API_ORIGIN keeps the container from starting. Only reserved example addresses are used.
#
#   scripts/ozid-web-smoke.sh            # needs a working `docker` and curl; run by CI (ozid-web-image)
#   IMAGE=ozid-web:dev SKIP_BUILD=1 scripts/ozid-web-smoke.sh   # test an image that is already built
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${IMAGE:-ozid-web:smoke}"
API_BASE="https://api.example.com/api"
API_ORIGIN="https://api.example.com"
OTHER_ORIGIN="https://api.example.org:8443" # a second valid origin: the same image, other config
tmp="$(mktemp -d)"
containers=()
failures=0

cleanup() {
  for c in "${containers[@]:-}"; do [ -n "$c" ] && docker rm -f "$c" >/dev/null 2>&1 || true; done
  rm -rf "$tmp"
}
trap cleanup EXIT

pass() { echo "ok   $*"; }
fail() { echo "FAIL $*" >&2; failures=$((failures + 1)); }
check() { # check <description> <command...>
  local what="$1"
  shift
  if "$@"; then pass "$what"; else fail "$what"; fi
}

# --- build ---------------------------------------------------------------------------------
if [ "${SKIP_BUILD:-}" != "1" ]; then
  docker build -f deploy/ozid-web/Dockerfile --build-arg "VITE_API_BASE_URL=$API_BASE" -t "$IMAGE" .
fi
# The image has no default API address: a build without one, or with a same-origin /api, must fail.
if [ "${SKIP_BUILD:-}" != "1" ] && [ "${NEGATIVE_BUILDS:-1}" = "1" ]; then
  if docker build -q -f deploy/ozid-web/Dockerfile -t "$IMAGE-nobase" . >"$tmp/nobase.log" 2>&1; then
    fail "a build without VITE_API_BASE_URL must fail"
  else
    pass "a build without VITE_API_BASE_URL fails"
  fi
  if docker build -q -f deploy/ozid-web/Dockerfile --build-arg VITE_API_BASE_URL=/api -t "$IMAGE-relative" . >"$tmp/relative.log" 2>&1; then
    fail "a build with a relative VITE_API_BASE_URL must fail"
  else
    pass "a build with a relative VITE_API_BASE_URL fails"
  fi
fi

# --- run -----------------------------------------------------------------------------------
# start <API_ORIGIN> sets START_URL (not a subshell: the container must reach `containers`).
start() {
  local cid port
  cid="$(docker run -d -p 127.0.0.1::80 -e "API_ORIGIN=$1" "$IMAGE")"
  containers+=("$cid")
  port="$(docker port "$cid" 80/tcp | head -n1 | sed 's/.*://')"
  START_URL="http://127.0.0.1:$port"
  for _ in $(seq 1 60); do
    if curl -fsS "$START_URL/healthz" >/dev/null 2>&1; then return 0; fi
    sleep 0.5
  done
  echo "container did not become ready:" >&2
  docker logs "$cid" >&2 || true
  return 1
}

header() { # header <file> <name>: the value of a response header, case-insensitively, CR removed
  tr -d '\r' <"$1" | awk -v n="$(echo "$2" | tr 'A-Z' 'a-z')" 'BEGIN{FS=": "} tolower($1)==n {sub(/^[^:]*: */, ""); print; exit}'
}
status_is() { [ "$(head -n1 "$1" | awk '{print $2}')" = "$2" ]; }          # status_is <headfile> <code>
header_is() { [ "$(header "$1" "$2")" = "$3" ]; }                          # header_is <headfile> <name> <value>
# header_has <headfile> <name> <substring>: in any line of that header (Nginx sends two Cache-Control
# lines for assets: the one `expires` makes and the immutable one).
header_has() { tr -d '\r' <"$1" | grep -i -- "^$2:" | grep -qi -- "$3"; }
contains() { grep -qF -- "$2" <<<"$1"; }                                   # contains <text> <substring>
no_wildcard() { [[ "$1" != *'*'* ]]; }
get() { # get <url> <name>: writes $tmp/<name>.head and $tmp/<name>.body
  curl -sS -D "$tmp/$2.head" -o "$tmp/$2.body" "$1"
}

start "$API_ORIGIN"
BASE="$START_URL"
echo "ozid-web is up at $BASE for API_ORIGIN=$API_ORIGIN"

# healthz
get "$BASE/healthz" health
check "/healthz answers 200" status_is "$tmp/health.head" 200
check "/healthz says ok" test "$(tr -d '\n' <"$tmp/health.body")" = ok
check "/healthz is plain text" header_has "$tmp/health.head" content-type text/plain

# the page
get "$BASE/" index
check "/ answers 200 with HTML" status_is "$tmp/index.head" 200
check "/ is served as text/html" header_has "$tmp/index.head" content-type text/html
check "/ is the OzID shell" grep -q 'id="root"' "$tmp/index.body"
check "/ is not cached" header_has "$tmp/index.head" cache-control no-store

# the static assets the page names
js="$(grep -o 'src="/assets/[^"]*\.js"' "$tmp/index.body" | head -n1 | sed 's/src="//; s/"$//' || true)"
css="$(grep -o 'href="/assets/[^"]*\.css"' "$tmp/index.body" | head -n1 | sed 's/href="//; s/"$//' || true)"
check "the page references a script" test -n "$js"
check "the page references a stylesheet" test -n "$css"
if [ -n "$js" ]; then
  get "$BASE$js" js
  check "the script answers 200" status_is "$tmp/js.head" 200
  check "the script is served as JavaScript" header_has "$tmp/js.head" content-type javascript
  check "assets are cached as immutable" header_has "$tmp/js.head" cache-control immutable
  check "assets are cached for a long time" header_has "$tmp/js.head" cache-control max-age=31536000
  check "assets are sent with nosniff" header_has "$tmp/js.head" x-content-type-options nosniff
  check "the bundle carries the API base it was built for" grep -qF "$API_BASE" "$tmp/js.body"
fi
if [ -n "$css" ]; then
  get "$BASE$css" css
  check "the stylesheet answers 200" status_is "$tmp/css.head" 200
  check "the stylesheet is served as CSS" header_has "$tmp/css.head" content-type text/css
fi
get "$BASE/assets/does-not-exist.js" missing
check "a missing asset is 404, not the page" status_is "$tmp/missing.head" 404
get "$BASE/favicon.svg" favicon
check "the favicon is served" status_is "$tmp/favicon.head" 200

# the headers real Nginx sends, on the shell and on every route that falls back to it
expect_headers() { # expect_headers <name> <origin>
  local f="$tmp/$1.head" origin="$2" csp
  csp="$(header "$f" content-security-policy)"
  check "$1: X-Frame-Options is DENY" header_is "$f" x-frame-options DENY
  check "$1: CSP lets the page connect to itself and $origin" contains "$csp" "connect-src 'self' $origin;"
  check "$1: CSP script-src is 'self'" contains "$csp" "script-src 'self';"
  check "$1: CSP forbids framing" contains "$csp" "frame-ancestors 'none'"
  check "$1: CSP forbids objects" contains "$csp" "object-src 'none'"
  check "$1: CSP forbids base changes" contains "$csp" "base-uri 'none'"
  check "$1: CSP has no wildcard source" no_wildcard "$csp"
  check "$1: Referrer-Policy is no-referrer" header_is "$f" referrer-policy no-referrer
  check "$1: nosniff" header_is "$f" x-content-type-options nosniff
}
expect_headers index "$API_ORIGIN"
for route in /login /security /index.html; do
  name="route$(printf '%s' "$route" | tr -c 'a-z' '_')"
  get "$BASE$route?app_id=metro&return_path=%2Fcity%2Fshanghai" "$name"
  check "$route answers 200 (single-page fallback)" status_is "$tmp/$name.head" 200
  expect_headers "$name" "$API_ORIGIN"
done
# The mail landing pages (#80): the token lives in the fragment, so these pages must never be
# cached, must not leak a Referer, and may only run same-origin scripts. They are served by the
# explicit location, with a trailing slash too, and they carry the same strict headers.
for route in /account/verify-email /account/reset-password /account/verify-email/ /account/reset-password/; do
  name="landing$(printf '%s' "$route" | tr -c 'a-z' '_')"
  get "$BASE$route" "$name"
  check "$route answers 200 with the shell" status_is "$tmp/$name.head" 200
  check "$route is the OzID shell" grep -q 'id="root"' "$tmp/$name.body"
  check "$route is never cached" header_is "$tmp/$name.head" cache-control no-store
  expect_headers "$name" "$API_ORIGIN"
done
# A fragment never reaches the server, but a token that was put in the query by mistake must not
# be cached or leak either (and is not special-cased: same page, same headers).
get "$BASE/account/reset-password?token=not-a-real-token" landing_query
check "a query on a landing page is not cached" header_is "$tmp/landing_query.head" cache-control no-store
check "a query on a landing page leaks no Referer" header_is "$tmp/landing_query.head" referrer-policy no-referrer
# OzID never proxies to the BFF: an API path is not answered by anything behind this server.
get "$BASE/api/bff.v1.AuthService/Me" api
check "no /api proxy: the path gets the page, not a backend answer" grep -q 'id="root"' "$tmp/api.body"

# --- one image, other configuration --------------------------------------------------------
start "$OTHER_ORIGIN"
get "$START_URL/" other
expect_headers other "$OTHER_ORIGIN"
get "$START_URL/account/verify-email" other_landing
expect_headers other_landing "$OTHER_ORIGIN"
check "other_landing: never cached" header_is "$tmp/other_landing.head" cache-control no-store

# --- a missing or illegal API_ORIGIN keeps the container from starting -----------------------
expect_refused() { # expect_refused <description> [API_ORIGIN value; omit the variable when unset]
  local what="$1" cid state code logs
  if [ "$#" -ge 2 ]; then
    cid="$(docker run -d -e "API_ORIGIN=$2" "$IMAGE")"
  else
    cid="$(docker run -d "$IMAGE")"
  fi
  containers+=("$cid")
  for _ in $(seq 1 60); do
    state="$(docker inspect -f '{{.State.Running}}' "$cid")"
    [ "$state" = false ] && break
    sleep 0.5
  done
  code="$(docker inspect -f '{{.State.ExitCode}}' "$cid")"
  logs="$(docker logs "$cid" 2>&1 || true)"
  if [ "$state" = false ] && [ "$code" != 0 ] && echo "$logs" | grep -q "ozid-web: API_ORIGIN"; then
    pass "refused to start: $what"
  else
    fail "refused to start: $what (running=$state exit=$code)"
    echo "$logs" | tail -n 5 >&2
  fi
  docker rm -f "$cid" >/dev/null 2>&1 || true
}
expect_refused "API_ORIGIN is not set"
expect_refused "API_ORIGIN is empty" ""
expect_refused "API_ORIGIN is plain http" "http://api.example.com"
expect_refused "API_ORIGIN has no scheme" "api.example.com"
expect_refused "API_ORIGIN carries a path" "https://api.example.com/api"
expect_refused "API_ORIGIN has a trailing slash" "https://api.example.com/"
expect_refused "API_ORIGIN is a wildcard" "https://*.example.com"
expect_refused "API_ORIGIN smuggles a second CSP directive" "https://api.example.com;script-src *"
expect_refused "API_ORIGIN contains a space" "https://api.example.com evil.example.org"

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "ozid-web container acceptance passed"
