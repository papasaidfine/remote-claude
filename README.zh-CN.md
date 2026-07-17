# reverse-ssh-bootstrap

[English](README.md) | 中文

让远程服务器上的 Claude / Codex 在**你自己电脑**（Windows / macOS / Linux）的代码上
干活：你平时连服务器的那条 SSH 连接会顺带建立一条反向隧道，agent 通过它
`ssh my-device` 回到你的电脑，所有项目操作都在本地执行。

```
你      ── VSCode Remote-SSH / ssh remote-claude ──▶  服务器
agent   ── ssh my-device ──▶  你的电脑    （走反向隧道）
```

- **不用额外跑任何东西**——像平时一样连服务器（VSCode Remote-SSH，或终端里
  `ssh remote-claude`），反向隧道随连接建立、随断开关闭。
- **同一时刻只有一条连接**——反向端口只能被占用一次，第二条 `remote-claude`
  连接会直接失败，先断开旧的。
- **天生私密**——反向端口两端都只绑 `127.0.0.1`，绝不暴露到局域网或公网；
  服务器侧的 key 只能通过隧道使用。
- **两侧都是菜单式配置**——每项独立、幂等、显示是否已配置，随时可重跑。
  公钥交换全靠复制粘贴，脚本自己不发起任何 SSH 连接。
- **服务器开箱即用**——自动安装 `~/.claude/CLAUDE.md`，让 `claude` 的项目操作都走
  `ssh my-device`；另有 agent 自己维护的 facts 文件（你的系统、项目路径），
  新会话不用重新摸索。
- **可选 xray（VLESS）代理**应对差网络——节点就是一个纯文本列表文件，
  每次 xray 启动随机选一个。
- **好回滚**——脚本动过的文件全部先备份（`*.claude-bak-<时间戳>`）并带 `claude` 标记。

## 1. 配置本地电脑

**macOS / Linux**（只有 sshd 那一项需要 sudo）：

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

按菜单从上到下做即可。问到 "server-side public key" 时，粘贴第 2 步打印的公钥
（也可以先跳过，之后重跑该项）。

## 2. 配置服务器

在远程服务器上（不需要 sudo）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

- 第一项打印服务器公钥 → 粘到本地 bootstrap 询问 "server-side public key" 的地方。
- 另一项询问**你本地电脑的公钥**（本地菜单的"显示公钥"项会打印）→ 授权隧道登录。
- 其余项安装 `~/.claude/CLAUDE.md` 指令和 facts 文件，让 `claude` 在你电脑上干活。
- 两边的反向端口填一样的（默认 2222）。

## 3. 开始使用

像平时一样连服务器——**VSCode Remote-SSH**（host 选 `remote-claude`）或终端里
`ssh remote-claude`——然后启动 `claude`，告诉它做哪个本地项目
（"在 `~/projects/foo` 上工作"）。

快速验证（在服务器上）：

```bash
ssh my-device 'echo ok'                # 应打印 ok
```

如果因为反向端口被占用而连接失败：先断开上一条 `remote-claude` 连接——
隧道同时只能有一条。

## 可选：让隧道走 xray（VLESS）代理

网络较差/受限时，macOS 和 Windows 的引导脚本各多两项。**6) xray client** 安装
xray 并把你的 `vless://` URL 写进节点列表（macOS 在
`~/.config/remote-claude/vless-nodes.txt`，Windows 在
`%LOCALAPPDATA%\remote-claude\vless-nodes.txt`）——一行一个节点、`#` 开头是注释，
每次 xray 启动随机选一个，改文件下次连接即生效。**7)** 一键开/关隧道走代理。
Windows 每条连接自带一个 xray、连接断了自动清理；macOS 共享一个按需 xray，
`pkill xray` 后重连即换节点。

## 卸载

脚本改过的文件都有备份（`*.claude-bak-<时间戳>`），文件名/配置块里都带 `claude`
标记。让你的 AI 助手读本仓库的脚本带你完整回滚即可。

## 遇到问题？

看 [TROUBLESHOOTING.zh-CN.md](TROUBLESHOOTING.zh-CN.md)——按症状逐跳排查。
