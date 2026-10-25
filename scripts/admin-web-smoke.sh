#!/usr/bin/env bash
# Container acceptance for the admin-web image (deploy/admin-web): builds it for reserved example
# addresses, starts it behind a stand-in gateway (the test sends the forwarded header itself), and
# checks what the real Nginx answers:
#   - the page, its static assets and the single-page fallback are served only to an allowlisted
#     source, and every other source gets 403 for ALL of them (a missing asset included);
#   - a forged forwarded header cannot lift a source into the list (only a trusted proxy's header is
#     believed, read from the right); another Host (or a Host with a port) gets no response at all;
#   - /healthz answers for probes only on the default server;
#   - the CSP and clickjacking headers, and that a missing or illegal setting keeps the container
#     from starting (an empty allowlist or a fully open one is a refusal, never "open").
# Only reserved example addresses are used.
#
#   scripts/admin-web-smoke.sh            # needs a working `docker` and curl; run by CI (admin-web-image)
#   IMAGE=admin-web:dev SKIP_BUILD=1 scripts/admin-web-smoke.sh   # test an image that is already built
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${IMAGE:-admin-web:smoke}"
API_BASE="https://api.example.com/api"
API_ORIGIN="https://api.example.com"
OZID_ORIGIN="https://id.example.com"
HOST="admin.example.com"
OFFICE="203.0.113.0/24"      # the allowlisted network (TEST-NET-3)
INSIDE="203.0.113.9"
OUTSIDE="198.51.100.7"       # TEST-NET-2: not on the list
# The test client reaches the container from the docker bridge, so that range is the "gateway".
GATEWAYS="10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
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
  docker build -f deploy/admin-web/Dockerfile \
    --build-arg "VITE_API_BASE_URL=$API_BASE" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN" -t "$IMAGE" .
fi
# The image has no default addresses: a build without either, or with a relative/http one, must fail.
if [ "${SKIP_BUILD:-}" != "1" ] && [ "${NEGATIVE_BUILDS:-1}" = "1" ]; then
  neg() { # neg <description> <build-arg...>
    local what="$1"
    shift
    if docker build -q -f deploy/admin-web/Dockerfile "$@" -t "$IMAGE-neg" . >"$tmp/neg.log" 2>&1; then
      fail "a build $what must fail"
    else
      pass "a build $what fails"
    fi
  }
  neg "without VITE_API_BASE_URL and VITE_OZID_ORIGIN"
  neg "without VITE_OZID_ORIGIN" --build-arg "VITE_API_BASE_URL=$API_BASE"
  neg "without VITE_API_BASE_URL" --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN"
  neg "with a relative VITE_API_BASE_URL" --build-arg VITE_API_BASE_URL=/api --build-arg "VITE_OZID_ORIGIN=$OZID_ORIGIN"
  neg "with a plain-http VITE_OZID_ORIGIN" --build-arg "VITE_API_BASE_URL=$API_BASE" --build-arg VITE_OZID_ORIGIN=http://id.example.com
fi

# --- run -----------------------------------------------------------------------------------
# start <CIDRS> [PROXIES] sets START_URL (not a subshell: the container must reach `containers`).
start() {
  local cid port
  cid="$(docker run -d -p 127.0.0.1::80 \
    -e "API_ORIGIN=$API_ORIGIN" -e "ADMIN_HOST=$HOST" -e "ADMIN_ALLOWED_CIDRS=$1" -e "ADMIN_TRUSTED_PROXIES=${2:-}" "$IMAGE")"
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
header_has() { tr -d '\r' <"$1" | grep -i -- "^$2:" | grep -qi -- "$3"; }  # header_has <headfile> <name> <substring>
contains() { grep -qF -- "$2" <<<"$1"; }                                   # contains <text> <substring>
no_wildcard() { [[ "$1" != *'*'* ]]; }
# get <base> <name> <path> [curl args...]: writes $tmp/<name>.head and .body; the status is "000"
# when the server closed the connection without answering.
get() {
  local base="$1" name="$2" path="$3"
  shift 3
  curl -sS --path-as-is -D "$tmp/$name.head" -o "$tmp/$name.body" "$@" "$base$path" 2>/dev/null || : >"$tmp/$name.head"
}
dropped() { [ ! -s "$1" ]; }                                               # dropped <headfile>: no response at all

# console <base> <name> <path> <client-ip> [more curl args]: as the console host, via the "gateway".
console() {
  local base="$1" name="$2" path="$3" ip="$4"
  shift 4
  get "$base" "$name" "$path" -H "Host: $HOST" -H "X-Forwarded-For: $ip" "$@"
}

# ===== container 1: the gateway is trusted, one office network is allowed ========================
start "$OFFICE" "$GATEWAYS"
BASE="$START_URL"
echo "admin-web is up at $BASE (allowlist $OFFICE, trusted gateways $GATEWAYS)"

# healthz: only the default server answers it, to any source, and says only "ok".
get "$BASE" health /healthz -H "Host: 10.9.9.9"
check "/healthz answers 200 for a probe (default server)" status_is "$tmp/health.head" 200
check "/healthz says ok" test "$(tr -d '\n' <"$tmp/health.body")" = ok
console "$BASE" health_console /healthz "$OUTSIDE"
check "/healthz on the console host is not an open door: an outside source gets 403" status_is "$tmp/health_console.head" 403

# an allowlisted source gets the page, the assets and the fallback
console "$BASE" index / "$INSIDE"
check "an allowed source gets the page (200)" status_is "$tmp/index.head" 200
check "the page is HTML" header_has "$tmp/index.head" content-type text/html
check "the page is the console shell" grep -q 'id="root"' "$tmp/index.body"
check "the page is not cached" header_has "$tmp/index.head" cache-control no-store
js="$(grep -o 'src="/assets/[^"]*\.js"' "$tmp/index.body" | head -n1 | sed 's/src="//; s/"$//' || true)"
css="$(grep -o 'href="/assets/[^"]*\.css"' "$tmp/index.body" | head -n1 | sed 's/href="//; s/"$//' || true)"
check "the page references a script" test -n "$js"
check "the page references a stylesheet" test -n "$css"
if [ -n "$js" ]; then
  console "$BASE" js "$js" "$INSIDE"
  check "an allowed source gets the script (200)" status_is "$tmp/js.head" 200
  check "the script is JavaScript" header_has "$tmp/js.head" content-type javascript
  check "assets are immutable" header_has "$tmp/js.head" cache-control immutable
  check "the bundle carries the API base it was built for" grep -qF "$API_BASE" "$tmp/js.body"
  check "the bundle carries the OzID origin it was built for" grep -qF "$OZID_ORIGIN" "$tmp/js.body"
fi
for route in /users /users/00000000-0000-4000-8000-000000000001 /anything/else; do
  name="route$(printf '%s' "$route" | tr -c 'a-z' '_')"
  console "$BASE" "$name" "$route" "$INSIDE"
  check "$route falls back to the shell for an allowed source" status_is "$tmp/$name.head" 200
  check "$route is the console shell" grep -q 'id="root"' "$tmp/$name.body"
done
console "$BASE" favicon /favicon.svg "$INSIDE"
check "the favicon is served to an allowed source" status_is "$tmp/favicon.head" 200

# every other source gets nothing: not the page, not an asset, not the fallback, not a 404 that
# tells it which files exist
console "$BASE" denied_index / "$OUTSIDE"
check "an outside source is refused the page (403)" status_is "$tmp/denied_index.head" 403
check "the refusal carries no console content" test "$(wc -c <"$tmp/denied_index.body")" -lt 64
check "the refusal does not contain the shell" bash -c "! grep -q 'id=\"root\"' '$tmp/denied_index.body'"
[ -n "$js" ] && { console "$BASE" denied_js "$js" "$OUTSIDE"; check "an outside source is refused the script (403)" status_is "$tmp/denied_js.head" 403; }
for route in /users /users/00000000-0000-4000-8000-000000000001 /favicon.svg /index.html /assets/does-not-exist.js /x/../index.html; do
  name="denied$(printf '%s' "$route" | tr -c 'a-z' '_')"
  console "$BASE" "$name" "$route" "$OUTSIDE"
  check "an outside source is refused $route (403, not 200/404)" status_is "$tmp/$name.head" 403
done
console "$BASE" denied_options / "$OUTSIDE" -X OPTIONS -H "Origin: https://admin.example.com" -H "Access-Control-Request-Method: POST"
check "a preflight from an outside source is refused (403)" status_is "$tmp/denied_options.head" 403
check "the refusal grants no CORS" bash -c "! tr -d '\r' <'$tmp/denied_options.head' | grep -qi '^access-control-allow'"
console "$BASE" denied_post / "$OUTSIDE" -X POST -d x=1
check "a POST from an outside source is refused (403)" status_is "$tmp/denied_post.head" 403

# forged forwarded headers: the proxy appends the real peer last, so what the client wrote is to the left
get "$BASE" forged_prefix / -H "Host: $HOST" -H "X-Forwarded-For: $INSIDE, $OUTSIDE"
check "forged: an allowed address written first by the client does not help (403)" status_is "$tmp/forged_prefix.head" 403
get "$BASE" two_lines / -H "Host: $HOST" -H "X-Forwarded-For: $INSIDE" -H "X-Forwarded-For: $OUTSIDE"
check "forged: a second header line with the real source wins (403)" status_is "$tmp/two_lines.head" 403
get "$BASE" no_header / -H "Host: $HOST"
check "no forwarded header: the gateway itself is the client and is not on the list (403)" status_is "$tmp/no_header.head" 403
get "$BASE" other_header / -H "Host: $HOST" -H "X-Real-IP: $INSIDE" -H "Forwarded: for=$INSIDE" -H "X-Client-IP: $INSIDE"
check "forged: other 'real IP' headers are not read (403)" status_is "$tmp/other_header.head" 403

# another Host, or a port on the Host, gets no response at all (even from an allowed source)
for h in "other.example.net" "admin.example.com:8443" "ADMIN.example.com" "10.1.2.3" "admin.example.com.evil.example"; do
  name="host$(printf '%s' "$h" | tr -c 'a-z0-9' '_')"
  get "$BASE" "$name" / -H "Host: $h" -H "X-Forwarded-For: $INSIDE"
  check "Host '$h' gets no response" dropped "$tmp/$name.head"
done

# the headers real Nginx sends
expect_headers() { # expect_headers <name>
  local f="$tmp/$1.head" csp
  csp="$(header "$f" content-security-policy)"
  check "$1: X-Frame-Options is DENY" header_is "$f" x-frame-options DENY
  check "$1: CSP lets the page connect to itself and the API origin only" contains "$csp" "connect-src 'self' $API_ORIGIN;"
  check "$1: CSP script-src is 'self'" contains "$csp" "script-src 'self';"
  check "$1: CSP forbids framing" contains "$csp" "frame-ancestors 'none'"
  check "$1: CSP forbids objects" contains "$csp" "object-src 'none'"
  check "$1: CSP forbids base changes" contains "$csp" "base-uri 'none'"
  check "$1: CSP has no wildcard source" no_wildcard "$csp"
  check "$1: Referrer-Policy is no-referrer" header_is "$f" referrer-policy no-referrer
  check "$1: nosniff" header_is "$f" x-content-type-options nosniff
  check "$1: not indexed" header_has "$f" x-robots-tag noindex
}
expect_headers index
expect_headers route_users
expect_headers denied_index

# ===== container 2: no trusted proxy: the forwarded header is never believed ====================
# A client could write anything: only the TCP peer counts. The test's own address (the docker
# bridge) is on this list, so the page opens; a forged header naming the office does not matter.
start "$GATEWAYS"
BASE="$START_URL"
console "$BASE" notrust_page / "$OUTSIDE"
check "no trusted proxy: the peer (the bridge) is allowed, whatever the header says (200)" status_is "$tmp/notrust_page.head" 200
start "$OFFICE"
BASE="$START_URL"
console "$BASE" notrust_forged / "$INSIDE"
check "no trusted proxy: a header naming an allowed address does not lift the peer (403)" status_is "$tmp/notrust_forged.head" 403

# ===== a missing or illegal setting keeps the container from starting ==========================
expect_refused() { # expect_refused <description> <expected message part> [VAR=value ...]
  local what="$1" want="$2" cid state="true" code logs
  shift 2
  local envs=(-e "API_ORIGIN=$API_ORIGIN" -e "ADMIN_HOST=$HOST" -e "ADMIN_ALLOWED_CIDRS=$OFFICE")
  local a
  for a in "$@"; do envs+=(-e "$a"); done
  cid="$(docker run -d "${envs[@]}" "$IMAGE")"
  containers+=("$cid")
  for _ in $(seq 1 60); do
    state="$(docker inspect -f '{{.State.Running}}' "$cid")"
    [ "$state" = false ] && break
    sleep 0.5
  done
  code="$(docker inspect -f '{{.State.ExitCode}}' "$cid")"
  logs="$(docker logs "$cid" 2>&1 || true)"
  if [ "$state" = false ] && [ "$code" != 0 ] && grep -qF "admin-web: $want" <<<"$logs"; then
    pass "refused to start: $what"
  else
    fail "refused to start: $what (running=$state exit=$code)"
    echo "$logs" | tail -n 5 >&2
  fi
  docker rm -f "$cid" >/dev/null 2>&1 || true
}
expect_refused "the allowlist is empty (an empty list is a refusal, not 'open')" "ADMIN_ALLOWED_CIDRS is empty" ADMIN_ALLOWED_CIDRS=
expect_refused "the allowlist opens IPv4 to everyone" "ADMIN_ALLOWED_CIDRS" ADMIN_ALLOWED_CIDRS=0.0.0.0/0
expect_refused "the allowlist opens IPv6 to everyone" "ADMIN_ALLOWED_CIDRS" ADMIN_ALLOWED_CIDRS=::/0
expect_refused "the allowlist entry has host bits" "ADMIN_ALLOWED_CIDRS" ADMIN_ALLOWED_CIDRS=203.0.113.5/24
expect_refused "the allowlist entry has no prefix" "ADMIN_ALLOWED_CIDRS" ADMIN_ALLOWED_CIDRS=203.0.113.5
expect_refused "the allowlist is a wildcard" "ADMIN_ALLOWED_CIDRS" 'ADMIN_ALLOWED_CIDRS=*'
expect_refused "the allowlist smuggles a directive" "ADMIN_ALLOWED_CIDRS" 'ADMIN_ALLOWED_CIDRS=203.0.113.0/24;allow all'
expect_refused "a trusted proxy range opens everything" "ADMIN_TRUSTED_PROXIES" ADMIN_TRUSTED_PROXIES=0.0.0.0/0
expect_refused "ADMIN_HOST is a wildcard" "ADMIN_HOST" 'ADMIN_HOST=*.example.com'
expect_refused "ADMIN_HOST carries a port" "ADMIN_HOST" ADMIN_HOST=admin.example.com:8443
expect_refused "ADMIN_HOST is empty" "ADMIN_HOST" ADMIN_HOST=
expect_refused "API_ORIGIN is plain http" "API_ORIGIN" API_ORIGIN=http://api.example.com
expect_refused "API_ORIGIN carries a path" "API_ORIGIN" API_ORIGIN=https://api.example.com/api
expect_refused "API_ORIGIN smuggles a second CSP directive" "API_ORIGIN" "API_ORIGIN=https://api.example.com;script-src *"
expect_refused "API_ORIGIN is empty" "API_ORIGIN" API_ORIGIN=
# not set at all: the image's own defaults are empty, which is the same refusal
cid="$(docker run -d "$IMAGE")"
containers+=("$cid")
for _ in $(seq 1 60); do
  [ "$(docker inspect -f '{{.State.Running}}' "$cid")" = false ] && break
  sleep 0.5
done
if [ "$(docker inspect -f '{{.State.Running}}' "$cid")" = false ] && [ "$(docker inspect -f '{{.State.ExitCode}}' "$cid")" != 0 ]; then
  pass "refused to start: nothing is configured"
else
  fail "refused to start: nothing is configured"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "admin-web container acceptance passed"
