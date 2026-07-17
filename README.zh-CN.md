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

菜单（每项显示是否已配置，从上到下做即可）：

1. 接收 SSH 连接（sshd）
2. 本地 SSH key
3. 授权服务器的反连公钥
4. SSH config 快捷方式（`Host remote-claude`）
5. 反向隧道端口
6. xray 代理客户端——可选，网络差时用
7. 隧道走 xray
8. 显示本地公钥

Linux 没有 xray，菜单为 1–5 加"显示本地公钥"。

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
