# 故障排查

[English](TROUBLESHOOTING.md) | 中文

按数据路径分层排查：**本地 → 服务器（隧道跳） → 服务器上的反向端口 → 本地 sshd**。
`README.zh-CN.md` 的"手动测试"一节给出了每一跳的独立测试命令，先跑那四步确定断在哪一层。

## 1. 隧道建不起来（`ssh -N remote-claude` 失败）

### `Permission denied (publickey)`（连服务器时）

- 本地隧道 key 的公钥没有加入服务器的 `~/.ssh/authorized_keys`；
- 服务器上该文件/目录权限过宽：`chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys`；
- 确认使用的是隧道 key：`ssh -v remote-claude` 观察 `Offering public key: ~/.ssh/id_ed25519`。

### `remote port forwarding failed for listen port <reverse_port>`

服务器上这个端口已被占用——最常见的原因是上一条隧道断了但服务器侧 sshd 还没回收端口，或者有别的进程占用：

```bash
# 在服务器上查看
ss -tlnp | grep <reverse_port>
```

处理：
- 等 1–2 分钟让服务器回收，或在服务器上 kill 掉旧的 sshd 会话进程；
- 换一个 reverse port（重新运行 bootstrap 工具改端口即可，配置块会被更新）；
- 建议在**服务器**的 sshd_config 中开启 `ClientAliveInterval 30`，加速死连接回收。

因为配置了 `ExitOnForwardFailure yes`，端口转发失败时 ssh 会直接退出而不是静默降级——这是有意的。

## 2. 隧道通了，但服务器上反连失败

### `Connection refused`（`ssh -p <reverse_port> ... 127.0.0.1`）

- 隧道其实没在：本地检查 `ssh -N remote-claude` 进程是否存活；
- 本地 sshd 没起来：
  - macOS：`sudo launchctl print system/com.openssh.sshd`；系统设置 → 共享 → 远程登录是否开启；
  - Linux：`systemctl status sshd`（Debian/Ubuntu 是 `ssh`），必要时 `sudo systemctl start sshd`；
  - Windows：`Get-Service sshd`，必要时 `Start-Service sshd`；
- 本地 sshd 没监听 127.0.0.1：`netstat -an | grep :22`（mac）/ `netstat -an | findstr :22`（Win）。

### `Permission denied (publickey)`（反连本地时）

最常见的一类问题，逐项检查：

1. **Windows 管理员用户**：确认 `%ProgramData%\ssh\sshd_config` 中 `Match Group administrators` 块已被注释（bootstrap 工具会处理；如果你手动装过 sshd，重跑一次工具）。否则 sshd 只认 `administrators_authorized_keys`，不看你用户目录下的 key。
2. **authorized_keys 权限 / ACL**：
   - macOS：`chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys`；
   - Windows：文件 ACL 过宽会被 sshd 拒绝。重跑 bootstrap 工具（它会用 icacls 收紧 ACL），或手动：

     ```powershell
     icacls $env:USERPROFILE\.ssh\authorized_keys /inheritance:r /grant "*S-1-5-18:(F)" /grant "*S-1-5-32-544:(F)" /grant "$env:USERNAME:(F)"
     ```
3. **`from=` 限制**：authorized_keys 里的行带 `from="127.0.0.1,::1"`。反连**必须**走隧道（目标是 `127.0.0.1`）。如果你从局域网直接测试这把 key，会被拒绝——这是预期行为，不是 bug。
4. **key 对不上**：服务器上 `ssh -i` 指向的私钥和写入本地 authorized_keys 的公钥要是同一对。`ssh-keygen -lf` 对比两边指纹。
5. **看本地 sshd 日志**：
   - macOS：`log stream --predicate 'process == "sshd"' --info`（连接时观察）；
   - Linux：`sudo journalctl -u sshd -f`（Debian/Ubuntu 是 `-u ssh`）；
   - Windows：`Get-WinEvent -LogName OpenSSH/Operational -MaxEvents 30 | Format-List TimeCreated,Message`。

### 反连时用户名/主机确认

反连命令中的用户是**本地电脑的用户名**（bootstrap 交互中的 "Local username"），不是服务器用户名。Windows 域账户可能需要 `DOMAIN\user` 或 `user@domain` 形式。

## 3. 隧道频繁断开

- 已配置 `ServerAliveInterval 30` / `ServerAliveCountMax 3`（约 90 秒发现死连接并退出）；
- 自启动方案会自动重连：macOS LaunchAgent 的 `KeepAlive`，Windows keepalive 脚本的重连循环；
- 若手动前台跑，可以自己包一层循环：`while true; do ssh -N remote-claude; sleep 15; done`；
- 笔记本睡眠后 TCP 会断，唤醒后等自动重连（最多约 90s + 15s）。

## 4. sshd 配置类问题

### `sshd -t` 校验失败

bootstrap 工具在校验失败时会自动回滚到备份。手动改过配置的话：

```bash
# macOS
sudo /usr/sbin/sshd -t          # 输出具体错误行号
# Windows（管理员）
& "$env:SystemRoot\System32\OpenSSH\sshd.exe" -t
```

备份文件在原文件旁边，命名为 `*.claude-bak-<时间戳>`。

### macOS：`systemsetup -setremotelogin on` 失败

新版 macOS 对 `systemsetup` 有 TCC 限制（需要给终端"完全磁盘访问权限"）。绕过方式：直接在 系统设置 → 通用 → 共享 → 远程登录 手动打开，然后重跑脚本（其余步骤不受影响）。

### macOS：改了 sshd_config 不生效

macOS 的 sshd 由 launchd 按连接拉起，新配置对**新连接**生效。强制重载：

```bash
sudo launchctl kickstart -k system/com.openssh.sshd
```

### Windows：`Add-WindowsCapability` 失败（0x800f0954 等）

通常是 WSUS/组策略挡住了按需功能下载。可让管理员放开 "Specify settings for optional component installation"，或用离线方式安装 OpenSSH Server（微软官方发布的 Win32-OpenSSH：`https://github.com/PowerShell/openssh-portable/releases`）。

### Windows：`Bad owner or permissions on .ssh/config`

ssh 客户端也校验 config 文件 ACL。重跑 bootstrap 工具，或对 `%USERPROFILE%\.ssh\config` 执行与上面 authorized_keys 相同的 icacls 命令。

## 5. 自启动问题

### macOS LaunchAgent 没拉起

```bash
launchctl print gui/$(id -u)/com.claude.dev-tunnel     # 查看状态和上次退出码
cat ~/Library/Logs/remote-claude.err.log            # 看 ssh 报错
```

常见原因：key 尚未加到服务器（Permission denied 循环重试，注意 `ThrottleInterval 30` 会限制重试频率）。

### Windows 计划任务没启动 / 一闪而过

```powershell
Get-ScheduledTaskInfo -TaskName ClaudeDevTunnel    # LastRunTime / LastTaskResult
```

- 任务默认"仅当用户登录时运行"，注销后 tunnel 会停止——这是预期行为（key 的 ACL 属于该用户）；
- 手动验证 keepalive 脚本：`powershell -File $env:USERPROFILE\.ssh\remote-claude-keepalive.ps1`（前台跑，直接看 ssh 输出）。

### Linux systemd user service 没启动 / 注销后停止

```bash
systemctl --user status remote-claude.service
journalctl --user -u remote-claude -f
```

- user service 会在最后一个会话结束时停止；开启 lingering 可让 tunnel 持续运行：`sudo loginctl enable-linger $USER`；
- 改过 unit 文件后要 `systemctl --user daemon-reload`。

## 6. 服务器端问题（ssh my-device / sshfs 挂载）

### `ssh my-device` 报 `Host key verification failed` / `REMOTE HOST IDENTIFICATION HAS CHANGED`

`127.0.0.1:<reverse_port>` 背后应答的主机 key 变了——通常是因为现在持有隧道的是*另一台*本地机器（或本地重装了系统）。如果这个变化是你预期的：

```bash
rm ~/.ssh/known_hosts.my-device     # 该别名使用独立的 known-hosts 文件
ssh my-device 'echo ok'             # accept-new 会存下新 key
```

如果你**没有**预期隧道背后的机器发生变化，先停下来检查反向端口上实际监听的是什么。

### sshfs 挂载失败或变成僵尸

- 服务器上必须装有 `sshfs`（`apt install sshfs` 等）；
- 隧道必须在线（先 `ssh my-device 'echo ok'`）；
- 隧道断开后挂载点卡死（"Transport endpoint is not connected"）：`fusermount -u <挂载点>` 后重新挂载（挂载带 `-o reconnect` 有帮助，但长时间中断仍可能卡住）。

## 7. 一切正常但想确认安全性

```bash
# 服务器上：反向端口应只监听 127.0.0.1
ss -tln | grep <reverse_port>        # 期望 127.0.0.1:<reverse_port>，不能是 0.0.0.0

# 本地：服务器侧 key 应只能通过隧道使用——确认 authorized_keys 里那一行
# 带有 from="127.0.0.1,::1" 限制
grep 'from="127.0.0.1' ~/.ssh/authorized_keys
```
