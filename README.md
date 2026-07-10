# reverse-ssh-bootstrap

English | [中文](README.zh-CN.md)

Let Claude / Codex on a remote server SSH back into your local machine (Windows / macOS / Linux) through a reverse tunnel, and work in your local project directory. Everything stays on `127.0.0.1` — nothing is exposed to the LAN or internet.

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

It will ask for your server's address/user/port, a reverse port (default 2222), and the server-side public key from step 2 — you can also run step 2 first and paste, or leave it empty and re-run later.

## 2. Set up the server

On the remote server (no sudo needed):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

It prints a public key — paste it into the local bootstrap when asked for the "server-side public key". Use the same reverse port on both sides.

Also make sure the **local** public key the bootstrap printed is in the server's `~/.ssh/authorized_keys` — the local bootstrap offers to upload it for you.

## 3. Start the tunnel and use it

On your local machine (keep it running; both bootstraps also offer autostart):

```bash
ssh -N remote-claude
```

On the server:

```bash
ssh my-device 'echo ok'      # test: should print ok
claude-local                    # interactive shell on your machine
claude-local git status         # run one command on your machine

# let Claude Code run all its shell commands on your machine,
# inside the project directory you configured:
SHELL=~/.local/bin/claude-local-shell claude
```

## Stop / uninstall

- Stop the tunnel: `Ctrl-C`, or if autostart was enabled:
  - macOS: `launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.claude.dev-tunnel.plist`
  - Linux: `systemctl --user disable --now remote-claude.service`
  - Windows: `Stop-ScheduledTask -TaskName ClaudeDevTunnel`
- Everything the scripts change is backed up first (`*.claude-bak-<timestamp>`) and marked with `claude` in the file/block name, so it's easy to find and remove. Ask your AI assistant to walk you through a full rollback, or just point it at the scripts in this repo.

## Something not working?

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — it's organized by symptom, hop by hop.
