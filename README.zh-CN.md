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
开反向隧道、走代理、经连接配置好服务器端、以及查看 Claude 用量和花费。一台服务器
可以同时服务你的多台设备——每台取一个名字，agent 会连到你当前所在的那一台。

## 获取

从 [最新 release](https://github.com/papasaidfine/remote-claude/releases) 下载——
需要先登录 GitHub，且账号对本仓库有访问权限。

**桌面应用**（一般用这个）：

- Windows —— `remote-claude-gui_windows_amd64.exe`
- macOS —— `remote-claude-gui_darwin_arm64.dmg`
- Linux —— `remote-claude-gui_linux_amd64`

二进制未签名；Windows 首次运行点掉 SmartScreen 提示即可。装好之后
**设置 → 检查更新** 会就地装上新版本，所以只需要手动下载这一次。

**终端应用**，给没有桌面的机器用——在同一页下载 `remote-claude_<os>_<arch>`，然后：

```bash
chmod +x remote-claude
./remote-claude          # 终端界面
./remote-claude serve    # 只维持隧道，无界面（给 systemd / nohup 用）
```

一台机器上两者只能同时跑一个——重复启动会把已经在跑的那个唤到前台，而不是再开一个。

## 使用

打开应用，在 **主机** 页：

1. **给这台机器起名**（如 `lc-pc`）——agent 靠这个名字连回你。名字默认锁定，点
   **编辑** 才能改。
2. **添加主机**（或用 `~/.ssh/config` 里已有的）——地址、SSH 用户、端口。
3. **启用反向隧道**，然后进这台主机的 **编辑** 设置端口——需要的话，也在这里打开
   **使用代理** 和 **打开应用时启动隧道**。
4. **启动**——隧道建立并自动重连。
5. **配置服务器**——经这条连接把远端配好，**只用密钥、从不用密码**。如果服务器还
   没授权这台机器的密钥，它会把公钥显示出来让你加到服务器的 `authorized_keys`；加好
   后再点一次 **配置服务器** 即可。
6. **用量**——按主机查看 Claude 用量与 Anthropic 计价，分模型，覆盖过去 1 / 7 /
   30 天。

在 **设置** 页：

- **通用**——界面语言（English / 中文）、登录时自动启动本应用。
- **代理**（可选，网络受限或慢时用）——先安装代理组件，再填 `vless://` 节点，每行
  一个。具体哪台主机走代理，在那台主机的 **编辑** 里打开。
- **本地 ssh 服务器**——安装 / 确保它在跑，好让 agent 能连回来（可能需要 `sudo` /
  管理员权限）。
- **SSH 密钥**——显示这台机器的公钥，可以提前拿去服务器上授权。
- **关于**——版本号和 **检查更新**。直连下载卡住时会自动改走代理重试。

关窗会缩到托盘，从托盘菜单退出。

终端应用里这些东西分别在 `1`（主机）和 `2`（设置）两页，底部按键栏列出每个键的作用。

然后照常连服务器（VSCode Remote-SSH 或 `ssh <host>`）并启动 Claude。在服务器上，
agent 通过 `ssh "$LC_CLIENT_NAME"` 在你的电脑上干活。
