#!/bin/sh
# Runs before the Nginx image's own template step (20-envsubst-on-templates.sh) so a bad value never
# reaches the generated configuration. A non-zero exit stops the container's entrypoint.
# API_ORIGIN may be unset or empty: the page then talks to its own origin only (the image's default
# VITE_API_BASE_URL=/). A value must be a usable origin: a wildcard or path would widen what the page
# may connect to, and plain http would let the session be intercepted.
set -eu
[ -z "${API_ORIGIN:-}" ] && exit 0
case "${API_ORIGIN}" in
  https://*[!/]) ;;
  *) echo "ozid-web: API_ORIGIN must be empty or the BFF's https origin, for example https://api.example.com" >&2; exit 1 ;;
esac
case "${API_ORIGIN#https://}" in
  */*|*\**|*\;*|*\ *|*\"*|*\'*|*\\*) echo "ozid-web: API_ORIGIN must be an origin only (no path, wildcard or spaces)" >&2; exit 1 ;;
esac
