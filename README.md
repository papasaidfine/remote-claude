# reverse-ssh-bootstrap

English | [中文](README.zh-CN.md)

Use Claude / Codex on a remote server to work on the code on **your own
machine** (Windows / macOS / Linux). Your normal SSH connection to the server
carries a reverse tunnel; the agent uses it to `ssh my-device` back into your
machine and does all project work there.

```
you    ── VSCode Remote-SSH / ssh remote-claude ──▶  server
agent  ── ssh my-device ──▶  your machine    (through the reverse tunnel)
```

You don't run anything special to keep the tunnel up: connect to the server the
way you always do and it comes with you. Only one connection can hold it at a
time.

## 1. Set up your local machine

**macOS / Linux** (only the sshd item needs sudo):

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

Work through the menu top to bottom. When asked for the "server-side public
key", paste the key printed in step 2 (or skip that item and re-run it later).

## 2. Set up the server

On the remote server (no sudo needed):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

- Its first item prints the server's public key → paste it into the local
  bootstrap when asked for the "server-side public key".
- Another item asks for **your local machine's key** (the local menu's "show
  key" item prints it) → that authorizes the tunnel login.
- The remaining items install the `~/.claude/CLAUDE.md` instructions and the
  facts file that make `claude` work on your machine.
- Use the same reverse port on both sides (default 2222).

## 3. Use it

Connect to the server as usual — **VSCode Remote-SSH** (host `remote-claude`)
or a plain `ssh remote-claude` in a terminal — and start `claude`. Tell it
which local project to work on ("work on `~/projects/foo`").

Quick test, on the server:

```bash
ssh my-device 'echo ok'                # should print ok
```

If the connection fails because the reverse port is taken, close the previous
`remote-claude` session first — only one connection can hold the tunnel.

## Optional: route the tunnel through an xray (VLESS) proxy

On a poor / censored network, the macOS and Windows bootstraps have two extra
items. **6) xray client** downloads xray (or, if it's already installed,
version-checks it against the latest release and updates it) and creates the
node list (`~/.config/remote-claude/vless-nodes.txt` on macOS,
`%LOCALAPPDATA%\remote-claude\vless-nodes.txt` on Windows) — put your `vless://`
URLs there, one per line, `#` comments, a random node per xray start; edits
take effect on the next connect. **7)** toggles routing the tunnel through the
proxy. On Windows each connection runs its own xray and it dies with the
connection; on macOS one on-demand xray is shared — `pkill xray` and reconnect
to switch node.

## Uninstall

Everything the scripts change is backed up first (`*.claude-bak-<timestamp>`)
and marked with `claude` in the file or block name. Point your AI assistant at
this repo and ask it to walk you through a full rollback.

## Something not working?

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — organized by symptom, hop by hop.
