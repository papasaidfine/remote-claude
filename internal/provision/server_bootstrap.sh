set -eu
# Driven non-interactively by the app over `ssh remote-claude 'bash -s'`.
# The app prepends ALIAS / REVERSE_PORT / LOCAL_USER / LOCAL_PUBKEY assignments.
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

# 4. Hand the connect-back public key back to the app to authorize locally.
printf '<<<RC_PUBKEY_BEGIN>>>\n'
cat "$KEY.pub"
printf '<<<RC_PUBKEY_END>>>\n'
