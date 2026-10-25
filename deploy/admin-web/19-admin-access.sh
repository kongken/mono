#!/bin/sh
# Runs before the Nginx image's own template step (20-envsubst-on-templates.sh), so a bad value
# never reaches the generated configuration; a non-zero exit stops the container's entrypoint.
#
# It validates the settings that decide WHO may see the console and writes the access rules
# (/etc/nginx/admin-access.conf, included by the console's server block):
#
#   API_ORIGIN             the BFF's https origin (the page's CSP connect-src), origin only
#   ADMIN_HOST             the one host name this console answers to; any other Host gets nothing
#   ADMIN_ALLOWED_CIDRS    comma-separated source networks allowed to load the console. Required:
#                          empty means refuse to start, never "open to everyone". A network
#                          without a prefix length, with host bits (IPv4), wider than /8 (IPv4) or
#                          /16 (IPv6), an IPv4-mapped IPv6 network or a wildcard is refused.
#   ADMIN_TRUSTED_PROXIES  optional comma-separated networks of the gateways in front of this
#                          container. Only then is the forwarded header read, right to left,
#                          skipping these hops; without it the TCP peer is the client.
#   ADMIN_REAL_IP_HEADER   the forwarded header (default X-Forwarded-For)
#
# The two networks lists must not overlap. Nginx falls back to the address of the gateway itself
# whenever the forwarded chain is unusable (an entry that is not an address, or only gateways in
# it), and that address is then judged by the allowlist. A gateway inside the allowlist would
# therefore let anyone who can send a malformed header through it open the console, so the
# container refuses to start with such a combination instead of hoping the header is well formed.
#
# The BFF checks the same allowlist again on its own (admin.allowed_cidrs): this is the first of
# two independent layers, not a replacement for the second.
set -eu
set -f

fail() {
  echo "admin-web: $*" >&2
  exit 1
}

conf_dir="${ADMIN_CONF_DIR:-/etc/nginx}"

case "${API_ORIGIN:-}" in
  https://*[!/]) ;;
  *) fail "API_ORIGIN must be the BFF's https origin, for example https://api.example.com" ;;
esac
case "${API_ORIGIN#https://}" in
  */*|*\**|*\;*|*\ *|*\"*|*\'*|*\\*|*\$*) fail "API_ORIGIN must be an origin only (no path, wildcard or spaces)" ;;
esac

case "${ADMIN_HOST:-}" in
  ''|*[!a-z0-9.-]*|.*|*.|*..*|-*) fail "ADMIN_HOST must be one lower-case host name such as admin.example.com (no wildcard, port or path)" ;;
esac

header="${ADMIN_REAL_IP_HEADER:-X-Forwarded-For}"
case "$header" in
  ''|*[!A-Za-z0-9-]*) fail "ADMIN_REAL_IP_HEADER must be a header name such as X-Forwarded-For" ;;
esac

# v6_groups <setting> <value> <lower-case address>: leaves the eight 16-bit groups, as decimal
# numbers separated by commas, in $groups; anything that is not a plain IPv6 address is refused.
v6_groups() {
  _s="$1"
  _v="$2"
  _a="$3"
  case "$_a" in
    *:::*|*::*::*) fail "$_s: '$_v' is not an IPv6 network" ;;
  esac
  case "$_a" in
    *::*) _head="${_a%%::*}"; _tail="${_a#*::}"; _dbl=1 ;;
    *) _head="$_a"; _tail=""; _dbl=0 ;;
  esac
  _old="$IFS"
  IFS=:
  # shellcheck disable=SC2086
  set -- $_head
  _nh=$#
  _hg=""
  for _g in "$@"; do
    case "$_g" in ''|*[!0-9a-f]*|?????*) fail "$_s: '$_v' is not an IPv6 network" ;; esac
    _hg="$_hg,$((0x$_g))"
  done
  # shellcheck disable=SC2086
  set -- $_tail
  _nt=$#
  _tg=""
  for _g in "$@"; do
    case "$_g" in ''|*[!0-9a-f]*|?????*) fail "$_s: '$_v' is not an IPv6 network" ;; esac
    _tg="$_tg,$((0x$_g))"
  done
  IFS="$_old"
  if [ "$_dbl" -eq 1 ]; then
    _fill=$((8 - _nh - _nt))
    [ "$_fill" -ge 1 ] || fail "$_s: '$_v' is not an IPv6 network"
    _z=""
    _i=0
    while [ "$_i" -lt "$_fill" ]; do _z="$_z,0"; _i=$((_i + 1)); done
    groups="$_hg$_z$_tg"
  else
    [ "$_nh" -eq 8 ] || fail "$_s: '$_v' is not an IPv6 network"
    groups="$_hg"
  fi
  groups="${groups#,}"
}

# overlaps <record> <record>: true when two networks (as recorded by check_cidr) share an address.
overlaps() {
  _old="$IFS"
  IFS='|'
  # shellcheck disable=SC2086
  set -- $1 $2
  IFS="$_old"
  [ "$1" = "$5" ] || return 1
  _common="$3"
  [ "$7" -ge "$_common" ] || _common="$7"
  if [ "$1" = v4 ]; then
    _mask=$(( (0xffffffff << (32 - _common)) & 0xffffffff ))
    [ $(( $2 & _mask )) -eq $(( $6 & _mask )) ]
    return
  fi
  _old="$IFS"
  IFS=,
  # shellcheck disable=SC2086
  set -- $2 $6
  IFS="$_old"
  _k=0
  while [ "$_k" -lt 8 ]; do
    _bits=$(( _common - 16 * _k ))
    [ "$_bits" -gt 0 ] || break
    [ "$_bits" -le 16 ] || _bits=16
    _mask=$(( (0xffff << (16 - _bits)) & 0xffff ))
    eval "_x=\${$((_k + 1))}"
    eval "_y=\${$((_k + 9))}"
    [ $(( _x & _mask )) -eq $(( _y & _mask )) ] || return 1
    _k=$((_k + 1))
  done
  return 0
}

# check_cidr <setting> <value>: validates one network and appends it to $rec.
check_cidr() {
  setting="$1"
  value="$2"
  addr="${value%/*}"
  len="${value#*/}"
  [ "$addr" != "$value" ] || fail "$setting: '$value' needs a prefix length, for example 203.0.113.0/24"
  case "$len" in
    ''|*[!0-9]*) fail "$setting: '$value' has an invalid prefix length" ;;
  esac
  case "$addr" in
    *:*)
      case "$addr" in
        *[!0-9a-fA-F:]*) fail "$setting: '$value' is not an IPv6 network" ;;
      esac
      lower="$(printf '%s' "$addr" | tr 'A-F' 'a-f')"
      case "$lower" in
        ::ffff:*|*:ffff:*.*) fail "$setting: '$value' is an IPv4-mapped network; list the IPv4 network itself" ;;
      esac
      [ "$len" -le 128 ] || fail "$setting: '$value' has an invalid prefix length"
      [ "$len" -ge 16 ] || fail "$setting: '$value' is wider than /16, which is almost everyone"
      v6_groups "$setting" "$value" "$lower"
      rec="$rec v6|$groups|$len|$value"
      ;;
    *)
      case "$addr" in
        *[!0-9.]*|*..*|.*|*.) fail "$setting: '$value' is not an IPv4 network" ;;
      esac
      oldifs="$IFS"
      IFS=.
      # shellcheck disable=SC2086
      set -- $addr
      IFS="$oldifs"
      [ "$#" -eq 4 ] || fail "$setting: '$value' is not an IPv4 network"
      for octet in "$@"; do
        case "$octet" in
          0?*) fail "$setting: '$value' has a leading zero in an address part (it is ambiguous: octal or decimal)" ;;
        esac
        [ "${#octet}" -le 3 ] && [ "$octet" -le 255 ] || fail "$setting: '$value' is not an IPv4 network"
      done
      [ "$len" -le 32 ] || fail "$setting: '$value' has an invalid prefix length"
      [ "$len" -ge 8 ] || fail "$setting: '$value' is wider than /8, which is almost everyone"
      ip=$(( ($1 << 24) | ($2 << 16) | ($3 << 8) | $4 ))
      hostbits=$(( ip & ((1 << (32 - len)) - 1) ))
      [ "$hostbits" -eq 0 ] || fail "$setting: '$value' has host bits set; write the network address, for example 203.0.113.0/24"
      rec="$rec v4|$ip|$len|$value"
      ;;
  esac
}

# validate_list <setting> <value>: validates every item (a bad one stops the script right here,
# not in a subshell) and leaves the items, space separated, in $validated and their parsed form
# in $rec.
validate_list() {
  validated=""
  rec=""
  items="$(printf '%s' "$2" | tr ',' ' ')"
  for item in $items; do
    check_cidr "$1" "$item"
    validated="$validated $item"
  done
}

validate_list ADMIN_ALLOWED_CIDRS "${ADMIN_ALLOWED_CIDRS:-}"
[ -n "$validated" ] || fail "ADMIN_ALLOWED_CIDRS is empty: an empty allowlist denies everyone, so the console would be unusable. List the networks that may use it."
allowed="$validated"
allowed_rec="$rec"
proxies=""
if [ -n "${ADMIN_TRUSTED_PROXIES:-}" ]; then
  validate_list ADMIN_TRUSTED_PROXIES "$ADMIN_TRUSTED_PROXIES"
  [ -n "$validated" ] || fail "ADMIN_TRUSTED_PROXIES has no usable item"
  proxies="$validated"
  # A gateway inside the allowlist is the bypass this refuses: see the header of this file.
  for a in $allowed_rec; do
    for p in $rec; do
      if overlaps "$a" "$p"; then
        a_net="${a#*|*|*|}"
        p_net="${p#*|*|*|}"
        fail "ADMIN_TRUSTED_PROXIES: '$p_net' overlaps ADMIN_ALLOWED_CIDRS '$a_net'. A request whose forwarded chain is unusable is judged by the gateway's own address, so a gateway inside the allowlist would open the console to everyone behind it. Keep the two lists apart."
      fi
    done
  done
fi

out="$conf_dir/admin-access.conf"
{
  echo "# Generated by 19-admin-access.sh from the container's environment. Do not edit."
  if [ -n "$proxies" ]; then
    # The client is the first hop, read from the right, that is not one of these gateways.
    echo "real_ip_header $header;"
    echo "real_ip_recursive on;"
    for p in $proxies; do echo "set_real_ip_from $p;"; done
  fi
  for c in $allowed; do echo "allow $c;"; done
  echo "deny all;"
} >"$out"
