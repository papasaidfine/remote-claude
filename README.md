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
per host you can turn on a reverse tunnel, route through a proxy, set up the
server side over the connection, and see Claude token usage & cost. One server
can serve several of your devices — each device gets a name, and the agent
reaches whichever one you're connected from.

## Get it

Download from the
[latest release](https://github.com/papasaidfine/remote-claude/releases).

**Desktop app** (the usual choice):

- Windows — `remote-claude-gui_windows_amd64.exe`
- macOS — `remote-claude-gui_darwin_arm64.dmg`
- Linux — `remote-claude-gui_linux_amd64`

The binaries are unsigned; on Windows, click through the SmartScreen prompt on
first run. After that, **Settings → Check for updates** installs new versions in
place, so this is the only manual download.

**Terminal app**, for a headless box with no desktop — `remote-claude_<os>_<arch>`
from the same page. Then:

```bash
chmod +x remote-claude
./remote-claude          # terminal UI
./remote-claude serve    # hold the tunnels up, no UI (for systemd / nohup)
```

Only one of the two can run on a machine at a time — launching a second one
brings the running app forward instead of starting a rival.

## Use

Open the app. On the **Hosts** tab:

1. **Name this machine** (e.g. `lc-pc`) — the agent reaches you back by this
   name. It's locked; click **Edit** to change it.
2. **Add host** (or use one already in `~/.ssh/config`) — its address, SSH user,
   and port.
3. **Enable reverse tunnel**, then **Edit** the host to set its port — and, if
   you need it, **Use proxy** and **Start tunnel when app opens**.
4. **Start** — the tunnel comes up and stays reconnected.
5. **Set up server** — configures the server side over the connection using key
   auth only (never a password). If the server hasn't authorized this machine's
   key yet, it shows the public key for you to add to the server's
   `authorized_keys`; add it, then run **Set up server** again.
6. **Usage** — Claude token usage & Anthropic-priced cost per host, by model,
   over the past 1 / 7 / 30 days.

On the **Settings** tab:

- **General** — UI language (English / 中文), and start this app when you log in.
- **Proxy** (optional, for restricted or slow networks) — install the proxy
  components, then add your `vless://` nodes, one per line. Turn it on per host
  under that host's **Edit**.
- **Local ssh server** — install/ensure it so the agent can reach back in (may
  ask for `sudo` / Administrator).
- **SSH key** — show this machine's public key, to authorize it on a server
  ahead of time.
- **About** — version, and **Check for updates**. If the direct download stalls
  it retries through the proxy automatically.

Closing the window hides to the tray; quit from the tray menu.

In the terminal app the same things live behind `1` (hosts) and `2` (settings);
the key bar along the bottom lists what each key does.

Then connect to the server as usual (VSCode Remote-SSH or `ssh <host>`) and start
Claude. On the server the agent works on your machine through
`ssh "$LC_CLIENT_NAME"`.
