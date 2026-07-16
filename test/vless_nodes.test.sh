#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RC_SOURCED_FOR_TEST=1
# shellcheck source=/dev/null
source "$HERE/../local/bootstrap-macos.sh"

# Sandbox every path item 6 touches; sourced functions read these at call time.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
RC_CONFIG_DIR="$TMP/rc"
VLESS_NODES="$RC_CONFIG_DIR/vless-nodes.txt"
XRAY_JSON="$RC_CONFIG_DIR/xray.json"
XRAY_LAUNCHER="$RC_CONFIG_DIR/xray-proxy.sh"
XRAY_VENDOR_BIN="$RC_CONFIG_DIR/bin/xray"
mkdir -p "$RC_CONFIG_DIR"

fail=0
check() { # check <description> <haystack> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf 'ok   - %s\n' "$1"
  else
    printf 'FAIL - %s (missing: %s)\n' "$1" "$3"; fail=1
  fi
}

# --- read_vless_nodes: comments and blanks dropped -----------------------------
cat > "$VLESS_NODES" <<'EOF'

# comment line
vless://uuid-a@a.example:443?type=tcp#node-a
   # indented comment
vless://uuid-b@b.example:443?type=tcp#node-b
EOF
nodes="$(read_vless_nodes "$VLESS_NODES")"
check 'nodes: node-a kept' "$nodes" 'vless://uuid-a@a.example:443?type=tcp#node-a'
check 'nodes: node-b kept' "$nodes" 'vless://uuid-b@b.example:443?type=tcp#node-b'
if [[ "$(printf '%s\n' "$nodes" | grep -c 'vless://')" == 2 ]]; then
  printf 'ok   - nodes: exactly 2 nodes survive filtering\n'
else
  printf 'FAIL - nodes: expected exactly 2 nodes\n'; fail=1
fi

# --- pick_random_node -----------------------------------------------------------
ok_pick=1
for _ in 1 2 3 4 5; do
  picked="$(pick_random_node "$VLESS_NODES")" || ok_pick=0
  case "$picked" in
    'vless://uuid-a@a.example:443?type=tcp#node-a') ;;
    'vless://uuid-b@b.example:443?type=tcp#node-b') ;;
    *) ok_pick=0 ;;
  esac
done
if [[ $ok_pick == 1 ]]; then
  printf 'ok   - pick: always one of the listed nodes\n'
else
  printf 'FAIL - pick: returned something outside the node set\n'; fail=1
fi

printf 'vless://only@one.example:443?type=tcp#solo\n' > "$TMP/one.txt"
check 'pick: single-node file returns it' "$(pick_random_node "$TMP/one.txt")" 'vless://only@one.example'

if pick_random_node "$TMP/missing.txt" >/dev/null 2>&1; then
  printf 'FAIL - pick: missing file should error\n'; fail=1
else
  printf 'ok   - pick: missing file errors\n'
fi

printf '# only comments here\n\n' > "$TMP/comments.txt"
if pick_random_node "$TMP/comments.txt" >/dev/null 2>&1; then
  printf 'FAIL - pick: comments-only file should error\n'; fail=1
else
  printf 'ok   - pick: comments-only file errors\n'
fi

# --- write_xray_launcher: embeds the parser, reads the nodes file --------------
write_xray_launcher >/dev/null
if bash -n "$XRAY_LAUNCHER"; then
  printf 'ok   - launcher: bash -n clean\n'
else
  printf 'FAIL - launcher: syntax error\n'; fail=1
fi
src="$(cat "$XRAY_LAUNCHER")"
check 'launcher: embeds vless_url_to_json' "$src" 'vless_url_to_json ()'
check 'launcher: embeds urldecode'         "$src" 'urldecode ()'
check 'launcher: embeds read_vless_nodes'  "$src" 'read_vless_nodes ()'
check 'launcher: embeds pick_random_node'  "$src" 'pick_random_node ()'
check 'launcher: embeds err'               "$src" 'err ()'
check 'launcher: reads the nodes file'     "$src" 'vless-nodes.txt'
check 'launcher: writes current config'    "$src" 'xray-current.json'
check 'launcher: still a SOCKS bridge'     "$src" 'nc -X 5 -x'

exit $fail
