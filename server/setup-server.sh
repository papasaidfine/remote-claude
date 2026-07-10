#!/usr/bin/env bash
#
# setup-server.sh — run this ON THE REMOTE SERVER.
#
# Prepares the server side of the reverse SSH setup so Claude / Codex running
# on the server can work on the LOCAL machine through the reverse tunnel:
#
#   1. Uses (or generates) the default ~/.ssh/id_ed25519 as the connect-back key and
#      prints its public key — paste it into the local bootstrap script.
#   2. Writes a managed "Host my-device" block into ~/.ssh/config that
#      points at the reverse tunnel (127.0.0.1:<reverse_port>).
#   3. Installs helper commands into ~/.local/bin — for the human user; the
#      agent itself just runs `ssh my-device ...` as the CLAUDE.md instructs:
#        claude-local        open a shell / run a command on the local machine
#                            (handy for testing the tunnel)
#        claude-local-mount  mount the local project dir here via sshfs so the
#                            agent's file tools see the real files (live
#                            mount — no mutagen-style sync needed)
#   4. Stores defaults (local project dir) in ~/.config/claude-local/env.
#   5. Optionally installs agent instructions into ~/.claude/CLAUDE.md telling
#      Claude Code to run all project work through `ssh my-device`, keep this
#      server to lightweight script drafting (no data, no toolchains; scratch
#      in ~/tmp), and reach project files with the file tools only via scp
#      round-trips or an sshfs mount.
#
# Everything is user-level: no sudo required, idempotent, safe to re-run.
#
# Usage:  ./setup-server.sh
# Non-interactive overrides via env vars: REVERSE_PORT, LOCAL_USER,
# LOCAL_PROJECT_DIR.

set -euo pipefail

LOCAL_ALIAS="my-device"
KEY_NAME="id_ed25519"
SSH_DIR="$HOME/.ssh"
KEY_PATH="$SSH_DIR/$KEY_NAME"
SSH_CONFIG="$SSH_DIR/config"
BIN_DIR="$HOME/.local/bin"
CONF_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/claude-local"
CONF_FILE="$CONF_DIR/env"
TS="$(date +%Y%m%d-%H%M%S)"

BEGIN_MARK="# >>> ${LOCAL_ALIAS} (managed by reverse-ssh-bootstrap) >>>"
END_MARK="# <<< ${LOCAL_ALIAS} <<<"

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

cat <<'EOF'
==========================================================
 Reverse SSH bootstrap — SERVER side
 Installs the my-device ssh alias, agent instructions and
 helper commands so agents on this server can work on your
 local machine.
==========================================================
EOF
echo

# ---------------------------------------------------------------- inputs
REVERSE_PORT="${REVERSE_PORT:-$(ask 'Reverse SSH port on this server (must match the local bootstrap)' '2222')}"
[[ "$REVERSE_PORT" =~ ^[0-9]+$ ]] || die "Reverse port must be a number"
LOCAL_USER="${LOCAL_USER:-$(ask 'Username on the LOCAL machine')}"
[[ -n "$LOCAL_USER" ]] || die "Local username must not be empty"
echo
echo "Optional: DEFAULT project directory on the LOCAL machine. This is only a"
echo "default — just tell the agent which project to work on per session, or"
echo "set the CLAUDE_LOCAL_DIR environment variable."
LOCAL_PROJECT_DIR="${LOCAL_PROJECT_DIR:-$(ask 'Default local project directory (empty = local home directory)' '')}"

# ---------------------------------------------------------------- connect-back key
mkdir -p "$SSH_DIR"
chmod 700 "$SSH_DIR"
# Use the default SSH key; generate it only when it does not exist yet.
if [[ -f "$KEY_PATH" ]]; then
  if [[ ! -f "$KEY_PATH.pub" ]]; then
    ssh-keygen -y -P "" -f "$KEY_PATH" > "$KEY_PATH.pub" 2>/dev/null \
      || die "$KEY_PATH exists but $KEY_PATH.pub is missing and could not be derived (passphrase-protected?); please fix and re-run"
  fi
  log "Using existing SSH key: $KEY_PATH"
  ssh-keygen -y -P "" -f "$KEY_PATH" >/dev/null 2>&1 \
    || warn "This key appears to be passphrase-protected; agents will need a running ssh-agent to use it non-interactively"
else
  log "Generating the default SSH key: $KEY_PATH"
  ssh-keygen -t ed25519 -f "$KEY_PATH" -N "" >/dev/null
fi
chmod 600 "$KEY_PATH"

# ---------------------------------------------------------------- ~/.ssh/config
write_ssh_config_block() {
  local block tmp
  block="$(cat <<EOF
$BEGIN_MARK
Host $LOCAL_ALIAS
    HostName 127.0.0.1
    Port $REVERSE_PORT
    User $LOCAL_USER
    IdentityFile ~/.ssh/$KEY_NAME
    IdentitiesOnly yes
    StrictHostKeyChecking accept-new
    UserKnownHostsFile ~/.ssh/known_hosts.$LOCAL_ALIAS
    ForwardAgent no
    ServerAliveInterval 30
    ServerAliveCountMax 3
$END_MARK
EOF
)"
  touch "$SSH_CONFIG"
  chmod 600 "$SSH_CONFIG"

  if grep -qF "$BEGIN_MARK" "$SSH_CONFIG"; then
    if ! ask_yn "~/.ssh/config already contains a $LOCAL_ALIAS block, update it" "Y"; then
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
    if grep -qE "^[[:space:]]*Host[[:space:]]+.*\b$LOCAL_ALIAS\b" "$SSH_CONFIG"; then
      warn "~/.ssh/config contains a 'Host $LOCAL_ALIAS' block that is not managed by this tool."
      warn "ssh uses first-match-wins, so the earlier block would override what this tool writes."
      if ! ask_yn "Write the block anyway (cleaning up the old block manually is recommended)" "N"; then
        die "Aborted. Please remove the old Host $LOCAL_ALIAS block and re-run"
      fi
    fi
    cp "$SSH_CONFIG" "$SSH_CONFIG.claude-bak-$TS"
    log "Backed up ~/.ssh/config -> $SSH_CONFIG.claude-bak-$TS"
  fi

  { [[ -s "$SSH_CONFIG" ]] && [[ "$(tail -c1 "$SSH_CONFIG")" != "" ]] && echo; printf '%s\n' "$block"; } >> "$SSH_CONFIG"
  log "Wrote Host $LOCAL_ALIAS to ~/.ssh/config"
}
write_ssh_config_block

# ---------------------------------------------------------------- defaults file
mkdir -p "$CONF_DIR"
if [[ -f "$CONF_FILE" ]]; then
  cp "$CONF_FILE" "$CONF_FILE.claude-bak-$TS"
fi
cat > "$CONF_FILE" <<EOF
# Defaults for the claude-local wrappers (managed by reverse-ssh-bootstrap).
# Environment variables set at run time take precedence over this file.
CLAUDE_LOCAL_HOST="$LOCAL_ALIAS"
CLAUDE_LOCAL_DIR="$LOCAL_PROJECT_DIR"
EOF
log "Wrote defaults to $CONF_FILE"

# ---------------------------------------------------------------- scratch dir
# The agent instructions point at ~/tmp for scratch files; make sure it exists.
mkdir -p "$HOME/tmp"

# ---------------------------------------------------------------- wrappers
mkdir -p "$BIN_DIR"

install_wrapper() { # install_wrapper <name>  (content on stdin)
  local path="$BIN_DIR/$1"
  cat > "$path"
  chmod 755 "$path"
  log "Installed $path"
}

install_wrapper "claude-local" <<'WRAPPER'
#!/usr/bin/env bash
#
# claude-local — convenience command for the human user (managed by
# reverse-ssh-bootstrap). The agent does not need it: it runs
# `ssh my-device ...` directly, as instructed by ~/.claude/CLAUDE.md.
#
#   claude-local              interactive shell on the local machine
#   claude-local <command>    run the command on the local machine
#
# Configuration (env vars beat ~/.config/claude-local/env):
#   CLAUDE_LOCAL_HOST  ssh Host alias to use            (default: my-device)
#   CLAUDE_LOCAL_DIR   directory on the local machine to run commands in
#                      (default: empty = the local user's home directory)

set -u

# Load defaults from the config file; environment variables take precedence.
CONF_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/claude-local/env"
env_host="${CLAUDE_LOCAL_HOST:-}"
env_dir_set="${CLAUDE_LOCAL_DIR+x}"
env_dir="${CLAUDE_LOCAL_DIR:-}"
if [ -f "$CONF_FILE" ]; then
  # shellcheck disable=SC1090
  . "$CONF_FILE"
fi
[ -n "$env_host" ] && CLAUDE_LOCAL_HOST="$env_host"
[ -n "$env_dir_set" ] && CLAUDE_LOCAL_DIR="$env_dir"
TARGET="${CLAUDE_LOCAL_HOST:-my-device}"
DIR="${CLAUDE_LOCAL_DIR:-}"

# The remote command string is interpreted by the local user's login shell,
# so only the directory needs quoting — the command itself IS shell code.
qdir=""
prefix=""
if [ -n "$DIR" ]; then
  qdir="$(printf '%q' "$DIR")"
  prefix="cd $qdir && "
fi

if [ $# -gt 0 ]; then
  exec ssh -q -o LogLevel=ERROR "$TARGET" "${prefix}$*"
fi

# No arguments: open an interactive shell on the local machine.
if [ -n "$DIR" ]; then
  exec ssh -t -o LogLevel=ERROR "$TARGET" "cd $qdir && exec \"\$SHELL\" -l"
fi
exec ssh -t -o LogLevel=ERROR "$TARGET"
WRAPPER

install_wrapper "claude-local-mount" <<'WRAPPER'
#!/usr/bin/env bash
#
# claude-local-mount — optional helper (managed by reverse-ssh-bootstrap).
# Mounts the local project directory on this server via sshfs so file tools
# (Read/Edit) see the same files that shell commands touch through
# `ssh my-device`. A live mount — no mutagen-style syncing involved.
#
#   claude-local-mount [mountpoint]     default: ~/claude-local-project
#   claude-local-mount -u [mountpoint]  unmount
#
# Requires sshfs on this server and CLAUDE_LOCAL_DIR to be set (or configured
# in ~/.config/claude-local/env).

set -eu

# Load defaults from the config file; environment variables take precedence.
CONF_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/claude-local/env"
env_host="${CLAUDE_LOCAL_HOST:-}"
env_dir_set="${CLAUDE_LOCAL_DIR+x}"
env_dir="${CLAUDE_LOCAL_DIR:-}"
if [ -f "$CONF_FILE" ]; then
  # shellcheck disable=SC1090
  . "$CONF_FILE"
fi
[ -n "$env_host" ] && CLAUDE_LOCAL_HOST="$env_host"
[ -n "$env_dir_set" ] && CLAUDE_LOCAL_DIR="$env_dir"
TARGET="${CLAUDE_LOCAL_HOST:-my-device}"
DIR="${CLAUDE_LOCAL_DIR:-}"

unmount=0
if [ "${1:-}" = "-u" ]; then
  unmount=1
  shift
fi
MOUNTPOINT="${1:-$HOME/claude-local-project}"

if [ "$unmount" -eq 1 ]; then
  fusermount -u "$MOUNTPOINT" 2>/dev/null || umount "$MOUNTPOINT"
  echo "Unmounted $MOUNTPOINT"
  exit 0
fi

command -v sshfs >/dev/null || {
  echo "sshfs is not installed on this server (e.g. apt install sshfs)" >&2
  exit 1
}
[ -n "$DIR" ] || {
  echo "CLAUDE_LOCAL_DIR is not set; set it or configure $CONF_FILE" >&2
  exit 1
}

mkdir -p "$MOUNTPOINT"
sshfs "$TARGET:$DIR" "$MOUNTPOINT" -o reconnect,ServerAliveInterval=15,ServerAliveCountMax=3
echo "Mounted $TARGET:$DIR at $MOUNTPOINT"
echo "Unmount with: claude-local-mount -u $MOUNTPOINT"
WRAPPER

# Remove wrappers installed by older versions of this tool (the SHELL
# override workflow was dropped in favor of the CLAUDE.md instructions).
for stale in claude-local-shell claude-my-device; do
  if [[ -f "$BIN_DIR/$stale" ]]; then
    rm -f "$BIN_DIR/$stale"
    log "Removed obsolete wrapper: $BIN_DIR/$stale"
  fi
done

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) warn "$BIN_DIR is not on your PATH; add it (e.g. export PATH=\"\$HOME/.local/bin:\$PATH\")" ;;
esac

# ---------------------------------------------------------------- CLAUDE.md (optional)
# Global agent memory telling Claude Code to do ALL project work through
# `ssh my-device` instead of this server's filesystem (no Read/Edit/Glob on
# project files). Managed as a marker-delimited block, so re-runs update it
# and user content around it is preserved.
CLAUDE_MD="$HOME/.claude/CLAUDE.md"
CLAUDE_MD_BEGIN="<!-- >>> my-device (managed by reverse-ssh-bootstrap) >>> -->"
CLAUDE_MD_END="<!-- <<< my-device <<< -->"

install_claude_md() {
  local proj content tmp
  proj="${LOCAL_PROJECT_DIR:-<none configured yet — ask the user>}"
  content="$(cat <<'CLAUDE_MD_EOF'
# my-device: all project work happens over SSH

This machine is only where the agent runs. The real development environment —
the project files, toolchain, tests, git, data — is the user's own machine,
reachable as `my-device` through a reverse SSH tunnel.

Project directory on my-device: the user will normally say which project to
work on — use that path in every `cd`. If they did not say, the configured
default is `__PROJECT_DIR__` (also `CLAUDE_LOCAL_DIR` in
`~/.config/claude-local/env`). When no directory is clear, ask the user
instead of guessing, and record the answer by updating `CLAUDE_LOCAL_DIR`
in that file so later sessions start with the right default (touching that
file here is fine — it is config, not project data). Note that
`ssh my-device` lands in the home directory, so every command must `cd`
explicitly.

## Hard rules

- Everything that touches the project — build, test, lint, git, running any
  program, generating any data — happens ON MY-DEVICE through the Bash tool:
  `ssh my-device 'cd <project dir> && <command>'`
- This server is for lightweight work only: drafting small scripts and
  patches, and network tools (WebSearch / WebFetch), which run here and are
  fine to use directly. Do not generate project data on this machine and do
  not install project toolchains or dependencies here.
- Use `~/tmp/` on this server for scratch files.
- Read, Edit, Write, Glob, Grep, and NotebookEdit operate on THIS machine's
  filesystem, which does not contain the project. Never point them at
  project paths directly — anything they read is wrong and anything they
  write is lost. To use them on project files, set up one of the two
  arrangements below first.

## Using file tools on project files

1. scp round-trip — copy the file to `~/tmp/`, edit it there with the file
   tools, copy it back:

       scp my-device:'<project dir>/src/main.py' ~/tmp/
       (Read / Edit ~/tmp/main.py)
       scp ~/tmp/main.py my-device:'<project dir>/src/main.py'

   If commands on my-device may have changed the file in between, re-copy
   it before editing again.

2. sshfs mount — mount the project directory onto this server (needs sshfs
   installed here); file tools then see the real project files at the
   mountpoint:

       mkdir -p ~/claude-local-project
       sshfs my-device:'<project dir>' ~/claude-local-project \
             -o reconnect,ServerAliveInterval=15,ServerAliveCountMax=3
       fusermount -u ~/claude-local-project     # unmount when done

   Still run commands through `ssh my-device`, not against the mount.

## Patterns

Explore and read:

    ssh my-device 'cd <project dir> && ls -la'
    ssh my-device 'cd <project dir> && sed -n "1,120p" src/main.py'
    ssh my-device 'cd <project dir> && grep -rn "pattern" src/'

Run, test, git:

    ssh my-device 'cd <project dir> && make test'
    ssh my-device 'cd <project dir> && git status'

Write a script here, ship it, run it over there:

    (Write ~/tmp/fix.py with the file tools)
    scp ~/tmp/fix.py my-device:'<project dir>/'
    ssh my-device 'cd <project dir> && python fix.py'

Small edits — prefer a patch over rewriting the file:

    ssh my-device 'cd <project dir> && git apply' <<'EOF'
    diff --git a/src/main.py b/src/main.py
    ...
    EOF

When quoting through two shells gets hairy, pipe a whole script instead
(quoted delimiters stop local expansion):

    ssh my-device 'bash -s' <<'REMOTE'
    cd <project dir>
    cat > src/config.py <<'EOF'
    ...new file content...
    EOF
    REMOTE

## When ssh my-device fails

- `Connection refused`: the reverse tunnel is down. Tell the user to start
  `ssh -N remote-claude` on their machine (or check its autostart). Nothing
  on this server can fix it — do not retry endlessly or work around it by
  editing files here.
- Host key mismatch: stop and tell the user; the machine behind the tunnel
  may have changed.
CLAUDE_MD_EOF
)"
  content="${content//__PROJECT_DIR__/$proj}"

  mkdir -p "$(dirname "$CLAUDE_MD")"
  touch "$CLAUDE_MD"
  if grep -qF "$CLAUDE_MD_BEGIN" "$CLAUDE_MD"; then
    cp "$CLAUDE_MD" "$CLAUDE_MD.claude-bak-$TS"
    tmp="$(mktemp)"
    awk -v begin="$CLAUDE_MD_BEGIN" -v end="$CLAUDE_MD_END" '
      $0 == begin { skip = 1; next }
      $0 == end   { skip = 0; next }
      !skip { print }
    ' "$CLAUDE_MD" > "$tmp"
    cat "$tmp" > "$CLAUDE_MD"
    rm -f "$tmp"
  elif [[ -s "$CLAUDE_MD" ]]; then
    cp "$CLAUDE_MD" "$CLAUDE_MD.claude-bak-$TS"
  fi
  { [[ -s "$CLAUDE_MD" ]] && [[ "$(tail -c1 "$CLAUDE_MD")" != "" ]] && echo; \
    printf '%s\n%s\n%s\n' "$CLAUDE_MD_BEGIN" "$content" "$CLAUDE_MD_END"; } >> "$CLAUDE_MD"
  log "Installed agent instructions into $CLAUDE_MD"
}

echo
if ask_yn "Install agent instructions into ~/.claude/CLAUDE.md (tell Claude Code to work on my-device via ssh, not on this server's files)" "Y"; then
  install_claude_md
fi

# ---------------------------------------------------------------- summary
cat <<EOF

==========================================================
 Done! Next steps
==========================================================
1. Paste this public key into the LOCAL bootstrap script when it asks for
   the "server-side public key" (it adds the loopback-only restriction):

$(cat "$KEY_PATH.pub")

2. Start the tunnel on the LOCAL machine:
     ssh -N remote-claude

3. Test the connect-back from this server:
     ssh $LOCAL_ALIAS 'echo ok'

4. Normal workflow: connect to this server as usual (e.g. VSCode
   Remote-SSH) and just start 'claude'. With the ~/.claude/CLAUDE.md
   installed by this script, it does all project work on your machine
   through 'ssh my-device' — simply tell it which local project to use${LOCAL_PROJECT_DIR:+
   (default: $LOCAL_PROJECT_DIR)}.

   Helper commands for YOU at the terminal (the agent does not need them):
     claude-local                          # interactive shell over there
     claude-local git status               # run one command over there
     claude-local-mount                    # sshfs-mount the project here so
                                           # file tools see the real files
EOF
