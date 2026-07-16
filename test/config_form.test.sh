#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RC_SOURCED_FOR_TEST=1
# shellcheck source=/dev/null
source "$HERE/../local/bootstrap-macos.sh"

# Sandbox every path the form touches; sourced functions read these at call time.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SSH_DIR="$TMP/ssh"
SSH_CONFIG="$SSH_DIR/config"
AUTH_KEYS="$SSH_DIR/authorized_keys"
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

# --- fresh config: set host + user, apply; ports get defaults ------------------
( run_config ) >/dev/null 2>&1 <<'IN'
1
203.0.113.9
2
dave
a
IN
cfg="$(cat "$SSH_CONFIG")"
check 'apply: host written'            "$cfg" 'HostName 203.0.113.9'
check 'apply: user written'            "$cfg" 'User dave'
check 'apply: default ssh port'        "$cfg" 'Port 22'
check 'apply: default reverse port'    "$cfg" 'RemoteForward 127.0.0.1:2222 127.0.0.1:22'
check_absent 'apply: no proxy without xray' "$cfg" 'ProxyCommand'

# --- pre-fill from the block: edit only the reverse port, apply ----------------
( run_config ) >/dev/null 2>&1 <<'IN'
4
2345
a
IN
cfg="$(cat "$SSH_CONFIG")"
check 'prefill: host preserved'        "$cfg" 'HostName 203.0.113.9'
check 'prefill: user preserved'        "$cfg" 'User dave'
check 'prefill: reverse port updated'  "$cfg" 'RemoteForward 127.0.0.1:2345 127.0.0.1:22'
[[ "$(grep -cF "$BEGIN_MARK" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - prefill: single managed block\n' \
  || { printf 'FAIL - prefill: managed block duplicated\n'; fail=1; }

# --- cancel writes nothing ------------------------------------------------------
( run_config ) >/dev/null 2>&1 <<'IN'
1
changed.example
q
IN
check_absent 'cancel: edit not written' "$(cat "$SSH_CONFIG")" 'changed.example'

# --- EOF on the select prompt exits without writing ----------------------------
( run_config ) </dev/null >/dev/null 2>&1 \
  || { printf 'FAIL - eof: run_config should exit zero\n'; fail=1; }
check 'eof: config unchanged' "$(cat "$SSH_CONFIG")" 'HostName 203.0.113.9'

# --- validation: apply with empty host errors, form continues ------------------
out="$( ( SSH_CONFIG="$TMP/fresh-config"; run_config ) 2>&1 >/dev/null <<'IN'
a
1
h.example
2
u
a
IN
)"
check 'validate: empty host reported' "$out" 'Server host must not be empty'
check 'validate: fixed then applied'  "$(cat "$TMP/fresh-config")" 'HostName h.example'

# --- validation: non-numeric port rejected, then fixed --------------------------
out="$( ( run_config ) 2>&1 >/dev/null <<'IN'
3
abc
a
3
2200
a
IN
)"
check 'validate: bad port reported' "$out" 'SSH port must be a number'
check 'validate: fixed port applied' "$(cat "$SSH_CONFIG")" 'Port 2200'

# --- proxy row appears + toggles when xray is configured ------------------------
mkdir -p "$TMP/bin"
printf 'vless://u@h.example:443?type=tcp#t\n' > "$VLESS_NODES"
: > "$XRAY_LAUNCHER"
printf '#!/bin/sh\n' > "$XRAY_VENDOR_BIN"; chmod +x "$XRAY_VENDOR_BIN"
( run_config ) >/dev/null 2>&1 <<'IN'
5
a
IN
check 'proxy: toggled on and written' "$(cat "$SSH_CONFIG")" "ProxyCommand $XRAY_LAUNCHER %h %p"
( run_config ) >/dev/null 2>&1 <<'IN'
5
a
IN
check_absent 'proxy: toggled back off' "$(cat "$SSH_CONFIG")" 'ProxyCommand'

# --- non-interactive: SERVER_HOST + SERVER_USER skip the form -------------------
( SSH_CONFIG="$TMP/auto-config" SERVER_HOST=192.0.2.10 SERVER_USER=auto run_config ) </dev/null >/dev/null 2>&1 \
  || { printf 'FAIL - auto: run_config exited non-zero\n'; fail=1; }
cfg="$(cat "$TMP/auto-config")"
check 'auto: host written'  "$cfg" 'HostName 192.0.2.10'
check 'auto: user written'  "$cfg" 'User auto'
check 'auto: default ports' "$cfg" 'RemoteForward 127.0.0.1:2222 127.0.0.1:22'

exit $fail
