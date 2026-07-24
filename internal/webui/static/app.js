"use strict";

const $ = (sel, root = document) => root.querySelector(sel);

// ---- i18n (client-side; the native GUI has its own Go catalog) ----
const I18N = {
  en: {
    language: "Language",
    sub: 'Your <code>~/.ssh/config</code> hosts and their reverse tunnels. Editing a host writes its ssh config; “Start” just launches the tunnel per that config. Keep this window open to keep tunnels up.',
    machine_name: "This machine's name",
    save_name: "Save name",
    alias_hint: 'Written as <code>SetEnv LC_CLIENT_NAME</code> on hosts you add; the server reaches you back as <code>ssh &lt;name&gt;</code>.',
    hosts: "Hosts",
    hosts_sub: "(scanned from ~/.ssh/config)",
    add_host: "+ Add host",
    local_ssh: "Local ssh server",
    local_ssh_hint: "The server reaches you back through the tunnel into this machine's sshd. Installing/hardening it may prompt for <code>sudo</code> in the launching terminal.",
    install_ensure: "Install / ensure running",
    disable_pw: "also disable password login",
    xray_hint: 'Optional, for censored/slow networks. Download the xray binary, then add <code>vless://</code> nodes; turn on “xray” per host to route it through them.',
    xray_download: "Download / update xray",
    xray_proxy_ph: "download via proxy, e.g. http://127.0.0.1:7890 (optional, one-time)",
    nodes: "Nodes",
    nodes_hint: "one vless:// URL per line; each connection picks a random node.",
    save_nodes: "Save nodes",
    reverse_tunnel: "reverse tunnel",
    auto_start: "auto-start",
    start_tunnel: "Start tunnel",
    restart: "Restart",
    stop: "Stop",
    setup_server: "Set up server",
    usage: "Usage",
    edit: "Edit",
    delete: "Delete",
    add_host_title: "Add host",
    alias_ssh: "Alias (the ssh name)",
    host_ip: "Server host / IP",
    ssh_user: "SSH user",
    ssh_port: "SSH port",
    cancel: "Cancel",
    add: "Add",
    all_config_lines: "All config lines for this host:",
    save: "Save",
    past_1d: "Past 1 day",
    past_7d: "Past 7 days",
    past_30d: "Past 30 days",
    loading: "Loading…",
    close: "Close",
    // dynamic
    no_hosts: 'No hosts in ~/.ssh/config yet. Click “+ Add host”.',
    reverse: "reverse",
    no_reverse: "no reverse tunnel",
    xray_on: "xray on",
    direct: "direct",
    tunnel: "tunnel",
    reconnects: "reconnects",
    delete_confirm: "Delete host “%s” from ~/.ssh/config? This stops its tunnel.",
    setup_ok: 'Server configured as “%s”. Its connect-back key was %s on this machine.',
    authorized: "authorized",
    already_present: "already present",
    authorize_title: "Authorize this machine on the server",
    authorize_instr: "The server %s hasn't authorized this machine's key yet. Add the public key below to ~/.ssh/authorized_keys on the server, then click “Set up server” again.",
    copy: "Copy",
    copied: "Public key copied to clipboard.",
    col_model: "Model",
    col_input: "Input",
    col_output: "Output",
    col_cache_w: "CacheW",
    col_cache_r: "CacheR",
    col_cost: "Cost",
    col_total: "TOTAL",
    no_usage: "No usage in this window.",
    reading_usage: "Reading usage from %s …",
    usage_title: "Claude usage — %s",
    downloading: "Downloading… (this can take a moment)",
    xray_ready: "xray ready.",
    saving: "Saving…",
    saved: "Saved (%s).",
    n_nodes: "%s node(s)",
    working: "Working… (may prompt for sudo in the launching terminal)",
    sshd_ready: "Local ssh server ready.",
    platform: "Platform: %s",
    xray_optional: " (xray optional on this OS)",
    running: "running",
    not_running: "not running",
    installed: "installed",
    not_installed: "not installed",
    st_up: "up",
    st_stopped: "stopped",
    st_connecting: "connecting",
    st_retrying: "retrying",
  },
  zh: {
    language: "语言",
    sub: '你 <code>~/.ssh/config</code> 里的主机及其反向隧道。编辑主机会写入它的 ssh 配置；“启动”只是按该配置拉起隧道。保持此窗口打开以维持隧道。',
    machine_name: "这台机器的名字",
    save_name: "保存名字",
    alias_hint: '会作为 <code>SetEnv LC_CLIENT_NAME</code> 写到你添加的主机上；服务器通过 <code>ssh &lt;名字&gt;</code> 连回你。',
    hosts: "主机",
    hosts_sub: "（读取自 ~/.ssh/config）",
    add_host: "+ 添加主机",
    local_ssh: "本地 ssh 服务器",
    local_ssh_hint: "服务器通过隧道连回本机的 sshd。安装/加固它可能会在启动终端里要求 <code>sudo</code>。",
    install_ensure: "安装 / 确保运行",
    disable_pw: "同时禁用密码登录",
    xray_hint: '可选，用于受限/缓慢的网络。下载 xray 二进制，然后添加 <code>vless://</code> 节点；对每个主机打开“xray”即可经它们转发。',
    xray_download: "下载 / 更新 xray",
    xray_proxy_ph: "经代理下载，如 http://127.0.0.1:7890（可选，一次性）",
    nodes: "节点",
    nodes_hint: "每行一个 vless:// URL；每条连接随机选一个节点。",
    save_nodes: "保存节点",
    reverse_tunnel: "反向隧道",
    auto_start: "自动启动",
    start_tunnel: "启动隧道",
    restart: "重启",
    stop: "停止",
    setup_server: "配置服务器",
    usage: "用量",
    edit: "编辑",
    delete: "删除",
    add_host_title: "添加主机",
    alias_ssh: "别名（ssh 名称）",
    host_ip: "服务器主机 / IP",
    ssh_user: "SSH 用户",
    ssh_port: "SSH 端口",
    cancel: "取消",
    add: "添加",
    all_config_lines: "该主机的所有配置行：",
    save: "保存",
    past_1d: "过去 1 天",
    past_7d: "过去 7 天",
    past_30d: "过去 30 天",
    loading: "加载中…",
    close: "关闭",
    // dynamic
    no_hosts: '~/.ssh/config 里还没有主机。点“+ 添加主机”。',
    reverse: "反向",
    no_reverse: "无反向隧道",
    xray_on: "xray 开",
    direct: "直连",
    tunnel: "隧道",
    reconnects: "重连",
    delete_confirm: "从 ~/.ssh/config 删除主机“%s”？这会停止它的隧道。",
    setup_ok: '服务器已配置为“%s”。它的回连密钥在本机%s。',
    authorized: "已授权",
    already_present: "本已存在",
    authorize_title: "在服务器上授权这台机器",
    authorize_instr: "服务器 %s 还没授权这台机器的密钥。把下面这段公钥加到服务器的 ~/.ssh/authorized_keys，然后再点一次“配置服务器”。",
    copy: "复制",
    copied: "公钥已复制到剪贴板。",
    col_model: "模型",
    col_input: "输入",
    col_output: "输出",
    col_cache_w: "缓存写",
    col_cache_r: "缓存读",
    col_cost: "花费",
    col_total: "合计",
    no_usage: "此时间段没有用量。",
    reading_usage: "正在从 %s 读取用量 …",
    usage_title: "Claude 用量 — %s",
    downloading: "下载中…（可能要等一会儿）",
    xray_ready: "xray 就绪。",
    saving: "保存中…",
    saved: "已保存（%s）。",
    n_nodes: "%s 个节点",
    working: "处理中…（可能会在启动终端里要求 sudo）",
    sshd_ready: "本地 ssh 服务器就绪。",
    platform: "平台：%s",
    xray_optional: "（此系统 xray 可选）",
    running: "运行中",
    not_running: "未运行",
    installed: "已安装",
    not_installed: "未安装",
    st_up: "已连接",
    st_stopped: "已停止",
    st_connecting: "连接中",
    st_retrying: "重连中",
  },
};

let LANG = localStorage.getItem("rc_lang");
if (LANG !== "en" && LANG !== "zh") {
  LANG = (navigator.language || "").toLowerCase().startsWith("zh") ? "zh" : "en";
}
function t(key) { return (I18N[LANG] && I18N[LANG][key]) || I18N.en[key] || key; }
function fmt(key) {
  let s = t(key);
  for (let i = 1; i < arguments.length; i++) s = s.replace("%s", arguments[i]);
  return s;
}
function stateName(s) { return t("st_" + s) !== "st_" + s ? t("st_" + s) : s; }
function applyI18n() {
  document.documentElement.lang = LANG === "zh" ? "zh-CN" : "en";
  document.querySelectorAll("[data-i18n]").forEach(el => { el.textContent = t(el.dataset.i18n); });
  document.querySelectorAll("[data-i18n-ph]").forEach(el => { el.placeholder = t(el.dataset.i18nPh); });
  document.querySelectorAll("[data-i18n-html]").forEach(el => { el.innerHTML = t(el.dataset.i18nHtml); });
}
function setLang(l) {
  LANG = l;
  localStorage.setItem("rc_lang", l);
  applyI18n();
  refresh();
}

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  let data = null;
  try { data = await res.json(); } catch (_) {}
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

// act runs a mutating call then refreshes; errors go to an alert.
async function act(fn) {
  try { await fn(); await refresh(); }
  catch (e) { alert(e.message); await refresh(); }
}

function renderHosts(state) {
  const box = $("#hosts");
  box.innerHTML = "";
  if (!state.hosts || state.hosts.length === 0) {
    const d = document.createElement("div");
    d.className = "empty";
    d.textContent = t("no_hosts");
    box.appendChild(d);
    return;
  }
  const tpl = $("#host-tpl");
  for (const h of state.hosts) {
    const st = h.status || { state: "stopped" };
    const s = st.state || "stopped";
    const node = tpl.content.cloneNode(true);
    $(".badge", node).classList.add(s);
    $(".name", node).textContent = h.alias;
    const target = [h.user && h.user + "@", h.hostname || "?", h.port && ":" + h.port].filter(Boolean).join("");
    $(".target", node).textContent = target;
    const bits = [
      h.has_reverse ? `${t("reverse")} :${h.reverse_port}` : t("no_reverse"),
      h.has_proxy ? t("xray_on") : t("direct"),
      `${t("tunnel")}: ${stateName(s)}`,
    ];
    if (st.restarts) bits.push(`${t("reconnects")}: ${st.restarts}`);
    $(".host-meta", node).textContent = bits.join("  ·  ");
    if (st.last_error && (s === "retrying" || s === "connecting")) $(".host-err", node).textContent = st.last_error;

    // inline config toggles
    const revOn = $(".rev-on", node), revPort = $(".rev-port", node);
    revOn.checked = h.has_reverse;
    revPort.value = h.reverse_port || "2222";
    revPort.disabled = !h.has_reverse;
    revOn.onchange = () => act(() => api("POST", `/api/hosts/${h.alias}/reverse`, { port: revOn.checked ? (parseInt(revPort.value, 10) || 2222) : 0 }));
    revPort.onchange = () => { if (revOn.checked) act(() => api("POST", `/api/hosts/${h.alias}/reverse`, { port: parseInt(revPort.value, 10) || 2222 })); };

    const xray = $(".xray-on", node);
    xray.checked = h.has_proxy;
    xray.onchange = () => act(() => api("POST", `/api/hosts/${h.alias}/proxy`, { on: xray.checked }));

    const auto = $(".auto-on", node);
    auto.checked = !!h.auto_start;
    auto.onchange = () => act(() => api("POST", `/api/hosts/${h.alias}/autostart`, { on: auto.checked }));

    const running = s !== "stopped";
    $(".start", node).textContent = running ? t("restart") : t("start_tunnel");
    $(".stop", node).disabled = !running;
    $(".start", node).onclick = () => act(() => api("POST", `/api/hosts/${h.alias}/start`));
    $(".stop", node).onclick = () => act(() => api("POST", `/api/hosts/${h.alias}/stop`));
    $(".setupserver", node).onclick = () => setupServer(h.alias);
    $(".usage", node).onclick = () => openUsage(h.alias);
    $(".edit", node).onclick = () => openEdit(h);
    $(".del", node).onclick = () => {
      if (confirm(fmt("delete_confirm", h.alias)))
        act(() => api("DELETE", `/api/hosts/${h.alias}`));
    };
    box.appendChild(node);
  }
}

function setupOk(res) {
  alert(fmt("setup_ok", res.alias, res.authorized ? t("authorized") : t("already_present")));
}

// Key/agent auth only — never a password. If the server hasn't authorized this
// machine's key, show the public key to copy over; any other failure shows the
// real error.
async function setupServer(alias) {
  try {
    setupOk(await api("POST", `/api/hosts/${alias}/setup-server`));
    await refresh();
  } catch (e) {
    if (/permission denied|publickey/i.test(e.message)) {
      await showAuthorizeKey(alias);
    } else {
      alert(e.message); // not an auth problem — show the real error
    }
  }
}

// showAuthorizeKey displays this machine's public key so the user can add it to
// the server's authorized_keys, then re-run "Set up server".
async function showAuthorizeKey(alias) {
  let pub = "";
  try { pub = (await api("GET", "/api/pubkey")).pubkey || ""; }
  catch (e) { alert(e.message); return; }
  $("#authkey-instr").textContent = fmt("authorize_instr", alias);
  $("#authkey-text").value = pub;
  $("#authkey-msg").textContent = "";
  authkeyDialog.showModal();
}

const authkeyDialog = $("#authkey-dialog");
$("#authkey-close").onclick = () => authkeyDialog.close();
$("#authkey-copy").onclick = async () => {
  const text = $("#authkey-text").value;
  try {
    await navigator.clipboard.writeText(text);
  } catch (_) {
    $("#authkey-text").select();
    document.execCommand("copy");
  }
  $("#authkey-msg").textContent = t("copied");
};

function renderFooter(state) {
  $("#platform").textContent = fmt("platform", state.platform) + (state.xray_supported ? "" : t("xray_optional"));
  $("#sshd-state").textContent = state.local_ssh_ok ? t("running") : t("not_running");
  $("#node-count").textContent = fmt("n_nodes", state.node_count);
  $("#xray-state").textContent = state.xray_installed ? t("installed") : t("not_installed");
}

$("#xray-download").onclick = async () => {
  const msg = $("#xray-msg");
  msg.className = "msg";
  msg.textContent = t("downloading");
  try {
    await api("POST", "/api/xray/install", { proxy: $("#xray-proxy").value.trim() });
    msg.className = "msg ok";
    msg.textContent = t("xray_ready");
    $("#xray-proxy").value = ""; // one-time, not persisted
    await refresh();
  } catch (e) {
    msg.className = "msg err";
    msg.textContent = e.message;
  }
};

$("#lang").value = LANG;
$("#lang").onchange = () => setLang($("#lang").value);

let aliasDirty = false;
$("#alias").addEventListener("input", () => { aliasDirty = true; });

// A refresh is skipped while a dialog is open or the user is editing a field, so
// live status polling never clobbers in-progress input.
function busy() {
  if (document.querySelector("dialog[open]")) return true;
  const a = document.activeElement;
  return a && (a.tagName === "INPUT" || a.tagName === "TEXTAREA" || a.tagName === "SELECT");
}

async function refresh() {
  try {
    const state = await api("GET", "/api/state");
    if (!aliasDirty && document.activeElement !== $("#alias")) $("#alias").value = state.client_alias || "";
    renderHosts(state);
    renderFooter(state);
    applyI18n(); // translate any freshly-rendered host-card labels
  } catch (e) { console.error("refresh failed", e); }
}

$("#alias-save").onclick = () => act(async () => {
  await api("POST", "/api/alias", { alias: $("#alias").value });
  aliasDirty = false;
});

// ---- add host ----
const addDialog = $("#add-dialog"), addForm = $("#add-form");
$("#add-host").onclick = () => { $("#add-err").textContent = ""; addForm.reset(); addForm.port.value = "22"; addDialog.showModal(); };
addForm.addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  try {
    await api("POST", "/api/hosts", {
      alias: addForm.alias.value.trim(),
      hostname: addForm.hostname.value.trim(),
      user: addForm.user.value.trim(),
      port: parseInt(addForm.port.value, 10) || 22,
    });
    addDialog.close();
    await refresh();
  } catch (e) { $("#add-err").textContent = e.message; }
});

// ---- edit host ----
const editDialog = $("#edit-dialog"), editForm = $("#edit-form");
async function openEdit(h) {
  $("#edit-err").textContent = "";
  $("#edit-title").textContent = h.alias;
  editForm.alias.value = h.alias;
  editForm.hostname.value = h.hostname || "";
  editForm.user.value = h.user || "";
  editForm.port.value = h.port || "";
  try {
    const params = await api("GET", `/api/hosts/${h.alias}/params`);
    $("#edit-params").textContent = (params || []).map(p => `${p.key} ${p.value}`).join("\n");
  } catch (_) { $("#edit-params").textContent = ""; }
  editDialog.showModal();
}
editForm.addEventListener("submit", async (ev) => {
  if (ev.submitter && ev.submitter.value === "cancel") return;
  ev.preventDefault();
  const alias = editForm.alias.value;
  const set = (key, value) => api("POST", `/api/hosts/${alias}/param`, { key, value });
  try {
    await set("HostName", editForm.hostname.value.trim());
    await set("User", editForm.user.value.trim());
    await set("Port", editForm.port.value.trim());
    editDialog.close();
    await refresh();
  } catch (e) { $("#edit-err").textContent = e.message; }
});

// ---- local sshd ----
$("#sshd-ensure").onclick = async () => {
  const msg = $("#sshd-msg"); msg.className = "msg"; msg.textContent = t("working");
  try {
    await api("POST", "/api/local-sshd", { disable_password: $("#sshd-nopass").checked });
    msg.className = "msg ok"; msg.textContent = t("sshd_ready");
    await refresh();
  } catch (e) { msg.className = "msg err"; msg.textContent = e.message; }
};

// ---- nodes ----
async function loadNodes() {
  try { $("#nodes").value = (await api("GET", "/api/nodes")).raw || ""; } catch (e) { console.error(e); }
}
$("#nodes-save").onclick = async () => {
  const msg = $("#nodes-msg"); msg.className = "msg"; msg.textContent = t("saving");
  try {
    const data = await api("POST", "/api/nodes", { raw: $("#nodes").value });
    msg.className = "msg ok"; msg.textContent = fmt("saved", fmt("n_nodes", data.count));
    await refresh();
  } catch (e) { msg.className = "msg err"; msg.textContent = e.message; }
};

// ---- Claude usage ----
let usageReport = null;
const usageDialog = $("#usage-dialog");
$("#usage-close").onclick = () => usageDialog.close();
document.querySelectorAll("#usage-dialog .tab").forEach(tab => tab.onclick = () => showUsageWindow(tab.dataset.w));

function fmtTok(n) {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "K";
  return "" + n;
}
function shortModel(s) {
  s = s.replace(/^claude-/, "");
  const i = s.indexOf("[");
  return i >= 0 ? s.slice(0, i) : s;
}
function usageRow(label, tk, cost, cls) {
  return `<tr${cls ? ` class="${cls}"` : ""}><td>${label}</td><td>${fmtTok(tk.input)}</td>` +
    `<td>${fmtTok(tk.output)}</td><td>${fmtTok(tk.cache_write)}</td><td>${fmtTok(tk.cache_read)}</td>` +
    `<td>$${cost.toFixed(2)}</td></tr>`;
}
function renderUsageWindow(w) {
  if (!w || !w.models || w.models.length === 0) return `<p class="hint">${t("no_usage")}</p>`;
  const body = w.models.map(m => usageRow(shortModel(m.model), m.tokens, m.cost)).join("") +
    usageRow(t("col_total"), w.total, w.cost, "total");
  return `<table class="usage-table"><thead><tr><th>${t("col_model")}</th><th>${t("col_input")}</th><th>${t("col_output")}</th>` +
    `<th>${t("col_cache_w")}</th><th>${t("col_cache_r")}</th><th>${t("col_cost")}</th></tr></thead><tbody>${body}</tbody></table>`;
}
function showUsageWindow(which) {
  document.querySelectorAll("#usage-dialog .tab").forEach(tab => tab.classList.toggle("active", tab.dataset.w === which));
  $("#usage-body").innerHTML = usageReport ? renderUsageWindow(usageReport[which]) : `<p class="hint">${t("loading")}</p>`;
}
async function openUsage(alias) {
  usageReport = null;
  $("#usage-title").textContent = fmt("usage_title", alias);
  $("#usage-body").innerHTML = `<p class="hint">${fmt("reading_usage", alias)}</p>`;
  usageDialog.showModal();
  try {
    usageReport = await api("GET", `/api/hosts/${alias}/usage`);
    showUsageWindow("day");
  } catch (e) {
    $("#usage-body").innerHTML = `<p class="msg err">${e.message}</p>`;
  }
}

// ---- boot ----
applyI18n();
loadNodes();
refresh();
setInterval(() => { if (!busy()) refresh(); }, 2500);
