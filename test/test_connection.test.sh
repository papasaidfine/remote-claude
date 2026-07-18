#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RC_SOURCED_FOR_TEST=1
# shellcheck source=/dev/null
source "$HERE/../local/bootstrap-macos.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SSH_DIR="$TMP/ssh"
SSH_CONFIG="$TMP/config"
XRAY_LAUNCHER="$TMP/xray-proxy.sh"

fail=0
check() { # check <description> <haystack> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf 'ok   - %s\n' "$1"
  else
    printf 'FAIL - %s (missing: %s)\n' "$1" "$3"; fail=1
  fi
}

# Mock ssh on PATH: logs each argv line, behaviour driven by MOCK_MODE
mkdir -p "$TMP/bin"
cat > "$TMP/bin/ssh" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$MOCK_LOG"
case "${MOCK_MODE:-ok}" in
  ok)
    if [[ "$*" == *ClearAllForwardings* ]]; then echo ok; else echo tunnel-ok; fi ;;
  no-login)  echo 'Permission denied (publickey).' >&2; exit 255 ;;
  no-tunnel) if [[ "$*" == *ClearAllForwardings* ]]; then echo ok; else exit 1; fi ;;
esac
MOCK
chmod +x "$TMP/bin/ssh"
export PATH="$TMP/bin:$PATH"
export MOCK_LOG="$TMP/ssh.log" MOCK_MODE=ok

# --- gate: no managed block -> dies pointing at item 1 ---------------------------
if out="$( ( SSH_CONFIG="$TMP/no-such"; run_test ) 2>&1 )"; then
  printf 'FAIL - gate: should error without a block\n'; fail=1
else
  printf 'ok   - gate: errors without a block\n'
fi
check 'gate: points at item 1' "$out" 'run item 1 first'

# --- full pass with reverse port -------------------------------------------------
write_ssh_config_block 'h.example' 'dave' '22' '2345' 0 1 >/dev/null
: > "$MOCK_LOG"
out="$( run_test 2>&1 )" || { printf 'FAIL - full pass exited non-zero\n'; fail=1; }
calls="$(cat "$MOCK_LOG")"
check 'outbound: BatchMode'       "$calls" 'BatchMode=yes'
check 'outbound: no forwards'     "$calls" 'ClearAllForwardings=yes'
check 'tunnel: forward tolerated' "$calls" 'ExitOnForwardFailure=no'
check 'tunnel: probes the rport'  "$calls" '/dev/tcp/127.0.0.1/2345'
[[ "$(wc -l < "$MOCK_LOG")" -eq 2 ]] \
  && printf 'ok   - exactly two ssh calls\n' \
  || { printf 'FAIL - expected 2 ssh calls\n'; fail=1; }
check 'report: outbound ok' "$out" 'outbound'
check 'report: tunnel ok'   "$out" 'reverse tunnel'
check 'report: direct path' "$out" 'direct'

# --- no reverse port: tunnel check skipped ---------------------------------------
write_ssh_config_block 'h.example' 'dave' '22' '' 0 1 >/dev/null
: > "$MOCK_LOG"
out="$( run_test 2>&1 )" || { printf 'FAIL - no-rport run exited non-zero\n'; fail=1; }
[[ "$(wc -l < "$MOCK_LOG")" -eq 1 ]] \
  && printf 'ok   - one ssh call without rport\n' \
  || { printf 'FAIL - expected 1 ssh call\n'; fail=1; }
check 'skip: mentions item 6' "$out" 'item 6'

# --- outbound failure: hint + non-zero, tunnel not probed ------------------------
write_ssh_config_block 'h.example' 'dave' '22' '2345' 0 1 >/dev/null
: > "$MOCK_LOG"
if out="$( MOCK_MODE=no-login run_test 2>&1 )"; then
  printf 'FAIL - no-login: should fail\n'; fail=1
else
  printf 'ok   - no-login: fails\n'
fi
check 'fail: authorize hint'   "$out" 'authorized your local key'
check 'fail: shows ssh stderr' "$out" 'Permission denied'
[[ "$(wc -l < "$MOCK_LOG")" -eq 1 ]] \
  && printf 'ok   - tunnel not probed after login failure\n' \
  || { printf 'FAIL - unexpected extra ssh calls\n'; fail=1; }

# --- tunnel failure: phase-2 hint + non-zero -------------------------------------
: > "$MOCK_LOG"
if out="$( MOCK_MODE=no-tunnel run_test 2>&1 )"; then
  printf 'FAIL - no-tunnel: should fail\n'; fail=1
else
  printf 'ok   - no-tunnel: fails\n'
fi
check 'fail: phase-2 hint' "$out" 'reverse port (item 6)'

# --- xray path reported when the proxy is on -------------------------------------
write_ssh_config_block 'h.example' 'dave' '22' '2345' 1 1 >/dev/null
out="$( run_test 2>&1 )" || { printf 'FAIL - proxy-path run exited non-zero\n'; fail=1; }
check 'report: xray path' "$out" 'via xray'

exit $fail
