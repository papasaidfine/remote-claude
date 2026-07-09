# reverse-ssh-bootstrap

English | [中文](README.zh-CN.md)

Let Claude / Codex running on a remote server SSH back into your local machine (Windows / macOS) through a **reverse SSH tunnel** and develop inside your local project directory.

This repo provides a locally-run bootstrap CLI tool that automates: local sshd installation and hardening, SSH key generation, `~/.ssh/config` writing, `authorized_keys` writing, and optional start-at-login for the tunnel.

## Architecture

```
┌──────────────┐   ① ssh -N claude-dev-tunnel    ┌──────────────┐
│  local PC    │ ───────────────────────────────▶ │ remote server│
│ (Win / mac)  │                                  │              │
│              │   ② RemoteForward reverse tunnel │              │
│ 127.0.0.1:22 │ ◀─────────────────────────────── │ 127.0.0.1:   │
│ (local sshd) │                                  │ <reverse_port>│
└──────────────┘                                  └──────┬───────┘
                                                         │ ③ Claude / Codex:
                                                         │ ssh -p <reverse_port>
                                                         │     <local_user>@127.0.0.1
```

1. The local machine SSHes to the remote server with a `RemoteForward`;
2. `127.0.0.1:<reverse_port>` on the server is forwarded to `127.0.0.1:22` on the local machine;
3. Claude / Codex on the server connects back with `ssh -p <reverse_port> <local_user>@127.0.0.1`.

Because both ends bind `127.0.0.1` only, neither side of the tunnel is exposed to the LAN or the internet.

## What RemoteForward means

This line in `~/.ssh/config`:

```
RemoteForward 127.0.0.1:<reverse_port> 127.0.0.1:22
```

means: listen on `127.0.0.1:<reverse_port>` **on the remote server**; any connection to that port is forwarded over this SSH connection to `127.0.0.1:22` on the **local machine** (the local sshd).

- The first address is the server-side listen address. It **must** be `127.0.0.1:<port>`, otherwise the reverse port could be exposed publicly depending on the server's `GatewayPorts` setting;
- The second address is the local-side target, pointing at the local sshd.

The reverse channel stays available for as long as the `ssh -N claude-dev-tunnel` connection is alive.

## Why two SSH key pairs

Different directions, different trust relationships — so two unrelated keys:

| Key | Lives on | Purpose |
|---|---|---|
| `~/.ssh/claude_tunnel_ed25519` | **local machine** | local → server, establishes the tunnel. Its `.pub` goes into the **server's** `~/.ssh/authorized_keys` |
| `~/.ssh/claude_to_local_ed25519` (name is up to you) | **remote server** | Claude / Codex on the server → local machine (through the tunnel). Its `.pub` goes into the **local machine's** `~/.ssh/authorized_keys` (the bootstrap tool writes it for you) |

Do not reuse one key for both: a leaked local key should not mean "anyone can log into your computer", and vice versa. Private keys never leave the machine they were created on.

## Quick start

### macOS

```bash
chmod +x bootstrap-macos.sh
./bootstrap-macos.sh
```

The script interactively asks for the server address, user, port, reverse port, the server-side public key, etc., then performs all local configuration (system-level changes require sudo).

### Windows

Open PowerShell **as Administrator**:

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
.\bootstrap-windows.ps1
```

Parameters are also supported (skips the corresponding prompts):

```powershell
.\bootstrap-windows.ps1 -ServerHost 1.2.3.4 -ServerUser dev -ServerPort 22 -ReversePort 2222
```

### Server-side preparation

1. Generate the connect-back key for Claude / Codex on the server (if you don't have one yet):

   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/claude_to_local_ed25519 -N "" -C "claude-to-local"
   cat ~/.ssh/claude_to_local_ed25519.pub   # paste this line into the bootstrap tool
   ```

2. Add the **local tunnel public key** printed by the bootstrap tool (`claude_tunnel_ed25519.pub`) to the server's `~/.ssh/authorized_keys` (the tool also offers an ssh-copy-id-style automatic upload).

### Start the tunnel

On the local machine:

```bash
ssh -N claude-dev-tunnel
```

Keep that connection alive, then on the server (run by Claude / Codex):

```bash
ssh -i ~/.ssh/claude_to_local_ed25519 -p <reverse_port> <local_user>@127.0.0.1
```

and you're back in a shell / project directory on the local machine.

## The Windows administrators_authorized_keys pitfall

The default Windows OpenSSH Server `sshd_config` ends with:

```
Match Group administrators
       AuthorizedKeysFile __PROGRAMDATA__/ssh/administrators_authorized_keys
```

Effect: **whenever the current user belongs to the Administrators group**, sshd ignores `%USERPROFILE%\.ssh\authorized_keys` and only consults the global `%ProgramData%\ssh\administrators_authorized_keys`. A lot of "I added the key but still get Permission denied" cases come from exactly this.

How the bootstrap tool handles it: it comments out those two lines with a `# claude-bootstrap disabled:` prefix, so administrator users fall back to the standard `.ssh/authorized_keys` path. The `sshd_config` is backed up before the change, validated with `sshd -t` afterwards, and only then is the service restarted; re-runs are idempotent (already-commented lines are not processed again).

## Security hardening applied by the tool

- The server-side reverse port only listens on `127.0.0.1` (`RemoteForward 127.0.0.1:<port> ...`);
- The local sshd listens on `127.0.0.1` only by default (optional, can be turned off interactively);
- Password login is disabled by default, public key only (optional);
- The server-side key written into `authorized_keys` carries a restriction prefix:

  ```
  from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding ssh-ed25519 AAAA...
  ```

  i.e. this key can **only** log in from local loopback through the reverse tunnel — holding the private key on the LAN / internet is not enough to log in directly;
- No agent forwarding (`ForwardAgent no`);
- Every modified system file is backed up first (`*.claude-bak-<timestamp>`);
- Private keys are never printed to the terminal or written to logs.

## Manual testing

Verify hop by hop so problems can be localized quickly:

```bash
# 1. local -> server (the tunnel hop) works
ssh -i ~/.ssh/claude_tunnel_ed25519 -p <server_port> <server_user>@<server_host> 'echo ok'

# 2. the local sshd works (loopback self-test on the local machine)
ssh -p 22 <local_user>@127.0.0.1 'echo ok'

# 3. establish the tunnel (-v shows whether the RemoteForward succeeded)
ssh -v -N claude-dev-tunnel

# 4. test the connect-back from the server
ssh -i ~/.ssh/claude_to_local_ed25519 -p <reverse_port> <local_user>@127.0.0.1 'echo ok'
```

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for more.

## Stopping the tunnel

- Foreground run: just `Ctrl-C`;
- macOS LaunchAgent:

  ```bash
  launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.claude.dev-tunnel.plist
  ```

- Windows Scheduled Task:

  ```powershell
  Stop-ScheduledTask -TaskName ClaudeDevTunnel
  Get-Process ssh -ErrorAction SilentlyContinue | Stop-Process   # kill leftover ssh processes
  ```

## Removal / rollback

Every change can be undone individually:

### Common (both platforms)

1. **`~/.ssh/config`**: delete the whole block between the `# >>> claude-dev-tunnel ... # <<< claude-dev-tunnel <<<` markers; or restore the backup `~/.ssh/config.claude-bak-<timestamp>`.
2. **`~/.ssh/authorized_keys`**: delete the line starting with `from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding` that carries the server-side key.
3. **Local tunnel key**: delete `~/.ssh/claude_tunnel_ed25519{,.pub}` and remove the corresponding public key from the server's `~/.ssh/authorized_keys`.

### macOS

```bash
# restore the sshd config (with the drop-in approach, deleting the file is enough)
sudo rm -f /etc/ssh/sshd_config.d/100-claude-dev-tunnel.conf
# older systems (where the main config was edited directly):
#   sudo cp /etc/ssh/sshd_config.claude-bak-<timestamp> /etc/ssh/sshd_config
sudo /usr/sbin/sshd -t && sudo launchctl kickstart -k system/com.openssh.sshd

# turn Remote Login off (if it was off before)
sudo systemsetup -setremotelogin off

# remove the autostart
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.claude.dev-tunnel.plist
rm -f ~/Library/LaunchAgents/com.claude.dev-tunnel.plist
```

### Windows (elevated PowerShell)

```powershell
# restore the sshd config
Copy-Item "$env:ProgramData\ssh\sshd_config.claude-bak-<timestamp>" "$env:ProgramData\ssh\sshd_config" -Force
Restart-Service sshd

# to fully disable / uninstall sshd
Stop-Service sshd
Set-Service sshd -StartupType Disabled
# Remove-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0

# remove the autostart
Unregister-ScheduledTask -TaskName ClaudeDevTunnel -Confirm:$false
Remove-Item "$env:USERPROFILE\.ssh\claude-dev-tunnel-keepalive.ps1" -ErrorAction SilentlyContinue
```

## Repository layout

```
bootstrap-macos.sh                      macOS bootstrap script (bash)
bootstrap-windows.ps1                   Windows bootstrap script (PowerShell)
README.md / README.zh-CN.md             this document (English / Chinese)
TROUBLESHOOTING.md /
  TROUBLESHOOTING.zh-CN.md              troubleshooting guide (English / Chinese)
examples/
  ssh_config.example                    Host block written into ~/.ssh/config
  authorized_keys.example               authorized_keys line with the from= restriction
  sshd_config.macos.conf                macOS sshd drop-in config example
  sshd_config.windows.snippet           Windows sshd_config changes example
  com.claude.dev-tunnel.plist.example   macOS LaunchAgent example
```
