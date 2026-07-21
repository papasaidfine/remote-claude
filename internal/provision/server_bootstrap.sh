# Body of the server-side bootstrap, run over `ssh remote-claude 'bash -s'`.
# The app PREPENDS to this: `set -eu`, the ALIAS / REVERSE_PORT / LOCAL_USER /
# LOCAL_PUBKEY assignments, and a heredoc writing the CLAUDE.md body to
# $HOME/.claude/.rc-claude-md.new. This file is never run on its own.
: "${ALIAS:?}"; : "${REVERSE_PORT:?}"; : "${LOCAL_USER:?}"; : "${LOCAL_PUBKEY:?}"

SSH_DIR="$HOME/.ssh"
mkdir -p "$SSH_DIR"; chmod 700 "$SSH_DIR"
KEY="$SSH_DIR/id_ed25519"

# 1. Connect-back key: the key the server uses to ssh back into the client.
if [ ! -f "$KEY" ]; then
  ssh-keygen -t ed25519 -f "$KEY" -N "" >/dev/null 2>&1
fi
chmod 600 "$KEY"
[ -f "$KEY.pub" ] || ssh-keygen -y -P "" -f "$KEY" > "$KEY.pub" 2>/dev/null

# 2. Authorize the client's key so the tunnel login (ssh remote-claude) works.
AUTH="$SSH_DIR/authorized_keys"; touch "$AUTH"; chmod 600 "$AUTH"
BLOB=$(printf '%s\n' "$LOCAL_PUBKEY" | awk '{for(i=1;i<=NF;i++) if($i ~ /^AAAA/){print $i; exit}}')
if [ -n "$BLOB" ] && ! grep -qF "$BLOB" "$AUTH"; then
  printf '%s remote-claude-tunnel\n' "$LOCAL_PUBKEY" >> "$AUTH"
fi

# 3. Reverse Host block: server -> client, over the reverse tunnel, named by the
#    client alias. Port is the single source of truth passed by the app.
CFG="$SSH_DIR/config"; touch "$CFG"; chmod 600 "$CFG"
BEGIN="# >>> $ALIAS (managed by remote-claude) >>>"
END="# <<< $ALIAS <<<"
tmp=$(mktemp)
awk -v b="$BEGIN" -v e="$END" '$0==b{s=1;next} $0==e{s=0;next} !s{print}' "$CFG" > "$tmp"
{
  cat "$tmp"
  printf '%s\n' "$BEGIN"
  printf 'Host %s\n' "$ALIAS"
  printf '    HostName 127.0.0.1\n'
  printf '    Port %s\n' "$REVERSE_PORT"
  printf '    User %s\n' "$LOCAL_USER"
  printf '    IdentityFile ~/.ssh/id_ed25519\n'
  printf '    IdentitiesOnly yes\n'
  printf '    StrictHostKeyChecking accept-new\n'
  printf '    UserKnownHostsFile ~/.ssh/known_hosts.%s\n' "$ALIAS"
  printf '%s\n' "$END"
} > "$CFG"
rm -f "$tmp"

# 4. Agent instructions in ~/.claude/CLAUDE.md (single global file, device-aware
#    via $LC_CLIENT_NAME). Managed marker block; user content around it is kept.
CLAUDE_MD="$HOME/.claude/CLAUDE.md"
NEW="$HOME/.claude/.rc-claude-md.new"
mkdir -p "$HOME/.claude" "$HOME/tmp"
if [ -f "$NEW" ]; then
  touch "$CLAUDE_MD"
  B="<!-- >>> remote-claude (managed) >>> -->"
  E="<!-- <<< remote-claude <<< -->"
  tmp2=$(mktemp)
  awk -v b="$B" -v e="$E" '$0==b{s=1;next} $0==e{s=0;next} !s{print}' "$CLAUDE_MD" > "$tmp2"
  {
    cat "$tmp2"
    printf '%s\n' "$B"
    cat "$NEW"
    printf '%s\n' "$E"
  } > "$CLAUDE_MD"
  rm -f "$tmp2" "$NEW"
fi

# 5. Per-device facts, keyed by the client alias (laptop and desktop stay
#    separate). Seed only when missing — the agent maintains it after that.
FACTS_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/remote-claude/facts"
FACTS="$FACTS_DIR/$ALIAS.json"
mkdir -p "$FACTS_DIR"
if [ ! -f "$FACTS" ]; then
  printf '%s\n' \
    '{' \
    '  "machine": { "os": "unknown", "ssh_shell": "unknown" },' \
    '  "projects": {}' \
    '}' > "$FACTS"
fi

# 6. Hand the connect-back public key back to the app to authorize locally.
printf '<<<RC_PUBKEY_BEGIN>>>\n'
cat "$KEY.pub"
printf '<<<RC_PUBKEY_END>>>\n'
