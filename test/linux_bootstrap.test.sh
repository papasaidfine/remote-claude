#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RC_SOURCED_FOR_TEST=1
# shellcheck source=/dev/null
source "$HERE/../local/bootstrap-linux.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SSH_DIR="$TMP/ssh"
SSH_CONFIG="$SSH_DIR/config"
AUTH_KEYS="$SSH_DIR/authorized_keys"

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

# --- base form: host + user, apply; no RemoteForward ------------------------------
( run_config ) >/dev/null 2>&1 <<'IN'
1
203.0.113.9
2
dave
a
IN
cfg="$(cat "$SSH_CONFIG")"
check 'form: host written'     "$cfg" 'HostName 203.0.113.9'
check 'form: user written'     "$cfg" 'User dave'
check 'form: default ssh port' "$cfg" 'Port 22'
check_absent 'form: no RemoteForward in base block' "$cfg" 'RemoteForward'

# --- run_rport: gate + add + preserve ----------------------------------------------
if ( SSH_CONFIG="$TMP/no-such"; run_rport ) >/dev/null 2>&1; then
  printf 'FAIL - rport: should error without a block\n'; fail=1
else
  printf 'ok   - rport: errors without a block\n'
fi
( REVERSE_PORT=2400 run_rport ) >/dev/null 2>&1 \
  || { printf 'FAIL - rport: add exited non-zero\n'; fail=1; }
cfg="$(cat "$SSH_CONFIG")"
check 'rport: RemoteForward added' "$cfg" 'RemoteForward 127.0.0.1:2400 127.0.0.1:22'
check 'rport: host preserved'      "$cfg" 'HostName 203.0.113.9'

# --- item 1 re-run preserves the reverse port --------------------------------------
( run_config ) >/dev/null 2>&1 <<'IN'
1
198.51.100.7
a
IN
cfg="$(cat "$SSH_CONFIG")"
check 'preserve: host updated'       "$cfg" 'HostName 198.51.100.7'
check 'preserve: RemoteForward kept' "$cfg" 'RemoteForward 127.0.0.1:2400 127.0.0.1:22'
[[ "$(grep -cF "$BEGIN_MARK" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - preserve: single managed block\n' \
  || { printf 'FAIL - preserve: managed block duplicated\n'; fail=1; }

# --- run_test: gate + adaptive two-call flow (mocked ssh) --------------------------
mkdir -p "$TMP/bin"
cat > "$TMP/bin/ssh" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$MOCK_LOG"
if [[ "$*" == *ClearAllForwardings* ]]; then echo ok; else echo tunnel-ok; fi
MOCK
chmod +x "$TMP/bin/ssh"
export PATH="$TMP/bin:$PATH" MOCK_LOG="$TMP/ssh.log"

if ( SSH_CONFIG="$TMP/no-such"; run_test ) >/dev/null 2>&1; then
  printf 'FAIL - test: should error without a block\n'; fail=1
else
  printf 'ok   - test: errors without a block\n'
fi
: > "$MOCK_LOG"
( run_test ) >/dev/null 2>&1 || { printf 'FAIL - test: full pass exited non-zero\n'; fail=1; }
check 'test: probes the rport' "$(cat "$MOCK_LOG")" '/dev/tcp/127.0.0.1/2400'
[[ "$(wc -l < "$MOCK_LOG")" -eq 2 ]] \
  && printf 'ok   - test: two ssh calls\n' \
  || { printf 'FAIL - test: expected 2 ssh calls\n'; fail=1; }

exit $fail
