# reverse-ssh-bootstrap

[English](README.md) | 中文

让远程服务器上的 Claude / Codex 通过反向 SSH 隧道回到你的本地电脑（Windows / macOS / Linux），在本地项目目录中干活。隧道两端都只绑定 `127.0.0.1`——反向端口绝不暴露到局域网或公网，服务器侧的 key 也只能通过隧道使用。

```
你（本地）        : ssh -N remote-claude        （保持运行）
服务器上的 agent  : ssh my-device                （落到你的电脑上）
```

## 1. 配置本地电脑

**macOS / Linux** — 运行后从菜单里选要做的项——每一项都相互独立、可重复运行、并显示是否已配置好（只有 sshd 那一项需要 sudo）：

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

各菜单项只询问自己需要的信息：隧道配置项询问服务器地址/用户/端口和反向端口（默认 2222）；授权项询问第 2 步打印的服务器侧公钥——可以先做第 2 步再粘贴，也可以先跳过该项之后重跑。脚本本身不会发起任何 SSH 连接，公钥交换全部通过复制粘贴完成。

### 可选：让隧道走 xray（VLESS）代理（macOS）

网络较差 / 受限时，macOS 引导脚本多了一个菜单项（**6) xray client**）：粘贴一个
`vless://` URL，它会装好 xray 并起一个本地 SOCKS 代理。随后菜单项 4 会询问是否把
`remote-claude` 隧道走该代理——于是 VSCode Remote-SSH 和 `ssh remote-claude -t
"claude"` 会自动把 SSH 套进 xray；xray 在连接时按需拉起，无后台常驻服务。

如果隧道配置（菜单项 4）已经写好、xray 是后来才加的：用菜单项 **7** 一键开/关
代理——它复用 config block 里已有的服务器信息，无需重新输入。

停止按需启动的 xray：

```bash
pkill -f 'xray run -c .*remote-claude/xray.json'
```

## 2. 配置服务器

在远程服务器上（不需要 sudo）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

它也是同样的菜单形式。第一个菜单项会打印一个公钥——粘贴到本地 bootstrap 询问 "server-side public key" 的地方。两边的反向端口要填一样的。另有独立菜单项负责安装 `~/.claude/CLAUDE.md` 指令，让 Claude Code 的所有项目操作都走 `ssh my-device`，而不是读写服务器本地文件；以及种下一个由 agent 自己维护的 facts 文件（`~/.config/remote-claude/facts.json`——你机器的操作系统、各项目路径和简介），agent 每次会话开始先读它、学到新事实就更新，新会话不用每次重新摸索。

如果当时跳过了 CLAUDE.md 那一步（或只想要这份指令、不装其他东西），可以直接下载：

```bash
mkdir -p ~/.claude && curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/CLAUDE.md >> ~/.claude/CLAUDE.md
```

这是追加写入，已有的 `~/.claude/CLAUDE.md` 内容会保留——但重复执行会追加出重复内容，而且 setup 脚本的受管安装不会识别这种手动加入的副本。两种安装方式选一种即可。

还有一个菜单项询问**本地机器的公钥**（本地 bootstrap 的"显示公钥"项会打印；也可以在你电脑上 `cat ~/.ssh/id_ed25519.pub`），粘贴后写入服务器的 `~/.ssh/authorized_keys`——这一步授权的就是隧道登录。当时跳过的话，重跑对应菜单项粘贴，或改用 `ssh-copy-id`。

## 3. 启动隧道，开始使用

在本地电脑上（保持运行）：

```bash
ssh -N remote-claude
```

在服务器上，日常用法就是：像平时一样连上服务器（比如 **VSCode Remote-SSH**），直接启动 `claude`。第 2 步装好的 `~/.claude/CLAUDE.md` 会让它的所有项目操作都走 `ssh my-device` 在你电脑上执行——你只需要告诉它这次在哪个本地项目干活（"在 `~/projects/foo` 上工作"）。

快速隧道测试：

```bash
ssh my-device 'echo ok'                # 应打印 ok
```

## 停止 / 卸载

- 停止隧道：在运行 `ssh -N remote-claude` 的终端里 `Ctrl-C`。
- 脚本改动的所有文件都先备份（`*.claude-bak-<时间戳>`），且文件名/配置块里都带 `claude` 标记，很容易找到并删除。需要完整回滚时，直接让你的 AI 助手读本仓库的脚本带你操作即可。

## 遇到问题？

看 [TROUBLESHOOTING.zh-CN.md](TROUBLESHOOTING.zh-CN.md)——按症状逐跳排查。
