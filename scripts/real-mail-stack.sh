#!/usr/bin/env bash
# Starts the real account chain for the optional real-mail Playwright specs
# (apps/trip-map/tests/real-mail.spec.ts, apps/metro/tests/real-mail.spec.ts):
# usersrv, tripmap, metro and the BFF, wired to the PostgreSQL, Redis and S3 you already
# run (`make deps-up` provides all three on the default ports) and to a mail catcher that
# the specs start themselves on $SMTP_PORT.
#
#   scripts/real-mail-stack.sh start   build, generate configs under .real-stack/, start
#   scripts/real-mail-stack.sh stop    stop what `start` started
#
# Then, with the stack up:
#   E2E_REAL_MAIL=1 E2E_SMTP_PORT=1025 E2E_PORT=4174 TRIPMAP_DEV_BACKEND=http://127.0.0.1:18081 \
#     npm run test:e2e:trip-map -- tests/real-mail.spec.ts
#   E2E_REAL_MAIL=1 E2E_SMTP_PORT=1025 E2E_PORT=4175 METRO_DEV_BACKEND=http://127.0.0.1:18081 \
#     npm run test:e2e:metro -- tests/real-mail.spec.ts
#
# Ports and databases are overridable (all are loopback only):
#   PG_PORT=15432 PG_USER=tripmap PG_PASSWORD=tripmap-local REDIS_PORT=16379 REDIS_PASSWORD=tripmap-local
#   S3_PORT=19000 SMTP_PORT=1025 BASE=18080 TRIPMAP_WEB=http://127.0.0.1:4174 METRO_WEB=http://127.0.0.1:4175
# The four databases bff, usersrv, tripmap and metro must exist (createdb), and the S3 bucket
# `tripmap` must exist (`make storage-init`). Mail links point at TRIPMAP_WEB / METRO_WEB, so
# those must be the origins the Playwright servers use (E2E_PORT above).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="$root/.real-stack"
PG_PORT="${PG_PORT:-15432}"
REDIS_PORT="${REDIS_PORT:-16379}"
S3_PORT="${S3_PORT:-19000}"
SMTP_PORT="${SMTP_PORT:-1025}"
BASE="${BASE:-18080}"
TRIPMAP_WEB="${TRIPMAP_WEB:-http://127.0.0.1:4174}"
METRO_WEB="${METRO_WEB:-http://127.0.0.1:4175}"

svc_port() { # name -> HTTP port
  case "$1" in bff) echo $((BASE + 1)) ;; tripmap) echo $((BASE + 2)) ;; metro) echo $((BASE + 4)) ;; usersrv) echo $((BASE + 6)) ;; esac
}

generate() {
  mkdir -p "$dir"
  for n in bff tripmap metro usersrv; do
    sed -e "s/15432/$PG_PORT/g; s/16379/$REDIS_PORT/g; s/19000/$S3_PORT/g" \
      -e "s#:1808\([1-7]\)#:$((BASE / 10))\1#g" \
      -e "s#http://localhost:5173#$TRIPMAP_WEB#g; s#http://localhost:5174#$METRO_WEB#g" \
      -e "s#dev-secrets/#$root/dev-secrets/#g" \
      -e "s/^\(    port: \)1025\$/\1$SMTP_PORT/" \
      "$root/config.$n.example.yaml" >"$dir/config.$n.yaml"
    if [ -n "${PG_USER:-}" ]; then sed -i "s/user: tripmap/user: $PG_USER/" "$dir/config.$n.yaml"; fi
    if [ -n "${PG_PASSWORD:-}" ]; then sed -i "s/password: tripmap-local\$/password: $PG_PASSWORD/" "$dir/config.$n.yaml"; fi
  done
}

start() {
  [ -f "$root/dev-secrets/bff.key" ] || (cd "$root" && go run ./cmd/dev-secrets)
  generate
  for n in bff tripmap metro usersrv; do (cd "$root" && go build -o "$dir/$n" "./cmd/$n"); done
  # usersrv first: the BFF needs it at start.
  for n in usersrv tripmap metro bff; do
    (
      cd "$dir"
      # Detached from this script's output so a pipe reading it can finish.
      BUTTERFLY_CONFIG_TYPE=file BUTTERFLY_CONFIG_FILE_PATH="./config.$n.yaml" PORT="$(svc_port $n)" \
        setsid nohup "./$n" >"$n.log" 2>&1 </dev/null &
      echo $! >"$n.pid"
    )
  done
  bff="http://127.0.0.1:$(svc_port bff)"
  for _ in $(seq 1 60); do
    if curl -fsS -m 2 "$bff/readyz" >/dev/null 2>&1; then
      echo "stack ready: BFF $bff (logs in $dir/*.log)"
      return 0
    fi
    sleep 1
  done
  echo "stack did not become ready; see $dir/*.log" >&2
  return 1
}

stop() {
  for n in bff metro tripmap usersrv; do
    if [ -f "$dir/$n.pid" ]; then kill "$(cat "$dir/$n.pid")" 2>/dev/null || true; rm -f "$dir/$n.pid"; fi
  done
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  *) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac
