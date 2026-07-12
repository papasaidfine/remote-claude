# macOS VLESS/xray Client SSH Tunnel — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a macOS-only bootstrap menu item that turns a pasted `vless://` URL into a local xray SOCKS proxy, and route the `remote-claude` SSH tunnel through it via `ProxyCommand`, so VSCode Remote-SSH / `ssh remote-claude -t "claude"` auto-tunnel through xray.

**Architecture:** All logic lives in `local/bootstrap-macos.sh` as new `status_xray`/`run_xray` plus helpers; the launcher (`~/.config/remote-claude/xray-proxy.sh`) and config (`~/.config/remote-claude/xray.json`) are generated at runtime in the user's home. xray is started on demand by the `ProxyCommand` launcher — no daemon. Item 4 gains an optional `ProxyCommand` line that it solely owns.

**Tech Stack:** Bash (macOS default bash 3.2 — keep array code 3.2-safe), xray-core, OpenBSD `nc` (SOCKS5), Homebrew or a downloaded release binary.

**Spec:** `docs/superpowers/specs/2026-07-12-vless-xray-client-tunnel-design.md`

---

## File structure

- **Modify** `local/bootstrap-macos.sh`
  - New config vars (`RC_CONFIG_DIR`, `XRAY_JSON`, `XRAY_LAUNCHER`, `XRAY_VENDOR_BIN`, `SOCKS_PORT`, `XRAY_LOG`).
  - New helpers: `urldecode`, `vless_url_to_json`, `xray_bin`, `install_xray`, `write_xray_config`, `write_xray_launcher`, `config_proxy_on`.
  - New menu pair: `status_xray`, `run_xray`.
  - Modify `write_ssh_config_block` (optional `ProxyCommand`), `run_config` (proxy prompt), `draw_menu` (item 6 + item-4 `[xray]` marker), the select prompt, and the menu `case`.
  - Guard the top-level menu loop behind `RC_SOURCED_FOR_TEST` so the file can be `source`d by tests.
- **Create** `test/xray_url_to_json.test.sh` — sources the bootstrap and asserts on generated JSON. (Dev artifact; does not affect the curl-one-liner runtime.)
- **Modify** `README.md`, `README.zh-CN.md`, `TROUBLESHOOTING.md`, `TROUBLESHOOTING.zh-CN.md` — document the item, auto-tunnel behavior, and the stop one-liner.

Only the macOS bootstrap changes this iteration; the Linux/Windows near-copies are intentionally left for a later parity pass (see spec Non-goals).

---

## Task 1: Config vars + source guard

**Files:**
- Modify: `local/bootstrap-macos.sh:41` (after the `TS=` line) and `local/bootstrap-macos.sh:350-364` (the menu loop).

- [ ] **Step 1: Add config vars** after the `TS="$(date ...)"` line (currently line 41)

```bash
# xray / VLESS client (item 6)
RC_CONFIG_DIR="$HOME/.config/remote-claude"
XRAY_JSON="$RC_CONFIG_DIR/xray.json"
XRAY_LAUNCHER="$RC_CONFIG_DIR/xray-proxy.sh"
XRAY_VENDOR_BIN="$RC_CONFIG_DIR/bin/xray"
XRAY_LOG="$RC_CONFIG_DIR/xray.log"
SOCKS_PORT=10808
```

- [ ] **Step 2: Guard the menu loop.** Wrap the existing top-level `while true; do ... done` block and its trailing `echo` (currently lines 350-364) so sourcing for tests skips it:

```bash
if [[ -z "${RC_SOURCED_FOR_TEST:-}" ]]; then
while true; do
  draw_menu
  read -r -p "Select [1-6, q]: " choice || break
  case "$choice" in
    1) run_item run_sshd ;;
    2) run_item run_key ;;
    3) run_item run_authorize ;;
    4) run_item run_config ;;
    5) run_item run_show_key ;;
    6) run_item run_xray ;;
    q|Q) break ;;
    *) warn "Unknown selection: $choice" ;;
  esac
done

echo
fi
```

(This step already folds in the item-6 `case` line and the `[1-6, q]` prompt from Task 6; Task 6 only adds the `draw_menu` label.)

- [ ] **Step 3: Verify the script still parses**

Run: `bash -n local/bootstrap-macos.sh`
Expected: no output, exit 0.

- [ ] **Step 4: Verify sourcing does not launch the menu**

Run: `RC_SOURCED_FOR_TEST=1 bash -c 'source local/bootstrap-macos.sh && echo SOURCED_OK'`
Expected: prints `SOURCED_OK` and returns immediately (no menu drawn). It will warn about `run_xray`/`status_xray` being undefined only if referenced — they are not referenced at source time, so no error.

- [ ] **Step 5: Commit**

```bash
git add local/bootstrap-macos.sh
git commit -m "Add xray config vars and source guard to macOS bootstrap"
```

---

## Task 2: `urldecode` + `vless_url_to_json` (TDD)

**Files:**
- Create: `test/xray_url_to_json.test.sh`
- Modify: `local/bootstrap-macos.sh` (add helpers before the `# --- status checks` section, near the other write helpers ~line 116).

- [ ] **Step 1: Write the failing test** — create `test/xray_url_to_json.test.sh`

```bash
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
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `bash test/xray_url_to_json.test.sh`
Expected: FAIL — `vless_url_to_json: command not found` (or unbound) because the function does not exist yet.

- [ ] **Step 3: Implement the helpers** — insert into `local/bootstrap-macos.sh` just before the `# ---- status checks` comment (~line 316)

```bash
# ---------------------------------------------------------------- xray / VLESS
urldecode() { # urldecode <string>
  local s="${1//+/ }"
  printf '%b' "${s//%/\\x}"
}

vless_url_to_json() { # vless_url_to_json <vless://...>  -> JSON on stdout
  local url="$1"
  case "$url" in vless://*) ;; *) err "Not a vless:// URL"; return 1 ;; esac
  local rest="${url#vless://}"
  rest="${rest%%#*}"                       # drop #fragment
  local uuid="${rest%%@*}"
  local after="${rest#*@}"                  # host:port[?query]
  local hostport="${after%%\?*}"
  local query=""
  [[ "$after" == *\?* ]] && query="${after#*\?}"
  local host="${hostport%%:*}"
  local port="${hostport##*:}"
  [[ -n "$uuid" && -n "$host" && -n "$port" ]] \
    || { err "Malformed vless:// URL (need uuid@host:port)"; return 1; }

  local type=tcp security=none flow="" sni="" fp="" pbk="" sid="" alpn="" path="" hosthdr="" servicename=""
  local -a pairs=()
  [[ -n "$query" ]] && IFS='&' read -ra pairs <<< "$query"
  if [[ ${#pairs[@]} -gt 0 ]]; then
    local pair k v
    for pair in "${pairs[@]}"; do
      k="${pair%%=*}"; v="${pair#*=}"; v="$(urldecode "$v")"
      case "$k" in
        type|network) type="$v" ;;
        security)     security="$v" ;;
        flow)         flow="$v" ;;
        sni)          sni="$v" ;;
        fp)           fp="$v" ;;
        pbk)          pbk="$v" ;;
        sid)          sid="$v" ;;
        alpn)         alpn="$v" ;;
        path)         path="$v" ;;
        host)         hosthdr="$v" ;;
        serviceName)  servicename="$v" ;;
      esac
    done
  fi

  [[ -z "$security" ]] && security=none
  case "$security" in reality|tls|none) ;; *) err "Unsupported security='$security' (supported: reality, tls, none)"; return 1 ;; esac
  case "$type" in tcp|ws|grpc) ;; *) err "Unsupported network type='$type' (supported: tcp, ws, grpc)"; return 1 ;; esac

  local security_json="" transport_json=""
  case "$security" in
    reality)
      [[ -n "$pbk" ]] || { err "reality requires pbk (publicKey) in the URL"; return 1; }
      security_json=$(cat <<JSON
        "security": "reality",
        "realitySettings": {
          "serverName": "$sni",
          "fingerprint": "${fp:-chrome}",
          "publicKey": "$pbk",
          "shortId": "$sid",
          "spiderX": ""
        },
JSON
) ;;
    tls)
      local alpn_json="[]"
      [[ -n "$alpn" ]] && alpn_json="[\"${alpn//,/\",\"}\"]"
      security_json=$(cat <<JSON
        "security": "tls",
        "tlsSettings": {
          "serverName": "$sni",
          "fingerprint": "${fp:-chrome}",
          "alpn": $alpn_json
        },
JSON
) ;;
    none)
      security_json='        "security": "none",' ;;
  esac

  case "$type" in
    ws)
      transport_json=$(cat <<JSON
        "wsSettings": {
          "path": "${path:-/}",
          "headers": { "Host": "$hosthdr" }
        }
JSON
) ;;
    grpc)
      transport_json=$(cat <<JSON
        "grpcSettings": {
          "serviceName": "$servicename"
        }
JSON
) ;;
    tcp)
      transport_json='        "tcpSettings": {}' ;;
  esac

  local flow_json=""
  [[ -n "$flow" ]] && flow_json=",
                \"flow\": \"$flow\""

  cat <<JSON
{
  "log": { "loglevel": "warning" },
  "inbounds": [
    {
      "listen": "127.0.0.1",
      "port": $SOCKS_PORT,
      "protocol": "socks",
      "settings": { "udp": true }
    }
  ],
  "outbounds": [
    {
      "protocol": "vless",
      "settings": {
        "vnext": [
          {
            "address": "$host",
            "port": $port,
            "users": [
              {
                "id": "$uuid",
                "encryption": "none"$flow_json
              }
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "$type",
$security_json
$transport_json
      }
    }
  ]
}
JSON
}
```

- [ ] **Step 4: Run the test to confirm it passes**

Run: `bash test/xray_url_to_json.test.sh`
Expected: all lines `ok   - ...`, exit 0.

- [ ] **Step 5: Sanity-check the JSON is well-formed** (if `python3`/`jq` is available)

Run: `RC_SOURCED_FOR_TEST=1 bash -c 'source local/bootstrap-macos.sh; vless_url_to_json "vless://u@h.tld:443?type=tcp&security=reality&pbk=K&sid=1a&sni=a.b&flow=xtls-rprx-vision"' | python3 -m json.tool >/dev/null && echo VALID_JSON`
Expected: `VALID_JSON`. (Skip if no `python3`; the test in Step 4 is the gate.)

- [ ] **Step 6: Commit**

```bash
git add local/bootstrap-macos.sh test/xray_url_to_json.test.sh
git commit -m "Parse vless:// URL into xray JSON config (item 6 core)"
```

---

## Task 3: xray binary resolution + install + status

**Files:**
- Modify: `local/bootstrap-macos.sh` (add after the xray helpers from Task 2).

- [ ] **Step 1: Add `xray_bin`, `install_xray`, and `status_xray`**

```bash
xray_bin() { # echo path to an xray binary, or return 1
  if [[ -x "$XRAY_VENDOR_BIN" ]]; then printf '%s\n' "$XRAY_VENDOR_BIN"; return 0; fi
  command -v xray 2>/dev/null
}

install_xray() {
  if xray_bin >/dev/null; then log "xray already available: $(xray_bin)"; return 0; fi
  if command -v brew >/dev/null 2>&1; then
    log "Installing xray via Homebrew"
    brew install xray || die "brew install xray failed"
    return 0
  fi
  log "Homebrew not found; downloading the xray-core release binary"
  local asset tmp
  case "$(uname -m)" in
    arm64)  asset="Xray-macos-arm64-v8a.zip" ;;
    x86_64) asset="Xray-macos-64.zip" ;;
    *) die "Unsupported macOS arch: $(uname -m)" ;;
  esac
  mkdir -p "$(dirname "$XRAY_VENDOR_BIN")"
  tmp="$(mktemp -d)"
  curl -fsSL "https://github.com/XTLS/Xray-core/releases/latest/download/$asset" -o "$tmp/xray.zip" \
    || die "Failed to download $asset"
  unzip -o "$tmp/xray.zip" xray -d "$(dirname "$XRAY_VENDOR_BIN")" >/dev/null \
    || die "Failed to unzip xray"
  chmod +x "$XRAY_VENDOR_BIN"
  xattr -dr com.apple.quarantine "$XRAY_VENDOR_BIN" 2>/dev/null || true
  rm -rf "$tmp"
  log "xray installed to $XRAY_VENDOR_BIN"
}
```

And add this next to the other status functions (after `status_config` ~line 325):

```bash
status_xray() { [[ -f "$XRAY_JSON" && -f "$XRAY_LAUNCHER" ]] && xray_bin >/dev/null 2>&1; }
```

- [ ] **Step 2: Verify the script still parses and functions resolve**

Run: `bash -n local/bootstrap-macos.sh && RC_SOURCED_FOR_TEST=1 bash -c 'source local/bootstrap-macos.sh; type xray_bin install_xray status_xray >/dev/null && echo FUNCS_OK'`
Expected: `FUNCS_OK`.

- [ ] **Step 3: Verify `status_xray` is false on a clean machine**

Run: `RC_SOURCED_FOR_TEST=1 bash -c 'source local/bootstrap-macos.sh; status_xray && echo ON || echo OFF'`
Expected: `OFF` (no `xray.json` yet).

- [ ] **Step 4: Commit**

```bash
git add local/bootstrap-macos.sh
git commit -m "Add xray install and status detection to macOS bootstrap"
```

---

## Task 4: `write_xray_config`, `write_xray_launcher`, `run_xray`

**Files:**
- Modify: `local/bootstrap-macos.sh` (add after the install helpers; `run_xray` near the other `run_*` functions).

- [ ] **Step 1: Add `write_xray_config` and `write_xray_launcher`**

```bash
write_xray_config() { # write_xray_config <vless-url>
  local url="$1" json
  json="$(vless_url_to_json "$url")" || return 1
  mkdir -p "$RC_CONFIG_DIR"
  printf '%s\n' "$json" > "$XRAY_JSON"
  chmod 600 "$XRAY_JSON"
  log "Wrote $XRAY_JSON"
}

write_xray_launcher() {
  mkdir -p "$RC_CONFIG_DIR"
  cat > "$XRAY_LAUNCHER" <<'LAUNCH'
#!/usr/bin/env bash
# Auto-generated by bootstrap-macos.sh — on-demand xray starter for ssh ProxyCommand.
# Usage (from ssh_config): ProxyCommand <this-script> %h %p
set -uo pipefail
RC_CONFIG_DIR="$HOME/.config/remote-claude"
CONF="$RC_CONFIG_DIR/xray.json"
LOG="$RC_CONFIG_DIR/xray.log"
SOCKS_PORT=10808

resolve_xray() {
  if [[ -x "$RC_CONFIG_DIR/bin/xray" ]]; then printf '%s\n' "$RC_CONFIG_DIR/bin/xray"; return 0; fi
  command -v xray 2>/dev/null
}
port_up() { nc -z 127.0.0.1 "$SOCKS_PORT" >/dev/null 2>&1; }

if ! port_up; then
  bin="$(resolve_xray)" || { echo "xray binary not found" >&2; exit 1; }
  nohup "$bin" run -c "$CONF" >"$LOG" 2>&1 &
  for _ in $(seq 1 25); do port_up && break; sleep 0.2; done
  port_up || { echo "xray did not come up within timeout; see $LOG" >&2; exit 1; }
fi
exec nc -X 5 -x "127.0.0.1:$SOCKS_PORT" "$1" "$2"
LAUNCH
  chmod +x "$XRAY_LAUNCHER"
  log "Wrote $XRAY_LAUNCHER"
}
```

- [ ] **Step 2: Add `run_xray`** (item 6) after `run_show_key` (~line 314)

```bash
run_xray() { # item 6: install xray + build config from a vless:// URL
  command -v nc >/dev/null 2>&1 || die "nc (netcat) not found — required for the SOCKS ProxyCommand"
  local url
  url="${VLESS_URL:-$(ask 'Paste your vless:// URL')}"
  [[ -n "$url" ]] || die "No URL given; nothing changed"
  case "$url" in vless://*) ;; *) die "Not a vless:// URL" ;; esac
  vless_url_to_json "$url" >/dev/null || die "Could not parse the vless:// URL (see message above)"
  install_xray
  write_xray_config "$url"
  write_xray_launcher
  log "xray client ready. Turn it on for the tunnel via menu item 4 (answer Y to route through the proxy)."
}
```

- [ ] **Step 3: Verify parse + functions resolve**

Run: `bash -n local/bootstrap-macos.sh && RC_SOURCED_FOR_TEST=1 bash -c 'source local/bootstrap-macos.sh; type write_xray_config write_xray_launcher run_xray >/dev/null && echo FUNCS_OK'`
Expected: `FUNCS_OK`.

- [ ] **Step 4: Exercise config + launcher writing into a temp HOME** (no network, no server)

Run:
```bash
RC_SOURCED_FOR_TEST=1 HOME="$(mktemp -d)" bash -c '
  source local/bootstrap-macos.sh
  write_xray_config "vless://u@h.tld:443?type=tcp&security=reality&pbk=K&sid=1a&sni=a.b&flow=xtls-rprx-vision"
  write_xray_launcher
  test -s "$XRAY_JSON" && test -x "$XRAY_LAUNCHER" && grep -q "exec nc -X 5" "$XRAY_LAUNCHER" && echo WROTE_OK
'
```
Expected: log lines plus `WROTE_OK`.

- [ ] **Step 5: Commit**

```bash
git add local/bootstrap-macos.sh
git commit -m "Write xray.json and on-demand ProxyCommand launcher (item 6)"
```

---

## Task 5: Item 4 — optional ProxyCommand injection

**Files:**
- Modify: `local/bootstrap-macos.sh:118-136` (`write_ssh_config_block`), `:287-299` (`run_config`), add `config_proxy_on`.

- [ ] **Step 1: Replace the block-building portion of `write_ssh_config_block`.** Replace the signature line and the `block="$(cat <<EOF ... EOF)"` heredoc (lines 118-136) with:

```bash
write_ssh_config_block() { # <host> <user> <port> <reverse_port> [use_proxy 0|1]
  local SERVER_HOST="$1" SERVER_USER="$2" SERVER_PORT="$3" REVERSE_PORT="$4" USE_PROXY="${5:-0}"
  local block tmp
  block=$(
    printf '%s\n' "$BEGIN_MARK"
    printf 'Host %s\n' "$TUNNEL_ALIAS"
    printf '    HostName %s\n' "$SERVER_HOST"
    printf '    User %s\n' "$SERVER_USER"
    printf '    Port %s\n' "$SERVER_PORT"
    printf '    IdentityFile ~/.ssh/%s\n' "$KEY_NAME"
    printf '    IdentitiesOnly yes\n'
    [[ "$USE_PROXY" == "1" ]] && printf '    ProxyCommand %s %%h %%p\n' "$XRAY_LAUNCHER"
    printf '    RemoteForward 127.0.0.1:%s 127.0.0.1:22\n' "$REVERSE_PORT"
    printf '    ExitOnForwardFailure yes\n'
    printf '    ServerAliveInterval 30\n'
    printf '    ServerAliveCountMax 3\n'
    printf '    ForwardAgent no\n'
    printf '%s\n' "$END_MARK"
  )
```

Leave the rest of the function (from `touch "$SSH_CONFIG"` onward) unchanged.

- [ ] **Step 2: Add `config_proxy_on`** next to `status_config` (~line 325)

```bash
config_proxy_on() { grep -qF "ProxyCommand $XRAY_LAUNCHER" "$SSH_CONFIG" 2>/dev/null; }
```

- [ ] **Step 3: Add the proxy prompt to `run_config`.** Change the local declaration and the final call. Replace line 289 `local server_host server_user server_port reverse_port` with:

```bash
  local server_host server_user server_port reverse_port use_proxy=0
```

and replace the final call (line 298) with:

```bash
  if status_xray; then
    if [[ -n "${USE_XRAY_PROXY:-}" ]]; then
      [[ "$USE_XRAY_PROXY" == "1" ]] && use_proxy=1
    elif ask_yn "Route this tunnel through the local xray proxy" "Y"; then
      use_proxy=1
    fi
  fi
  write_ssh_config_block "$server_host" "$server_user" "$server_port" "$reverse_port" "$use_proxy"
```

- [ ] **Step 4: Verify parse + block content with proxy on/off** into a temp HOME

Run:
```bash
bash -n local/bootstrap-macos.sh && \
RC_SOURCED_FOR_TEST=1 HOME="$(mktemp -d)" bash -c '
  source local/bootstrap-macos.sh
  mkdir -p "$SSH_DIR"
  write_ssh_config_block host.tld me 22 2222 1
  grep -q "ProxyCommand $XRAY_LAUNCHER %h %p" "$SSH_CONFIG" && echo PROXY_ON_OK
  config_proxy_on && echo DETECT_ON_OK
  write_ssh_config_block host.tld me 22 2222 0
  grep -q ProxyCommand "$SSH_CONFIG" || echo PROXY_OFF_OK
  config_proxy_on || echo DETECT_OFF_OK
'
```
Expected: `PROXY_ON_OK`, `DETECT_ON_OK`, `PROXY_OFF_OK`, `DETECT_OFF_OK` (the second write cleanly removes the line — confirms item 4 sole ownership / no strand).

- [ ] **Step 5: Commit**

```bash
git add local/bootstrap-macos.sh
git commit -m "Optionally route the remote-claude tunnel through xray (item 4)"
```

---

## Task 6: Menu display for item 6 + item-4 marker

**Files:**
- Modify: `local/bootstrap-macos.sh:330-339` (`draw_menu`). (The select prompt and `case` were already updated in Task 1 Step 2.)

- [ ] **Step 1: Update `draw_menu`.** Replace the item-4 line and add item 6. The new `draw_menu` body:

```bash
draw_menu() {
  echo
  echo "----------------------------------------------------------"
  local cfg_label='Tunnel config (Host remote-claude)'
  config_proxy_on && cfg_label='Tunnel config (Host remote-claude) [xray]'
  printf '  1) %-50s %s\n' 'Incoming SSH — Remote Login + harden  [sudo]' "$(mark status_sshd)"
  printf '  2) %-50s %s\n' 'Local SSH key (~/.ssh/id_ed25519)' "$(mark status_key)"
  printf '  3) %-50s %s\n' "Authorize the server's connect-back key" "$(mark status_authorize)"
  printf '  4) %-50s %s\n' "$cfg_label" "$(mark status_config)"
  printf '  5) %s\n' 'Show local public key (paste into server setup)'
  printf '  6) %-50s %s\n' 'xray client (paste vless:// URL)' "$(mark status_xray)"
  echo   '  q) Quit'
}
```

- [ ] **Step 2: Verify the menu renders with item 6**

Run: `printf 'q\n' | bash local/bootstrap-macos.sh | grep -E '6\) xray client'`
Expected: the `6) xray client (paste vless:// URL)   [ -  ]` line prints.

- [ ] **Step 3: Commit**

```bash
git add local/bootstrap-macos.sh
git commit -m "Show item 6 and the item-4 [xray] marker in the macOS menu"
```

---

## Task 7: Documentation

**Files:**
- Modify: `README.md`, `README.zh-CN.md`, `TROUBLESHOOTING.md`, `TROUBLESHOOTING.zh-CN.md`.

- [ ] **Step 1: README.md** — add a short subsection after the local-setup section (step 1). Text to insert:

```markdown
### Optional: route the tunnel through an xray (VLESS) proxy (macOS)

On a poor / censored network, the macOS bootstrap has an extra menu item
(**6) xray client**) that takes a `vless://` URL, installs xray, and runs a
local SOCKS proxy. Then menu item 4 offers to route the `remote-claude`
tunnel through it, so VSCode Remote-SSH and `ssh remote-claude -t "claude"`
automatically tunnel SSH through xray — xray is started on demand at connect
time, no background service.

Stop the on-demand xray:

    pkill -f 'xray run -c .*remote-claude/xray.json'
```

- [ ] **Step 2: README.zh-CN.md** — add the parallel Chinese subsection:

```markdown
### 可选：让隧道走 xray（VLESS）代理（macOS）

网络较差 / 受限时，macOS 引导脚本多了一个菜单项（**6) xray client**）：粘贴一个
`vless://` URL，它会装好 xray 并起一个本地 SOCKS 代理。随后菜单项 4 会询问是否把
`remote-claude` 隧道走该代理——于是 VSCode Remote-SSH 和 `ssh remote-claude -t
"claude"` 会自动把 SSH 套进 xray；xray 在连接时按需拉起，无后台常驻服务。

停止按需启动的 xray：

    pkill -f 'xray run -c .*remote-claude/xray.json'
```

- [ ] **Step 3: TROUBLESHOOTING.md** — add a symptom entry:

```markdown
## xray proxy: connect hangs or fails after enabling item 4's proxy

The `ProxyCommand` starts xray on demand and waits for `127.0.0.1:10808`.
If SSH fails right after enabling the proxy:

- Check the log: `cat ~/.config/remote-claude/xray.log` — a bad `vless://`
  URL or unreachable server shows up here.
- Confirm the SOCKS port came up: `nc -z 127.0.0.1 10808 && echo up`.
- Re-run bootstrap item 6 to regenerate `~/.config/remote-claude/xray.json`
  if you pasted the wrong URL.
- To bypass xray temporarily, re-run item 4 and answer **n** to the proxy
  question.
```

- [ ] **Step 4: TROUBLESHOOTING.zh-CN.md** — add the parallel Chinese entry:

```markdown
## xray 代理：开了 item 4 的代理后连接卡住 / 失败

`ProxyCommand` 会按需拉起 xray 并等待 `127.0.0.1:10808`。开启代理后 SSH 失败时：

- 看日志：`cat ~/.config/remote-claude/xray.log`——`vless://` URL 有误或服务器
  不可达都会在这里体现。
- 确认 SOCKS 端口起来了：`nc -z 127.0.0.1 10808 && echo up`。
- URL 粘错了就重跑引导脚本 item 6，重新生成 `~/.config/remote-claude/xray.json`。
- 想临时不走 xray：重跑 item 4，代理那问选 **n**。
```

- [ ] **Step 5: Commit**

```bash
git add README.md README.zh-CN.md TROUBLESHOOTING.md TROUBLESHOOTING.zh-CN.md
git commit -m "Document the macOS xray/VLESS tunnel option"
```

---

## Task 8: Manual end-to-end verification

No code. Run on a real macOS machine with a working `vless://` URL and the
existing `remote-claude` server reachable. Record results.

- [ ] **Step 1: Run the automated parser test**

Run: `bash test/xray_url_to_json.test.sh`
Expected: all `ok`, exit 0.

- [ ] **Step 2: Item 6 end to end**

Run `bash local/bootstrap-macos.sh`, choose `6`, paste the real `vless://`
URL. Expected: xray installs (brew or vendored), `~/.config/remote-claude/xray.json`
and `xray-proxy.sh` exist, menu item 6 now shows `[done]`.

- [ ] **Step 3: Turn on the proxy in item 4**

Choose `4`, accept the existing values, answer **Y** to "Route this tunnel
through the local xray proxy". Expected: item 4 shows `[xray]`; `~/.ssh/config`
has `ProxyCommand ~/.config/remote-claude/xray-proxy.sh %h %p`.

- [ ] **Step 4: Connect through xray**

Run: `ssh remote-claude 'echo ok'`
Expected: prints `ok`; `pgrep -f 'xray run -c'` shows xray came up on demand;
`~/.config/remote-claude/xray.log` has no errors.

- [ ] **Step 5: Reverse hop still works**

With the tunnel session up, on the server run `ssh my-device 'echo ok'`.
Expected: prints `ok` (reverse forwarding unaffected by the proxy).

- [ ] **Step 6: VSCode Remote-SSH**

Connect VSCode Remote-SSH to `remote-claude`. Expected: connects (through
xray), terminal on the server works, `claude` can `ssh my-device`.

- [ ] **Step 7: Stop / off paths**

Run the stop one-liner `pkill -f 'xray run -c .*remote-claude/xray.json'`;
re-run item 4 answering **n**; confirm `ProxyCommand` is gone and a direct
`ssh remote-claude 'echo ok'` still works (when the network allows).

---

## Self-review notes

- **Spec coverage:** item 6 (Task 4/6), URL parsing with reality/tls/none × tcp/ws/grpc (Task 2), on-demand launcher no-daemon (Task 4), item-4 sole-owned ProxyCommand (Task 5), status/menu (Task 6), error handling via `xray.log` + non-zero ProxyCommand (Task 4/7), stop one-liner (Task 7), macOS-only scope (whole plan). All covered.
- **No placeholders:** every code step has complete content.
- **Type/name consistency:** `XRAY_LAUNCHER`, `XRAY_JSON`, `SOCKS_PORT=10808`, `vless_url_to_json`, `config_proxy_on`, `status_xray` used identically across tasks; launcher hardcodes the same `10808`.
- **bash 3.2 safety:** array expansion guarded by `${#pairs[@]} -gt 0`; no associative arrays.
