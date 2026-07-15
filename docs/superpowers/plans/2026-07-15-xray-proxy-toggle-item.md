# xray Proxy Toggle Menu Item Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add menu item 7 to `local/bootstrap-macos.sh` that flips the xray `ProxyCommand` on/off for the managed `Host remote-claude` block without re-prompting for server details.

**Architecture:** A `config_block_value` helper parses the existing managed block; `run_proxy` feeds those values back into the existing `write_ssh_config_block`, which gains an optional `force` parameter to skip its "update it?" confirmation. The whole block is regenerated each time (existing ownership invariant), so the toggle can never strand or duplicate a `ProxyCommand` line.

**Tech Stack:** bash, awk; tests follow `test/xray_url_to_json.test.sh` (source the script with `RC_SOURCED_FOR_TEST=1`, call functions, assert with grep).

Spec: `docs/superpowers/specs/2026-07-15-xray-proxy-toggle-item-design.md`

---

### Task 1: `config_block_value` helper

**Files:**
- Create: `test/config_proxy_toggle.test.sh`
- Modify: `local/bootstrap-macos.sh` (insert after `write_ssh_config_block`, before the `# ---- sshd helpers` section, ~line 180)

- [ ] **Step 1: Write the failing test**

Create `test/config_proxy_toggle.test.sh`:

```bash
#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export RC_SOURCED_FOR_TEST=1
# shellcheck source=/dev/null
source "$HERE/../local/bootstrap-macos.sh"

# Sandbox every path the toggle touches; the sourced functions read these
# globals at call time.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SSH_CONFIG="$TMP/config"
XRAY_JSON="$TMP/xray.json"
XRAY_LAUNCHER="$TMP/xray-proxy.sh"
XRAY_VENDOR_BIN="$TMP/bin/xray"

fail=0
check() { # check <description> <haystack> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf 'ok   - %s\n' "$1"
  else
    printf 'FAIL - %s (missing: %s)\n' "$1" "$3"; fail=1
  fi
}
check_absent() { # check_absent <description> <haystack> <needle>
  if printf '%s' "$2" | grep -qF -- "$3"; then
    printf 'FAIL - %s (unexpected: %s)\n' "$1" "$3"; fail=1
  else
    printf 'ok   - %s\n' "$1"
  fi
}

# A managed block below some unmanaged content
printf 'Host other\n    User bob\n' > "$SSH_CONFIG"
write_ssh_config_block '203.0.113.7' 'ubuntu' '22' '2222' 0 >/dev/null

# Values are wrapped in x...x so substring matches cannot pass by accident
check 'parse: HostName'      "x$(config_block_value HostName)x"      'x203.0.113.7x'
check 'parse: User'          "x$(config_block_value User)x"          'xubuntux'
check 'parse: Port'          "x$(config_block_value Port)x"          'x22x'
check 'parse: RemoteForward' "x$(config_block_value RemoteForward)x" 'x127.0.0.1:2222x'
check 'parse: missing key is empty' "x$(config_block_value ProxyCommand)x" 'xx'

exit $fail
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash test/config_proxy_toggle.test.sh`
Expected: FAIL lines for every `parse:` check (`config_block_value: command not found` on stderr), exit 1.

- [ ] **Step 3: Implement the helper**

In `local/bootstrap-macos.sh`, directly after the closing `}` of `write_ssh_config_block` (before `# ---------------------------------------------------------------- sshd helpers`):

```bash
config_block_value() { # config_block_value <Key> -> that key's value inside the managed block
  awk -v begin="$BEGIN_MARK" -v end="$END_MARK" -v key="$1" '
    $0 == begin { inblk = 1; next }
    $0 == end   { inblk = 0 }
    inblk && $1 == key { print $2; exit }
  ' "$SSH_CONFIG" 2>/dev/null
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bash test/config_proxy_toggle.test.sh`
Expected: 5 × `ok`, exit 0.

- [ ] **Step 5: Commit**

```bash
git add test/config_proxy_toggle.test.sh local/bootstrap-macos.sh
git commit -m "Add config_block_value to read fields from the managed ssh block"
```

---

### Task 2: `force` parameter for `write_ssh_config_block`

**Files:**
- Modify: `local/bootstrap-macos.sh:128-130` (signature) and `local/bootstrap-macos.sh:151` (confirmation)
- Modify: `test/config_proxy_toggle.test.sh`

- [ ] **Step 1: Write the failing test**

In `test/config_proxy_toggle.test.sh`, insert before `exit $fail`:

```bash
# force=1 skips the "update it?" confirmation: the 'n' on stdin must NOT be
# consumed as an answer, so the rewrite happens anyway.
write_ssh_config_block '198.51.100.9' 'carol' '2200' '2345' 0 1 >/dev/null <<< 'n'
cfg="$(cat "$SSH_CONFIG")"
check 'force: block rewritten despite n on stdin' "$cfg" 'HostName 198.51.100.9'
check 'force: reverse port updated' "$cfg" 'RemoteForward 127.0.0.1:2345 127.0.0.1:22'
check 'force: unmanaged content kept' "$cfg" 'Host other'
[[ "$(grep -cF "$BEGIN_MARK" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - force: single managed block\n' \
  || { printf 'FAIL - force: managed block duplicated\n'; fail=1; }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash test/config_proxy_toggle.test.sh`
Expected: `FAIL - force: block rewritten despite n on stdin` (the `n` is consumed by `ask_yn`, which keeps the old block), exit 1.

- [ ] **Step 3: Implement force**

In `local/bootstrap-macos.sh` change the function head:

```bash
write_ssh_config_block() { # <host> <user> <port> <reverse_port> [use_proxy 0|1] [force 0|1]
  local SERVER_HOST="$1" SERVER_USER="$2" SERVER_PORT="$3" REVERSE_PORT="$4" USE_PROXY="${5:-0}" FORCE="${6:-0}"
```

and the confirmation inside `if grep -qF "$BEGIN_MARK" "$SSH_CONFIG"; then`:

```bash
    if [[ "$FORCE" != "1" ]] && ! ask_yn "~/.ssh/config already contains a $TUNNEL_ALIAS block, update it" "Y"; then
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bash test/config_proxy_toggle.test.sh`
Expected: all `ok`, exit 0.

- [ ] **Step 5: Commit**

```bash
git add local/bootstrap-macos.sh test/config_proxy_toggle.test.sh
git commit -m "Allow write_ssh_config_block to skip the update confirmation"
```

---

### Task 3: `run_proxy` and menu wiring

**Files:**
- Modify: `local/bootstrap-macos.sh` — new `run_proxy` after `run_xray` (~line 344); menu item 7 in `draw_menu`; `Select [1-7, q]` + case `7`; `run_xray` closing hint; header comment items 6–7
- Modify: `test/config_proxy_toggle.test.sh`

- [ ] **Step 1: Write the failing tests**

In `test/config_proxy_toggle.test.sh`, insert before `exit $fail`:

```bash
# --- run_proxy (menu item 7) -------------------------------------------------
# Reset to a known block without the proxy
write_ssh_config_block '203.0.113.7' 'ubuntu' '22' '2222' 0 1 >/dev/null

# No managed block -> error
if ( SSH_CONFIG="$TMP/no-such-config"; run_proxy ) >/dev/null 2>&1; then
  printf 'FAIL - run_proxy without a managed block should error\n'; fail=1
else
  printf 'ok   - run_proxy without a managed block errors\n'
fi

# xray not configured -> enabling errors and the config is untouched
if ( run_proxy ) >/dev/null 2>&1; then
  printf 'FAIL - run_proxy without xray configured should error\n'; fail=1
else
  printf 'ok   - run_proxy without xray configured errors\n'
fi
check_absent 'no xray: ProxyCommand still absent' "$(cat "$SSH_CONFIG")" 'ProxyCommand'

# Fake the xray artifacts so status_xray passes
mkdir -p "$TMP/bin"
: > "$XRAY_JSON"
: > "$XRAY_LAUNCHER"
printf '#!/bin/sh\n' > "$XRAY_VENDOR_BIN"
chmod +x "$XRAY_VENDOR_BIN"

# Toggle ON — the 'n' on stdin must not be consumed by any prompt
( run_proxy ) >/dev/null 2>&1 <<< 'n' || { printf 'FAIL - toggle on exited non-zero\n'; fail=1; }
cfg="$(cat "$SSH_CONFIG")"
check 'toggle on: ProxyCommand added'     "$cfg" "ProxyCommand $XRAY_LAUNCHER %h %p"
check 'toggle on: HostName preserved'     "$cfg" 'HostName 203.0.113.7'
check 'toggle on: User preserved'         "$cfg" 'User ubuntu'
check 'toggle on: reverse port preserved' "$cfg" 'RemoteForward 127.0.0.1:2222 127.0.0.1:22'
check 'toggle on: unmanaged content kept' "$cfg" 'Host other'

# Toggle OFF
( run_proxy ) >/dev/null 2>&1 <<< 'n' || { printf 'FAIL - toggle off exited non-zero\n'; fail=1; }
check_absent 'toggle off: ProxyCommand removed' "$(cat "$SSH_CONFIG")" 'ProxyCommand'
check 'toggle off: HostName preserved' "$(cat "$SSH_CONFIG")" 'HostName 203.0.113.7'

# Toggle ON again — nothing duplicated
( run_proxy ) >/dev/null 2>&1 || { printf 'FAIL - second toggle on exited non-zero\n'; fail=1; }
[[ "$(grep -cF "ProxyCommand $XRAY_LAUNCHER" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - re-toggle: exactly one ProxyCommand\n' \
  || { printf 'FAIL - re-toggle: ProxyCommand count != 1\n'; fail=1; }
[[ "$(grep -cF "$BEGIN_MARK" "$SSH_CONFIG")" == 1 ]] \
  && printf 'ok   - re-toggle: exactly one managed block\n' \
  || { printf 'FAIL - re-toggle: managed block count != 1\n'; fail=1; }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash test/config_proxy_toggle.test.sh`
Expected: the two error-path checks pass vacuously (`run_proxy: command not found` → non-zero), but `toggle on:` checks FAIL, exit 1.

- [ ] **Step 3: Implement `run_proxy` and wire the menu**

In `local/bootstrap-macos.sh`, after the closing `}` of `run_xray`:

```bash
run_proxy() { # item 7: toggle routing the tunnel through the xray proxy
  status_config || die "No managed Host $TUNNEL_ALIAS block yet — run item 4 first"
  local host user port rport
  host="$(config_block_value HostName)"
  user="$(config_block_value User)"
  port="$(config_block_value Port)"
  rport="$(config_block_value RemoteForward)"; rport="${rport##*:}"
  [[ -n "$host" && -n "$user" && -n "$port" && -n "$rport" ]] \
    || die "Could not read the Host $TUNNEL_ALIAS block — re-run item 4"
  if config_proxy_on; then
    write_ssh_config_block "$host" "$user" "$port" "$rport" 0 1
    log "Proxy OFF — ssh $TUNNEL_ALIAS connects directly again"
  else
    status_xray || die "xray client not configured — run item 6 first"
    write_ssh_config_block "$host" "$user" "$port" "$rport" 1 1
    log "Proxy ON — ssh $TUNNEL_ALIAS now routes through xray"
  fi
}
```

In `draw_menu`, after the item 6 printf:

```bash
  printf '  7) %-50s %s\n' 'Route tunnel through xray (ProxyCommand)' "$(mark config_proxy_on)"
```

In the menu loop, change the prompt and add the case:

```bash
  read -r -p "Select [1-7, q]: " choice || break
```

```bash
    7) run_item run_proxy ;;
```

Change `run_xray`'s closing hint from

```bash
  log "xray client ready. Turn it on for the tunnel via menu item 4 (answer Y to route through the proxy)."
```

to

```bash
  log "xray client ready. Turn it on for the tunnel via menu item 7 (proxy toggle)."
```

In the header comment, after the item-5 line (`#   5. Show the local public key...`), add:

```bash
#   6. xray client: parse a vless:// URL, install xray, write the config and
#      the on-demand SOCKS launcher used by ProxyCommand.
#   7. Toggle routing the tunnel through the xray proxy — rewrites the managed
#      block reusing its stored values, no re-prompting.
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bash test/config_proxy_toggle.test.sh && bash test/xray_url_to_json.test.sh && bash -n local/bootstrap-macos.sh`
Expected: all `ok` in both suites, no syntax errors, exit 0.

- [ ] **Step 5: Commit**

```bash
git add local/bootstrap-macos.sh test/config_proxy_toggle.test.sh
git commit -m "Add menu item 7 to toggle the xray proxy on the tunnel config"
```

---

### Task 4: Documentation

**Files:**
- Modify: `README.md:31-44`, `README.zh-CN.md:31-42`
- Modify: `TROUBLESHOOTING.md:130-131`, `TROUBLESHOOTING.zh-CN.md:128`

- [ ] **Step 1: README.md** — after the paragraph ending "…with no background service." add:

```markdown
Added xray after the tunnel config was already written? Menu item **7** toggles
the proxy on/off in place — it reuses the server details stored in the config
block, so nothing needs to be retyped.
```

- [ ] **Step 2: README.zh-CN.md** — after the matching paragraph (ending "…无后台常驻服务。") add:

```markdown
如果隧道配置（菜单项 4）已经写好、xray 是后来才加的：用菜单项 **7** 一键开/关
代理——它复用 config block 里已有的服务器信息，无需重新输入。
```

- [ ] **Step 3: TROUBLESHOOTING.md** — replace

```markdown
- To bypass xray temporarily, re-run item 4 and answer **n** to the proxy
  question.
```

with

```markdown
- To bypass xray temporarily, run bootstrap item 7 to toggle the proxy off
  (run it again to re-enable).
```

- [ ] **Step 4: TROUBLESHOOTING.zh-CN.md** — replace

```markdown
- 想临时不走 xray：重跑 item 4，代理那问选 **n**。
```

with

```markdown
- 想临时不走 xray：跑 item 7 把代理关掉（再跑一次重新打开）。
```

- [ ] **Step 5: Commit**

```bash
git add README.md README.zh-CN.md TROUBLESHOOTING.md TROUBLESHOOTING.zh-CN.md
git commit -m "Point docs at the new xray proxy toggle item"
```
