# reverse-ssh-bootstrap

English | [中文](README.zh-CN.md)

Let Claude / Codex on a remote server SSH back into your local machine (Windows / macOS / Linux) through a reverse tunnel, and work in your local project directory. The tunnel stays on `127.0.0.1` at both ends — the reverse port is never exposed to the LAN or internet, and the server-side key only works through it.

```
you                      : ssh -N remote-claude        (keep it running)
agent on the server      : ssh my-device                (lands on your machine)
```

## 1. Set up your local machine

**macOS / Linux** — run and answer the prompts (sudo needed for sshd setup):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-macos.sh)   # macOS
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-linux.sh)   # Linux
```

**Windows** — in an **Administrator** PowerShell:

```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-windows.ps1 -OutFile bootstrap-windows.ps1
Set-ExecutionPolicy -Scope Process Bypass -Force
.\bootstrap-windows.ps1
```

It will ask for your server's address/user/port, a reverse port (default 2222), and the server-side public key from step 2 — run step 2 first and paste it, or leave it empty and re-run later. The scripts never SSH anywhere themselves; key exchange is copy-paste.

## 2. Set up the server

On the remote server (no sudo needed):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

It prints a public key — paste it into the local bootstrap when asked for the "server-side public key". Use the same reverse port on both sides. It also offers to install `~/.claude/CLAUDE.md` instructions so Claude Code does all project work through `ssh my-device` instead of touching this server's filesystem, and seeds an agent-maintained "my-device facts" memory in the same file (your machine's OS, project paths, mount points) so new sessions skip the rediscovery.

If you skipped that CLAUDE.md prompt (or just want the instructions without the rest), fetch them directly:

```bash
mkdir -p ~/.claude && curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/CLAUDE.md >> ~/.claude/CLAUDE.md
```

This appends, so an existing `~/.claude/CLAUDE.md` keeps its content — but running it twice duplicates the block, and the setup script's managed install won't deduplicate a copy added this way. Pick one method and stick with it.

It also asks for the **local** machine's public key (the local bootstrap prints it at the end; or `cat ~/.ssh/id_ed25519.pub` on your machine) and adds it to this server's `~/.ssh/authorized_keys` — that is what authorizes the tunnel login. Left it empty? Re-run the script and paste it, or use `ssh-copy-id`.

## 3. Start the tunnel and use it

On your local machine (keep it running; both bootstraps also offer autostart):

```bash
ssh -N remote-claude
```

On the server, the normal workflow is: connect however you usually do (e.g. **VSCode Remote-SSH**) and just start `claude`. The `~/.claude/CLAUDE.md` installed in step 2 makes it do all project work on your machine through `ssh my-device` — simply tell it which local project to work on ("work on `~/projects/foo`").

Quick tunnel test:

```bash
ssh my-device 'echo ok'                # should print ok
```

## Stop / uninstall

- Stop the tunnel: `Ctrl-C`, or if autostart was enabled:
  - macOS: `launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.claude.dev-tunnel.plist`
  - Linux: `systemctl --user disable --now remote-claude.service`
  - Windows: `Stop-ScheduledTask -TaskName ClaudeDevTunnel`
- Everything the scripts change is backed up first (`*.claude-bak-<timestamp>`) and marked with `claude` in the file/block name, so it's easy to find and remove. Ask your AI assistant to walk you through a full rollback, or just point it at the scripts in this repo.

## Something not working?

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — it's organized by symptom, hop by hop.
