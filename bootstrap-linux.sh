#!/usr/bin/env bash
#
# bootstrap-linux.sh — Reverse SSH dev-environment bootstrap for Linux.
#
# Prepares this Linux machine so a remote server's Claude / Codex agent can
# SSH back into it through a reverse tunnel:
#
#   local linux  ── ssh -N claude-dev-tunnel ──▶  remote server
#   remote server 127.0.0.1:<reverse_port>  ──▶  local linux 127.0.0.1:22
#
# What it does (idempotent, safe to re-run):
#   1. Installs openssh-server if missing (apt/dnf/yum/pacman/zypper),
#      enables and starts the sshd systemd service.
#   2. Hardens sshd: pubkey auth on, password auth off (optional),
#      loopback-only listen (optional). Backs up config, validates with
#      `sshd -t` before restarting.
#   3. Creates ~/.ssh + authorized_keys with correct permissions.
#   4. Appends the server-side public key to authorized_keys with a
#      from="127.0.0.1,::1" restriction (dedup by key blob).
#   5. Generates ~/.ssh/claude_tunnel_ed25519 for the local→server hop.
#   6. Writes a managed "Host claude-dev-tunnel" block into ~/.ssh/config.
#   7. Optionally installs a systemd user service that keeps the tunnel up.
#
# Usage:  ./bootstrap-linux.sh
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
USER_UNIT_DIR="$HOME/.config/systemd/user"
USER_UNIT="$USER_UNIT_DIR/$TUNNEL_ALIAS.service"
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
[[ "$(uname -s)" == "Linux" ]] || die "This script is for Linux. Use bootstrap-macos.sh on macOS or bootstrap-windows.ps1 on Windows."

cat <<'EOF'
==========================================================
 Reverse SSH bootstrap (Linux)
 local linux  ->  remote server  ->  (reverse tunnel)  -> local linux
==========================================================
This will modify:
  - openssh-server install / sshd service / sshd_config (requires sudo)
  - ~/.ssh/{config,authorized_keys,claude_tunnel_ed25519}
All modified system files are backed up first.
EOF
echo

# ---------------------------------------------------------------- inputs
SERVER_HOST="${SERVER_HOST:-$(ask 'Remote server hostname / IP')}"
[[ -n "$SERVER_HOST" ]] || die "Server hostname must not be empty"
SERVER_USER="${SERVER_USER:-$(ask 'Remote server SSH user')}"
[[ -n "$SERVER_USER" ]] || die "Server user must not be empty"
SERVER_PORT="${SERVER_PORT:-$(ask 'Remote server SSH port' '22')}"
REVERSE_PORT="${REVERSE_PORT:-$(ask 'Reverse SSH port on the server (used by Claude/Codex to connect back)' '2222')}"
LOCAL_USER="${LOCAL_USER:-$(ask 'Local username (used when connecting back from the server)' "$USER")}"

[[ "$SERVER_PORT" =~ ^[0-9]+$ ]] || die "SSH port must be a number"
[[ "$REVERSE_PORT" =~ ^[0-9]+$ ]] || die "Reverse port must be a number"

echo
echo "Server-side public key: the .pub of the key that Claude / Codex on the"
echo "server will use to SSH back into this machine."
echo "(paste the whole line, e.g. 'ssh-ed25519 AAAA... comment'; leave empty to skip)"
SERVER_PUBKEY="${SERVER_PUBKEY:-$(ask 'Server-side public key' '')}"

if ask_yn "Disable password login for the local sshd (recommended, public key only)" "Y"; then
  DISABLE_PASSWORD=1
else
  DISABLE_PASSWORD=0
fi

# If this session itself came in over SSH, loopback-only would lock the user
# out of this machine after the next reconnect — default to No in that case.
LOOPBACK_DEFAULT="Y"
if [[ -n "${SSH_CONNECTION:-}" ]]; then
  warn "You appear to be logged into this machine over SSH (SSH_CONNECTION is set)."
  warn "Restricting sshd to 127.0.0.1 would prevent you from SSHing back in remotely."
  LOOPBACK_DEFAULT="N"
fi
if ask_yn "Make the local sshd listen on 127.0.0.1 only (recommended for desktops; note: direct SSH from the LAN will stop working)" "$LOOPBACK_DEFAULT"; then
  LOOPBACK_ONLY=1
else
  LOOPBACK_ONLY=0
fi

# ---------------------------------------------------------------- ~/.ssh
log "Preparing ~/.ssh directory and permissions"
mkdir -p "$SSH_DIR"
chmod 700 "$SSH_DIR"
touch "$AUTH_KEYS"
chmod 600 "$AUTH_KEYS"

# ---------------------------------------------------------------- local key
if [[ -f "$KEY_PATH" && -f "$KEY_PATH.pub" ]]; then
  log "Local tunnel key already exists: $KEY_PATH"
else
  log "Generating the SSH key used to connect to the server: $KEY_PATH"
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

if [[ -n "$SERVER_PUBKEY" ]]; then
  add_authorized_key "$SERVER_PUBKEY"
else
  warn "No server-side public key provided. You can append it to $AUTH_KEYS later,"
  warn "recommended format: from=\"127.0.0.1,::1\",no-agent-forwarding,no-X11-forwarding <public-key>"
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
    if ! ask_yn "~/.ssh/config already contains a $TUNNEL_ALIAS block, update it" "Y"; then
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
write_ssh_config_block

# ---------------------------------------------------------------- sshd (needs sudo)
echo
log "Configuring the system sshd next (requires sudo)"

find_sshd_bin() {
  local p
  for p in /usr/sbin/sshd /usr/bin/sshd /usr/local/sbin/sshd; do
    [[ -x "$p" ]] && { printf '%s\n' "$p"; return 0; }
  done
  command -v sshd 2>/dev/null || return 1
}

install_sshd() {
  if find_sshd_bin >/dev/null; then
    log "openssh-server is already installed"
    return 0
  fi
  log "Installing openssh-server"
  if command -v apt-get >/dev/null; then
    sudo apt-get update -qq && sudo apt-get install -y openssh-server
  elif command -v dnf >/dev/null; then
    sudo dnf install -y openssh-server
  elif command -v yum >/dev/null; then
    sudo yum install -y openssh-server
  elif command -v pacman >/dev/null; then
    sudo pacman -S --noconfirm openssh
  elif command -v zypper >/dev/null; then
    sudo zypper install -y openssh
  else
    die "No supported package manager found (apt/dnf/yum/pacman/zypper); please install openssh-server manually and re-run"
  fi
  find_sshd_bin >/dev/null || die "openssh-server installation did not provide an sshd binary"
}
install_sshd
SSHD_BIN="$(find_sshd_bin)"

# Debian/Ubuntu name the unit ssh.service; most other distros use sshd.service
find_sshd_unit() {
  local u
  for u in sshd ssh; do
    if systemctl list-unit-files "$u.service" --no-legend 2>/dev/null | grep -q "$u.service"; then
      printf '%s\n' "$u"
      return 0
    fi
  done
  return 1
}

SSHD_UNIT=""
if command -v systemctl >/dev/null && [[ -d /run/systemd/system ]]; then
  SSHD_UNIT="$(find_sshd_unit || true)"
fi
if [[ -n "$SSHD_UNIT" ]]; then
  log "Enabling and starting $SSHD_UNIT.service"
  sudo systemctl enable --now "$SSHD_UNIT.service"
else
  warn "systemd not detected; please make sure sshd is running and enabled with your init system"
fi

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
    log "Writing sshd drop-in config: $SSHD_DROPIN"
    if [[ -f "$SSHD_DROPIN" ]]; then
      sudo cp "$SSHD_DROPIN" "$SSHD_DROPIN.claude-bak-$TS"
    fi
    printf '%s\n' "$settings" | sudo tee "$SSHD_DROPIN" >/dev/null
    if [[ "$LOOPBACK_ONLY" -eq 1 ]] && sudo grep -qE '^[[:space:]]*ListenAddress[[:space:]]' "$SSHD_CONFIG"; then
      warn "$SSHD_CONFIG already contains ListenAddress directives; ListenAddress is additive, please verify the final listen addresses yourself"
    fi
    if ! sudo "$SSHD_BIN" -t; then
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
        sudo sed -i -E "s|^[#[:space:]]*(${key})([[:space:]].*)?\$|\\1 ${value}|" "$SSHD_CONFIG"
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
    if ! sudo "$SSHD_BIN" -t; then
      err "sshd config validation failed, restoring the backup"
      sudo cp "$SSHD_CONFIG.claude-bak-$TS" "$SSHD_CONFIG"
      die "sshd -t did not pass, original config restored"
    fi
  fi
  log "sshd config validation passed"

  # Ubuntu 23.10+ uses systemd socket activation for sshd; the socket unit,
  # not sshd_config, decides the listen address in that mode.
  if [[ "$LOOPBACK_ONLY" -eq 1 ]] && command -v systemctl >/dev/null \
     && systemctl is-enabled ssh.socket >/dev/null 2>&1; then
    warn "ssh.socket (systemd socket activation) is enabled on this system."
    warn "In that mode the ListenAddress directive is IGNORED; the socket unit decides the address."
    warn "Either 'sudo systemctl disable --now ssh.socket && sudo systemctl enable --now ssh.service',"
    warn "or add a socket override: sudo systemctl edit ssh.socket  (set ListenStream=127.0.0.1:22)"
  fi

  if [[ -n "$SSHD_UNIT" ]]; then
    sudo systemctl restart "$SSHD_UNIT.service"
    log "sshd restarted"
  else
    warn "Please restart sshd manually to apply the new configuration"
  fi
}
configure_sshd

# ---------------------------------------------------------------- copy key to server (optional)
echo
log "Local tunnel public key (add it to ~/.ssh/authorized_keys of $SERVER_USER on the server):"
echo
cat "$KEY_PATH.pub"
echo
if ask_yn "Upload it to the server now via ssh-copy-id (needs the server password or existing working auth)" "N"; then
  ssh-copy-id -i "$KEY_PATH.pub" -p "$SERVER_PORT" "$SERVER_USER@$SERVER_HOST" \
    || warn "ssh-copy-id failed; please append the public key above to ~/.ssh/authorized_keys on the server manually"
fi

# ---------------------------------------------------------------- systemd user service (optional)
echo
AUTOSTART_INSTALLED=0
if command -v systemctl >/dev/null && [[ -d /run/systemd/system ]]; then
  if ask_yn "Install a systemd user service to start and keep the tunnel up (optional)" "N"; then
    mkdir -p "$USER_UNIT_DIR"
    if [[ -f "$USER_UNIT" ]]; then
      cp "$USER_UNIT" "$USER_UNIT.claude-bak-$TS"
    fi
    SSH_BIN="$(command -v ssh)"
    cat > "$USER_UNIT" <<EOF
[Unit]
Description=$TUNNEL_ALIAS reverse SSH tunnel
After=network-online.target

[Service]
ExecStart=$SSH_BIN -N -o ExitOnForwardFailure=yes $TUNNEL_ALIAS
Restart=always
RestartSec=15

[Install]
WantedBy=default.target
EOF
    systemctl --user daemon-reload
    systemctl --user enable --now "$TUNNEL_ALIAS.service"
    log "systemd user service installed and started: $USER_UNIT"
    log "Logs: journalctl --user -u $TUNNEL_ALIAS -f"
    if ask_yn "Enable lingering so the tunnel also runs when you are not logged in (sudo loginctl enable-linger)" "N"; then
      sudo loginctl enable-linger "$USER" \
        && log "Lingering enabled for $USER" \
        || warn "Could not enable lingering; the tunnel will only run while you are logged in"
    fi
    AUTOSTART_INSTALLED=1
  fi
else
  warn "systemd not detected; skipping the autostart option (run 'ssh -N $TUNNEL_ALIAS' manually or use your init system)"
fi

# ---------------------------------------------------------------- summary
cat <<EOF

==========================================================
 Done! Next steps
==========================================================
1. Make sure the local tunnel public key is added on the server
   (~$SERVER_USER/.ssh/authorized_keys):
     $KEY_PATH.pub

2. Start the tunnel manually (keeps running in the foreground):
     ssh -N $TUNNEL_ALIAS

3. While the tunnel stays connected, Claude / Codex on the server can use:
     ssh -i ~/.ssh/claude_to_local_ed25519 -p $REVERSE_PORT $LOCAL_USER@127.0.0.1
   (point -i at the actual private key path of the connect-back key on the server)

   Tip: run server/setup-server.sh on the server to install the
   claude-local ssh alias and the claude-local-shell SHELL wrapper.

EOF
if [[ "$AUTOSTART_INSTALLED" -eq 1 ]]; then
  cat <<EOF
The tunnel is managed by a systemd user service. To stop it:
     systemctl --user disable --now $TUNNEL_ALIAS.service
EOF
else
  echo "For autostart, re-run this script and answer yes at the systemd user service step."
fi
echo
echo "See the 'Removal / rollback' section of README.md for rollback instructions."
