"use strict";

const $ = (sel, root = document) => root.querySelector(sel);

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
    d.textContent = "No hosts in ~/.ssh/config yet. Click “+ Add host”.";
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
      h.has_reverse ? `reverse :${h.reverse_port}` : "no reverse tunnel",
      h.has_proxy ? "xray on" : "direct",
      `tunnel: ${s}`,
    ];
    if (st.restarts) bits.push(`reconnects: ${st.restarts}`);
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
    $(".start", node).textContent = running ? "Restart" : "Start tunnel";
    $(".stop", node).disabled = !running;
    $(".start", node).onclick = () => act(() => api("POST", `/api/hosts/${h.alias}/start`));
    $(".stop", node).onclick = () => act(() => api("POST", `/api/hosts/${h.alias}/stop`));
    $(".setupserver", node).onclick = () => setupServer(h.alias);
    $(".edit", node).onclick = () => openEdit(h);
    $(".del", node).onclick = () => {
      if (confirm(`Delete host “${h.alias}” from ~/.ssh/config? This stops its tunnel.`))
        act(() => api("DELETE", `/api/hosts/${h.alias}`));
    };
    box.appendChild(node);
  }
}

function setupOk(res) {
  alert(`Server configured as “${res.alias}”. Its connect-back key was ${res.authorized ? "authorized" : "already present"} on this machine.`);
}

// Try key/agent auth first. Only ask for a password if the server actually
// rejected the key; any other failure shows the real error (no password demand).
async function setupServer(alias) {
  try {
    setupOk(await api("POST", `/api/hosts/${alias}/setup-server`, { password: "" }));
    await refresh();
    return;
  } catch (e) {
    if (!/permission denied|publickey/i.test(e.message)) {
      alert(e.message); // not an auth problem — show the real error
      return;
    }
    const pw = prompt("The server rejected your key — it isn't authorized there yet. If the\nserver accepts a password, enter it to authorize your key (Cancel to abort).\nKey-only server? Cancel and add ~/.ssh/id_ed25519.pub to its authorized_keys.");
    if (pw === null) return;
    try {
      setupOk(await api("POST", `/api/hosts/${alias}/setup-server`, { password: pw }));
      await refresh();
    } catch (e2) { alert(e2.message); }
  }
}

function renderFooter(state) {
  $("#platform").textContent = `Platform: ${state.platform}${state.xray_supported ? "" : " (xray optional on this OS)"}`;
  $("#sshd-state").textContent = state.local_ssh_ok ? "running" : "not running";
  $("#node-count").textContent = `${state.node_count} node${state.node_count === 1 ? "" : "s"}`;
}

let aliasDirty = false;
$("#alias").addEventListener("input", () => { aliasDirty = true; });

// A refresh is skipped while a dialog is open or the user is editing a field, so
// live status polling never clobbers in-progress input.
function busy() {
  if (document.querySelector("dialog[open]")) return true;
  const a = document.activeElement;
  return a && (a.tagName === "INPUT" || a.tagName === "TEXTAREA");
}

async function refresh() {
  try {
    const state = await api("GET", "/api/state");
    if (!aliasDirty && document.activeElement !== $("#alias")) $("#alias").value = state.client_alias || "";
    renderHosts(state);
    renderFooter(state);
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
  const msg = $("#sshd-msg"); msg.className = "msg"; msg.textContent = "Working… (may prompt for sudo in the launching terminal)";
  try {
    await api("POST", "/api/local-sshd", { disable_password: $("#sshd-nopass").checked });
    msg.className = "msg ok"; msg.textContent = "Local ssh server ready.";
    await refresh();
  } catch (e) { msg.className = "msg err"; msg.textContent = e.message; }
};

// ---- nodes ----
async function loadNodes() {
  try { $("#nodes").value = (await api("GET", "/api/nodes")).raw || ""; } catch (e) { console.error(e); }
}
$("#nodes-save").onclick = async () => {
  const msg = $("#nodes-msg"); msg.className = "msg"; msg.textContent = "Saving…";
  try {
    const data = await api("POST", "/api/nodes", { raw: $("#nodes").value });
    msg.className = "msg ok"; msg.textContent = `Saved (${data.count} node${data.count === 1 ? "" : "s"}).`;
    await refresh();
  } catch (e) { msg.className = "msg err"; msg.textContent = e.message; }
};

// ---- boot ----
loadNodes();
refresh();
setInterval(() => { if (!busy()) refresh(); }, 2500);
