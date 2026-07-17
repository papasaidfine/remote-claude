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
VLESS_NODES="$TMP/vless-nodes.txt"
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

# --- optional reverse port ------------------------------------------------------
write_ssh_config_block '203.0.113.7' 'ubuntu' '22' '' 0 1 >/dev/null
cfg="$(cat "$SSH_CONFIG")"
check_absent 'no-rport: RemoteForward omitted'        "$cfg" 'RemoteForward'
check_absent 'no-rport: ExitOnForwardFailure omitted' "$cfg" 'ExitOnForwardFailure'
check 'no-rport: base fields still written' "$cfg" 'HostName 203.0.113.7'
check 'no-rport: keepalive still written'   "$cfg" 'ServerAliveInterval 30'
check 'no-rport: rport helper empty' "x$(config_block_rport)x" 'xx'
if status_rport; then
  printf 'FAIL - no-rport: status_rport should be false\n'; fail=1
else
  printf 'ok   - no-rport: status_rport false\n'
fi

write_ssh_config_block '203.0.113.7' 'ubuntu' '22' '2222' 0 1 >/dev/null
check 'rport: RemoteForward back' "$(cat "$SSH_CONFIG")" 'RemoteForward 127.0.0.1:2222 127.0.0.1:22'
check 'rport: helper reads port'  "x$(config_block_rport)x" 'x2222x'
status_rport && printf 'ok   - rport: status_rport true\n' \
  || { printf 'FAIL - rport: status_rport should be true\n'; fail=1; }

# --- run_proxy (menu item 7) -------------------------------------------------
# Reset to a known block without the proxy
write_ssh_config_block '203.0.113.7' 'ubuntu' '22' '2222' 0 1 >/dev/null

# No managed block -> error
if ( SSH_CONFIG="$TMP/no-such-config"; run_proxy ) >/dev/null 2>&1; then
  printf 'FAIL - run_proxy without a managed block should error\n'; fail=1
else
  printf 'ok   - run_proxy without a managed block errors\n'
fi

# xray not configured -> enabling errors and the config is untouched
if ( run_proxy ) >/dev/null 2>&1; then
  printf 'FAIL - run_proxy without xray configured should error\n'; fail=1
else
  printf 'ok   - run_proxy without xray configured errors\n'
fi
check_absent 'no xray: ProxyCommand still absent' "$(cat "$SSH_CONFIG")" 'ProxyCommand'

# Fake the xray artifacts so status_xray passes
mkdir -p "$TMP/bin"
printf 'vless://u@h.example:443?type=tcp#t\n' > "$VLESS_NODES"
: > "$XRAY_LAUNCHER"
printf '#!/bin/sh\n' > "$XRAY_VENDOR_BIN"
chmod +x "$XRAY_VENDOR_BIN"

# Toggle ON — the 'n' on stdin must not be consumed by any prompt
( run_proxy ) >/dev/null 2>&1 <<< 'n' || { printf 'FAIL - toggle on exited non-zero\n'; fail=1; }
cfg="$(cat "$SSH_CONFIG")"
check 'toggle on: ProxyCommand added'     "$cfg" "ProxyCommand $XRAY_LAUNCHER %h %p"
check 'toggle on: HostName preserved'     "$cfg" 'HostName 203.0.113.7'
check 'toggle on: User preserved'         "$cfg" 'User ubuntu'
check 'toggle on: reverse port preserved' "$cfg" 'RemoteForward 127.0.0.1:2222 127.0.0.1:22'
check 'toggle on: unmanaged content kept' "$cfg" 'Host other'

# Toggle OFF
( run_proxy ) >/dev/null 2>&1 <<< 'n' || { printf 'FAIL - toggle off exited non-zero\n'; fail=1; }
check_absent 'toggle off: ProxyCommand removed' "$(cat "$SSH_CONFIG")" 'ProxyCommand'
check 'toggle off: HostName preserved' "$(cat "$SSH_CONFIG")" 'HostName 203.0.113.7'

# Toggle ON again — nothing duplicated
( run_proxy ) >/dev/null 2>&1 || { printf 'FAIL - second toggle on exited non-zero\n'; fail=1; }
[[ "$(grep -cF "ProxyCommand $XRAY_LAUNCHER" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - re-toggle: exactly one ProxyCommand\n' \
  || { printf 'FAIL - re-toggle: ProxyCommand count != 1\n'; fail=1; }
[[ "$(grep -cF "$BEGIN_MARK" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - re-toggle: exactly one managed block\n' \
  || { printf 'FAIL - re-toggle: managed block count != 1\n'; fail=1; }

# --- run_rport (menu item 5) ------------------------------------------------------
if ( SSH_CONFIG="$TMP/no-such-config"; run_rport ) >/dev/null 2>&1; then
  printf 'FAIL - rport: should error without a managed block\n'; fail=1
else
  printf 'ok   - rport: errors without a managed block\n'
fi

# Base block without reverse port; REVERSE_PORT env drives it non-interactively
write_ssh_config_block '203.0.113.7' 'ubuntu' '22' '' 0 1 >/dev/null
( REVERSE_PORT=2400 run_rport ) >/dev/null 2>&1 \
  || { printf 'FAIL - rport: add exited non-zero\n'; fail=1; }
check 'rport: RemoteForward added' "$(cat "$SSH_CONFIG")" 'RemoteForward 127.0.0.1:2400 127.0.0.1:22'
check 'rport: base fields kept'    "$(cat "$SSH_CONFIG")" 'HostName 203.0.113.7'

# Interactive: default offers the current port; typed value replaces it
( run_rport ) >/dev/null 2>&1 <<< '2500' \
  || { printf 'FAIL - rport: update exited non-zero\n'; fail=1; }
check 'rport: port updated interactively' "$(cat "$SSH_CONFIG")" 'RemoteForward 127.0.0.1:2500 127.0.0.1:22'

# Bad port errors
if ( REVERSE_PORT=abc run_rport ) >/dev/null 2>&1; then
  printf 'FAIL - rport: non-numeric port should error\n'; fail=1
else
  printf 'ok   - rport: non-numeric port errors\n'
fi

exit $fail
