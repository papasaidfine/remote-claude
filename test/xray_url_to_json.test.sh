#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RC_SOURCED_FOR_TEST=1
# shellcheck source=/dev/null
source "$HERE/../local/bootstrap-macos.sh"

fail=0
check() { # check <description> <haystack> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf 'ok   - %s\n' "$1"
  else
    printf 'FAIL - %s (missing: %s)\n' "$1" "$3"; fail=1
  fi
}

# Reality + tcp + vision
u='vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=PUBKEYXYZ&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#node'
j="$(vless_url_to_json "$u")"
check 'reality: uuid'       "$j" '"id": "11111111-2222-3333-4444-555555555555"'
check 'reality: address'    "$j" '"address": "example.com"'
check 'reality: server port' "$j" '"port": 443'
check 'reality: security'   "$j" '"security": "reality"'
check 'reality: pbk'        "$j" '"publicKey": "PUBKEYXYZ"'
check 'reality: sid'        "$j" '"shortId": "ab12"'
check 'reality: sni'        "$j" '"serverName": "www.microsoft.com"'
check 'reality: flow'       "$j" '"flow": "xtls-rprx-vision"'
check 'reality: socks port' "$j" '"port": 10808'

# TLS + ws (path is percent-encoded)
u2='vless://aaaa@host.tld:8443?type=ws&security=tls&sni=host.tld&path=%2Fws&host=host.tld'
j2="$(vless_url_to_json "$u2")"
check 'tls-ws: security'  "$j2" '"security": "tls"'
check 'tls-ws: ws path'   "$j2" '"path": "/ws"'
check 'tls-ws: ws host'   "$j2" '"Host": "host.tld"'

# Unsupported security must error (non-zero)
if vless_url_to_json 'vless://x@h:1?security=weird&type=tcp' >/dev/null 2>&1; then
  printf 'FAIL - unsupported security should error\n'; fail=1
else
  printf 'ok   - unsupported security errors\n'
fi

exit $fail
