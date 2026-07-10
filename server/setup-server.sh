#!/usr/bin/env bash
#
# setup-server.sh — run this ON THE REMOTE SERVER.
#
# Prepares the server side of the reverse SSH setup so Claude / Codex running
# on the server can work on the LOCAL machine through the reverse tunnel:
#
#   1. Uses (or generates) the default ~/.ssh/id_ed25519 as the connect-back key and
#      prints its public key — paste it into the local bootstrap script.
#   2. Asks for the LOCAL machine's public key and appends it to this
#      server's ~/.ssh/authorized_keys (dedup by key blob) — that is what
#      authorizes the tunnel login (ssh -N remote-claude).
#   3. Writes a managed "Host my-device" block into ~/.ssh/config that
#      points at the reverse tunnel (127.0.0.1:<reverse_port>).
#   4. Optionally installs agent instructions into ~/.claude/CLAUDE.md telling
#      Claude Code to run all project work through `ssh my-device`, keep this
#      server to lightweight script drafting (no data, no toolchains; scratch
#      in ~/tmp), and reach project files with the file tools only via scp
#      round-trips. Seeds an agent-maintained facts file
#      (~/.config/remote-claude/facts.json: OS, project paths/descriptions)
#      that the agent reads at session start, so nothing is rediscovered.
#
# Everything is user-level: no sudo required, idempotent, safe to re-run.
#
# Usage:  ./setup-server.sh
# Non-interactive overrides via env vars: REVERSE_PORT, LOCAL_USER,
# LOCAL_PUBKEY, LOCAL_PROJECT_DIR.

set -euo pipefail

LOCAL_ALIAS="my-device"
KEY_NAME="id_ed25519"
SSH_DIR="$HOME/.ssh"
KEY_PATH="$SSH_DIR/$KEY_NAME"
SSH_CONFIG="$SSH_DIR/config"
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
 Installs the my-device ssh alias and the CLAUDE.md agent
 instructions so agents on this server can work on your
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
echo "Public key of the LOCAL machine: the .pub of the key the tunnel"
echo "(ssh -N remote-claude) logs in to this server with. The local bootstrap"
echo "prints it at the end, or run: cat ~/.ssh/id_ed25519.pub  on your machine."
echo "(paste the whole line; leave empty to skip and authorize it yourself later)"
LOCAL_PUBKEY="${LOCAL_PUBKEY:-$(ask 'Local machine public key' '')}"
echo
echo "Optional: a project directory on the LOCAL machine to pre-record in the"
echo "agent's memory (the facts file). You can always just tell the agent"
echo "which project to work on per session."
LOCAL_PROJECT_DIR="${LOCAL_PROJECT_DIR:-$(ask 'Local project directory to pre-record (empty = skip)' '')}"

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

# ---------------------------------------------------------------- authorized_keys (tunnel login)
AUTH_KEYS="$SSH_DIR/authorized_keys"
add_authorized_key() { # add_authorized_key <pubkey line>
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
  touch "$AUTH_KEYS"
  chmod 600 "$AUTH_KEYS"
  if grep -qF "$blob" "$AUTH_KEYS"; then
    log "This public key is already in authorized_keys, skipping"
    return 0
  fi
  printf '%s\n' "$pubkey" >> "$AUTH_KEYS"
  log "Added the local machine's key to ~/.ssh/authorized_keys (authorizes the tunnel login)"
}

if [[ -n "$LOCAL_PUBKEY" ]]; then
  add_authorized_key "$LOCAL_PUBKEY"
else
  warn "No local public key provided — the tunnel login is not authorized yet."
  warn "Re-run this script and paste it, or run on the LOCAL machine:"
  warn "  ssh-copy-id -i ~/.ssh/id_ed25519.pub <your user>@<this server>"
fi

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

# ---------------------------------------------------------------- scratch dir
# The agent instructions point at ~/tmp for scratch files; make sure it exists.
mkdir -p "$HOME/tmp"

# ---------------------------------------------------------------- CLAUDE.md (optional)
# Global agent memory telling Claude Code to do ALL project work through
# `ssh my-device` instead of this server's filesystem (no Read/Edit/Glob on
# project files). Managed as a marker-delimited block, so re-runs update it
# and user content around it is preserved.
CLAUDE_MD="$HOME/.claude/CLAUDE.md"
CLAUDE_MD_BEGIN="<!-- >>> my-device (managed by reverse-ssh-bootstrap) >>> -->"
CLAUDE_MD_END="<!-- <<< my-device <<< -->"

install_claude_md() {
  local content tmp
  content="$(cat <<'CLAUDE_MD_EOF'
# my-device: the project lives on the user's machine

You run on this server, but the real development environment — project
files, toolchain, tests, git, data — is the user's own machine, reachable
as `my-device` through a reverse SSH tunnel.

Work EXACTLY as you would on a local project: explore first, read the
relevant files, then plan, then edit, then run and verify. Being remote
changes none of that order. Only two mechanics differ:

1. Shell commands run on my-device through the Bash tool:
   `ssh my-device 'cd <project dir> && <command>'`
   Every ssh lands in the home directory — cd explicitly in each command.
2. The file tools (Read, Edit, Write, Glob, Grep, NotebookEdit) are the
   one exception: they operate on THIS server's filesystem, which does not
   contain the project. Never point them at project paths — use the scp
   round-trip below.

The tell-tale failure to avoid: drafting project code under `~/tmp/`
before you have even listed the project directory. If you would have
explored first on a local path, explore first here — over ssh.

## Boundaries

- Build, test, lint, git, running programs, generating data: on my-device
  only. Do not install project toolchains or dependencies on this server,
  and do not keep project data here.
- Fine on this server: network tools (WebSearch / WebFetch) and scratch
  files under `~/tmp/`.

## File tools: the scp round-trip

Edit an existing project file — copy it here, edit, copy it back:

    scp my-device:'<project dir>/src/main.py' ~/tmp/
    (Read / Edit ~/tmp/main.py)
    scp ~/tmp/main.py my-device:'<project dir>/src/main.py'

Create a new project file — Write it under `~/tmp/`, then scp it in:

    (Write ~/tmp/snake.py)
    scp ~/tmp/snake.py my-device:'<project dir>/src/snake.py'

If commands on my-device may have changed a file in between, re-copy it
before editing again.

## Durable facts: ~/.config/remote-claude/facts.json

Your persistent memory about the user's machine. Read it at the START of
every session; update it (create it if missing) whenever you learn a
durable fact.

    {
      "machine": { "os": "unknown", "ssh_shell": "unknown" },
      "projects": {
        "foo": { "path": "~/projects/foo", "desc": "Rust CLI tool" }
      }
    }

- machine: while "unknown", detect on first contact (`ssh my-device
  uname -s`; if that errors it is likely Windows — try
  `ssh my-device cmd /c ver`) and record the result.
- projects: one entry per project — path on my-device plus a one-line
  description.

Which project to work on: the user normally says; otherwise check the
facts file; when still unclear, ask — never guess. Record the answer.

## Patterns

These assume a POSIX shell on my-device. If the facts file says Windows
(cmd / PowerShell), translate accordingly and record working equivalents
in the facts file.

Explore and read:

    ssh my-device 'cd <project dir> && ls -la'
    ssh my-device 'cd <project dir> && sed -n "1,120p" src/main.py'
    ssh my-device 'cd <project dir> && grep -rn "pattern" src/'

Run, test, git:

    ssh my-device 'cd <project dir> && make test'
    ssh my-device 'cd <project dir> && git status'

Small edits — a patch beats rewriting the file:

    ssh my-device 'cd <project dir> && git apply' <<'EOF'
    diff --git a/src/main.py b/src/main.py
    ...
    EOF

When quoting through two shells gets hairy, pipe a whole script instead
(quoted delimiters stop local expansion):

    ssh my-device 'bash -s' <<'REMOTE'
    cd <project dir>
    ...
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

# Agent-maintained memory (OS of my-device, projects) that the
# instructions above tell the agent to read at session start and keep
# updated. Seeded once; never overwritten on re-runs — the agent owns it.
FACTS_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/remote-claude/facts.json"

install_facts_file() {
  if [[ -f "$FACTS_FILE" ]]; then
    log "Facts file already present, leaving it as is: $FACTS_FILE"
    return 0
  fi
  local projects="{}"
  if [[ -n "$LOCAL_PROJECT_DIR" ]]; then
    # JSON-escape backslashes and quotes (think Windows paths)
    local esc name
    esc="${LOCAL_PROJECT_DIR//\\/\\\\}"; esc="${esc//\"/\\\"}"
    name="$(basename "${LOCAL_PROJECT_DIR//\\//}")"
    name="${name//\"/\\\"}"
    projects="{ \"$name\": { \"path\": \"$esc\", \"desc\": \"\" } }"
  fi
  mkdir -p "$(dirname "$FACTS_FILE")"
  cat > "$FACTS_FILE" <<EOF
{
  "machine": { "os": "unknown", "ssh_shell": "unknown" },
  "projects": $projects
}
EOF
  log "Seeded the facts file: $FACTS_FILE"
}

echo
if ask_yn "Install agent instructions into ~/.claude/CLAUDE.md (tell Claude Code to work on my-device via ssh, not on this server's files)" "Y"; then
  install_claude_md
  install_facts_file
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
   (pre-recorded in its memory: $LOCAL_PROJECT_DIR)}.
EOF

if [[ -z "$LOCAL_PUBKEY" ]]; then
  echo
  warn "Reminder: the tunnel login (step 2) is NOT authorized yet — no local"
  warn "public key was pasted. Re-run this script and paste it, or from the"
  warn "local machine: ssh-copy-id -i ~/.ssh/id_ed25519.pub $USER@<this server>"
fi
