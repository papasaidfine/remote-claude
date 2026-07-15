#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RC_SOURCED_FOR_TEST=1
# shellcheck source=/dev/null
source "$HERE/../local/bootstrap-macos.sh"

# Sandbox every path the toggle touches; the sourced functions read these
# globals at call time.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SSH_CONFIG="$TMP/config"
XRAY_JSON="$TMP/xray.json"
XRAY_LAUNCHER="$TMP/xray-proxy.sh"
XRAY_VENDOR_BIN="$TMP/bin/xray"

fail=0
check() { # check <description> <haystack> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf 'ok   - %s\n' "$1"
  else
    printf 'FAIL - %s (missing: %s)\n' "$1" "$3"; fail=1
  fi
}
check_absent() { # check_absent <description> <haystack> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf 'FAIL - %s (unexpected: %s)\n' "$1" "$3"; fail=1
  else
    printf 'ok   - %s\n' "$1"
  fi
}

# A managed block below some unmanaged content
printf 'Host other\n    User bob\n' > "$SSH_CONFIG"
write_ssh_config_block '203.0.113.7' 'ubuntu' '22' '2222' 0 >/dev/null

# Values are wrapped in x...x so substring matches cannot pass by accident
check 'parse: HostName'      "x$(config_block_value HostName)x"      'x203.0.113.7x'
check 'parse: User'          "x$(config_block_value User)x"          'xubuntux'
check 'parse: Port'          "x$(config_block_value Port)x"          'x22x'
check 'parse: RemoteForward' "x$(config_block_value RemoteForward)x" 'x127.0.0.1:2222x'
check 'parse: missing key is empty' "x$(config_block_value ProxyCommand)x" 'xx'

# force=1 skips the "update it?" confirmation: the 'n' on stdin must NOT be
# consumed as an answer, so the rewrite happens anyway.
write_ssh_config_block '198.51.100.9' 'carol' '2200' '2345' 0 1 >/dev/null <<< 'n'
cfg="$(cat "$SSH_CONFIG")"
check 'force: block rewritten despite n on stdin' "$cfg" 'HostName 198.51.100.9'
check 'force: reverse port updated' "$cfg" 'RemoteForward 127.0.0.1:2345 127.0.0.1:22'
check 'force: unmanaged content kept' "$cfg" 'Host other'
[[ "$(grep -cF "$BEGIN_MARK" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - force: single managed block\n' \
  || { printf 'FAIL - force: managed block duplicated\n'; fail=1; }

exit $fail
