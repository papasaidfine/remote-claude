# remote-claude

[English](README.md) | 中文

让远程服务器上的 Claude 在**你自己电脑**（Windows / macOS / Linux）的代码上
干活。你电脑上的一个小应用常驻着一条反向 SSH 隧道，agent 通过它 `ssh` 回到你
的电脑，所有项目操作都在本地执行。你连服务器的普通 SSH / VSCode Remote-SSH
会话想开几条开几条——隧道是单独维护的，互不阻塞。

```
你      ── VSCode Remote-SSH / ssh remote-claude ──▶  服务器   （会话数量不限）
agent   ── ssh "$LC_CLIENT_NAME" ──▶  你的电脑              （反向隧道，由应用维护）
```

一台服务器可以同时服务你的多台设备——每台设备取一个名字，agent 会连到你当前
所在的那一台。

## 安装

**macOS / Linux**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)
```

**Windows**

```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1 | iex
```

GitHub 连不上或很慢？用 `RC_PROXY` 让下载走你自己的代理：

```bash
export RC_PROXY=http://127.0.0.1:7890
bash <(curl -fsSL --proxy "$RC_PROXY" https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)
```

```powershell
$env:RC_PROXY = 'http://127.0.0.1:7890'
(irm -Proxy $env:RC_PROXY https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1) | iex
```

## 使用

运行 `remote-claude`，它会在浏览器里打开应用，并常驻后台维持隧道。在应用里：

1. **给这台机器起名**（如 `lisa-laptop`）——agent 就靠这个名字连回你。
2. **添加服务器**——地址、SSH 用户、反向端口。网络差就打开 xray 并填入你的
   `vless://` 节点。
3. **安装 / 确保本地 ssh 服务器**——让 agent 能连回来（可能需要 `sudo` /
   管理员）。
4. **启动隧道**——建立反向隧道并自动重连。
5. **配置服务器**——经这条连接把远端配好。**首次**需要输入你在**服务器上**的
   密码以授权你的 key，之后全自动。

然后照常连服务器（VSCode Remote-SSH 或 `ssh remote-claude`）并启动 Claude。在
服务器上，agent 通过 `ssh "$LC_CLIENT_NAME"` 在你的电脑上干活。

无头机器上用 `remote-claude serve` 启动应用而不打开浏览器。
