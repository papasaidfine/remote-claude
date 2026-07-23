# remote-claude

[English](README.md) | 中文

让远程服务器上的 Claude 在**你自己电脑**（Windows / macOS / Linux）的代码上
干活。你电脑上的一个托盘小应用常驻着一条反向 SSH 隧道，agent 通过它 `ssh` 回到
你的电脑，所有项目操作都在本地执行。你连服务器的普通 SSH / VSCode Remote-SSH
会话想开几条开几条——隧道是单独维护的，互不阻塞。

```
你      ── VSCode Remote-SSH / ssh <host> ──▶  服务器   （会话数量不限）
agent   ── ssh "$LC_CLIENT_NAME" ──▶  你的电脑          （反向隧道，由应用维护）
```

这个应用本质是你 `~/.ssh/config` 的一个可视化界面：列出你的 host，每个 host 都能
开反向隧道、走 xray、经连接配置好服务器端、以及查看 Claude 用量和花费。一台服务器
可以同时服务你的多台设备——每台取一个名字，agent 会连到你当前所在的那一台。

## 获取

从 [最新 release](https://github.com/papasaidfine/remote-claude/releases) 下载对应
系统的桌面应用：

- Windows —— `remote-claude-gui_windows_amd64.exe`
- macOS —— `remote-claude-gui_darwin_arm64`
- Linux —— `remote-claude-gui_linux_amd64`

（二进制未签名；Windows 首次运行点掉 SmartScreen 提示即可。）

想用终端 / 无头机器？CLI 提供同一套界面（本地网页形式）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)   # macOS / Linux
```
```powershell
irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1 | iex           # Windows
```

然后 `remote-claude serve` 会在浏览器打开界面。GitHub 连不上或很慢？用 `RC_PROXY`
让下载走你自己的代理（如 `export RC_PROXY=http://127.0.0.1:7890`）。

## 使用

打开应用，然后：

1. **给这台机器起名**（如 `lc-pc`）——agent 靠这个名字连回你。名字默认锁定，点
   **Edit** 才能改。
2. **安装 / 确保本地 ssh 服务器**——让 agent 能连回来（可能需要 `sudo` / 管理员）。
3. **添加 host**（或用 `~/.ssh/config` 里已有的）——地址、SSH 用户、端口。
4. **启用反向隧道**并设端口，再 **Start**——建立并自动重连。
5. **配置服务器**——经这条连接把远端配好。首次可能要输你在**服务器上**的密码以
   授权你的 key，之后全自动。
6. **Xray**（可选，网络受限/慢时用）——在 Xray 区下载二进制、填 `vless://` 节点，
   然后按 host 打开 xray。
7. **Usage**——按 host 查看 Claude 用量与 Anthropic 计价，分模型，覆盖过去 1 / 7 /
   30 天。

打开 **Start this app when I log in** 可让隧道开机自动常驻。关窗会缩到托盘，从托盘
菜单退出。顶部的语言选择器可切换界面语言（English / 中文）。**Check for updates**
会就地安装最新版本——直连下载卡住时会自动改走 xray 重试。

然后照常连服务器（VSCode Remote-SSH 或 `ssh <host>`）并启动 Claude。在服务器上，
agent 通过 `ssh "$LC_CLIENT_NAME"` 在你的电脑上干活。
