#!/usr/bin/env bash
# Image acceptance for metro-web (deploy/metro-web). Only reserved example addresses are used; nothing is
# published.
#
# metro has no local-only mode: every account step is OzID's and the shared session Cookie belongs to the BFF's
# public API host, so the image needs both public addresses at build time, and the build must fail without
# them (or with a same-origin /api, which could not carry the shared session):
#
#   VITE_API_BASE_URL=https://api.example.com/api   VITE_OZID_ORIGIN=https://id.example.com
#
# The bundle must carry exactly those addresses, the single-page routes the sign-in returns to must be served,
# the retired same-origin /api and /auth routes must stay 404, and the mail landing pages keep their
# no-store / no-referrer / CSP headers.
#
#   scripts/metro-web-smoke.sh            # needs a working `docker` and curl; run by CI (metro-web-image)
set -euo pipefail
cd "$(dirname "$0")/.."

API_BASE="https://api.example.com/api"
OZID_ORIGIN="https://id.example.com"
IMAGE="${IMAGE:-metro-web:smoke}"
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
contains() { grep -rqF -- "$1" "$2"; } # contains <text> <dir>

# --- builds --------------------------------------------------------------------------------
docker build -f deploy/metro-web/Dockerfile --build-arg "VITE_API_BASE_URL=$API_BASE" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN" -t "$IMAGE" .

must_fail() { # must_fail <description> <build args...>
  local what="$1"
  shift
  if docker build -q -f deploy/metro-web/Dockerfile "$@" -t "$IMAGE-bad" . >"$tmp/bad.log" 2>&1; then
    fail "$what must fail the build"
  else
    pass "$what fails the build"
  fi
}
must_fail "a build without any address" 
must_fail "a build without VITE_OZID_ORIGIN" --build-arg "VITE_API_BASE_URL=$API_BASE"
must_fail "a build without VITE_API_BASE_URL" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN"
must_fail "a build with a relative VITE_API_BASE_URL" --build-arg "VITE_API_BASE_URL=/api" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN"
must_fail "a build with an OzID address that is not an origin" --build-arg "VITE_API_BASE_URL=$API_BASE" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN/login"

# --- bundle --------------------------------------------------------------------------------
cid="$(docker create "$IMAGE")"
containers+=("$cid")
mkdir -p "$tmp/dist"
docker cp "$cid:/usr/share/nginx/html/." "$tmp/dist"
check "the image ships an index page" test -s "$tmp/dist/index.html"
check "the configured backend address is in the bundle" contains "\`$API_BASE\`" "$tmp/dist"
check "the configured OzID address is in the bundle" contains "\`$OZID_ORIGIN\`" "$tmp/dist"

# --- run -----------------------------------------------------------------------------------
cid="$(docker run -d -p 127.0.0.1::80 "$IMAGE")"
containers+=("$cid")
port="$(docker port "$cid" 80/tcp | head -n1 | sed 's/.*://')"
START_URL="http://127.0.0.1:$port"
ready=0
for _ in $(seq 1 60); do
  if curl -fsS "$START_URL/healthz" >/dev/null 2>&1; then ready=1; break; fi
  sleep 0.5
done
if [ "$ready" != 1 ]; then
  echo "container did not become ready:" >&2
  docker logs "$cid" >&2 || true
  exit 1
fi
code() { curl -s -o /dev/null -w '%{http_code}' "$1"; }
header() { curl -s -D - -o /dev/null "$1" | tr -d '\r' | grep -i "^$2:" | head -n1; } # header <url> <name>

check "/healthz answers" test "$(code "$START_URL/healthz")" = 200
check "the start page is served" test "$(code "$START_URL/")" = 200
for page in /cities/shanghai /topics/six-cars /u/someone /me; do
  check "$page is the single-page fallback (a sign-in returns to it)" test "$(code "$START_URL$page")" = 200
done
check "the retired same-origin /api is 404" test "$(code "$START_URL/api/bff.v1.AuthService/Me")" = 404
check "the retired /auth/login route is 404" test "$(code "$START_URL/auth/login")" = 404
check "an old mail landing page is served" test "$(code "$START_URL/account/verify-email")" = 200
check "an old mail landing page is not cached" test "$(header "$START_URL/account/verify-email" cache-control)" = "Cache-Control: no-store"
check "an old mail landing page leaks no Referer" test "$(header "$START_URL/account/reset-password" referrer-policy)" = "Referrer-Policy: no-referrer"
check "an old mail landing page has a script CSP" header "$START_URL/account/reset-password" content-security-policy

if [ "$failures" -gt 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all metro-web image checks passed"
