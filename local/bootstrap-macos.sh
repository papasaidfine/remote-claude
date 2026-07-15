#!/usr/bin/env bash
#
# bootstrap-macos.sh — Reverse SSH dev-environment bootstrap for macOS.
#
# Prepares this Mac so a remote server's Claude / Codex agent can SSH back
# into it through a reverse tunnel:
#
#   local mac  ── ssh -N remote-claude ──▶  remote server
#   remote server 127.0.0.1:<reverse_port>  ──▶  local mac 127.0.0.1:22
#
# Presents a menu of independent items — each is idempotent, shows whether
# it is already configured, and can be run (or fail, or be re-run) on its own:
#
#   1. Incoming SSH: enable Remote Login (sshd) via systemsetup and harden
#      sshd (pubkey auth on, password auth off optional). Backs up config,
#      validates with `sshd -t` before reloading. The only item that needs
#      sudo.
#   2. Ensure the default ~/.ssh/id_ed25519 exists (local→server hop).
#   3. Append the server-side public key to authorized_keys with a
#      from="127.0.0.1,::1" restriction (dedup by key blob).
#   4. Write a managed "Host remote-claude" block into ~/.ssh/config.
#   5. Show the local public key to paste into the server-side setup.
#
# Usage:  ./bootstrap-macos.sh
# Non-interactive overrides via env vars: SERVER_HOST, SERVER_USER,
# SERVER_PORT, REVERSE_PORT, SERVER_PUBKEY.

# No -e at the top level: menu items run in their own `set -e` subshell so a
# failing item returns to the menu instead of killing the script.
set -uo pipefail

TUNNEL_ALIAS="remote-claude"
KEY_NAME="id_ed25519"
SSH_DIR="$HOME/.ssh"
KEY_PATH="$SSH_DIR/$KEY_NAME"
SSH_CONFIG="$SSH_DIR/config"
AUTH_KEYS="$SSH_DIR/authorized_keys"
SSHD_CONFIG="/etc/ssh/sshd_config"
SSHD_DROPIN_DIR="/etc/ssh/sshd_config.d"
SSHD_DROPIN="$SSHD_DROPIN_DIR/100-remote-claude.conf"
TS="$(date +%Y%m%d-%H%M%S)"

# xray / VLESS client (item 6)
RC_CONFIG_DIR="$HOME/.config/remote-claude"
XRAY_JSON="$RC_CONFIG_DIR/xray.json"
XRAY_LAUNCHER="$RC_CONFIG_DIR/xray-proxy.sh"
XRAY_VENDOR_BIN="$RC_CONFIG_DIR/bin/xray"
XRAY_LOG="$RC_CONFIG_DIR/xray.log"
SOCKS_PORT=10808

BEGIN_MARK="# >>> ${TUNNEL_ALIAS} (managed by reverse-ssh-bootstrap) >>>"
END_MARK="# <<< ${TUNNEL_ALIAS} <<<"

log()  { printf '\033[1;32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

ask() { # ask <prompt> [default]  -> echoes answer
  local prompt="$1" default="${2:-}" reply
  if [[ -n "$default" ]]; then
    read -r -p "$prompt [$default]: " reply
    printf '%s\n' "${reply:-$default}"
  else
    read -r -p "$prompt: " reply
    printf '%s\n' "$reply"
  fi
}

ask_yn() { # ask_yn <prompt> <Y|N>  -> exit status 0 = yes
  local prompt="$1" default="$2" reply hint
  hint="y/N"; [[ "$default" == "Y" ]] && hint="Y/n"
  while true; do
    read -r -p "$prompt [$hint]: " reply
    reply="${reply:-$default}"
    case "$reply" in
      [Yy]*) return 0 ;;
      [Nn]*) return 1 ;;
    esac
  done
}

# ---------------------------------------------------------------- platform
if [[ -z "${RC_SOURCED_FOR_TEST:-}" ]]; then
[[ "$(uname -s)" == "Darwin" ]] || die "This script is for macOS. On Windows, run bootstrap-windows.ps1 in an elevated PowerShell."

cat <<'EOF'
==========================================================
 Reverse SSH bootstrap (macOS)
 local mac  ->  remote server  ->  (reverse tunnel)  -> local mac
==========================================================
Pick items from the menu below; each one is independent, idempotent,
and shows whether it is already configured. Files this can modify:
  - Remote Login / sshd settings (item 1, sudo)
  - ~/.ssh/{config,authorized_keys,id_ed25519}
All modified system files are backed up first (*.claude-bak-<timestamp>).
EOF
fi

# ---------------------------------------------------------------- shared prep
ensure_ssh_dir() {
  mkdir -p "$SSH_DIR"
  chmod 700 "$SSH_DIR"
  touch "$AUTH_KEYS"
  chmod 600 "$AUTH_KEYS"
}

# ---------------------------------------------------------------- helpers
add_authorized_key() {
  local pubkey="$1" tmp blob
  tmp="$(mktemp)"
  printf '%s\n' "$pubkey" > "$tmp"
  if ! ssh-keygen -lf "$tmp" >/dev/null 2>&1; then
    rm -f "$tmp"
    die "The pasted content is not a valid SSH public key; please check and re-run"
  fi
  rm -f "$tmp"
  blob="$(awk '{for (i = 1; i <= NF; i++) if ($i ~ /^AAAA/) { print $i; exit }}' <<<"$pubkey")"
  [[ -n "$blob" ]] || die "Could not parse the key data from the public key"
  if grep -qF "$blob" "$AUTH_KEYS"; then
    log "This public key is already in authorized_keys, skipping"
    return 0
  fi
  printf 'from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding %s\n' "$pubkey" >> "$AUTH_KEYS"
  log "Written to authorized_keys (restricted to loopback logins only)"
}

write_ssh_config_block() { # <host> <user> <port> <reverse_port> [use_proxy 0|1] [force 0|1]
  local SERVER_HOST="$1" SERVER_USER="$2" SERVER_PORT="$3" REVERSE_PORT="$4" USE_PROXY="${5:-0}" FORCE="${6:-0}"
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
  touch "$SSH_CONFIG"
  chmod 600 "$SSH_CONFIG"

  if grep -qF "$BEGIN_MARK" "$SSH_CONFIG"; then
    if [[ "$FORCE" != "1" ]] && ! ask_yn "~/.ssh/config already contains a $TUNNEL_ALIAS block, update it" "Y"; then
      warn "Keeping the existing block, skipping the write"
      return 0
    fi
    cp "$SSH_CONFIG" "$SSH_CONFIG.claude-bak-$TS"
    log "Backed up ~/.ssh/config -> $SSH_CONFIG.claude-bak-$TS"
    tmp="$(mktemp)"
    awk -v begin="$BEGIN_MARK" -v end="$END_MARK" '
      $0 == begin { skip = 1; next }
      $0 == end   { skip = 0; next }
      !skip { print }
    ' "$SSH_CONFIG" > "$tmp"
    cat "$tmp" > "$SSH_CONFIG"
    rm -f "$tmp"
  else
    if grep -qE "^[[:space:]]*Host[[:space:]]+.*\b$TUNNEL_ALIAS\b" "$SSH_CONFIG"; then
      warn "~/.ssh/config contains a 'Host $TUNNEL_ALIAS' block that is not managed by this tool."
      warn "ssh uses first-match-wins, so the earlier block would override what this tool writes."
      if ! ask_yn "Write the block anyway (cleaning up the old block manually is recommended)" "N"; then
        die "Aborted. Please remove the old Host $TUNNEL_ALIAS block and re-run"
      fi
    fi
    cp "$SSH_CONFIG" "$SSH_CONFIG.claude-bak-$TS"
    log "Backed up ~/.ssh/config -> $SSH_CONFIG.claude-bak-$TS"
  fi

  { [[ -s "$SSH_CONFIG" ]] && [[ "$(tail -c1 "$SSH_CONFIG")" != "" ]] && echo; printf '%s\n' "$block"; } >> "$SSH_CONFIG"
  log "Wrote Host $TUNNEL_ALIAS to ~/.ssh/config"
}

config_block_value() { # config_block_value <Key> -> that key's value inside the managed block
  awk -v begin="$BEGIN_MARK" -v end="$END_MARK" -v key="$1" '
    $0 == begin { inblk = 1; next }
    $0 == end   { inblk = 0 }
    inblk && $1 == key { print $2; exit }
  ' "$SSH_CONFIG" 2>/dev/null
}

# ---------------------------------------------------------------- sshd helpers
enable_remote_login() {
  local status
  status="$(sudo systemsetup -getremotelogin 2>/dev/null || true)"
  if [[ "$status" == *": On"* ]]; then
    log "Remote Login is already enabled"
    return 0
  fi
  log "Enabling Remote Login (sshd)"
  if ! sudo systemsetup -setremotelogin on 2>/dev/null; then
    warn "systemsetup failed (recent macOS versions may require Full Disk Access)."
    warn "Please enable it manually: System Settings -> General -> Sharing -> Remote Login, then re-run this item."
    return 1
  fi
}

configure_sshd() {
  local use_dropin=0
  if [[ -d /etc/ssh ]] && grep -qE '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/\*' "$SSHD_CONFIG" 2>/dev/null; then
    use_dropin=1
  fi

  local settings
  settings="# Managed by reverse-ssh-bootstrap ($TUNNEL_ALIAS). Delete this file to roll back.
PubkeyAuthentication yes
AuthorizedKeysFile .ssh/authorized_keys"
  if [[ "$DISABLE_PASSWORD" -eq 1 ]]; then
    settings+=$'\nPasswordAuthentication no\nKbdInteractiveAuthentication no'
  fi

  if [[ "$use_dropin" -eq 1 ]]; then
    log "Writing sshd drop-in config: $SSHD_DROPIN"
    if [[ -f "$SSHD_DROPIN" ]]; then
      sudo cp "$SSHD_DROPIN" "$SSHD_DROPIN.claude-bak-$TS"
    fi
    printf '%s\n' "$settings" | sudo tee "$SSHD_DROPIN" >/dev/null
    if ! sudo /usr/sbin/sshd -t; then
      err "sshd config validation failed, rolling back the drop-in"
      sudo rm -f "$SSHD_DROPIN"
      [[ -f "$SSHD_DROPIN.claude-bak-$TS" ]] && sudo mv "$SSHD_DROPIN.claude-bak-$TS" "$SSHD_DROPIN"
      die "sshd -t did not pass, rolled back"
    fi
  else
    log "sshd_config.d is not supported on this system, editing $SSHD_CONFIG directly (backed up)"
    sudo cp "$SSHD_CONFIG" "$SSHD_CONFIG.claude-bak-$TS"
    log "Backed up -> $SSHD_CONFIG.claude-bak-$TS"
    set_sshd_option() { # set_sshd_option <Key> <Value>
      local key="$1" value="$2"
      if sudo grep -qE "^[#[:space:]]*${key}([[:space:]]|\$)" "$SSHD_CONFIG"; then
        sudo sed -i '' -E "s|^[#[:space:]]*(${key})([[:space:]].*)?\$|\\1 ${value}|" "$SSHD_CONFIG"
      else
        printf '%s %s\n' "$key" "$value" | sudo tee -a "$SSHD_CONFIG" >/dev/null
      fi
    }
    set_sshd_option "PubkeyAuthentication" "yes"
    set_sshd_option "AuthorizedKeysFile" ".ssh/authorized_keys"
    if [[ "$DISABLE_PASSWORD" -eq 1 ]]; then
      set_sshd_option "PasswordAuthentication" "no"
      set_sshd_option "KbdInteractiveAuthentication" "no"
    fi
    if ! sudo /usr/sbin/sshd -t; then
      err "sshd config validation failed, restoring the backup"
      sudo cp "$SSHD_CONFIG.claude-bak-$TS" "$SSHD_CONFIG"
      die "sshd -t did not pass, original config restored"
    fi
  fi
  log "sshd config validation passed"

  # macOS launches sshd on demand via launchd; kickstart applies the new config now
  sudo launchctl kickstart -k system/com.openssh.sshd 2>/dev/null \
    || warn "launchctl kickstart failed (safe to ignore: macOS will use the new config on the next connection)"
  log "sshd reloaded"
}

# ---------------------------------------------------------------- menu items
run_sshd() { # item 1: enable Remote Login + harden sshd (sudo)
  log "This item enables Remote Login and hardens sshd (requires sudo)"
  local DISABLE_PASSWORD
  if ask_yn "Disable password login for the local sshd (recommended, public key only)" "Y"; then
    DISABLE_PASSWORD=1
  else
    DISABLE_PASSWORD=0
  fi
  enable_remote_login || die "Could not enable Remote Login"
  configure_sshd
}

run_key() { # item 2: ensure ~/.ssh/id_ed25519 exists
  ensure_ssh_dir
  # Use the default SSH key; generate it only when it does not exist yet.
  if [[ -f "$KEY_PATH" ]]; then
    if [[ ! -f "$KEY_PATH.pub" ]]; then
      ssh-keygen -y -P "" -f "$KEY_PATH" > "$KEY_PATH.pub" 2>/dev/null \
        || die "$KEY_PATH exists but $KEY_PATH.pub is missing and could not be derived (passphrase-protected?); please fix and re-run"
    fi
    log "Using existing SSH key: $KEY_PATH"
    ssh-keygen -y -P "" -f "$KEY_PATH" >/dev/null 2>&1 \
      || warn "This key appears to be passphrase-protected; the tunnel will need an ssh-agent to work"
  else
    log "Generating the default SSH key: $KEY_PATH"
    ssh-keygen -t ed25519 -f "$KEY_PATH" -N "" >/dev/null
  fi
  chmod 600 "$KEY_PATH"
}

run_authorize() { # item 3: authorize the server's connect-back key
  ensure_ssh_dir
  local pubkey
  echo "Server-side public key: the .pub of the key that Claude / Codex on the"
  echo "server will use to SSH back into this machine (setup-server.sh item 1"
  echo "prints it, or: cat ~/.ssh/id_ed25519.pub on the server)."
  pubkey="${SERVER_PUBKEY:-$(ask 'Server-side public key' '')}"
  [[ -n "$pubkey" ]] || die "No key pasted; nothing changed"
  add_authorized_key "$pubkey"
}

run_config() { # item 4: Host remote-claude block
  ensure_ssh_dir
  local server_host server_user server_port reverse_port use_proxy=0
  server_host="${SERVER_HOST:-$(ask 'Remote server hostname / IP')}"
  [[ -n "$server_host" ]] || die "Server hostname must not be empty"
  server_user="${SERVER_USER:-$(ask 'Remote server SSH user')}"
  [[ -n "$server_user" ]] || die "Server user must not be empty"
  server_port="${SERVER_PORT:-$(ask 'Remote server SSH port' '22')}"
  [[ "$server_port" =~ ^[0-9]+$ ]] || die "SSH port must be a number"
  reverse_port="${REVERSE_PORT:-$(ask 'Reverse SSH port on the server (used by Claude/Codex to connect back)' '2222')}"
  [[ "$reverse_port" =~ ^[0-9]+$ ]] || die "Reverse port must be a number"
  if status_xray; then
    if [[ -n "${USE_XRAY_PROXY:-}" ]]; then
      [[ "$USE_XRAY_PROXY" == "1" ]] && use_proxy=1
    elif ask_yn "Route this tunnel through the local xray proxy" "Y"; then
      use_proxy=1
    fi
  fi
  write_ssh_config_block "$server_host" "$server_user" "$server_port" "$reverse_port" "$use_proxy"
}

run_show_key() { # item 5: print the local public key for the server-side handoff
  if [[ ! -f "$KEY_PATH.pub" ]]; then
    if [[ ! -f "$KEY_PATH" ]]; then
      ask_yn "No local key yet — generate it now" "Y" || die "No key to show"
    fi
    run_key
  fi
  echo
  log "Local public key — paste it into server/setup-server.sh (item 2) on the"
  log "server; that authorizes the tunnel login (ssh -N $TUNNEL_ALIAS):"
  echo
  cat "$KEY_PATH.pub"
  echo
}

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

# ---------------------------------------------------------------- status checks
status_sshd() {
  # systemsetup/launchctl need sudo, too heavy for drawing a menu — probe the port
  nc -z 127.0.0.1 22 >/dev/null 2>&1 || return 1
  [[ -f "$SSHD_DROPIN" ]] && return 0
  grep -qE '^[[:space:]]*PubkeyAuthentication[[:space:]]+yes' "$SSHD_CONFIG" 2>/dev/null
}
status_key()       { [[ -f "$KEY_PATH" ]]; }
status_authorize() { grep -qF 'from="127.0.0.1,::1"' "$AUTH_KEYS" 2>/dev/null; }
status_config()    { grep -qF "$BEGIN_MARK" "$SSH_CONFIG" 2>/dev/null; }
status_xray()      { [[ -f "$XRAY_JSON" && -f "$XRAY_LAUNCHER" ]] && xray_bin >/dev/null 2>&1; }
config_proxy_on()  { grep -qF "ProxyCommand $XRAY_LAUNCHER" "$SSH_CONFIG" 2>/dev/null; }

# ---------------------------------------------------------------- menu
mark() { if "$1"; then printf '[done]'; else printf '[ -  ]'; fi; }

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

run_item() { # run_item <run-function>; failures return to the menu
  echo
  ( set -e; "$1" )
  local rc=$?
  if [[ $rc -ne 0 ]]; then
    err "Item did not complete (see above). Other items are unaffected."
  fi
}

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
log "Start the tunnel with: ssh -N $TUNNEL_ALIAS   (keep it running)"
log "Then on the server: ssh my-device 'echo ok' should print ok"
fi
