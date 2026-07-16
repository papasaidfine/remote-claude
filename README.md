# reverse-ssh-bootstrap

English | [中文](README.zh-CN.md)

Let Claude / Codex on a remote server SSH back into your local machine (Windows / macOS / Linux) through a reverse tunnel, and work in your local project directory. The tunnel stays on `127.0.0.1` at both ends — the reverse port is never exposed to the LAN or internet, and the server-side key only works through it.

```
you                      : ssh -N remote-claude        (keep it running)
agent on the server      : ssh my-device                (lands on your machine)
```

## 1. Set up your local machine

**macOS / Linux** — run it and pick items from the menu; each item is independent, idempotent, and shows whether it is already configured (only the sshd item needs sudo):

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

The menu items ask only for what they need: the tunnel-config item asks for your server's address/user/port and a reverse port (default 2222); the authorize item asks for the server-side public key from step 2 — run step 2 first and paste it, or skip that item and re-run it later. The scripts never SSH anywhere themselves; key exchange is copy-paste.

### Optional: route the tunnel through an xray (VLESS) proxy

On a poor / censored network, the macOS bootstrap has an extra menu item
(**6) xray client**) that installs xray, seeds
`~/.config/remote-claude/vless-nodes.txt` with your `vless://` URL, and runs a
local SOCKS proxy. List several nodes in that file (one URL per line, `#`
comments) and every xray start picks one at random — swap or add nodes by
editing the file, no re-run needed. Then menu item 4 offers to route the `remote-claude` tunnel
through it, so VSCode Remote-SSH and `ssh remote-claude -t "claude"`
automatically tunnel SSH through xray — xray is started on demand at connect
time, with no background service.

Added xray after the tunnel config was already written? Menu item **7** toggles
the proxy on/off in place — it reuses the server details stored in the config
block, so nothing needs to be retyped.

On Windows, `bootstrap-windows.ps1` has the same items 6 and 7. The model is
different from macOS: instead of one shared on-demand xray, **every ssh
connection starts its own xray and the kernel kills it the moment that
connection closes** (kill-on-close Job Object) — nothing to start, nothing to
stop, no leftover processes. Per-connection logs live in `%TEMP%\rc-xray-*.log`.
Every connection independently picks a random node from
`%LOCALAPPDATA%\remote-claude\vless-nodes.txt`.

Stop the on-demand xray (macOS only; Windows cleans up by itself) — the next
connect starts it again with a freshly picked node:

```bash
pkill -f 'xray run -c .*remote-claude/xray-current.json'
```

## 2. Set up the server

On the remote server (no sudo needed):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

It presents the same kind of menu. Its first item prints a public key — paste it into the local bootstrap when asked for the "server-side public key". Use the same reverse port on both sides. Separate menu items install `~/.claude/CLAUDE.md` instructions so Claude Code does all project work through `ssh my-device` instead of touching this server's filesystem, and seed an agent-maintained facts file (`~/.config/remote-claude/facts.json` — your machine's OS, project paths and descriptions) that Claude reads at session start and keeps updated, so new sessions skip the rediscovery.

If you skipped that CLAUDE.md prompt (or just want the instructions without the rest), fetch them directly:

```bash
mkdir -p ~/.claude && curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/CLAUDE.md >> ~/.claude/CLAUDE.md
```

This appends, so an existing `~/.claude/CLAUDE.md` keeps its content — but running it twice duplicates the block, and the setup script's managed install won't deduplicate a copy added this way. Pick one method and stick with it.

Another menu item asks for the **local** machine's public key (the local bootstrap's "show key" item prints it; or `cat ~/.ssh/id_ed25519.pub` on your machine) and adds it to this server's `~/.ssh/authorized_keys` — that is what authorizes the tunnel login. Skipped it? Re-run that item and paste it, or use `ssh-copy-id`.

## 3. Start the tunnel and use it

On your local machine (keep it running):

```bash
ssh -N remote-claude
```

On the server, the normal workflow is: connect however you usually do (e.g. **VSCode Remote-SSH**) and just start `claude`. The `~/.claude/CLAUDE.md` installed in step 2 makes it do all project work on your machine through `ssh my-device` — simply tell it which local project to work on ("work on `~/projects/foo`").

Quick tunnel test:

```bash
ssh my-device 'echo ok'                # should print ok
```

## Stop / uninstall

- Stop the tunnel: `Ctrl-C` in the terminal running `ssh -N remote-claude`.
- Everything the scripts change is backed up first (`*.claude-bak-<timestamp>`) and marked with `claude` in the file/block name, so it's easy to find and remove. Ask your AI assistant to walk you through a full rollback, or just point it at the scripts in this repo.

## Something not working?

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — it's organized by symptom, hop by hop.
