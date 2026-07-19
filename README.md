# reverse-ssh-bootstrap

English | [中文](README.zh-CN.md)

Use Claude on a remote server to work on the code on **your own
machine** (Windows / macOS / Linux). Your normal SSH connection to the server
carries a reverse tunnel; the agent uses it to `ssh my-device` back into your
machine and does all project work there.

```
you    ── VSCode Remote-SSH / ssh remote-claude ──▶  server
agent  ── ssh my-device ──▶  your machine    (reverse ssh)
```

## 1. Set up your local machine

**macOS / Linux:**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)
```

**Windows:**

```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1 | iex
```

This downloads the `remote-claude` binary and opens the setup menu (re-run to
update). Only the **Incoming SSH** item needs an elevated session —
Administrator PowerShell on Windows, `sudo` on macOS/Linux.

Menu — three phases, work top to bottom (each item shows whether it's
already configured):

**① Local ──▶ Claude** — reach the server running Claude Code

1. SSH config shortcut (`Host remote-claude`)
2. Local SSH key — create & show the public key
3. Test connection — checks the outbound hop; once the reverse tunnel is
   configured it validates the whole loop too

**② Claude ──▶ Local** — let the agent ssh back into this machine

4. Incoming SSH (sshd)
5. Authorize the server's connect-back key
6. Reverse tunnel port

**③ xray ═[ ssh ]═▶** — optional, for bad networks; asks for a download
proxy first (Enter = direct)

7. xray proxy client
8. Route the tunnel through xray

Linux has no xray, so its menu is phases ①–② (items 1–6).

## 2. Set up the server

On the remote server (no sudo needed):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

Menu:

1. Connect-back key (create + show its public key)
2. Authorize your local machine's key (the tunnel login)
3. `my-device` ssh alias
4. Agent instructions (`~/.claude/CLAUDE.md`)
5. Agent facts file (`facts.json`)
