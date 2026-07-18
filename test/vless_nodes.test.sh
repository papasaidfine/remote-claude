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

# --- version helpers -------------------------------------------------------------
mkdir -p "$RC_CONFIG_DIR/bin"
printf '#!/bin/sh\necho "Xray 25.0.0 (Xray, Penetrates Everything.)"\n' > "$XRAY_VENDOR_BIN"
chmod +x "$XRAY_VENDOR_BIN"
check 'version: local parsed from binary' "x$(xray_local_version "$XRAY_VENDOR_BIN")x" 'x25.0.0x'

# --- ask_dl_proxy: optional download proxy ---------------------------------------
# Host machines may have proxy vars set; clear them so the checks are honest.
unset https_proxy HTTPS_PROXY all_proxy ALL_PROXY
got="$(printf 'http://127.0.0.1:7890\n' | { ask_dl_proxy >/dev/null 2>&1; printf '%s|%s|%s|%s' "${https_proxy:-}" "${HTTPS_PROXY:-}" "${all_proxy:-}" "${ALL_PROXY:-}"; })"
if [[ "$got" == 'http://127.0.0.1:7890|http://127.0.0.1:7890|http://127.0.0.1:7890|http://127.0.0.1:7890' ]]; then
  printf 'ok   - proxy: entered proxy exported to all four vars\n'
else
  printf 'FAIL - proxy: expected all four vars exported, got %s\n' "$got"; fail=1
fi
got="$(printf '\n' | { ask_dl_proxy >/dev/null 2>&1; printf '%s' "${https_proxy:-unset}"; })"
if [[ "$got" == 'unset' ]]; then
  printf 'ok   - proxy: empty answer leaves the environment alone\n'
else
  printf 'FAIL - proxy: empty answer should not export, got %s\n' "$got"; fail=1
fi

# --- run_xray (item 6): update flow via overrides ---------------------------------
# Stub the network-facing pieces; overrides are inherited by the ( run_xray ) subshells.
nc() { :; }
install_xray_release() { : > "$TMP/downloaded"; }
xray_latest_version() { printf '%s\n' "$FAKE_LATEST"; }
export FAKE_LATEST

: > "$XRAY_JSON"        # stale pre-nodes-file config that item 6 must remove
rm -f "$VLESS_NODES" "$TMP/downloaded"

FAKE_LATEST=25.0.0
( run_xray ) </dev/null >/dev/null 2>&1 \
  || { printf 'FAIL - item6: up-to-date run exited non-zero\n'; fail=1; }
[[ ! -f "$TMP/downloaded" ]] \
  && printf 'ok   - item6: up-to-date -> no download\n' \
  || { printf 'FAIL - item6: downloaded despite matching version\n'; fail=1; }
[[ ! -f "$XRAY_JSON" ]] \
  && printf 'ok   - item6: stale xray.json removed\n' \
  || { printf 'FAIL - item6: stale xray.json still present\n'; fail=1; }
[[ -x "$XRAY_LAUNCHER" ]] \
  && printf 'ok   - item6: launcher written\n' \
  || { printf 'FAIL - item6: launcher missing\n'; fail=1; }

# Nodes template created without prompting: comments only, no nodes
check 'item6: nodes template has comments' "$(cat "$VLESS_NODES")" '# '
if [[ -z "$(read_vless_nodes "$VLESS_NODES")" ]]; then
  printf 'ok   - item6: template contains no nodes\n'
else
  printf 'FAIL - item6: template should contain no nodes\n'; fail=1
fi

FAKE_LATEST=26.0.0
( run_xray ) </dev/null >/dev/null 2>&1
[[ -f "$TMP/downloaded" ]] \
  && printf 'ok   - item6: newer release -> vendor download\n' \
  || { printf 'FAIL - item6: should download on version mismatch\n'; fail=1; }

FAKE_LATEST=""
rm -f "$TMP/downloaded"
out="$( ( run_xray ) </dev/null 2>/dev/null )"   # warn() prints to stdout
check 'item6: unreachable API warns' "$out" 'Could not check'
[[ ! -f "$TMP/downloaded" ]] \
  && printf 'ok   - item6: unreachable API -> no download\n' \
  || { printf 'FAIL - item6: downloaded without version info\n'; fail=1; }

# VLESS_URL seeds the first node when creating the file
rm -f "$VLESS_NODES"
FAKE_LATEST=25.0.0
( VLESS_URL='vless://seed@s.example:443?type=tcp#seeded' run_xray ) </dev/null >/dev/null 2>&1
check 'item6: VLESS_URL seeded' "$(cat "$VLESS_NODES")" 'vless://seed@s.example:443?type=tcp#seeded'
rm -f "$VLESS_NODES"
if ( VLESS_URL='vless://bad@h:1?security=weird&type=tcp' run_xray ) </dev/null >/dev/null 2>&1; then
  printf 'FAIL - item6: bad VLESS_URL should error when seeding\n'; fail=1
else
  printf 'ok   - item6: bad VLESS_URL errors when seeding\n'
fi

# An existing nodes file is left completely alone
printf 'vless://keep@k.example:443?type=tcp#keep\n' > "$VLESS_NODES"
( run_xray ) </dev/null >/dev/null 2>&1
check 'item6: existing nodes file untouched' "$(cat "$VLESS_NODES")" 'vless://keep@k.example'

# The proxy answer must be visible to the download step (update path re-downloads)
FAKE_LATEST=27.0.0
install_xray_release() { printf '%s' "${https_proxy:-none}" > "$TMP/proxy-seen"; }
( run_xray ) <<< 'http://127.0.0.1:7890' >/dev/null 2>&1
check 'item6: proxy reaches the download step' "$(cat "$TMP/proxy-seen")" 'http://127.0.0.1:7890'
( run_xray ) </dev/null >/dev/null 2>&1
check 'item6: direct download when no proxy entered' "$(cat "$TMP/proxy-seen")" 'none'

# --- status_xray: nodes no longer required ----------------------------------------
rm -f "$VLESS_NODES"
if status_xray; then
  printf 'ok   - status: ready with launcher + binary (no nodes needed)\n'
else
  printf 'FAIL - status: should be ready without nodes file\n'; fail=1
fi
rm -f "$XRAY_LAUNCHER"
if status_xray; then
  printf 'FAIL - status: should not be ready without launcher\n'; fail=1
else
  printf 'ok   - status: not ready without launcher\n'
fi

exit $fail
