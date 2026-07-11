# Menu Catalog Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the four setup scripts into interactive menu catalogs of independent, individually runnable items with detected status, and remove the tunnel-autostart feature everywhere.

**Architecture:** Each script becomes: constants + helpers, one `status_<item>`/`run_<item>` function pair per catalog item, and a menu loop that redraws the catalog with fresh statuses after every action. Items are executed in a subshell with `set -e` (bash) or a `try/catch` (PowerShell) so a failing item returns to the menu instead of killing the script. Per-item inputs replace the up-front input block; env-var/parameter overrides keep working.

**Tech Stack:** bash (Linux/macOS/server scripts), PowerShell 5.1+ (Windows script). No test framework exists in the repo; verification is `bash -n`, PowerShell AST parse (if `pwsh` is available), and sandboxed smoke runs with `HOME=$(mktemp -d)` and piped menu selections.

**Spec:** `docs/superpowers/specs/2026-07-11-menu-catalog-design.md`

---

## Shared design notes (read before any task)

- **Error semantics (bash):** the global `set -euo pipefail` becomes `set -uo pipefail` (no `-e`). Each menu selection runs as `( set -e; run_xxx )` — a plain subshell command, NOT inside `if`/`||` (bash suppresses `-e` in condition contexts). `die()` stays `exit 1`; inside the subshell it aborts only that item.
- **EOF safety:** the menu `read` must `|| break`, otherwise exhausted piped stdin loops forever.
- **`LOCAL_USER` on the local bootstraps:** it was only used in the deleted final summary; the prompt and variable disappear from the three local bootstraps (it remains an input of `setup-server.sh` item 3).
- **Status marks:** `[done]` / `[ -  ]` (5 chars each, aligned).
- Commit after each task. Never mention Claude in commit messages (user rule).

---

### Task 1: Restructure `local/bootstrap-linux.sh`

**Files:**
- Modify: `local/bootstrap-linux.sh` (structure per below; item bodies reuse existing code by the pre-change line numbers of commit `d5daf13`)

- [ ] **Step 1: Rewrite the header comment and top-level setup**

Replace the header (lines 1-27) so "What it does" lists the five catalog items and states that each is selected from a menu, is idempotent, and shows detected status. Keep the env-var note (`SERVER_HOST, SERVER_USER, SERVER_PORT, REVERSE_PORT, SERVER_PUBKEY`; drop `LOCAL_USER`). Change `set -euo pipefail` → `set -uo pipefail` with a comment:

```bash
# No -e at the top level: menu items run in their own `set -e` subshell so a
# failing item returns to the menu instead of killing the script.
set -uo pipefail
```

Delete the constants `USER_UNIT_DIR` and `USER_UNIT` (lines 38-39). Keep everything else in lines 28-75 (constants, `log/warn/err/die`, `ask`, `ask_yn`, platform check) unchanged.

- [ ] **Step 2: Replace the intro banner and delete the up-front input block**

Replace the banner heredoc (lines 77-87) with:

```bash
cat <<'EOF'
==========================================================
 Reverse SSH bootstrap (Linux)
 local linux  ->  remote server  ->  (reverse tunnel)  -> local linux
==========================================================
Pick items from the menu below; each one is independent, idempotent,
and shows whether it is already configured. Files this can modify:
  - openssh-server install / sshd service / sshd_config (item 1, sudo)
  - ~/.ssh/{config,authorized_keys,id_ed25519}
All modified system files are backed up first (*.claude-bak-<timestamp>).
EOF
```

Delete the whole input block (lines 89-112) and the `~/.ssh` prep block (lines 114-119); the prep becomes:

```bash
ensure_ssh_dir() {
  mkdir -p "$SSH_DIR"
  chmod 700 "$SSH_DIR"
  touch "$AUTH_KEYS"
  chmod 600 "$AUTH_KEYS"
}
```

- [ ] **Step 3: Turn the linear sections into `run_` functions**

Keep these existing functions verbatim: `add_authorized_key` (138-155), `find_sshd_bin` (222-228), `install_sshd` (230-251), `find_sshd_unit` (255-264), `configure_sshd` (277-336).

`write_ssh_config_block` (165-215): keep the body, but parameterize — first line of the function becomes:

```bash
write_ssh_config_block() { # <host> <user> <port> <reverse_port>
  local SERVER_HOST="$1" SERVER_USER="$2" SERVER_PORT="$3" REVERSE_PORT="$4"
  local block tmp
```

Then define the five items:

```bash
run_sshd() { # item 1: install/enable sshd + harden (sudo)
  log "This item installs/enables the system sshd and hardens its config (requires sudo)"
  local DISABLE_PASSWORD
  if ask_yn "Disable password login for the local sshd (recommended, public key only)" "Y"; then
    DISABLE_PASSWORD=1
  else
    DISABLE_PASSWORD=0
  fi
  install_sshd
  SSHD_BIN="$(find_sshd_bin)"
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
  configure_sshd
}

run_key() { # item 2: ensure ~/.ssh/id_ed25519 exists
  ensure_ssh_dir
  # ...existing key logic, lines 123-135, unchanged...
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
  local server_host server_user server_port reverse_port
  server_host="${SERVER_HOST:-$(ask 'Remote server hostname / IP')}"
  [[ -n "$server_host" ]] || die "Server hostname must not be empty"
  server_user="${SERVER_USER:-$(ask 'Remote server SSH user')}"
  [[ -n "$server_user" ]] || die "Server user must not be empty"
  server_port="${SERVER_PORT:-$(ask 'Remote server SSH port' '22')}"
  [[ "$server_port" =~ ^[0-9]+$ ]] || die "SSH port must be a number"
  reverse_port="${REVERSE_PORT:-$(ask 'Reverse SSH port on the server (used by Claude/Codex to connect back)' '2222')}"
  [[ "$reverse_port" =~ ^[0-9]+$ ]] || die "Reverse port must be a number"
  write_ssh_config_block "$server_host" "$server_user" "$server_port" "$reverse_port"
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
```

Delete: the old sshd top-level driver lines (218-220, 251-253, 266-275, 336), the pubkey-handoff section (338-346), the whole systemd-user-service autostart section (348-389), and the summary heredoc (391-422).

- [ ] **Step 4: Add status functions and the menu loop (end of file)**

```bash
# ---------------------------------------------------------------- status checks
status_sshd() {
  find_sshd_bin >/dev/null || return 1
  if command -v systemctl >/dev/null && [[ -d /run/systemd/system ]]; then
    local unit
    unit="$(find_sshd_unit || true)"
    [[ -n "$unit" ]] || return 1
    systemctl is-active --quiet "$unit.service" || return 1
  fi
  [[ -f "$SSHD_DROPIN" ]] && return 0
  grep -qE '^[[:space:]]*PubkeyAuthentication[[:space:]]+yes' "$SSHD_CONFIG" 2>/dev/null
}
status_key()       { [[ -f "$KEY_PATH" ]]; }
status_authorize() { grep -qF 'from="127.0.0.1,::1"' "$AUTH_KEYS" 2>/dev/null; }
status_config()    { grep -qF "$BEGIN_MARK" "$SSH_CONFIG" 2>/dev/null; }

# ---------------------------------------------------------------- menu
mark() { if "$1"; then printf '[done]'; else printf '[ -  ]'; fi; }

draw_menu() {
  echo
  echo "----------------------------------------------------------"
  printf '  1) %-50s %s\n' 'Incoming SSH — sshd install + harden  [sudo]' "$(mark status_sshd)"
  printf '  2) %-50s %s\n' 'Local SSH key (~/.ssh/id_ed25519)' "$(mark status_key)"
  printf '  3) %-50s %s\n' "Authorize the server's connect-back key" "$(mark status_authorize)"
  printf '  4) %-50s %s\n' 'Tunnel config (Host remote-claude)' "$(mark status_config)"
  printf '  5) %s\n' 'Show local public key (paste into server setup)'
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

while true; do
  draw_menu
  read -r -p "Select [1-5, q]: " choice || break
  case "$choice" in
    1) run_item run_sshd ;;
    2) run_item run_key ;;
    3) run_item run_authorize ;;
    4) run_item run_config ;;
    5) run_item run_show_key ;;
    q|Q) break ;;
    *) warn "Unknown selection: $choice" ;;
  esac
done

echo
log "Start the tunnel with: ssh -N $TUNNEL_ALIAS   (keep it running)"
log "Then on the server: ssh my-device 'echo ok' should print ok"
```

- [ ] **Step 5: Syntax check**

Run: `bash -n local/bootstrap-linux.sh`
Expected: no output, exit 0.

- [ ] **Step 6: Sandboxed smoke test**

```bash
T=$(mktemp -d)
HOME=$T bash local/bootstrap-linux.sh <<< $'q\n'          # menu draws, exits 0
HOME=$T bash local/bootstrap-linux.sh <<< $'2\nq\n'       # item 2 generates key
HOME=$T bash local/bootstrap-linux.sh <<< $'3\n\nq\n'     # item 3, empty key -> error, back to menu
ls "$T/.ssh/id_ed25519" && rm -rf "$T"
```

Expected: first run shows the catalog with `[ -  ]` for items 2-4; second run creates `$T/.ssh/id_ed25519` and the redrawn menu shows item 2 `[done]`; third run prints the "No key pasted" error then redraws the menu (exit 0, script not killed). Item 1's status may read the real `/etc/ssh` — that is read-only and fine.

- [ ] **Step 7: Commit**

```bash
git add local/bootstrap-linux.sh
git commit -m "Restructure bootstrap-linux.sh as a status-aware menu catalog"
```

---

### Task 2: Restructure `local/bootstrap-macos.sh`

**Files:**
- Modify: `local/bootstrap-macos.sh` (same catalog as Task 1; macOS deltas below)

- [ ] **Step 1: Apply the Task 1 structure with macOS deltas**

Same header rewrite, `set -uo pipefail`, banner, `ensure_ssh_dir`, deletion of the input block, `run_key`/`run_authorize`/`run_config`/`run_show_key`, parameterized `write_ssh_config_block`, menu loop, and closing hints — identical code to Task 1 except:

- Delete constants `LAUNCH_AGENT_LABEL`, `LAUNCH_AGENT_PLIST`, `LOG_DIR` (lines 37-39).
- Keep `enable_remote_login` (222-235) and macOS `configure_sshd` (238-294) verbatim; item 1 is:

```bash
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
```

- `status_sshd` cannot use `systemsetup`/`launchctl print` (both need sudo — too heavy for drawing a menu). Probe the sshd port instead:

```bash
status_sshd() {
  nc -z 127.0.0.1 22 >/dev/null 2>&1 || return 1
  [[ -f "$SSHD_DROPIN" ]] && return 0
  grep -qE '^[[:space:]]*PubkeyAuthentication[[:space:]]+yes' "$SSHD_CONFIG" 2>/dev/null
}
```

- Menu item 1 label: `'Incoming SSH — Remote Login + harden  [sudo]'`.
- Delete: pubkey-handoff section (297-305), LaunchAgent autostart section (307-355), summary heredoc (357-388).

- [ ] **Step 2: Syntax check**

Run: `bash -n local/bootstrap-macos.sh`
Expected: no output, exit 0.

- [ ] **Step 3: Structural parity diff**

Run: `diff <(grep -E '^(status_|run_|ensure_|mark|draw_menu|run_item)' local/bootstrap-linux.sh) <(grep -E '^(status_|run_|ensure_|mark|draw_menu|run_item)' local/bootstrap-macos.sh)`
Expected: no output — both scripts define the same function set.

(No Linux-side smoke run: the script dies at the `uname` platform check by design.)

- [ ] **Step 4: Commit**

```bash
git add local/bootstrap-macos.sh
git commit -m "Restructure bootstrap-macos.sh as a status-aware menu catalog"
```

---

### Task 3: Restructure `server/setup-server.sh`

**Files:**
- Modify: `server/setup-server.sh`

- [ ] **Step 1: Header, setup, constants**

Rewrite the header (lines 1-27) to list the five items (connect-back key + show, authorize local key, my-device alias, CLAUDE.md, facts file). Keep the env-var note (`REVERSE_PORT, LOCAL_USER, LOCAL_PUBKEY, LOCAL_PROJECT_DIR`). `set -euo pipefail` → `set -uo pipefail` (same comment as Task 1). Move these constants up next to the others (lines 31-39): `AUTH_KEYS="$SSH_DIR/authorized_keys"` (from 116), `CLAUDE_MD`/`CLAUDE_MD_BEGIN`/`CLAUDE_MD_END` (from 209-211), `FACTS_FILE` (from 350). Update the banner (70-77) to say items are picked from a menu. Delete the input block (80-95).

- [ ] **Step 2: Item functions**

Keep `install_claude_md` (213-345) verbatim. Parameterize `write_ssh_config_block` (147-197): first lines become

```bash
write_ssh_config_block() { # <reverse_port> <local_user>
  local REVERSE_PORT="$1" LOCAL_USER="$2"
  local block tmp
```

Modify `add_authorized_key` (117-136): the append line (134) gets the detection tag —

```bash
  printf '%s remote-claude-tunnel\n' "$pubkey" >> "$AUTH_KEYS"
  log "Added the local machine's key to ~/.ssh/authorized_keys (authorizes the tunnel login)"
```

Parameterize `install_facts_file` (352-374): replace `LOCAL_PROJECT_DIR` with `local project_dir="$1"` (drop its early-return; `run_facts` handles the exists case). Then:

```bash
run_key_show() { # item 1: ensure the connect-back key exists + print its .pub
  mkdir -p "$SSH_DIR"
  chmod 700 "$SSH_DIR"
  # ...existing key logic, lines 101-113, unchanged...
  echo
  log "Connect-back public key — paste it into the LOCAL bootstrap (item 3,"
  log "'server-side public key'); it adds the loopback-only restriction:"
  echo
  cat "$KEY_PATH.pub"
  echo
}

run_authorize_local() { # item 2: authorize the local machine's key (tunnel login)
  mkdir -p "$SSH_DIR"
  chmod 700 "$SSH_DIR"
  local pubkey
  echo "Public key of the LOCAL machine: the .pub of the key the tunnel"
  echo "(ssh -N remote-claude) logs in to this server with. The local bootstrap"
  echo "shows it (item 5), or run: cat ~/.ssh/id_ed25519.pub  on your machine."
  pubkey="${LOCAL_PUBKEY:-$(ask 'Local machine public key' '')}"
  [[ -n "$pubkey" ]] || die "No key pasted; nothing changed"
  add_authorized_key "$pubkey"
}

run_alias() { # item 3: Host my-device block
  local reverse_port local_user
  reverse_port="${REVERSE_PORT:-$(ask 'Reverse SSH port on this server (must match the local bootstrap)' '2222')}"
  [[ "$reverse_port" =~ ^[0-9]+$ ]] || die "Reverse port must be a number"
  local_user="${LOCAL_USER:-$(ask 'Username on the LOCAL machine')}"
  [[ -n "$local_user" ]] || die "Local username must not be empty"
  mkdir -p "$SSH_DIR"
  chmod 700 "$SSH_DIR"
  write_ssh_config_block "$reverse_port" "$local_user"
}

run_claude_md() { # item 4: agent instructions + scratch dir
  install_claude_md
  mkdir -p "$HOME/tmp"
}

run_facts() { # item 5: seed the agent facts file (never overwrites)
  if [[ -f "$FACTS_FILE" ]]; then
    log "Facts file already present, leaving it as is: $FACTS_FILE"
    return 0
  fi
  local project_dir
  echo "Optional: a project directory on the LOCAL machine to pre-record in the"
  echo "agent's memory. You can always just tell the agent per session."
  project_dir="${LOCAL_PROJECT_DIR:-$(ask 'Local project directory to pre-record (empty = skip)' '')}"
  install_facts_file "$project_dir"
}
```

Delete: the old top-level drivers (97-99 dir prep stays only inside items, 138-144, 200-202, 376-380) and the summary heredoc + reminder (382-411).

- [ ] **Step 3: Status functions and menu**

```bash
status_key()       { [[ -f "$KEY_PATH" ]]; }
status_authorize() { grep -qF 'remote-claude-tunnel' "$AUTH_KEYS" 2>/dev/null; }
status_alias()     { grep -qF "$BEGIN_MARK" "$SSH_CONFIG" 2>/dev/null; }
status_claude_md() { grep -qF "$CLAUDE_MD_BEGIN" "$CLAUDE_MD" 2>/dev/null; }
status_facts()     { [[ -f "$FACTS_FILE" ]]; }

mark() { if "$1"; then printf '[done]'; else printf '[ -  ]'; fi; }

draw_menu() {
  echo
  echo "----------------------------------------------------------"
  printf '  1) %-50s %s\n' 'Connect-back key — ensure + show public key' "$(mark status_key)"
  printf '  2) %-50s %s\n' "Authorize the local machine's key (tunnel login)" "$(mark status_authorize)"
  printf '  3) %-50s %s\n' 'my-device ssh alias (Host block)' "$(mark status_alias)"
  printf '  4) %-50s %s\n' 'Agent instructions (~/.claude/CLAUDE.md)' "$(mark status_claude_md)"
  printf '  5) %-50s %s\n' 'Agent facts file (facts.json)' "$(mark status_facts)"
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

while true; do
  draw_menu
  read -r -p "Select [1-5, q]: " choice || break
  case "$choice" in
    1) run_item run_key_show ;;
    2) run_item run_authorize_local ;;
    3) run_item run_alias ;;
    4) run_item run_claude_md ;;
    5) run_item run_facts ;;
    q|Q) break ;;
    *) warn "Unknown selection: $choice" ;;
  esac
done

echo
log "Start the tunnel on the LOCAL machine: ssh -N remote-claude"
log "Then test from this server: ssh $LOCAL_ALIAS 'echo ok'"
log "Normal workflow: connect as usual (e.g. VSCode Remote-SSH) and start 'claude'."
```

- [ ] **Step 4: Syntax check**

Run: `bash -n server/setup-server.sh`
Expected: no output, exit 0.

- [ ] **Step 5: Sandboxed smoke test**

```bash
T=$(mktemp -d)
HOME=$T bash server/setup-server.sh <<< $'q\n'                       # all [ -  ]
HOME=$T bash server/setup-server.sh <<< $'1\n4\n5\n\nq\n'            # key, CLAUDE.md, facts (empty project)
HOME=$T bash server/setup-server.sh <<< $'q\n'                       # 1/4/5 now [done]
grep -c 'my-device' "$T/.claude/CLAUDE.md"; cat "$T/.config/remote-claude/facts.json"; rm -rf "$T"
```

Expected: second run prints the pubkey, installs the CLAUDE.md block, seeds facts.json; third run shows items 1, 4, 5 as `[done]`, 2 and 3 as `[ -  ]`. Also verify the tag: run item 2 with a real pubkey (`ssh-keygen -t ed25519 -N '' -f $T/k` first) and check `grep 'remote-claude-tunnel' $T/.ssh/authorized_keys`.

- [ ] **Step 6: Commit**

```bash
git add server/setup-server.sh
git commit -m "Restructure setup-server.sh as a status-aware menu catalog"
```

---

### Task 4: Restructure `local/bootstrap-windows.ps1`

**Files:**
- Modify: `local/bootstrap-windows.ps1`

- [ ] **Step 1: Header, params, elevation**

Rewrite the comment-based help (lines 1-35) to describe the five menu items; note that only item 1 needs an elevated PowerShell. Keep the `param()` block minus `[string]$LocalUser`. Delete `$TaskName` and `$KeepAlivePs1` (lines 58-59). Keep `$ErrorActionPreference = 'Stop'`. Replace the hard elevation exit (lines 109-116) with:

```powershell
function Test-IsAdmin {
    $principal = New-Object System.Security.Principal.WindowsPrincipal(
        [System.Security.Principal.WindowsIdentity]::GetCurrent())
    return $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
}
```

Update the banner (118-127): items are picked from a menu; only item 1 modifies system state and needs Administrator. Delete the input block (130-145).

- [ ] **Step 2: Item functions**

Wrap the existing sections as functions, converting every `exit 1` inside them to `throw '<same message>'`:

```powershell
function Invoke-ItemSshd {   # item 1: OpenSSH Server install + harden (admin)
    if (-not (Test-IsAdmin)) {
        throw 'Administrator privileges are required for this item (OpenSSH Server install / sshd_config / service control). Re-run this script from an elevated PowerShell to use it.'
    }
    $DisablePassword = Read-YesNo 'Disable password login for the local sshd (recommended, public key only)' $true
    # ...existing code: capability install (148-165), service start (167-172),
    #    sshd_config hardening + validation + restart (174-230), unchanged...
}

function Invoke-ItemKey {    # item 2: ensure %USERPROFILE%\.ssh\id_ed25519 exists
    Initialize-SshDir
    # ...existing key logic (272-296), unchanged...
}

function Invoke-ItemAuthorize {  # item 3: authorize the server's connect-back key
    Initialize-SshDir
    Write-Host "Server-side public key: the .pub of the key that Claude / Codex on the"
    Write-Host "server will use to SSH back into this machine (setup-server.sh item 1"
    Write-Host "prints it, or: cat ~/.ssh/id_ed25519.pub on the server)."
    $pubkey = $ServerPublicKey
    if (-not $pubkey) { $pubkey = Read-Default 'Server-side public key' '' }
    if (-not $pubkey) { throw 'No key pasted; nothing changed' }
    # ...existing validation + dedup + append logic (241-265), using $pubkey
    #    in place of $ServerPublicKey, unchanged otherwise...
}

function Invoke-ItemConfig {     # item 4: Host remote-claude block
    Initialize-SshDir
    $srvHost = $ServerHost
    if (-not $srvHost) { $srvHost = Read-Default 'Remote server hostname / IP' }
    if (-not $srvHost) { throw 'Server hostname must not be empty' }
    $srvUser = $ServerUser
    if (-not $srvUser) { $srvUser = Read-Default 'Remote server SSH user' }
    if (-not $srvUser) { throw 'Server user must not be empty' }
    $srvPort = $ServerPort
    if ($srvPort -le 0) { $srvPort = [int](Read-Default 'Remote server SSH port' '22') }
    $revPort = $ReversePort
    if ($revPort -le 0) { $revPort = [int](Read-Default 'Reverse SSH port on the server (used by Claude/Codex to connect back)' '2222') }
    # ...existing config-block logic (299-351), with $ServerHost/$ServerUser/
    #    $ServerPort/$ReversePort replaced by $srvHost/$srvUser/$srvPort/$revPort,
    #    and its `exit 1` turned into `throw`...
}

function Invoke-ItemShowKey {    # item 5: print the local public key
    if (-not (Test-Path "$KeyPath.pub")) {
        if (-not (Test-Path $KeyPath)) {
            if (-not (Read-YesNo 'No local key yet - generate it now' $true)) { throw 'No key to show' }
        }
        Invoke-ItemKey
    }
    Write-Host ''
    Write-Info "Local public key - paste it into server/setup-server.sh (item 2) on the server; that authorizes the tunnel login (ssh -N $TunnelAlias):"
    Write-Host ''
    Get-Content "$KeyPath.pub" | Write-Host
    Write-Host ''
}
```

Add the shared dir prep (replaces lines 232-237, now called per item):

```powershell
function Initialize-SshDir {
    if (-not (Test-Path $SshDir)) { New-Item -ItemType Directory -Path $SshDir | Out-Null }
    if (-not (Test-Path $AuthKeys)) { New-Item -ItemType File -Path $AuthKeys | Out-Null }
    Set-StrictAcl -Path $SshDir -Directory
    Set-StrictAcl -Path $AuthKeys
}
```

Delete: the pubkey handoff section (353-361), the Scheduled Task autostart section (363-398), and the summary here-strings (400-430).

- [ ] **Step 3: Status functions and menu loop**

```powershell
function Test-StatusSshd {
    $svc = Get-Service -Name sshd -ErrorAction SilentlyContinue
    if (-not $svc -or $svc.Status -ne 'Running') { return $false }
    $raw = Get-Content -Raw $SshdConfig -ErrorAction SilentlyContinue
    return [bool]($raw -match '(?m)^[ \t]*PubkeyAuthentication[ \t]+yes')
}
function Test-StatusKey { return (Test-Path $KeyPath) }
function Test-StatusAuthorize {
    $raw = Get-Content -Raw $AuthKeys -ErrorAction SilentlyContinue
    return [bool]($raw -and $raw.Contains('from="127.0.0.1,::1"'))
}
function Test-StatusConfig {
    $raw = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
    return [bool]($raw -and $raw.Contains($BeginMark))
}

function Format-Mark { param([bool]$Ok) if ($Ok) { '[done]' } else { '[ -  ]' } }

function Show-Menu {
    Write-Host ''
    Write-Host '----------------------------------------------------------'
    Write-Host ('  1) {0,-50} {1}' -f 'Incoming SSH - OpenSSH Server + harden  [admin]', (Format-Mark (Test-StatusSshd)))
    Write-Host ('  2) {0,-50} {1}' -f 'Local SSH key (~\.ssh\id_ed25519)', (Format-Mark (Test-StatusKey)))
    Write-Host ('  3) {0,-50} {1}' -f "Authorize the server's connect-back key", (Format-Mark (Test-StatusAuthorize)))
    Write-Host ('  4) {0,-50} {1}' -f 'Tunnel config (Host remote-claude)', (Format-Mark (Test-StatusConfig)))
    Write-Host  '  5) Show local public key (paste into server setup)'
    Write-Host  '  q) Quit'
}

:menu while ($true) {
    Show-Menu
    $choice = (Read-Host 'Select [1-5, q]').Trim()
    if ($choice -match '^[Qq]$') { break menu }
    $fn = switch ($choice) {
        '1' { 'Invoke-ItemSshd' }
        '2' { 'Invoke-ItemKey' }
        '3' { 'Invoke-ItemAuthorize' }
        '4' { 'Invoke-ItemConfig' }
        '5' { 'Invoke-ItemShowKey' }
        default { $null }
    }
    if (-not $fn) { Write-Warn "Unknown selection: $choice"; continue }
    Write-Host ''
    try { & $fn }
    catch {
        Write-Err "Item did not complete: $_"
        Write-Err 'Other items are unaffected.'
    }
}

Write-Host ''
Write-Info "Start the tunnel with: ssh -N $TunnelAlias   (keep it running)"
Write-Info "Then on the server: ssh my-device 'echo ok' should print ok"
```

Keep the platform check (105-108) but only warn-and-exit for non-Windows.

- [ ] **Step 4: Parse check**

Run (skip with a note if `pwsh` is not installed):

```bash
pwsh -NoProfile -Command '$e=$null; [System.Management.Automation.Language.Parser]::ParseFile("local/bootstrap-windows.ps1",[ref]$null,[ref]$e)|Out-Null; if($e){$e; exit 1}; "parse ok"'
```

Expected: `parse ok`.

- [ ] **Step 5: Commit**

```bash
git add local/bootstrap-windows.ps1
git commit -m "Restructure bootstrap-windows.ps1 as a status-aware menu catalog"
```

---

### Task 5: Remove autostart artifacts and scrub the docs

**Files:**
- Delete: `examples/remote-claude.service.example`, `examples/com.claude.dev-tunnel.plist.example`
- Modify: `README.md`, `README.zh-CN.md`, `TROUBLESHOOTING.md`, `TROUBLESHOOTING.zh-CN.md`

- [ ] **Step 1: Delete the example files**

```bash
git rm examples/remote-claude.service.example examples/com.claude.dev-tunnel.plist.example
```

- [ ] **Step 2: README.md**

- Line 14: "run and answer the prompts (sudo needed for sshd setup)" → "run it and pick items from the menu — each is independent, idempotent, and shows whether it is already configured (only the sshd item needs sudo)".
- Line 29 paragraph → "The menu items ask only for what they need: the sshd item asks nothing network-related; the tunnel-config item asks for your server's address/user/port and a reverse port (default 2222); the authorize item asks for the server-side public key from step 2 — run step 2 first and paste it, or skip that item and re-run it later. The scripts never SSH anywhere themselves; key exchange is copy-paste."
- Step 2 paragraph (line 39): "It prints a public key" → "Its first menu item prints a public key"; "It also offers to install" → "Separate menu items install"; keep the facts-file description.
- Line 49: "Re-run the script and paste it" → "Re-run that menu item and paste it".
- Line 53: "(keep it running; both bootstraps also offer autostart)" → "(keep it running)".
- Lines 69-72 → single bullet: "- Stop the tunnel: `Ctrl-C` in the terminal running `ssh -N remote-claude`."
- [ ] **Step 3: README.zh-CN.md (mirror)**

- Line 14: “运行后按提示回答（配置 sshd 需要 sudo）” → “运行后从菜单里选要做的项——每一项都相互独立、可重复运行、并显示是否已配置好（只有 sshd 那一项需要 sudo）”。
- Line 29 → “各菜单项只询问自己需要的信息：隧道配置项询问服务器地址/用户/端口和反向端口（默认 2222）；授权项询问第 2 步打印的服务器侧公钥——可以先做第 2 步再粘贴，也可以先跳过该项之后重跑。脚本本身不会发起任何 SSH 连接，公钥交换全部通过复制粘贴完成。”
- Line 39: “它会打印一个公钥” → “它的第一个菜单项会打印一个公钥”；“它还会询问是否安装” → “另有独立菜单项负责安装”。
- Line 49: “重跑一遍脚本粘贴” → “重跑对应菜单项粘贴”。
- Line 53: “（保持运行；两侧 bootstrap 都提供开机自启选项）” → “（保持运行）”。
- Lines 69-72 → “- 停止隧道：在运行 `ssh -N remote-claude` 的终端里 `Ctrl-C`。”
- [ ] **Step 4: TROUBLESHOOTING.md and TROUBLESHOOTING.zh-CN.md**

In both files:
- Section 3: delete the autostart-reconnect bullet (en line 69 / zh line 69); keep the manual `while true; do ssh -N remote-claude; sleep 15; done` bullet, and change the last bullet's "wait for the auto-reconnect (up to ~90s + 15s)" to "restart the tunnel" (zh: “等待自动重连” → “重新启动隧道”).
- Delete section "## 5. Autostart issues" entirely (en lines 108-136 / zh 108-136) and renumber sections 6 and 7 to 5 and 6.
- [ ] **Step 5: Verify no references remain**

Run: `grep -rn -i 'autostart\|LaunchAgent\|Scheduled Task\|dev-tunnel\|remote-claude.service\|keepalive\|linger\|自启' --include='*.md' --include='*.sh' --include='*.ps1' . | grep -v docs/superpowers | grep -v 'launchctl print system\|launchctl kickstart'`
Expected: only `server/CLAUDE.md:104` ("or check its autostart") remains — fix it too: "(or check its autostart)" → "" in `server/CLAUDE.md` AND in the embedded copy inside `server/setup-server.sh` (the `install_claude_md` heredoc), keeping the two copies identical: `diff <(sed -n '/^# my-device/,/may have changed\.$/p' server/CLAUDE.md) <(sed -n '/^# my-device/,/may have changed\.$/p' server/setup-server.sh)` → empty.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "Remove the tunnel-autostart feature and scrub docs"
```

---

### Task 6: Final verification

- [ ] **Step 1: Syntax checks on everything**

```bash
bash -n local/bootstrap.sh && bash -n local/bootstrap-linux.sh && bash -n local/bootstrap-macos.sh && bash -n server/setup-server.sh
```

Expected: exit 0, no output.

- [ ] **Step 2: Full sandboxed walk-through of both bash entry points**

Re-run the Task 1 Step 6 and Task 3 Step 5 smoke tests from clean temp HOMEs; additionally drive `setup-server.sh` item 2 with a real generated pubkey and confirm `status_authorize` flips to `[done]` on the redrawn menu, and drive item 3 (`3\n2222\ntestuser\nq\n`) and confirm the managed block lands in `$T/.ssh/config`.

- [ ] **Step 3: Repo-wide leftover scan**

```bash
grep -rn -i 'systemd user service\|LaunchAgent\|ScheduledTask\|Scheduled Task\|autostart' --include='*.sh' --include='*.ps1' --include='*.md' . | grep -v docs/superpowers
```

Expected: no output.

- [ ] **Step 4: Commit any stragglers**

Only if steps 2-3 forced fixes: `git add -A && git commit -m "Fix issues found in final verification"`.
