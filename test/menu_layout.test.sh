#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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
strip_ansi() { sed -E $'s/\x1b\\[[0-9;]*m//g'; }

menu_of() { # menu_of <script> -> banner + menu on stdout (sandboxed paths)
  bash -c '
    export RC_SOURCED_FOR_TEST=1
    source "$1"
    TMP="$(mktemp -d)"; trap "rm -rf \"$TMP\"" EXIT
    SSH_CONFIG="$TMP/none"; AUTH_KEYS="$TMP/none"; KEY_PATH="$TMP/none"
    SSHD_CONFIG="$TMP/none"; SSHD_DROPIN="$TMP/none"
    XRAY_LAUNCHER="$TMP/none"; XRAY_VENDOR_BIN="$TMP/none"
    print_banner
    draw_menu
  ' _ "$1"
}

m="$(menu_of "$HERE/../local/bootstrap-macos.sh" | strip_ansi)"
check 'macos: phase 1 header' "$m" '① Local ──▶ Claude'
check 'macos: phase 2 header' "$m" '② Claude ──▶ Local'
check 'macos: phase 3 header' "$m" '③ xray ═[ ssh ]═▶'
check 'macos: item 1 config'  "$m" '1) SSH config shortcut'
check 'macos: item 2 key'     "$m" '2) Local SSH key'
check 'macos: item 3 test'    "$m" '3) Test connection'
check 'macos: item 4 sshd'    "$m" '4) Incoming SSH'
check 'macos: item 5 auth'    "$m" "5) Authorize the server's connect-back key"
check 'macos: item 6 rport'   "$m" '6) Reverse tunnel port'
check 'macos: item 7 xray'    "$m" '7) xray client'
check 'macos: item 8 route'   "$m" '8) Route the tunnel through xray'
check 'macos: banner box'     "$m" '╭'
check 'macos: banner title'   "$m" 'remote-claude · reverse SSH bootstrap (macOS)'
check_absent 'macos: show-key item gone' "$m" 'Show local public key'
# Box alignment: every interior banner line must still end with │
# (catches editors stripping the trailing padding spaces)
if printf '%s\n' "$m" | awk '/^│/ && !/│$/ { bad = 1 } END { exit bad }'; then
  printf 'ok   - macos: banner right border aligned\n'
else
  printf 'FAIL - macos: banner right border misaligned\n'; fail=1
fi

# --- Linux assertions are enabled in the Linux task ---
# l="$(menu_of "$HERE/../local/bootstrap-linux.sh" | strip_ansi)"
# check 'linux: phase 1 header' "$l" '① Local ──▶ Claude'
# check 'linux: item 3 test'    "$l" '3) Test connection'
# check 'linux: item 6 rport'   "$l" '6) Reverse tunnel port'
# check 'linux: banner title'   "$l" 'remote-claude · reverse SSH bootstrap (Linux)'
# check_absent 'linux: no xray phase' "$l" 'xray'
# check_absent 'linux: show-key item gone' "$l" 'Show local public key'
# if printf '%s\n' "$l" | awk '/^│/ && !/│$/ { bad = 1 } END { exit bad }'; then
#   printf 'ok   - linux: banner right border aligned\n'
# else
#   printf 'FAIL - linux: banner right border misaligned\n'; fail=1
# fi

exit $fail
