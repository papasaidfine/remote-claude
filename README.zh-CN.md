# reverse-ssh-bootstrap

[English](README.md) | 中文

让远程服务器上的 Claude 在**你自己电脑**（Windows / macOS / Linux）的代码上
干活：你平时连服务器的那条 SSH 连接会顺带建立一条反向隧道，agent 通过它
`ssh my-device` 回到你的电脑，所有项目操作都在本地执行。

```
你      ── VSCode Remote-SSH / ssh remote-claude ──▶  服务器
agent   ── ssh my-device ──▶  你的电脑    （反向 ssh）
```

## 1. 配置本地电脑

**macOS / Linux：**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)
```

**Windows：**

```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1 | iex
```

会下载 `remote-claude` 二进制并打开配置菜单（重新运行即可更新）。只有
**接收 SSH 连接**那一项需要提权——Windows 用管理员 PowerShell，macOS/Linux 用
`sudo`。

菜单分三个阶段（每项显示是否已配置，从上到下做即可）：

**① 本机 ──▶ Claude**——先连上跑 Claude Code 的服务器

1. SSH config 快捷方式（`Host remote-claude`）
2. 本地 SSH key——生成并显示公钥
3. 测试连接——检查出向连接；反向隧道配置好后也会一并验证整条回路

**② Claude ──▶ 本机**——让 agent 能 ssh 回你的电脑

4. 接收 SSH 连接（sshd）
5. 授权服务器的反连公钥
6. 反向隧道端口

**③ xray ═[ ssh ]═▶**——可选，网络差时用；会先询问下载代理（回车 = 直连）

7. xray 代理客户端
8. 隧道走 xray

Linux 没有 xray，菜单为阶段 ①–②（第 1–6 项）。

## 2. 配置服务器

在远程服务器上（不需要 sudo）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/server/setup-server.sh)
```

菜单：

1. 反连 key（生成 + 显示公钥）
2. 授权你本地电脑的公钥（隧道登录用）
3. `my-device` ssh 别名
4. Agent 指令（`~/.claude/CLAUDE.md`）
5. Agent facts 文件（`facts.json`）
