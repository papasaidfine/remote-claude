// Package i18n holds the UI string catalog and language selection. Strings are
// keyed; T looks a key up in the active language, falling back to English and
// then to the key itself, so a missing translation degrades gracefully instead
// of showing blank.
//
// Both front-ends — the desktop GUI and the terminal UI — read this one catalog,
// so their wording cannot drift apart.
package i18n

import (
	"os"
	"strings"
)

// Lang is a supported UI language code.
type Lang string

const (
	EN Lang = "en"
	ZH Lang = "zh"
)

// Available lists the selectable languages, in display order.
var Available = []Lang{EN, ZH}

// Name is the language's own-script display name (for the picker).
func (l Lang) Name() string {
	switch l {
	case ZH:
		return "中文"
	default:
		return "English"
	}
}

// Parse maps a stored code to a Lang. An empty or unknown code falls back to
// Detect (the OS-locale guess).
func Parse(code string) Lang {
	switch Lang(strings.ToLower(strings.TrimSpace(code))) {
	case EN:
		return EN
	case ZH:
		return ZH
	default:
		return Detect()
	}
}

// Detect guesses a default language from the POSIX locale environment. Windows
// leaves these unset, so it defaults to English; the user's saved choice (if
// any) takes precedence over this anyway.
func Detect() Lang {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		if v := strings.ToLower(os.Getenv(k)); v != "" {
			if strings.HasPrefix(v, "zh") || strings.Contains(v, "zh_") || strings.Contains(v, "zh-") {
				return ZH
			}
			return EN
		}
	}
	return EN
}

// T returns the message for key in lang, falling back to English then the key.
func T(lang Lang, key string) string {
	if m, ok := messages[key]; ok {
		if s, ok := m[lang]; ok && s != "" {
			return s
		}
		if s, ok := m[EN]; ok && s != "" {
			return s
		}
	}
	return key
}

// Printer binds a language so call sites can write p.T("key").
type Printer struct{ Lang Lang }

// P builds a Printer for lang.
func P(lang Lang) Printer { return Printer{Lang: lang} }

// T looks up key in the printer's language.
func (p Printer) T(key string) string { return T(p.Lang, key) }

// messages is the catalog: key -> language -> text. Format strings keep their
// %-verbs identical across languages (callers fmt.Sprintf the result).
//
// The UI never names the proxy implementation (xray) — not in a value and not in
// a key, since T echoes an unknown key straight to the screen. TestNoXrayInUserFacingText
// enforces this.
var messages = map[string]map[Lang]string{
	// top bar / chrome
	"machine_name_label": {EN: "This machine's name", ZH: "这台机器的名字"},
	"machine_name_ph":    {EN: "this machine's name, e.g. lc-pc", ZH: "这台机器的名字，如 lc-pc"},
	"edit":               {EN: "Edit", ZH: "编辑"},
	"save":               {EN: "Save", ZH: "保存"},
	"start_on_login":     {EN: "Start this app when I log in", ZH: "登录时自动启动本应用"},
	// No leading "+": the button carries an add icon, and the two together read
	// as "+ + Add host".
	"add_host":         {EN: "Add host", ZH: "添加主机"},
	"local_ssh_server": {EN: "Local ssh server", ZH: "本地 ssh 服务器"},
	"refresh":          {EN: "Refresh", ZH: "刷新"},
	"language":         {EN: "Language", ZH: "语言"},
	"check_update":     {EN: "Check for updates", ZH: "检查更新"},
	"tab_hosts":        {EN: "Hosts", ZH: "主机"},
	"tab_settings":     {EN: "Settings", ZH: "设置"},

	// status line
	"status_fmt":   {EN: "%s  ·  local ssh server: %s  ·  %d proxy node(s)  ·  hosts from ~/.ssh/config", ZH: "%s  ·  本地 ssh 服务器：%s  ·  %d 个代理节点  ·  主机读取自 ~/.ssh/config"},
	"running":      {EN: "running", ZH: "运行中"},
	"not_detected": {EN: "not detected", ZH: "未检测到"},

	// host list
	"no_hosts":             {EN: "No hosts in ~/.ssh/config yet — click “Add host”.", ZH: "~/.ssh/config 里还没有主机——点“添加主机”。"},
	"reverse_status_fmt":   {EN: "reverse tunnel :%d  ·  tunnel: %s", ZH: "反向隧道 :%d  ·  隧道：%s"},
	"start":                {EN: "Start", ZH: "启动"},
	"stop":                 {EN: "Stop", ZH: "停止"},
	"setup_server":         {EN: "Set up server", ZH: "配置服务器"},
	"usage":                {EN: "Usage", ZH: "用量"},
	"open_vscode":          {EN: "VS Code", ZH: "VS Code"},
	"copy_cli":             {EN: "Claude command", ZH: "Claude 命令"},
	"cli_copied_fmt":       {EN: "Copied: %s", ZH: "已复制：%s"},
	"opening_vscode_fmt":   {EN: "Opening %s in VS Code…", ZH: "正在用 VS Code 打开 %s…"},
	"use_proxy":            {EN: "Use proxy", ZH: "使用代理"},
	"auto_start_tunnel":    {EN: "Start tunnel when app opens", ZH: "打开应用时启动隧道"},
	"delete":               {EN: "Delete", ZH: "删除"},
	"plain_host":           {EN: "plain ssh host — no reverse tunnel", ZH: "普通 ssh 主机——无反向隧道"},
	"delete_host_title":    {EN: "Delete host", ZH: "删除主机"},
	"delete_host_conf_fmt": {EN: "Delete “%s” from ~/.ssh/config? This stops its tunnel.", ZH: "从 ~/.ssh/config 删除“%s”？这会停止它的隧道。"},

	// host table (terminal UI)
	"col_alias":     {EN: "Alias", ZH: "别名"},
	"col_target":    {EN: "Target", ZH: "目标"},
	"col_tunnel":    {EN: "Tunnel", ZH: "隧道"},
	"col_state":     {EN: "State", ZH: "状态"},
	"keys_hosts":    {EN: " s start   x stop   c set up server   u usage   e edit   a add   d delete   r refresh   q quit", ZH: " s 启动   x 停止   c 配置服务器   u 用量   e 编辑   a 添加   d 删除   r 刷新   q 退出"},
	"keys_settings": {EN: " Tab/arrows move   Enter activate   1 back to hosts   q quit", ZH: " Tab/方向键 移动   Enter 执行   1 回到主机   q 退出"},

	// tunnel states
	"state_stopped":    {EN: "stopped", ZH: "已停止"},
	"state_connecting": {EN: "connecting", ZH: "连接中"},
	"state_up":         {EN: "up", ZH: "已连接"},
	"state_retrying":   {EN: "retrying", ZH: "重连中"},

	// add/edit dialogs
	"add_host_title": {EN: "Add host", ZH: "添加主机"},
	"add":            {EN: "Add", ZH: "添加"},
	"cancel":         {EN: "Cancel", ZH: "取消"},
	"alias_ssh_name": {EN: "Alias (ssh name)", ZH: "别名（ssh 名称）"},
	"host_ip":        {EN: "Host / IP", ZH: "主机 / IP"},
	"ssh_user":       {EN: "SSH user", ZH: "SSH 用户"},
	"ssh_port":       {EN: "SSH port", ZH: "SSH 端口"},
	"edit_title_fmt": {EN: "Edit %s", ZH: "编辑 %s"},
	"reverse_port":   {EN: "Reverse port (blank = off)", ZH: "反向端口（留空 = 关闭）"},

	// set up server
	"connecting_fmt":    {EN: "Connecting to %s …", ZH: "正在连接 %s …"},
	"authorize_title":   {EN: "Authorize this machine on the server", ZH: "在服务器上授权这台机器"},
	"authorize_instr":   {EN: "The server %s hasn't authorized this machine's key yet.\nAdd the public key below to ~/.ssh/authorized_keys on the server, then click “Set up server” again.", ZH: "服务器 %s 还没授权这台机器的密钥。\n把下面这段公钥加到服务器的 ~/.ssh/authorized_keys，然后再点一次“配置服务器”。"},
	"copy":              {EN: "Copy", ZH: "复制"},
	"copied":            {EN: "Public key copied to clipboard.", ZH: "公钥已复制到剪贴板。"},
	"server_configured": {EN: "Server configured", ZH: "服务器已配置"},
	"server_conf_fmt":   {EN: "Configured as %q. Its connect-back key was %s on this machine.", ZH: "已配置为 %q。它的回连密钥在本机%s。"},
	"authorized":        {EN: "authorized", ZH: "已授权"},
	"already_present":   {EN: "already present", ZH: "本已存在"},

	// usage
	"reading_usage_fmt": {EN: "Reading Claude usage from %s …", ZH: "正在从 %s 读取 Claude 用量 …"},
	"usage_title_fmt":   {EN: "Claude usage — %s", ZH: "Claude 用量 — %s"},
	"close":             {EN: "Close", ZH: "关闭"},
	"past_1d":           {EN: "Past 1 day", ZH: "过去 1 天"},
	"past_7d":           {EN: "Past 7 days", ZH: "过去 7 天"},
	"past_30d":          {EN: "Past 30 days", ZH: "过去 30 天"},
	"no_usage_window":   {EN: "No usage in this window.", ZH: "此时间段没有用量。"},
	"failed_fmt":        {EN: "Failed: %s", ZH: "失败：%s"},
	"col_model":         {EN: "Model", ZH: "模型"},
	"col_input":         {EN: "Input", ZH: "输入"},
	"col_output":        {EN: "Output", ZH: "输出"},
	"col_cache_w":       {EN: "CacheW", ZH: "缓存写"},
	"col_cache_r":       {EN: "CacheR", ZH: "缓存读"},
	"col_cost":          {EN: "Cost", ZH: "花费"},
	"col_total":         {EN: "TOTAL", ZH: "合计"},
	"col_share":         {EN: "Share of cost", ZH: "花费占比"},
	"usage_total_fmt":   {EN: "%s total", ZH: "合计 %s"},

	// settings — section headings
	"settings_general":   {EN: "General", ZH: "通用"},
	"settings_proxy":     {EN: "Proxy", ZH: "代理"},
	"settings_local_ssh": {EN: "Local ssh server", ZH: "本地 ssh 服务器"},
	"settings_keys":      {EN: "SSH key", ZH: "SSH 密钥"},
	"settings_about":     {EN: "About", ZH: "关于"},

	// settings — proxy
	"proxy_install":     {EN: "Install / update proxy components", ZH: "安装 / 更新代理组件"},
	"proxy_via_ph":      {EN: "download via proxy, e.g. http://127.0.0.1:7890 (optional, one-time)", ZH: "经代理下载，如 http://127.0.0.1:7890（可选，一次性）"},
	"proxy_via_label":   {EN: "Download via", ZH: "经代理下载"},
	"proxy_intro":       {EN: "For restricted or slow networks. Install the components, add nodes below, then turn on “Use proxy” for a host under Edit.", ZH: "用于受限或缓慢的网络。先安装组件、在下面添加节点，然后在主机的「编辑」里打开「使用代理」。"},
	"downloading":       {EN: "Downloading… (this can take a moment)", ZH: "下载中…（可能要等一会儿）"},
	"proxy_ready":       {EN: "Proxy ready.", ZH: "代理就绪。"},
	"proxy_installed":   {EN: "installed", ZH: "已安装"},
	"proxy_missing":     {EN: "not installed", ZH: "未安装"},
	"proxy_unsupported": {EN: "The proxy is optional on this system.", ZH: "此系统上代理为可选项。"},
	"nodes_label":       {EN: "Nodes (one vless:// per line):", ZH: "节点（每行一个 vless://）："},
	"save_nodes":        {EN: "Save nodes", ZH: "保存节点"},
	"nodes_ph":          {EN: "one vless:// URL per line; # comments allowed", ZH: "每行一个 vless:// URL；允许 # 注释"},
	"nodes_saved_fmt":   {EN: "Saved — %d node(s).", ZH: "已保存——%d 个节点。"},

	// settings — local ssh server
	"sshd_disable_pw": {EN: "also disable password login (recommended)", ZH: "同时禁用密码登录（推荐）"},
	"sshd_info":       {EN: "Install/ensure the local ssh server so the agent can reach back in. May prompt for sudo / Administrator in the terminal where you launched the app.", ZH: "安装/确保本地 ssh 服务器，好让 agent 能连回来。可能会在你启动应用的终端里要求 sudo / 管理员权限。"},
	"sshd_install":    {EN: "Install local ssh server", ZH: "安装本地 ssh 服务器"},
	"sshd_done_fmt":   {EN: "Done — running: %s", ZH: "完成——运行中：%s"},

	// settings — ssh key
	"key_show":    {EN: "Create / show public key", ZH: "创建 / 显示公钥"},
	"key_title":   {EN: "This machine's public key", ZH: "这台机器的公钥"},
	"key_intro":   {EN: "A server must have this key in its ~/.ssh/authorized_keys before this machine can connect to it.", ZH: "服务器的 ~/.ssh/authorized_keys 里要有这段公钥，这台机器才连得上它。"},
	"version_fmt": {EN: "Version %s", ZH: "版本 %s"},

	// self-update
	"update_title":        {EN: "Update", ZH: "更新"},
	"update_checking":     {EN: "Checking for updates…", ZH: "正在检查更新…"},
	"update_latest_fmt":   {EN: "You're on the latest version (%s).", ZH: "已是最新版本（%s）。"},
	"update_dev":          {EN: "This is a development build — nothing to update.", ZH: "这是开发版——无需更新。"},
	"update_avail_fmt":    {EN: "Update available: %s → %s.\nDownload and install now?", ZH: "有可用更新：%s → %s。\n现在下载并安装吗？"},
	"update_download_yes": {EN: "Download & install", ZH: "下载并安装"},
	"update_downloading":  {EN: "Downloading update… (retries through the proxy if the direct download stalls)", ZH: "正在下载更新…（直连超时会自动尝试经代理）"},
	"update_failed_fmt":   {EN: "Update failed: %s", ZH: "更新失败：%s"},
	"update_done":         {EN: "Update installed. Restart now to use the new version?", ZH: "更新已安装。现在重启以使用新版本吗？"},
	"restart_now":         {EN: "Restart now", ZH: "现在重启"},
	"later":               {EN: "Later", ZH: "以后"},
}
