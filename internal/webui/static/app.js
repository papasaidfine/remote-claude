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
  if (!res.ok) {
    const msg = (data && data.error) || res.statusText;
    throw new Error(msg);
  }
  return data;
}

function badgeState(status) {
  return (status && status.state) || "stopped";
}

function renderHosts(state) {
  const host = $("#hosts");
  host.innerHTML = "";
  if (!state.hosts || state.hosts.length === 0) {
    const d = document.createElement("div");
    d.className = "empty";
    d.textContent = "No hosts yet. Click “+ Add host” to add your server.";
    host.appendChild(d);
    return;
  }
  const tpl = $("#host-tpl");
  for (const h of state.hosts) {
    const node = tpl.content.cloneNode(true);
    const st = state.statuses[h.id] || { state: "stopped" };
    const s = badgeState(st);
    $(".badge", node).classList.add(s);
    $(".name", node).textContent = h.name;
    $(".target", node).textContent = `${h.user}@${h.hostname}:${h.port}`;
    const bits = [`reverse :${h.reverse_port}`, h.use_xray ? "xray on" : "direct", `tunnel: ${s}`];
    if (st.restarts) bits.push(`reconnects: ${st.restarts}`);
    $(".host-meta", node).textContent = bits.join("  ·  ");
    const errEl = $(".host-err", node);
    if (st.last_error && (s === "retrying" || s === "connecting")) errEl.textContent = st.last_error;

    const running = s !== "stopped";
    $(".start", node).textContent = running ? "Restart" : "Start tunnel";
    $(".stop", node).disabled = !running;

    $(".start", node).onclick = () => act(() => api("POST", `/api/hosts/${h.id}/start`));
    $(".stop", node).onclick = () => act(() => api("POST", `/api/hosts/${h.id}/stop`));
    $(".setupserver", node).onclick = () => act(async () => {
      const res = await api("POST", `/api/hosts/${h.id}/setup-server`);
      alert(`Server configured as “${res.alias}”. Its connect-back key was ${res.authorized ? "authorized" : "already present"} on this machine.`);
    });
    $(".edit", node).onclick = () => openHostDialog(h);
    $(".del", node).onclick = () => {
      if (confirm(`Delete host “${h.name}”? This stops its tunnel.`)) {
        act(() => api("DELETE", `/api/hosts/${h.id}`));
      }
    };
    host.appendChild(node);
  }
}

function renderFooter(state) {
  $("#platform").textContent = `Platform: ${state.platform}${state.xray_supported ? "" : " (xray optional on this OS)"}`;
  $("#local-ssh").textContent = `Local ssh server: ${state.local_ssh_ok ? "✔ running" : "✘ not detected"}`;
  $("#node-count").textContent = `${state.node_count} node${state.node_count === 1 ? "" : "s"}`;
  $("#sshd-state").textContent = state.local_ssh_ok ? "running" : "not running";
}

$("#sshd-ensure").onclick = async () => {
  const msg = $("#sshd-msg");
  msg.className = "msg";
  msg.textContent = "Working… (may prompt for sudo in the launching terminal)";
  try {
    await api("POST", "/api/local-sshd", { disable_password: $("#sshd-nopass").checked });
    msg.className = "msg ok";
    msg.textContent = "Local ssh server ready.";
    await refresh();
  } catch (e) {
    msg.className = "msg err";
    msg.textContent = e.message;
  }
};

let aliasDirty = false;
$("#alias").addEventListener("input", () => { aliasDirty = true; });

async function refresh() {
  try {
    const state = await api("GET", "/api/state");
    if (!aliasDirty && document.activeElement !== $("#alias")) {
      $("#alias").value = state.client_alias || "";
    }
    renderHosts(state);
    renderFooter(state);
  } catch (e) {
    console.error("refresh failed", e);
  }
}

// act runs a mutating call, then refreshes; surfaces errors as an alert.
async function act(fn) {
  try {
    await fn();
    await refresh();
  } catch (e) {
    alert(e.message);
    await refresh();
  }
}

// ---- alias ----
$("#alias-save").onclick = () => act(async () => {
  await api("POST", "/api/alias", { alias: $("#alias").value });
  aliasDirty = false;
});

// ---- host dialog ----
const dialog = $("#host-dialog");
const form = $("#host-form");

function openHostDialog(h) {
  $("#host-form-title").textContent = h ? "Edit host" : "Add host";
  $("#host-form-err").textContent = "";
  form.reset();
  form.id.value = h ? h.id : "";
  if (h) {
    form.name.value = h.name;
    form.hostname.value = h.hostname;
    form.user.value = h.user;
    form.port.value = h.port;
    form.reverse_port.value = h.reverse_port;
    form.use_xray.checked = !!h.use_xray;
    form.auto_start.checked = !!h.auto_start;
  }
  dialog.showModal();
}

$("#add-host").onclick = () => openHostDialog(null);

form.addEventListener("submit", async (ev) => {
  // The dialog closes automatically (method=dialog). Only act on "save".
  const submitter = ev.submitter;
  if (submitter && submitter.value === "cancel") return;
  ev.preventDefault();
  const payload = {
    name: form.name.value.trim(),
    hostname: form.hostname.value.trim(),
    user: form.user.value.trim(),
    port: parseInt(form.port.value, 10) || 22,
    reverse_port: parseInt(form.reverse_port.value, 10) || 2222,
    use_xray: form.use_xray.checked,
    auto_start: form.auto_start.checked,
  };
  const id = form.id.value;
  try {
    if (id) await api("PUT", `/api/hosts/${id}`, payload);
    else await api("POST", "/api/hosts", payload);
    dialog.close();
    await refresh();
  } catch (e) {
    $("#host-form-err").textContent = e.message;
  }
});

// ---- nodes ----
async function loadNodes() {
  try {
    const data = await api("GET", "/api/nodes");
    $("#nodes").value = data.raw || "";
  } catch (e) { console.error(e); }
}
$("#nodes-save").onclick = async () => {
  const msg = $("#nodes-msg");
  msg.className = "msg";
  msg.textContent = "Saving…";
  try {
    const data = await api("POST", "/api/nodes", { raw: $("#nodes").value });
    msg.className = "msg ok";
    msg.textContent = `Saved (${data.count} node${data.count === 1 ? "" : "s"}).`;
    await refresh();
  } catch (e) {
    msg.className = "msg err";
    msg.textContent = e.message;
  }
};

// ---- boot ----
loadNodes();
refresh();
setInterval(refresh, 2000);
