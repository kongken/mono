#!/bin/sh
# Refuse to start with an unusable API_ORIGIN: a CSP without it would block every sign-in, and a
# wildcard or path would widen what the page may connect to.
set -eu
case "${API_ORIGIN:-}" in
  https://*[!/]) ;;
  *) echo "ozid-web: API_ORIGIN must be the BFF's https origin, for example https://api.example.com" >&2; exit 1 ;;
esac
case "${API_ORIGIN#https://}" in
  */*|*\**|*\;*|*\ *|*\"*) echo "ozid-web: API_ORIGIN must be an origin only (no path, wildcard or spaces)" >&2; exit 1 ;;
esac
