# reverse-ssh-bootstrap

[English](README.md) | 中文

让远程服务器上的 Claude / Codex 通过**反向 SSH 隧道**回到你的本地电脑（Windows / macOS），在本地项目目录中进行开发。

本仓库提供一个本地运行的 bootstrap 命令行工具，自动完成：本地 sshd 安装与加固、SSH key 生成、`~/.ssh/config` 写入、`authorized_keys` 写入、以及可选的 tunnel 开机自启。

## 整体架构

```
┌──────────────┐   ① ssh -N claude-dev-tunnel    ┌──────────────┐
│  本地电脑     │ ───────────────────────────────▶ │  远程服务器   │
│ (Win / mac)  │                                  │              │
│              │   ② RemoteForward 反向隧道        │              │
│ 127.0.0.1:22 │ ◀─────────────────────────────── │ 127.0.0.1:   │
│  (本地 sshd)  │                                  │ <reverse_port>│
└──────────────┘                                  └──────┬───────┘
                                                         │ ③ Claude / Codex:
                                                         │ ssh -p <reverse_port>
                                                         │     <local_user>@127.0.0.1
```

1. 本地电脑主动 SSH 到远程服务器，并带上 `RemoteForward`；
2. 服务器上的 `127.0.0.1:<reverse_port>` 被转发到本地电脑的 `127.0.0.1:22`；
3. 服务器上的 Claude / Codex 通过 `ssh -p <reverse_port> <local_user>@127.0.0.1` 反连回本地电脑。

因为两端都只绑定 `127.0.0.1`，隧道两侧都不会暴露到局域网或公网。

## 什么是 RemoteForward

`~/.ssh/config` 中的这一行：

```
RemoteForward 127.0.0.1:<reverse_port> 127.0.0.1:22
```

含义是：在**远程服务器**上监听 `127.0.0.1:<reverse_port>`；任何连到这个端口的连接，都会经由这条 SSH 连接转发到**本地电脑**的 `127.0.0.1:22`（本地 sshd）。

- 第一段地址是服务器侧的监听地址，**必须**写成 `127.0.0.1:<port>`，否则可能受服务器 `GatewayPorts` 配置影响暴露到公网；
- 第二段地址是本地侧的目标地址，指向本地 sshd。

只要 `ssh -N claude-dev-tunnel` 这条连接保持存活，反向通道就一直有效。

## 为什么需要两组 SSH key

方向不同，信任关系不同，所以是两把互不相关的 key：

| Key | 位置 | 用途 |
|---|---|---|
| `~/.ssh/claude_tunnel_ed25519` | **本地电脑** | 本地 → 服务器，用来建立隧道。它的 `.pub` 要加到**服务器**的 `~/.ssh/authorized_keys` |
| `~/.ssh/claude_to_local_ed25519`（名字随意） | **远程服务器** | 服务器上的 Claude / Codex → 本地电脑（走隧道）。它的 `.pub` 要加到**本地电脑**的 `~/.ssh/authorized_keys`（bootstrap 工具会帮你写入） |

不要复用同一把 key：本地 key 泄漏不应等价于"任何人可以登录你的电脑"，反之亦然。私钥永远不离开它所在的机器。

## 快速开始

### macOS

```bash
chmod +x bootstrap-macos.sh
./bootstrap-macos.sh
```

脚本会交互式询问服务器地址、用户、端口、反向端口、服务器侧 public key 等，然后完成全部本地配置（系统级修改需要 sudo）。

### Windows

以**管理员身份**打开 PowerShell：

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
.\bootstrap-windows.ps1
```

也支持参数化调用（跳过对应交互）：

```powershell
.\bootstrap-windows.ps1 -ServerHost 1.2.3.4 -ServerUser dev -ServerPort 22 -ReversePort 2222
```

### 服务器侧准备

1. 在服务器上为 Claude / Codex 生成反连 key（如果还没有）：

   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/claude_to_local_ed25519 -N "" -C "claude-to-local"
   cat ~/.ssh/claude_to_local_ed25519.pub   # 把这行粘贴给 bootstrap 工具
   ```

2. 把 bootstrap 工具打印的**本地隧道 public key**（`claude_tunnel_ed25519.pub`）加入服务器的 `~/.ssh/authorized_keys`（工具也提供 ssh-copy-id 式的自动上传选项）。

### 启动隧道

在本地电脑上：

```bash
ssh -N claude-dev-tunnel
```

保持这条连接，然后在服务器上（Claude / Codex 执行）：

```bash
ssh -i ~/.ssh/claude_to_local_ed25519 -p <reverse_port> <local_user>@127.0.0.1
```

即可回到本地电脑的 shell / 项目目录。

## Windows administrators_authorized_keys 的坑

Windows OpenSSH Server 的默认 `sshd_config` 末尾有：

```
Match Group administrators
       AuthorizedKeysFile __PROGRAMDATA__/ssh/administrators_authorized_keys
```

这段配置的效果是：**只要当前用户属于 Administrators 组**，sshd 就不看 `%USERPROFILE%\.ssh\authorized_keys`，而是只看全局的 `%ProgramData%\ssh\administrators_authorized_keys`。很多"key 加了却 Permission denied"的问题就来自这里。

bootstrap 工具的处理方式：把这两行加上 `# claude-bootstrap disabled:` 前缀注释掉，使管理员用户也回到标准的 `.ssh/authorized_keys` 路径。修改前会备份 `sshd_config`，修改后先 `sshd -t` 校验再重启服务；重复运行是幂等的（已注释的行不会被二次处理）。

## 工具做的安全加固

- 服务器侧反向端口只监听 `127.0.0.1`（`RemoteForward 127.0.0.1:<port> ...`）；
- 本地 sshd 默认只监听 `127.0.0.1`（可选，交互中可关闭）；
- 默认禁用密码登录，仅允许 public key（可选）；
- 写入 `authorized_keys` 的服务器侧 key 带限制前缀：

  ```
  from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding ssh-ed25519 AAAA...
  ```

  即这把 key **只能**通过反向隧道从本机 loopback 登录，从局域网/公网直接拿着私钥也登不进来；
- 不开启 agent forwarding（`ForwardAgent no`）；
- 所有被修改的系统文件先备份（`*.claude-bak-<时间戳>`）；
- 私钥从不打印到终端或日志。

## 手动测试

按顺序验证，出问题时能快速定位在哪一跳：

```bash
# 1. 本地 -> 服务器（隧道那一跳）能通
ssh -i ~/.ssh/claude_tunnel_ed25519 -p <server_port> <server_user>@<server_host> 'echo ok'

# 2. 本地 sshd 正常（在本地电脑上自测回环登录）
ssh -p 22 <local_user>@127.0.0.1 'echo ok'

# 3. 建立隧道（加 -v 看 RemoteForward 是否成功）
ssh -v -N claude-dev-tunnel

# 4. 在服务器上测反连
ssh -i ~/.ssh/claude_to_local_ed25519 -p <reverse_port> <local_user>@127.0.0.1 'echo ok'
```

更多排查见 [TROUBLESHOOTING.zh-CN.md](TROUBLESHOOTING.zh-CN.md)。

## 如何停止 tunnel

- 前台运行的：`Ctrl-C` 即可；
- macOS LaunchAgent：

  ```bash
  launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.claude.dev-tunnel.plist
  ```

- Windows 计划任务：

  ```powershell
  Stop-ScheduledTask -TaskName ClaudeDevTunnel
  Get-Process ssh -ErrorAction SilentlyContinue | Stop-Process   # 结束残留 ssh 进程
  ```

## 删除 / 回滚

所有修改都可以逐项撤销：

### 通用（两个平台）

1. **`~/.ssh/config`**：删除 `# >>> claude-dev-tunnel ... # <<< claude-dev-tunnel <<<` 标记之间的整块；或直接用备份 `~/.ssh/config.claude-bak-<时间戳>` 覆盖回去。
2. **`~/.ssh/authorized_keys`**：删除以 `from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding` 开头、对应服务器侧 key 的那一行。
3. **本地隧道 key**：删除 `~/.ssh/claude_tunnel_ed25519{,.pub}`，并从服务器的 `~/.ssh/authorized_keys` 移除对应公钥。

### macOS

```bash
# 还原 sshd 配置（drop-in 方案直接删文件即可）
sudo rm -f /etc/ssh/sshd_config.d/100-claude-dev-tunnel.conf
# 老系统（直接编辑主配置的情况）：
#   sudo cp /etc/ssh/sshd_config.claude-bak-<时间戳> /etc/ssh/sshd_config
sudo /usr/sbin/sshd -t && sudo launchctl kickstart -k system/com.openssh.sshd

# 关闭 Remote Login（如果之前是关的）
sudo systemsetup -setremotelogin off

# 移除自启动
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.claude.dev-tunnel.plist
rm -f ~/Library/LaunchAgents/com.claude.dev-tunnel.plist
```

### Windows（管理员 PowerShell）

```powershell
# 还原 sshd 配置
Copy-Item "$env:ProgramData\ssh\sshd_config.claude-bak-<时间戳>" "$env:ProgramData\ssh\sshd_config" -Force
Restart-Service sshd

# 如果想完全停用 / 卸载 sshd
Stop-Service sshd
Set-Service sshd -StartupType Disabled
# Remove-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0

# 移除自启动
Unregister-ScheduledTask -TaskName ClaudeDevTunnel -Confirm:$false
Remove-Item "$env:USERPROFILE\.ssh\claude-dev-tunnel-keepalive.ps1" -ErrorAction SilentlyContinue
```

## 目录结构

```
bootstrap-macos.sh                      macOS bootstrap 脚本 (bash)
bootstrap-windows.ps1                   Windows bootstrap 脚本 (PowerShell)
README.md / README.zh-CN.md             本文档（英文 / 中文）
TROUBLESHOOTING.md /
  TROUBLESHOOTING.zh-CN.md              故障排查（英文 / 中文）
examples/
  ssh_config.example                    写入 ~/.ssh/config 的 Host 块示例
  authorized_keys.example               带 from= 限制的 authorized_keys 行示例
  sshd_config.macos.conf                macOS sshd drop-in 配置示例
  sshd_config.windows.snippet           Windows sshd_config 修改点示例
  com.claude.dev-tunnel.plist.example   macOS LaunchAgent 示例
```
