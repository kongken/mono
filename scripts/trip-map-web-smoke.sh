#!/usr/bin/env bash
# Image acceptance for trip-map-web (deploy/trip-map-web) in both build modes. Only reserved example
# addresses are used; nothing is published.
#
#   local mode    no build args: a static, local-only site. The bundle must carry no backend or
#                 OzID address, and the page must still be served.
#   backend mode  VITE_API_BASE_URL=https://api.example.com/api and VITE_OZID_ORIGIN=https://id.example.com
#                 (what a deployment passes): the bundle must carry exactly those two addresses, and
#                 the retired in-product sign-in routes stay 404.
#
# A half configuration (a backend address without OzID, a relative /api) must fail the build, since
# sign-in, registration, password and devices are OzID pages.
#
#   scripts/trip-map-web-smoke.sh            # needs a working `docker` and curl; run by CI (trip-map-web-image)
set -euo pipefail
cd "$(dirname "$0")/.."

API_BASE="https://api.example.com/api"
OZID_ORIGIN="https://id.example.com"
LOCAL_IMAGE="${LOCAL_IMAGE:-trip-map-web:smoke-local}"
BACKEND_IMAGE="${BACKEND_IMAGE:-trip-map-web:smoke-backend}"
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

build() { # build <tag> [--build-arg ...]
  local tag="$1"
  shift
  docker build -f deploy/trip-map-web/Dockerfile "$@" -t "$tag" .
}

# dist <image> <dir>: the built site, copied out of the image without running it.
dist() {
  local cid
  cid="$(docker create "$1")"
  containers+=("$cid")
  mkdir -p "$2"
  docker cp "$cid:/usr/share/nginx/html/." "$2"
}

contains() { grep -rqF -- "$1" "$2"; }       # contains <text> <dir>
lacks() { ! grep -rqF -- "$1" "$2"; }        # lacks <text> <dir>

# --- builds --------------------------------------------------------------------------------
build "$LOCAL_IMAGE"
build "$BACKEND_IMAGE" --build-arg "VITE_API_BASE_URL=$API_BASE" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN"

# A backend address without OzID, and a same-origin /api, both fail the build.
if docker build -q -f deploy/trip-map-web/Dockerfile --build-arg "VITE_API_BASE_URL=$API_BASE" -t "$BACKEND_IMAGE-noozid" . >"$tmp/noozid.log" 2>&1; then
  fail "a backend build without VITE_OZID_ORIGIN must fail"
else
  pass "a backend build without VITE_OZID_ORIGIN fails"
fi
if docker build -q -f deploy/trip-map-web/Dockerfile --build-arg "VITE_API_BASE_URL=/api" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN" -t "$BACKEND_IMAGE-relative" . >"$tmp/relative.log" 2>&1; then
  fail "a build with a relative VITE_API_BASE_URL must fail"
else
  pass "a build with a relative VITE_API_BASE_URL fails"
fi

# --- bundles -------------------------------------------------------------------------------
dist "$LOCAL_IMAGE" "$tmp/local"
dist "$BACKEND_IMAGE" "$tmp/backend"

check "local mode ships an index page" test -s "$tmp/local/index.html"
# The configured value is baked in as a quoted string (`$API_BASE`); the build's own error text mentions
# the example address unquoted, so the quote is part of the match.
check "local mode: no backend address in the bundle" lacks "\`$API_BASE\`" "$tmp/local"
check "local mode: no OzID address in the bundle" lacks "id.example.com" "$tmp/local"
check "backend mode ships an index page" test -s "$tmp/backend/index.html"
check "backend mode: the configured backend address is in the bundle" contains "\`$API_BASE\`" "$tmp/backend"
check "backend mode: the configured OzID address is in the bundle" contains "\`$OZID_ORIGIN\`" "$tmp/backend"

# --- run -----------------------------------------------------------------------------------
serve() { # serve <image>: sets START_URL
  local cid port
  cid="$(docker run -d -p 127.0.0.1::80 "$1")"
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
code() { curl -s -o /dev/null -w '%{http_code}' "$1"; }

for mode in local backend; do
  if [ "$mode" = local ]; then image="$LOCAL_IMAGE"; else image="$BACKEND_IMAGE"; fi
  serve "$image"
  check "$mode mode: /healthz answers" test "$(code "$START_URL/healthz")" = 200
  check "$mode mode: the start page is served" test "$(code "$START_URL/")" = 200
  check "$mode mode: /status is the single-page fallback" test "$(code "$START_URL/status")" = 200
  check "$mode mode: the retired in-product sign-in route is 404" test "$(code "$START_URL/auth/login")" = 404
  # T12: no same-origin proxy of the BFF's browser API (the account and management procedures); only the
  # anonymous public paths (share, unsubscribe, tiles) and the probes are the product's own.
  check "$mode mode: the account API is not proxied" test "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' -d '{}' "$START_URL/api/bff.v1.AuthService/LoginWithEmail")" = 404
  check "$mode mode: the management API is not proxied" test "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' -d '{}' "$START_URL/api/bff.v1.UserAdminService/ListUsers")" = 404
  check "$mode mode: an unknown /api path is 404 (no single-page fallback)" test "$(code "$START_URL/api/anything")" = 404
done

if [ "$failures" -gt 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all trip-map-web image checks passed"
