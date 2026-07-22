# remote-claude

English | [中文](README.zh-CN.md)

Run Claude on a remote server and have it work on the code on **your own
machine** (Windows / macOS / Linux). A small app on your machine holds a reverse
SSH tunnel open; the agent uses it to `ssh` back into your machine and does all
project work there. Open as many normal SSH / VSCode Remote-SSH sessions to the
server as you like — the tunnel is held separately, so nothing blocks.

```
you    ── VSCode Remote-SSH / ssh remote-claude ──▶  server   (any number of sessions)
agent  ── ssh "$LC_CLIENT_NAME" ──▶  your machine            (reverse tunnel, held by the app)
```

One server can serve several of your devices — each device gets a name, and the
agent reaches whichever one you're connected from.

## Install

**macOS / Linux**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)
```

**Windows**

```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1 | iex
```

GitHub blocked or slow? Route the download through your own proxy with
`RC_PROXY`:

```bash
export RC_PROXY=http://127.0.0.1:7890
bash <(curl -fsSL --proxy "$RC_PROXY" https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)
```

```powershell
$env:RC_PROXY = 'http://127.0.0.1:7890'
(irm -Proxy $env:RC_PROXY https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1) | iex
```

## Use

Run `remote-claude`. It opens the app in your browser and keeps running in the
background to hold the tunnel up. In the app:

1. **Name this machine** (e.g. `lisa-laptop`) — the agent reaches you back by
   this name.
2. **Add your host** — the server's address, SSH user, and reverse port. Turn on
   xray if your network needs it, and add your `vless://` nodes.
3. **Install / ensure the local ssh server** — so the agent can reach back in
   (may ask for `sudo` / Administrator).
4. **Start tunnel** — brings the reverse tunnel up and keeps it reconnected.
5. **Set up server** — configures the server side over the connection. The first
   time, enter your password *on the server* so it can authorize your key; after
   that it's automatic.

Then connect to the server as usual (VSCode Remote-SSH or `ssh remote-claude`)
and start Claude. On the server the agent works on your machine through
`ssh "$LC_CLIENT_NAME"`.

On a headless box, `remote-claude serve` runs the app without opening a browser.
