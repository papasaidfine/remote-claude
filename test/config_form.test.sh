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

# --- fresh config: set host + user, apply; NO reverse port, NO proxy -----------
( run_config ) >/dev/null 2>&1 <<'IN'
1
203.0.113.9
2
dave
a
IN
cfg="$(cat "$SSH_CONFIG")"
check 'apply: host written'      "$cfg" 'HostName 203.0.113.9'
check 'apply: user written'      "$cfg" 'User dave'
check 'apply: default ssh port'  "$cfg" 'Port 22'
check_absent 'apply: no RemoteForward in base block' "$cfg" 'RemoteForward'
check_absent 'apply: no proxy in base block'         "$cfg" 'ProxyCommand'

# --- pre-fill: edit only the ssh port, host/user survive ------------------------
( run_config ) >/dev/null 2>&1 <<'IN'
3
2200
a
IN
cfg="$(cat "$SSH_CONFIG")"
check 'prefill: host preserved' "$cfg" 'HostName 203.0.113.9'
check 'prefill: user preserved' "$cfg" 'User dave'
check 'prefill: port updated'   "$cfg" 'Port 2200'
[[ "$(grep -cF "$BEGIN_MARK" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - prefill: single managed block\n' \
  || { printf 'FAIL - prefill: managed block duplicated\n'; fail=1; }

# --- cross-preservation: item 1 must keep RemoteForward + ProxyCommand ----------
write_ssh_config_block '203.0.113.9' 'dave' '2200' '2345' 1 1 >/dev/null
( run_config ) >/dev/null 2>&1 <<'IN'
1
198.51.100.7
a
IN
cfg="$(cat "$SSH_CONFIG")"
check 'preserve: host updated'          "$cfg" 'HostName 198.51.100.7'
check 'preserve: RemoteForward kept'    "$cfg" 'RemoteForward 127.0.0.1:2345 127.0.0.1:22'
check 'preserve: ExitOnForwardFailure'  "$cfg" 'ExitOnForwardFailure yes'
check 'preserve: ProxyCommand kept'     "$cfg" "ProxyCommand $XRAY_LAUNCHER %h %p"

# --- cancel / EOF write nothing --------------------------------------------------
( run_config ) >/dev/null 2>&1 <<'IN'
1
changed.example
q
IN
check_absent 'cancel: edit not written' "$(cat "$SSH_CONFIG")" 'changed.example'
( run_config ) </dev/null >/dev/null 2>&1 \
  || { printf 'FAIL - eof: run_config should exit zero\n'; fail=1; }
check 'eof: config unchanged' "$(cat "$SSH_CONFIG")" 'HostName 198.51.100.7'

# --- validation: empty host reported, form continues -----------------------------
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

# --- non-interactive: SERVER_HOST + SERVER_USER skip the form --------------------
( SSH_CONFIG="$TMP/auto-config" SERVER_HOST=192.0.2.10 SERVER_USER=auto run_config ) </dev/null >/dev/null 2>&1 \
  || { printf 'FAIL - auto: run_config exited non-zero\n'; fail=1; }
cfg="$(cat "$TMP/auto-config")"
check 'auto: host written' "$cfg" 'HostName 192.0.2.10'
check 'auto: user written' "$cfg" 'User auto'
check_absent 'auto: no RemoteForward' "$cfg" 'RemoteForward'

exit $fail
