# remote-claude

English | [中文](README.zh-CN.md)

Run Claude on a remote server and have it work on the code on **your own
machine** (Windows / macOS / Linux). A small tray app on your machine holds a
reverse SSH tunnel open; the agent uses it to `ssh` back into your machine and
does all project work there. Open as many normal SSH / VSCode Remote-SSH
sessions to the server as you like — the tunnel is held separately, so nothing
blocks.

```
you    ── VSCode Remote-SSH / ssh <host> ──▶  server   (any number of sessions)
agent  ── ssh "$LC_CLIENT_NAME" ──▶  your machine       (reverse tunnel, held by the app)
```

The app is a friendly view over your `~/.ssh/config`: it lists your hosts, and
per host you can turn on a reverse tunnel, route through xray, set up the server
side over the connection, and see Claude token usage & cost. One server can
serve several of your devices — each device gets a name, and the agent reaches
whichever one you're connected from.

## Get it

Download the desktop app for your OS from the
[latest release](https://github.com/papasaidfine/remote-claude/releases):

- Windows — `remote-claude-gui_windows_amd64.exe`
- macOS — `remote-claude-gui_darwin_arm64`
- Linux — `remote-claude-gui_linux_amd64`

(The binaries are unsigned; on Windows, click through the SmartScreen prompt on
first run.)

Prefer a terminal / headless box? The CLI serves the same UI as a local web
page:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)   # macOS / Linux
```
```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1 | iex           # Windows
```

Then `remote-claude serve` opens the UI in your browser. GitHub blocked or slow?
Prefix the download with your own proxy via `RC_PROXY`
(e.g. `export RC_PROXY=http://127.0.0.1:7890`).

## Use

Open the app, then:

1. **Name this machine** (e.g. `lc-pc`) — the agent reaches you back by this
   name. It's locked; click **Edit** to change it.
2. **Install / ensure the local ssh server** — so the agent can reach back in
   (may ask for `sudo` / Administrator).
3. **Add your host** (or use one already in `~/.ssh/config`) — its address, SSH
   user, and port.
4. **Enable the reverse tunnel** and set its port, then **Start** — it comes up
   and stays reconnected.
5. **Set up server** — configures the server side over the connection using key
   auth only (never a password). If the server hasn't authorized this machine's
   key yet, it shows the public key for you to add to the server's
   `authorized_keys`; add it, then run **Set up server** again.
6. **Xray** (optional, for censored/slow networks) — download the binary and add
   your `vless://` nodes, then turn on xray per host.
7. **Usage** — Claude token usage & Anthropic-priced cost per host, by model,
   over the past 1 / 7 / 30 days.

Turn on **Start this app when I log in** to keep tunnels up automatically.
Closing the window hides to the tray; quit from the tray menu. Pick the UI
language (English / 中文) from the selector at the top. **Check for updates**
installs the latest release in place — if the direct download stalls it retries
through xray automatically.

Then connect to the server as usual (VSCode Remote-SSH or `ssh <host>`) and start
Claude. On the server the agent works on your machine through
`ssh "$LC_CLIENT_NAME"`.
