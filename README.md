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

**macOS:**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-macos.sh)
```

**Linux:**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-linux.sh)
```

**Windows** (run as administrator):

```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-windows.ps1 -OutFile bootstrap-windows.ps1
Set-ExecutionPolicy -Scope Process Bypass -Force
.\bootstrap-windows.ps1
```

Menu (each item shows whether it's already configured; work top to bottom):

1. Incoming SSH (sshd)
2. Local SSH key
3. Authorize the server's connect-back key
4. SSH config shortcut (`Host remote-claude`)
5. Reverse tunnel port
6. xray proxy client — optional, for bad networks
7. Route the tunnel through xray
8. Show the local public key

Linux has no xray, so its menu is 1–5 followed by "Show the local public key".

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
