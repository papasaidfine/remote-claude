#!/usr/bin/env bash
#
# bootstrap-macos.sh — Reverse SSH dev-environment bootstrap for macOS.
#
# Prepares this Mac so a remote server's Claude / Codex agent can SSH back
# into it through a reverse tunnel:
#
#   local mac  ── ssh -N claude-dev-tunnel ──▶  remote server
#   remote server 127.0.0.1:<reverse_port>  ──▶  local mac 127.0.0.1:22
#
# What it does (idempotent, safe to re-run):
#   1. Enables Remote Login (sshd) via systemsetup.
#   2. Hardens sshd: pubkey auth on, password auth off (optional),
#      loopback-only listen (optional). Backs up config, validates with
#      `sshd -t` before restarting.
#   3. Creates ~/.ssh + authorized_keys with correct permissions.
#   4. Appends the server-side public key to authorized_keys with a
#      from="127.0.0.1,::1" restriction (dedup by key blob).
#   5. Generates ~/.ssh/claude_tunnel_ed25519 for the local→server hop.
#   6. Writes a managed "Host claude-dev-tunnel" block into ~/.ssh/config.
#   7. Optionally installs a LaunchAgent that keeps the tunnel up at login.
#
# Usage:  ./bootstrap-macos.sh
# Non-interactive overrides via env vars: SERVER_HOST, SERVER_USER,
# SERVER_PORT, REVERSE_PORT, LOCAL_USER, SERVER_PUBKEY.

set -euo pipefail

TUNNEL_ALIAS="claude-dev-tunnel"
KEY_NAME="claude_tunnel_ed25519"
SSH_DIR="$HOME/.ssh"
KEY_PATH="$SSH_DIR/$KEY_NAME"
SSH_CONFIG="$SSH_DIR/config"
AUTH_KEYS="$SSH_DIR/authorized_keys"
SSHD_CONFIG="/etc/ssh/sshd_config"
SSHD_DROPIN_DIR="/etc/ssh/sshd_config.d"
SSHD_DROPIN="$SSHD_DROPIN_DIR/100-claude-dev-tunnel.conf"
LAUNCH_AGENT_LABEL="com.claude.dev-tunnel"
LAUNCH_AGENT_PLIST="$HOME/Library/LaunchAgents/$LAUNCH_AGENT_LABEL.plist"
LOG_DIR="$HOME/Library/Logs"
TS="$(date +%Y%m%d-%H%M%S)"

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
[[ "$(uname -s)" == "Darwin" ]] || die "This script is for macOS. On Windows, run bootstrap-windows.ps1 in an elevated PowerShell."

cat <<'EOF'
==========================================================
 Reverse SSH bootstrap (macOS)
 local mac  ->  remote server  ->  (reverse tunnel)  -> local mac
==========================================================
This will modify:
  - Remote Login / sshd settings (requires sudo)
  - ~/.ssh/{config,authorized_keys,claude_tunnel_ed25519}
All modified system files are backed up first.
EOF
echo

# ---------------------------------------------------------------- inputs
SERVER_HOST="${SERVER_HOST:-$(ask '远程服务器 hostname / IP')}"
[[ -n "$SERVER_HOST" ]] || die "服务器地址不能为空"
SERVER_USER="${SERVER_USER:-$(ask '远程服务器 SSH 用户名')}"
[[ -n "$SERVER_USER" ]] || die "服务器用户名不能为空"
SERVER_PORT="${SERVER_PORT:-$(ask '远程服务器 SSH 端口' '22')}"
REVERSE_PORT="${REVERSE_PORT:-$(ask '服务器上反向 SSH 端口 (Claude/Codex 连回本机用)' '2222')}"
LOCAL_USER="${LOCAL_USER:-$(ask '本地用户名 (服务器侧连回来时使用)' "$USER")}"

[[ "$SERVER_PORT" =~ ^[0-9]+$ ]] || die "SSH 端口必须是数字"
[[ "$REVERSE_PORT" =~ ^[0-9]+$ ]] || die "反向端口必须是数字"

echo
echo "服务器侧 public key：即服务器上 Claude / Codex 用来反连本机的那把 key 的 .pub 内容"
echo "(整行粘贴，例如 'ssh-ed25519 AAAA... comment'；留空则跳过这一步)"
SERVER_PUBKEY="${SERVER_PUBKEY:-$(ask '服务器侧 public key' '')}"

if ask_yn "禁用本机 sshd 密码登录 (推荐，仅允许 public key)" "Y"; then
  DISABLE_PASSWORD=1
else
  DISABLE_PASSWORD=0
fi
if ask_yn "让本机 sshd 只监听 127.0.0.1 (推荐；注意：局域网将无法直接 SSH 到本机)" "Y"; then
  LOOPBACK_ONLY=1
else
  LOOPBACK_ONLY=0
fi

# ---------------------------------------------------------------- ~/.ssh
log "准备 ~/.ssh 目录和权限"
mkdir -p "$SSH_DIR"
chmod 700 "$SSH_DIR"
touch "$AUTH_KEYS"
chmod 600 "$AUTH_KEYS"

# ---------------------------------------------------------------- local key
if [[ -f "$KEY_PATH" && -f "$KEY_PATH.pub" ]]; then
  log "本地隧道 key 已存在: $KEY_PATH"
else
  log "生成本地连接服务器用的 SSH key: $KEY_PATH"
  ssh-keygen -t ed25519 -f "$KEY_PATH" -N "" -C "claude-tunnel" >/dev/null
fi
chmod 600 "$KEY_PATH"

# ---------------------------------------------------------------- server pubkey -> authorized_keys
add_authorized_key() {
  local pubkey="$1" tmp blob
  tmp="$(mktemp)"
  printf '%s\n' "$pubkey" > "$tmp"
  if ! ssh-keygen -lf "$tmp" >/dev/null 2>&1; then
    rm -f "$tmp"
    die "粘贴的内容不是合法的 SSH public key，请检查后重新运行"
  fi
  rm -f "$tmp"
  blob="$(awk '{for (i = 1; i <= NF; i++) if ($i ~ /^AAAA/) { print $i; exit }}' <<<"$pubkey")"
  [[ -n "$blob" ]] || die "无法从 public key 中解析 key 数据"
  if grep -qF "$blob" "$AUTH_KEYS"; then
    log "该 public key 已在 authorized_keys 中，跳过"
    return 0
  fi
  printf 'from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding %s\n' "$pubkey" >> "$AUTH_KEYS"
  log "已写入 authorized_keys（限制为仅可从 loopback 登录）"
}

if [[ -n "$SERVER_PUBKEY" ]]; then
  add_authorized_key "$SERVER_PUBKEY"
else
  warn "未提供服务器侧 public key。之后可将其追加到 $AUTH_KEYS，"
  warn "建议格式: from=\"127.0.0.1,::1\",no-agent-forwarding,no-X11-forwarding <public-key>"
fi

# ---------------------------------------------------------------- ~/.ssh/config
write_ssh_config_block() {
  local block tmp
  block="$(cat <<EOF
$BEGIN_MARK
Host $TUNNEL_ALIAS
    HostName $SERVER_HOST
    User $SERVER_USER
    Port $SERVER_PORT
    IdentityFile ~/.ssh/$KEY_NAME
    IdentitiesOnly yes
    RemoteForward 127.0.0.1:$REVERSE_PORT 127.0.0.1:22
    ExitOnForwardFailure yes
    ServerAliveInterval 30
    ServerAliveCountMax 3
    ForwardAgent no
$END_MARK
EOF
)"
  touch "$SSH_CONFIG"
  chmod 600 "$SSH_CONFIG"

  if grep -qF "$BEGIN_MARK" "$SSH_CONFIG"; then
    if ! ask_yn "~/.ssh/config 中已有 $TUNNEL_ALIAS 配置块，是否更新" "Y"; then
      warn "保留现有配置块，跳过写入"
      return 0
    fi
    cp "$SSH_CONFIG" "$SSH_CONFIG.claude-bak-$TS"
    log "已备份 ~/.ssh/config -> $SSH_CONFIG.claude-bak-$TS"
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
      warn "~/.ssh/config 中存在非本工具管理的 'Host $TUNNEL_ALIAS' 配置块。"
      warn "ssh 采用先到先得，靠前的旧配置会覆盖本工具写入的内容。"
      if ! ask_yn "仍然继续写入 (建议先手动清理旧配置块)" "N"; then
        die "已中止。请手动移除旧的 Host $TUNNEL_ALIAS 块后重新运行"
      fi
    fi
    cp "$SSH_CONFIG" "$SSH_CONFIG.claude-bak-$TS"
    log "已备份 ~/.ssh/config -> $SSH_CONFIG.claude-bak-$TS"
  fi

  { [[ -s "$SSH_CONFIG" ]] && [[ "$(tail -c1 "$SSH_CONFIG")" != "" ]] && echo; printf '%s\n' "$block"; } >> "$SSH_CONFIG"
  log "已写入 Host $TUNNEL_ALIAS 到 ~/.ssh/config"
}
write_ssh_config_block

# ---------------------------------------------------------------- sshd (needs sudo)
echo
log "接下来配置系统 sshd（需要 sudo 权限）"

enable_remote_login() {
  local status
  status="$(sudo systemsetup -getremotelogin 2>/dev/null || true)"
  if [[ "$status" == *": On"* ]]; then
    log "Remote Login 已开启"
    return 0
  fi
  log "开启 Remote Login (sshd)"
  if ! sudo systemsetup -setremotelogin on 2>/dev/null; then
    warn "systemsetup 开启失败（新版 macOS 可能需要完全磁盘访问权限）。"
    warn "请手动开启：系统设置 -> 通用 -> 共享 -> 远程登录，然后重新运行本脚本。"
    return 1
  fi
}
enable_remote_login || die "无法开启 Remote Login"

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
  if [[ "$LOOPBACK_ONLY" -eq 1 ]]; then
    settings+=$'\nListenAddress 127.0.0.1\nListenAddress ::1'
  fi

  if [[ "$use_dropin" -eq 1 ]]; then
    log "写入 sshd drop-in 配置: $SSHD_DROPIN"
    if [[ -f "$SSHD_DROPIN" ]]; then
      sudo cp "$SSHD_DROPIN" "$SSHD_DROPIN.claude-bak-$TS"
    fi
    printf '%s\n' "$settings" | sudo tee "$SSHD_DROPIN" >/dev/null
    if [[ "$LOOPBACK_ONLY" -eq 1 ]] && sudo grep -qE '^[[:space:]]*ListenAddress[[:space:]]' "$SSHD_CONFIG"; then
      warn "$SSHD_CONFIG 中已有 ListenAddress 配置，ListenAddress 是累加语义，请人工确认最终监听地址"
    fi
    if ! sudo /usr/sbin/sshd -t; then
      err "sshd 配置校验失败，回滚 drop-in"
      sudo rm -f "$SSHD_DROPIN"
      [[ -f "$SSHD_DROPIN.claude-bak-$TS" ]] && sudo mv "$SSHD_DROPIN.claude-bak-$TS" "$SSHD_DROPIN"
      die "sshd -t 未通过，已回滚"
    fi
  else
    log "系统不支持 sshd_config.d，直接编辑 $SSHD_CONFIG（已备份）"
    sudo cp "$SSHD_CONFIG" "$SSHD_CONFIG.claude-bak-$TS"
    log "已备份 -> $SSHD_CONFIG.claude-bak-$TS"
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
    if [[ "$LOOPBACK_ONLY" -eq 1 ]]; then
      set_sshd_option "ListenAddress" "127.0.0.1"
    fi
    if ! sudo /usr/sbin/sshd -t; then
      err "sshd 配置校验失败，恢复备份"
      sudo cp "$SSHD_CONFIG.claude-bak-$TS" "$SSHD_CONFIG"
      die "sshd -t 未通过，已恢复原配置"
    fi
  fi
  log "sshd 配置校验通过"

  # macOS 的 sshd 由 launchd 按连接拉起；kickstart 让配置立即生效
  sudo launchctl kickstart -k system/com.openssh.sshd 2>/dev/null \
    || warn "launchctl kickstart 失败（可忽略：macOS 会在下一次连接时使用新配置）"
  log "sshd 已重载"
}
configure_sshd

# ---------------------------------------------------------------- copy key to server (optional)
echo
log "本地隧道 public key（需要加入服务器上 $SERVER_USER 的 ~/.ssh/authorized_keys）："
echo
cat "$KEY_PATH.pub"
echo
if ask_yn "现在通过 ssh-copy-id 自动上传到服务器 (需要输入服务器密码或已有可用认证)" "N"; then
  ssh-copy-id -i "$KEY_PATH.pub" -p "$SERVER_PORT" "$SERVER_USER@$SERVER_HOST" \
    || warn "ssh-copy-id 失败，请手动将上述 public key 追加到服务器的 ~/.ssh/authorized_keys"
fi

# ---------------------------------------------------------------- LaunchAgent (optional)
echo
if ask_yn "安装 LaunchAgent，登录后自动拉起并保持 tunnel (可选)" "N"; then
  mkdir -p "$(dirname "$LAUNCH_AGENT_PLIST")" "$LOG_DIR"
  if [[ -f "$LAUNCH_AGENT_PLIST" ]]; then
    cp "$LAUNCH_AGENT_PLIST" "$LAUNCH_AGENT_PLIST.claude-bak-$TS"
  fi
  cat > "$LAUNCH_AGENT_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>$LAUNCH_AGENT_LABEL</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/bin/ssh</string>
        <string>-N</string>
        <string>-o</string>
        <string>ExitOnForwardFailure=yes</string>
        <string>$TUNNEL_ALIAS</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>30</integer>
    <key>StandardOutPath</key>
    <string>$LOG_DIR/$TUNNEL_ALIAS.log</string>
    <key>StandardErrorPath</key>
    <string>$LOG_DIR/$TUNNEL_ALIAS.err.log</string>
</dict>
</plist>
EOF
  launchctl bootout "gui/$(id -u)" "$LAUNCH_AGENT_PLIST" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$LAUNCH_AGENT_PLIST" 2>/dev/null \
    || launchctl load -w "$LAUNCH_AGENT_PLIST"
  log "LaunchAgent 已安装并启动: $LAUNCH_AGENT_PLIST"
  log "日志: $LOG_DIR/$TUNNEL_ALIAS.log / $TUNNEL_ALIAS.err.log"
  AUTOSTART_INSTALLED=1
else
  AUTOSTART_INSTALLED=0
fi

# ---------------------------------------------------------------- summary
cat <<EOF

==========================================================
 完成！后续步骤
==========================================================
1. 确认本地隧道 public key 已加入服务器 (~$SERVER_USER/.ssh/authorized_keys)：
     $KEY_PATH.pub

2. 手动启动 tunnel（前台保持运行）：
     ssh -N $TUNNEL_ALIAS

3. 只要 tunnel 保持连接，在远程服务器上 Claude / Codex 即可用：
     ssh -i ~/.ssh/claude_to_local_ed25519 -p $REVERSE_PORT $LOCAL_USER@127.0.0.1
   （-i 指向服务器上那把反连 key 的私钥路径，按实际情况调整）

EOF
if [[ "$AUTOSTART_INSTALLED" -eq 1 ]]; then
  cat <<EOF
tunnel 已配置为登录自启（LaunchAgent）。停止方式：
     launchctl bootout gui/\$(id -u) $LAUNCH_AGENT_PLIST
EOF
else
  echo "如需登录自启，重新运行脚本并在 LaunchAgent 步骤选择 yes。"
fi
echo
echo "回滚方法见 README.md 的 “删除 / 回滚” 一节。"
