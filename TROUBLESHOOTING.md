# Troubleshooting

English | [中文](TROUBLESHOOTING.zh-CN.md)

Debug layer by layer along the data path: **local → server (tunnel hop) → reverse port on the server → local sshd**.
The "Manual testing" section of `README.md` gives an independent test command for each hop — run those four steps first to find out which layer is broken.

## 1. The tunnel won't come up (`ssh -N remote-claude` fails)

### `Permission denied (publickey)` (connecting to the server)

- The local tunnel key's public key was not added to the server's `~/.ssh/authorized_keys`;
- Permissions on that file/directory on the server are too loose: `chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys`;
- Confirm the tunnel key is actually used: `ssh -v remote-claude` and look for `Offering public key: ~/.ssh/id_ed25519`.

### `remote port forwarding failed for listen port <reverse_port>`

The port is already taken on the server — most commonly a previous tunnel died but the server-side sshd hasn't reclaimed the port yet, or another process holds it:

```bash
# on the server
ss -tlnp | grep <reverse_port>
```

Fixes:
- Wait 1–2 minutes for the server to reclaim it, or kill the stale sshd session process on the server;
- Use a different reverse port (re-run the bootstrap tool with the new port; the config block gets updated);
- Consider enabling `ClientAliveInterval 30` in the **server's** sshd_config to reclaim dead connections faster.

Because `ExitOnForwardFailure yes` is configured, ssh exits immediately when the forward fails instead of silently degrading — that is intentional.

## 2. Tunnel is up, but the connect-back from the server fails

### `Connection refused` (`ssh -p <reverse_port> ... 127.0.0.1`)

- The tunnel isn't actually up: check on the local machine that the `ssh -N remote-claude` process is alive;
- The local sshd isn't running:
  - macOS: `sudo launchctl print system/com.openssh.sshd`; check System Settings → Sharing → Remote Login;
  - Linux: `systemctl status sshd` (Debian/Ubuntu: `ssh`), then `sudo systemctl start sshd` if needed;
  - Windows: `Get-Service sshd`, then `Start-Service sshd` if needed;
- The local sshd isn't listening on 127.0.0.1: `netstat -an | grep :22` (mac) / `netstat -an | findstr :22` (Win).

### `Permission denied (publickey)` (connecting back to the local machine)

The most common class of problems — check each item:

1. **Windows administrator users**: confirm the `Match Group administrators` block in `%ProgramData%\ssh\sshd_config` is commented out (the bootstrap tool does this; if you installed sshd manually before, re-run the tool). Otherwise sshd only consults `administrators_authorized_keys` and ignores the key in your user profile.
2. **authorized_keys permissions / ACLs**:
   - macOS: `chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys`;
   - Windows: sshd rejects the file when its ACL is too loose. Re-run the bootstrap tool (it tightens the ACL with icacls), or manually:

     ```powershell
     icacls $env:USERPROFILE\.ssh\authorized_keys /inheritance:r /grant "*S-1-5-18:(F)" /grant "*S-1-5-32-544:(F)" /grant "$env:USERNAME:(F)"
     ```
3. **The `from=` restriction**: the authorized_keys line carries `from="127.0.0.1,::1"`. The connect-back **must** go through the tunnel (target `127.0.0.1`). If you test the key directly from the LAN, it is rejected — that is expected behavior, not a bug.
4. **Key mismatch**: the private key `ssh -i` points at on the server must be the pair of the public key written into the local authorized_keys. Compare fingerprints with `ssh-keygen -lf` on both sides.
5. **Read the local sshd log**:
   - macOS: `log stream --predicate 'process == "sshd"' --info` (watch while connecting);
   - Linux: `sudo journalctl -u sshd -f` (Debian/Ubuntu: `-u ssh`);
   - Windows: `Get-WinEvent -LogName OpenSSH/Operational -MaxEvents 30 | Format-List TimeCreated,Message`.

### Username / host in the connect-back command

The user in the connect-back command is the **local machine's username** (the "Local username" from the bootstrap prompts), not the server user. Windows domain accounts may need the `DOMAIN\user` or `user@domain` form.

## 3. The tunnel keeps dropping

- `ServerAliveInterval 30` / `ServerAliveCountMax 3` are configured (a dead connection is detected and exits within ~90 seconds);
- To reconnect automatically, wrap the command in a loop: `while true; do ssh -N remote-claude; sleep 15; done`;
- Laptop sleep kills the TCP connection; after waking, restart the tunnel.

## 4. sshd configuration issues

### `sshd -t` validation fails

The bootstrap tool automatically rolls back to the backup when validation fails. If you edited the config by hand:

```bash
# macOS
sudo /usr/sbin/sshd -t          # prints the exact error and line number
# Windows (elevated)
& "$env:SystemRoot\System32\OpenSSH\sshd.exe" -t
```

Backups live next to the original file, named `*.claude-bak-<timestamp>`.

### macOS: `systemsetup -setremotelogin on` fails

Recent macOS versions restrict `systemsetup` via TCC (the terminal needs Full Disk Access). Workaround: enable it manually via System Settings → General → Sharing → Remote Login, then re-run the script (all the other steps are unaffected).

### macOS: sshd_config changes don't take effect

macOS spawns sshd on demand via launchd, so a new config applies to **new connections**. To force a reload:

```bash
sudo launchctl kickstart -k system/com.openssh.sshd
```

### Windows: `Add-WindowsCapability` fails (0x800f0954 etc.)

Usually WSUS / group policy blocks Features-on-Demand downloads. Ask an admin to allow "Specify settings for optional component installation", or install OpenSSH Server offline (Microsoft's Win32-OpenSSH releases: `https://github.com/PowerShell/openssh-portable/releases`).

### Windows: `Bad owner or permissions on .ssh/config`

The ssh client validates the config file ACL too. Re-run the bootstrap tool, or apply the same icacls command as for authorized_keys above to `%USERPROFILE%\.ssh\config`.

## 5. Server-side issues (ssh my-device)

### `ssh my-device` says `Host key verification failed` / `REMOTE HOST IDENTIFICATION HAS CHANGED`

The host key of whatever answers on `127.0.0.1:<reverse_port>` changed — usually because a *different* local machine now holds the tunnel (or the local machine was reinstalled). If that change is expected:

```bash
rm ~/.ssh/known_hosts.my-device     # the alias uses a dedicated known-hosts file
ssh my-device 'echo ok'             # accept-new stores the new key
```

If you did NOT expect the machine behind the tunnel to change, stop and check what is actually listening on the reverse port first.

## 6. xray proxy: connect hangs or fails after enabling item 4's proxy

The `ProxyCommand` starts xray on demand and waits for `127.0.0.1:10808`.
If SSH fails right after enabling the proxy:

- Check the log: `cat ~/.config/remote-claude/xray.log` — a bad `vless://`
  URL or an unreachable server shows up here.
- Confirm the SOCKS port came up: `nc -z 127.0.0.1 10808 && echo up`.
- Re-run bootstrap item 6 to regenerate `~/.config/remote-claude/xray.json`
  if you pasted the wrong URL.
- To bypass xray temporarily, re-run item 4 and answer **n** to the proxy
  question.

## 7. Everything works, but you want to verify the security posture

```bash
# on the server: the reverse port must listen on 127.0.0.1 only
ss -tln | grep <reverse_port>        # expect 127.0.0.1:<reverse_port>, never 0.0.0.0

# locally: the server-side key must only work through the tunnel — check that
# its authorized_keys line carries the from="127.0.0.1,::1" restriction
grep 'from="127.0.0.1' ~/.ssh/authorized_keys
```
