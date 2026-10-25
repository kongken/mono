#!/bin/sh
# Runs before the Nginx image's own template step (20-envsubst-on-templates.sh) so a bad value never
# reaches the generated configuration. A non-zero exit stops the container's entrypoint.
# Refuse to start with an unusable API_ORIGIN: a CSP without it would block every sign-in, and a
# wildcard or path would widen what the page may connect to.
set -eu
case "${API_ORIGIN:-}" in
  https://*[!/]) ;;
  *) echo "ozid-web: API_ORIGIN must be the BFF's https origin, for example https://api.example.com" >&2; exit 1 ;;
esac
case "${API_ORIGIN#https://}" in
  */*|*\**|*\;*|*\ *|*\"*|*\'*|*\\*) echo "ozid-web: API_ORIGIN must be an origin only (no path, wildcard or spaces)" >&2; exit 1 ;;
esac
