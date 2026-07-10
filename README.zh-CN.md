# reverse-ssh-bootstrap

[English](README.md) | 中文

让远程服务器上的 Claude / Codex 通过反向 SSH 隧道回到你的本地电脑（Windows / macOS / Linux），在本地项目目录中干活。全程只走 `127.0.0.1`，不暴露到局域网或公网。

```
你（本地）        : ssh -N remote-claude        （保持运行）
服务器上的 agent  : ssh my-device                （落到你的电脑上）
```

## 1. 配置本地电脑

**macOS / Linux** — 运行后按提示回答（配置 sshd 需要 sudo）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-macos.sh)   # macOS
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-linux.sh)   # Linux
```

**Windows** — 在**管理员** PowerShell 中：

```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/local/bootstrap-windows.ps1 -OutFile bootstrap-windows.ps1
Set-ExecutionPolicy -Scope Process Bypass -Force
.\bootstrap-windows.ps1
```

脚本会询问服务器地址/用户/端口、反向端口（默认 2222）、以及第 2 步打印的服务器侧公钥——可以先做第 2 步再粘贴，也可以留空之后重跑。脚本本身不会发起任何 SSH 连接，公钥交换全部通过复制粘贴完成。

## 2. 配置服务器

在远程服务器上（不需要 sudo）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

它会打印一个公钥——粘贴到本地 bootstrap 询问 "server-side public key" 的地方。两边的反向端口要填一样的。它还会询问是否安装 `~/.claude/CLAUDE.md` 指令，让 Claude Code 的所有项目操作都走 `ssh my-device`，而不是读写服务器本地文件。

如果当时跳过了 CLAUDE.md 那一步（或只想要这份指令、不装其他东西），可以直接下载：

```bash
mkdir -p ~/.claude && curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/CLAUDE.md >> ~/.claude/CLAUDE.md
```

这是追加写入，已有的 `~/.claude/CLAUDE.md` 内容会保留——但重复执行会追加出重复内容，而且 setup 脚本的受管安装不会识别这种手动加入的副本。两种安装方式选一种即可。

另外把本地 bootstrap 打印的**本地公钥**自行加入服务器的 `~/.ssh/authorized_keys`（例如 `ssh-copy-id -i ~/.ssh/id_ed25519.pub <user>@<server>`，或通过你惯用的控制台粘贴）。

## 3. 启动隧道，开始使用

在本地电脑上（保持运行；两侧 bootstrap 都提供开机自启选项）：

```bash
ssh -N remote-claude
```

在服务器上，日常用法就是：像平时一样连上服务器（比如 **VSCode Remote-SSH**），直接启动 `claude`。第 2 步装好的 `~/.claude/CLAUDE.md` 会让它的所有项目操作都走 `ssh my-device` 在你电脑上执行——你只需要告诉它这次在哪个本地项目干活（"在 `~/projects/foo` 上工作"）。

快速测试和终端小工具——这些是给你自己在终端里用的，agent 不需要它们（它直接跑 `ssh my-device`）：

```bash
ssh my-device 'echo ok'                # 隧道测试：应打印 ok
claude-local                           # 打开你电脑上的交互 shell
claude-local git status                # 在你电脑上跑一条命令
claude-local-mount                     # 把本地项目 sshfs 挂载到服务器
```

## 停止 / 卸载

- 停止隧道：`Ctrl-C`；如果开了自启：
  - macOS：`launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.claude.dev-tunnel.plist`
  - Linux：`systemctl --user disable --now remote-claude.service`
  - Windows：`Stop-ScheduledTask -TaskName ClaudeDevTunnel`
- 脚本改动的所有文件都先备份（`*.claude-bak-<时间戳>`），且文件名/配置块里都带 `claude` 标记，很容易找到并删除。需要完整回滚时，直接让你的 AI 助手读本仓库的脚本带你操作即可。

## 遇到问题？

看 [TROUBLESHOOTING.zh-CN.md](TROUBLESHOOTING.zh-CN.md)——按症状逐跳排查。
